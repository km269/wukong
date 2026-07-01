# Gateway 部署文档

> Wukong Gateway 将外部 IM 平台（飞书、企业微信）的回调桥接到 Wukong Agent 核心循环。

## 目录

- [前置条件](#前置条件)
- [架构](#架构)
- [各平台接入步骤](#各平台接入步骤)
  - [飞书 / Lark](#飞书--lark)
  - [企业微信 / WeCom](#企业微信--wecom)
- [Nginx 反向代理（生产环境）](#nginx-反向代理生产环境)
- [Docker 部署](#docker-部署)
- [健康检查 & 监控](#健康检查--监控)
- [安全加固](#安全加固)
- [故障排查](#故障排查)

---

## 前置条件

| 组件 | 要求 |
|------|------|
| Go | ≥ 1.24 |
| Wukong | 已安装且 Agent 正常运作 |
| HTTPS 公网域名 | 各平台回调 URL 要求 HTTPS（开发环境可用 ngrok） |
| DNS 解析 | 确保域名指向 Gateway 所在服务器 |

---

## 架构

```
                    公网 HTTPS
        ┌─────────────┬─────────────┐
        │             │             │
   Feishu Bot    WeCom Bot     Slack Bot
        │             │             │
        ▼             ▼             ▼
   ┌─────────────────────────────────────┐
   │          Nginx (TLS Termination)    │
   │          :443 → :9093               │
   └─────────────────────────────────────┘
                    │
          ┌─────────┴─────────┐
          ▼                   ▼
   ┌─────────────┐    ┌─────────────┐
   │  Wukong     │    │  Wukong     │
   │  Gateway    │    │  Gateway    │
   │  :9093      │    │  :9093      │
   │  (node-1)   │    │  (node-2)   │
   └─────────────┘    └─────────────┘
          │                   │
          └─────────┬─────────┘
                    ▼
          ┌─────────────────┐
          │   Wukong Agent   │
          │   (CoreLoop)     │
          └─────────────────┘
```

**关键说明**：
- Gateway 是无状态的。去重和限流使用内存，重启后丢失但不影响正确性。
- 如在多节点部署，使用 Nginx `ip_hash` 确保同一用户路由到同一节点。
- Session 存储在共享 SQLite（`wukong.db`）中。

---

## 各平台接入步骤

### 飞书 / Lark

#### 1. 创建飞书应用

1. 访问 [飞书开放平台](https://open.feishu.cn/app) → 创建企业自建应用
2. 获取：
   - **App ID** (`cli_xxx`)
   - **App Secret**
3. 添加应用能力 → **机器人**

#### 2. 配置事件订阅

1. 事件订阅 → 配置请求网址
   - URL: `https://your-domain.com/feishu/callback`
2. 添加事件：
   - `im.message.receive_v1`（接收消息）
   - 如需卡片交互：`card.action.trigger`
3. 保存时会触发 URL 验证，确保 Gateway 已启动

#### 3. 配置权限

需要以下权限（在"权限管理"中搜索并开通）：

| 权限 | 用途 |
|------|------|
| `im:message` | 获取消息内容 |
| `im:message:send_as_bot` | 以机器人身份发消息 |
| `im:resource` | 下载文件/图片（可选） |

#### 4. 配置 Wukong

```yaml
gateway:
  enabled: true
  address: ":9093"
  feishu:
    enabled: true
    app_id: "cli_a7x9..."
    app_secret: "${FEISHU_APP_SECRET}"
    verification_token: "${FEISHU_VERIFICATION_TOKEN}"
    encrypt_key: "${FEISHU_ENCRYPT_KEY}"
    stream_card_enabled: true
```

环境变量：

```bash
export FEISHU_APP_SECRET="xxx"
export FEISHU_VERIFICATION_TOKEN="xxx"
export FEISHU_ENCRYPT_KEY="xxx"
```

#### 5. 验证

```bash
# 启动 Wukong
wukong run

# 查看启动日志，应包含：
# gateway: feishu channel registered
# gateway: starting server  address=:9093

# 在飞书中 @机器人 发送消息测试
```

---

### 企业微信 / WeCom

支持两种接入模式：

| 模式 | 适用场景 | 回调格式 | 签名方式 | 流式支持 |
|------|----------|----------|----------|----------|
| **AI Bot**（推荐） | 智能机器人 | JSON | SHA1 | ✅ aibot/stream API |
| **企业应用** | 自建应用 / 第三方应用 | 加密 XML | SHA1 + AES | ❌（仅被动回复） |

#### 模式一：AI Bot（推荐）

1. **企业微信管理后台** → 应用管理 → 自建应用 → **AI Bot**
2. 配置回调 URL：
   - **URL**: `https://your-domain.com/wecom/callback`
   - **Token**: 随机字符串
   - **EncodingAESKey**: 随机生成 43 位字符串
3. 获取：
   - **CorpID**: `wwxxxx...`（在企业信息页）
   - **Secret**: 应用的 Secret

```yaml
gateway:
  wecom:
    enabled: true
    corpid: "ww1234567890abcdef"
    secret: "${WECOM_SECRET}"
    token: "${WECOM_TOKEN}"
    encoding_aes_key: "${WECOM_ENCODING_AES_KEY}"
    stream_enabled: true   # AI Bot 支持流式消息
```

#### 模式二：企业应用

配置与 AI Bot 相同，但需要额外注意：

- 回调消息为 **加密 XML** 格式
- 收到消息后必须在 **5 秒内** 返回加密的 XML 响应
- 不支持流式消息（`stream_enabled` 无效）

```yaml
gateway:
  wecom:
    enabled: true
    corpid: "ww1234567890abcdef"
    secret: "${WECOM_SECRET}"
    token: "${WECOM_TOKEN}"
    encoding_aes_key: "${WECOM_ENCODING_AES_KEY}"
    stream_enabled: false  # 企业应用模式不支持流式
```

#### 环境变量

```bash
export WECOM_SECRET="xxx"
export WECOM_TOKEN="xxx"
export WECOM_ENCODING_AES_KEY="xxx"
```

---

## Nginx 反向代理（生产环境）

```nginx
upstream wukong_gateway {
    # 单节点
    server 127.0.0.1:9093;
}

server {
    listen 443 ssl http2;
    server_name wukong.your-domain.com;

    ssl_certificate     /etc/ssl/certs/wukong.pem;
    ssl_certificate_key /etc/ssl/private/wukong.key;
    ssl_protocols TLSv1.2 TLSv1.3;

    # Gateway 回调（所有平台共用此 location）
    location / {
        proxy_pass http://wukong_gateway;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # 流式回复需要长连接
        proxy_read_timeout 300s;
        proxy_buffering off;

        # 限制请求体大小（大部分平台 ≤ 1MB）
        client_max_body_size 10m;
    }

    # Metrics 端点（可选，仅内网可访问）
    location /metrics {
        allow 10.0.0.0/8;
        allow 172.16.0.0/12;
        allow 192.168.0.0/16;
        deny all;

        proxy_pass http://wukong_gateway;
    }
}

# HTTP → HTTPS 自动跳转
server {
    listen 80;
    server_name wukong.your-domain.com;
    return 301 https://$host$request_uri;
}
```

---

## Docker 部署

### Dockerfile

```dockerfile
FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o wukong ./cmd/wukong

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /app/wukong /usr/local/bin/wukong
COPY --from=builder /app/config.yaml /etc/wukong/config.yaml

EXPOSE 9093
ENTRYPOINT ["wukong", "--config", "/etc/wukong/config.yaml", "run"]
```

### docker-compose.yml

```yaml
version: "3.9"
services:
  wukong:
    build: .
    ports:
      - "9093:9093"
    volumes:
      - ./config.yaml:/etc/wukong/config.yaml:ro
      - ./data:/data
    environment:
      - FEISHU_APP_SECRET=${FEISHU_APP_SECRET}
      - FEISHU_VERIFICATION_TOKEN=${FEISHU_VERIFICATION_TOKEN}
      - FEISHU_ENCRYPT_KEY=${FEISHU_ENCRYPT_KEY}
      - WECOM_SECRET=${WECOM_SECRET}
      - WECOM_TOKEN=${WECOM_TOKEN}
      - WECOM_ENCODING_AES_KEY=${WECOM_ENCODING_AES_KEY}
    restart: unless-stopped
```

---

## 健康检查 & 监控

### Metrics 端点

```bash
curl -s http://localhost:9093/metrics | jq
```

输出：

```json
{
  "status": "ok",
  "dedup_size": 12,
  "rate_limiter": {
    "active_users": 2,
    "concurrent": 1,
    "max_concurrent": 100
  },
  "channels": [
    {"name": "feishu", "path": "/feishu"},
    {"name": "wecom", "path": "/wecom"}
  ]
}
```

### Prometheus / 监控脚本

可使用 cron + curl 定时采集：

```bash
#!/bin/bash
# check_gateway.sh
METRICS=$(curl -s http://localhost:9093/metrics)
STATUS=$(echo "$METRICS" | jq -r '.status')
CONCURRENT=$(echo "$METRICS" | jq -r '.rate_limiter.concurrent')

if [ "$STATUS" != "ok" ]; then
    echo "Gateway unhealthy: $STATUS"
    # 触发告警...
fi

if [ "$CONCURRENT" -gt 80 ]; then
    echo "High concurrency: $CONCURRENT"
    # 触发告警...
fi
```

### 关键日志

```bash
# 查看实时日志
tail -f wukong.log | grep "gateway:"

# 重点关注以下模式：
# - "duplicate message dropped" → 去重命中（正常）
# - "rate limit exceeded" → 限流触发（需关注）
# - "agent run failed" → Agent 异常（需排查）
# - "panic recovered" → Channel 崩溃（严重）
```

---

## 安全加固

### 1. 防火墙

```bash
# 仅允许 Nginx 访问 Gateway 端口
iptables -A INPUT -p tcp --dport 9093 -s 127.0.0.1 -j ACCEPT
iptables -A INPUT -p tcp --dport 9093 -j DROP
```

### 2. 限流保护

已内置双层层级保护：

| 层级 | 机制 | 默认值 |
|------|------|--------|
| 去重 | MessageID + TTL Map | 5 分钟窗口 |
| 用户限流 | 滑动窗口计数 | 10 req/10s per user |
| 并发控制 | Semaphore | 100 并发 sessions |

配置文件调整：

```yaml
gateway:
  max_concurrent_sessions: 50   # 降低并发限制
  message_dedup_ttl: "10m"      # 延长去重窗口
  rate_limit_per_user: 5        # 收紧用户限流
  rate_limit_window: "10s"
```

### 3. 密钥管理

- ❌ 禁止将 `app_secret`、`secret` 等写入 `config.yaml` 提交到 Git
- ✅ 使用 `${ENV_VAR}` 占位符 + 环境变量注入
- ✅ 生产环境使用 Vault / K8s Secrets 管理密钥

### 4. TLS 版本

- 强制 TLS 1.2+
- 定期更新证书（建议 Let's Encrypt + certbot 自动续期）
- 禁用不安全的加密套件

---

## 故障排查

### 问题 1: URL 验证失败

**症状**：飞书/企微后台配置回调 URL 时提示验证失败

**排查步骤**：

```bash
# 1. 检查 Gateway 是否运行
curl http://localhost:9093/metrics

# 2. 检查端口监听
netstat -tlnp | grep 9093

# 3. 查看日志
tail -100 wukong.log | grep "gateway:"

# 4. 手动模拟飞书 URL 验证
curl -X POST https://your-domain.com/feishu/callback \
  -H "Content-Type: application/json" \
  -d '{"type":"url_verification","token":"test"}'
```

**常见原因**：
- `verification_token` / `token` 配置错误
- `encrypt_key` / `encoding_aes_key` 解密失败
- 时间戳偏差超过 5 分钟（检查 NTP）

### 问题 2: 消息不回复

**症状**：收到消息但机器人不回复

**排查步骤**：

1. 查看日志确认消息是否进入 Agent 循环：
   ```
   gateway: processing message  channel=feishu  user=feishu:ou_xxx
   ```
2. 检查 Agent 执行状态：
   ```
   gateway: agent run failed  → Agent 异常
   gateway: send reply failed → 回复发送失败
   ```
3. 检查 API 调用是否成功（飞书 token 过期 / 企微 access_token 失效）

### 问题 3: 高并发请求堆积

**症状**：`rate_limiter.concurrent` 持续接近 `max_concurrent_sessions`

**解决**：
1. 增加 `max_concurrent_sessions`（如 `200`）
2. 降低 Agent `max_run_duration`（如 `60s`）
3. 使用更快/更便宜的模型处理 IM 场景
4. 考虑对非关键消息（如群聊 @）设置响应优先级

### 问题 4: 去重失效

**症状**：同一消息被 Agent 处理多次

**原因**：
- `MessageID` 为空（去重自动旁路）
- 平台重试间隔超过 `message_dedup_ttl`（5 分钟）
- Gateway 重启导致内存去重缓存丢失

**解决**：
- 延长 `message_dedup_ttl` 到 `"10m"`
- 确保 Channel.ParseMessage 正确提取 MessageID
