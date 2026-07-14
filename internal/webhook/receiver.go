package webhook

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"smart-alert-aggregator/internal/alert"
	"smart-alert-aggregator/internal/engine"
)

// Receiver handles Alertmanager webhook HTTP requests.
type Receiver struct {
	aggregator *engine.Aggregator
}

func NewReceiver(aggregator *engine.Aggregator) *Receiver {
	return &Receiver{aggregator: aggregator}
}

func (r *Receiver) Register(router *gin.Engine) {
	api := router.Group("/api/v1")
	{
		api.POST("/webhook/alertmanager", r.HandleAlertmanager)
	}
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
}

func (r *Receiver) HandleAlertmanager(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Printf("webhook: read body error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "read body failed"})
		return
	}

	logRawWebhook(body)

	alerts, err := alert.ParseWebhook(body)
	if err != nil {
		log.Printf("webhook: parse error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("webhook: parsed %d alert(s)", len(alerts))
	for i, a := range alerts {
		log.Printf("webhook: alert[%d] status=%s alertname=%s severity=%s hostname=%s instance=%s job=%s labels=%v annotations=%v",
			i, a.Status, a.AlertName(), a.Severity(), a.Hostname(), a.Instance(), a.Job(), a.Labels, a.Annotations)
	}

	r.aggregator.Ingest(alerts)
	c.JSON(http.StatusOK, gin.H{
		"status": "accepted",
		"count":  len(alerts),
	})
}

func logRawWebhook(body []byte) {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, body, "", "  "); err != nil {
		log.Printf("webhook: raw body (%d bytes):\n%s", len(body), string(body))
		return
	}
	text := pretty.String()
	// keep console readable for huge payloads
	const max = 8192
	if len(text) > max {
		text = text[:max] + "\n... (truncated)"
	}
	log.Printf("webhook: raw payload:\n%s", text)
	if strings.TrimSpace(string(body)) == "" || string(body) == "{}" {
		log.Printf("webhook: warning empty/minimal payload")
	}
}
