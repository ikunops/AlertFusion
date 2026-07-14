package alert

import (
	"testing"
)

func TestParseWebhook(t *testing.T) {
	body := []byte(`{
		"status": "firing",
		"alerts": [
			{
				"status": "firing",
				"labels": {
					"alertname": "ProbeFailed",
					"job": "blackbox_exporter",
					"instance": "10.0.0.5:9115",
					"target": "https://api.xxx.com",
					"severity": "warning"
				},
				"annotations": {"summary": "probe failed"},
				"startsAt": "2026-07-10T10:00:00Z",
				"endsAt": "0001-01-01T00:00:00Z",
				"generatorURL": "http://prometheus/graph"
			}
		]
	}`)

	alerts, err := ParseWebhook(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("want 1 alert, got %d", len(alerts))
	}
	if alerts[0].AlertName() != "ProbeFailed" {
		t.Fatalf("alertname=%s", alerts[0].AlertName())
	}
	if !IsBlackboxAlert(alerts[0]) {
		t.Fatal("expected blackbox alert")
	}
}

func TestDomainHelpers(t *testing.T) {
	a := Alert{Labels: map[string]string{"target": "https://pay.xxx.com/health"}}
	if got := a.Domain(); got != "pay.xxx.com" {
		t.Fatalf("domain=%s", got)
	}
}
