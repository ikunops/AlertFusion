package engine

import (
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

const maxHistory = 200

// Aggregator keeps a live view of firing alerts and only notifies when the
// aggregated picture for a rule (alertname / blackbox) changes.
type Aggregator struct {
	cfg       *config.Config
	analyzer  *Analyzer
	severity  *SeverityEngine
	notifiers []notifier.Notifier
	renderer  *template.Renderer
	mutes     *mute.Store

	mu sync.Mutex

	active map[string]alert.Alert

	buffer          []alert.Alert
	pendingResolved []alert.Alert
	timer           *time.Timer
	maxTimer        *time.Timer
	started         bool
	windowAt        time.Time

	// per-rule cooldown: "rule:HostOomKillDetected"
	lastByRule map[string]*ruleState

	history []HistoryEvent
	stats   Stats
}

// Stats are runtime counters for the dashboard.
type Stats struct {
	ReceivedTotal  int64 `json:"received_total"`
	NotifiedTotal  int64 `json:"notified_total"`
	MutedTotal     int64 `json:"muted_total"`
	SuppressedTotal int64 `json:"suppressed_total"`
	RecoveredTotal int64 `json:"recovered_total"`
}

func NewAggregator(cfg *config.Config, notifiers []notifier.Notifier, mutes *mute.Store) *Aggregator {
	return &Aggregator{
		cfg:             cfg,
		analyzer:        NewAnalyzer(cfg),
		severity:        NewSeverityEngine(cfg),
		notifiers:       notifiers,
		renderer:        template.NewRenderer(cfg.Notification.Cluster, cfg.Notification.MaxItems),
		mutes:           mutes,
		active:          make(map[string]alert.Alert),
		buffer:          make([]alert.Alert, 0),
		pendingResolved: make([]alert.Alert, 0),
		lastByRule:      make(map[string]*ruleState),
		history:         make([]HistoryEvent, 0, 64),
	}
}

// Snapshot for the UI.
type Snapshot struct {
	ActiveCount   int           `json:"active_count"`
	BufferCount   int           `json:"buffer_count"`
	WindowOpen    bool          `json:"window_open"`
	WindowAt      *time.Time    `json:"window_at,omitempty"`
	MuteActive    int           `json:"mute_active"`
	Cooldown      string        `json:"cooldown"`
	Cluster       string        `json:"cluster"`
	Stats         Stats         `json:"stats"`
	Notifiers     []string      `json:"notifiers"`
	ActiveAlerts  []AlertView   `json:"active_alerts"`
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
}

func (a *Aggregator) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()

	active := make([]AlertView, 0, len(a.active))
	for _, al := range a.active {
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
		}
		if a.mutes != nil {
			if m := a.mutes.Match(al); m != nil {
				view.Muted = true
				view.MuteID = m.ID
			}
		}
		active = append(active, view)
	}
	sort.Slice(active, func(i, j int) bool {
		if active[i].AlertName != active[j].AlertName {
			return active[i].AlertName < active[j].AlertName
		}
		return active[i].Instance < active[j].Instance
	})

	names := make([]string, 0, len(a.notifiers))
	for _, n := range a.notifiers {
		names = append(names, n.Name())
	}

	var windowAt *time.Time
	if a.started {
		t := a.windowAt
		windowAt = &t
	}
	muteActive := 0
	if a.mutes != nil {
		muteActive = a.mutes.CountActive()
	}

	return Snapshot{
		ActiveCount:  len(a.active),
		BufferCount:  len(a.buffer),
		WindowOpen:   a.started,
		WindowAt:     windowAt,
		MuteActive:   muteActive,
		Cooldown:     a.cooldown().String(),
		Cluster:      a.cfg.Notification.Cluster,
		Stats:        a.stats,
		Notifiers:    names,
		ActiveAlerts: active,
	}
}

func (a *Aggregator) History(limit int) []HistoryEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	if limit <= 0 || limit > len(a.history) {
		limit = len(a.history)
	}
	out := make([]HistoryEvent, limit)
	// newest first
	for i := 0; i < limit; i++ {
		out[i] = a.history[len(a.history)-1-i]
	}
	return out
}

func (a *Aggregator) pushHistoryLocked(ev HistoryEvent) {
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	a.history = append(a.history, ev)
	if len(a.history) > maxHistory {
		a.history = a.history[len(a.history)-maxHistory:]
	}
}

func (a *Aggregator) Ingest(alerts []alert.Alert) {
	a.mu.Lock()
	defer a.mu.Unlock()

	cooldown := a.cooldown()
	now := time.Now()
	a.purgeRuleStateLocked(now, cooldown)
	a.stats.ReceivedTotal += int64(len(alerts))

	var (
		firing         []alert.Alert
		resolvedAlerts []alert.Alert
		newKeys        int
	)

	for _, al := range alerts {
		key := alertDedupeKey(al)
		if !al.IsFiring() {
			delete(a.active, key)
			resolvedAlerts = append(resolvedAlerts, al)
			continue
		}
		firing = append(firing, al)
		if _, existed := a.active[key]; !existed {
			newKeys++
		}
		a.active[key] = al
	}

	needFlush := false

	if len(resolvedAlerts) > 0 {
		log.Printf("aggregator: recv resolved=%d rules=%v", len(resolvedAlerts), summarizeRules(resolvedAlerts))
		if a.cfg.Notification.SendResolved {
			a.pendingResolved = append(a.pendingResolved, resolvedAlerts...)
			needFlush = true
		}
	}

	if len(firing) > 0 {
		log.Printf("aggregator: recv firing=%d new_keys=%d active_total=%d rules=%v",
			len(firing), newKeys, len(a.active), summarizeRules(firing))

		if a.shouldIgnoreLocked(firing, now, cooldown) {
			log.Printf("aggregator: IGNORE repeat firing webhook (cooldown=%s)", cooldown)
		} else {
			a.buffer = append(a.buffer, firing...)
			needFlush = true
		}
	}

	if !needFlush {
		if len(firing) == 0 && len(resolvedAlerts) == 0 {
			log.Printf("aggregator: nothing to process")
		}
		return
	}
	a.scheduleFlushLocked(now)
}

func (a *Aggregator) shouldIgnoreLocked(firing []alert.Alert, now time.Time, cooldown time.Duration) bool {
	// Group incoming by rule key; ignore only if EVERY rule has no new targets.
	byRule := map[string][]string{}
	byRuleKeys := map[string][]string{}
	for _, al := range firing {
		rk := ruleKeyOfAlert(al)
		byRule[rk] = appendUniqueMany(byRule[rk], siteOf(al))
		byRuleKeys[rk] = append(byRuleKeys[rk], alertDedupeKey(al))
	}

	for rk, targets := range byRule {
		st := a.lastByRule[rk]
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
			// new fingerprint but same target → still ignore
			site := ""
			if al, ok := a.active[k]; ok {
				site = siteOf(al)
			}
			if site == "" || !containsString(st.targets, site) {
				return false
			}
		}
	}
	return true
}

func (a *Aggregator) scheduleFlushLocked(now time.Time) {
	wait := a.cfg.Aggregation.WaitTime.Duration
	if wait <= 0 {
		wait = 30 * time.Second
	}
	maxWait := a.cfg.Aggregation.MaxWait.Duration
	if maxWait <= 0 {
		maxWait = 3 * wait
	}

	if !a.started {
		a.started = true
		a.windowAt = now
		a.maxTimer = time.AfterFunc(maxWait, a.flush)
		log.Printf("aggregator: window opened idle=%s max_wait=%s", wait, maxWait)
	} else if a.timer != nil {
		a.timer.Stop()
		log.Printf("aggregator: debounce reset idle=%s buffer=%d active=%d",
			wait, len(a.buffer), len(a.active))
	}
	a.timer = time.AfterFunc(wait, a.flush)
}

func (a *Aggregator) flush() {
	a.mu.Lock()
	if a.timer != nil {
		a.timer.Stop()
		a.timer = nil
	}
	if a.maxTimer != nil {
		a.maxTimer.Stop()
		a.maxTimer = nil
	}
	a.started = false
	a.buffer = nil

	batch := make([]alert.Alert, 0, len(a.active))
	for _, al := range a.active {
		batch = append(batch, al)
	}
	resolvedBatch := dedupeAlerts(a.pendingResolved)
	a.pendingResolved = nil
	cooldown := a.cooldown()
	a.mu.Unlock()

	// 1) Recovery notifications first (resolved webhooks).
	if len(resolvedBatch) > 0 && a.cfg.Notification.SendResolved {
		a.flushResolved(resolvedBatch)
	}

	if len(batch) == 0 {
		log.Printf("aggregator: flush skipped firing, no active alerts")
		return
	}

	batch = dedupeAlerts(batch)
	log.Printf("aggregator: flushing active=%d", len(batch))

	incidents := a.analyzer.Analyze(batch)
	log.Printf("aggregator: produced %d firing incident(s)", len(incidents))

	for i := range incidents {
		inc := &incidents[i]
		inc.Severity = a.severity.Elevate(*inc)
		inc.Resolved = false
		rk := ruleKeyOfIncident(*inc)
		targets := targetsOfIncident(*inc)

		if a.mutes != nil {
			if m := a.mutes.MatchIncident(inc.Alerts, firstNonEmpty(inc.Source, inc.Status, inc.Title)); m != nil {
				log.Printf("aggregator: MUTED firing rule=%s mute=%s reason=%q targets=%v", rk, m.ID, m.Reason, targets)
				a.mu.Lock()
				a.stats.MutedTotal++
				a.pushHistoryLocked(HistoryEvent{
					Action:    "muted",
					AlertName: firstNonEmpty(inc.Source, inc.Title),
					Severity:  inc.Severity,
					Count:     inc.Count,
					Targets:   targets,
					Detail:    "屏蔽规则 " + m.ID + ": " + m.Reason,
				})
				a.mu.Unlock()
				continue
			}
		}

		if a.shouldSuppressIncident(rk, targets, cooldown) {
			log.Printf("aggregator: SUPPRESS firing rule=%s targets=%v", rk, targets)
			a.mu.Lock()
			a.stats.SuppressedTotal++
			a.pushHistoryLocked(HistoryEvent{
				Action:    "suppressed",
				AlertName: firstNonEmpty(inc.Source, inc.Title),
				Severity:  inc.Severity,
				Count:     inc.Count,
				Targets:   targets,
				Detail:    "cooldown " + cooldown.String(),
			})
			a.mu.Unlock()
			continue
		}

		log.Printf("aggregator: incident[%d] type=%s title=%s source=%s severity=%s count=%d targets=%v",
			i, inc.Type, inc.Title, inc.Source, inc.Severity, inc.Count, targets)

		msg := a.renderer.Render(*inc)
		log.Printf("aggregator: message[%d]:\n----------\n%s\n----------", i, msg.RawText)
		if a.dispatch(msg) {
			a.markRuleNotified(rk, *inc, targets)
			a.mu.Lock()
			a.stats.NotifiedTotal++
			a.pushHistoryLocked(HistoryEvent{
				Action:    "notified",
				AlertName: firstNonEmpty(inc.Source, inc.Title),
				Severity:  inc.Severity,
				Count:     inc.Count,
				Targets:   targets,
			})
			a.mu.Unlock()
		}
	}
}

func (a *Aggregator) flushResolved(resolved []alert.Alert) {
	// Only notify recovery for rules we previously alerted on.
	groups := map[string][]alert.Alert{}
	for _, al := range resolved {
		rk := ruleKeyOfAlert(al)
		a.mu.Lock()
		_, notified := a.lastByRule[rk]
		a.mu.Unlock()
		if !notified {
			log.Printf("aggregator: skip recovery for %s (never notified firing)", rk)
			continue
		}
		groups[rk] = append(groups[rk], al)
	}
	if len(groups) == 0 {
		return
	}

	now := time.Now()
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

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

			if a.mutes != nil {
				if m := a.mutes.MatchIncident(inc.Alerts, firstNonEmpty(inc.Source, inc.Status, inc.Title)); m != nil {
					log.Printf("aggregator: MUTED recovery rule=%s mute=%s", rk, m.ID)
					a.clearRecoveredTargets(rk, batch, targets)
					continue
				}
			}

			msg := a.renderer.Render(*inc)
			log.Printf("aggregator: recovery message:\n----------\n%s\n----------", msg.RawText)
			if a.dispatch(msg) {
				a.clearRecoveredTargets(rk, batch, targets)
				a.mu.Lock()
				a.stats.RecoveredTotal++
				a.pushHistoryLocked(HistoryEvent{
					Action:    "recovered",
					AlertName: firstNonEmpty(inc.Source, inc.Title),
					Severity:  inc.Severity,
					Count:     inc.Count,
					Targets:   targets,
					Resolved:  true,
				})
				a.mu.Unlock()
			}
		}
	}
	_ = now
}

func (a *Aggregator) clearRecoveredTargets(rk string, batch []alert.Alert, targets []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.lastByRule[rk]
	if st == nil {
		return
	}
	// Remove recovered alert keys.
	for _, al := range batch {
		delete(st.keys, alertDedupeKey(al))
	}
	// Remove recovered targets from cooldown set.
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
		delete(a.lastByRule, rk)
		log.Printf("aggregator: cleared rule state %s after full recovery", rk)
	} else {
		log.Printf("aggregator: partial recovery rule=%s remain_targets=%v", rk, st.targets)
	}
}

func (a *Aggregator) shouldSuppressIncident(rk string, targets []string, cooldown time.Duration) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.lastByRule[rk]
	if st == nil {
		return false
	}
	if time.Since(st.sentAt) >= cooldown {
		return false
	}
	return isSubset(targets, st.targets)
}

func (a *Aggregator) markRuleNotified(rk string, inc alert.Incident, targets []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	st := a.lastByRule[rk]
	if st == nil {
		st = &ruleState{keys: make(map[string]time.Time)}
		a.lastByRule[rk] = st
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
	if len(a.notifiers) == 0 {
		log.Printf("aggregator: no notifiers enabled, message logged only")
		return true
	}
	ok := true
	for _, n := range a.notifiers {
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

func (a *Aggregator) FlushNow() {
	a.mu.Lock()
	if a.timer != nil {
		a.timer.Stop()
		a.timer = nil
	}
	if a.maxTimer != nil {
		a.maxTimer.Stop()
		a.maxTimer = nil
	}
	a.started = false
	a.mu.Unlock()
	a.flush()
}

func (a *Aggregator) cooldown() time.Duration {
	d := a.cfg.Notification.Cooldown.Duration
	if d <= 0 {
		return time.Hour
	}
	return d
}

func (a *Aggregator) purgeRuleStateLocked(now time.Time, cooldown time.Duration) {
	for k, st := range a.lastByRule {
		if now.Sub(st.sentAt) >= cooldown {
			delete(a.lastByRule, k)
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

func siteOf(a alert.Alert) string {
	// Cooldown / suppress identity must match machine dedupe.
	if key := a.MachineKey(); key != "" {
		return key
	}
	return affectedTarget(a)
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
