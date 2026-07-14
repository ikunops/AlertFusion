package template

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"smart-alert-aggregator/internal/alert"
)

// Renderer turns an Incident into a notification Message.
type Renderer struct {
	cluster  string
	maxItems int
}

func NewRenderer(cluster string, maxItems int) *Renderer {
	if maxItems <= 0 {
		maxItems = 10
	}
	if cluster == "" {
		cluster = "xinghui_Prometheus"
	}
	return &Renderer{cluster: cluster, maxItems: maxItems}
}

func (r *Renderer) Render(inc alert.Incident) alert.Message {
	sev := strings.ToLower(inc.Severity)
	if sev == "" {
		sev = "warning"
	}
	alertname := firstNonEmpty(inc.Source, inc.Status, inc.Title)
	statusText := "Triggered"
	if inc.Resolved {
		statusText = "Resolved"
	}
	levelStatus := formatLevelStatus(sev, statusText)
	emoji := severityEmoji(sev, inc.Type)
	if inc.Resolved {
		emoji = "✅"
	}

	sendAt := time.Now()
	triggerAt := earliestStartsAt(inc.Alerts)
	if triggerAt.IsZero() {
		triggerAt = inc.FiredAt
	}
	if triggerAt.IsZero() {
		triggerAt = sendAt
	}

	var md strings.Builder
	var plain strings.Builder
	write := func(format string, args ...interface{}) {
		line := fmt.Sprintf(format, args...)
		md.WriteString(line)
		md.WriteString("\n")
		plain.WriteString(stripMD(line))
		plain.WriteString("\n")
	}

	// Header title = alertname (as in Nightingale-style alerts)
	write("%s **%s**", emoji, alertname)
	write("")
	write("**告警集群:** %s", r.cluster)
	write("**级别状态:** %s", levelStatus)
	write("**告警名称:** `%s`", alertname)

	if inc.Count > 1 {
		write("**影响数量:** %d", inc.Count)
	}

	write("**事件标签:**")
	labelLines := buildEventLabels(inc.Alerts, r.maxItems)
	if len(labelLines) == 0 {
		write("  -")
	} else {
		for _, line := range labelLines {
			write("  `%s`", line)
		}
		if len(inc.Alerts) > r.maxItems {
			write("  `... 共%d个`", len(inc.Alerts))
		}
	}

	if inc.Resolved {
		write("**恢复时间:** %s", sendAt.Format("2006-01-02 15:04:05"))
		write("**发送时间:** %s", sendAt.Format("2006-01-02 15:04:05"))
	} else {
		write("**触发时间:** %s", triggerAt.Format("2006-01-02 15:04:05"))
		write("**发送时间:** %s", sendAt.Format("2006-01-02 15:04:05"))
	}

	if !inc.Resolved {
		values := collectValues(inc.Alerts, r.maxItems)
		if len(values) == 1 {
			write("**触发时值:** %s", values[0])
		} else if len(values) > 1 {
			write("**触发时值:**")
			for _, v := range values {
				write("  - %s", v)
			}
		}
	}

	descs := collectDescriptions(inc.Alerts, r.maxItems)
	if len(descs) == 1 {
		write("**告警描述:** %s", descs[0])
	} else if len(descs) > 1 {
		write("**告警描述:**")
		for _, d := range descs {
			write("  - %s", d)
		}
	} else if title := strings.TrimSpace(inc.Title); title != "" && title != alertname {
		write("**告警描述:** %s", title)
	}

	if inc.Suggestion != "" && !inc.Resolved {
		write("**处理建议:** %s", inc.Suggestion)
	}
	if inc.Resolved {
		write("**状态说明:** 告警已恢复")
	}

	body := strings.TrimSpace(md.String())
	raw := strings.TrimSpace(plain.String())
	return alert.Message{
		Severity: sev,
		Title:    fmt.Sprintf("%s %s", emoji, alertname),
		Body:     body,
		RawText:  raw,
		Resolved: inc.Resolved,
	}
}

func formatLevelStatus(severity, status string) string {
	level := "S3"
	switch strings.ToLower(severity) {
	case "disaster":
		level = "S0"
	case "critical":
		level = "S1"
	case "warning":
		level = "S2"
	case "info":
		level = "S3"
	}
	if status == "" {
		status = "Triggered"
	}
	return level + " " + status
}

func firingStatus(inc alert.Incident) string {
	if inc.Resolved {
		return "Resolved"
	}
	for _, a := range inc.Alerts {
		if a.IsFiring() {
			return "Triggered"
		}
	}
	if len(inc.Alerts) > 0 {
		return "Resolved"
	}
	return "Triggered"
}

func earliestStartsAt(alerts []alert.Alert) time.Time {
	var t time.Time
	for _, a := range alerts {
		if a.StartsAt.IsZero() {
			continue
		}
		if t.IsZero() || a.StartsAt.Before(t) {
			t = a.StartsAt
		}
	}
	return t
}

func buildEventLabels(alerts []alert.Alert, maxItems int) []string {
	if maxItems <= 0 {
		maxItems = 10
	}
	// Dedupe public/private IP duplicates of the same machine before rendering.
	alerts = dedupeAlertsForDisplay(alerts)
	out := make([]string, 0, len(alerts))
	for i, a := range alerts {
		if i >= maxItems {
			break
		}
		out = append(out, formatLabelBracket(a))
	}
	return out
}

// dedupeAlertsForDisplay mirrors engine machine dedupe for template rendering.
func dedupeAlertsForDisplay(alerts []alert.Alert) []alert.Alert {
	best := map[string]alert.Alert{}
	order := make([]string, 0, len(alerts))
	for _, a := range alerts {
		key := a.MachineKey()
		if key == "" {
			key = a.Fingerprint
			if key == "" {
				key = a.Instance()
			}
		}
		prev, ok := best[key]
		if !ok {
			best[key] = a
			order = append(order, key)
			continue
		}
		// Prefer private/LAN instance for display.
		if alert.IsPrivateInstance(a.Instance()) && !alert.IsPrivateInstance(prev.Instance()) {
			best[key] = a
		} else if len(a.Labels) > len(prev.Labels) {
			best[key] = a
		}
	}
	out := make([]alert.Alert, 0, len(order))
	for _, k := range order {
		out = append(out, best[k])
	}
	return out
}

func formatLabelBracket(a alert.Alert) string {
	prefer := []string{
		"nodename", "hostname", "instance", "job",
		"target", "url", "domain", "pod", "service", "namespace",
	}
	seen := map[string]bool{}
	parts := make([]string, 0, 8)

	if name := a.AlertName(); name != "" {
		parts = append(parts, "rulename="+name)
		seen["alertname"] = true
		seen["rulename"] = true
	}

	for _, k := range prefer {
		if seen[k] {
			continue
		}
		v := a.Labels[k]
		if v == "" {
			continue
		}
		if k == "instance" || k == "target" || k == "url" {
			if alert.IsProbeTargetValue(v) {
				v = alert.NormalizeProbeTarget(v)
			}
		}
		parts = append(parts, k+"="+v)
		seen[k] = true
	}

	keys := make([]string, 0, len(a.Labels))
	for k := range a.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	skip := map[string]bool{
		"alertname": true, "severity": true, "prometheus": true,
		"endpoint": true, "metrics_path": true, "scheme": true,
		"node": true, // avoid duplicating nodename/hostname noise
	}
	for _, k := range keys {
		if seen[k] || skip[k] {
			continue
		}
		parts = append(parts, k+"="+a.Labels[k])
		if len(parts) >= 8 {
			break
		}
	}

	return "[" + strings.Join(parts, " ") + "]"
}

func collectValues(alerts []alert.Alert, maxItems int) []string {
	out := make([]string, 0)
	seen := map[string]bool{}
	for i, a := range alerts {
		if i >= maxItems {
			break
		}
		v := a.Value()
		if v == "" {
			continue
		}
		label := affectedShort(a)
		item := v
		if label != "" && len(alerts) > 1 {
			item = label + "=" + v
		}
		if seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func collectDescriptions(alerts []alert.Alert, maxItems int) []string {
	out := make([]string, 0)
	seen := map[string]bool{}
	for i, a := range alerts {
		if i >= maxItems {
			break
		}
		d := strings.TrimSpace(a.Description())
		if d == "" {
			continue
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

func affectedShort(a alert.Alert) string {
	if hn := a.Hostname(); hn != "" {
		return hn
	}
	if inst := a.Instance(); inst != "" {
		if alert.IsProbeTargetValue(inst) {
			return alert.NormalizeProbeTarget(inst)
		}
		return inst
	}
	return ""
}

func severityEmoji(sev string, t alert.IncidentType) string {
	if t == alert.IncidentHostFailure {
		return "🔥🔥"
	}
	switch sev {
	case "critical", "disaster":
		return "🔥"
	case "warning":
		return "⚠️"
	default:
		return "ℹ️"
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func stripMD(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "`", "")
	return s
}
