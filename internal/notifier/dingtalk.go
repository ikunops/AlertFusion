package notifier

import (
	"encoding/json"

	"smart-alert-aggregator/internal/alert"
	"smart-alert-aggregator/internal/config"
)

// Dingtalk sends messages to a DingTalk robot webhook.
type Dingtalk struct {
	*httpClient
}

func NewDingtalk(webhookURL string, retry config.RetryConfig) *Dingtalk {
	return &Dingtalk{httpClient: newHTTPClient("dingtalk", webhookURL, retry)}
}

func (d *Dingtalk) Name() string { return "dingtalk" }

func (d *Dingtalk) Send(message alert.Message) error {
	text := message.Title + "\n\n" + message.Body
	payload := map[string]interface{}{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"title": message.Title,
			"text":  text,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return d.postJSON(body)
}
