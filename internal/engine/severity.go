package engine

import (
	"strconv"
	"strings"

	"smart-alert-aggregator/internal/alert"
	"smart-alert-aggregator/internal/config"
)

// SeverityEngine elevates severity based on aggregation results.
type SeverityEngine struct {
	cfg *config.Config
}

func NewSeverityEngine(cfg *config.Config) *SeverityEngine {
	return &SeverityEngine{cfg: cfg}
}

func (s *SeverityEngine) Elevate(inc alert.Incident) string {
	current := strings.ToLower(inc.Severity)
	if current == "" {
		current = "warning"
	}

	elevated := current
	for _, rule := range s.cfg.Severity.Rules {
		if matchDomainCount(rule.Condition.DomainCount, inc) {
			elevated = higherSeverity(elevated, strings.ToLower(rule.Level))
		}
	}

	// Built-in: multi-site blackbox failures elevate to critical at threshold.
	if inc.Type == alert.IncidentBlackboxMulti {
		threshold := s.cfg.Blackbox.DomainThreshold.Critical
		if threshold <= 0 {
			threshold = 2
		}
		n := len(inc.Domains)
		if n == 0 {
			n = len(inc.Anomalies)
		}
		if n >= threshold {
			elevated = higherSeverity(elevated, "critical")
		}
	}

	// Host failure is always critical.
	if inc.Type == alert.IncidentHostFailure {
		elevated = "critical"
	}

	return elevated
}

func matchDomainCount(expr string, inc alert.Incident) bool {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return false
	}
	count := len(inc.Domains)
	if count == 0 {
		count = inc.Count
	}

	ops := []string{">=", "<=", "!=", ">", "<", "==", "="}
	for _, op := range ops {
		if strings.HasPrefix(expr, op) {
			n, err := strconv.Atoi(strings.TrimSpace(expr[len(op):]))
			if err != nil {
				return false
			}
			switch op {
			case ">":
				return count > n
			case ">=":
				return count >= n
			case "<":
				return count < n
			case "<=":
				return count <= n
			case "==", "=":
				return count == n
			case "!=":
				return count != n
			}
		}
	}
	return false
}

func higherSeverity(a, b string) string {
	rank := map[string]int{
		"info":     1,
		"warning":  2,
		"critical": 3,
		"disaster": 4,
	}
	if rank[b] > rank[a] {
		return b
	}
	return a
}
