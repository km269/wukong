# Wukong 配置参考手册

> **配置文件**: `config.yaml` | **加载器**: Viper (spf13/viper) | **配置段**: 30+ | **CLI+ENV覆盖支持**

---

## 配置加载机制

```
优先级（从高到低）:
1. CLI命令行参数 (--provider, --model, --temperature, --max-tokens, --no-stream, --session-id, --config)
2. 环境变量 (WUKONG_ 前缀, 如 WUKONG_DEFAULT_PROVIDER)
3. 当前目录 ./config.yaml
4. ~/.config/wukong/config.yaml
5. /etc/wukong/config.yaml
6. 内置默认值 (setDefaults())
```

环境变量引用语法：`${ENV_VAR}`（配置文件运行时自动展开）

---

## 1. 全局设置

```yaml
log_level: "info"              # 日志级别: debug | info | warn | error
                               # CLI --debug/--quiet 会覆盖此设置
```

---

## 2. Provider 配置

支持6种类型：`openai`, `anthropic`, `google`, `deepseek`, `ollama`, `lmstudio`

```yaml
default_provider: "openai"     # 默认使用的 Provider（必须匹配下方某个 name）

providers:
  - name: "openai"             # Provider 唯一标识
    type: "openai"             # 类型
    api_key: "${OPENAI_API_KEY}" # API密钥（支持环境变量展开）
    base_url: "https://api.openai.com/v1"  # API端点（不填则使用类型默认值）
    model: "gpt-4o"            # 默认模型

  - name: "anthropic"
    type: "anthropic"
    api_key: "${ANTHROPIC_API_KEY}"
    model: "claude-sonnet-4-20250514"

  - name: "google"
    type: "google"
    api_key: "${GOOGLE_API_KEY}"
    model: "gemini-2.5-flash"

  - name: "deepseek"
    type: "deepseek"
    api_key: "${DEEPSEEK_API_KEY}"
    model: "deepseek-chat"

  - name: "ollama"
    type: "ollama"
    api_key: "ollama"
    base_url: "http://localhost:11434/v1"
    model: "llama3"

  - name: "lmstudio"
    type: "lmstudio"
    api_key: "lmstudio"
    base_url: "http://localhost:1234/v1"
    model: "your-model-name"
```

| Provider | 默认 Base URL |
|----------|-------------|
| openai | `https://api.openai.com/v1` |
| anthropic | `https://api.anthropic.com/v1` |
| google | `https://generativelanguage.googleapis.com/v1beta/openai` |
| deepseek | `https://api.deepseek.com/v1` |
| ollama | `http://localhost:11434/v1` |
| lmstudio | `http://localhost:1234/v1` |

---

## 3. Agent 配置

```yaml
agent:
  # ---- 运行限制 ----
  max_llm_calls: 50                     # 每次运行最大 LLM 调用次数 (0=不限)
  max_tool_iterations: 30               # 最大工具迭代次数
  max_run_duration: "300s"              # 单次运行最大时长

  # ---- 生成参数 ----
  temperature: 0.7                      # 采样温度 (0.0 - 2.0)
  max_tokens: 4096                      # 每次调用最大输出 token 数
  streaming: true                       # 流式输出
  parallel_tools: true                  # 并行工具执行

  # ---- 工具重试 ----
  tool_retry_enabled: true              # 启用工具调用自动重试
  tool_retry_max_attempts: 3            # 最大重试次数
  tool_retry_initial_wait: "1s"         # 初始等待时间
  tool_retry_backoff_factor: 2.0        # 退避倍数 (指数增长)
  enable_post_tool_prompt: true         # 每次工具结果后注入提示

  # ---- Planner（规划与推理） ----
  # builtin: 适合支持原生thinking的模型 (Claude/Gemini/OpenAI o-series)
  # react:   适合不支持原生thinking的模型 (通过标签引导思考)
  # 留空:    不启用planner
  planner: ""
  # reasoning_effort: "medium"          # low / medium / high (仅 builtin planner)

  # ---- 工具搜索 (TopK自动筛选，减少token消耗) ----
  tool_search_enabled: false
  # tool_search_max_tools: 20

  # ---- 上下文压缩 ----
  # 启用后执行两遍压缩：
  #   Pass 1: 将旧的超大工具结果替换为占位符
  #   Pass 2: 对剩余超大结果进行首尾截断
  context_compaction: true
  # context_compaction_tool_result_max_tokens: 1024  # Pass 1 阈值
  # context_compaction_oversized_max_tokens: 8192    # Pass 2 阈值 (0=禁用)
  # context_compaction_keep_recent: 1                # 保护最近N个请求
  # context_compaction_force_clean_tools:            # 强制占位符化的工具
  #   - "shell"
  #   - "grep"
  # context_compaction_keep_tools:                   # 始终保留结果的工具
  #   - "memory_search"
  #   - "session_search"

  # ---- 会话回溯 ----
  session_recall_enabled: false         # 跨会话上下文预加载
  # session_recall_limit: 5

  # ---- JSON修复 ----
  json_repair_enabled: false            # 修复非标准JSON工具参数

  # ---- Todo工具 ----
  todo_tool_enabled: true               # tRPC原生 todo_write 工具
  todo_enforcer_enabled: true           # TodoEnforcer: 确保所有todo完成后才给最终答案

  # ---- Agent Tools (子代理) ----
  agent_tools_enabled: true             # 启用子Agent工具 (code-reviewer等)
  # agent_tools_stream: true            # 启用子Agent结果流式输出

  # ---- 提示词模板 ----
  system_prompt_dir: "~/.config/wukong/prompts/"  # 自定义 .md 提示词模板目录

  # ---- YAML Recipe 子代理 ----
  # recipe_dir: ".wukong/recipes/"      # Recipe 定义目录
  # recipe_enabled: true
```

| Agent 参数 | 默认值 | 说明 |
|-----------|--------|------|
| `max_llm_calls` | 50 | 每次运行最大LLM调用次数 |
| `max_tool_iterations` | 30 | 最大工具迭代次数 |
| `parallel_tools` | true | 并行工具执行 |
| `streaming` | true | 流式输出 |
| `max_run_duration` | 300s | 单次运行超时 |
| `temperature` | 0.7 | 采样温度 |
| `max_tokens` | 4096 | 单次最大输出token |
| `tool_retry_enabled` | true | 工具重试开关 |
| `tool_retry_max_attempts` | 3 | 最大重试次数 |
| `tool_retry_initial_wait` | 1s | 首次重试等待 |
| `tool_retry_backoff_factor` | 2.0 | 退避倍数 |
| `enable_post_tool_prompt` | true | 工具后提示 |
| `planner` | "" | builtin / react / 空 |
| `tool_search_enabled` | false | TopK工具筛选 |
| `tool_search_max_tools` | 20 | 最大暴露工具数 |
| `context_compaction` | false | 上下文压缩 |
| `session_recall_enabled` | false | 跨会话预加载 |
| `json_repair_enabled` | false | JSON修复 |
| `todo_tool_enabled` | true | tRPC todo_write |
| `todo_enforcer_enabled` | true | 任务强制完成 |
| `agent_tools_enabled` | true | 子Agent工具 |
| `agent_tools_stream` | false | 子Agent流式输出 |
| `system_prompt_dir` | ~/.config/wukong/prompts/ | 提示词模板目录 |
| `recipe_enabled` | true | YAML Recipe加载 |

---

## 4. Security 安全配置

```yaml
security:
  malware_scan_enabled: true            # 外部扩展恶意软件扫描
  default_timeout: "30s"                # 默认工具超时
  max_timeout: "300s"                   # 最大工具超时（硬上限）
  block_dangerous_commands: true        # 阻止危险Shell命令
  blocked_commands:                     # 被阻止的命令模式
    - "rm -rf /"
    - "dd if=/dev/zero"
    - "mkfs."
    - "> /dev/sda"
    - "fork bomb"
  require_approval: false               # (遗留) 全局审批要求

  # 权限模式
  permission_mode: "smart"              # auto | smart | manual | chat_only

  # 工具白名单/黑名单（*通配符）
  allowlist: []                         # 非空时：仅允许列表内工具
  denylist: []                          # 始终阻止的工具

  # Guardrail - Prompt 注入检测（增加延迟）
  guardrail_enabled: false

  # .wukongignore - 文件访问黑名单（gitignore兼容）
  ignore_file_enabled: true
  ignore_file: ".wukongignore"
```

### 权限模式说明

| 模式 | 行为 | 使用场景 |
|------|------|---------|
| `auto` | 所有工具自动执行 | 信任环境/自动化 |
| `smart` | 仅高风险操作需要审批 | **默认，推荐** |
| `manual` | 每次工具调用都需审批 | 高度安全需求 |
| `chat_only` | 禁止所有工具调用 | 纯对话模式 |

### 高风险工具清单（smart模式需审批）

**命令执行**: `bash`, `execute_command`, `run_command`, `shell`, `terminal`, `command`, `command_execute`

**文件写入**: `file_write`, `file_replace`, `file_delete`

**浏览器操作**: `browser_navigate`, `browser_screenshot`, `browser_click`, `browser_fill`

**Web请求**: `web_fetch`

---

## 5. Session 会话配置

```yaml
session:
  backend: "sqlite"              # 后端: sqlite | memory | redis
  db_path: "wukong.db"           # SQLite数据库路径
  event_limit: 500               # 每会话最大事件数
  ttl: "0h"                      # 会话过期时间 (0=不过期)
  enable_summary: true           # 启用自动摘要
  summary_trigger: 50            # 触发摘要的事件数阈值

  # Redis配置 (仅 backend="redis" 时生效)
  # redis_url: "redis://localhost:6379/0"    # Redis连接URL
  # 格式: redis://[user:pass@]host:port[/db]
```

---

## 6. Memory 长期记忆配置

```yaml
memory:
  backend: "sqlite"              # 后端: sqlite | memory
  db_path: "wukong.db"           # SQLite数据库路径
  max_memories: 100              # 每用户最大记忆数
  auto_extract: true             # 启用自动提取（LLM从对话中提取记忆）
  extract_timeout: "120s"        # 提取超时

  # 可选的独立提取模型（推荐使用更小/更快的模型降本增效）
  # extractor_provider: "deepseek"
  # extractor_model: "deepseek-chat"

  # 自定义提取Prompt（推荐本地小模型使用精简版）
  extractor_prompt: |            # 留空使用框架默认（280+行）
    You are a Memory Manager. Extract concise memories from the conversation.
    Today's date is {current_date}. Resolve ALL relative time references to absolute dates.
    <rules>
    1. Atomicity: One fact per memory.
    2. No subject prefix: Never start with "User", "The user", etc.
    3. Deduplication: Check existing memories before adding.
    4. Specificity: Include names, dates, places, quantities.
    5. Episode vs Fact: Use memory_kind appropriately.
    6. All speakers: Extract info about EVERY person.
    7. Skip transient: Don't create memories for greetings.
    </rules>
    Write memory content in the same language as the user's input.
    Call multiple tools in parallel for efficiency.
```

### 记忆工具列表

| 工具 | 说明 |
|------|------|
| `memory_add` | 添加记忆 |
| `memory_search` | 搜索记忆 |
| `memory_update` | 更新记忆 |
| `memory_delete` | 删除记忆 |
| `memory_load` | 加载所有记忆 |
| `memory_clear` | 清除所有记忆 |

### 自动预加载
每次对话开始时，自动将最多10条用户记忆注入系统提示词，让Agent自动了解偏好和事实。

---

## 7. Todo 任务跟踪配置

```yaml
todo:
  backend: "sqlite"              # 后端: sqlite | memory
  db_path: "wukong.db"           # SQLite数据库路径
  # enable_native_todo: true     # 启用 tRPC 原生 todo_write
  # enable_enforcer: true        # 启用 TodoEnforcer
```

支持两种模式（可同时启用）：
1. **自定义SQLite工具**: `todo_create`, `todo_update`, `todo_list`, `todo_complete`, `todo_delete`（精细管理）
2. **tRPC原生todo_write + TodoEnforcer**: 基于Session持久化，强制完成校验（默认启用）

---

## 8. Recall 会话回溯配置

```yaml
recall:
  enabled: true                  # 启用跨会话搜索
  backend: "sqlite"              # 后端: sqlite | memory
  db_path: "wukong.db"
  max_results: 10                # 最大搜索结果数
  max_messages_per_session: 200  # 每会话最大存储消息数

  # 搜索模式: "fts5" (全文搜索), "hybrid" (语义+全文混合)
  search_mode: "fts5"

  # hybrid 模式需要 embedding 配置
  # embedding_provider: "openai"           # 用于生成 embedding 的 provider
  # embedding_model: "text-embedding-3-small"
```

| 搜索模式 | 说明 |
|---------|------|
| `fts5` | 纯FTS5全文搜索（默认，零配置） |
| `hybrid` | 语义搜索 + FTS5混合排序（需要embedding provider） |

---

## 9. Revision 上下文修订配置

```yaml
revision:
  enabled: true                           # 启用上下文窗口管理
  revision_provider: ""                   # 修订模型Provider（空=使用默认）
  revision_model: ""                      # 修订模型名称
  max_command_output: 8000                # 命令输出最大保留字节数
  enable_semantic_search: false           # 语义搜索（实验性）
  search_strategy: "include_all"          # 搜索策略: include_all | semantic
  max_context_tokens: 64000               # 上下文Token软限制
  trim_ratio: 0.3                         # 裁剪比例 (0.0-1.0)
```

3种触发修订条件：
1. 估算Token超过 `max_context_tokens × (1 - trim_ratio)`
2. 消息数超过100条
3. 距上次修订超过5分钟

---

## 10. Browser 浏览器配置

```yaml
browser:
  enabled: true                            # 启用浏览器自动化
  browser_type: "chromium"                 # 浏览器类型: chromium
  headless: true                           # 无头模式
  cache_dir: ".wukong_cache"               # 文件缓存目录
  max_download_size: 104857600             # 最大下载大小 (100MB)
  timeout: "60s"                           # HTTP请求超时
  # browser_path: ""                       # 自定义Chrome路径

  # 视口设置
  viewport_width: 1280
  viewport_height: 720

  # Web搜索后端
  search_backend: "duckduckgo"             # duckduckgo | searxng | tavily
  # search_backend_url: ""                 # SearXNG实例URL
  # search_api_key: ""                     # Tavily API Key
```

### Computer Controller 工具列表（9个）

| 工具 | 说明 | 模式 |
|------|------|------|
| `web_fetch` | HTTP获取网页内容（1MB限制） | HTTP |
| `file_cache` | 下载并缓存文件 | HTTP |
| `cache_list` | 列出缓存文件 | 本地 |
| `cache_clear` | 清空缓存 | 本地 |
| `browser_navigate` | 导航并提取页面内容 | Chromedp |
| `browser_extract` | 提取页面清洁文本 | Chromedp |
| `browser_screenshot` | 保存页面HTML快照 | Chromedp |
| `browser_click` | 点击元素 (CSS选择器) | Chromedp |
| `browser_fill` | 填充表单 (CSS选择器+值) | Chromedp |

---

## 11. Visualiser 可视化配置

```yaml
visualiser:
  enabled: true                 # 启用自动可视化
  output_dir: ".wukong_visuals" # 输出目录
  max_width: 1200               # 最大图表宽度
  max_height: 800               # 最大图表高度
```

### Visualiser 工具列表（3个）

| 工具 | 支持类型 |
|------|---------|
| `visualiser_chart` | bar, line, pie, scatter, flow (SVG) |
| `visualiser_diagram` | flowchart, sequence, architecture, ER, class (Mermaid / HTML) |
| `visualiser_table` | HTML table |

---

## 12. Tutorial 教程配置

```yaml
tutorial:
  enabled: true                 # 启用交互式教程
  language: "zh"                # 语言: zh | en
```

内置教程：git, docker, go（支持 `.wukong_tutorials/*.md` 自定义教程文件）

---

## 13. Top of Mind 配置

```yaml
top_of_mind:
  enabled: true                           # 启用持久化指令注入
  instruction_file: ".wukong_instructions.md"  # 指令文件路径
  max_length: 2000                        # 最大指令长度（字符数）
```

指令从文件加载，支持自动热重载（文件变更检测 + 双重检查锁）。

---

## 14. Code Mode 配置

```yaml
code_mode:
  enabled: true                 # 启用JS沙箱
  timeout: "10s"                # 执行超时
  max_memory_mb: 128            # 内存限制（预留）
```

使用 [goja](https://github.com/dop251/goja) 纯Go JavaScript引擎，沙箱限制：
- 禁用 `eval`, `Function`, `setInterval`
- 提供安全 `console.log`, `JSON`, `Math` 实现
- 确定性 `Math.random()` (始终返回0.5)

---

## 15. Apps 配置

```yaml
apps:
  enabled: true                 # 启用HTML应用管理
  app_dir: ".wukong_apps"       # 应用存储目录
```

### Apps 工具列表（5个）

| 工具 | 说明 |
|------|------|
| `app_create` | 创建新的HTML应用 |
| `app_list` | 列出所有应用 |
| `app_get` | 获取应用详情 |
| `app_update` | 更新应用内容 |
| `app_delete` | 删除应用 |

---

## 16. Summon 配置

```yaml
summon:
  enabled: true                 # 启用子代理调度
  skills_dir: ".wukong_skills"  # Skill定义目录
  max_concurrent: 5             # 最大并发子代理数

  # A2A远程代理
  a2a_remotes: []
  # 示例:
  #   - name: "code-reviewer"
  #     description: "Reviews code for quality and security"
  #     server_url: "http://localhost:8081"
  #     auth_type: "api_key"          # api_key | jwt | oauth2
  #     api_key: "${A2A_API_KEY}"
  #     # api_key_header: "X-API-Key" # 自定义Header
```

### 内置子代理

| 名称 | 说明 | 配置 |
|------|------|------|
| `code-reviewer` | 代码质量审查专家 | Temperature 0.3, MaxTokens 2048, MaxLLMCalls 3 |
| `summarizer` | 内容摘要专家 | Temperature 0.3, MaxTokens 1024, MaxLLMCalls 2 |
| `code-generator` | 代码生成专家 | Temperature 0.2, MaxTokens 4096, MaxLLMCalls 3 |

---

## 17. Skill 配置

```yaml
skill:
  enabled: true                       # 启用Agent Skill系统
  skills_dir: ".wukong_agent_skills"  # SKILL.md文件目录
  auto_load: true                     # 启动时自动加载
  max_skills: 20                      # 最大加载Skill数
```

Skill使用tRPC-Agent-Go的`FSRepository`格式：每个Skill是目录，包含`SKILL.md`和可选的doc文件。

```markdown
<!-- .wukong_agent_skills/my-skill/SKILL.md -->
---
name: my-skill
description: 我的自定义技能
---
你是一个专注于XX领域的专家Agent...
```

---

## 18. Knowledge (RAG) 配置

```yaml
knowledge:
  enabled: false                         # 启用RAG知识库（默认关闭）

  # Embedding模型配置
  # embedder_provider: "openai"          # Embedding Provider (空=使用默认)
  embedder_model: "text-embedding-3-small" # 1536维向量

  # 文档源
  # sources:                             # 本地文档目录（递归扫描）
  #   - "./docs"
  #   - "./README.md"
  # source_urls: []                      # 远程URL文档

  vector_store: "inmemory"               # 向量存储: inmemory
  max_results: 5                         # 每次查询返回结果数
  enable_source_sync: false              # 源文件变更自动重新索引
  reranker_enabled: false               # 结果重排序（实验性）
  search_tool_name: "knowledge_search"   # Agent工具名
```

支持文档格式：txt, md, pdf, csv, json, docx

---

## 19. Workflow 工作流配置

```yaml
workflow:
  mode: "single"              # single | chain | parallel | cycle | graph
                              # team_coordinator | team_swarm | claude_code | codex | dify
  max_iterations: 10          # cycle/graph模式最大迭代次数

  # cycle模式选择
  cycle_mode: "default"       # default (planner↔executor) | code_review (generator↔reviewer)

  # Graph高级特性
  stream_mode: "none"         # none | hub (节点间流式通信)
  cache_enabled: false        # 节点缓存 (纯函数节点重复计算)
  engine: "bsp"               # bsp | dag (执行引擎)

  # 自定义子Agent
  sub_agents: []
  # 示例:
  #   - name: "planner"
  #     instruction: "You are a planning specialist..."
  #     allowed_tools: ["file_read", "code_search"]

  # Team模式成员 (mode="team_coordinator" 或 "team_swarm" 时生效)
  team_members: []
  # 示例:
  #   - name: "researcher"
  #     instruction: "You are a research specialist..."
  #   - name: "coder"
  #     instruction: "You are a coding specialist..."
  #   - name: "reviewer"
  #     instruction: "You are a quality reviewer..."

  # Claude Code CLI (mode="claude_code"时生效)
  # claude_code_bin: "claude"
  # Codex CLI (mode="codex"时生效)
  # codex_bin: "codex"
```

---

## 20. Dify 平台集成

```yaml
dify:
  enabled: false                       # 启用Dify集成
  # base_url: "https://api.dify.ai/v1" # Dify API端点
  # api_secret: "${DIFY_API_SECRET}"   # Dify API密钥
  agent_name: "dify"                   # 代理名称
  enable_streaming: false              # SSE流式输出
  timeout: "120s"                      # 请求超时
```

---

## 21. A2A Server 配置

```yaml
a2a_server:
  enabled: false                       # 启用A2A协议服务
  address: ":9090"                     # 监听地址
  agent_name: "wukong"                 # A2A协议暴露的Agent名称
  agent_description: "Wukong AI Agent - A2A service endpoint"
```

使用 tRPC-A2A-Go 实现，自动处理协议转换、流式支持和会话集成。

---

## 22. AG-UI Server 配置

```yaml
agui:
  enabled: true #false                 # 启用AG-UI SSE服务
  address: ":8080"                     # 监听地址
  path: "/agui"                        # SSE端点路径
```

兼容 AG-UI 协议规范，支持 CopilotKit、TDesign Chat 等客户端。提供 `/health` 健康检查端点。

---

## 23. Extensions 扩展管理

```yaml
extensions: []                         # 外部MCP扩展列表（内置扩展自动注册）

# 示例:
#   # stdio 传输
#   - name: "filesystem"
#     type: "external"
#     transport: "stdio"
#     command: "npx"
#     args: ["-y", "@anthropic/mcp-server-filesystem", "/tmp"]
#     enabled: true
#     timeout: "30s"
#     # 高级选项:
#     env:                              # 自定义环境变量
#       MY_VAR: "value"
#     mcp_tool_filter:                  # 只包含匹配的工具 (glob)
#       - "read_*"
#     mcp_tool_exclude:                 # 排除匹配的工具 (glob)
#       - "delete_*"
#     mcp_broker: false                 # 设为 true 则通过 MCP Broker 按需发现
#
#   # SSE 传输
#   - name: "remote-api"
#     type: "external"
#     transport: "sse"
#     url: "http://localhost:3001/sse"
#     enabled: true
#     mcp_session_reconnect: true       # 自动重连
#     mcp_session_reconnect_attempts: 3 # 最大重连次数
#
#   # 内置扩展（通常不需要手动配置，自动注册）
#   - name: "developer"
#     type: "builtin"
#     enabled: true
```

### 配置自定义内置扩展

如果需要禁用某个内置扩展：

```yaml
extensions:
  - name: "tutorial"          # 覆盖内置注册
    type: "builtin"
    enabled: false            # 禁用教程扩展
```

---

## 24. Telemetry 遥测

```yaml
telemetry:
  enabled: false                       # 启用OpenTelemetry分布式追踪
  exporter_type: "console"             # grpc | http | console
  endpoint: "localhost:4317"           # OTLP collector地址
  service_name: "wukong"               # 服务名称
  service_version: "1.0.0"             # 服务版本
  environment: "development"           # deployment | staging | production
  sample_rate: 1.0                     # 采样率 (0.0-1.0, 1.0=全量)
```

---

## 25. Observability 可观测性

```yaml
observability:
  langfuse_enabled: false              # 启用Langfuse LLM追踪
  # langfuse_host: ""                  # Langfuse host (不含 http://)
  # langfuse_public_key: ""            # Langfuse公钥
  # langfuse_secret_key: ""            # Langfuse密钥
```

也可以通过环境变量配置：`LANGFUSE_HOST`, `LANGFUSE_PUBLIC_KEY`, `LANGFUSE_SECRET_KEY`

---

## 26. Artifact 制品存储

```yaml
artifact:
  backend: "inmemory"                  # 后端: inmemory | cos

  # COS 配置 (仅 backend="cos" 时生效)
  # cos_bucket_url: "https://bucket.cos.region.myqcloud.com"
  # cos_secret_id: "${COS_SECRETID}"   # 或通过 COS_SECRETID 环境变量
  # cos_secret_key: "${COS_SECRETKEY}" # 或通过 COS_SECRETKEY 环境变量
```

---

## 27. Eval 评估

```yaml
eval:
  enabled: false                                # 启用自动评估
  evalset_path: ".wukong_evals/default.evalset.json"  # 测试用例文件
  results_path: ".wukong_evals/results.json"          # 结果输出

  # metrics:                                    # 评估指标
  #   - name: "tool_trajectory_match"
  #     threshold: 0.8
  #   - name: "response_not_empty"
  #     threshold: 1.0
  #   - name: "response_min_length"
  #     threshold: 0.5
```

---

## 28. Project 项目追踪

```yaml
# project_dir: "~/.config/wukong/"      # 项目数据存储目录
```

自动记录工作目录与最后会话，支持快速恢复。

---

## CLI 参数覆盖

| CLI 参数 | 覆盖配置项 | 示例 |
|---------|-----------|------|
| `-p / --provider` | `default_provider` | `--provider deepseek` |
| `-m / --model` | provider的`model` | `--model gpt-4o-mini` |
| `--temperature` | `agent.temperature` | `--temperature 0.5` |
| `--max-tokens` | `agent.max_tokens` | `--max-tokens 8192` |
| `--no-stream` | `agent.streaming = false` | `--no-stream` |
| `-s / --session-id` | session ID | `--session-id abc12345` |
| `-c / --config` | 配置文件路径 | `--config ./my-config.yaml` |

---

## 环境变量覆盖

```bash
# 所有配置项都可以通过 WUKONG_ 前缀的环境变量覆盖
export WUKONG_DEFAULT_PROVIDER=deepseek
export WUKONG_AGENT_TEMPERATURE=0.5
export WUKONG_AGENT_MAX_TOKENS=8192
export WUKONG_SECURITY_PERMISSION_MODE=auto
```
