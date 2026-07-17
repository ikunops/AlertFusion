package engine

import (
	"context"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"smart-alert-aggregator/internal/alert"
	"smart-alert-aggregator/internal/config"
	"smart-alert-aggregator/internal/mute"
	"smart-alert-aggregator/internal/notifier"
	"smart-alert-aggregator/internal/template"
)

type ruleState struct {
	sentAt  time.Time
	targets []string
	keys    map[string]time.Time
}

// HistoryEvent is a recent notify / mute / suppress record for the UI.
type HistoryEvent struct {
	Time      time.Time `json:"time"`
	Action    string    `json:"action"` // notified | muted | suppressed | recovered
	AlertName string    `json:"alertname"`
	Severity  string    `json:"severity"`
	Count     int       `json:"count"`
	Targets   []string  `json:"targets,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	Resolved  bool      `json:"resolved"`
}

const maxHistory = 5000

// Aggregator keeps a live view of firing alerts and only notifies when the
// aggregated picture for a rule (alertname / blackbox) changes.
//
// Concurrency model: every state-mutating operation is dispatched as a command
// onto a channel and executed serially inside a single worker goroutine. This
// removes all data races — there is never more than one goroutine touching
// a.active / a.buffer / a.lastByRule / a.history at a time. Read-only snapshots
// are served from a lock-protected copy produced by the worker.
type Aggregator struct {
	cfg       *config.Config
	analyzer  *Analyzer
	severity  *SeverityEngine
	notifiers []notifier.Notifier
	renderer  *template.Renderer
	mutes     *mute.Store

	cmdCh chan command
	done  chan struct{}

	// snapMu protects the read-only Snapshot served to HTTP handlers.
	snapMu sync.RWMutex
	snap   Snapshot

	// histMu protects the read-only history copy served to HTTP handlers.
	histMu   sync.RWMutex
	histCopy []HistoryEvent

	// incMu protects the read-only incident copy served to HTTP handlers.
	incMu    sync.RWMutex
	incCopy []IncidentView
}

// IncidentView is a UI-friendly aggregated incident row.
type IncidentView struct {
	Type       string   `json:"type"`
	Title      string   `json:"title"`
	Severity   string   `json:"severity"`
	Source     string   `json:"source"`
	Job        string   `json:"job"`
	Domains    []string `json:"domains,omitempty"`
	Anomalies  []string `json:"anomalies,omitempty"`
	Attached   []string `json:"attached,omitempty"`
	Count      int      `json:"count"`
	Suggestion string   `json:"suggestion,omitempty"`
	Possible   []string `json:"possible,omitempty"`
	FiredAt    time.Time `json:"fired_at"`
	Resolved   bool     `json:"resolved"`
	Status     string   `json:"status"` // firing / muted / suppressed / notified
	Targets    []string `json:"targets,omitempty"`
	AlertName  string   `json:"alertname"`
	MuteReason string   `json:"mute_reason,omitempty"`
}

// Stats are runtime counters for the dashboard.
type Stats struct {
	ReceivedTotal   int64 `json:"received_total"`
	NotifiedTotal   int64 `json:"notified_total"`
	MutedTotal      int64 `json:"muted_total"`
	SuppressedTotal int64 `json:"suppressed_total"`
	RecoveredTotal  int64 `json:"recovered_total"`
}

func NewAggregator(cfg *config.Config, notifiers []notifier.Notifier, mutes *mute.Store) *Aggregator {
	a := &Aggregator{
		cfg:       cfg,
		analyzer:  NewAnalyzer(cfg),
		severity:  NewSeverityEngine(cfg),
		notifiers: notifiers,
		renderer:  template.NewRenderer(cfg.Notification.Cluster, cfg.Notification.MaxItems),
		mutes:     mutes,
		cmdCh:     make(chan command, 256),
		done:      make(chan struct{}),
	}
	a.snap = Snapshot{
		Cluster:   cfg.Notification.Cluster,
		Cooldown:  a.cooldown().String(),
		Notifiers: a.notifierNames(),
	}
	go a.worker()
	return a
}

// command is a unit of work executed serially by the worker goroutine.
type command struct {
	kind    cmdKind
	alerts  []alert.Alert
	notif   config.NotificationConfig
	resultC chan<- applyResult
	flushC  chan<- struct{}
	delta   int64
}

type cmdKind int

const (
	cmdIngest cmdKind = iota
	cmdFlushNow
	cmdApplyNotification
	cmdMuteStats
)

type applyResult struct {
	names []string
}

// ---- Worker: the only goroutine that touches live state ----

func (a *Aggregator) worker() {
	active := make(map[string]alert.Alert)
	var buffer []alert.Alert
	var pendingResolved []alert.Alert
	var timer, maxTimer *time.Timer
	started := false
	var windowAt time.Time
	lastByRule := make(map[string]*ruleState)
	stats := Stats{}

	for cmd := range a.cmdCh {
		switch cmd.kind {
		case cmdIngest:
			a.ingestLocked(&state{
				active: &active, buffer: &buffer, pendingResolved: &pendingResolved,
				timer: &timer, maxTimer: &maxTimer, started: &started, windowAt: &windowAt,
				lastByRule: &lastByRule, stats: &stats,
			}, time.Now(), cmd.alerts)
		case cmdFlushNow:
			a.flushLocked(&state{
				active: &active, buffer: &buffer, pendingResolved: &pendingResolved,
				timer: &timer, maxTimer: &maxTimer, started: &started, windowAt: &windowAt,
				lastByRule: &lastByRule, stats: &stats,
			})
			if cmd.flushC != nil {
				close(cmd.flushC)
			}
		case cmdApplyNotification:
			a.cfg.Notification = cmd.notif
			a.renderer = template.NewRenderer(cmd.notif.Cluster, cmd.notif.MaxItems)
			a.notifiers = notifier.BuildNotifiers(a.cfg)
			cmd.resultC <- applyResult{names: a.notifierNames()}
		case cmdMuteStats:
			stats.MutedTotal += cmd.delta
			if stats.MutedTotal < 0 {
				stats.MutedTotal = 0
			}
		}
		a.publishSnapshot(&state{
			active: &active, buffer: &buffer, pendingResolved: &pendingResolved,
			started: &started, windowAt: &windowAt,
			lastByRule: &lastByRule, stats: &stats,
		})
	}
	close(a.done)
}

// state bundles all live fields so helper methods share one source of truth.
type state struct {
	active          *map[string]alert.Alert
	buffer          *[]alert.Alert
	pendingResolved *[]alert.Alert
	timer           **time.Timer
	maxTimer        **time.Timer
	started         *bool
	windowAt        *time.Time
	lastByRule      *map[string]*ruleState
	stats           *Stats
}

// ---- Public API: dispatch commands to the worker ----

func (a *Aggregator) Ingest(ctx context.Context, alerts []alert.Alert) error {
	select {
	case a.cmdCh <- command{kind: cmdIngest, alerts: alerts}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// FlushNow forces a flush and blocks until the worker has processed it.
func (a *Aggregator) FlushNow() {
	flushC := make(chan struct{})
	a.cmdCh <- command{kind: cmdFlushNow, flushC: flushC}
	<-flushC
}

// ApplyNotification updates runtime notification settings and rebuilds notifiers.
func (a *Aggregator) ApplyNotification(n config.NotificationConfig) []string {
	resultC := make(chan applyResult, 1)
	a.cmdCh <- command{kind: cmdApplyNotification, notif: n, resultC: resultC}
	res := <-resultC
	return res.names
}

// AdjustMutedTotal adjusts the muted notification counter by delta (+1 on create, -1 on delete).
func (a *Aggregator) AdjustMutedTotal(delta int64) {
	a.cmdCh <- command{kind: cmdMuteStats, delta: delta}
}

// Stop terminates the worker goroutine.
func (a *Aggregator) Stop() {
	close(a.cmdCh)
	<-a.done
}

func (a *Aggregator) notifierNames() []string {
	names := make([]string, 0, len(a.notifiers))
	for _, n := range a.notifiers {
		names = append(names, n.Name())
	}
	return names
}

// ---- Snapshot / read API ----

// Snapshot for the UI.
type Snapshot struct {
	ActiveCount  int          `json:"active_count"`
	IncidentCount int         `json:"incident_count"`
	BufferCount  int          `json:"buffer_count"`
	WindowOpen   bool         `json:"window_open"`
	WindowAt     *time.Time   `json:"window_at,omitempty"`
	MuteActive   int          `json:"mute_active"`
	Cooldown     string       `json:"cooldown"`
	Cluster      string       `json:"cluster"`
	Stats        Stats        `json:"stats"`
	Notifiers    []string     `json:"notifiers"`
	ActiveAlerts []AlertView  `json:"active_alerts"`
}

// AlertView is a UI-friendly alert row.
type AlertView struct {
	Fingerprint string            `json:"fingerprint"`
	Status      string            `json:"status"`
	AlertName   string            `json:"alertname"`
	Severity    string            `json:"severity"`
	Instance    string            `json:"instance"`
	Hostname    string            `json:"hostname"`
	Job         string            `json:"job"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    time.Time         `json:"starts_at"`
	Muted       bool              `json:"muted"`
	MuteID      string            `json:"mute_id,omitempty"`
	Description string            `json:"description"`
	Value       string            `json:"value"`
	GeneratorURL string           `json:"generator_url,omitempty"`
}

func (a *Aggregator) publishSnapshot(s *state) {
	active := *s.active
	muteActive := 0
	if a.mutes != nil {
		muteActive = a.mutes.CountActive()
	}

	views := make([]AlertView, 0, len(active))
	for _, al := range active {
		view := AlertView{
			Fingerprint: al.Fingerprint,
			Status:      al.Status,
			AlertName:   al.AlertName(),
			Severity:    al.Severity(),
			Instance:    al.Instance(),
			Hostname:    al.Hostname(),
			Job:         al.Job(),
			Labels:      al.Labels,
			Annotations: al.Annotations,
			StartsAt:    al.StartsAt,
			Description: al.Description(),
			Value:       al.Value(),
			GeneratorURL: al.GeneratorURL,
		}
		if a.mutes != nil {
			if m := a.mutes.Match(al); m != nil {
				view.Muted = true
				view.MuteID = m.ID
			}
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].AlertName != views[j].AlertName {
			return views[i].AlertName < views[j].AlertName
		}
		return views[i].Instance < views[j].Instance
	})

	var windowAt *time.Time
	if *s.started {
		t := *s.windowAt
		windowAt = &t
	}

	a.incMu.RLock()
	incCount := len(a.incCopy)
	a.incMu.RUnlock()

	snap := Snapshot{
		ActiveCount:   len(active),
		IncidentCount: incCount,
		BufferCount:   len(*s.buffer),
		WindowOpen:   *s.started,
		WindowAt:     windowAt,
		MuteActive:   muteActive,
		Cooldown:     a.cooldown().String(),
		Cluster:      a.cfg.Notification.Cluster,
		Stats:        *s.stats,
		Notifiers:    a.notifierNames(),
		ActiveAlerts: views,
	}

	a.snapMu.Lock()
	a.snap = snap
	a.snapMu.Unlock()
}

func (a *Aggregator) Snapshot() Snapshot {
	a.snapMu.RLock()
	defer a.snapMu.RUnlock()
	return a.snap
}

func (a *Aggregator) History(limit int) []HistoryEvent {
	a.histMu.RLock()
	hist := append([]HistoryEvent(nil), a.histCopy...)
	a.histMu.RUnlock()
	if limit <= 0 || limit > len(hist) {
		limit = len(hist)
	}
	out := make([]HistoryEvent, limit)
	for i := 0; i < limit; i++ {
		out[i] = hist[len(hist)-1-i]
	}
	return out
}

func (a *Aggregator) pushHistoryCopy(ev HistoryEvent) {
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	a.histMu.Lock()
	a.histCopy = append(a.histCopy, ev)
	if len(a.histCopy) > maxHistory {
		a.histCopy = a.histCopy[len(a.histCopy)-maxHistory:]
	}
	a.histMu.Unlock()
}

func (a *Aggregator) pushIncidentsCopy(views []IncidentView) {
	a.incMu.Lock()
	if views == nil {
		views = []IncidentView{}
	}
	a.incCopy = views
	a.incMu.Unlock()
}

func (a *Aggregator) Incidents() []IncidentView {
	a.incMu.RLock()
	out := append([]IncidentView(nil), a.incCopy...)
	a.incMu.RUnlock()
	return out
}

func (a *Aggregator) syncResolvedIncidents(s *state) {
	// Keep existing incidents if active map still has alerts,
	// otherwise publish empty list.
	if len(*s.active) == 0 {
		a.pushIncidentsCopy(nil)
	}
}

func makeIncidentView(inc *alert.Incident, targets []string, status, muteReason string) IncidentView {
	firedAt := inc.FiredAt
	if firedAt.IsZero() {
		firedAt = time.Now()
	}
	return IncidentView{
		Type:       string(inc.Type),
		Title:      inc.Title,
		Severity:   inc.Severity,
		Source:     inc.Source,
		Job:        inc.Job,
		Domains:    inc.Domains,
		Anomalies:  inc.Anomalies,
		Attached:   inc.Attached,
		Count:      inc.Count,
		Suggestion: inc.Suggestion,
		Possible:   inc.Possible,
		FiredAt:    firedAt,
		Resolved:   inc.Resolved,
		Status:     status,
		Targets:    targets,
		AlertName:  alert.FirstNonEmpty(inc.Source, inc.Title),
		MuteReason: muteReason,
	}
}

func muteReason(m *mute.Rule) string {
	if m == nil {
		return ""
	}
	return m.Reason
}

// ---- Live-state operations (worker only) ----

func (a *Aggregator) ingestLocked(s *state, now time.Time, alerts []alert.Alert) {
	if len(alerts) == 0 {
		return
	}
	cooldown := a.cooldown()
	a.purgeRuleStateLocked(s, now, cooldown)
	s.stats.ReceivedTotal += int64(len(alerts))

	var (
		firing         []alert.Alert
		resolvedAlerts []alert.Alert
		newKeys        int
	)

	for _, al := range alerts {
		key := alertDedupeKey(al)
		if !al.IsFiring() {
			delete(*s.active, key)
			resolvedAlerts = append(resolvedAlerts, al)
			continue
		}
		if al.StartsAt.IsZero() {
			al.StartsAt = time.Now()
		}
		firing = append(firing, al)
		if _, existed := (*s.active)[key]; !existed {
			newKeys++
		}
		(*s.active)[key] = al
	}

	needFlush := false

	if len(resolvedAlerts) > 0 {
		log.Printf("aggregator: recv resolved=%d rules=%v", len(resolvedAlerts), summarizeRules(resolvedAlerts))
		if a.cfg.Notification.SendResolved {
			*s.pendingResolved = append(*s.pendingResolved, resolvedAlerts...)
			needFlush = true
		}
	}

	if len(firing) > 0 {
		log.Printf("aggregator: recv firing=%d new_keys=%d active_total=%d rules=%v",
			len(firing), newKeys, len(*s.active), summarizeRules(firing))

		if a.shouldIgnoreLocked(s, firing, now, cooldown) {
			log.Printf("aggregator: IGNORE repeat firing webhook (cooldown=%s)", cooldown)
		} else {
			*s.buffer = append(*s.buffer, firing...)
			needFlush = true
		}
	}

	if !needFlush {
		return
	}
	a.scheduleFlushLocked(s, now)
}

func (a *Aggregator) shouldIgnoreLocked(s *state, firing []alert.Alert, now time.Time, cooldown time.Duration) bool {
	byRule := map[string][]string{}
	byRuleKeys := map[string][]string{}
	for _, al := range firing {
		rk := ruleKeyOfAlert(al)
		target := affectedTarget(al)
		byRule[rk] = appendUniqueMany(byRule[rk], target)
		byRuleKeys[rk] = append(byRuleKeys[rk], alertDedupeKey(al))
	}

	for rk, targets := range byRule {
		st := (*s.lastByRule)[rk]
		if st == nil || now.Sub(st.sentAt) >= cooldown {
			return false
		}
		if !isSubset(targets, st.targets) {
			return false
		}
		for _, k := range byRuleKeys[rk] {
			if _, ok := st.keys[k]; ok {
				continue
			}
			target := ""
			if al, ok := (*s.active)[k]; ok {
				target = affectedTarget(al)
			}
			if target == "" || !containsString(st.targets, target) {
				return false
			}
		}
	}
	return true
}

func (a *Aggregator) scheduleFlushLocked(s *state, now time.Time) {
	wait := a.cfg.Aggregation.WaitTime.Duration
	if wait <= 0 {
		wait = 30 * time.Second
	}
	maxWait := a.cfg.Aggregation.MaxWait.Duration
	if maxWait <= 0 {
		maxWait = 3 * wait
	}

	if !*s.started {
		*s.started = true
		*s.windowAt = now
		*s.maxTimer = time.AfterFunc(maxWait, a.flushFromTimer)
		log.Printf("aggregator: window opened idle=%s max_wait=%s", wait, maxWait)
	} else if *s.timer != nil {
		(*s.timer).Stop()
		log.Printf("aggregator: debounce reset idle=%s buffer=%d active=%d",
			wait, len(*s.buffer), len(*s.active))
	}
	*s.timer = time.AfterFunc(wait, a.flushFromTimer)
}

// flushFromTimer is called by the timers; it dispatches a flush command so the
// worker performs the flush under its own (single) goroutine.
func (a *Aggregator) flushFromTimer() {
	a.cmdCh <- command{kind: cmdFlushNow}
}

func (a *Aggregator) flushLocked(s *state) {
	if *s.timer != nil {
		(*s.timer).Stop()
		*s.timer = nil
	}
	if *s.maxTimer != nil {
		(*s.maxTimer).Stop()
		*s.maxTimer = nil
	}
	*s.started = false
	*s.buffer = nil

	batch := make([]alert.Alert, 0, len(*s.active))
	for _, al := range *s.active {
		batch = append(batch, al)
	}
	resolvedBatch := dedupeAlerts(*s.pendingResolved)
	*s.pendingResolved = nil
	cooldown := a.cooldown()

	// 1) Recovery notifications first (resolved webhooks).
	if len(resolvedBatch) > 0 && a.cfg.Notification.SendResolved {
		a.flushResolvedLocked(s, resolvedBatch)
	}

	if len(batch) == 0 {
		log.Printf("aggregator: flush skipped firing, no active alerts")
		a.syncResolvedIncidents(s)
		return
	}

	batch = dedupeAlerts(batch)
	log.Printf("aggregator: flushing active=%d", len(batch))

	incidents := a.analyzer.Analyze(batch)
	log.Printf("aggregator: produced %d firing incident(s)", len(incidents))

	incidentViews := make([]IncidentView, 0, len(incidents))

	for i := range incidents {
		inc := &incidents[i]
		inc.Severity = a.severity.Elevate(*inc)
		inc.Resolved = false
		rk := ruleKeyOfIncident(*inc)
		targets := targetsOfIncident(*inc)

		if muted, m := a.mutesMatch(*inc); muted {
			log.Printf("aggregator: MUTED firing rule=%s mute=%s reason=%q targets=%v", rk, m.ID, m.Reason, targets)
			s.stats.MutedTotal++
			a.pushHistoryCopy(HistoryEvent{
				Action:    "muted",
				AlertName: alert.FirstNonEmpty(inc.Source, inc.Title),
				Severity:  inc.Severity,
				Count:     inc.Count,
				Targets:   targets,
				Detail:    "屏蔽规则 " + m.ID + ": " + m.Reason,
			})
			incidentViews = append(incidentViews, makeIncidentView(inc, targets, "muted", m.Reason))
			continue
		}

		if a.shouldSuppressIncidentLocked(s, rk, targets, cooldown) {
			log.Printf("aggregator: SUPPRESS firing rule=%s targets=%v", rk, targets)
			s.stats.SuppressedTotal++
			a.pushHistoryCopy(HistoryEvent{
				Action:    "suppressed",
				AlertName: alert.FirstNonEmpty(inc.Source, inc.Title),
				Severity:  inc.Severity,
				Count:     inc.Count,
				Targets:   targets,
				Detail:    "cooldown " + cooldown.String(),
			})
			incidentViews = append(incidentViews, makeIncidentView(inc, targets, "suppressed", ""))
			continue
		}

		log.Printf("aggregator: incident[%d] type=%s title=%s source=%s severity=%s count=%d targets=%v",
			i, inc.Type, inc.Title, inc.Source, inc.Severity, inc.Count, targets)

		msg := a.renderer.Render(*inc)
		log.Printf("aggregator: message[%d]:\n----------\n%s\n----------", i, msg.RawText)
		if a.dispatch(msg) {
			a.markRuleNotifiedLocked(s, rk, *inc, targets)
			s.stats.NotifiedTotal++
			a.pushHistoryCopy(HistoryEvent{
				Action:    "notified",
				AlertName: alert.FirstNonEmpty(inc.Source, inc.Title),
				Severity:  inc.Severity,
				Count:     inc.Count,
				Targets:   targets,
			})
			incidentViews = append(incidentViews, makeIncidentView(inc, targets, "notified", ""))
		} else {
			a.pushHistoryCopy(HistoryEvent{
				Action:    "firing",
				AlertName: alert.FirstNonEmpty(inc.Source, inc.Title),
				Severity:  inc.Severity,
				Count:     inc.Count,
				Targets:   targets,
				Detail:    "所有通知通道发送失败",
			})
			incidentViews = append(incidentViews, makeIncidentView(inc, targets, "firing", ""))
		}
	}

	if len(incidentViews) > 0 || len(*s.active) > 0 {
		a.pushIncidentsCopy(incidentViews)
	}
}

// mutesMatch returns whether the whole incident is covered by a single mute rule.
// An incident is considered muted only if EVERY alert in it matches the same rule.
func (a *Aggregator) mutesMatch(inc alert.Incident) (bool, *mute.Rule) {
	if a.mutes == nil {
		return false, nil
	}
	if len(inc.Alerts) == 0 {
		if m := a.mutes.Match(alert.Alert{Labels: map[string]string{"alertname": inc.Source}}); m != nil {
			return true, m
		}
		return false, nil
	}
	var covering *mute.Rule
	for _, al := range inc.Alerts {
		m := a.mutes.Match(al)
		if m == nil {
			// One alert not covered -> the whole incident is NOT muted.
			return false, nil
		}
		if covering == nil {
			cp := *m
			covering = &cp
		}
	}
	if covering == nil {
		// Fallback: alertname-level rule via MatchIncident.
		if m := a.mutes.MatchIncident(inc.Alerts, alert.FirstNonEmpty(inc.Source, inc.Status, inc.Title)); m != nil {
			return true, m
		}
		return false, nil
	}
	return true, covering
}

func (a *Aggregator) flushResolvedLocked(s *state, resolved []alert.Alert) {
	groups := map[string][]alert.Alert{}
	for _, al := range resolved {
		rk := ruleKeyOfAlert(al)
		if _, notified := (*s.lastByRule)[rk]; !notified {
			log.Printf("aggregator: skip recovery for %s (never notified firing)", rk)
			continue
		}
		groups[rk] = append(groups[rk], al)
	}
	if len(groups) == 0 {
		return
	}

	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var resolvedViews []IncidentView

	for _, rk := range keys {
		batch := groups[rk]
		incidents := a.analyzer.Analyze(batch)
		for i := range incidents {
			inc := &incidents[i]
			inc.Resolved = true
			inc.Severity = a.severity.Elevate(*inc)
			targets := targetsOfIncident(*inc)
			log.Printf("aggregator: RECOVERY rule=%s source=%s count=%d targets=%v",
				rk, inc.Source, inc.Count, targets)

			if muted, m := a.mutesMatch(*inc); muted {
				log.Printf("aggregator: MUTED recovery rule=%s", rk)
				a.clearRecoveredTargetsLocked(s, rk, batch, targets)
				resolvedViews = append(resolvedViews, makeIncidentView(inc, targets, "muted", m.Reason))
				continue
			}

			msg := a.renderer.Render(*inc)
			log.Printf("aggregator: recovery message:\n----------\n%s\n----------", msg.RawText)
			if a.dispatch(msg) {
				a.clearRecoveredTargetsLocked(s, rk, batch, targets)
				s.stats.RecoveredTotal++
				a.pushHistoryCopy(HistoryEvent{
					Action:    "recovered",
					AlertName: alert.FirstNonEmpty(inc.Source, inc.Title),
					Severity:  inc.Severity,
					Count:     inc.Count,
					Targets:   targets,
					Resolved:  true,
				})
				resolvedViews = append(resolvedViews, makeIncidentView(inc, targets, "resolved", ""))
			}
		}
	}

	if len(resolvedViews) > 0 {
		a.pushIncidentsCopy(resolvedViews)
	}
}

func (a *Aggregator) clearRecoveredTargetsLocked(s *state, rk string, batch []alert.Alert, targets []string) {
	st := (*s.lastByRule)[rk]
	if st == nil {
		return
	}
	for _, al := range batch {
		delete(st.keys, alertDedupeKey(al))
	}
	remain := make([]string, 0, len(st.targets))
	remove := map[string]bool{}
	for _, t := range targets {
		remove[t] = true
	}
	for _, t := range st.targets {
		if !remove[t] {
			remain = append(remain, t)
		}
	}
	st.targets = remain
	if len(st.targets) == 0 && len(st.keys) == 0 {
		delete(*s.lastByRule, rk)
		log.Printf("aggregator: cleared rule state %s after full recovery", rk)
	} else {
		log.Printf("aggregator: partial recovery rule=%s remain_targets=%v", rk, st.targets)
	}
}

func (a *Aggregator) shouldSuppressIncidentLocked(s *state, rk string, targets []string, cooldown time.Duration) bool {
	st := (*s.lastByRule)[rk]
	if st == nil {
		return false
	}
	if time.Since(st.sentAt) >= cooldown {
		return false
	}
	return isSubset(targets, st.targets)
}

func (a *Aggregator) markRuleNotifiedLocked(s *state, rk string, inc alert.Incident, targets []string) {
	now := time.Now()
	st := (*s.lastByRule)[rk]
	if st == nil {
		st = &ruleState{keys: make(map[string]time.Time)}
		(*s.lastByRule)[rk] = st
	}
	st.sentAt = now
	st.targets = unionStrings(st.targets, targets)
	sort.Strings(st.targets)
	for _, al := range inc.Alerts {
		st.keys[alertDedupeKey(al)] = now
	}
	log.Printf("aggregator: notify committed rule=%s targets=%v cooldown=%s", rk, st.targets, a.cooldown())
}

func (a *Aggregator) dispatch(msg alert.Message) bool {
	ns := append([]notifier.Notifier(nil), a.notifiers...)
	if len(ns) == 0 {
		log.Printf("aggregator: no notifiers enabled, message logged only")
		return true
	}
	ok := true
	for _, n := range ns {
		log.Printf("aggregator: sending via %s ...", n.Name())
		if err := n.Send(msg); err != nil {
			log.Printf("notifier %s FAILED: %v", n.Name(), err)
			ok = false
		} else {
			log.Printf("notifier %s OK", n.Name())
		}
	}
	return ok
}

func (a *Aggregator) cooldown() time.Duration {
	d := a.cfg.Notification.Cooldown.Duration
	if d <= 0 {
		return time.Hour
	}
	return d
}

func (a *Aggregator) purgeRuleStateLocked(s *state, now time.Time, cooldown time.Duration) {
	for k, st := range *s.lastByRule {
		if now.Sub(st.sentAt) >= cooldown {
			delete(*s.lastByRule, k)
		}
	}
}

func ruleKeyOfAlert(a alert.Alert) string {
	name := a.AlertName()
	if name == "" {
		name = "_unknown"
	}
	return "rule:" + name
}

func ruleKeyOfIncident(inc alert.Incident) string {
	if inc.Source != "" {
		return "rule:" + inc.Source
	}
	if inc.Status != "" {
		return "rule:" + inc.Status
	}
	return "rule:" + inc.Title
}

func targetsOfIncident(inc alert.Incident) []string {
	if len(inc.Anomalies) > 0 {
		return append([]string(nil), inc.Anomalies...)
	}
	if len(inc.Domains) > 0 {
		return append([]string(nil), inc.Domains...)
	}
	out := make([]string, 0, 2)
	if inc.Hostname != "" {
		out = append(out, inc.Hostname)
	}
	if inc.Host != "" && inc.Host != inc.Hostname {
		out = append(out, inc.Host)
	}
	return out
}

func summarizeRules(alerts []alert.Alert) []string {
	set := map[string]struct{}{}
	out := make([]string, 0)
	for _, a := range alerts {
		k := ruleKeyOfAlert(a)
		if _, ok := set[k]; ok {
			continue
		}
		set[k] = struct{}{}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func appendUniqueMany(list []string, items ...string) []string {
	for _, item := range items {
		if item == "" {
			continue
		}
		found := false
		for _, v := range list {
			if v == item {
				found = true
				break
			}
		}
		if !found {
			list = append(list, item)
		}
	}
	return list
}

func dedupeAlerts(alerts []alert.Alert) []alert.Alert {
	seen := make(map[string]alert.Alert, len(alerts))
	order := make([]string, 0, len(alerts))
	for _, a := range alerts {
		key := alertDedupeKey(a)
		if _, ok := seen[key]; !ok {
			order = append(order, key)
		}
		seen[key] = a
	}
	out := make([]alert.Alert, 0, len(order))
	for _, k := range order {
		out = append(out, seen[k])
	}
	return out
}

func alertDedupeKey(a alert.Alert) string {
	if a.Fingerprint != "" {
		return "fp:" + a.Fingerprint
	}
	return strings.Join([]string{
		a.AlertName(),
		a.Instance(),
		a.Hostname(),
		a.Job(),
		a.Labels["target"],
		a.Labels["url"],
	}, "|")
}

func isSubset(child, parent []string) bool {
	if len(child) == 0 {
		return true
	}
	set := make(map[string]struct{}, len(parent))
	for _, p := range parent {
		set[p] = struct{}{}
	}
	for _, c := range child {
		if _, ok := set[c]; !ok {
			return false
		}
	}
	return true
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func unionStrings(a, b []string) []string {
	set := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, xs := range [][]string{a, b} {
		for _, x := range xs {
			if x == "" {
				continue
			}
			if _, ok := set[x]; ok {
				continue
			}
			set[x] = struct{}{}
			out = append(out, x)
		}
	}
	return out
}
