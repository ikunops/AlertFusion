package notifier

import (
	"encoding/json"
	"strings"

	"smart-alert-aggregator/internal/alert"
	"smart-alert-aggregator/internal/config"
)

// Feishu sends messages to a Feishu/Lark bot webhook.
type Feishu struct {
	*httpClient
}

func NewFeishu(webhookURL string, retry config.RetryConfig) *Feishu {
	return &Feishu{httpClient: newHTTPClient("feishu", webhookURL, retry)}
}

func (f *Feishu) Name() string { return "feishu" }

func (f *Feishu) Send(message alert.Message) error {
	color := feishuHeaderColor(message.Severity)
	if message.Resolved {
		color = "green"
	}
	payload := map[string]interface{}{
		"msg_type": "interactive",
		"card": map[string]interface{}{
			"header": map[string]interface{}{
				"title": map[string]string{
					"tag":     "plain_text",
					"content": truncateTitle(message.Title, 50),
				},
				"template": color,
			},
			"elements": []map[string]interface{}{
				{
					"tag": "div",
					"text": map[string]string{
						"tag":     "lark_md",
						"content": message.Body,
					},
				},
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return f.postJSON(body)
}

func feishuHeaderColor(severity string) string {
	switch strings.ToLower(severity) {
	case "critical", "disaster":
		return "red"
	case "warning":
		return "orange"
	default:
		return "blue"
	}
}

func truncateTitle(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}
