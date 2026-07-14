package engine

import (
	"strings"
	"testing"
	"time"

	"smart-alert-aggregator/internal/alert"
	"smart-alert-aggregator/internal/config"
	"smart-alert-aggregator/internal/notifier"
	"smart-alert-aggregator/internal/template"
)

func TestAggregateStrictlyByAlertName(t *testing.T) {
	cfg := config.Default()
	an := NewAnalyzer(cfg)

	alerts := []alert.Alert{
		{Status: "firing", Labels: map[string]string{
			"alertname": "RedisDisconnectedSlaves", "instance": "redis-01:6379",
			"hostname": "redis-01", "severity": "critical", "job": "redis",
		}},
		{Status: "firing", Labels: map[string]string{
			"alertname": "RedisDisconnectedSlaves", "instance": "redis-02:6379",
			"hostname": "redis-02", "severity": "critical", "job": "redis",
		}},
		{Status: "firing", Labels: map[string]string{
			"alertname": "ProbeFailed", "job": "blackbox_http_get_2xx",
			"instance": "https://marketing-qa.dramagrowth.com", "severity": "critical",
		}},
		{Status: "firing", Labels: map[string]string{
			"alertname": "ProbeFailed", "job": "blackbox_http_get_2xx",
			"instance": "https://www.tuotuai.com", "severity": "critical",
		}},
	}

	incidents := an.Analyze(alerts)
	if len(incidents) != 2 {
		t.Fatalf("want 2 incidents by alertname, got %d", len(incidents))
	}
}

func TestNightingaleStyleTemplate(t *testing.T) {
	cfg := config.Default()
	an := NewAnalyzer(cfg)
	alerts := []alert.Alert{{
		Status:   "firing",
		StartsAt: time.Date(2026, 7, 14, 11, 30, 41, 0, time.Local),
		Labels: map[string]string{
			"alertname": "HostMemoryUnderMemoryPressure",
			"instance":  "172.16.91.57:9100",
			"job":       "node_exporter",
			"nodename":  "ToonShort-test",
			"severity":  "warning",
		},
		Annotations: map[string]string{
			"description": "Host memory under memory pressure (instance 172.16.91.57:9100) (hostanme ToonShort-test)",
			"value":       "1170.07",
		},
	}}

	inc := an.Analyze(alerts)[0]
	msg := template.NewRenderer(cfg.Notification.Cluster, 10).Render(inc)

	need := []string{
		"HostMemoryUnderMemoryPressure",
		"告警集群", "xinghui_Prometheus",
		"级别状态", "S2 Triggered",
		"告警名称",
		"事件标签",
		"instance=172.16.91.57:9100",
		"nodename=ToonShort-test",
		"触发时间", "2026-07-14 11:30:41",
		"发送时间",
		"触发时值", "1170.07",
		"告警描述", "Host memory under memory pressure",
	}
	for _, n := range need {
		if !strings.Contains(msg.RawText, n) && !strings.Contains(msg.Body, n) {
			t.Fatalf("missing %q in message:\n%s", n, msg.RawText)
		}
	}
}

func TestAggregatedTemplateShowsMultipleLabels(t *testing.T) {
	cfg := config.Default()
	an := NewAnalyzer(cfg)
	alerts := []alert.Alert{
		{
			Status: "firing",
			Labels: map[string]string{"alertname": "HostMemoryUnderMemoryPressure", "instance": "10.0.0.1:9100", "nodename": "web-01", "job": "node_exporter", "severity": "warning"},
			Annotations: map[string]string{"value": "100", "description": "pressure on web-01"},
		},
		{
			Status: "firing",
			Labels: map[string]string{"alertname": "HostMemoryUnderMemoryPressure", "instance": "10.0.0.2:9100", "nodename": "web-02", "job": "node_exporter", "severity": "warning"},
			Annotations: map[string]string{"value": "200", "description": "pressure on web-02"},
		},
	}
	inc := an.Analyze(alerts)[0]
	msg := template.NewRenderer("xinghui_Prometheus", 10).Render(inc)
	if !strings.Contains(msg.RawText, "影响数量") || !strings.Contains(msg.RawText, "web-01") || !strings.Contains(msg.RawText, "web-02") {
		t.Fatalf("aggregated labels missing:\n%s", msg.RawText)
	}
}

func TestDedupePublicAndPrivateIPSameHost(t *testing.T) {
	cfg := config.Default()
	an := NewAnalyzer(cfg)

	alerts := []alert.Alert{
		{
			Status: "firing",
			Labels: map[string]string{
				"alertname": "HostCpuHighIowait",
				"instance":  "42.121.216.118:9100",
				"nodename":  "ToonShort-test",
				"job":       "node_exporter",
				"severity":  "warning",
			},
		},
		{
			Status: "firing",
			Labels: map[string]string{
				"alertname": "HostCpuHighIowait",
				"instance":  "172.16.91.57:9100",
				"nodename":  "ToonShort-test",
				"job":       "node_exporter",
				"severity":  "warning",
			},
		},
	}

	inc := an.Analyze(alerts)[0]
	if inc.Count != 1 {
		t.Fatalf("want 1 machine after dedupe, got count=%d anomalies=%v alerts=%d",
			inc.Count, inc.Anomalies, len(inc.Alerts))
	}
	if len(inc.Alerts) != 1 {
		t.Fatalf("want 1 alert kept, got %d", len(inc.Alerts))
	}
	// Prefer private IP for display.
	if inc.Alerts[0].Instance() != "172.16.91.57:9100" {
		t.Fatalf("want private instance kept, got %s", inc.Alerts[0].Instance())
	}

	msg := template.NewRenderer("xinghui_Prometheus", 10).Render(inc)
	if strings.Count(msg.RawText, "instance=") != 1 {
		t.Fatalf("event labels should list one instance only:\n%s", msg.RawText)
	}
	if strings.Contains(msg.RawText, "42.121.216.118") {
		t.Fatalf("public IP should not appear after dedupe:\n%s", msg.RawText)
	}
	if !strings.Contains(msg.RawText, "172.16.91.57:9100") || !strings.Contains(msg.RawText, "ToonShort-test") {
		t.Fatalf("private IP + nodename expected:\n%s", msg.RawText)
	}
}

func TestCooldownPerAlertName(t *testing.T) {
	cfg := config.Default()
	cfg.Aggregation.WaitTime = config.Duration{Duration: 20 * time.Millisecond}
	cfg.Aggregation.MaxWait = config.Duration{Duration: 200 * time.Millisecond}
	cfg.Notification.Cooldown = config.Duration{Duration: time.Hour}

	var sent int
	stub := notifierStub(func(msg alert.Message) error { sent++; return nil })
	agg := NewAggregator(cfg, []notifier.Notifier{stub})

	agg.Ingest([]alert.Alert{
		{Status: "firing", Fingerprint: "1", Labels: map[string]string{"alertname": "ProbeFailed", "job": "blackbox_http_get_2xx", "instance": "https://a.com", "severity": "critical"}},
		{Status: "firing", Fingerprint: "2", Labels: map[string]string{"alertname": "ProbeFailed", "job": "blackbox_http_get_2xx", "instance": "https://b.com", "severity": "critical"}},
	})
	time.Sleep(60 * time.Millisecond)
	if sent != 1 {
		t.Fatalf("want 1 ProbeFailed notify, got %d", sent)
	}
}

type notifierStub func(msg alert.Message) error

func (n notifierStub) Name() string                 { return "stub" }
func (n notifierStub) Send(msg alert.Message) error { return n(msg) }
