# Gateway — Multi-Platform Channel Development Guide

`internal/gateway/` is Wukong's messaging gateway. It is
**transport-agnostic**: the gateway itself owns no listener — each
registered Channel owns its inbound transport (e.g. a Feishu
WebSocket long-connection that dials out to the platform) and pushes
normalized messages into a shared processing pipeline that runs the
agent and dispatches the reply.

## 目录

- [架构概览](#架构概览)
- [快速开始：添加新平台](#快速开始添加新平台)
- [Channel 接口详解](#channel-接口详解)
- [消息处理流水线](#消息处理流水线)
- [现有 Channel 实现](#现有-channel-实现)
- [配置参考](#配置参考)

## 架构概览

```
Wukong (gateway)                       Platform
    │                                      │
    │  ──── wss:// dial (outbound) ──────► │  (Feishu long-conn gateway)
    │                                      │
    │  ◄── im.message.receive_v1 push ──── │
    │                                      │
    ▼                                      │
┌────────────────────────────────────────────┐
│ Channel (e.g. FeishuChannel)               │
│   · owns the inbound transport             │
│   · parses platform event → GatewayMessage │
│   · calls gateway dispatch handler         │
└──────────────────┬─────────────────────────┘
                   │ GatewayMessage
                   ▼
┌──────────────────────────────────────────────────────────┐
│ GatewayServer (orchestrator, no listener)                │
│   dispatch(ctx, msg):                                    │
│     1. dedup   (drop if MessageID seen within TTL)       │
│     2. BuildUserID / BuildSessionID                      │
│     3. rate limit  (per-user window + concurrency gate)  │
│     4. session mapping persistence                       │
│     5. go processMessage()  ← async, detached ctx        │
│            coreLoop.Run(...) → Channel.SendReply(...)    │
└──────────────────────────────────────────────────────────┘
                   │
                   ▼  (reply path uses platform HTTP API,
                      e.g. FeishuSender via tenant_access_token)
```

Key consequence: **no public callback URL, domain, or HTTPS ingress
is required**. The connection direction is reversed versus the old
HTTP-webhook design — Wukong dials out, so it runs from a local or
intranet host.

## 快速开始：添加新平台

1. Implement the `Channel` interface (see below) in a new subpackage,
   e.g. `internal/gateway/<platform>/`. Establish whatever transport the
   platform requires inside `Start`.
2. Register it during bootstrap in `internal/cli/session.go`:
   ```go
   ch := yourplatform.NewChannel(wukongCfg)
   if err := ch.Validate(); err != nil { /* fail fast */ }
   state.GatewayServer.RegisterChannel(ch)
   ```
3. Add platform config under `gateway` in `config.yaml` and the
   matching struct in `internal/gateway/config.go`.

## Channel 接口详解

```go
type MessageHandler func(ctx context.Context, msg *GatewayMessage)

type Channel interface {
    Name() string
    Start(ctx context.Context, handle MessageHandler) error
    Stop(ctx context.Context) error
    BuildUserID(msg *GatewayMessage) string
    BuildSessionID(msg *GatewayMessage) string
    SendReply(ctx context.Context, msg *GatewayMessage,
        events <-chan *event.Event) error
}
```

- **`Start`** establishes the platform connection and blocks until
  `ctx` is cancelled or a fatal error occurs. For each inbound message
  it must invoke `handle` with a populated `*GatewayMessage`. The
  platform SDK is expected to handle reconnection/heartbeat. `handle`
  is the gateway dispatch pipeline and runs the agent asynchronously,
  so it is safe to call `handle` synchronously.
- **`Stop`** releases channel-owned resources (the inbound connection
  is torn down when `Start`'s context is cancelled by the gateway).
- **`SendReply`** consumes the agent event stream and sends the reply
  via whatever outbound API the platform provides. This is independent
  of the inbound transport.

`GatewayMessage` is a purely internal, non-serialized type (`json:"-"`
on every field) that carries the normalized message between a Channel
and the gateway pipeline.

## 消息处理流水线

The shared pipeline lives in `GatewayServer.dispatch`
(`internal/gateway/gateway.go`):

1. **Dedup** — same `(platform, MessageID)` within `message_dedup_ttl`
   is dropped (the SDK may re-deliver after transient errors).
2. **Build IDs** — `BuildUserID` / `BuildSessionID` isolate Wukong
   users/sessions per platform identity.
3. **Rate limit** — per-user sliding window + global concurrency gate
   (`max_concurrent_sessions`). Over-limit messages are dropped (the
   user may resend).
4. **Session mapping** — persists platform-identity ↔ Wukong-identity
   in SQLite (`gateway_sessions`); degrades to in-memory with no pool.
5. **Async agent run** — `processMessage` runs `coreLoop.Run` in a
   background goroutine with a context derived from
   `context.Background()` (NOT the inbound ctx), so a slow agent never
   blocks the channel's event loop. The concurrency slot is released
   via `defer` (panic-safe).

## 现有 Channel 实现

### Feishu (`internal/gateway/feishu/`)

Receives messages over a WebSocket long-connection using the Lark SDK
(`larksuite/oapi-sdk-go/v3` `ws` subpackage). Replies use the Lark
Open API via `FeishuSender` (`Im.V1.Message.Create` + periodic
`Message.Patch` for a streaming card experience), authenticated with a
`tenant_access_token` managed automatically by the SDK.

- `channel.go` — `Start` builds the event dispatcher
  (`dispatcher.NewEventDispatcher` + `OnP2MessageReceiveV1`) and runs
  `wsClient.Start` in a goroutine. `Validate` fail-fast checks
  `app_id`/`app_secret`.
- `message.go` — `parseP2MessageReceiveV1` maps the typed SDK event to
  `GatewayMessage`; strips `@_user_N` mention placeholders.
- `sender.go` — streaming card + text reply (unchanged from the
  webhook era; transport-independent).

> **Feishu console prerequisite:** set the event subscription mode to
> "使用长连接接收事件/回调" and subscribe to `im.message.receive_v1`.

### WeCom

Removed. The official WeCom WebSocket long-connection has no Go SDK,
and the pure-WebSocket architecture (decided for this refactor) cannot
accommodate it. To restore it, implement a new `Channel` that brings
its own inbound transport.

## 配置参考

```yaml
gateway:
  enabled: true
  default_timeout: "120s"
  max_concurrent_sessions: 100
  message_dedup_ttl: "5m"
  rate_limit_per_user: 20
  rate_limit_window: "60s"

  feishu:
    enabled: true
    app_id: "cli_xxx"
    app_secret: "${FEISHU_APP_SECRET}"
    api_base: "https://open.feishu.cn/open-apis"
    encrypt_key: "${FEISHU_ENCRYPT_KEY}"            # only if encryption is enabled
    verification_token: "${FEISHU_VERIFICATION_TOKEN}"  # deprecated in long-conn mode
    stream_card_enabled: true
    stream_card_update_interval: "500ms"
    max_message_length: 4096
    enable_file_receive: true
```

There is no `gateway.address` — the gateway has no listener.

## 限制

- **Single-instance push (Feishu):** in long-connection mode the
  platform delivers each event to only one connected instance (no
  broadcast). Dedup is in-memory per-instance, so multi-instance
  deployments need a shared store (e.g. Redis) to avoid duplicate
  processing.
