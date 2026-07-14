package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gin-gonic/gin"

	"smart-alert-aggregator/internal/config"
	"smart-alert-aggregator/internal/engine"
	"smart-alert-aggregator/internal/notifier"
	"smart-alert-aggregator/internal/webhook"
)

func main() {
	configPath := flag.String("config", "config/config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

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

	aggregator := engine.NewAggregator(cfg, notifiers)
	receiver := webhook.NewReceiver(aggregator)

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery(), gin.Logger())
	receiver.Register(router)

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	log.Printf("Smart Alert Aggregator listening on %s (wait_time=%s max_wait=%s cooldown=%s)",
		addr, cfg.Aggregation.WaitTime.Duration, cfg.Aggregation.MaxWait.Duration, cfg.Notification.Cooldown.Duration)

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
	log.Printf("bye")
}
