# Smart Alert Aggregator Gateway

[中文](#中文) | [English](#english)

---

## 中文

### 这个组件是做什么的？

**Smart Alert Aggregator（智能告警聚合网关）** 是挂在 **Prometheus Alertmanager 后面** 的一层通知网关。

它**不替代** Prometheus / Alertmanager，只做一件事：

> 把 Alertmanager 推过来的大量、碎片化 webhook 告警，按规则智能合并后，再发到飞书 / 钉钉 / 企业微信，减少告警噪音。

#### 解决什么问题？

生产里常见「告警风暴」：

| 场景 | Alertmanager 原始行为 | 本组件处理后 |
|------|----------------------|--------------|
| 入口异常，几十个域名同时探测失败 | 每个域名一条消息，刷屏几十条 | 按 `alertname` 合并成 **1 条**，列出影响主机 |
| 同一规则在多台机器触发（如 OOM、内存压力） | 每台机器一条 | 合并为一条，展示全部实例 |
| Alertmanager 反复推送同一组 firing 告警 | 持续重复通知 | `cooldown` 冷却期内抑制重复 |
| 告警恢复 | 默认不通知或噪音大 | 可发送绿色 **Resolved** 恢复通知 |

#### 在架构里的位置

```
Prometheus
    │
Alertmanager          ← 负责规则、路由、分组、静默
    │  Webhook
Smart Alert Aggregator ← 本组件：聚合 + 降噪 + 通知模板
    │
飞书 / 钉钉 / 企业微信
```

#### 核心能力

- 接收标准 Alertmanager Webhook：`POST /api/v1/webhook/alertmanager`
- **按 `alertname` 聚合**（同一规则多实例合并为一条）
- 防抖等待窗口（`wait_time` / `max_wait`），错峰到达的 webhook 也能合并
- **重复告警抑制**（`notification.cooldown`）
- **恢复通知**（`send_resolved`，需 Alertmanager 打开 `send_resolved: true`）
- 统一通知模板（集群、级别、标签、触发值、描述等）
- 飞书卡片 / 钉钉 / 企业微信机器人，失败自动重试

#### 非目标（本组件不做）

- Prometheus 规则管理、Silence、Routing
- 替代 Alertmanager
- Kubernetes 拓扑 / AI 根因 / 自动修复

---

## English

### What is this component for?

**Smart Alert Aggregator** is a **post-Alertmanager notification gateway** for Prometheus.

It does **not** replace Prometheus or Alertmanager. Its single job is:

> Take noisy, fragmented Alertmanager webhook alerts, intelligently aggregate them by rule, then deliver clean notifications to Feishu (Lark) / DingTalk / WeCom.

#### Problems it solves

| Scenario | Raw Alertmanager behavior | With this gateway |
|----------|---------------------------|-------------------|
| Edge / CDN failure: dozens of domains fail probes | One chat message per domain | **One** message keyed by `alertname`, listing affected hosts |
| Same rule fires on many hosts (OOM, memory pressure, …) | One message per host | One aggregated message with all instances |
| Alertmanager repeatedly re-sends the same firing set | Notification spam | Suppressed during `cooldown` if no new instances |
| Alert recovery | Often missing or noisy | Optional green **Resolved** notifications |

#### Where it sits in the stack

```
Prometheus
    │
Alertmanager                 ← rules, routing, grouping, silences
    │  Webhook
Smart Alert Aggregator       ← this component: aggregate + denoise + templates
    │
Feishu / DingTalk / WeCom
```

#### Key capabilities

- Standard Alertmanager webhook: `POST /api/v1/webhook/alertmanager`
- **Aggregate by `alertname`** (same rule, many instances → one notice)
- Debounced wait window (`wait_time` / `max_wait`) so staggered webhooks still merge
- **Repeat suppression** via `notification.cooldown`
- **Recovery notifications** (`send_resolved`; Alertmanager must also set `send_resolved: true`)
- Unified template (cluster, severity, labels, value, description)
- Feishu card / DingTalk / WeCom bots with retries

#### Non-goals (v1)

- Prometheus rule management, Silence, or Routing
- Replacing Alertmanager
- K8s topology / AI RCA / auto-remediation

---

## Build

Requires Go 1.24+.

```bash
make build          # current platform → dist/smart-alert-aggregator
make build-linux    # linux-amd64 / linux-arm64
make build-all
```

Or:

```bash
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" \
  -o dist/smart-alert-aggregator-linux-amd64 ./cmd
```

---

## Quick start

### 1. Config

Edit `config/config.yaml` and set bot webhooks:

```yaml
notification:
  cluster: xinghui_Prometheus
  cooldown: 1h            # repeat suppression window
  send_resolved: true     # recovery notifications
  channels:
    feishu:
      enabled: true
      webhook_url: https://open.feishu.cn/open-apis/bot/v2/hook/xxxx
```

### 2. Binary + systemd (recommended for production)

```bash
sudo useradd -r -s /usr/sbin/nologin smart-alert || true
sudo mkdir -p /opt/smart-alert-aggregator/config

sudo cp dist/smart-alert-aggregator-linux-amd64 /opt/smart-alert-aggregator/smart-alert-aggregator
sudo chmod +x /opt/smart-alert-aggregator/smart-alert-aggregator
sudo cp config/config.yaml /opt/smart-alert-aggregator/config/config.yaml
sudo chown -R smart-alert:smart-alert /opt/smart-alert-aggregator

sudo cp deploy/smart-alert-aggregator.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now smart-alert-aggregator
```

```bash
sudo systemctl status smart-alert-aggregator
journalctl -u smart-alert-aggregator -f
curl http://localhost:8088/healthz
```

### 3. Docker (no Compose plugin)

```bash
docker rm -f smart-alert-aggregator 2>/dev/null || true
docker build -t smart-alert-aggregator:latest .
docker run -d \
  --name smart-alert-aggregator \
  --restart unless-stopped \
  -p 8088:8088 \
  -v "$PWD/config/config.yaml:/app/config/config.yaml:ro" \
  smart-alert-aggregator:latest
```

If you have Compose V2 (`docker compose` with a space):

```bash
docker rm -f smart-alert-aggregator 2>/dev/null || true
docker compose up -d --build --force-recreate
```

> Note: legacy `docker-compose` (hyphen) + new Docker Engine often fails with `'ContainerConfig'`. Prefer `docker build`/`docker run` or Compose V2.

### 4. Local run

```bash
go run ./cmd -config config/config.yaml
```

### 5. Alertmanager

```yaml
receivers:
  - name: smart-aggregator
    webhook_configs:
      - url: http://<aggregator-host>:8088/api/v1/webhook/alertmanager
        send_resolved: true   # required for recovery notifications

route:
  receiver: smart-aggregator
```

Examples: `http://127.0.0.1:8088/api/v1/webhook/alertmanager`

---

## Repeat alerts & recovery

| Setting | Where | Default | Meaning |
|---------|-------|---------|---------|
| `notification.cooldown` | `config/config.yaml` | `1h` | **Repeat suppression.** Same `alertname` is not re-sent within this window unless new instances appear |
| `notification.send_resolved` | `config/config.yaml` | `true` | Send **recovery** notifications |
| Alertmanager `send_resolved` | Alertmanager config | must be `true` | Otherwise this service never receives resolved events |

```yaml
notification:
  cluster: xinghui_Prometheus
  cooldown: 1h          # e.g. 30m / 2h
  send_resolved: true
```

Recovery messages use `S1/S2 Resolved`, a green Feishu card, and “告警已恢复”.

---

## API

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/webhook/alertmanager` | Alertmanager webhook |
| GET | `/healthz` | Health check |

---

## Configuration reference

| Key | Default | Description |
|-----|---------|-------------|
| `server.port` | `8088` | HTTP port |
| `aggregation.wait_time` | `30s` | Debounce idle time before flush |
| `aggregation.max_wait` | `90s` | Max wait from first alert in a window |
| `notification.cluster` | `xinghui_Prometheus` | Cluster name in the template |
| `notification.cooldown` | `1h` | Repeat suppression per `alertname` |
| `notification.send_resolved` | `true` | Enable recovery notifications |
| `notification.max_items` | `10` | Max listed items in one message |
| `notification.retry.count` | `3` | Notify retry count |
| `notification.retry.interval` | `5s` | Retry interval |

---

## Project layout

```
cmd/main.go
internal/
  webhook/     # HTTP receiver
  alert/       # models & parsing
  engine/      # buffer, analyze, severity, cooldown
  notifier/    # Feishu / DingTalk / WeCom
  template/    # notification templates
  config/      # config loader
config/config.yaml
deploy/smart-alert-aggregator.service
Dockerfile
docker-compose.yaml
Makefile
```
