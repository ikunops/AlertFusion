package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"smart-alert-aggregator/internal/config"
	"smart-alert-aggregator/internal/engine"
	"smart-alert-aggregator/internal/mute"
)

// Handler serves dashboard APIs and mute management.
type Handler struct {
	cfg        *config.Config
	aggregator *engine.Aggregator
	mutes      *mute.Store
}

func New(cfg *config.Config, aggregator *engine.Aggregator, mutes *mute.Store) *Handler {
	return &Handler{cfg: cfg, aggregator: aggregator, mutes: mutes}
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
