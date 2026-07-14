package alert

import (
	"net/url"
	"strings"
)

func looksLikeDomain(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
		return true
	}
	// host:port is instance, not domain for blackbox target display
	if strings.Count(v, ".") >= 1 && !strings.Contains(v, "/") {
		// exclude pure IPs like 10.0.0.5:9115
		host := v
		if i := strings.LastIndex(v, ":"); i > 0 {
			host = v[:i]
		}
		parts := strings.Split(host, ".")
		if len(parts) == 4 {
			allDigits := true
			for _, p := range parts {
				for _, c := range p {
					if c < '0' || c > '9' {
						allDigits = false
						break
					}
				}
				if !allDigits {
					break
				}
			}
			if allDigits {
				return false
			}
		}
		return strings.Contains(host, ".")
	}
	return false
}

func stripURL(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
		u, err := url.Parse(v)
		if err == nil && u.Host != "" {
			return u.Host
		}
	}
	return v
}

// IsProbeTargetValue reports whether v looks like a probed URL/domain
// (as opposed to an exporter host like 10.0.0.5:9115).
func IsProbeTargetValue(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
		return true
	}
	return looksLikeDomain(v)
}

// NormalizeProbeTarget strips scheme/path and returns host for display.
func NormalizeProbeTarget(v string) string {
	return stripURL(v)
}

// IsBlackboxAlert detects blackbox / probe related alerts.
func IsBlackboxAlert(a Alert) bool {
	name := strings.ToLower(a.AlertName())
	job := strings.ToLower(a.Job())
	if strings.Contains(name, "probe") || strings.Contains(name, "blackbox") {
		return true
	}
	if strings.Contains(job, "blackbox") {
		return true
	}
	if a.Labels["module"] != "" && (strings.Contains(job, "probe") || a.Labels["target"] != "") {
		return true
	}
	return false
}

// IsPrometheusSystemAlert detects Prometheus / Alertmanager component alerts.
func IsPrometheusSystemAlert(a Alert) bool {
	switch a.AlertName() {
	case "PrometheusDown",
		"AlertmanagerDown",
		"PrometheusRuleEvaluationFailures",
		"PrometheusTSDBCompactionsFailed",
		"PrometheusNotConnectedToAlertmanagers",
		"PrometheusTSDBWALCorruptions",
		"PrometheusErrorSendingAlerts":
		return true
	default:
		return false
	}
}

// IsPrometheusTargetAlert detects target missing / down alerts.
func IsPrometheusTargetAlert(a Alert) bool {
	switch a.AlertName() {
	case "PrometheusTargetMissing", "TargetDown":
		return true
	default:
		return false
	}
}
