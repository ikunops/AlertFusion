package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server       ServerConfig       `yaml:"server"`
	Aggregation  AggregationConfig  `yaml:"aggregation"`
	Blackbox     BlackboxConfig     `yaml:"blackbox"`
	HostFailure  HostFailureConfig  `yaml:"host_failure"`
	Severity     SeverityConfig     `yaml:"severity"`
	Notification NotificationConfig `yaml:"notification"`
	Mute         MuteConfig         `yaml:"mute"`
}

type ServerConfig struct {
	Port int `yaml:"port"`
}

type AggregationConfig struct {
	WaitTime Duration `yaml:"wait_time"` // idle debounce after last alert
	MaxWait  Duration `yaml:"max_wait"`  // force flush after first alert
}

type BlackboxConfig struct {
	Enabled         bool                  `yaml:"enabled"`
	DomainThreshold DomainThresholdConfig `yaml:"domain_threshold"`
}

type DomainThresholdConfig struct {
	Critical int `yaml:"critical"`
}

type HostFailureConfig struct {
	CriticalAlerts []string `yaml:"critical_alerts"`
}

type SeverityConfig struct {
	Rules []SeverityRule `yaml:"rules"`
}

type SeverityRule struct {
	Condition SeverityCondition `yaml:"condition"`
	Level     string            `yaml:"level"`
}

type SeverityCondition struct {
	DomainCount string `yaml:"domain_count"`
}

type NotificationConfig struct {
	Cluster      string               `yaml:"cluster"`       // 告警集群显示名
	MaxItems     int                  `yaml:"max_items"`
	Cooldown     Duration             `yaml:"cooldown"`      // 相同 alertname 重复告警抑制时间
	SendResolved bool                 `yaml:"send_resolved"` // 是否发送恢复通知
	Retry        RetryConfig          `yaml:"retry"`
	Channels     NotificationChannels `yaml:"channels"`
}

type RetryConfig struct {
	Count    int      `yaml:"count"`
	Interval Duration `yaml:"interval"`
}

type NotificationChannels struct {
	Feishu   ChannelConfig `yaml:"feishu"`
	Dingtalk ChannelConfig `yaml:"dingtalk"`
	Wechat   ChannelConfig `yaml:"wechat"`
}

type ChannelConfig struct {
	Enabled    bool   `yaml:"enabled"`
	WebhookURL string `yaml:"webhook_url"`
}

type MuteConfig struct {
	StorePath string `yaml:"store_path"` // JSON persistence path for mute rules
}

// Duration wraps time.Duration for YAML unmarshaling of values like "30s".
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	d.Duration = parsed
	return nil
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := Default()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.applyDefaults()
	return cfg, nil
}

func Default() *Config {
	return &Config{
		Server: ServerConfig{Port: 8088},
		Aggregation: AggregationConfig{
			WaitTime: Duration{Duration: 30 * time.Second},
			MaxWait:  Duration{Duration: 90 * time.Second},
		},
		Blackbox: BlackboxConfig{
			Enabled: true,
			DomainThreshold: DomainThresholdConfig{
				Critical: 2,
			},
		},
		HostFailure: HostFailureConfig{
			CriticalAlerts: []string{
				"NodeDown",
				"TargetDown",
				"PrometheusTargetMissing",
				"InstanceDown",
			},
		},
		Severity: SeverityConfig{
			Rules: []SeverityRule{
				{
					Condition: SeverityCondition{DomainCount: ">=2"},
					Level:     "critical",
				},
			},
		},
		Notification: NotificationConfig{
			Cluster:      "xinghui_Prometheus",
			MaxItems:     10,
			Cooldown:     Duration{Duration: time.Hour},
			SendResolved: true,
			Retry: RetryConfig{
				Count:    3,
				Interval: Duration{Duration: 5 * time.Second},
			},
		},
		Mute: MuteConfig{
			StorePath: "data/mutes.json",
		},
	}
}

func (c *Config) applyDefaults() {
	if c.Server.Port == 0 {
		c.Server.Port = 8088
	}
	if c.Aggregation.WaitTime.Duration == 0 {
		c.Aggregation.WaitTime = Duration{Duration: 30 * time.Second}
	}
	if c.Aggregation.MaxWait.Duration == 0 {
		c.Aggregation.MaxWait = Duration{Duration: 3 * c.Aggregation.WaitTime.Duration}
	}
	if c.Blackbox.DomainThreshold.Critical == 0 {
		c.Blackbox.DomainThreshold.Critical = 2
	}
	if len(c.HostFailure.CriticalAlerts) == 0 {
		c.HostFailure.CriticalAlerts = []string{
			"NodeDown",
			"TargetDown",
			"PrometheusTargetMissing",
			"InstanceDown",
		}
	}
	if c.Notification.Cluster == "" {
		c.Notification.Cluster = "xinghui_Prometheus"
	}
	if c.Notification.MaxItems == 0 {
		c.Notification.MaxItems = 10
	}
	if c.Notification.Cooldown.Duration == 0 {
		c.Notification.Cooldown = Duration{Duration: time.Hour}
	}
	if c.Notification.Retry.Count == 0 {
		c.Notification.Retry.Count = 3
	}
	if c.Notification.Retry.Interval.Duration == 0 {
		c.Notification.Retry.Interval = Duration{Duration: 5 * time.Second}
	}
	if c.Mute.StorePath == "" {
		c.Mute.StorePath = "data/mutes.json"
	}
}

func (c *Config) IsHostFailureAlert(alertname string) bool {
	for _, name := range c.HostFailure.CriticalAlerts {
		if name == alertname {
			return true
		}
	}
	return false
}
