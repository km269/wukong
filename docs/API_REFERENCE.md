# Wukong API 参考文档

> 本文档提供 Wukong 核心模块的 API 接口定义和使用示例。

---

## 目录

1. [CoreLoop API](#1-coreloop-api)
2. [Agent API](#2-agent-api)
3. [Provider API](#3-provider-api)
4. [Memory API](#4-memory-api)
5. [Extension API](#5-extension-api)
6. [Security API](#6-security-api)
7. [Apps API](#7-apps-api)
8. [Browser API](#8-browser-api)
9. [CLI API](#9-cli-api)

---

## 1. CoreLoop API

### CoreLoop 结构

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
}
```

### 主要方法

#### NewCoreLoop

创建 CoreLoop 实例。

```go
func NewCoreLoop(cfg *config.WukongConfig) (*CoreLoop, error)
```

**参数**:
- `cfg`: 配置对象

**返回**:
- `*CoreLoop`: CoreLoop 实例
- `error`: 错误信息

**示例**:

```go
cfg := config.Load("config.yaml")
loop, err := NewCoreLoop(cfg)
if err != nil {
    log.Fatal(err)
}
```

#### Run

执行 Agent 运行。

```go
func (l *CoreLoop) Run(ctx context.Context, input string) (*Result, error)
```

**参数**:
- `ctx`: 上下文
- `input`: 用户输入

**返回**:
- `*Result`: 运行结果
- `error`: 错误信息

**示例**:

```go
result, err := loop.Run(ctx, "分析项目结构")
if err != nil {
    log.Fatal(err)
}
fmt.Println(result.Output)
```

---

## 2. Agent API

### Agent 接口

```go
type Agent interface {
    Run(ctx context.Context, input string) (*Result, error)
    Name() string
    Tools() []tool.Tool
}
```

### LLMAgent

LLM Agent 实现。

```go
type LLMAgent struct {
    provider provider.Provider
    tools    []tool.Tool
    planner  planner.Planner
}
```

#### NewLLMAgent

```go
func NewLLMAgent(opts ...AgentOption) *LLMAgent
```

**示例**:

```go
agent := NewLLMAgent(
    WithProvider(openaiProvider),
    WithTools(tools),
    WithPlanner(planner),
)
```

---

## 3. Provider API

### Provider 接口

```go
type Provider interface {
    Call(ctx context.Context, req *Request) (*Response, error)
    Name() string
    Models() []string
}
```

### Request 结构

```go
type Request struct {
    Messages []Message
    Tools    []tool.Tool
    Config   CallConfig
}
```

### Response 结构

```go
type Response struct {
    Content     string
    ToolCalls   []ToolCall
    Usage       Usage
    FinishReason string
}
```

### OpenAI Provider

```go
provider := openai.NewProvider(
    openai.WithAPIKey("sk-..."),
    openai.WithModel("gpt-4o"),
)
```

---

## 4. Memory API

### MemoryFlow

```go
type MemoryFlowService struct {
    store     *CortexStore
    extractor extractor.Extractor
}
```

#### IngestTurn

吸收会话轮次。

```go
func (m *MemoryFlowService) IngestTurn(ctx context.Context, turn *Turn) error
```

#### WakeUp

唤醒相关记忆。

```go
func (m *MemoryFlowService) WakeUp(ctx context.Context, query string, k int) ([]Memory, error)
```

### CortexStore

```go
type CortexStore struct {
    db       *sql.DB
    hnsw     *hnsw.Index
    embedder embedder.Embedder
}
```

#### Store

存储记忆。

```go
func (s *CortexStore) Store(ctx context.Context, memory *Memory) error
```

#### Search

搜索记忆。

```go
func (s *CortexStore) Search(ctx context.Context, query string, opts SearchOptions) ([]Result, error)
```

---

## 5. Extension API

### Extension 接口

```go
type Extension interface {
    Name() string
    Initialize(ctx context.Context) error
    Tools() []tool.Tool
    Shutdown(ctx context.Context) error
}
```

### ExtensionManager

```go
type ExtensionManager struct {
    extensions map[string]Extension
    registry   *Registry
}
```

#### Register

注册扩展。

```go
func (m *ExtensionManager) Register(ext Extension) error
```

#### GetTool

获取工具。

```go
func (m *ExtensionManager) GetTool(name string) (tool.Tool, error)
```

---

## 6. Security API

### Guard

```go
type Guard struct {
    mode          PermissionMode
    allowlist     []string
    denylist      []string
    malwareScanner scanner.Scanner
}
```

#### Check

检查权限。

```go
func (g *Guard) Check(ctx context.Context, action *Action) (*Decision, error)
```

**返回**:

```go
type Decision struct {
    Allow   bool
    Reason  string
    Timeout time.Duration
}
```

---

## 7. Apps API

### AppManager

```go
type AppManager struct {
    appsDir string
    store   *AppStore
}
```

#### Clone

克隆网站。

```go
func (m *AppManager) Clone(ctx context.Context, url string, opts CloneOptions) (*CloneResult, error)
```

**参数**:

```go
type CloneOptions struct {
    MaxPages    int
    MaxDepth    int
    Workers     int
    RespectRobots bool
}
```

---

## 8. Browser API

### BrowserEngine

```go
type BrowserEngine struct {
    allocator browser.Allocator
    pool      *PagePool
}
```

#### NewBrowserEngine

```go
func NewBrowserEngine(opts ...BrowserOption) *BrowserEngine
```

#### Navigate

导航到 URL。

```go
func (b *BrowserEngine) Navigate(ctx context.Context, url string) (*Page, error)
```

#### Screenshot

截取页面。

```go
func (b *BrowserEngine) Screenshot(ctx context.Context, page *Page) ([]byte, error)
```

---

## 9. CLI API

### 命令结构

```go
type Command struct {
    Use   string
    Short string
    Long  string
    Run   func(cmd *Command, args []string)
}
```

### 注册命令

```go
func init() {
    rootCmd.AddCommand(myCmd)
}

var myCmd = &cobra.Command{
    Use:   "mycommand",
    Short: "My command description",
    Run:   runMyCommand,
}
```

---

## 配置 API

### Config 结构

```go
type WukongConfig struct {
    LogLevel         string
    DefaultProvider  string
    Providers        []ProviderConfig
    Agent            AgentConfig
    Security         SecurityConfig
    Memory           MemoryConfig
    Extensions       []ExtensionConfig
}
```

### 加载配置

```go
cfg, err := config.Load("config.yaml")
```

---

**相关文档**:
- [系统概述](../SYSTEM_OVERVIEW.md)
- [技术架构](ARCHITECTURE.md)
- [开发者指南](DEVELOPER_GUIDE.md)
