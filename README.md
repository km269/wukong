# Wukong 🐵

> **本地优先、可扩展的 AI Agent 平台** | Go 语言 | tRPC 生态系统

**Wukong** 是一个本地优先、可扩展的 AI Agent 平台，灵感来源于 [Goose](https://github.com/aaif-goose/goose)。它基于 [tRPC](https://github.com/trpc-group) 生态系统的三个核心框架构建，提供完整的 AI Agent 体验。

---

## 核心框架

| 框架 | 版本 | 用途 |
|------|------|------|
| [tRPC-Agent-Go](https://github.com/trpc-group/trpc-agent-go) | v1.10.0 | Agent 核心：Runner、Session、Memory、Tool 系统 |
| [tRPC-MCP-Go](https://github.com/trpc-group/trpc-mcp-go) | v0.0.16 | MCP 协议实现，支持外部工具扩展 |
| [tRPC-A2A-Go](https://github.com/trpc-group/trpc-a2a-go) | v0.2.5 | Agent-to-Agent 通信协议 |

---

## 功能特性

- **交互式 Agent 循环** — 用户请求 → LLM 思考 → 工具调用 → 结果 → 响应
- **MCP 扩展系统** — 8 个内置扩展 + 外部 MCP 服务器支持
- **多模型支持** — OpenAI、Anthropic、Google Gemini、DeepSeek、Ollama、LMStudio
- **长期记忆** — SQLite 持久化 + 自动记忆提取 + 手动记忆管理
- **跨会话搜索** — FTS5 全文搜索聊天历史
- **子代理委派** — Summon 系统 + Agent Skill 系统 + A2A 远程代理
- **安全防护** — 4 级权限模式 + 命令拦截 + 恶意软件扫描
- **上下文优化** — Token 管理 + 自动摘要 + 上下文裁剪
- **任务跟踪** — Todo 系统 (创建/更新/完成/列表)
- **持久化指令** — Top of Mind 系统，跨会话注入指令
- **现代 TUI** — Bubbletea + Lipgloss 终端界面，流式输出
- **可观测性** — OpenTelemetry 分布式追踪 + 健康检查端点

---

## 快速开始

### 前置条件

- Go 1.26+
- LLM API Key（或本地 Ollama/LMStudio）

### 安装

```bash
git clone https://github.com/km269/wukong.git
cd wukong
go build -o wukong ./cmd/wukong/
```

### 配置

创建 `~/.config/wukong/config.yaml`：

```yaml
default_provider: openai

providers:
  - name: openai
    type: openai
    base_url: https://api.openai.com/v1
    api_key: ${OPENAI_API_KEY}
    model: gpt-4o

extensions:
  - name: developer
    type: builtin
    enabled: true
  - name: memory
    type: builtin
    enabled: true

session:
  backend: sqlite
  db_path: wukong.db
```

> 完整配置参考：[CONFIG.md](CONFIG.md)

### 使用

```bash
# 启动交互式会话
./wukong session

# 使用特定提供商
./wukong session --provider deepseek

# 恢复之前的会话
./wukong session --session-id <session-id>

# 交互式配置向导
./wukong configure

# 管理扩展
./wukong extension list
./wukong extension install <deeplink-url>
```

### TUI 操作

| 操作 | 说明 |
|------|------|
| 输入消息 + `Ctrl+D` | 发送消息 |
| `Ctrl+C` | 退出 |
| `/new` | 开始新会话 |
| `/clear` | 清屏 |
| `/model` | 查看当前模型 |
| `/model <name>` | 切换模型 |
| `/exts` | 查看可用扩展 |
| `/help` | 显示帮助 |
| `/exit` | 退出 |

---

## 内置工具

### 开发者工具 (developer)

| 工具 | 功能 |
|------|------|
| `file_read` | 读取文件内容 |
| `file_write` | 写入文件 |
| `file_replace` | 查找替换文本 |
| `command_execute` | 执行 Shell 命令 |
| `code_search` | 代码搜索 (rg) |
| `directory_list` | 列出目录 |

### 记忆工具 (memory)

| 工具 | 功能 |
|------|------|
| `memory_add` | 添加记忆 |
| `memory_search` | 搜索记忆 |
| `memory_update` | 更新记忆 |
| `memory_delete` | 删除记忆 |
| `memory_load` | 加载所有记忆 |
| `memory_clear` | 清空记忆 |

### 计算机控制 (computer_controller)

| 工具 | 功能 |
|------|------|
| `web_fetch` | HTTP 获取网页内容 |
| `file_cache` | 下载并缓存文件 |
| `browser_navigate` | 浏览器导航 |
| `browser_extract` | 提取页面文本 |
| `browser_screenshot` | 页面截图 |

### 可视化 (auto_visualiser)

| 工具 | 功能 |
|------|------|
| `visualiser_chart` | 生成图表 (SVG) |
| `visualiser_diagram` | 生成流程图 (Mermaid) |
| `visualiser_table` | 生成表格 (HTML) |

### 其他工具

| 系统 | 工具 |
|------|------|
| Todo | `todo_create`, `todo_update`, `todo_list`, `todo_complete`, `todo_delete` |
| Recall | `recall_search`, `recall_sessions` |
| Tutorial | `tutorial_start`, `tutorial_list`, `tutorial_step` |

---

## 项目结构

```
wukong/
├── cmd/wukong/main.go              # 入口
├── internal/
│   ├── agent/                      # 核心 Agent 循环 + 上下文管理 + 工作流
│   ├── apps/                       # 自定义 HTML 应用
│   ├── browser/                    # 浏览器自动化 (HTTP + Chromedp)
│   ├── cli/                        # CLI 命令 + TUI 界面
│   ├── codemode/                   # JavaScript 沙箱 (goja)
│   ├── config/                     # 配置加载器 (Viper)
│   ├── extension/                  # MCP 扩展系统
│   │   └── builtin/                # 内置扩展实现
│   ├── health/                     # 健康检查端点
│   ├── memory/                     # 记忆管理器
│   ├── provider/                   # LLM 模型工厂
│   ├── recall/                     # 跨会话聊天搜索 (FTS5)
│   ├── security/                   # 安全守卫
│   ├── session/                    # 会话服务
│   ├── skill/                      # Agent Skill 系统
│   ├── summon/                     # 子代理委派 + A2A 凭证
│   ├── telemetry/                  # OpenTelemetry 遥测
│   ├── todo/                       # 任务跟踪系统
│   ├── topofmind/                  # 持久化指令注入
│   └── util/                       # 工具库 (DB 连接池、日志、指针)
├── config.yaml                     # 默认配置
├── Makefile / Taskfile.yaml        # 构建脚本
├── ARCHITECTURE.md                 # 架构文档
├── CONFIG.md                       # 配置说明
└── README.md                       # 本文档
```

---

## 架构

```
┌──────────────────────────────────────────────┐
│          CLI / TUI (Cobra + Bubbletea)        │
├──────────────────────────────────────────────┤
│              Wukong Core Engine               │
├──────────────────────────────────────────────┤
│ Agent Loop  │ Context Mgr │ Extension Mgr     │
├──────────────────────────────────────────────┤
│         tRPC-Agent-Go Runner                  │
├──────┬──────┬──────┬───────┬─────────────────┤
│ LLM  │Session│Memory│ Tool  │ Callbacks       │
│Agent │Service│Service│System │                 │
├──────┴──────┴──────┼───────┴─────────────────┤
│   tRPC-MCP-Go      │   tRPC-A2A-Go           │
│   (MCP Client)     │   (Agent-to-Agent)      │
└────────────────────┴─────────────────────────┘
```

详细架构说明请参阅 [ARCHITECTURE.md](ARCHITECTURE.md)。

---

## 与 Goose 对比

| 维度 | Goose (Rust) | Wukong (Go) |
|------|-------------|-------------|
| 语言 | Rust | Go |
| Agent 引擎 | 自定义 Rust 循环 | tRPC-Agent-Go Runner |
| MCP 客户端 | goose-mcp crate | tRPC-MCP-Go |
| Session | 自定义实现 | tRPC Session (SQLite) |
| Memory | 内置扩展 | tRPC Memory (SQLite + 自动提取) |
| TUI | Electron + Rust | Bubbletea (纯 Go) |
| Providers | 15+ Rust providers | OpenAI 兼容 API |
| 配置 | ~/.config/goose/config.yaml | ~/.config/wukong/config.yaml |

---

## 开发

```bash
# 构建
make build          # 或 task build

# 运行测试
make test           # 或 task test

# 代码检查
make lint           # 或 task lint

# 运行
make run            # 或 task run

# 构建所有平台
make build-all      # 或 task build-all
```

---

## 许可证

本项目仅供学习和研究使用。

## 致谢

- [Goose](https://github.com/aaif-goose/goose) — 启发本项目的原始 AI Agent
- [tRPC-Agent-Go](https://github.com/trpc-group/trpc-agent-go) — Go Agent 核心框架
- [tRPC-MCP-Go](https://github.com/trpc-group/trpc-mcp-go) — MCP 协议实现
- [tRPC-A2A-Go](https://github.com/trpc-group/trpc-a2a-go) — A2A 协议实现
