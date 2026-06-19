# Wukong 配置参考手册

> **配置文件**: `config.yaml` | **加载器**: Viper | **配置段**: 30 | **覆盖**: CLI > ENV > YAML > 默认值
>
> 版本：v0.6.0

---

## 配置加载机制

```
优先级（从高到低）:
  1. CLI 命令行参数 (--provider, --model, --temperature, --max-tokens,
                     --no-stream, --session-id, --config, --debug, --quiet)
  2. 环境变量 (WUKONG_ 前缀, 点号替换为下划线.
              如 WUKONG_DEFAULT_PROVIDER → default_provider)
  3. 当前目录 ./config.yaml
  4. ~/.config/wukong/config.yaml
  5. /etc/wukong/config.yaml
  6. 内置默认值 (setDefaults())
```

环境变量引用语法：
```yaml
api_key: "${OPENAI_API_KEY}"    # 运行时自动展开
base_url: "https://api.openai.com/v1"  # 已知 Provider 自动填充
```

---

## 1. 全局设置

```yaml
log_level: "info"              # 日志级别: debug | info | warn | error
default_provider: "lmstudio"   # 默认 LLM Provider (需匹配下方 providers[].name)
```

CLI 覆盖：
```bash
wukong session --debug          # log_level → debug
wukong session --quiet          # log_level → error
```

---

## 2. Providers — LLM 模型提供商

```yaml
providers:
  - name: "openai"
    type: "openai"
    api_key: "${OPENAI_API_KEY}"
    base_url: "https://api.openai.com/v1"
    model: "gpt-4o"

  - name: "anthropic"
    type: "anthropic"
    api_key: "${ANTHROPIC_API_KEY}"
    base_url: "https://api.anthropic.com"
    model: "claude-sonnet-4-20250514"

  - name: "deepseek"
    type: "deepseek"
    api_key: "${DEEPSEEK_API_KEY}"
    base_url: "https://api.deepseek.com"
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
    model: "google/gemma-4-26b-a4b"

  - name: "acp-coder"
    type: "acp"
    agent_url: "http://localhost:4000"
    model: "acp-default"
```

| 字段 | 必填 | 说明 |
|------|------|------|
| `name` | ✅ | 唯一标识（被 default_provider/revision_provider/extractor_provider 引用） |
| `type` | ✅ | openai / anthropic / google / deepseek / ollama / lmstudio / acp |
| `api_key` | ✅ | API 密钥，支持 `${ENV_VAR}` |
| `base_url` | ❌ | API 端点，已知厂商自动填充 |
| `model` | ❌ | 默认模型名 |
| `max_tokens` | ❌ | Provider 级最大 token，覆盖 agent.max_tokens |

---

## 3. Agent — 核心行为配置

```yaml
agent:
  max_llm_calls: 50             # 单次对话最大 LLM 调用次数
  max_tool_iterations: 30       # 单次对话最大工具调用次数
  parallel_tools: true          # 允许并行工具调用
  streaming: true               # 流式输出
  max_run_duration: "300s"      # 单次运行最大时长
  temperature: 0.7              # 生成温度 (0-2)
  max_tokens: 4096              # 模型最大输出 token
```

### 3.1 工具重试

```yaml
  tool_retry_enabled: true
  tool_retry_max_attempts: 3
  tool_retry_initial_wait: "1s"
  tool_retry_backoff_factor: 2.0
```

### 3.2 Planner — 结构化推理

```yaml
  planner: ""                   # 留空=不启用
                                # "builtin" = 原生 thinking (Claude/Gemini/OpenAI o-series)
                                # "react"   = 标签引导思考 (通用模型)
  # reasoning_effort: "medium"  # low / medium / high (仅 builtin planner)
```

### 3.3 Tool Search — 自动工具筛选

```yaml
  tool_search_enabled: true     # 启用自动工具筛选，减少 token 消耗
  # tool_search_max_tools: 20   # 每次筛选保留的最大工具数
```

### 3.4 Context Compaction — 框架级上下文压缩

```yaml
  context_compaction: true
  # context_compaction_tool_result_max_tokens: 1024   # Pass 1 阈值
  # context_compaction_oversized_max_tokens: 8192     # Pass 2 阈值 (0=禁用)
  # context_compaction_keep_recent: 1                  # 保留最近 N 个请求
  # context_compaction_force_clean_tools:              # 强制占位符化的工具
  #   - "shell"
  #   - "grep"
  # context_compaction_keep_tools:                     # 始终保留结果的工具
  #   - "memory_search"
  #   - "session_search"
```

**双通道压缩**：
- **Pass 1**：旧的超大工具结果 → 替换为占位符（结果 < tool_result_max_tokens）
- **Pass 2**：剩余超大结果 → 首尾截断（结果 > oversized_max_tokens）

### 3.5 Session Recall — 跨会话上下文预加载

```yaml
  session_recall_enabled: false  # 启用跨会话上下文
  # session_recall_limit: 5
```

### 3.6 JSON Repair — 工具参数修复

```yaml
  json_repair_enabled: false    # 修复非标准 JSON 工具参数
```

### 3.7 Todo — 任务追踪

```yaml
  todo_tool_enabled: true       # tRPC 原生 todo_write 工具
  todo_enforcer_enabled: true   # 确保所有 todo 完成才给最终答案
```

**TodoEnforcer 行为**：当 Agent 尝试输出最终答案时，检测是否还有未完成的 todo。如果有，阻止输出并提示 Agent 先完成任务。

### 3.8 Agent Tools — 子 Agent 工具

```yaml
  agent_tools_enabled: false    # 将子 Agent 包装为可调用工具
                                # (code-reviewer / summarizer / code-generator)
  # agent_tools_stream: true    # 启用子 Agent 结果流式输出
```

**注意**：子 Agent 工具名会以 `system-` 前缀暴露。

### 3.9 YAML Recipe — 配置化子代理

```yaml
  # recipe_dir: ".wukong/recipes/"
  # recipe_enabled: true
```

每个 `.yaml` 文件定义一个命名子代理：

```yaml
# .wukong/recipes/code-reviewer.yaml
name: "code-reviewer"
instruction: "You are a code reviewer..."
model: "deepseek-chat"
tools:
  - "file_read"
  - "grep"
```

### 3.10 提示词管理

```yaml
  system_prompt_dir: "~/.config/wukong/prompts/"
  # 加载目录下所有 .md 文件拼接为 system instruction
  # 空目录或不存在时回退到内置默认提示词
```

---

## 4. Security — 安全策略

```yaml
security:
  malware_scan_enabled: true
  default_timeout: "30s"
  max_timeout: "300s"
  block_dangerous_commands: true
  blocked_commands:             # 自定义危险命令黑名单
    - "rm -rf /"
    - "dd if=/dev/zero"
    - "mkfs."
    - "> /dev/sda"
    - "fork bomb"
  require_approval: false
  permission_mode: "smart"      # auto | smart | manual | chat_only
  allowlist: []                 # 工具白名单 (如 ["developer/*", "memory_search"])
  denylist: []                  # 工具黑名单 (如 ["shell", "file_delete"])
```

### 4.1 Guardrail — 提示注入检测

```yaml
  guardrail_enabled: false      # 启用 Prompt 注入检测 (增加延迟)
```

### 4.2 文件访问控制

```yaml
  ignore_file_enabled: true     # 启用 .wukongignore
  ignore_file: ".wukongignore"  # gitignore 兼容语法
```

---

## 5. Session — 会话存储

```yaml
session:
  backend: "sqlite"             # sqlite / memory / redis
  db_path: "wukong.db"
  event_limit: 500              # 每会话最大事件数
  ttl: "0h"                     # 会话过期 (0=永不过期)
  enable_summary: true          # 启用框架级异步摘要
  summary_trigger: 50           # 触发摘要的事件数阈值
  # redis_url: "redis://localhost:6379/0"  # 仅 backend=redis
```

---

## 6. Memory — 长期记忆

```yaml
memory:
  backend: "sqlite"
  db_path: "wukong.db"
  max_memories: 100             # 每用户最大记忆数
  auto_extract: true            # 启用异步记忆提取
  extract_timeout: "600s"       # 单次提取超时
  extractor_provider: "lmstudio"          # 独立提取模型 provider
  extractor_model: "gemma-4-e4b-it"       # 轻量模型做提取
  extractor_prompt: |           # 自定义提取 Prompt
    You are a Memory Manager. Extract concise memories...
```

**AutoExtract 规则**（内置 Prompt）：
1. **原子性**：每个记忆一个事实
2. **无主语前缀**：不以前缀开头
3. **去重**：检查已有记忆
4. **特异性**：包含名称、日期、地点、数量
5. **类型区分**：Episode（事件+时间）vs Fact（属性/偏好）
6. **全参与者**：提取所有说话人的信息
7. **跳过瞬时**：不记录问候语等

**记忆工具**（6个）：
| 工具 | 功能 | SQL |
|------|------|-----|
| `memory_add` | 添加 | INSERT INTO memories |
| `memory_search` | 搜索 | SELECT ... LIKE |
| `memory_update` | 更新 | UPDATE memories |
| `memory_delete` | 删除 | DELETE FROM memories |
| `memory_load` | 全部加载 | SELECT * FROM memories |
| `memory_clear` | 清空 | DELETE FROM memories |

---

## 7. Todo — 任务追踪存储

```yaml
todo:
  backend: "sqlite"
  db_path: "wukong.db"
  enable_native_todo: true      # tRPC 原生 todo_write 工具
  enable_enforcer: true         # todoenforcer 强制完成校验
```

**两种任务管理模式**（可同时启用）：
1. **自定义 SQLite 工具**：`todo_create/update/list/complete/delete`（精细管理）
2. **tRPC 原生**：`todo_write` + TodoEnforcer（推荐，基于 Session 持久化）

---

## 8. Recall — 跨会话回溯

```yaml
recall:
  enabled: true
  backend: "sqlite"
  db_path: "wukong.db"
  max_results: 10
  max_messages_per_session: 200
  search_mode: "fts5"           # fts5 (全文搜索) | hybrid (语义+全文混合)
  # embedding_provider: "openai"    # hybrid 模式需要
  # embedding_model: "text-embedding-3-small"
```

| 搜索模式 | 说明 | 需求 |
|----------|------|------|
| `fts5` | SQLite FTS5 全文搜索（BM25） | 零配置 |
| `hybrid` | 语义搜索 + FTS5 混合重排 | embedding provider + model |

**回溯工具**（2个）：
| 工具 | 功能 |
|------|------|
| `recall_search` | 跨会话搜索历史消息 |
| `recall_sessions` | 列出历史会话 |

---

## 9. Cortex — 智能回溯（CortexDB）

```yaml
cortex:
  enabled: true                 # 启用 CortexDB 智能回溯
  db_path: "wukong.db"          # 合并到主库
  max_results: 10
  max_messages_per_session: 200

  # 向量嵌入配置
  embedding_base_url: "http://localhost:1234"
  embedding_api_key: "lmstudio"
  embedding_model: "qwen3-embedding-0.6b-graphql"
```

**与 recall 的关系**：启用 cortex 后，后台存储自动切换为 CortexDB 的 HNSW 向量索引 + FTS5 混合搜索。未启用时回退到原生 FTS5。

---

## 10. MemoryFlow — 对话记忆工作流

```yaml
memoryflow:
  enabled: true                 # 启用 MemoryFlow
  db_path: "wukong.db"          # 合并到主库
  namespace: "assistant"        # 记忆命名空间
  embedding_dimensions: 0       # 向量维度 (0=自动检测)
  planner_model: "gemma-4-e4b-it"          # 检索策略规划模型
  extractor_model: "gemma-4-e4b-it"        # 会话内容提取模型
```

| 能力 | 触发时机 | 说明 |
|------|----------|------|
| **IngestTurn** | 每轮对话前后 | 记录用户和助手的对话转录 |
| **WakeUp** | 每次 Agent.Run 前 | 向量+FTS5 搜索历史转录，注入 message 前缀 |
| **PromoteFacts** | 每次 Agent.Run 后 | 从转录提取候选事实，桥接到 tRPC Memory |

---

## 11. GraphFlow — 知识图谱

```yaml
graphflow:
  enabled: true                 # 启用知识图谱构建
  db_path: "wukong.db"          # 合并到主库
  max_chars_per_doc: 8000       # 单文档最大提取字符
  auto_extract: false           # 每轮对话后自动提取实体/关系
  extractor_model: "gemma-4-e4b-it"  # LLM 提取模型 (空=启发式)
```

| 能力 | 说明 |
|------|------|
| **实体提取** | 从对话转录中识别实体和关系 |
| **SPARQL 查询** | `knowledge_graph_query` 工具 |
| **图谱分析** | `knowledge_graph_analyze` 工具（统计+模式分析） |

---

## 12. ImportFlow — 结构化数据导入

```yaml
importflow:
  enabled: true                 # 启用数据导入
  db_path: "wukong.db"          # 合并到主库
```

**导入工具**（4个）：
| 工具 | 功能 |
|------|------|
| `importflow_ddl_parse` | 解析 CREATE TABLE DDL |
| `importflow_ddl_plan` | 生成导入计划（启发式） |
| `importflow_ddl_plan_ai` | 生成导入计划（LLM 优化） |
| `importflow_csv` | 导入 CSV 数据 |

---

## 13. Revision — 上下文压缩

```yaml
revision:
  enabled: true
  revision_provider: "lmstudio"       # 独立摘要模型 provider
  revision_model: "gemma-4-e4b-it"    # 轻量摘要模型
  enable_llm_summarize: true          # 启用 LLM 智能摘要
  summary_cooldown: 120s              # 渐进式摘要最小间隔
  summary_timeout: 30s                # 单次摘要超时
  max_command_output: 8000            # 命令输出最大字符
  enable_semantic_search: false       # 语义搜索辅助
  search_strategy: "include_all"      # include_all | semantic
  max_context_tokens: 64000           # 上下文窗口 token 上限
  trim_ratio: 0.3                     # 触发压缩的 token 比例
```

**三层压缩策略**：

| 层级 | 策略 | 触发条件 |
|------|------|----------|
| 1 | **LLM 智能摘要** | enable_llm_summarize=true + revision_model 已配置 |
| 2 | **渐进式压缩** | 已有摘要 + 新消息增量合并 |
| 3 | **算法截断** | LLM 不可用时回退 |

**三种触发条件**（任一满足即触发）：
1. 估算 token 超过 `max_context_tokens × (1 - trim_ratio)`
2. 消息数超过 100 条
3. 距上次修订超过 5 分钟

---

## 14. Browser — 浏览器自动化

```yaml
browser:
  enabled: true
  browser_type: "chromium"          # chromium (默认)
  headless: true                    # 无头模式
  cache_dir: ".wukong/cache"
  max_download_size: 104857600      # 100MB
  timeout: "60s"
  viewport_width: 1280
  viewport_height: 720
  search_backend: "duckduckgo"      # duckduckgo / searxng / tavily
  search_backend_url: ""            # SearXNG/Tavily 实例 URL
  search_api_key: ""                # API Key (DuckDuckGo 留空)
```

---

## 15. Visualiser — 图表生成

```yaml
visualiser:
  enabled: true
  output_dir: ".wukong/visuals"
  max_width: 1200
  max_height: 800
```

支持 Mermaid 流程图和 ECharts 图表。

---

## 16. Tutorial — 交互式教程

```yaml
tutorial:
  enabled: true
  language: "zh"
```

---

## 17. Top of Mind — 持久指令

```yaml
top_of_mind:
  enabled: true
  instruction_file: ".wukong/instructions.md"
  max_length: 2000
```

持久指令在每次对话开始时注入到系统提示词中。

**CRUD 工具**（4个）：`top_of_mind_add/show/remove/list`

---

## 18. Code Mode — JS 沙箱

```yaml
code_mode:
  enabled: true
  timeout: "10s"
  max_memory_mb: 128
```

使用 goja 运行时执行 JavaScript，支持超时和内存限制。

**工具**（2个）：`codemode_execute / codemode_evaluate`

---

## 19. Apps — HTML 应用管理

```yaml
apps:
  enabled: true
  app_dir: ".wukong/apps"
```

**CRUD 工具**（5个）：`apps_create/read/update/delete/list`

---

## 20. Summon — 子代理调度

```yaml
summon:
  enabled: true
  skills_dir: ".wukong/skills"       # 统一技能目录
  max_concurrent: 5                  # 最大并发子代理
  a2a_remotes: []                    # 远程 A2A 代理列表
```

**A2A 远程代理示例**：
```yaml
  a2a_remotes:
    - name: "code-reviewer"
      description: "Reviews code for quality and security"
      server_url: "http://localhost:8081"
      auth_type: "api_key"
      api_key: "${A2A_API_KEY}"
```

---

## 21. Skill — 技能仓库

```yaml
skill:
  enabled: true
  skills_dir: ".wukong/skills"
  auto_load: true
  max_skills: 20
```

技能目录结构：
```
.wukong/skills/
├── code-review/
│   └── SKILL.md          # 技能定义
├── data-analysis/
│   └── SKILL.md
└── ...
```

---

## 22. Evolution — 技能自进化

```yaml
evolution:
  enabled: false                # 启用技能进化
  auto_patch: false             # 自动应用补丁 (false=仅记录建议)
  analysis_provider: ""         # 分析模型 provider (空=默认)
  analysis_model: ""            # 分析模型名 (空=默认)
  min_confidence: 0.7           # 接受补丁的最低置信度
  cooldown_period: "30m"        # 同技能两次修补最短间隔
  max_patches_per_day: 10       # 每技能每日最大修补数
  max_versions_kept: 10         # 保留的历史版本数
  max_patch_size: 8192          # 补丁最大字节数
  analysis_timeout: "60s"       # 分析超时
```

**进化管线**：
```
执行追踪 → LLM 分析 → 补丁生成 → 版本存储 → 热重载
```

---

## 23. Knowledge — RAG 知识检索

```yaml
knowledge:
  enabled: false
  embedder_provider: "lmstudio"
  embedder_model: "qwen3-embedding-0.6b-graphql"
  vector_store: "inmemory"         # inmemory (唯一后端)
  max_results: 5
  enable_source_sync: false        # 源文件变更自动重新索引
  reranker_enabled: false          # 结果重排序
  search_tool_name: "knowledge_search"
```

支持的文档格式：txt, md, pdf, csv, json, docx

---

## 24. Workflow — 多模式 Agent 编排

```yaml
workflow:
  mode: "single"                   # single | chain | parallel | cycle | graph |
                                   # team_coordinator | team_swarm |
                                   # claude_code | codex | dify
  max_iterations: 10
  cycle_mode: "default"            # default (planner↔executor) |
                                   # code_review (generator↔reviewer)
  stream_mode: "none"              # none | hub (节点间流式通信)
  cache_enabled: false             # 节点缓存 (纯函数节点)
  engine: "bsp"                    # bsp | dag
  sub_agents: []                   # 自定义子代理配置
```

**Team 模式成员配置**：
```yaml
  team_members:
    - name: "researcher"
      instruction: "You are a research specialist..."
    - name: "coder"
      instruction: "You are a coding specialist..."
    - name: "reviewer"
      instruction: "You are a quality reviewer..."
```

**Claude Code 模式**：
```yaml
  claude_code_bin: "claude"
```

**Codex 模式**：
```yaml
  codex_bin: "codex"
```

**10 种编排模式概览**：

| 模式 | 拓扑 | 适用场景 |
|------|------|----------|
| `single` | 单 Agent | 简单对话 |
| `chain` | 顺序管道 | 分析→执行→审查 |
| `parallel` | 并发执行 | 多角度分析 |
| `cycle` | 迭代循环 | 自我改进 |
| `graph` | 条件路由 | 复杂决策 |
| `team_coordinator` | 中央协调 | 任务分派 |
| `team_swarm` | 蜂群自主 | 自主协作 |
| `claude_code` | 外部委托 | Claude Code 集成 |
| `codex` | 外部委托 | Codex CLI 集成 |
| `dify` | 可视化平台 | Dify 集成 |

---

## 25. Dify — AI 平台集成

```yaml
dify:
  enabled: false
  # base_url: "https://api.dify.ai/v1"
  # api_secret: "${DIFY_API_SECRET}"
  agent_name: "dify"
  enable_streaming: false
  timeout: "120s"
```

---

## 26. A2A Server — Agent-to-Agent 服务

```yaml
a2a_server:
  enabled: true
  address: ":9090"
  agent_name: "wukong"
  agent_description: "Wukong AI Agent - A2A service endpoint"
```

使用 tRPC-A2A-Go 框架，自动处理协议转换和流式支持。

---

## 27. AG-UI Server — Web UI SSE 服务

```yaml
agui:
  enabled: true
  address: ":8080"
  path: "/agui"
```

支持 CopilotKit / TDesign Chat 等 AG-UI 协议客户端。

---

## 28. ACP Server — Agent Client Protocol 服务

```yaml
acp_server:
  enabled: true
  address: ":9091"
  path: "/acp"
  enable_streaming: true
  # auth_type: "api_key"         # api_key | jwt | "" (无)
  # api_key: "${ACP_API_KEY}"
```

---

## 29. ACP MCP Bridge — 跨协议工具桥接

```yaml
acp_mcp:
  enabled: true
  address: ":3400"
  path: "/mcp"
```

将 Wukong 扩展暴露为 MCP Server，供 ACP 代理提供商调用。

---

## 30. Eval — 代理评估

```yaml
eval:
  enabled: false
  evalset_path: ".wukong/evals/default.evalset.json"
  results_path: ".wukong/evals/results.json"
```

---

## 31. Extensions — 外部 MCP 扩展

```yaml
extensions: []
```

内置扩展（12个）自动注册，此处仅配置外部 MCP 扩展。

---

## 32. Telemetry — 遥测

```yaml
telemetry:
  enabled: false
  exporter_type: "console"
  endpoint: "localhost:4317"
  service_name: "wukong"
  service_version: "1.0.0"
  environment: "development"
  sample_rate: 1.0
```

---

## 33. Observability — 增强可观测性

```yaml
observability:
  langfuse_enabled: false
  # langfuse_host: ""
  # langfuse_public_key: ""
  # langfuse_secret_key: ""
```

---

## 34. Artifact — 制品存储

```yaml
artifact:
  backend: "inmemory"           # inmemory | cos
  # cos_bucket_url: "https://bucket.cos.region.myqcloud.com"
  # cos_secret_id: "${COS_SECRETID}"
  # cos_secret_key: "${COS_SECRETKEY}"
```

---

## 35. Project — 项目追踪

```yaml
project_dir: "~/.config/wukong/"
```

自动记录工作目录与最后会话，支持快速恢复。

---

## 推荐配置

### 最小化配置（开箱即用）

```yaml
default_provider: "lmstudio"

providers:
  - name: "lmstudio"
    type: "lmstudio"
    base_url: "http://localhost:1234/v1"
    api_key: "lmstudio"
    model: "google/gemma-4-26b-a4b"
```

### 记忆增强配置（推荐）

```yaml
memory:
  auto_extract: true
  extractor_provider: "lmstudio"
  extractor_model: "gemma-4-e4b-it"

memoryflow:
  enabled: true

graphflow:
  enabled: true

importflow:
  enabled: true

revision:
  enabled: true
  revision_provider: "lmstudio"
  revision_model: "gemma-4-e4b-it"
  enable_llm_summarize: true
```

### 完整配置（所有能力）

```yaml
memory:
  auto_extract: true
  extractor_provider: "lmstudio"
  extractor_model: "gemma-4-e4b-it"

memoryflow:
  enabled: true
  planner_model: "gemma-4-e4b-it"
  extractor_model: "gemma-4-e4b-it"

graphflow:
  enabled: true
  auto_extract: true
  extractor_model: "gemma-4-e4b-it"

importflow:
  enabled: true

revision:
  enabled: true
  revision_provider: "lmstudio"
  revision_model: "gemma-4-e4b-it"
  enable_llm_summarize: true

agent:
  todo_tool_enabled: true
  todo_enforcer_enabled: true
  tool_search_enabled: true
  context_compaction: true

evolution:
  enabled: true
  auto_patch: false              # 先设置为 false 观察建议

a2a_server:
  enabled: true

agui:
  enabled: true

acp_server:
  enabled: true
```
