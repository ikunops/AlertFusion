package alert

import (
	"strings"
	"unicode"
)

// HumanizeAlertName converts alertname to a short Chinese / readable description.
func HumanizeAlertName(name string) string {
	mapping := map[string]string{
		"CPUHigh":                 "CPU使用率高",
		"MemoryHigh":              "内存使用率高",
		"DiskFull":                "磁盘空间不足",
		"FilesystemFull":          "文件系统空间不足",
		"NodeDown":                "节点不可达",
		"InstanceDown":            "实例不可达",
		"TargetDown":              "监控目标不可达",
		"PrometheusTargetMissing": "Prometheus目标缺失",
		"ProbeFailed":             "探测失败",
		"ProbeSuccess":            "探测失败",
		"HostMemoryUnderMemoryPressure": "主机内存压力过高",
		"HostOomKillDetected":           "主机OOM杀进程",
		"HostOutOfMemory":               "主机内存耗尽",
		"HostHighCpuLoad":               "主机CPU负载高",
		"RedisDisconnectedSlaves":       "Redis从节点断开",
		"RedisDown":                     "Redis不可用",
		"HostUnusualNetworkThroughputIn":  "主机入网流量异常",
		"HostUnusualNetworkThroughputOut": "主机出网流量异常",
	}
	if v, ok := mapping[name]; ok {
		return v
	}
	if name == "" {
		return "未知告警"
	}
	return SplitAlertName(name)
}

// SplitAlertName turns CamelCase / snake_case alert names into readable text.
// HostOomKillDetected -> Host Oom Kill Detected
func SplitAlertName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return name
	}
	if strings.Contains(name, "_") {
		parts := strings.Split(name, "_")
		for i, p := range parts {
			if p == "" {
				continue
			}
			parts[i] = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
		}
		return strings.Join(parts, " ")
	}

	var b strings.Builder
	runes := []rune(name)
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(prev) || (unicode.IsUpper(prev) && nextLower) {
				b.WriteByte(' ')
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ShortAnomalyName returns a short label for attached anomalies.
func ShortAnomalyName(name string) string {
	mapping := map[string]string{
		"CPUHigh":             "CPU",
		"MemoryHigh":          "Memory",
		"DiskFull":            "Disk",
		"FilesystemFull":      "Filesystem",
		"HostOomKillDetected": "OOM",
	}
	if v, ok := mapping[name]; ok {
		return v
	}
	return name
}
