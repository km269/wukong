# Wukong 🐵

> **本地优先、可扩展的 AI Agent 平台** | Go 1.26 | tRPC 生态 | 3 种执行模式 | 10 种工作流 | 12 个内置扩展 | 7 种 Provider | ACP/A2A/MCP 三协议 | 106 源文件

Wukong 是一个本地优先、可扩展的 AI Agent 平台，基于 [tRPC-Agent-Go](https://github.com/trpc-group/trpc-agent-go) v1.10.0、[tRPC-MCP-Go](https://github.com/trpc-group/trpc-mcp-go) v0.0.16 和 [tRPC-A2A-Go](https://github.com/trpc-group/trpc-a2a-go) v0.2.5 构建。提供CLI 交互体验，支持多种 LLM 后端、工具调用、浏览器自动化、长期记忆、RAG 知识检索、技能自进化等能力。

---

## 功能矩阵

| 领域 | 特性 |
|------|------|
| **Agent 引擎** | 交互式工具调用循环 · 10 种工作流模式 · 两遍上下文压缩 · Token 预算管理 · 消息上限 500 |
| **多 Agent 编排** | Chain/Parallel/Cycle/Graph · Team(Coordinator/Swarm) · AgentTool · A2A 协议 |
| **外部 Agent** | Claude Code CLI · Codex CLI · Dify AI 平台 · 远程 A2A 代理（JWT/APIKey/OAuth2） |
| **LLM Provider** | OpenAI · Anthropic · Google Gemini · DeepSeek · Ollama · LMStudio · **ACP**（7 种） |
| **扩展系统** | 12 个内置扩展 · 外部 MCP 服务器（stdio/sse/streamable） · MCP Broker 按需发现 · Tool Filter(glob) · SessionReconnect · Deeplink 一键安装 · Extension Manager 动态管理 · **ACP MCP Bridge**（扩展透传） |
| **内置扩展** | Developer(6) · ComputerController(9) · Memory(6) · Visualiser(3) · Tutorial(3) · Web(1) · AgentTools(3) · Apps(5) · Recall(2) · CodeMode(2) · Todo(5+1) · TopOfMind(4) |
| **协议支持** | **ACP**（Server + Provider + MCP Bridge） · **A2A**（Server + 客户端） · **MCP**（客户端 + Broker） · **AG-UI**（SSE 服务端） |
| **技能自进化** | 🆕 执行轨迹捕获 → LLM 分析问题 → 自动 Patch SKILL.md → 版本备份 → 热刷新 |
| **浏览器自动化** | Chromedp(CDP 协议) · 9 个工具 · 双模式(HTTP+Chromedp) · Chrome 泄漏修复 |
| **Web 搜索** | DuckDuckGo · SearXNG · Tavily · 可配置搜索后端 |
| **RAG 知识库** | OpenAI Embedding(1536 维) · Inmemory Vector Store · dir/URL 文档源 · knowledge_search · 可选 ReRanker |
| **长期记忆** | SQLite 持久化 · 异步提取(3 worker) · 6 个手动工具 · 自动预加载(10 条) · WaitGroup 优雅停止(5s 超时) · 自定义提取 Prompt |
| **会话管理** | SQLite/Redis/Memory · 异步摘要 · TTL · 事件分页 · 跨会话回溯(FTS5/Hybrid) |
| **任务跟踪** | 双层 Todo：自定义 SQLite(5) + tRPC 原生 todo_write · TodoEnforcer 强制完成 |
| **安全防护** | 4 级权限(auto/smart/manual/chat_only) · Allowlist/Denylist · 12 种命令拦截 · Prompt 注入检测 · .wukongignore · 恶意软件扫描 |
| **上下文优化** | 两遍压缩(占位符+截断) · 修订摘要模型 · Per-Tool 控制 · Token 裁剪 · SessionRecall |
| **子代理系统** | 3 个内置子 Agent · Skill 仓库(SKILL.md) · Summon 调度(并发 5) · YAML Recipe |
| **可观测性** | OpenTelemetry 分布式追踪 · Langfuse LLM 追踪 · 结构化日志(slog) · 健康检查 |
| **制品存储** | Inmemory(默认) · Tencent COS(云端) |
| **分布式** | A2A Server · **ACP Server + Provider** · AG-UI SSE 服务器 · Redis Session |
| **评估** | JSON EvalSet · 4 种指标 · 回归测试 CLI |
| **HITL** | Graph 节点中断/恢复 · 静态/动态两种模式 · Checkpoint 状态持久化 |
| **TUI** | Bubbletea + Lipgloss · Ctrl+C 流式取消 · 友好错误处理 |
| **Prompt 管理** | 自定义 .md 模板 · 变量替换 · YAML Recipe 子代理 · TopOfMind 持久化指令 |
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

### 内置扩展（开箱即用，12 个）

| 扩展 | 分类 | 工具数 | 说明 |
|------|------|--------|------|
| Developer | 功能性 | 6 | 文件读写/命令执行/代码搜索/目录列表 |
| Computer Controller | 功能性 | 9 | Web 抓取/文件缓存/浏览器自动化 |
| Memory | 功能性 | 6 | 长期记忆存储/搜索/管理 |
| Auto Visualiser | 功能性 | 3 | SVG 图表/Mermaid 图/HTML 表格生成 |
| Tutorial | 功能性 | 3 | 交互式教程(git/docker/go 等) |
| Web | 功能性 | 1 | 搜索引擎(DuckDuckGo/SearXNG/Tavily) |
| Apps | 平台 | 5 | HTML 应用创建/管理 |
| Chat Recall | 平台 | 2 | FTS5 跨会话对话搜索 |
| Code Mode | 平台 | 2 | goja JS 沙箱执行+工具发现 |
| Extension Manager | 平台 | 4 | 扩展列表/启用/禁用/安装 |
| Summon | 平台 | 3+ | 子 Agent 调度+Skills+A2A |
| Todo | 平台 | 6 | 双层任务跟踪+强制完成 |
| Top of Mind | 平台 | 4 | 持久化指令注入 |

### 安装外部 MCP 服务器

```yaml
extensions:
  # stdio 传输
  - name: "filesystem"
    type: "external"
    transport: "stdio"
    command: "npx"
    args: ["-y", "@anthropic/mcp-server-filesystem", "/tmp"]
    enabled: true
    mcp_tool_filter: ["read_file", "write_file"]    # 只包含指定工具
    mcp_tool_exclude: ["delete_file"]                # 排除指定工具

  # SSE 传输
  - name: "remote-server"
    type: "external"
    transport: "sse"
    url: "http://localhost:3001/sse"
    enabled: true
    mcp_session_reconnect: true                      # 自动重连
    mcp_session_reconnect_attempts: 3

  # MCP Broker 模式（按需发现，避免工具列表臃肿）
  - name: "large-tool-server"
    type: "external"
    transport: "stdio"
    command: "npx"
    args: ["-y", "@some/mcp-server-with-many-tools"]
    enabled: true
    mcp_broker: true                                 # 通过 Broker 按需调用
```

### Deeplink 一键安装扩展

```
wukong://extension?name=github&type=external&transport=stdio&command=npx&args=-y&args=@modelcontextprotocol/server-github
```

---

## 技能自进化 🆕

开启后，技能执行过程中会由 LLM 自动分析问题并修补 SKILL.md：

```yaml
evolution:
  enabled: true            # 启用技能进化
  auto_patch: true         # 自动应用补丁（false=仅记录建议）
  min_confidence: 0.7      # 接受补丁的最低置信度
  cooldown_period: "30m"   # 同技能两次修补的最短间隔
```

**核心闭环**: 技能执行 → AfterAgent 轨迹采集 → LLM 分析问题 → Patch 生成 → 备份 + 应用 → 版本记录 → 热刷新

---

## ACP（代理客户端协议）集成

**ACP Server — 让 ACP 客户端原生连接 Wukong**:

```yaml
acp_server:
  enabled: true
  address: ":9091"
```

端点：`POST /acp/message/send` · `GET /acp/tools/list` · `POST /acp/tools/call` · `GET /acp/.well-known/agent.json`

**ACP Provider — 将 ACP 代理作为 LLM 提供商**:

```yaml
providers:
  - name: "acp-coder"
    type: "acp"
    agent_url: "http://localhost:4000"
    model: "acp-default"
```

**ACP MCP Bridge — 扩展工具透传**: 系统扩展自动注册为 MCP Tool，ACP 代理通过标准 JSON-RPC 协议发现和调用。

---

## 工作流模式

```yaml
workflow:
  mode: "single"              # single | chain | parallel | cycle | graph
                              # team_coordinator | team_swarm | claude_code | codex | dify
  max_iterations: 10
  cycle_mode: "default"       # default | code_review

  # Team 模式成员
  team_members:
    - name: "researcher"
      instruction: "You are a research specialist..."
    - name: "coder"
      instruction: "You are a coding specialist..."
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

高风险操作（smart 模式需审批）：
- 命令执行：`command_execute`, `bash`, `shell` 等
- 文件写入：`file_write`, `file_replace`, `file_delete`
- 浏览器操作：`browser_navigate`, `browser_screenshot`, `browser_click`, `browser_fill`
- Web 请求：`web_fetch`

`.wukongignore` 文件（gitignore 语法）可额外限制文件访问：

```gitignore
# 保护敏感文件
.env
*.pem
**/secrets/**
```

---

## 执行模式

| 模式 | 命令 | 交互 | 上下文保持 | 适用场景 |
|------|------|------|-----------|---------|
| **TUI** | `wukong session` | Bubbletea UI | 自动 | 日常开发对话 |
| **单次** | `wukong run -m "..."` | 无 | 仅当 -s 指定 | 脚本/管道/CI |
| **对话** | `wukong run -d` | Shell REPL | 自动 | 轻量多轮 |

---

## 项目结构

```
wukong/
├── cmd/wukong/main.go          程序入口
├── config.yaml                主配置文件（28 段）
├── internal/
│   ├── agent/                  CoreLoop · WorkflowBuilder(10) · TeamBuilder · Dify · HITL · TodoEnforcer · Recipe · PromptTemplate
│   ├── apps/                   HTML 应用文件管理
│   ├── artifact/               inmemory/COS 制品工厂
│   ├── browser/                HTTP+Chromedp(CDP) 双模引擎
│   ├── cli/+tui/               9 子命令 · Bubbletea TUI · 对话模式 REPL
│   ├── codemode/               goja JS 沙箱
│   ├── config/                 Viper 配置 · 28 配置段（~60KB）
│   ├── eval/                   EvalSet/Metric/Evaluator
│   ├── evolution/              🆕 技能自进化引擎（engine/analyzer/patcher/store）
│   ├── extension/              MCP 管理器+12 内置+MCP Client+ACP Bridge
│   ├── health/                 健康检查
│   ├── knowledge/              RAG(Embedding+VectorStore+Source)
│   ├── memory/                 长期记忆(GracefulShutdown)
│   ├── observability/          Langfuse OTLP
│   ├── project/                项目追踪
│   ├── provider/               7 种 LLM 工厂（含 ACP）
│   ├── recall/                 FTS5 跨会话搜索
│   ├── security/               4 级权限+命令拦截+.wukongignore
│   ├── server/                 AG-UI SSE + ACP Server
│   ├── session/                sqlite/redis 会话
│   ├── skill/                  Agent Skill 仓库 + Evolution Hook
│   ├── summon/                 A2A+子代理调度+并发控制
│   ├── telemetry/              OTel 分布式追踪
│   ├── todo/                   任务跟踪(SQLite)
│   ├── topofmind/              持久化指令注入
│   └── util/                   DB 池(WAL)+Logger(slog)
```

---

## 技术栈

- **Go 1.26** | **tRPC-Agent-Go v1.10.0** | **tRPC-MCP-Go v0.0.16** | **tRPC-A2A-Go v0.2.5**
- **Bubbletea + Lipgloss** (TUI) | **Cobra + Viper** (CLI/Config)
- **SQLite (WAL + FTS5)** | **go-redis/v9** | **COS SDK**
- **Chromedp** (浏览器自动化) | **goja** (JS 沙箱)
- **OpenTelemetry** | **Langfuse** (LLM 追踪)

---

## 文档

| 文档 | 说明 |
|------|------|
| [README](README.md) | 项目概览与快速开始 |
| [ARCHITECTURE](ARCHITECTURE.md) | 完整系统架构文档 |
| [CONFIG](CONFIG.md) | 配置参考手册 |
