# Wukong

> **记忆优先 · 编排驱动 · 安全纵深 AI Agent 平台**
>
> Go 1.26 | tRPC-Agent-Go v1.10.0 | CortexDB v2.25.0 | GNU AGPL-3.0

Wukong 是一个本地优先、框架组装、可深度扩展的 AI Agent 平台。核心理念：**Agent 的真正智能不取决于单次对话的表现，而取决于跨会话的记忆积累、多 Agent 的协同编排、多层纵深的安全防御、以及技能的持续自进化。**

---

## 核心能力矩阵

| 维度 | 方案 | 实现 |
|------|------|------|
| **文件规模** | 174+ `.go` / 28+ 包 | `cmd/`(1) + `pkg/`(2包) + `internal/`(28+包) |
| **编排模式** | 10 种 | single / chain / parallel / cycle / graph / team_coordinator / team_swarm / claude_code / codex / dify |
| **Recipe 功能** | 14 项 | 参数化/结构化输出/子配方/重试/继承/内联/模型覆盖/超时/发现/热重载/指令模板/指标 |
| **LLM 后端** | 7 种 | OpenAI / Anthropic / Google / DeepSeek / Ollama / LMStudio / ACP |
| **记忆引擎** | 双引擎三层 | tRPC Memory(SQLite KV+SmartCleanup) × CortexDB Stack(HNSW+FTS5+RDF) |
| **安全防御** | 5 层纵深 | Guard → goja JS沙箱 → OS沙箱 → .wukongignore → OS权限 |
| **内置扩展** | 12 个 | developer / memory / cortex / code_mode / browser / visualiser / tutorial / apps / web / topofmind / agent_tools / ard |
| **协议服务** | 4 种 | A2A(:9090) / ACP(:9091) / AG-UI SSE(:8080) / ACP MCP(:3400) |
| **部署形态** | 单文件 | wukong.db 承载全栈：session/memory/recall/cortex/todo/evolution/apps_history |

---

## 安全纵深模型

```
Layer 5: Guard 权限控制    → auto/smart/manual/chat_only + 命令拦截 + Prompt注入检测
Layer 4: goja JS 沙箱      → API白名单 + 128MB限制 + 5并发 + ReDoS防护 + 1MB输入限制
Layer 3: sandbox OS 级隔离  → Landlock(linux) / sandbox-exec(macOS) / Low IL(Windows)
Layer 2: .wukongignore      → gitignore兼容文件访问黑名单
Layer 1: OS 进程权限         → 非root + ulimit
```

---

## 快速开始

```bash
# 安装
go install github.com/km269/wukong/cmd/wukong@latest

# 交互式配置
wukong configure

# 启动交互会话 (自动启用: 对话持久化/记忆闭环/知识图谱/上下文压缩/安全防护/热重载/可观测性)
wukong session

# 指定模型
wukong session --provider deepseek --model deepseek-chat

# 非交互式单次执行
wukong run --prompt "分析当前项目结构"

# 扩展管理
wukong extension list
wukong extension install --url wukong://extension?name=memory&version=v1.0.0
```

---

## Recipe 系统

基于 YAML 的结构化子 Agent 定义系统，14 项功能：

```yaml
# .wukong/recipes/code_reviewer.yaml
name: code_reviewer
description: "Parameterized code reviewer"
instruction: "You are a {{.language}} expert."
prompt: "Review: {{.code}}"
extends: base_reviewer              # 继承
parameters:                         # 参数化
  - key: language
    type: select
    options: [go, python, rust]
tools: [file_read, recipe-sub-reviewer]  # 子配方
model: "gpt-4o"                     # 模型覆盖
timeout: "30s"
retry: {max_attempts: 3}            # 重试
response:                           # 结构化输出
  json_schema: {type: object, required: [issues]}
  validate_output: true
```

**辅组工具**: `list_recipes` (发现) / `reload_recipes` (热重载) / `recipe_stats` (指标)

---

## 项目结构

```
wukong/
├── cmd/wukong/main.go              # 入口
├── config.yaml                     # 完整配置 (30+ 配置段, 38 结构体)
├── pkg/
│   ├── sandbox/                    # OS 级文件沙箱 (10文件)
│   │   ├── sandbox.go              #   Command/CommandContext 公共API
│   │   ├── sandbox_linux.go        #   Landlock LSM (kernel 5.13+)
│   │   ├── sandbox_darwin.go       #   sandbox-exec + Seatbelt
│   │   └── sandbox_windows.go      #   Low Integrity Level
│   └── zim/                        # ZIM 文件格式库 (4文件)
│       ├── format.go / zim.go      #   写入器(Packer)
│       ├── reader.go               #   读取器(Reader)
│       └── codec.go                #   压缩/解压 (zstd)
├── internal/
│   ├── agent/                      # 核心引擎 (21文件)
│   │   ├── loop.go (1560行)        #   CoreLoop 中央编排器
│   │   ├── workflow.go             #   10种编排模式
│   │   ├── team.go                 #   多Agent团队
│   │   ├── context.go              #   3层上下文压缩
│   │   ├── hitl.go                 #   HITL 人机协同
│   │   ├── prompt_template.go      #   提示模板
│   │   ├── todo_enforcer.go        #   Todo 执行器
│   │   ├── dify.go                 #   Dify 集成
│   │   ├── recipe.go (795行)       #   Recipe 定义+加载
│   │   ├── recipe_tool.go          #   参数化模板+指标
│   │   ├── recipe_compose.go       #   子配方+继承+重试
│   │   ├── recipe_advance.go       #   模型覆盖+超时+热重载
│   │   └── recipe_metrics.go       #   执行指标+统计工具
│   ├── config/config.go            # Viper 配置 (38 结构体)
│   ├── provider/factory.go         # LLM 工厂 (7 backend)
│   ├── cli/                        # CLI + BubbleTea TUI (11文件)
│   │   ├── session.go (1317行)     #   会话引导 (28子系统初始化)
│   │   └── {run,configure,extension,eval,project}.go
│   ├── cortex/                     # CortexDB 智能记忆栈 (12文件)
│   │   ├── store.go                #   HNSW + FTS5 混合搜索
│   │   ├── lexical.go (587行)      #   FTS5 词法搜索 + 向量索引
│   │   ├── memoryflow.go           #   转录/唤醒/PromoteFacts
│   │   ├── graphflow.go            #   RDF 知识图谱
│   │   ├── extractor.go (439行)    #   LLM+启发式 3层回退
│   │   ├── embedder.go             #   OpenAI 向量客户端
│   │   └── {recall_manager, import_*, kg_tools, planner}.go
│   ├── extension/                  # 扩展系统 (25文件)
│   │   ├── manager.go              #   生命周期管理
│   │   ├── mcp_client.go           #   MCP 客户端 (3传输)
│   │   ├── acp_mcp.go              #   ACP MCP 桥接 (JSON-RPC 2.0)
│   │   └── builtin/                #   12 个内置扩展
│   ├── memory/store.go             # tRPC Memory (AutoExtract+SmartCleanup)
│   ├── recall/                     # FTS5 跨会话搜索
│   ├── security/guard.go           # 安全守卫 (5层)
│   ├── evolution/                  # 技能自进化引擎 (6文件)
│   ├── summon/                     # 子代理委派 + A2A (6文件)
│   ├── skill/manager.go            # Agent Skill 系统
│   ├── server/                     # ACP + AG-UI SSE 服务
│   ├── codemode/executor.go        # goja JS 沙箱
│   ├── browser/controller.go       # Chromedp 浏览器
│   ├── knowledge/manager.go        # RAG 知识库
│   ├── apps/                       # HTML 应用管理
│   │   ├── manager.go + history.go #   生命周期 + 版本历史
│   │   ├── clone/ / pack/ / sanitize/ / server/ / mcpapps/
│   ├── ard/                        # ARD 资源发现 (15文件)
│   │   └── {urn,catalog,registry,federation,tools,server,client,trust}.go
│   ├── {topofmind,health,eval,telemetry,observability,artifact,project}/
│   └── util/                       # DatabasePool/MultiPool/Logger
└── docs/                           # 文档
    ├── README.md                   #   项目概览
    ├── ARCHITECTURE.md             #   系统架构深度分析 (18章, 28 ADR)
    └── CONFIG.md                   #   配置参考手册 (38配置段)
```

---

## 文档

| 文档 | 说明 |
|------|------|
| [项目概述](docs/README.md) | 架构哲学、核心优势、特性矩阵 |
| [架构分析](docs/ARCHITECTURE.md) | 18章完整架构、子系统设计、数据流、28 ADR、模块依赖图 |
| [配置手册](docs/CONFIG.md) | 38配置段完整参考、4种推荐方案、加载优先级 |

---

## 技术选型

| 类别 | 选择 | 版本 | 理由 |
|------|------|------|------|
| Agent框架 | tRPC-Agent-Go | v1.10.0 | 多Agent编排/Session/Memory/Planner/Skill/TodoEnforcer |
| MCP协议 | tRPC-MCP-Go | v0.0.16 | stdio/SSE/Streamable 三传输 |
| A2A协议 | tRPC-A2A-Go | v0.2.5 | Agent间标准通信 |
| 智能记忆 | CortexDB | v2.25.0 | HNSW+FTS5+RDF/SPARQL |
| JS引擎 | goja | latest | 纯Go零CGO, 多层沙箱安全 |
| OS沙箱 | pkg/sandbox | 自维护 | Landlock/Seatbelt/Low IL, 无需Docker |
| 数据库 | SQLite WAL | single-file | 单文件零配置, WAL并发读写 |
| 前端TUI | BubbleTea+LipGloss | latest | 纯Go, 零外部依赖 |
| 浏览器 | Chromedp | latest | Chrome DevTools Protocol |
| 配置 | Viper+Cobra | latest | CLI > ENV > YAML |
| 可观测 | OpenTelemetry+Langfuse | latest | 全链路+LLM专用追踪 |
| 文件监听 | fsnotify | v1.8.0 | Recipe热重载 |
| 模板 | text/template | stdlib | Recipe prompt/instruction渲染 |
| HTML清洗 | golang.org/x/net/html | latest | DOM树安全遍历+正则回退 |
| 压缩 | zstd(klauspost/compress) | v1.18.6 | ZIM文本簇压缩 |
| 语言 | Go | 1.26 | 跨平台单二进制分发 |

## 许可证

GNU AGPL-3.0 — 见 [LICENSE](docs/LICENSE)
