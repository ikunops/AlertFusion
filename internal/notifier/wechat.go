package notifier

import (
	"encoding/json"

	"smart-alert-aggregator/internal/alert"
	"smart-alert-aggregator/internal/config"
)

// Wechat sends messages to a WeCom (企业微信) robot webhook.
type Wechat struct {
	*httpClient
}

func NewWechat(webhookURL string, retry config.RetryConfig) *Wechat {
	return &Wechat{httpClient: newHTTPClient("wechat", webhookURL, retry)}
}

func (w *Wechat) Name() string { return "wechat" }

func (w *Wechat) Send(message alert.Message) error {
	content := message.Title + "\n\n" + message.Body
	payload := map[string]interface{}{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"content": content,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return w.postJSON(body)
}
