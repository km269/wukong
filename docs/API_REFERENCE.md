# Wukong API 参考文档

> 本文档基于源码逐字核对，描述 Wukong 核心模块（Agent 循环、Provider 工厂、Context 管理、Recipe、Workflow 编排、Extension、Security、Cortex、Memory、Session、Health、ARD、Summon）的公开 Go 类型与方法签名，供二次开发与集成参考。**本文档描述的是 Go 接口/结构体，非 REST API。**

---

## 目录

1. [CoreLoop — Agent 核心循环](#1-coreloop--agent-核心循环)
2. [ContextManager — 上下文管理](#2-contextmanager--上下文管理)
3. [RecipeToolSet — Recipe 子 Agent](#3-recipetoolset--recipe-子-agent)
4. [WorkflowBuilder — 多模式编排](#4-workflowbuilder--多模式编排)
5. [Provider Factory — 模型工厂](#5-provider-factory--模型工厂)
6. [Extension Manager — 扩展管理](#6-extension-manager--扩展管理)
7. [Security Guard — 安全守卫](#7-security-guard--安全守卫)
8. [CortexStore — 混合检索](#8-cortexstore--混合检索)
9. [MemoryManager — 长期记忆](#9-memorymanager--长期记忆)
10. [Session Service — 会话存储](#10-session-service--会话存储)
11. [Health Registry — 健康检查](#11-health-registry--健康检查)
12. [ARD ToolSet — 资源发现](#12-ard-toolset--资源发现)
13. [SummonManager — 子 Agent 委派](#13-summonmanager--子-agent-委派)
14. [常见文档错误纠正](#14-常见文档错误纠正)

---

## 1. CoreLoop — Agent 核心循环

**源码**: `internal/agent/loop.go`

`CoreLoop` 是 Wukong 的主交互执行循环，编排 Runner、Session、Memory、Tool 等子系统，提供上下文注入、记忆召回、安全校验与事件流输出。

### CoreLoop 结构体（`loop.go:53-74`）

```go
type CoreLoop struct {
    agent          agent.Agent
    runner         runner.Runner
    sessionService session.Service
    memoryService  memory.Service
    factory        *provider.Factory
    cfg            *config.WukongConfig
    contextMgr     *ContextManager
    security       *security.Guard
    recallStore    *recall.Store
    cortexStore    *cortex.CortexStore   // optional: HNSW vector sync
    memoryFlow     *cortex.MemoryFlowService
    graphFlow      *cortex.GraphFlowService // optional: KG auto-extract
    closeFn        func() error

    mu     sync.RWMutex
    closed bool
    bgWg   sync.WaitGroup // 后台协程（graceful shutdown）
    runWg  sync.WaitGroup // RunStream 同步后置写入
}
```

### CoreLoopConfig — 构造依赖（`loop.go:77-124`）

```go
type CoreLoopConfig struct {
    Config          *config.WukongConfig
    Factory         *provider.Factory
    SessionService  session.Service
    MemoryService   memory.Service
    ArtifactService artifact.Service
    ToolSets        []tool.ToolSet
    FunctionTools   []tool.Tool
    SecurityGuard   *security.Guard
    RecallStore     *recall.Store
    CortexStore     *cortex.CortexStore
    RevisionModel   provider.RevisionModel
    MemoryFlowService *cortex.MemoryFlowService
    GraphFlowService  *cortex.GraphFlowService
    TopOfMindInstructions string
    TelemetryShutdown func(context.Context) error
    MemoryClose, EvolutionClose, DBPoolClose func() error
    WorkingDir, SessionID, UserID string
}
```

### 关键方法签名

```go
// 构造 CoreLoop。参数是 CoreLoopConfig 结构体，不是 *config.WukongConfig。
func NewCoreLoop(cfg CoreLoopConfig) (*CoreLoop, error)

// 执行单条用户消息，返回事件流 channel。
func (l *CoreLoop) Run(ctx context.Context, userID, sessionID string, message model.Message) (<-chan *event.Event, error)

// 流式处理事件，对每个事件回调 onEvent，返回最终响应文本。
func (l *CoreLoop) RunStream(ctx context.Context, userID, sessionID string, message model.Message, onEvent func(*event.Event) error) (string, error)

// 便捷方法：字符串输入 → RunStream(nil 回调) → 返回最终响应文本。
func (l *CoreLoop) RunUserMessage(ctx context.Context, userID, sessionID string, content string) (string, error)

// 关闭循环并释放资源（等待进行中的写入与后台协程）。
func (l *CoreLoop) Close() error
```

### 访问器方法

```go
func (l *CoreLoop) GetRunner() runner.Runner
func (l *CoreLoop) GetAgent() agent.Agent
func (l *CoreLoop) GetSessionService() session.Service
func (l *CoreLoop) GetSecurityGuard() *security.Guard
func (l *CoreLoop) GetContextManager() *ContextManager
```

### Run() 四重上下文注入顺序

1. **PrepareContext** — `contextMgr.PrepareContext` 执行令牌预算检查与异步摘要触发。
2. **MemoryFlow** — `IngestTurn` 记录用户消息 + `WakeUp` 注入 `[Context from past conversations]`。
3. **Recall/Cortex** — `Search` 检索历史并注入 `[Relevant conversation history]`。
4. **tRPC Memory** — `memoryService.ReadMemories` 注入 `[Remembered facts from previous conversations]`，与 WakeUp 上下文做 60% 重叠去重（`isMemoryDuplicated`，滑窗 30 字符）。

### RunStream() 后置写入顺序

- **同步**: 响应/工具调用/工具结果存入 recall 或 cortex；`MemoryFlow.IngestTurn` 记录 assistant 回合。
- **异步**（`bgWg`）: `MemoryFlow.PromoteFacts`（事实提升到 tRPC Memory）+ `GraphFlow.ExtractFromTranscript`/`BuildGraph`（受 `AutoExtract` 控制）。
- **最后**: `contextMgr.AfterRun(ctx, responseText, allEvents)`。

### Close() 关闭链

先 `waitWithTimeout` 等待 `runWg`/`bgWg`（各 5s），再执行 `closeFn` 七步链：

1. `runner.Close()`
2. `EvolutionClose()`
3. `MemoryClose()`
4. `SessionService.Close()`（若实现）
5. `GraphFlowService.Close()`
6. `TelemetryShutdown(ctx)`（10s 超时）
7. `DBPoolClose()`（**最后**执行，确保所有 WAL 写入已刷盘）

---

## 2. ContextManager — 上下文管理

**源码**: `internal/agent/context.go`

### ContextManager（`context.go:ContextManager`）

```go
type ContextManager struct {
    engine *ContextRevisionEngine
    cfg    *config.WukongConfig
}

func NewContextManager(cfg *config.WukongConfig) *ContextManager

func (m *ContextManager) PrepareContext(ctx context.Context, sessionKey session.Key) context.Context
func (m *ContextManager) AfterRun(ctx context.Context, response string, evts []event.Event)
func (m *ContextManager) ShouldSummarize() bool
func (m *ContextManager) GetEstimatedTokens() int
func (m *ContextManager) GetMessageCount() int
func (m *ContextManager) Reset()
func (m *ContextManager) GetEngine() *ContextRevisionEngine
func (m *ContextManager) SetSessionService(svc session.Service)
```

### ContextRevisionEngine（`context.go:28-49`）

```go
type ContextRevisionEngine struct {
    mu               sync.RWMutex
    cfg              *config.WukongConfig
    messageCount     int
    estimatedTokens  int
    lastSummarized   time.Time
    lastRevisionTime time.Time
    recentOutputs    []string // ring buffer
    maxRecent        int
    revisionModel    provider.RevisionModel
    sessionService   session.Service
}

func NewContextRevisionEngine(cfg *config.WukongConfig) *ContextRevisionEngine
func (e *ContextRevisionEngine) SetRevisionModel(m provider.RevisionModel)
func (e *ContextRevisionEngine) SetSessionService(svc session.Service)
func (e *ContextRevisionEngine) PrepareContext(ctx context.Context, sessionKey session.Key) context.Context
func (e *ContextRevisionEngine) AfterRun(ctx context.Context, response string, evts []event.Event)
func (e *ContextRevisionEngine) SummarizeContent(ctx context.Context, content string) (string, error)
func (e *ContextRevisionEngine) TruncateCommandOutput(output string) string
func (e *ContextRevisionEngine) ShouldSummarize() bool
func (e *ContextRevisionEngine) GetEstimatedTokens() int
func (e *ContextRevisionEngine) GetMessageCount() int
func (e *ContextRevisionEngine) Reset()
```

### 令牌预算与触发条件

- `maxTokens` 默认 `64000`（当 `cfg.Revision.MaxContextTokens <= 0` 时）。
- 实际截断阈值取 `min(max_context_tokens, EffectiveContextWindowForDefault())`。
- 触发摘要/压缩条件（满足任一）：
  - `estimatedTokens > maxTokens × (1 - TrimRatio)`
  - `messageCount > 100`
  - 距上次摘要超过 `5min`
- `AfterRun` 令牌累加：优先用 `evt.Response.Usage.TotalTokens`；不可用时回退到 `len(response)/4`。

---

## 3. RecipeToolSet — Recipe 子 Agent

**源码**: `internal/agent/recipe.go` · `internal/agent/recipe_tool.go`

Recipe 系统从 `.wukong/recipes/*.yaml` 加载子 Agent 定义，注册为 `recipe-<name>` 工具供主 Agent 调用。

### RecipeToolSet（`recipe.go:178-191`）

```go
type RecipeToolSet struct {
    mu        sync.Mutex
    tools     []tool.Tool
    subAgents []agent.Agent
    reloader  *hotReloader        // P3-D: fsnotify 热重载
    factory   providerModelFactory
    agentCfg  *config.AgentConfig
    allToolsFn func() []tool.Tool
}

// 扫描 recipe 目录，加载所有 .yaml 定义。RecipeEnabled=false 时返回 nil。
func NewRecipeToolSet(factory *provider.Factory, agentCfg *config.AgentConfig, allTools []tool.Tool) *RecipeToolSet

// 返回已注册的 recipe 工具列表。
func (ts *RecipeToolSet) Tools() []tool.Tool

// 从磁盘重建所有 recipe 工具（热重载）。线程安全，返回是否成功。
func (ts *RecipeToolSet) Reload() bool
```

### RecipeConfig（`recipe.go:107-166`，YAML tag）

```go
type RecipeConfig struct {
    Name        string                 `yaml:"name"`         // 唯一标识，工具名前缀 recipe-<name>
    Description string                 `yaml:"description"`  // 主 Agent 选择工具时参考
    Instruction string                 `yaml:"instruction"`  // 子 Agent 系统提示词
    Prompt      string                 `yaml:"prompt"`       // Go text/template 参数化模板
    Parameters  []RecipeParameter      `yaml:"parameters"`   // 动态参数
    Response    *RecipeResponseConfig  `yaml:"response"`     // JSON Schema 输出约束
    Retry       *RecipeRetryConfig     `yaml:"retry"`        // 指数退避重试
    Extends     string                 `yaml:"extends"`      // 继承父 Recipe
    Model       string                 `yaml:"model"`        // 模型覆盖
    Tools       []string               `yaml:"tools"`        // 授权工具名（可含 recipe- 引用）
    Temperature float64                `yaml:"temperature"`  // 默认 0.3
    MaxTokens   int                    `yaml:"max_tokens"`   // 默认 1024
    MaxIterations int                  `yaml:"max_iterations"` // 默认 3
    SkipSummarization bool             `yaml:"skip_summarization"`
    Timeout     string                 `yaml:"timeout"`      // Go duration 字符串
}
```

### RecipeParameter（`recipe_tool.go:39-52`）

```go
type RecipeParameter struct {
    Key         string   `yaml:"key"`         // 模板标识符 {{.key}}
    Description string   `yaml:"description"` // 向主 Agent 解释
    Type        string   `yaml:"type"`        // string/number/boolean/select
    Required    bool     `yaml:"required"`    // 必填参数不能有 Default
    Default     string   `yaml:"default"`     // 默认值
    Options     []string `yaml:"options"`     // select 类型的选项
}
```

---

## 4. WorkflowBuilder — 多模式编排

**源码**: `internal/agent/workflow.go`

### WorkflowMode 枚举（`workflow.go:29-40`）

```go
type WorkflowMode string

const (
    WorkflowSingle          WorkflowMode = "single"
    WorkflowChain           WorkflowMode = "chain"
    WorkflowParallel        WorkflowMode = "parallel"
    WorkflowCycle           WorkflowMode = "cycle"
    WorkflowGraph           WorkflowMode = "graph"
    WorkflowTeamCoordinator WorkflowMode = "team_coordinator"
    WorkflowTeamSwarm       WorkflowMode = "team_swarm"
    WorkflowClaudeCode      WorkflowMode = "claude_code"
    WorkflowCodex           WorkflowMode = "codex"
    WorkflowDify            WorkflowMode = "dify"
)
```

### OrchestrationConfig（`workflow.go:43-48`）

```go
type OrchestrationConfig struct {
    Mode          WorkflowMode
    SubAgents     []agent.Agent
    GraphSchema   *graph.Graph
    MaxIterations int
}
```

### WorkflowBuilder（`workflow.go:52-81`）

```go
type WorkflowBuilder struct {
    factory   *provider.Factory
    cfg       *config.WukongConfig
    model     model.Model
    genConfig model.GenerationConfig
    tools     []tool.Tool
    toolSets  []tool.ToolSet
}

func NewWorkflowBuilder(factory *provider.Factory, cfg *config.WukongConfig, tools []tool.Tool, toolSets []tool.ToolSet) (*WorkflowBuilder, error)

// 按 wfCfg.Mode 分发到对应 build 方法。nil wfCfg 默认 WorkflowSingle。
func (b *WorkflowBuilder) Build(ctx context.Context, wfCfg *OrchestrationConfig) (agent.Agent, error)
```

`Build` 内部 switch 分发：`buildSingleAgent`、`buildChainAgent`、`buildParallelAgent`、`buildCycleAgent`、`buildGraphAgent`，团队模式委托给 `TeamBuilder`。

---

## 5. Provider Factory — 模型工厂

**源码**: `internal/provider/factory.go` · `internal/provider/acp.go`

### 默认 Base URL 常量（`factory.go:18-26`）

```go
const (
    OpenAIBaseURL    = "https://api.openai.com/v1"
    AnthropicBaseURL = "https://api.anthropic.com/v1"
    GoogleBaseURL    = "https://generativelanguage.googleapis.com/v1beta/openai"
    DeepSeekBaseURL  = "https://api.deepseek.com/v1"
    OllamaBaseURL    = "http://localhost:11434/v1"
    LMStudioBaseURL  = "http://localhost:1234/v1"
    VLLMBaseURL      = "http://localhost:8000/v1"
)
```

### Factory（`factory.go:29-42`）

```go
type Factory struct {
    cfg     *config.WukongConfig
    mcpAddr string // ACP MCP bridge 地址（外部设置）
}

func NewFactory(cfg *config.WukongConfig) *Factory
func (f *Factory) SetACPMCPAddr(addr string)

// 按 name 查找 provider 创建模型。name 为空时用 default provider。
func (f *Factory) CreateModel(name string) (model.Model, error)

// 按 providerName 查找，但覆盖 model 名。providerName 为空时用 default。
func (f *Factory) CreateModelWithName(providerName, modelName string) (model.Model, error)

// 创建 default provider 的模型。
func (f *Factory) CreateDefaultModel() (model.Model, error)

// 创建 RevisionModel（用于上下文摘要）。
func (f *Factory) CreateRevisionModel() (RevisionModel, error)

// 包级函数：从 AgentConfig 构造 GenerationConfig。
func GetDefaultGenerationConfig(cfg *config.AgentConfig) model.GenerationConfig
```

### RevisionModel 接口（`factory.go:241-243`）

```go
type RevisionModel interface {
    Summarize(ctx context.Context, content string, maxTokens int) (string, error)
}
```

> **关键**: tRPC-agent-go 的 `model.Model` 接口是 `Info() model.Info` + `GenerateContent(ctx, *model.Request) (<-chan *model.Response, error)`，**没有 `Call` 方法**。

### Provider 类型分发（`factory.go:63-73`）

`CreateModel` 按 `p.Type` 分发：
- `openai`/`anthropic`/`google`/`deepseek`/`ollama`/`lmstudio`/`vllm` → 全部走 `createOpenAI`（OpenAI 兼容客户端）。
- `acp` → `createACP`。

当 `ContextWindow > 0` 时，`createOpenAI` 附加 `openai.WithContextWindow` + `openai.WithEnableTokenTailoring(true)`。

### RevisionModel 解析顺序（`factory.go:211-240`）

`CreateRevisionModel` 的 provider/model 解析顺序：
1. `revision.revision_provider` / `revision.revision_model`（显式）
2. `lightweight_provider` / `lightweight_model`（全局轻量）
3. `default_provider` / 其 model（兜底）

`Summarize` 以 `[Existing Summary]` 前缀判断模式：存在则走 Progressive（合并旧摘要+新消息），否则走 Fresh（压缩原始对话）。

---

## 6. Extension Manager — 扩展管理

**源码**: `internal/extension/manager.go` · `internal/extension/types.go`

### Manager（`manager.go:27-42`）

```go
type Manager struct {
    mu       sync.RWMutex
    toolSets map[string]tool.ToolSet
    status   map[string]ExtensionInfo
    cfg      *config.WukongConfig
    ardTS    *ard.ToolSet // 可选 ARD 集成
}

func NewManager(cfg *config.WukongConfig) *Manager
func (m *Manager) SetARDToolSet(ts *ard.ToolSet)
func (m *Manager) Initialize(ctx context.Context) error
func (m *Manager) ToolSets() []tool.ToolSet
func (m *Manager) EnableExtension(ctx context.Context, name string) error
func (m *Manager) DisableExtension(name string) error
func (m *Manager) GetStatus(name string) (ExtensionInfo, bool)
func (m *Manager) ListExtensions() []ExtensionInfo
func (m *Manager) RegisterFromDeeplink(ctx context.Context, deeplinkURL string) error
func (m *Manager) SetMemoryService(svc any, appName, userID string)
func (m *Manager) SetCortexStore(cs any, userID string)
func (m *Manager) Close() error
```

> **关键**: **没有自定义 `Extension` 接口**。所有内置/外部扩展都实现 tRPC-agent-go 的 `tool.ToolSet` 接口（`Tools(ctx) []tool.Tool` + `Close() error`）。

### ExtensionInfo / ExtensionStatus（`types.go`）

```go
type ExtensionStatus string

const (
    StatusEnabled  ExtensionStatus = "enabled"
    StatusDisabled ExtensionStatus = "disabled"
    StatusError    ExtensionStatus = "error"
    StatusLoading  ExtensionStatus = "loading"
)

type ExtensionInfo struct {
    Name         string
    Type         string
    Status       ExtensionStatus
    Transport    string
    ToolCount    int
    Permissions  []config.ToolPermission
    Error        string
    RegisteredAt time.Time
}
```

### 内置扩展注册（`RegisterBuiltins`）

注册 **12 个**内置扩展（仅当配置中未显式声明时追加）。MCP Broker 模式下聚合为 4 个工具：`mcp_list_servers`、`mcp_list_tools`、`mcp_inspect_tools`、`mcp_call`。

---

## 7. Security Guard — 安全守卫

**源码**: `internal/security/guard.go` · `ssrf.go` · `command_tokens.go` · `ignore.go`

### Guard（`guard.go:26-37`）

```go
type Guard struct {
    mu               sync.RWMutex
    cfg              *config.SecurityConfig
    approvedCommands map[string]bool
    blockedCount     atomic.Int64
    ignoreMatcher    *IgnoreMatcher
}
```

> `NewGuard` 参数是 `*config.SecurityConfig`（**不是** `*config.WukongConfig`）。nil 时使用默认值。

### 关键方法签名（`guard.go`）

```go
func NewGuard(cfg *config.SecurityConfig) *Guard

func (g *Guard) GetPermissionMode() config.PermissionMode
func (g *Guard) SetPermissionMode(mode config.PermissionMode)

// 按黑/白名单 + permission mode 校验工具权限。
func (g *Guard) CheckToolPermission(toolName string, permissions []config.ToolPermission) error

// 判断某次工具调用是否需要用户审批（按 PermissionMode 分支）。
func (g *Guard) NeedsApproval(toolName string, argsJSON []byte) bool

// 校验命令是否命中危险规则（token 级分析）。
func (g *Guard) ValidateCommand(command string) error

// 根据请求时长与配置上限返回实际超时。
func (g *Guard) GetTimeout(requested time.Duration) time.Duration

// 旧接口：判断命令是否需要审批。
func (g *Guard) RequireApproval(command string) bool

// 扫描外部扩展命令/参数安全性。
func (g *Guard) ScanExtension(command string, args []string) error

func (g *Guard) ApproveCommand(command string)
func (g *Guard) IsApproved(command string) bool
func (g *Guard) GetBlockedCount() int

// 校验文件路径是否被 .wukongignore 屏蔽。
func (g *Guard) CheckFilePath(filePath string) error

// 安全执行命令（受超时与危险命令拦截保护）。
func (g *Guard) ExecuteSafe(ctx context.Context, command, workDir string, timeout time.Duration) (stdout, stderr string, exitCode int, err error)
```

### PermissionMode 常量（定义在 config 包 `types_agent.go:57-64`）

```go
type PermissionMode string

const (
    PermissionAuto     PermissionMode = "auto"
    PermissionSmart    PermissionMode = "smart"
    PermissionManual   PermissionMode = "manual"
    PermissionChatOnly PermissionMode = "chat_only"
)
```

`NeedsApproval` 分支：`Auto`→false；`Manual`→true；`ChatOnly`→true（`CheckToolPermission` 拒绝所有）；`Smart`→仅高风险 true。

> **关键**: **没有** `Decision` 结构体。危险命令检测走 `command_tokens.go` 的 `dangerousRule` 表（token 级）。

### SSRF 防护（`ssrf.go`）

```go
// 包级函数：拦截 loopback/link-local(含 169.254.169.4 元数据)/private/unspecified/multicast 地址。
func CheckURL(rawURL string) error
```

### IgnoreMatcher（`ignore.go`）

```go
func NewIgnoreMatcher(ignoreFile string, enabled bool) *IgnoreMatcher
func (m *IgnoreMatcher) IsEnabled() bool
func (m *IgnoreMatcher) IsIgnored(absPath string) bool
func (m *IgnoreMatcher) CheckFilePath(filePath string) error

func IsFileAccessTool(toolName string) bool
func ExtractFilePathFromArgs(args []byte) []string
```

---

## 8. CortexStore — 混合检索

**源码**: `internal/cortex/store.go`

### CortexStore（`store.go:28-39`）

```go
type CortexStore struct {
    cfg         *config.CortexConfig
    embedder    *Embedder
    reranker    *Reranker
    router      *vertical.Router       // 垂直域路由
    chunker     *chunking.Chunker      // 语义分块
    metrics     *metrics.SearchMetrics // 搜索可观测性
    db          *cortexdb.DB           // CortexDB (HNSW + FTS5)
    lexical     *lexicalStore
    vectorCache *VectorCache
    genome      search.SearchGenome    // 搜索策略参数
}

func NewStore(cfg *config.CortexConfig, embedder *Embedder, sharedDB *sql.DB) (*CortexStore, error)

func (s *CortexStore) StoreMessage(ctx context.Context, msg recall.ChatMessage) error
func (s *CortexStore) Search(ctx context.Context, query, userID string, limit int) ([]recall.SearchResult, error)
func (s *CortexStore) SearchBySession(/* ... */)
func (s *CortexStore) ListSessions(userID string) ([]string, error)
func (s *CortexStore) DeleteSession(sessionID string) error
func (s *CortexStore) Close() error

func (s *CortexStore) SetGenome(g search.SearchGenome)
func (s *CortexStore) Genome() search.SearchGenome
func (s *CortexStore) MetricsSnapshot() metrics.Snapshot
func (s *CortexStore) SetDB(db *cortexdb.DB)
func (s *CortexStore) DB() *cortexdb.DB
func (s *CortexStore) RecallStore() (*recall.Store, error)
func (s *CortexStore) SearchWithMemory(ctx context.Context, query, userID string, limit int, memoryReader func(ctx context.Context, query string) ([]string, error)) ([]recall.SearchResult, error)
```

`Search` 按 `SearchGenome` 分发：
- `LexicalOnly` → FTS5
- `VectorOnly` → HNSW
- `Hybrid` → 五阶段流水线：FTS5 + HNSW 双路 → RRF/加权融合 → Cross-Encoder reranker → MMR 去重 → topK

> 词法存储（`lexicalStore`）共享与 session/memory/todo 相同的 `*sql.DB`，避免 SQLite 事务冲突。

---

## 9. MemoryManager — 长期记忆

**源码**: `internal/memory/store.go`

### MemoryManager（`store.go:43-55`）

```go
type MemoryManager struct {
    svc             memory.Service
    cfg             *config.MemoryConfig
    pool            *util.DatabasePool
    ownsPool        bool
    metadataManager *MetadataManager
    active          sync.WaitGroup // 进行中的提取任务
    shutdown        sync.WaitGroup
    isClosing       bool
    mu              sync.Mutex
}

func NewMemoryManager(cfg *config.MemoryConfig, extractorModel model.Model, pool *util.DatabasePool) (*MemoryManager, error)

func (mm *MemoryManager) Service() memory.Service
func (mm *MemoryManager) Tools() []tool.Tool
func (mm *MemoryManager) Close() error
func (mm *MemoryManager) SmartCleanup(ctx context.Context, userKey, ttl) (int, error)
func (mm *MemoryManager) RecordMemoryReference(/* ... */)
func (mm *MemoryManager) BatchRecordMemoryReferences(/* ... */)
func (mm *MemoryManager) MarkMemoryImportance(/* ... */)
func (mm *MemoryManager) MetadataManager() *MetadataManager
```

`SmartCleanup` 评分模型：`40% recency + 30% reference + 20% importance + 10% length`；容量超 `cleanup_trigger_threshold`(0.8) 触发淘汰，直到降到 `cleanup_target_threshold`(0.6)。

---

## 10. Session Service — 会话存储

**源码**: `internal/session/store.go`

```go
// SessionService 嵌入 tRPC 框架的 session.Service 接口。
type SessionService struct {
    session.Service
    pool *util.DatabasePool
}

func NewSessionService(cfg *config.SessionConfig, pool *util.DatabasePool) (*SessionService, error)
func (s *SessionService) Close() error
```

> **关键**: **没有自定义 `Service` 接口**。`SessionService` 直接嵌入 tRPC-agent-go 的 `session.Service`。三种后端：
> - `sqlite` → `sessionsqlite.NewService`
> - `memory` → `sessioninmemory`
> - `redis` → 本仓库 `NewRedisSessionService`

---

## 11. Health Registry — 健康检查

**源码**: `internal/health/health.go`

### Status 枚举（`health.go:20-30`）

```go
type Status string

const (
    StatusHealthy   Status = "healthy"
    StatusDegraded  Status = "degraded"
    StatusUnhealthy Status = "unhealthy"
    StatusUnknown   Status = "unknown"
)
```

### Registry（`health.go:55-69`）

```go
type Registry struct {
    mu        sync.RWMutex
    checkers  map[string]Checker
    startTime time.Time
    version   string
}

// Checker 是健康检查函数类型。
type Checker func(ctx context.Context) ComponentHealth

func NewRegistry(version string) *Registry
func (r *Registry) Register(name string, checker Checker)
func (r *Registry) Unregister(name string)

// 运行所有检查，返回总体结果。
func (r *Registry) Check(ctx context.Context) CheckResult

// HTTP Handler（JSON 输出，支持 ?verbose=true）。Unhealthy→503，Degraded→200。
func (r *Registry) HTTPHandler() http.Handler

// Kubernetes Readiness Probe：全部 healthy 才 200，否则 503。
func (r *Registry) ReadinessHandler() http.Handler

// 包级：Liveness Probe，始终返回 {"alive":true} 200。
func LivenessHandler() http.Handler

// 包级：最简健康检查。
func SimpleHTTPHandler() http.Handler
```

### CheckResult / ComponentHealth（`health.go:33-48`）

```go
type ComponentHealth struct {
    Name      string    `json:"name"`
    Status    Status    `json:"status"`
    Message   string    `json:"message,omitempty"`
    CheckedAt time.Time `json:"checked_at"`
    LatencyMs int64     `json:"latency_ms,omitempty"`
}

type CheckResult struct {
    Status     Status            `json:"status"`
    Version    string            `json:"version"`
    Uptime     string            `json:"uptime"`
    Components []ComponentHealth `json:"components"`
    Timestamp  time.Time         `json:"timestamp"`
}
```

---

## 12. ARD ToolSet — 资源发现

**源码**: `internal/ard/tools.go`

### ToolSet（`tools.go:17-23`）

```go
type ToolSet struct {
    client  *Client
    manager *CatalogManager

    mu           sync.RWMutex
    registryURLs []string // 远程注册中心 URL 列表
}

func NewToolSet(registryURL string, catalogPath string) (*ToolSet, error)

// 设置远程注册中心 URL（验证 scheme 为 http/https，自动去重）。
func (ts *ToolSet) SetRegistryURL(rawURL string) error
```

> 初始化时若 Catalog 为空，自动填充 `WukongBuiltInEntries()`。ToolSet 实现 `tool.ToolSet` 接口，提供 ARD 发现工具。

---

## 13. SummonManager — 子 Agent 委派

**源码**: `internal/summon/delegate.go`

### Delegate / DelegateConfig（`delegate.go:29-43`）

```go
type Delegate struct {
    name        string
    description string
    agent       agent.Agent
    tool        tool.Tool
}

type DelegateConfig struct {
    Name        string
    Description string
    Instruction string
    Model       model.Model
    Tools       []tool.Tool
}

func NewDelegate(cfg DelegateConfig) (*Delegate, error)
```

子 Agent 配置：`MaxLLMCalls=10`、`MaxToolIterations=5`、`MaxTokens=2048`、`Temperature=0.3`、`ResponseModeFinalOnly`、工具重试 `MaxAttempts=2`。

### SummonManager（`delegate.go:118-150`）

```go
type SummonManager struct {
    mu        sync.RWMutex
    cfg       *config.SummonConfig
    delegates map[string]*Delegate
    infos     map[string]DelegateInfo
    model     model.Model
    sem       chan struct{} // 并发限制器
}

type DelegateInfo struct {
    Name        string `json:"name"`
    Description string `json:"description"`
    FilePath    string `json:"file_path"`
    Instruction string `json:"instruction"`
}

// 创建 SummonManager。maxConcurrent 默认 5。
func NewSummonManager(cfg *config.SummonConfig, mdl model.Model) *SummonManager

// 从 delegates_dir 加载 .md 委派定义。每个文件成为一个 Delegate。
func (m *SummonManager) LoadDelegates(ctx context.Context) error

// 获取并发槽位，返回释放函数。
func (m *SummonManager) AcquireSlot() func()

// 返回所有 delegate 工具。
func (m *SummonManager) Tools() []tool.Tool
```

---

## 14. 常见文档错误纠正

| # | 旧文档（错误） | 真实签名 | 说明 |
|---|---------------|---------|------|
| 1 | `NewCoreLoop(cfg *config.WukongConfig)` | `NewCoreLoop(cfg CoreLoopConfig)` | 参数是 `CoreLoopConfig` 结构体 |
| 2 | `Run(ctx, input string)` | `Run(ctx, userID, sessionID string, message model.Message) (<-chan *event.Event, error)` | 输入是 `model.Message`，返回事件流 |
| 3 | `model.Model` 有 `Call` 方法 | `Info() model.Info` + `GenerateContent(ctx, *model.Request) (<-chan *model.Response, error)` | **没有 `Call` 方法** |
| 4 | `Guard.Check(...)` 单参校验 | `CheckToolPermission(toolName string, permissions []config.ToolPermission) error` | 方法名与签名不同 |
| 5 | 存在自定义 `Extension` 接口 | 所有扩展实现 `tool.ToolSet` | **没有自定义 `Extension` 接口** |
| 6 | "13 个内置扩展" | `RegisterBuiltins` 注册 **12 个** | 见第 6 节 |
| 7 | 存在 `Decision` 结构体 | 危险命令走 `dangerousRule` 表（token 级） | **没有 `Decision` 结构体** |
| 8 | `NewGuard(cfg *config.WukongConfig)` | `NewGuard(cfg *config.SecurityConfig)` | 参数是 `*config.SecurityConfig` |
| 9 | 浏览器后端接口名 `Backend` | 接口名 `BrowserBackend`，在 `internal/browser/types` 子包 | 名字与包位置不同 |
| 10 | Session 有自定义 `Service` 接口 | `SessionService` 嵌入 tRPC 框架 `session.Service` | 复用框架接口 |
| 11 | `NewRecipeToolSet(ctx, cfg, factory, extraTools)` | `NewRecipeToolSet(factory, agentCfg, allTools)` | 参数是 factory + agentCfg + allTools |
| 12 | `NewSummonManager(cfg, model)` 返回复杂类型 | `NewSummonManager(cfg *config.SummonConfig, mdl model.Model) *SummonManager` | model 参数为 `model.Model` |

---

> **版本**: v0.2.0 | **最后更新**: 2026-08-11

### 相关文档

| 文档 | 说明 |
|------|------|
| [架构总览](./ARCHITECTURE.md) | 系统整体架构与模块关系 |
| [记忆系统架构](./MEMORY_ARCHITECTURE.md) | Memory/Recall/Cortex 多层记忆栈细节 |
| [配置参考](./CONFIG.md) | `WukongConfig` 全字段说明 |
| [技术实现](./TECHNICAL_IMPLEMENTATION.md) | 关键流程实现细节 |
| [开发者指南](./DEVELOPER_GUIDE.md) | 二次开发与扩展编写 |