# Wukong 🐵

> **本地优先、可扩展的 AI Agent 平台** | Go 1.26 | tRPC 生态 | 3种执行模式 | 10种工作流 | 12个内置扩展 | 7种Provider | ACP/A2A/MCP三协议 | 101源文件

Wukong 是一个本地优先、可扩展的 AI Agent 平台，基于 [tRPC-Agent-Go](https://github.com/trpc-group/trpc-agent-go) v1.10.0、[tRPC-MCP-Go](https://github.com/trpc-group/trpc-mcp-go) 和 [tRPC-A2A-Go](https://github.com/trpc-group/trpc-a2a-go) 构建。提供类似 Goose 的 CLI 交互体验，支持多种 LLM 后端、工具调用、浏览器自动化、长期记忆、RAG 知识检索等能力。

---

## 功能矩阵

| 领域 | 特性 |
|------|------|
| **Agent 引擎** | 交互式工具调用循环 · 10种工作流模式 · 两遍上下文压缩 · Token预算管理 · 消息上限500 |
| **多Agent编排** | Chain/Parallel/Cycle/Graph · Team(Coordinator/Swarm) · AgentTool · A2A协议 |
| **外部Agent** | Claude Code CLI · Codex CLI · Dify AI平台 · 远程A2A代理（JWT/APIKey/OAuth2） |
| **LLM Provider** | OpenAI · Anthropic · Google Gemini · DeepSeek · Ollama · LMStudio · **ACP**（7种，统一OpenAI兼容API + ACP代理协议） |
| **扩展系统** | 12个内置扩展 · 外部MCP服务器（stdio/sse/streamable） · MCP Broker按需发现 · Tool Filter(glob) · SessionReconnect · Deeplink一键安装 · Extension Manager动态管理 · **ACP MCP Bridge（扩展透传）** |
| **内置扩展** | Developer(6) · ComputerController(9) · Memory(6) · Visualiser(3) · Tutorial(3) · Web(1) · AgentTools(3) · Apps(5) · Recall(2) · CodeMode(2) · Todo(5+1) · TopOfMind(4) |
| **协议支持** | **ACP**（Server + Provider + MCP Bridge） · **A2A**（Server + 客户端） · **MCP**（客户端 + Broker） · **AG-UI**（SSE服务端） |
| **浏览器自动化** | Chromedp(CDP协议) · 9个工具(navigate/extract/screenshot/click/fill/web_fetch/cache) · 双模式(HTTP+Chromedp) · Chrome泄漏修复 |
| **Web搜索** | DuckDuckGo即时回答 · 预留SearXNG/Tavily · 可配置搜索后端 |
| **RAG知识库** | OpenAI Embedding(1536维) · Inmemory Vector Store · dir/URL文档源 · knowledge_search工具 · 可选ReRanker |
| **长期记忆** | SQLite持久化 · 异步提取(3worker) · 6个手动工具 · 自动预加载(10条) · WaitGroup优雅停止(5s超时) · 自定义提取Prompt |
| **会话管理** | SQLite/Redis/Inmemory · 异步摘要 · TTL · 事件分页 · 跨会话回溯(FTS5) |
| **任务跟踪** | 双层Todo：自定义SQLite工具(5个) + tRPC原生todo_write · TodoEnforcer强制完成校验 |
| **安全防护** | 4级权限模型(auto/smart/manual/chat_only) · Allowlist/Denylist · 12种命令拦截 · Prompt注入检测 · .wukongignore文件黑名单 · 恶意软件扫描 |
| **上下文优化** | 两遍压缩(占位符+截断) · 修订摘要模型 · Per-Tool控制 · Token裁剪 · SessionRecall |
| **子代理系统** | 内置3个子Agent(code-reviewer/summarizer/code-generator) · Skill仓库(SKILL.md) · Summon调度(并发控制5) · YAML Recipe配方 |
| **可观测性** | OpenTelemetry分布式追踪 · Langfuse LLM追踪 · 结构化日志(slog) · 健康检查 |
| **制品存储** | Inmemory(默认) · Tencent COS(云端) |
| **分布式** | A2A Server(tRPC-A2A-Go) · **ACP Server** · AG-UI SSE服务器 · Redis Session | 
| **评估** | JSON EvalSet · 4种指标(tool_trajectory_match/response_contains_pattern/...) · 回归测试CLI |
| **HITL** | Graph节点中断/恢复 · 静态/动态两种模式 · Checkpoint状态持久化 |
| **TUI** | Bubbletea + Lipgloss · Ctrl+C流式取消 · 友好错误处理(401/429/500) |
| **Prompt管理** | 自定义.md模板目录 · 变量替换 · YAML Recipe子代理 · TopOfMind持久化指令 |
| **项目追踪** | 工作目录自动记录 · 会话快速恢复 · 项目数据持久化 |

---

## 快速开始

### 前置要求

- Go 1.26+
- ripgrep (`rg`) — `code_search` 工具所需（可选）
- Chrome/Chromium — 浏览器自动化（可选）

### 安装

```bash
git clone https://github.com/km269/wukong.git && cd wukong
go build -o wukong ./cmd/wukong/
```

### 最小配置 (~/.config/wukong/config.yaml)

```yaml
default_provider: "openai"
providers:
  - name: "openai"
    type: "openai"
    api_key: "${OPENAI_API_KEY}"
    model: "gpt-4o"
```

### 使用

```bash
# 交互会话（TUI）
./wukong session

# 指定 Provider 和模型
./wukong session --provider deepseek --model deepseek-chat

# 恢复之前的会话
./wukong session --session-id <session-id-prefix>

# 单次执行
./wukong run -m "解释这个项目的架构"
echo "优化 app.go" | ./wukong run

# 多轮对话（Shell REPL，自动保持上下文）
./wukong run -d
./wukong run -d -p deepseek --model deepseek-chat
./wukong run -d -s my-task          # 自定义 session，可随时恢复

# 扩展管理
./wukong extension list             # 列出所有扩展
./wukong extension enable memory    # 动态启用扩展

# 配置向导
./wukong configure

# 评估回归测试
./wukong eval
```

### 使用本地模型 (LMStudio/Ollama)

```yaml
default_provider: "lmstudio"
providers:
  - name: "lmstudio"
    type: "lmstudio"
    api_key: "lmstudio"
    base_url: "http://localhost:1234/v1"
    model: "your-model-name"
```

---

## 扩展系统

### 内置扩展（开箱即用，12个）

所有内置扩展默认启用，无需额外配置：

| 扩展 | 分类 | 工具数 | 说明 |
|------|------|--------|------|
| Developer | 功能性 | 6 | 文件读写/命令执行/代码搜索/目录列表 |
| Computer Controller | 功能性 | 9 | Web抓取/文件缓存/浏览器自动化 |
| Memory | 功能性 | 6 | 长期记忆存储/搜索/管理 |
| Auto Visualiser | 功能性 | 3 | SVG图表/Mermaid图/HTML表格生成 |
| Tutorial | 功能性 | 3 | 交互式教程(git/docker/go等) |
| Web | 功能性 | 1 | DuckDuckGo搜索引擎 |
| Apps | 平台 | 5 | HTML应用创建/管理 |
| Chat Recall | 平台 | 2 | FTS5跨会话对话搜索 |
| Code Mode | 平台 | 2 | goja JS沙箱执行+工具发现 |
| Extension Manager | 平台 | 4 | 扩展列表/启用/禁用/安装 |
| Summon | 平台 | 3+ | 子Agent调度+Skills+A2A |
| Todo | 平台 | 6 | 双层任务跟踪+强制完成 |
| Top of Mind | 平台 | 4 | 持久化指令注入 |

### 安装外部MCP服务器

```yaml
extensions:
  # stdio传输
  - name: "filesystem"
    type: "external"
    transport: "stdio"
    command: "npx"
    args: ["-y", "@anthropic/mcp-server-filesystem", "/tmp"]
    enabled: true
    mcp_tool_filter: ["read_file", "write_file"]    # 只包含指定工具
    mcp_tool_exclude: ["delete_file"]                # 排除指定工具

  # SSE传输
  - name: "remote-server"
    type: "external"
    transport: "sse"
    url: "http://localhost:3001/sse"
    enabled: true
    mcp_session_reconnect: true                      # 自动重连
    mcp_session_reconnect_attempts: 3

  # MCP Broker模式（按需发现，避免工具列表臃肿）
  - name: "large-tool-server"
    type: "external"
    transport: "stdio"
    command: "npx"
    args: ["-y", "@some/mcp-server-with-many-tools"]
    enabled: true
    mcp_broker: true                                 # 通过Broker按需调用
```

### Deeplink 一键安装扩展

```
wukong://extension?name=github&type=external&transport=stdio&command=npx&args=-y&args=@modelcontextprotocol/server-github
```

### ACP（代理客户端协议）集成

Wukong 完整支持 ACP（Agent Client Protocol），实现双向集成：

**1. ACP Server — 让 ACP 客户端原生连接 Wukong**

```yaml
acp_server:
  enabled: true
  address: ":9091"
```

启动后暴露端点：
- `POST /acp/message/send` — 用户消息 + SSE 流式响应
- `GET /acp/tools/list` — Agent Card + 全部工具列表
- `POST /acp/tools/call` — 直接工具调用
- `GET /acp/.well-known/agent.json` — 能力发现

**2. ACP Provider — 将 ACP 代理作为 LLM 提供商**

```yaml
providers:
  - name: "acp-coder"
    type: "acp"
    agent_url: "http://localhost:4000"
    model: "acp-default"
```

Wukong 扩展自动通过 MCP Bridge（`:3400/mcp`）透传给 ACP 代理调用。

**3. ACP MCP Bridge — 扩展工具透传**

系统扩展（Developer、Memory、Browser 等）自动注册为 MCP Tool，ACP 代理通过标准 JSON-RPC 协议发现和调用。

---

## 工作流模式

通过 `workflow.mode` 配置切换：

```yaml
workflow:
  mode: "single"              # single | chain | parallel | cycle | graph
                              # team_coordinator | team_swarm | claude_code | codex | dify
  max_iterations: 10
  cycle_mode: "default"       # default | code_review

  # Team模式成员
  team_members:
    - name: "researcher"
      instruction: "You are a research specialist..."
    - name: "coder"
      instruction: "You are a coding specialist..."

  # Claude Code CLI
  claude_code_bin: "claude"
  # Codex CLI
  codex_bin: "codex"
```

---

## 安全模型

```
权限模式:
  auto      → 全自动，无需审批
  smart     → 仅高风险操作审批（默认，推荐）
  manual    → 每次调用都需审批
  chat_only → 纯文本，禁止工具
```

高风险操作（smart模式需审批）：
- 命令执行：`command_execute`, `bash`, `shell` 等
- 文件写入：`file_write`, `file_replace`, `file_delete`
- 浏览器操作：`browser_navigate`, `browser_screenshot`, `browser_click`, `browser_fill`
- Web请求：`web_fetch`

`.wukongignore` 文件（gitignore语法）可额外限制文件访问：

```gitignore
# 保护敏感文件
.env
*.pem
**/secrets/**
```

---

## 项目结构

```
wukong/
├── cmd/wukong/main.go          程序入口
├── config.yaml                  主配置文件
├── internal/
│   ├── agent/                   CoreLoop · WorkflowBuilder(10) · Team-Builder · Dify · HITL · TodoEnforcer · Recipe · PromptTemplate
│   ├── apps/                    HTML应用文件管理
│   ├── artifact/                inmemory/COS制品工厂
│   ├── browser/                 HTTP+Chromedp(CDP)双模引擎
│   ├── cli/+tui/                7子命令 · Bubbletea TUI · 对话模式REPL
│   ├── codemode/                goja JS沙箱
│   ├── config/                  Viper配置 · 30+配置段
│   ├── eval/                    EvalSet/Metric/Evaluator
│   ├── extension/               MCP管理器+12内置+MCP Client+ACP Bridge
│   ├── health/                  健康检查
│   ├── knowledge/               RAG(Embedding+VectorStore+Source)
│   ├── memory/                  长期记忆(GracefulShutdown)
│   ├── observability/           Langfuse OTLP
│   ├── project/                 项目追踪
│   ├── provider/                7种LLM工厂（含ACP）
│   ├── recall/                  FTS5跨会话搜索
│   ├── security/                4级权限+命令拦截+.wukongignore
│   ├── server/                  AG-UI SSE + ACP Server
│   ├── session/                 sqlite/redis会话
│   ├── skill/                   Agent Skill仓库
│   ├── summon/                  A2A+子代理调度+并发控制
│   ├── telemetry/               OTel分布式追踪
│   ├── todo/                    任务跟踪(SQLite)
│   ├── topofmind/               持久化指令注入
│   └── util/                    DB池(WAL)+日志
```

---

## 技术栈

- **Go 1.26** | **tRPC-Agent-Go v1.10.0** | **tRPC-MCP-Go v0.0.16** | **tRPC-A2A-Go v0.2.5**
- **Bubbletea + Lipgloss** (TUI) | **Cobra + Viper** (CLI/Config)
- **SQLite (WAL + FTS5)** | **go-redis/v9** | **COS SDK**
- **Chromedp** (浏览器自动化) | **goja** (JS沙箱)
- **OpenTelemetry** | **Langfuse** (LLM追踪)

---

## 文档

| 文档 | 说明 |
|------|------|
| [README](README.md) | 项目概览与快速开始 |
| [ARCHITECTURE](ARCHITECTURE.md) | 完整系统架构文档 |
| [CONFIG](CONFIG.md) | 配置参考手册 |
