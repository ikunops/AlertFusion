package alert

import (
	"encoding/json"
	"fmt"
	"time"
)

// AlertmanagerWebhook is the standard Alertmanager webhook payload.
type AlertmanagerWebhook struct {
	Version           string            `json:"version"`
	GroupKey          string            `json:"groupKey"`
	TruncatedAlerts   int               `json:"truncatedAlerts"`
	Status            string            `json:"status"`
	Receiver          string            `json:"receiver"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`
	Alerts            []AMAlert         `json:"alerts"`
}

// AMAlert is a single alert in the Alertmanager webhook.
type AMAlert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

// ParseWebhook parses raw JSON into internal Alert objects.
func ParseWebhook(body []byte) ([]Alert, error) {
	var payload AlertmanagerWebhook
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("invalid alertmanager webhook: %w", err)
	}
	if len(payload.Alerts) == 0 {
		return nil, fmt.Errorf("webhook contains no alerts")
	}

	alerts := make([]Alert, 0, len(payload.Alerts))
	for _, am := range payload.Alerts {
		labels := am.Labels
		if labels == nil {
			labels = map[string]string{}
		}
		annotations := am.Annotations
		if annotations == nil {
			annotations = map[string]string{}
		}
		status := am.Status
		if status == "" {
			status = payload.Status
		}
		alerts = append(alerts, Alert{
			Status:       status,
			Labels:       labels,
			Annotations:  annotations,
			StartsAt:     am.StartsAt,
			EndsAt:       am.EndsAt,
			GeneratorURL: am.GeneratorURL,
			Fingerprint:  am.Fingerprint,
		})
	}
	return alerts, nil
}
