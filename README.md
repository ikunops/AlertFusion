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
Smart Alert Aggregator ← 本组件：聚合 + 降噪 + 通知模板 + Web 控制台
    │
飞书 / 钉钉 / 企业微信
```

#### 核心能力

- 接收标准 Alertmanager Webhook：`POST /api/v1/webhook/alertmanager`
- **按 `alertname` 聚合**（同一规则多实例合并为一条）
- 防抖等待窗口（`wait_time` / `max_wait`），错峰到达的 webhook 也能合并
- **重复告警抑制**（`notification.cooldown`）
- **恢复通知**（`send_resolved`，需 Alertmanager 打开 `send_resolved: true`）
- **Web 控制台**：查看活跃告警、通知历史、编辑通知通道
- **按时间段屏蔽告警**：快捷时长或自定义起止时间，匹配规则在窗口内不推送
- 统一通知模板（集群、级别、标签、触发值、描述等）
- 飞书卡片 / 钉钉 / 企业微信机器人，失败自动重试

#### 非目标（本组件不做）

- Prometheus 规则管理、Silence、Routing（本组件的「屏蔽」只抑制下游通知，不替代 Alertmanager Silence）
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
Smart Alert Aggregator       ← aggregate + denoise + templates + Web UI
    │
Feishu / DingTalk / WeCom
```

#### Key capabilities

- Standard Alertmanager webhook: `POST /api/v1/webhook/alertmanager`
- **Aggregate by `alertname`** (same rule, many instances → one notice)
- Debounced wait window (`wait_time` / `max_wait`) so staggered webhooks still merge
- **Repeat suppression** via `notification.cooldown`
- **Recovery notifications** (`send_resolved`; Alertmanager must also set `send_resolved: true`)
- **Web console** for active alerts and notification history
- **Time-window mute**: preset durations or custom start/end; matched alerts are not pushed
- Unified template (cluster, severity, labels, value, description)
- Feishu card / DingTalk / WeCom bots with retries

#### Non-goals (v1)

- Prometheus rule management, Silence, or Routing (UI mute only blocks downstream notify)
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

仓库只提交示例配置。本地请复制后填写真实 webhook（`config/config.yaml` 已被 gitignore）：

```bash
cp config/config.example.yaml config/config.yaml
```

编辑 `config/config.yaml`：

```yaml
notification:
  cluster: xinghui_Prometheus
  cooldown: 1h            # repeat suppression window
  send_resolved: true     # recovery notifications
  channels:
    feishu:
      enabled: true
      webhook_url: https://open.feishu.cn/open-apis/bot/v2/hook/xxxx

mute:
  store_path: data/mutes.json   # 屏蔽规则持久化路径
```

### 2. Binary + systemd (recommended for production)

```bash
sudo useradd -r -s /usr/sbin/nologin smart-alert || true
sudo mkdir -p /opt/smart-alert-aggregator/config /opt/smart-alert-aggregator/data

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
  --user root \
  --restart unless-stopped \
  -p 8088:8088 \
  -v "$PWD/config/config.yaml:/app/config/config.yaml:ro" \
  -v "$PWD/data:/app/data" \
  smart-alert-aggregator:latest
```

> 容器以 **root** 运行，避免挂载的 `./data` 因属主不是 uid 1000 而写失败。Web「通知方式」写入 `data/notification.yaml`；`config.yaml` 可只读。

If you have Compose V2 (`docker compose` with a space):

```bash
docker rm -f smart-alert-aggregator 2>/dev/null || true
docker compose up -d --build --force-recreate
```

> Note: legacy `docker-compose` (hyphen) + new Docker Engine often fails with `'ContainerConfig'`. Prefer `docker build`/`docker run` or Compose V2.

### 4. Local run

```bash
cp config/config.example.yaml config/config.yaml   # first time
go run ./cmd -config config/config.yaml
```

打开控制台：<http://127.0.0.1:8088/>

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

## Web 控制台

启动服务后访问根路径即可：

| 页面 | 功能 |
|------|------|
| 活跃告警 | 查看当前 firing 告警，一键创建屏蔽 |
| 屏蔽规则 | 查看生效 / 待生效规则，解除屏蔽 |
| 通知方式 | 编辑飞书 / 钉钉 / 企微 webhook、冷却与恢复通知；保存到 `data/notification.yaml` 并立即热生效 |
| 通知历史 | 通知发送、屏蔽拦截、冷却抑制、恢复事件 |

### 按时间段屏蔽

创建屏蔽时可选择：

- **快捷时长**：1 小时 / 4 小时 / 12 小时 / 1 天 / 7 天 / 永久
- **自定义时间段**：指定开始、结束时间（可预约未来生效）

匹配条件支持 `alertname`、`hostname`、`instance`（可组合）。规则持久化到 `mute.store_path`（默认 `data/mutes.json`），进程重启后仍生效。

生效窗口内匹配到的告警**不会推送到**飞书 / 钉钉 / 企微（不等同于 Alertmanager Silence）。

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
| GET | `/` | Web 控制台 |
| GET | `/healthz` | Health check |
| POST | `/api/v1/webhook/alertmanager` | Alertmanager webhook |
| GET | `/api/v1/dashboard` | 概览：活跃告警、统计、通道信息 |
| GET | `/api/v1/alerts/active` | 当前活跃告警列表 |
| GET | `/api/v1/alerts/history` | 最近通知 / 屏蔽 / 抑制历史 |
| GET | `/api/v1/mutes` | 屏蔽规则列表 |
| POST | `/api/v1/mutes` | 创建屏蔽规则（支持 `duration` 或 `starts_at`/`expires_at`） |
| DELETE | `/api/v1/mutes/:id` | 解除屏蔽 |
| GET | `/api/v1/settings/notification` | 读取通知设置（通道 / 冷却 / 恢复） |
| PUT | `/api/v1/settings/notification` | 更新通知设置并热生效，持久化到 `data/notification.yaml` |
| POST | `/api/v1/aggregator/flush` | 立刻刷出缓冲中的告警 |

### 创建屏蔽示例

快捷时长：

```bash
curl -X POST http://127.0.0.1:8088/api/v1/mutes \
  -H 'Content-Type: application/json' \
  -d '{"alertname":"NodeDown","reason":"维护","duration":"4h"}'
```

自定义时间段：

```bash
curl -X POST http://127.0.0.1:8088/api/v1/mutes \
  -H 'Content-Type: application/json' \
  -d '{
    "alertname":"ProbeFailed",
    "reason":"错峰窗口",
    "starts_at":"2026-07-16T22:00:00+08:00",
    "expires_at":"2026-07-17T06:00:00+08:00"
  }'
```

永久屏蔽：省略 `duration` 与 `expires_at`。

---

## Configuration reference

| Key | Default | Description |
|-----|---------|-------------|
| `server.port` | `8088` | HTTP port |
| `mute.store_path` | `data/mutes.json` | Mute rules persistence file |
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
  api/         # dashboard & mute HTTP APIs
  webhook/     # Alertmanager webhook receiver
  alert/       # models & parsing
  engine/      # buffer, analyze, severity, cooldown, mute hook
  mute/        # time-window mute store
  notifier/    # Feishu / DingTalk / WeCom
  template/    # notification templates
  config/      # config loader
web/           # embedded Web UI (go:embed)
config/config.example.yaml
deploy/smart-alert-aggregator.service
Dockerfile
docker-compose.yaml
Makefile
```
