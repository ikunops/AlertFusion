package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"smart-alert-aggregator/internal/alert"
	"smart-alert-aggregator/internal/config"
)

// Notifier sends aggregated messages to an external channel.
type Notifier interface {
	Name() string
	Send(message alert.Message) error
}

// BuildNotifiers creates enabled notifiers from config.
func BuildNotifiers(cfg *config.Config) []Notifier {
	retry := cfg.Notification.Retry
	var list []Notifier

	ch := cfg.Notification.Channels
	logChannel("feishu", ch.Feishu)
	logChannel("dingtalk", ch.Dingtalk)
	logChannel("wechat", ch.Wechat)

	if ch.Feishu.Enabled && ch.Feishu.WebhookURL != "" && !looksLikePlaceholder(ch.Feishu.WebhookURL) {
		list = append(list, NewFeishu(ch.Feishu.WebhookURL, retry))
	}
	if ch.Dingtalk.Enabled && ch.Dingtalk.WebhookURL != "" && !looksLikePlaceholder(ch.Dingtalk.WebhookURL) {
		list = append(list, NewDingtalk(ch.Dingtalk.WebhookURL, retry))
	}
	if ch.Wechat.Enabled && ch.Wechat.WebhookURL != "" && !looksLikePlaceholder(ch.Wechat.WebhookURL) {
		list = append(list, NewWechat(ch.Wechat.WebhookURL, retry))
	}
	return list
}

func logChannel(name string, ch config.ChannelConfig) {
	if !ch.Enabled {
		log.Printf("notifier: channel %s disabled", name)
		return
	}
	if ch.WebhookURL == "" || looksLikePlaceholder(ch.WebhookURL) {
		log.Printf("notifier: channel %s enabled but webhook_url looks empty/placeholder: %s", name, ch.WebhookURL)
		return
	}
	log.Printf("notifier: channel %s enabled url=%s", name, maskURL(ch.WebhookURL))
}

func looksLikePlaceholder(url string) bool {
	return bytes.Contains([]byte(url), []byte("xxxx"))
}

func maskURL(url string) string {
	if len(url) <= 48 {
		return url
	}
	return url[:36] + "..." + url[len(url)-8:]
}

type httpClient struct {
	client  *http.Client
	retry   config.RetryConfig
	name    string
	webhook string
}

func newHTTPClient(name, webhook string, retry config.RetryConfig) *httpClient {
	return &httpClient{
		client:  &http.Client{Timeout: 10 * time.Second},
		retry:   retry,
		name:    name,
		webhook: webhook,
	}
}

func (c *httpClient) postJSON(payload []byte) error {
	attempts := c.retry.Count
	if attempts <= 0 {
		attempts = 1
	}
	interval := c.retry.Interval.Duration
	if interval <= 0 {
		interval = 5 * time.Second
	}

	log.Printf("[%s] request payload: %s", c.name, truncate(string(payload), 1024))

	var lastErr error
	for i := 1; i <= attempts; i++ {
		status, body, err := c.doPost(payload)
		if err != nil {
			lastErr = err
			log.Printf("[%s] send error attempt=%d/%d: %v", c.name, i, attempts, err)
		} else if status < 200 || status >= 300 {
			lastErr = fmt.Errorf("http status=%d body=%s", status, truncate(body, 512))
			log.Printf("[%s] send failed attempt=%d/%d status=%d body=%s",
				c.name, i, attempts, status, truncate(body, 256))
		} else if bizErr := checkBotBizError(c.name, body); bizErr != nil {
			// HTTP 200 but bot API business failure (common for Feishu/DingTalk/WeChat)
			lastErr = bizErr
			log.Printf("[%s] business error attempt=%d/%d: %v body=%s",
				c.name, i, attempts, bizErr, truncate(body, 256))
		} else {
			log.Printf("[%s] send ok status=%d attempt=%d body=%s", c.name, status, i, truncate(body, 256))
			return nil
		}
		if i < attempts {
			time.Sleep(interval)
		}
	}
	return fmt.Errorf("%s notify failed after %d attempts: %w", c.name, attempts, lastErr)
}

func checkBotBizError(name, body string) error {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		// non-JSON success body is rare; treat as ok
		return nil
	}
	// Feishu: {"code":0,"msg":"success"}
	if code, ok := asInt(m["code"]); ok && code != 0 {
		return fmt.Errorf("feishu code=%d msg=%v", code, m["msg"])
	}
	if statusCode, ok := asInt(m["StatusCode"]); ok && statusCode != 0 {
		return fmt.Errorf("feishu StatusCode=%d StatusMessage=%v", statusCode, m["StatusMessage"])
	}
	// DingTalk / WeChat: {"errcode":0,"errmsg":"ok"}
	if errcode, ok := asInt(m["errcode"]); ok && errcode != 0 {
		return fmt.Errorf("%s errcode=%d errmsg=%v", name, errcode, m["errmsg"])
	}
	return nil
}

func asInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}

func (c *httpClient) doPost(payload []byte) (int, string, error) {
	req, err := http.NewRequest(http.MethodPost, c.webhook, bytes.NewReader(payload))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, string(body), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
