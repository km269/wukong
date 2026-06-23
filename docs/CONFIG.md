# Wukong 配置参考手册

> **配置文件**: `config.yaml` | **加载器**: Viper + Cobra | **配置段**: 30+ | **结构体**: 38
>
> 版本: v0.9.0 | 定义: `internal/config/config.go`

---

## 加载优先级 (7级)

```
1. CLI 参数 (--provider, --model, --temperature, --max-tokens, --no-stream, --config)
2. 环境变量 (WUKONG_ 前缀, 如 WUKONG_DEFAULT_PROVIDER)
3. --config CLI 指定的文件
4. 当前目录 ./config.yaml
5. ~/.config/wukong/config.yaml
6. /etc/wukong/config.yaml (非 Windows)
7. 内置默认值
```

**环境变量引用**: `${ENV_VAR}` 运行时自动展开
```yaml
api_key: "${OPENAI_API_KEY}"
```

---

## 1. 全局设置

```yaml
log_level: "info"           # debug | info | warn | error
default_provider: "lmstudio" # 必须匹配 providers 列表中某 name
lightweight_provider: "lmstudio"  # 后台任务的轻量模型 provider
lightweight_model: "gemma-4-e4b-it"  # 后台任务模型, 未指定时回退
```

**回退链**: `子系统.extractor_model → lightweight_model → default_provider`

---

## 2. Providers — AI 模型提供商

7种类型: `openai` | `anthropic` | `google` | `deepseek` | `ollama` | `lmstudio` | `acp`

```yaml
providers:
  - name: "openai"              # 唯一标识(必填)
    type: "openai"              # 提供商类型(必填)
    api_key: "${OPENAI_API_KEY}"# API密钥(必填, 支持${ENV_VAR})
    base_url: "https://api.openai.com/v1"
    model: "gpt-4o"

  - name: "acp-coder"           # ACP Agent 远程
    type: "acp"
    agent_url: "http://localhost:4000"
    model: "acp-default"
```

---

## 3. Agent — 核心行为 (35+ 字段)

```yaml
agent:
  max_llm_calls: 50           # 单次运行最大LLM调用
  max_tool_iterations: 30     # 最大工具迭代次数
  max_run_duration: "300s"    # 最大运行时长
  parallel_tools: true        # 并行工具调用
  streaming: true             # 流式输出
  temperature: 0.7            # [0.0, 2.0]
  max_tokens: 4096
  # reasoning_effort: "medium" # low | medium | high (仅builtin planner)

  tool_retry_enabled: true            # 工具重试(指数退避)
  tool_retry_max_attempts: 3
  tool_retry_initial_wait: "1s"
  tool_retry_backoff_factor: 2.0
  enable_post_tool_prompt: true

  planner: ""                 # builtin | react | ""(不启用)
  tool_search_enabled: true   # 自动工具筛选
  # tool_search_max_tools: 20

  context_compaction: true    # 两遍上下文压缩
  # context_compaction_tool_result_max_tokens: 1024  # Pass1阈值
  # context_compaction_oversized_max_tokens: 8192    # Pass2阈值
  # context_compaction_keep_recent: 1
  # context_compaction_force_clean_tools: [shell, grep]
  # context_compaction_keep_tools: [memory_search, session_search]

  session_recall_enabled: false   # 跨会话上下文预加载
  # session_recall_limit: 5

  json_repair_enabled: false      # LLM JSON修复

  todo_tool_enabled: true         # tRPC todo_write工具
  todo_enforcer_enabled: true     # todo强制执行器

  agent_tools_enabled: false      # 子Agent工具暴露
  # agent_tools_stream: true       # 子Agent结果流式输出

  system_prompt_dir: "~/.config/wukong/prompts/"  # 自定义指令模板

  # recipe_dir: ".wukong/recipes/"   # Recipe配方目录
  # recipe_enabled: true
```

---

## 4. Security — 安全策略 (12 字段)

```yaml
security:
  permission_mode: "smart"      # auto | smart | manual | chat_only
  require_approval: false
  malware_scan_enabled: true
  block_dangerous_commands: true
  blocked_commands:             # 危险命令黑名单
    - "rm -rf /"
    - "dd if=/dev/zero"
    - "mkfs."
  default_timeout: "30s"
  max_timeout: "300s"
  allowlist: []                 # 工具白名单(支持通配符)
  denylist: []                  # 工具黑名单
  guardrail_enabled: false      # Prompt注入检测
  ignore_file_enabled: true     # .wukongignore启用
  ignore_file: ".wukongignore"
```

### 权限模式

| 模式 | 行为 |
|------|------|
| `auto` | 全自动批准 |
| `smart` | 根据白名单+威胁扫描智能决策(默认) |
| `manual` | 每次确认 |
| `chat_only` | 禁止所有工具 |

---

## 5-7. 存储配置

### Session
```yaml
session:
  backend: "sqlite"     # sqlite | memory | redis
  db_path: "wukong.db"
  event_limit: 500
  ttl: "0h"             # 0=永不过期
  enable_summary: true
  summary_trigger: 50
  # redis_url: "redis://localhost:6379/0"
```

### Memory
```yaml
memory:
  backend: "sqlite"
  db_path: "wukong.db"
  max_memories: 100         # 0=不限制
  auto_extract: true        # 异步LLM提取
  extract_timeout: "600s"
  extractor_provider: "lmstudio"
  # extractor_model: ""     # 留空→lightweight_model
  enable_smart_cleanup: true
  cleanup_trigger_threshold: 0.8  # 80%触发
  cleanup_target_threshold: 0.6   # 60%目标
  memory_ttl: "720h"
```

**SmartCleanup**: <80%仅清过期; ≥80%评分淘汰(70%新鲜度+30%长度)→60%

### Todo
```yaml
todo:
  backend: "sqlite"
  db_path: "wukong.db"
  enable_native_todo: true
  enable_enforcer: true
```

---

## 8-11. CortexDB 记忆栈

### Recall (FTS5全文搜索)
```yaml
recall:
  enabled: true; backend: "sqlite"; db_path: "wukong.db"
  max_results: 10; max_messages_per_session: 200
  search_mode: "fts5"         # fts5 | hybrid
```

### CortexDB (HNSW+FTS5)
```yaml
cortex:
  enabled: true; db_path: "wukong.db"
  max_results: 10; max_messages_per_session: 200
  embedding_base_url: "http://localhost:1234"
  embedding_api_key: "lmstudio"
  embedding_model: "qwen3-embedding-0.6b-graphql"
```

### MemoryFlow (转录+唤醒)
```yaml
memoryflow:
  enabled: true; db_path: "wukong.db"
  namespace: "assistant"; embedding_dimensions: 0  # 0=自动检测
```

### GraphFlow (知识图谱)
```yaml
graphflow:
  enabled: true; db_path: "wukong.db"
  max_chars_per_doc: 8000; auto_extract: true
```

### ImportFlow (数据导入)
```yaml
importflow:
  enabled: true; db_path: "wukong.db"
```

---

## 12. Revision — 3层上下文压缩

```yaml
revision:
  enabled: true
  revision_provider: "lmstudio"
  enable_llm_summarize: true
  summary_cooldown: 120s       # 两次摘要最小间隔
  summary_timeout: 30s         # 摘要超时
  max_command_output: 8000     # 命令输出最大保留字符
  enable_semantic_search: false
  search_strategy: "include_all"
  max_context_tokens: 64000    # 上下文最大token
  trim_ratio: 0.3              # 算法截断比例
```

---

## 13-19. 功能工具

### Browser
```yaml
browser:
  enabled: true; browser_type: "chromium"; headless: true
  timeout: "60s"; viewport_width: 1280; viewport_height: 720
  search_backend: "duckduckgo"    # duckduckgo | searxng | tavily
  cache_dir: ".wukong/cache"; max_download_size: 104857600
```

### Visualiser
```yaml
visualiser:
  enabled: true; output_dir: ".wukong/visuals"
  max_width: 1200; max_height: 800
```

### Tutorial
```yaml
tutorial:
  enabled: true; language: "zh"
```

### Top of Mind
```yaml
top_of_mind:
  enabled: true; instruction_file: ".wukong/instructions.md"; max_length: 2000
```

### Code Mode (goja JS沙箱)
```yaml
code_mode:
  enabled: true; timeout: "10s"; max_memory_mb: 128
```

安全措施: API白名单(console/JSON/Math) + 禁用(eval/Function/RegExp) + 5并发 + Panic恢复

### Apps
```yaml
apps:
  enabled: true; app_dir: ".wukong/apps"
```

支持: Git克隆/ZIP解压/打包(ZIP+ZIM)/HTML清洗/本地预览/版本历史(max 20)

### ARD (Agentic Resource Discovery)
```yaml
ard:
  enabled: false                       # 默认关闭
  registry_url: ""                     # 远程注册表URL
  catalog_path: ".wukong/ard/catalog.json"  # 本地目录
```

7个工具: ard_search/discover/list/get/register/unregister/export

---

## 20-25. 编排系统

### Summon
```yaml
summon:
  enabled: true; skills_dir: ".wukong/skills"; max_concurrent: 5
  a2a_remotes: []
  # - name: "remote-coder"; server_url: "http://host:9090"
  #   auth_type: "api_key"; api_key: "${A2A_API_KEY}"
```

### Skill
```yaml
skill:
  enabled: true; skills_dir: ".wukong/skills"
  auto_load: true; max_skills: 20
```

### Evolution
```yaml
evolution:
  enabled: false; auto_patch: false       # 实验性功能
  min_confidence: 0.7; cooldown_period: "30m"
  max_patches_per_day: 10; max_versions_kept: 10
  max_patch_size: 8192; analysis_timeout: "60s"
```

### Knowledge (RAG)
```yaml
knowledge:
  enabled: false; embedder_provider: "lmstudio"
  embedder_model: "qwen3-embedding-4b"
  vector_store: "inmemory"; max_results: 5
  enable_source_sync: false; reranker_enabled: false
```

### Workflow (10种模式)
```yaml
workflow:
  mode: "single"               # single|chain|parallel|cycle|graph|
                               # team_coordinator|team_swarm|claude_code|codex|dify
  max_iterations: 10; cycle_mode: "default"
  stream_mode: "none"; cache_enabled: false; engine: "bsp"
  sub_agents: []
  # team_members: [{name: "coder", instruction: "..."}]
```

### Dify
```yaml
dify:
  enabled: false; agent_name: "dify"
  enable_streaming: false; timeout: "120s"
```

---

## 26. 服务端点

```yaml
a2a_server:       # Agent-to-Agent :9090
  enabled: true; address: ":9090"
  agent_name: "wukong"

agui:             # Web UI SSE :8080
  enabled: true; address: ":8080"; path: "/agui"

acp_server:       # Agent Client :9091
  enabled: true; address: ":9091"; path: "/acp"
  enable_streaming: true

acp_mcp:          # MCP桥接 :3400
  enabled: true; address: ":3400"; path: "/mcp"
```

---

## 27-32. 观测/扩展/项目

```yaml
eval:
  enabled: false
  evalset_path: ".wukong/evals/default.evalset.json"
  results_path: ".wukong/evals/results.json"

extensions: []    # 外部MCP扩展(内置自动注册)

telemetry:
  enabled: false; exporter_type: "console"
  endpoint: "localhost:4317"; sample_rate: 1.0

observability:
  langfuse_enabled: false

artifact:
  backend: "inmemory"  # inmemory | cos

project_dir: "~/.config/wukong/"
```

---

## 推荐配置方案

### 最小配置 (开箱即用)
```yaml
default_provider: "lmstudio"
lightweight_provider: "lmstudio"
lightweight_model: "gemma-4-e4b-it"
providers:
  - name: "lmstudio"; type: "lmstudio"
    base_url: "http://localhost:1234/v1"; api_key: "lmstudio"
    model: "google/gemma-4-26b-a4b"
```

### 完整记忆配置
在最小配置基础上，额外启用:
```yaml
memory: { auto_extract: true, max_memories: 100 }
cortex: { enabled: true, embedding_model: "..." }
memoryflow: { enabled: true }
graphflow: { enabled: true, auto_extract: true }
importflow: { enabled: true }
recall: { enabled: true }
revision: { enabled: true, enable_llm_summarize: true }
```

### 安全增强配置
```yaml
security:
  permission_mode: "smart"; block_dangerous_commands: true
  ignore_file_enabled: true; malware_scan_enabled: true
agent:
  todo_enforcer_enabled: true; json_repair_enabled: true
code_mode: { enabled: true, timeout: "10s", max_memory_mb: 128 }
```

### 全功能配置
启用所有以上 +:
```yaml
browser: { enabled: true }; a2a_server: { enabled: true }
agui: { enabled: true }; acp_server: { enabled: true }
acp_mcp: { enabled: true }
```

---

## 配置项索引 (完整)

| 配置段 | 结构体 | mapstructure标签 | 核心文件 |
|--------|--------|-----------------|----------|
| — | `WukongConfig` | — | config/config.go |
| Providers | `ProviderConfig` | `providers` | provider/factory.go |
| Agent | `AgentConfig` | `agent` | agent/loop.go |
| Security | `SecurityConfig` | `security` | security/guard.go |
| Session | `SessionConfig` | `session` | session/ |
| Memory | `MemoryConfig` | `memory` | memory/store.go |
| Todo | `TodoConfig` | `todo` | todo/ |
| Recall | `RecallConfig` | `recall` | recall/store.go |
| Cortex | `CortexConfig` | `cortex` | cortex/store.go |
| MemoryFlow | `MemoryFlowConfig` | `memoryflow` | cortex/memoryflow.go |
| GraphFlow | `GraphFlowConfig` | `graphflow` | cortex/graphflow.go |
| ImportFlow | `ImportFlowConfig` | `importflow` | cortex/import_flow.go |
| Revision | `RevisionConfig` | `revision` | agent/context.go |
| Browser | `BrowserConfig` | `browser` | browser/controller.go |
| Visualiser | `VisualiserConfig` | `visualiser` | extension/builtin/ |
| Tutorial | `TutorialConfig` | `tutorial` | extension/builtin/ |
| TopOfMind | `TopOfMindConfig` | `top_of_mind` | topofmind/mind.go |
| CodeMode | `CodeModeConfig` | `code_mode` | codemode/executor.go |
| Apps | `AppsConfig` | `apps` | apps/manager.go |
| ARD | `ARDConfig` | `ard` | config/config.go |
| Summon | `SummonConfig` | `summon` | summon/delegate.go |
| Skill | `SkillConfig` | `skill` | skill/manager.go |
| Evolution | `EvolutionConfig` | `evolution` | evolution/engine.go |
| Knowledge | `KnowledgeConfig` | `knowledge` | knowledge/manager.go |
| Workflow | `WorkflowConfig` | `workflow` | agent/workflow.go |
| Dify | `DifyConfig` | `dify` | agent/dify.go |
| A2AServer | `A2AServerConfig` | `a2a_server` | summon/a2a.go |
| AGUI | `AGUIConfig` | `agui` | server/agui.go |
| ACPServer | `ACPServerConfig` | `acp_server` | server/acp.go |
| ACPMCP | `ACPMCPConfig` | `acp_mcp` | extension/acp_mcp.go |
| Eval | `EvalConfig` | `eval` | eval/eval.go |
| Artifact | `ArtifactConfig` | `artifact` | artifact/factory.go |
| Telemetry | `TelemetryConfig` | `telemetry` | telemetry/telemetry.go |
| Observability | `ObservabilityConfig` | `observability` | observability/langfuse.go |
| Extension | `ExtensionConfig` | `extensions` | extension/manager.go |
