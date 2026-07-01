# Wukong Gateway & Channel 架构设计方案

> **版本**: v1.0 | **日期**: 2026-07-01 | **状态**: 设计提案

---

## 目录

1. [动机与目标](#1-动机与目标)
2. [现有架构分析](#2-现有架构分析)
3. [目标平台分析](#3-目标平台分析)
4. [总体架构设计](#4-总体架构设计)
5. [核心接口定义](#5-核心接口定义)
6. [Channel 实现方案](#6-channel-实现方案)
7. [配置设计](#7-配置设计)
8. [实现任务分解](#9-实现任务分解)
9. [风险与建议](#10-风险与建议)

---

## 1. 动机与目标

### 1.1 问题陈述

Wukong 目前已支持 4 种协议端点（A2A、ACP、AG-UI、ACP MCP），但这些端点面向的都是开发者或 Agent-to-Agent 场景。要让 Wukong 真正成为企业级 AI 助手，必须接入飞书、企业微信等日常办公协作平台。

### 1.2 目标

| 目标 | 描述 |
|------|------|
| **多平台接入** | 支持飞书（Lark）、企业微信（WeCom）、钉钉、Slack 等主流 IM 平台 |
| **插件化扩展** | 新增一个平台只需实现标准 Channel 接口，无需修改核心代码 |
| **会话复用** | 复用现有 Session/Memory/Recall 机制，保持上下文连续性 |
| **流式响应** | 支持飞书/企微的流式消息/AI 流式卡片 |
| **安全可控** | 复用现有 Security 框架，对来自外部平台的消息进行安全审查 |

---

## 2. 现有架构分析

### 2.1 已有代码资产

```
internal/
├── server/
│   ├── agui.go      # AG-UI SSE 服务器 (:8080/agui)
│   └── acp.go       # ACP 协议服务器 (:9091/acp)
├── cli/
│   ├── run.go       # 单次执行 / 对话模式（共享 bootstrap）
│   ├── server.go    # 无头服务器启动（启动 4 个协议端点）
│   └── session.go   # TUI 模式
├── agent/
│   ├── loop.go      # CoreLoop — 核心 Agent 循环
│   └── workflow.go  # 多模式编排
├── summon/
│   └── a2a.go       # A2A 服务器（:9090）
├── config/
│   ├── config.go    # 配置加载器
│   └── types.go     # 全部配置类型定义
└── session/
    └── store.go     # 会话存储（SQLite/Redis）
```

### 2.2 关键接口

```go
// CoreLoop.Run — 所有入口最终都调用这个方法
func (l *CoreLoop) Run(ctx context.Context, userID string, sessionID string,
    message model.Message) (<-chan *event.Event, error)

// CoreLoop.RunStream — 带回调的流式版本
func (l *CoreLoop) RunStream(ctx context.Context, userID string, sessionID string,
    message model.Message, onEvent func(evt *event.Event) error) (string, error)
```

### 2.3 数据流

```
用户输入 → CoreLoop.Run() → Runner → LLMAgent → LLM API
                                        ↓
                                   Tools (shell/browser/...)
                                        ↓
                            event.Event channel → SSE/ACP/AGUI 响应
```

### 2.4 现有可复用资产

| 资产 | 说明 |
|------|------|
| `BootstrapState` | 统一管理所有服务器启动/关闭生命周期 |
| `Session Service` | 按 (appName, userID, sessionID) 管理会话 |
| `Memory Service` | 跨会话记忆持久化 |
| `Security Guard` | 工具执行权限控制、命令拦截 |
| `Event Channel` | 流式事件管道，天然支持 SSE |

---

## 3. 目标平台分析

### 3.1 飞书（Lark）

| 特性 | 详情 |
|------|------|
| **接入方式** | 企业自建应用 → 添加机器人能力 |
| **消息接收** | Event Subscription 回调（HTTP POST），含签名验证 |
| **消息回复** | 被动回复（响应回调）+ 主动回复（response_url / 飞书卡片消息 API） |
| **流式支持** | AI 流式卡片（streaming card），建议使用 |
| **验证机制** | URL 挑战 + HMAC-SHA256 签名 |
| **认证方式** | tenant_access_token → API 调用 |
| **消息类型** | 文本、富文本、卡片、图片、文件等 |

**数据流**：
```
用户@机器人 → 飞书服务器 → POST /gateway/feishu/callback → Wukong
Wukong → response_url POST / 创建流式卡片 → 飞书 → 用户
```

### 3.2 企业微信（WeCom）

| 特性 | 详情 |
|------|------|
| **接入方式** | 智能机器人（AI Bot） |
| **消息接收** | 消息回调（aibot_msg_callback）+ 长连接（推荐） |
| **消息回复** | 被动回复 + 主动回复（response_url） |
| **流式支持** | 流式消息（stream_id + finish 标记） |
| **ID 转换** | 事件 ID → 明文内容 ID 转换（stream 化前必需） |
| **验证机制** | URL 验证（echostr）+ AES 加解密 |
| **用户标识** | 企微 userid，需要关联 Wukong 的 userID |

**数据流**：
```
用户@机器人 → 企微服务器 → POST /gateway/wecom/callback → Wukong
Wukong → response_url POST 流式消息 → 企微 → 用户
```

### 3.3 统一抽象

虽然各平台细节不同，但核心流程高度一致：

```
1. 接收来自平台的 HTTP POST 请求
2. 验证请求签名/合法性
3. 解析用户消息内容
4. 建立 userID（platform:userid 映射）
5. 调用 CoreLoop.Run() 获取事件流
6. 将事件流转换为平台特定的消息格式
7. 通过 response_url / API 发送回复
```

---

## 4. 总体架构设计

### 4.1 架构全景图

```
┌─────────────────────────────────────────────────────────────────┐
│                        Wukong Server                              │
│                                                                   │
│  ┌───────────┐ ┌───────────┐ ┌───────────┐ ┌───────────┐        │
│  │A2A :9090  │ │ACP :9091  │ │AGUI :8080 │ │MCP :3400  │        │
│  └───────────┘ └───────────┘ └───────────┘ └───────────┘        │
│                                                                   │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                   Gateway :9093                           │    │
│  │  ┌─────────────────────────────────────────────────┐    │    │
│  │  │              GatewayServer                        │    │    │
│  │  │  - 路由分发 (/feishu/callback, /wecom/callback)  │    │    │
│  │  │  - 签名验证中间件                                  │    │    │
│  │  │  - 请求日志 & 限流                                 │    │    │
│  │  │  - 统一错误处理                                    │    │    │
│  │  └─────────────────────┬───────────────────────────┘    │    │
│  │                        │                                 │    │
│  │  ┌──────────┐  ┌───────┴──────┐  ┌──────────┐         │    │
│  │  │ Feishu   │  │ WeCom Channel │  │ (Slack)  │         │    │
│  │  │ Channel  │  │               │  │ Channel  │         │    │
│  │  └────┬─────┘  └───────┬───────┘  └────┬─────┘         │    │
│  │       │                │                │                │    │
│  │       └────────────────┼────────────────┘                │    │
│  │                        │                                  │    │
│  │               ┌────────┴────────┐                       │    │
│  │               │  ChannelRouter  │                       │    │
│  │               │ (接口注册/发现) │                       │    │
│  │               └────────┬────────┘                       │    │
│  └────────────────────────┼─────────────────────────────────┘    │
│                           ▼                                        │
│                  ┌─────────────────┐                               │
│                  │    CoreLoop     │                               │
│                  │  (Agent Runner) │                               │
│                  └─────────────────┘                               │
└─────────────────────────────────────────────────────────────────┘
```

### 4.2 关键设计原则

1. **Channel 是插件**：每个平台对应一个 Channel 实现，通过 `ChannelRouter` 管理生命周期
2. **不依赖外部 Go SDK**：使用标准 `crypto/hmac`、`crypto/sha256`、`crypto/aes`、`net/http` 自实现加密验证
3. **统一消息模型**：所有 Channel 将平台消息转换为 `GatewayMessage`，再转换为 `model.Message`
4. **会话映射**：`platformID` → `wukong userID/sessionID`，存储在 SQLite mapping 表
5. **容错设计**：Channel 崩溃不影响其他 Channel 和核心服务

### 4.3 目录结构（新增）

```
internal/
├── gateway/               # 新增：Gateway 核心
│   ├── gateway.go         # GatewayServer — HTTP 入口服务器
│   ├── router.go          # ChannelRouter — Channel 注册/发现/路由
│   ├── types.go           # 统一消息类型定义
│   ├── session.go         # Platform ↔ Wukong Session 映射
│   ├── middleware.go      # 签名验证、限流、日志中间件
│   │
│   ├── feishu/            # 飞书 Channel
│   │   ├── channel.go     # FeishuChannel 实现
│   │   ├── crypto.go      # 签名验证、token 管理
│   │   ├── message.go     # 飞书消息 ↔ GatewayMessage 转换
│   │   └── sender.go      # 消息回复（被动/主动/流式卡片）
│   │
│   ├── wecom/             # 企业微信 Channel
│   │   ├── channel.go     # WeComChannel 实现
│   │   ├── crypto.go      # 消息加解密（AES）、echostr 验证
│   │   ├── message.go     # 企微消息 ↔ GatewayMessage 转换
│   │   └── sender.go      # 消息回复（被动/主动/流式）
│   │
│   └── README.md          # 如何添加新 Channel 的指南
│
config.yaml                # 新增 gateway 配置段
```

---

## 5. 核心接口定义

### 5.1 Channel 接口

```go
// Channel 是所有消息平台适配器的标准接口。
// 新增平台只需实现此接口并注册到 ChannelRouter。
type Channel interface {
    // Name 返回 Channel 唯一标识（如 "feishu", "wecom"）。
    Name() string

    // RoutePath 返回该 Channel 的 HTTP 回调路径前缀。
    // 例如 "/feishu" 会注册 "/feishu/callback" 路由。
    RoutePath() string

    // VerifyRequest 验证来自平台的 HTTP 请求合法性。
    // 包括签名校验、timestamp/nonce 防重放。
    // 返回验证后的原始 body 或错误。
    VerifyRequest(r *http.Request) (body []byte, err error)

    // ParseMessage 将平台原始消息解析为统一的 GatewayMessage。
    ParseMessage(body []byte) (*GatewayMessage, error)

    // BuildUserID 从平台消息中提取或构建 Wukong 的用户标识。
    // 典型实现：platformName + ":" + platformUserID。
    BuildUserID(msg *GatewayMessage) string

    // BuildSessionID 为平台对话构建 Wukong 的 session 标识。
    // 建议格式：platformName + "-" + conversationID。
    BuildSessionID(msg *GatewayMessage) string

    // SendReply 将 Agent 响应发送回平台。
    // ctx 用于超时控制，msg 是原始请求，events 是 Agent 事件流。
    SendReply(ctx context.Context, msg *GatewayMessage,
        events <-chan *event.Event) error
}
```

### 5.2 统一消息类型

```go
// GatewayMessage 是所有平台消息的统一内部表示。
type GatewayMessage struct {
    // Platform 是平台标识（"feishu", "wecom"）。
    Platform string

    // PlatformUserID 是平台侧的用户 ID
    //（飞书 open_id、企微 userid）。
    PlatformUserID string

    // ConversationID 是会话/群聊 ID
    //（飞书 chat_id、企微 chatid/aibotid）。
    ConversationID string

    // Content 是用户发送的纯文本内容。
    Content string

    // ContentType 是消息类型（text, image, file, mixed）。
    ContentType string

    // ResponseURL 是平台提供的主动回复地址（可选）。
    // 用于流式消息回复。
    ResponseURL string

    // MessageID 是平台侧的消息唯一 ID（用于去重）。
    MessageID string

    // Timestamp 是消息时间戳。
    Timestamp int64

    // RawData 保存平台原始数据（Channel 内部使用）。
    RawData json.RawMessage

    // Metadata 是平台相关元数据（如 tenant_key）。
    Metadata map[string]string
}

// PlatformEvent 表示平台特殊事件（非消息）。
type PlatformEvent struct {
    Platform string
    Type     string // "url_verify", "card_action", "app_open"
    Data     json.RawMessage
    Metadata map[string]string
}
```

### 5.3 ChannelRouter

```go
// ChannelRouter 管理所有 Channel 的生命周期和路由。
type ChannelRouter struct {
    mu       sync.RWMutex
    channels map[string]Channel        // name → Channel
    paths    map[string]Channel        // path → Channel
    coreLoop *agent.CoreLoop
    session  *GatewaySessionStore
    cfg      *config.WukongConfig
}

// Register 注册一个 Channel。
func (r *ChannelRouter) Register(ch Channel) error
// Unregister 注销一个 Channel。
func (r *ChannelRouter) Unregister(name string) error
// Route 根据路径匹配 Channel。
func (r *ChannelRouter) Route(path string) (Channel, error)
// List 列出所有已注册的 Channel。
func (r *ChannelRouter) List() []ChannelInfo
// Handler 返回可供挂载的 http.Handler。
func (r *ChannelRouter) Handler() http.Handler
```

### 5.4 GatewayServer

```go
// GatewayServer 是 Gateway 的 HTTP 服务器入口。
// 负责启动 HTTP 服务器、挂载中间件、注册 Channel 路由。
type GatewayServer struct {
    mu      sync.RWMutex
    router  *ChannelRouter
    server  *http.Server
    address string
    running bool
}

// 简化启动流程
func NewGatewayServer(cfg *config.WukongConfig, loop *agent.CoreLoop,
    store *GatewaySessionStore) (*GatewayServer, error)
func (gs *GatewayServer) Start(addr string) error
func (gs *GatewayServer) Stop(ctx context.Context) error
```

---

## 6. Channel 实现方案

### 6.1 飞书 Channel（feishu）

#### 6.1.1 验证流程

```
POST /gateway/feishu/callback
     ↓
1. URL Challenge 检测
   { "type": "url_verification", "challenge": "xxx" }
   → 直接返回 { "challenge": "xxx" }
     ↓
2. Event Callback 签名验证
   计算: HMAC-SHA256(timestamp + nonce + encrypt_key, body)
   比对: 请求头 X-Lark-Signature
     ↓
3. 解密事件体（如果加密）
   使用应用的 Encrypt Key 进行 AES-256-CBC 解密
     ↓
4. 解析事件类型 → 分发处理
```

#### 6.1.2 消息回复

```go
// 方式一：被动回复（简单场景，直接返回 JSON）
func (fc *FeishuChannel) sendPassiveReply(w http.ResponseWriter, content string)

// 方式二：流式卡片回复（推荐，支持实时展示）
func (fc *FeishuChannel) sendStreamCard(ctx context.Context,
    msg *GatewayMessage, events <-chan *event.Event) error {
    // 1. 获取 tenant_access_token
    // 2. 创建流式消息卡片（streaming card）
    // 3. 逐 chunk 更新卡片内容
    // 4. 最终 finish 流式消息
}
```

#### 6.1.3 配置项

```yaml
gateway:
  feishu:
    enabled: true
    app_id: "cli_xxxxxxxx"              # 应用 App ID
    app_secret: "${FEISHU_APP_SECRET}"  # 应用 Secret
    encrypt_key: "${FEISHU_ENCRYPT_KEY}" # 事件加密 Key
    verification_token: "${FEISHU_VERIFICATION_TOKEN}" # 验证 Token
    stream_card_enabled: true           # 启用流式卡片回复
    stream_card_update_interval: "500ms" # 流式卡片更新间隔
```

### 6.2 企业微信 Channel（wecom）

#### 6.2.1 验证流程

```
GET /gateway/wecom/callback?msg_signature=xxx&timestamp=xxx&nonce=xxx&echostr=xxx
     ↓
1. URL 验证
   - 使用 Token + EncodingAESKey 解密 echostr
   - 返回解密后的明文（企微验证 URL 有效性）
     ↓
POST /gateway/wecom/callback
     ↓
2. 消息回调解密
   - 验证 msg_signature（SHA1）
   - AES-256-CBC 解密 Encrypt 字段
   - 去除 PKCS7 padding 和 random bytes
   - 提取 XML 消息体 → 解析为结构体
     ↓
3. 消息分发
   - text 类型 → 文本回复
   - event 类型 → 事件处理
```

#### 6.2.2 消息回复

```go
// 方式一：被动回复（响应回调 POST，内嵌 XML）
func (wc *WeComChannel) sendPassiveReply(w http.ResponseWriter,
    toUser string, content string) error

// 方式二：流式消息回复（推荐）
func (wc *WeComChannel) sendStreamReply(ctx context.Context,
    msg *GatewayMessage, events <-chan *event.Event) error {
    // 1. 调用 /cgi-bin/aibot/stream/send 创建 stream
    // 2. 逐 chunk 调用 /cgi-bin/aibot/stream/update
    // 3. 调用 /cgi-bin/aibot/stream/finish 结束流式
    // 注意：流式消息的 content 需要先做 ID 转换
}
```

#### 6.2.3 Token 管理

```go
// TokenManager 缓存 access_token（7200s 有效）
type TokenManager struct {
    mu      sync.RWMutex
    token   string
    expires time.Time
    corpid  string
    secret  string
}
func (tm *TokenManager) GetAccessToken(ctx context.Context) (string, error)
```

#### 6.2.4 配置项

```yaml
gateway:
  wecom:
    enabled: true
    corpid: "wwxxxxxxxxxxxx"                          # 企业 ID
    secret: "${WECOM_SECRET}"                         # 应用 Secret
    token: "${WECOM_TOKEN}"                           # 回调 Token
    encoding_aes_key: "${WECOM_ENCODING_AES_KEY}"     # EncodingAESKey
    stream_enabled: true                              # 启用流式消息
    stream_update_interval: "1s"                      # 流式更新间隔
    message_dedup_enabled: true                       # 启用消息去重
    message_dedup_ttl: "5m"                           # 去重窗口
```

---

## 7. 配置设计

### 7.1 config.yaml 新增段

> **注意**: config.yaml 已重组为 A-O 15 个逻辑分组，Gateway 配置归属到 **J 组：服务端点**。

```yaml
# ---------------------------------------------------------------------------
# J5. Gateway — 多平台消息通道（飞书、企微、Slack 等）
# ---------------------------------------------------------------------------
gateway:
  # 全局 Gateway 设置
  enabled: true                       # 启用 Gateway 功能
  address: ":9093"                    # Gateway HTTP 监听地址

  # 默认行为
  default_timeout: "120s"             # Agent 单次回复超时
  max_concurrent_sessions: 100        # 最大并发会话数
  message_dedup_ttl: "5m"            # 消息去重窗口

  # ==================== 飞书 ====================
  feishu:
    enabled: false
    app_id: ""
    app_secret: "${FEISHU_APP_SECRET}"
    encrypt_key: "${FEISHU_ENCRYPT_KEY}"
    verification_token: "${FEISHU_VERIFICATION_TOKEN}"
    # 流式卡片
    stream_card_enabled: true
    stream_card_update_interval: "500ms"
    # 消息处理
    max_message_length: 4096          # 飞书单条消息最大字符数
    enable_file_receive: false        # 是否接收文件消息

  # ==================== 企业微信 ====================
  wecom:
    enabled: false
    corpid: ""
    secret: "${WECOM_SECRET}"
    token: "${WECOM_TOKEN}"
    encoding_aes_key: "${WECOM_ENCODING_AES_KEY}"
    # 流式消息
    stream_enabled: true
    stream_update_interval: "1s"
    # 消息处理
    max_message_length: 2048          # 企微单条消息最大字符数
    enable_card_reply: true           # 是否使用模板卡片回复
```

### 7.2 配置类型定义（新增到 types.go）

```go
// GatewayConfig 定义多平台消息通道配置。
type GatewayConfig struct {
    Enabled                bool   `mapstructure:"enabled"`
    Address                string `mapstructure:"address"`
    DefaultTimeout         time.Duration `mapstructure:"default_timeout"`
    MaxConcurrentSessions  int    `mapstructure:"max_concurrent_sessions"`
    MessageDedupTTL        time.Duration `mapstructure:"message_dedup_ttl"`

    Feishu FeishuChannelConfig `mapstructure:"feishu"`
    WeCom  WeComChannelConfig  `mapstructure:"wecom"`
}

type FeishuChannelConfig struct {
    Enabled                    bool          `mapstructure:"enabled"`
    AppID                      string        `mapstructure:"app_id"`
    AppSecret                  string        `mapstructure:"app_secret"`
    EncryptKey                 string        `mapstructure:"encrypt_key"`
    VerificationToken          string        `mapstructure:"verification_token"`
    StreamCardEnabled          bool          `mapstructure:"stream_card_enabled"`
    StreamCardUpdateInterval   time.Duration `mapstructure:"stream_card_update_interval"`
    MaxMessageLength           int           `mapstructure:"max_message_length"`
    EnableFileReceive          bool          `mapstructure:"enable_file_receive"`
}

type WeComChannelConfig struct {
    Enabled                  bool          `mapstructure:"enabled"`
    CorpID                   string        `mapstructure:"corpid"`
    Secret                   string        `mapstructure:"secret"`
    Token                    string        `mapstructure:"token"`
    EncodingAESKey           string        `mapstructure:"encoding_aes_key"`
    StreamEnabled            bool          `mapstructure:"stream_enabled"`
    StreamUpdateInterval     time.Duration `mapstructure:"stream_update_interval"`
    MaxMessageLength         int           `mapstructure:"max_message_length"`
    EnableCardReply          bool          `mapstructure:"enable_card_reply"`
}
```

---

## 8. 实现任务分解

### Phase 1：基础框架（3-4 天）

| 任务 | 产出 | 说明 |
|------|------|------|
| 1.1 定义接口与类型 | `internal/gateway/types.go` | Channel 接口、GatewayMessage、PlatformEvent |
| 1.2 实现 ChannelRouter | `internal/gateway/router.go` | Channel 注册/注销/路由+并发安全 |
| 1.3 实现 GatewayServer | `internal/gateway/gateway.go` | HTTP 服务器+中间件（签名/限流/日志） |
| 1.4 实现 SessionStore | `internal/gateway/session.go` | 平台用户↔Wukong Session 映射（SQLite） |
| 1.5 集成 Bootstrap | 修改 `internal/cli/server.go` | 在 `BootstrapState` 加入 GatewayServer 生命周期 |
| 1.6 配置集成 | 修改 `internal/config/types.go` | 添加 GatewayConfig 及相关子类型 |

### Phase 2：飞书 Channel（3-4 天）

| 任务 | 产出 | 说明 |
|------|------|------|
| 2.1 URL 验证 & 签名 | `internal/gateway/feishu/crypto.go` | HMAC-SHA256 签名、token 缓存 |
| 2.2 消息解析 | `internal/gateway/feishu/message.go` | JSON 解析、text/image/card 消息处理 |
| 2.3 Channel 主体 | `internal/gateway/feishu/channel.go` | 实现 Channel 接口 |
| 2.4 消息发送 | `internal/gateway/feishu/sender.go` | 被动回复、API 主动推送、流式卡片 |
| 2.5 流式卡片 | `internal/gateway/feishu/stream.go` | 创建/更新/完成流式卡片 |
| 2.6 集成测试 | `internal/gateway/feishu/*_test.go` | Mock 飞书回调测试 |

### Phase 3：企业微信 Channel（3-4 天）

| 任务 | 产出 | 说明 |
|------|------|------|
| 3.1 URL 验证 & 加解密 | `internal/gateway/wecom/crypto.go` | echostr 解密、AES 加解密、SHA1 签名 |
| 3.2 消息解析 | `internal/gateway/wecom/message.go` | XML 解析、text/event 消息处理 |
| 3.3 Channel 主体 | `internal/gateway/wecom/channel.go` | 实现 Channel 接口 |
| 3.4 Token 管理 | `internal/gateway/wecom/token.go` | access_token 缓存和自动刷新 |
| 3.5 消息发送 | `internal/gateway/wecom/sender.go` | 被动回复 XML、API 主动推送、流式消息 |
| 3.6 流式消息 | `internal/gateway/wecom/stream.go` | 创建/追加/完成流式消息 + ID 转换 |
| 3.7 集成测试 | `internal/gateway/wecom/*_test.go` | Mock 企微回调测试 |

### Phase 4：增强与文档（2-3 天）

| 任务 | 产出 | 说明 |
|------|------|------|
| 4.1 消息去重 | `internal/gateway/dedup.go` | 基于 MessageID 的去重（内存+定期清理） |
| 4.2 错误友好化 | 各 Channel sender | 将 Agent 错误转为用户友好的 Channel 消息 |
| 4.3 限流保护 | `internal/gateway/middleware.go` | 按 platform+user 限流，防止滥用 |
| 4.4 集成文档 | `internal/gateway/README.md` | Channel 开发指南 |
| 4.5 部署文档 | `docs/GATEWAY_DEPLOY.md` | Nginx 反向代理、域名、SSL 配置 |

### 估算总工期：11-15 天

---

## 9. 风险与建议

### 9.1 技术风险

| 风险 | 级别 | 缓解措施 |
|------|------|----------|
| **平台 API 变更** | 中 | Channel 接口隔离变更，只影响单个 Channel |
| **流式消息 SDK 版本** | 中 | 不使用第三方 SDK，直接 HTTP 调用 |
| **大模型响应延迟** | 中 | 被动回复 3s 内先返回"思考中"，后续再流式更新 |
| **消息并发量大** | 低 | 复用现有 Session/Memory 的 SQLite WAL 并发能力 |
| **签名密钥泄露** | 低 | 环境变量注入，不写入配置文件 |

### 9.2 架构建议

1. **优先实现流式回复模式**：被动回复有 3-5 秒超时限制，Agent 回复通常超过此限制。流式卡片/消息是最佳实践。

2. **消息分片发送**：当 Agent 回复超过平台单条消息限制时，自动切分为多条消息。

3. **Channel 降级策略**：某个 Channel 故障不应影响其他 Channel 和核心 Agent 服务。

4. **监控指标**：
   - 各 Channel 的消息量、延迟、错误率
   - token 缓存命中率
   - 消息去重命中数
   - 流式消息完成率

5. **安全建议**：
   - 所有 Channel 消息都经过 `Security Guard` 工具权限控制
   - 外部平台用户默认使用 `chat_only` 权限，需要管理员提升
   - 支持按平台/用户配置工具白名单

6. **用户体验优化**：
   - Agent 正在思考时返回"正在处理..."消息
   - 长回复自动分页（"续上...", "第 2/3 页"）
   - 支持 `/reset` 命令清除会话上下文

---

## 附录 A：如何添加新 Channel

以 Slack 为例：

```
1. 创建 internal/gateway/slack/ 目录
2. 实现 Channel 接口的 6 个方法
3. 在 GatewayServer 初始化时注册：
   router.Register(slack.NewSlackChannel(cfg, loop, store))
4. 添加 slack 配置到 config.yaml 的 gateway 段
5. 编写集成测试
```

---

## 附录 B：Channel 接口最小实现骨架

```go
package slack

import (
    "context"
    "net/http"
    "trpc.group/trpc-go/trpc-agent-go/event"
    "github.com/km269/wukong/internal/gateway"
)

type SlackChannel struct {
    cfg  *config.WukongConfig
    loop *agent.CoreLoop
}

func NewSlackChannel(cfg *config.WukongConfig, loop *agent.CoreLoop,
) *SlackChannel {
    return &SlackChannel{cfg: cfg, loop: loop}
}

func (sc *SlackChannel) Name() string                  { return "slack" }
func (sc *SlackChannel) RoutePath() string              { return "/slack" }

func (sc *SlackChannel) VerifyRequest(r *http.Request) ([]byte, error) {
    // 实现 Slack Signing Secret 验证
    return nil, nil
}

func (sc *SlackChannel) ParseMessage(body []byte) (*gateway.GatewayMessage, error) {
    // 解析 Slack Event API 消息
    return nil, nil
}

func (sc *SlackChannel) BuildUserID(msg *gateway.GatewayMessage) string {
    return "slack:" + msg.PlatformUserID
}

func (sc *SlackChannel) BuildSessionID(msg *gateway.GatewayMessage) string {
    return "slack-" + msg.ConversationID
}

func (sc *SlackChannel) SendReply(ctx context.Context,
    msg *gateway.GatewayMessage, events <-chan *event.Event) error {
    // 1. 收集流式事件
    // 2. 通过 Slack Web API chat.postMessage 发送
    return nil
}
```
