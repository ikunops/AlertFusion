package engine

import (
	"sort"
	"strings"
	"time"

	"smart-alert-aggregator/internal/alert"
	"smart-alert-aggregator/internal/config"
)

// Analyzer groups alerts strictly by alertname.
type Analyzer struct {
	cfg *config.Config
}

func NewAnalyzer(cfg *config.Config) *Analyzer {
	return &Analyzer{cfg: cfg}
}

func (an *Analyzer) Analyze(alerts []alert.Alert) []alert.Incident {
	if len(alerts) == 0 {
		return nil
	}

	now := time.Now()
	groups := map[string][]alert.Alert{}
	for _, a := range alerts {
		name := a.AlertName()
		if name == "" {
			name = "_unknown"
		}
		groups[name] = append(groups[name], a)
	}

	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)

	incidents := make([]alert.Incident, 0, len(names))
	for _, name := range names {
		incidents = append(incidents, an.analyzeByAlertName(name, groups[name], now))
	}
	return incidents
}

func (an *Analyzer) analyzeByAlertName(alertname string, alerts []alert.Alert, now time.Time) alert.Incident {
	// Same physical host may appear as public IP + private IP instances — keep one.
	alerts = dedupeAlertsByMachine(alerts)

	targets := make([]string, 0, len(alerts))
	for _, a := range alerts {
		if t := affectedTarget(a); t != "" {
			targets = appendUnique(targets, t)
		}
	}
	sort.Strings(targets)

	sev := maxSeverity(alerts)
	title := alert.HumanizeAlertName(alertname)
	source := alertname
	job := ""
	if len(alerts) > 0 {
		job = alerts[0].Job()
	}
	if alertname == "_unknown" {
		source = firstNonEmpty(job, "unknown")
		title = "未命名告警"
	}

	incType := alert.IncidentRule
	suggestion := ""
	if an.cfg.IsHostFailureAlert(alertname) {
		incType = alert.IncidentHostFailure
		if len(targets) > 1 {
			title = "多主机不可达"
		}
		suggestion = "检查服务器状态与网络连通性"
		sev = "critical"
	}

	if isProbeAlertName(alertname) || looksLikeProbeGroup(alerts) {
		if len(targets) > 1 {
			title = title + "（多目标）"
		}
	}

	hn, inst := "", ""
	if len(targets) == 1 {
		hn, inst = hostIdentity(alerts)
		if hn == "" && inst == "" {
			inst = targets[0]
		}
	}

	return alert.Incident{
		Type:       incType,
		Title:      title,
		Severity:   sev,
		Source:     source,
		Job:        job,
		Hostname:   hn,
		Host:       inst,
		Status:     alertname,
		Anomalies:  targets,
		Suggestion: suggestion,
		Count:      max(len(targets), 1),
		Alerts:     alerts,
		FiredAt:    now,
	}
}

// affectedTarget picks a stable display identity for one alert under a rule.
func affectedTarget(a alert.Alert) string {
	// URL / domain probes first.
	for _, key := range []string{"target", "url", "domain"} {
		if v := a.Labels[key]; alert.IsProbeTargetValue(v) {
			return alert.NormalizeProbeTarget(v)
		}
	}
	if v := a.Labels["instance"]; alert.IsProbeTargetValue(v) {
		return alert.NormalizeProbeTarget(v)
	}

	// Same machine: prefer hostname/nodename only (do NOT append public/private IP,
	// otherwise one host becomes two rows).
	if hn := a.Hostname(); hn != "" {
		return hn
	}

	for _, key := range []string{"instance", "pod", "service", "host", "addr", "redis", "name"} {
		if v := a.Labels[key]; v != "" && !alert.IsProbeTargetValue(v) {
			return v
		}
		if v := a.Labels[key]; alert.IsProbeTargetValue(v) {
			return alert.NormalizeProbeTarget(v)
		}
	}
	return ""
}

// dedupeAlertsByMachine keeps one alert per physical machine.
// When the same host is scraped via public + private instance, prefer private IP.
func dedupeAlertsByMachine(alerts []alert.Alert) []alert.Alert {
	best := map[string]alert.Alert{}
	order := make([]string, 0, len(alerts))
	for _, a := range alerts {
		key := a.MachineKey()
		if key == "" {
			key = alertDedupeKey(a)
		}
		prev, ok := best[key]
		if !ok {
			best[key] = a
			order = append(order, key)
			continue
		}
		best[key] = preferMachineAlert(prev, a)
	}
	out := make([]alert.Alert, 0, len(order))
	for _, k := range order {
		out = append(out, best[k])
	}
	return out
}

func preferMachineAlert(a, b alert.Alert) alert.Alert {
	aPriv := alert.IsPrivateInstance(a.Instance())
	bPriv := alert.IsPrivateInstance(b.Instance())
	switch {
	case bPriv && !aPriv:
		return b
	case aPriv && !bPriv:
		return a
	default:
		// Prefer richer labels / annotations.
		if labelScore(b) > labelScore(a) {
			return b
		}
		return a
	}
}

func labelScore(a alert.Alert) int {
	n := len(a.Labels) + len(a.Annotations)
	if a.Hostname() != "" {
		n += 2
	}
	if a.Description() != "" {
		n++
	}
	if a.Value() != "" {
		n++
	}
	return n
}

func isProbeAlertName(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "probe") || strings.Contains(n, "blackbox") || strings.Contains(n, "http")
}

func looksLikeProbeGroup(alerts []alert.Alert) bool {
	for _, a := range alerts {
		if alert.IsBlackboxAlert(a) || isBlackboxTargetAlert(a) {
			return true
		}
	}
	return false
}

func isBlackboxTargetAlert(a alert.Alert) bool {
	if alert.IsProbeTargetValue(a.Labels["instance"]) {
		return true
	}
	if alert.IsProbeTargetValue(a.Labels["target"]) {
		return true
	}
	if alert.IsProbeTargetValue(a.Labels["url"]) {
		return true
	}
	return false
}

func hostIdentity(alerts []alert.Alert) (hostname, instance string) {
	for _, a := range alerts {
		if hostname == "" {
			hostname = a.Hostname()
		}
		if instance == "" {
			instance = a.Instance()
		}
		if hostname != "" && instance != "" {
			break
		}
	}
	return hostname, instance
}

func maxSeverity(alerts []alert.Alert) string {
	rank := map[string]int{
		"info":     1,
		"warning":  2,
		"critical": 3,
		"disaster": 4,
	}
	best := "warning"
	bestRank := 0
	for _, a := range alerts {
		s := strings.ToLower(a.Severity())
		if r := rank[s]; r > bestRank {
			bestRank = r
			best = s
		}
	}
	return best
}

func appendUnique(list []string, item string) []string {
	for _, v := range list {
		if v == item {
			return list
		}
	}
	return append(list, item)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
