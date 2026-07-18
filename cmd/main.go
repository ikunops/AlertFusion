package main

import (
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/gin-gonic/gin"

	"smart-alert-aggregator/internal/api"
	"smart-alert-aggregator/internal/config"
	"smart-alert-aggregator/internal/engine"
	"smart-alert-aggregator/internal/mute"
	"smart-alert-aggregator/internal/notifier"
	"smart-alert-aggregator/internal/webhook"
	"smart-alert-aggregator/web"
)

func main() {
	configPath := flag.String("config", "config/config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	overlayPath := config.NotificationOverlayPath(cfg.Mute.StorePath)
	if applied, err := config.ApplyNotificationOverlay(overlayPath, cfg); err != nil {
		log.Fatalf("load notification overlay: %v", err)
	} else if applied {
		log.Printf("notification overlay loaded: %s", overlayPath)
	}

	muteStore, err := mute.NewStore(cfg.Mute.StorePath)
	if err != nil {
		log.Fatalf("load mute store: %v", err)
	}
	log.Printf("mute store: %s (%d rule(s))", muteStore.Path(), muteStore.CountActive())

	notifiers := notifier.BuildNotifiers(cfg)
	if len(notifiers) == 0 {
		log.Printf("WARNING: no notification channels enabled — alerts will be aggregated but NOT sent")
		log.Printf("WARNING: set notification.channels.<feishu|dingtalk|wechat>.enabled=true and a real webhook_url")
	} else {
		names := make([]string, 0, len(notifiers))
		for _, n := range notifiers {
			names = append(names, n.Name())
		}
		log.Printf("enabled notifiers: %v", names)
	}

	aggregator := engine.NewAggregator(cfg, notifiers, muteStore)
	histPath, histCount := aggregator.HistoryStoreInfo()
	log.Printf("history store: %s (%d event(s) loaded)", histPath, histCount)
	receiver := webhook.NewReceiver(aggregator)
	ui := api.New(cfg, *configPath, overlayPath, aggregator, muteStore)

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery(), gin.Logger())
	receiver.Register(router)
	ui.Register(router)
	mountUI(router)

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	log.Printf("Smart Alert Aggregator listening on %s (wait_time=%s max_wait=%s cooldown=%s)",
		addr, cfg.Aggregation.WaitTime.Duration, cfg.Aggregation.MaxWait.Duration, cfg.Notification.Cooldown.Duration)
	log.Printf("dashboard UI: http://127.0.0.1%s/", addr)

	go func() {
		if err := router.Run(addr); err != nil {
			log.Fatalf("server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Printf("shutting down, flushing buffered alerts...")
	aggregator.FlushNow()
	aggregator.Stop()
	log.Printf("bye")
}

func mountUI(router *gin.Engine) {
	staticFS, err := fs.Sub(web.Static, "static")
	if err != nil {
		log.Fatalf("embed static: %v", err)
	}
	router.StaticFS("/static", http.FS(staticFS))
	router.GET("/", func(c *gin.Context) {
		data, err := fs.ReadFile(staticFS, "index.html")
		if err != nil {
			c.String(http.StatusInternalServerError, "ui missing")
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", data)
	})
}
