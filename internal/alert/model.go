package alert

import (
	"strings"
	"time"
)

// Alert is the internal representation of a single Alertmanager alert.
type Alert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

func (a Alert) AlertName() string {
	return a.Labels["alertname"]
}

func (a Alert) Severity() string {
	if s := a.Labels["severity"]; s != "" {
		return s
	}
	return "warning"
}

func (a Alert) Instance() string {
	for _, key := range []string{"instance", "host", "node"} {
		if v := a.Labels[key]; v != "" {
			return v
		}
	}
	return ""
}

// Hostname returns the hostname label used to identify a machine.
func (a Alert) Hostname() string {
	for _, key := range []string{"hostname", "nodename"} {
		if v := a.Labels[key]; v != "" {
			return v
		}
	}
	return ""
}

// MachineKey uniquely identifies a physical/logical machine for dedupe.
// Prefer hostname/nodename so public+private instance IPs of the same host merge.
func (a Alert) MachineKey() string {
	if hn := a.Hostname(); hn != "" {
		return "host:" + strings.ToLower(hn)
	}
	// Probe URL targets: identity is the site itself.
	for _, key := range []string{"target", "url", "domain"} {
		if v := a.Labels[key]; IsProbeTargetValue(v) {
			return "site:" + strings.ToLower(NormalizeProbeTarget(v))
		}
	}
	if v := a.Labels["instance"]; IsProbeTargetValue(v) {
		return "site:" + strings.ToLower(NormalizeProbeTarget(v))
	}
	if inst := a.Instance(); inst != "" {
		return "instance:" + strings.ToLower(stripInstancePort(inst))
	}
	if a.Fingerprint != "" {
		return "fp:" + a.Fingerprint
	}
	return ""
}

func stripInstancePort(v string) string {
	v = strings.TrimSpace(v)
	// keep IPv6 intact; only strip :port for host:port / ipv4:port
	if strings.Count(v, ":") == 1 {
		if i := strings.LastIndex(v, ":"); i > 0 {
			return v[:i]
		}
	}
	return v
}

// IsPrivateInstance reports whether instance looks like a private/LAN address.
func IsPrivateInstance(instance string) bool {
	host := stripInstancePort(instance)
	parts := strings.Split(host, ".")
	if len(parts) != 4 {
		return false
	}
	// 10.0.0.0/8
	if parts[0] == "10" {
		return true
	}
	// 192.168.0.0/16
	if parts[0] == "192" && parts[1] == "168" {
		return true
	}
	// 172.16.0.0/12
	if parts[0] == "172" {
		var n int
		for _, c := range parts[1] {
			if c < '0' || c > '9' {
				return false
			}
			n = n*10 + int(c-'0')
		}
		return n >= 16 && n <= 31
	}
	return false
}

func (a Alert) Annotation(keys ...string) string {
	if a.Annotations == nil {
		return ""
	}
	for _, k := range keys {
		if v := a.Annotations[k]; v != "" {
			return v
		}
	}
	return ""
}

func (a Alert) Description() string {
	return a.Annotation("description", "Description", "summary", "message", "desc")
}

func (a Alert) Value() string {
	return a.Annotation("value", "current", "cur_value", "trigger_value", "val")
}

// HostKey groups alerts by hostname first, then instance.
func (a Alert) HostKey() string {
	if hn := a.Hostname(); hn != "" {
		return "hostname:" + hn
	}
	if inst := a.Instance(); inst != "" {
		return "instance:" + inst
	}
	return ""
}

func (a Alert) Job() string {
	return a.Labels["job"]
}

func (a Alert) Domain() string {
	for _, key := range []string{"target", "instance", "domain", "url"} {
		if v := a.Labels[key]; v != "" && looksLikeDomain(v) {
			return stripURL(v)
		}
	}
	return ""
}

func (a Alert) IsFiring() bool {
	return a.Status == "" || a.Status == "firing"
}

// IncidentType classifies aggregated notification kinds.
type IncidentType string

const (
	IncidentBlackboxSingle   IncidentType = "blackbox_single"
	IncidentBlackboxMulti    IncidentType = "blackbox_multi"
	IncidentHostResource     IncidentType = "host_resource"
	IncidentHostFailure      IncidentType = "host_failure"
	IncidentPrometheusTarget IncidentType = "prometheus_target"
	IncidentPrometheusSystem IncidentType = "prometheus_system"
	IncidentJob              IncidentType = "job"
	IncidentRule             IncidentType = "rule" // same alertname across hosts
	IncidentGeneric          IncidentType = "generic"
)

// Incident is an aggregated notification unit produced by the engine.
type Incident struct {
	Type       IncidentType
	Title      string
	Severity   string
	Source     string
	Hostname   string // from labels.hostname
	Host       string // instance / IP:port
	Job        string
	Target     string
	Status     string
	Domains    []string
	Anomalies  []string
	Attached   []string
	Count      int
	Suggestion string
	Possible   []string
	Alerts     []Alert
	FiredAt    time.Time
	Resolved   bool // recovery / resolved notification
}

// Message is the rendered notification payload.
type Message struct {
	Severity string
	Title    string // card / markdown title
	Body     string // markdown body for rich channels
	RawText  string // plain text fallback / logs
	Resolved bool
}
