# Gateway — Multi-Platform Channel Development Guide

`internal/gateway/` 是 Wukong 的多平台消息网关，统一接收和分发来自外部 IM 平台
（飞书、企业微信等）的回调，将其桥接到 Wukong Agent 核心循环。

## 目录

- [架构概览](#架构概览)
- [快速开始：添加新平台](#快速开始添加新平台)
- [Channel 接口详解](#channel-接口详解)
- [消息处理流水线](#消息处理流水线)
- [现有 Channel 实现](#现有-channel-实现)
- [配置参考](#配置参考)
- [监控与运维](#监控与运维)

## 架构概览

```
外部IM平台 (Feishu / WeCom / Slack)
        │
        │ HTTP callback (POST /feishu/callback)
        ▼
┌─────────────────────────────────────────────┐
│ GatewayServer (:9093)                       │
│                                              │
│  ┌────────────┐  ┌────────────┐             │
│  │ Middleware  │  │ Channel    │             │
│  │  · Logging  │  │ Router     │             │
│  │  · Recovery │──│            │──Channel──┐ │
│  └────────────┘  └────────────┘           │ │
│                                              │ │
│  ┌──────────────────────────────────────┐   │ │
│  │ handleChannel (Pipeline)             │   │ │
│  │  1. VerifyRequest  (签名/解密)       │◄──┘ │
│  │  2. parsePlatformEvent (URL验证)     │     │
│  │  3. ParseMessage    (统一格式)       │     │
│  │  4. MessageDedup    (去重)           │     │
│  │  5. BuildUserID / SessionID (身份)   │     │
│  │  6. RateLimiter     (限流/并发控制)  │     │
│  │  7. SessionStore    (会话持久化)      │     │
│  │  8. CoreLoop.Run    (Agent 执行)     │     │
│  │  9. SendReply       (回复/流式推送)  │     │
│  └──────────────────────────────────────┘   │
│                                              │
│  ┌────────────┐  ┌────────────┐             │
│  │ Dedup      │  │ RateLimiter│             │
│  │ (sync.Map  │  │ (Sliding   │             │
│  │   + TTL)   │  │  Window)   │             │
│  └────────────┘  └────────────┘             │
└─────────────────────────────────────────────┘
```

## 快速开始：添加新平台

以添加 Slack Channel 为例，共需 4 步：

### Step 1: 实现 Channel 接口

```go
// internal/gateway/slack/channel.go
package slack

import "github.com/km269/wukong/internal/gateway"

type SlackChannel struct {
    cfg      *config.WukongConfig
    coreLoop *agent.CoreLoop
    sender   *SlackSender
}

func NewSlackChannel(cfg *config.WukongConfig, loop *agent.CoreLoop) *SlackChannel {
    return &SlackChannel{
        cfg:      cfg,
        coreLoop: loop,
        sender:   NewSlackSender(cfg),
    }
}

// 实现 7 个接口方法...
func (s *SlackChannel) Name() string         { return "slack" }
func (s *SlackChannel) RoutePath() string     { return "/slack" }
func (s *SlackChannel) VerifyRequest(r *http.Request) ([]byte, error) { ... }
func (s *SlackChannel) ParseMessage(body []byte) (*gateway.GatewayMessage, error) { ... }
func (s *SlackChannel) BuildUserID(msg *gateway.GatewayMessage) string { ... }
func (s *SlackChannel) BuildSessionID(msg *gateway.GatewayMessage) string { ... }
func (s *SlackChannel) SendReply(ctx context.Context, msg *gateway.GatewayMessage, events <-chan *event.Event) error { ... }
func (s *SlackChannel) HandlePlatformEvent(w http.ResponseWriter, evt *gateway.PlatformEvent) ([]byte, error) { ... }
```

### Step 2: 编写模块（参考现有实现）

```
internal/gateway/slack/
├── channel.go   # Channel 接口实现
├── crypto.go    # 签名验证（Slack Signing Secret）
├── message.go   # Slack 消息 ↔ GatewayMessage 转换
├── sender.go    # Slack API 消息发送 + 流式支持
└── token.go     # Bot Token 管理（如需轮换）
```

参考实现：
- **最简单**：Feishu — JSON 回调 + HMAC-SHA256 签名
- **较复杂**：WeCom — XML 加密 + SHA1 签名 + 双模式（AI Bot/企业应用）

### Step 3: 添加配置类型

在 `internal/config/types.go` 中添加：

```go
type SlackChannelConfig struct {
    Enabled            bool          `mapstructure:"enabled"`
    BotToken           string        `mapstructure:"bot_token"`
    SigningSecret      string        `mapstructure:"signing_secret"`
    StreamEnabled      bool          `mapstructure:"stream_enabled"`
    MaxMessageLength   int           `mapstructure:"max_message_length"`
}
```

在 `GatewayConfig` 中添加字段：

```go
Slack SlackChannelConfig `mapstructure:"slack"`
```

### Step 4: 注册到 Bootstrap

在 `internal/cli/session.go` 的 `bootstrapSession()` 中：

```go
if wukongCfg.Gateway.Slack.Enabled {
    sc := slack.NewSlackChannel(wukongCfg, loop)
    if err := state.GatewayServer.RegisterChannel(sc); err != nil {
        util.Logger.Warn("gateway: register slack failed", ...)
    }
}
```

## Channel 接口详解

```go
type Channel interface {
    Name() string                                           // "feishu", "wecom", "slack"
    RoutePath() string                                      // "/feishu", "/wecom" → 注册 /feishu/callback
    VerifyRequest(r *http.Request) ([]byte, error)          // 签名验证/解密，返回body
    ParseMessage(body []byte) (*GatewayMessage, error)      // 平台消息 → 统一格式
    BuildUserID(msg *GatewayMessage) string                 // 构建 Wukong 用户ID
    BuildSessionID(msg *GatewayMessage) string              // 构建 Wukong 会话ID
    SendReply(ctx context.Context, msg *GatewayMessage,
        events <-chan *event.Event) error                   // 发送回复（含流式）
    HandlePlatformEvent(w http.ResponseWriter,
        evt *PlatformEvent) ([]byte, error)                 // 平台事件处理
}
```

### VerifyRequest — 签名验证

| 平台 | 机制 |
|------|------|
| Feishu | HMAC-SHA256 请求体 + timestamp 防重放 |
| WeCom | SHA1(msg_signature, token, timestamp, nonce, echostr/body) |
| Slack | HMAC-SHA256: `v0:{timestamp}:{body}` |

**注意**：VerifyRequest 读取 body 后，需要返回解密/验证后的 body（如果是加密通道）。

### ParseMessage — 消息解析

将平台原始消息转为 `GatewayMessage`：

```go
type GatewayMessage struct {
    Platform       string          // 平台标识（由 gateway 自动填充）
    PlatformUserID string          // 平台用户ID（open_id / userid）
    ConversationID string          // 会话/群聊ID
    Content        string          // 纯文本内容
    ContentType    string          // "text", "image", "file", "mixed"
    ResponseURL    string          // 流式回复 URL（可选）
    MessageID      string          // 消息唯一ID（用于去重）
    Timestamp      int64           // 消息时间戳
    RawData        json.RawMessage // 原始数据（供 SendReply 使用）
}
```

### BuildUserID / BuildSessionID — 身份映射

最佳实践：

```go
func (f *FeishuChannel) BuildUserID(msg *GatewayMessage) string {
    return "feishu:" + msg.PlatformUserID
}

func (f *FeishuChannel) BuildSessionID(msg *GatewayMessage) string {
    return "feishu-" + msg.ConversationID
}
```

**重要**：使用 `:` 分隔符可防止 platform userID 和 sessionID 意外碰撞。

### SendReply — 回复发送

两种模式：

1. **简单回复**（无流式）：收集所有 event，拼接成一条消息发送。
2. **流式回复**：监听 `<agent, text, content>` event，逐步更新消息。
   使用 `ResponseURL` 直接调用平台流式 API（不经过 Gateway）。

参考 `feishu/sender.go` 和 `wecom/sender.go` 的流式实现。

## 消息处理流水线

完整流水线有 9 个步骤，每个步骤的职责和失败处理如下：

| 步骤 | 方法/组件 | 失败处理 |
|------|-----------|----------|
| 1. Verify | `ch.VerifyRequest()` | 403 Forbidden |
| 2. Platform Event | `parsePlatformEvent()` + `ch.HandlePlatformEvent()` | 500 + 返回错误 |
| 3. Parse | `ch.ParseMessage()` | 400 Bad Request（nil 则静默 200） |
| 4. Dedup | `gs.dedup.IsDuplicate()` | 200 OK（静默丢弃） |
| 5. Identity | `ch.BuildUserID()` / `ch.BuildSessionID()` | 不可失败（纯字符串拼接） |
| 6. Rate Limit | `gs.ratelimit.Allow()` | 429 Too Many Requests |
| 7. Session | `gs.sessStore.GetOrCreateSession()` | 非致命，记录日志继续 |
| 8. Agent | `gs.coreLoop.Run()` | 200 OK + 记录错误（不重试） |
| 9. Reply | `ch.SendReply()` | 记录日志（平台已返回 200） |

**设计原则**：
- Gateway 只负责"路由 + 保护"，业务逻辑全在 Channel 实现中。
- 平台回调始终返回 200（Step 8 失败也不例外），防止平台无限重试。
- 去重和限流为可选层，当 `MessageID` 为空或限流未配置时自动旁路。
- Concurrency 控制使用 semaphore（条件变量），agent 完成后立即释放 slot。

## 现有 Channel 实现

| 文件 | 行数 | 平台 | 特征 |
|------|------|------|------|
| `feishu/channel.go` | ~280 | 飞书 | JSON 回调 + HMAC-SHA256 + 流式卡片 |
| `feishu/crypto.go` | ~90 | — | HMAC-SHA256 + SHA1 token 校验 |
| `feishu/sender.go` | ~380 | — | REST API + 流式卡片更新 |
| `feishu/message.go` | ~260 | — | 消息解析 + 内容提取 |
| `feishu/token.go` | ~100 | — | tenant_access_token 缓存 |
| `wecom/channel.go` | ~270 | 企业微信 | AI Bot JSON / 企业应用 XML 双模式 |
| `wecom/crypto.go` | ~270 | — | SHA1 签名 + AES-256-CBC + PKCS7 |
| `wecom/sender.go` | ~280 | — | 被动回复 + 流式 aibot/stream API |
| `wecom/message.go` | ~200 | — | JSON / XML 双格式解析 |
| `wecom/token.go` | ~120 | — | access_token 缓存 + 自动刷新 |

### 共享模块

| 文件 | 说明 |
|------|------|
| `types.go` | `Channel` 接口 、`GatewayMessage`、`PlatformEvent` |
| `gateway.go` | `GatewayServer` — HTTP 服务器 + 流水线编排 |
| `router.go` | `ChannelRouter` — 路径路由 + Channel 管理 |
| `middleware.go` | 日志 (withRequestLogging) + Panic 恢复 (withRecovery) |
| `dedup.go` | `MessageDeduplicator` — TTL 消息去重 |
| `ratelimit.go` | `RateLimiter` — 滑动窗口限流 + 并发控制 |
| `session.go` | `GatewaySessionStore` — 平台会话持久化 |

## 配置参考

```yaml
gateway:
  enabled: true
  address: ":9093"
  default_timeout: "120s"
  max_concurrent_sessions: 100
  message_dedup_ttl: "5m"
  rate_limit_per_user: 10       # 每用户每窗口最大请求数
  rate_limit_window: "10s"      # 限流滑动窗口

  feishu:
    enabled: true
    app_id: "cli_xxx"
    app_secret: "${FEISHU_APP_SECRET}"
    encrypt_key: "${FEISHU_ENCRYPT_KEY}"
    verification_token: "${FEISHU_VERIFICATION_TOKEN}"
    stream_card_enabled: true
    stream_card_update_interval: "500ms"
    max_message_length: 4096
    enable_file_receive: false

  wecom:
    enabled: true
    corpid: "wwxxxx"
    secret: "${WECOM_SECRET}"
    token: "${WECOM_TOKEN}"
    encoding_aes_key: "${WECOM_ENCODING_AES_KEY}"
    stream_enabled: true
    stream_update_interval: "1s"
    max_message_length: 2048
    enable_card_reply: true
```

## 监控与运维

### Metrics 端点

```bash
curl http://localhost:9093/metrics
```

返回示例：

```json
{
  "status": "ok",
  "dedup_size": 42,
  "rate_limiter": {
    "active_users": 3,
    "concurrent": 2,
    "max_concurrent": 100
  },
  "channels": [
    {"name": "feishu", "path": "/feishu"},
    {"name": "wecom", "path": "/wecom"}
  ]
}
```

### 关键日志

| 日志模式 | 级别 | 含义 |
|----------|------|------|
| `gateway: duplicate message dropped` | DEBUG | 消息去重命中 |
| `gateway: rate limit exceeded` | WARN | 用户触发限流 |
| `gateway: dedup eviction` | DEBUG | 定期 TTL 清理 |
| `gateway: processing message` | INFO | 消息进入 Agent 循环 |
| `gateway: agent run failed` | ERROR | Agent 执行异常 |
| `gateway: send reply failed` | ERROR | 回复发送失败 |
| `gateway: panic recovered` | ERROR | Channel handler panic |

### 健康检查

```bash
# 检查进程是否存活
curl -o /dev/null -s -w "%{http_code}" http://localhost:9093/health
```

> 健康检查端点由平台 Channel 自行实现（如 Feishu 需校验 token 签名），
> Gateway 本身不提供全局 health endpoint。

### 常见问题

**Q: 消息去重为什么使用内存而不是数据库？**

IM 回调的重试间隔通常在秒级（1-5s），使用内存 map + TTL 是最简单高效的方式。
5 分钟的 TTL 足够覆盖所有正常重试窗口，重启丢失去重缓存不会导致严重问题
（最多重试回调被当作新消息处理一次）。

**Q: 限流被触发后会发生什么？**

返回 HTTP 429 给平台。对于 WeCom，平台会记录失败并可能降级处理。
对于 Feishu，平台不会自动重试 429 响应。
建议配置合理的 `rate_limit_per_user`（默认 10/10s）以平衡安全和可用性。

**Q: 如何为不同平台设置不同的限流策略？**

当前限流器按 `platform:userID` 聚合，所有平台共用 `rate_limit_per_user` 和
`rate_limit_window`。如需差异化策略，可修改 `RateLimiter` 支持 per-channel config。

**Q: 并发控制 (max_concurrent_sessions) 如何工作？**

使用条件变量 (sync.Cond) 实现的 semaphore。当并发数达到上限时，
新请求会阻塞等待直到有 slot 释放。不会返回错误，保证有序执行。
