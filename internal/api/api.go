package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"smart-alert-aggregator/internal/config"
	"smart-alert-aggregator/internal/engine"
	"smart-alert-aggregator/internal/mute"
)

// Handler serves dashboard APIs and mute management.
type Handler struct {
	cfg         *config.Config
	configPath  string
	overlayPath string
	aggregator  *engine.Aggregator
	mutes       *mute.Store
}

func New(cfg *config.Config, configPath, overlayPath string, aggregator *engine.Aggregator, mutes *mute.Store) *Handler {
	return &Handler{
		cfg:         cfg,
		configPath:  configPath,
		overlayPath: overlayPath,
		aggregator:  aggregator,
		mutes:       mutes,
	}
}

func (h *Handler) Register(router *gin.Engine) {
	api := router.Group("/api/v1")
	{
		api.GET("/dashboard", h.Dashboard)
		api.GET("/alerts/active", h.ActiveAlerts)
		api.GET("/alerts/history", h.History)
		api.GET("/mutes", h.ListMutes)
		api.POST("/mutes", h.CreateMute)
		api.DELETE("/mutes/:id", h.DeleteMute)
		api.POST("/aggregator/flush", h.Flush)
		api.GET("/settings/notification", h.GetNotification)
		api.PUT("/settings/notification", h.UpdateNotification)
	}
}

func (h *Handler) Dashboard(c *gin.Context) {
	snap := h.aggregator.Snapshot()
	c.JSON(http.StatusOK, snap)
}

func (h *Handler) ActiveAlerts(c *gin.Context) {
	snap := h.aggregator.Snapshot()
	c.JSON(http.StatusOK, gin.H{
		"count":  len(snap.ActiveAlerts),
		"alerts": snap.ActiveAlerts,
	})
}

func (h *Handler) History(c *gin.Context) {
	events := h.aggregator.History(100)
	c.JSON(http.StatusOK, gin.H{
		"count":  len(events),
		"events": events,
	})
}

type muteView struct {
	mute.Rule
	Status string `json:"status"`
}

func (h *Handler) ListMutes(c *gin.Context) {
	now := time.Now()
	rules := h.mutes.List()
	out := make([]muteView, 0, len(rules))
	for _, r := range rules {
		out = append(out, muteView{Rule: r, Status: r.StatusAt(now)})
	}
	c.JSON(http.StatusOK, gin.H{
		"count": len(out),
		"mutes": out,
	})
}

func (h *Handler) CreateMute(c *gin.Context) {
	var req mute.CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rule, err := h.mutes.Add(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"mute":   rule,
		"status": rule.StatusAt(time.Now()),
	})
}

func (h *Handler) DeleteMute(c *gin.Context) {
	id := c.Param("id")
	if !h.mutes.Delete(id) {
		c.JSON(http.StatusNotFound, gin.H{"error": "mute not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted", "id": id})
}

func (h *Handler) Flush(c *gin.Context) {
	go h.aggregator.FlushNow()
	c.JSON(http.StatusOK, gin.H{"status": "flushing"})
}

type channelView struct {
	Enabled    bool   `json:"enabled"`
	WebhookURL string `json:"webhook_url"`
	Active     bool   `json:"active"` // enabled and usable at runtime
}

type notificationView struct {
	Cluster      string `json:"cluster"`
	Cooldown     string `json:"cooldown"`
	SendResolved bool   `json:"send_resolved"`
	MaxItems     int    `json:"max_items"`
	Channels     struct {
		Feishu   channelView `json:"feishu"`
		Dingtalk channelView `json:"dingtalk"`
		Wechat   channelView `json:"wechat"`
	} `json:"channels"`
	ActiveNotifiers []string `json:"active_notifiers"`
	ConfigPath      string   `json:"config_path"`
}

type notificationUpdate struct {
	Cluster      string `json:"cluster"`
	Cooldown     string `json:"cooldown"`
	SendResolved bool   `json:"send_resolved"`
	MaxItems     int    `json:"max_items"`
	Channels     struct {
		Feishu   channelUpdate `json:"feishu"`
		Dingtalk channelUpdate `json:"dingtalk"`
		Wechat   channelUpdate `json:"wechat"`
	} `json:"channels"`
}

type channelUpdate struct {
	Enabled    bool   `json:"enabled"`
	WebhookURL string `json:"webhook_url"`
}

func (h *Handler) GetNotification(c *gin.Context) {
	c.JSON(http.StatusOK, h.notificationView())
}

func (h *Handler) UpdateNotification(c *gin.Context) {
	var req notificationUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cooldown := h.cfg.Notification.Cooldown
	if strings.TrimSpace(req.Cooldown) != "" {
		d, err := time.ParseDuration(req.Cooldown)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效 cooldown，例如 1h / 30m / 8h"})
			return
		}
		if d <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "cooldown 必须为正数"})
			return
		}
		cooldown = config.Duration{Duration: d}
	}

	n := h.cfg.Notification
	if strings.TrimSpace(req.Cluster) != "" {
		n.Cluster = strings.TrimSpace(req.Cluster)
	}
	n.Cooldown = cooldown
	n.SendResolved = req.SendResolved
	if req.MaxItems > 0 {
		n.MaxItems = req.MaxItems
	}
	n.Channels.Feishu = config.ChannelConfig{
		Enabled:    req.Channels.Feishu.Enabled,
		WebhookURL: strings.TrimSpace(req.Channels.Feishu.WebhookURL),
	}
	n.Channels.Dingtalk = config.ChannelConfig{
		Enabled:    req.Channels.Dingtalk.Enabled,
		WebhookURL: strings.TrimSpace(req.Channels.Dingtalk.WebhookURL),
	}
	n.Channels.Wechat = config.ChannelConfig{
		Enabled:    req.Channels.Wechat.Enabled,
		WebhookURL: strings.TrimSpace(req.Channels.Wechat.WebhookURL),
	}

	active := h.aggregator.ApplyNotification(n)
	// Persist to data/ overlay (writable volume), not the possibly read-only / bind-mounted config.yaml.
	if err := config.SaveNotificationOverlay(h.overlayPath, n); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存配置失败: " + err.Error()})
		return
	}

	view := h.notificationView()
	c.JSON(http.StatusOK, gin.H{
		"status":           "ok",
		"active_notifiers": active,
		"notification":     view,
	})
}

func (h *Handler) notificationView() notificationView {
	n := h.cfg.Notification
	view := notificationView{
		Cluster:         n.Cluster,
		Cooldown:        n.Cooldown.Duration.String(),
		SendResolved:    n.SendResolved,
		MaxItems:        n.MaxItems,
		ActiveNotifiers: h.aggregator.Snapshot().Notifiers,
		ConfigPath:      h.overlayPath,
	}
	view.Channels.Feishu = toChannelView(n.Channels.Feishu)
	view.Channels.Dingtalk = toChannelView(n.Channels.Dingtalk)
	view.Channels.Wechat = toChannelView(n.Channels.Wechat)
	return view
}

func toChannelView(ch config.ChannelConfig) channelView {
	return channelView{
		Enabled:    ch.Enabled,
		WebhookURL: ch.WebhookURL,
		Active:     ch.Enabled && ch.WebhookURL != "" && !strings.Contains(ch.WebhookURL, "xxxx"),
	}
}
