# Wukong 配置参考

> **配置文件**: `config.yaml` | **加载器**: Viper + Cobra
> **结构体**: 44 · ~340 字段 | **配置代码**: 4 文件 (config.go + types.go + defaults.go + validate.go)
>
> Go: 1.26 | 文件: 224 `.go` (51 `_test.go`) | CLI: 28 顶层 + 55+ 子命令

---

## 加载优先级 (4 级)

```
1. CLI 参数 (--provider, --model, --temperature, --max-tokens, --config)
2. 环境变量 (WUKONG_ 前缀，如 WUKONG_DEFAULT_PROVIDER)
3. YAML 配置文件 (--config 指定或默认搜索路径)
4. 内置默认值 (internal/config/defaults.go)
```

**配置文件搜索路径** (当未通过 `--config` 指定时):
1. `./config.yaml` (当前目录)
2. `~/.config/wukong/config.yaml`
3. `/etc/wukong/config.yaml` (非 Windows)

**环境变量展开**: `${ENV_VAR}` 语法，运行时自动展开。适用于 `providers[].api_key`、`summon.a2a_remotes[].api_key`、`summon.a2a_remotes[].jwt_secret`。

---

## 配置验证

加载后可通过 `Validate()` 方法检查致命错误：

| 检查项 | 类型 |
|--------|------|
| `default_provider` 在 providers 列表中存在 | 致命 |
| `agent.temperature` 在 [0.0, 2.0] 范围内 | 致命 |
| `security.permission_mode` 为有效值 | 致命 |
| `agent.max_tokens` >= 0 | 致命 |
| `evolution.min_confidence` 在 [0.0, 1.0] 范围内 | 致命 |
| `memory.cleanup_target_threshold` < `cleanup_trigger_threshold` | 致命 |
| `telemetry.sample_rate` 在 [0.0, 1.0] 范围内 | 致命 |

非致命警告通过 `Warnings()` 方法获取。

**CLI**: `wukong config validate`

---

## 1. 全局设置

```yaml
log_level: "info"                    # debug | info | warn | error
default_provider: "lmstudio"         # 必须匹配 providers[] 中的 name
lightweight_provider: "lmstudio"     # 后台任务 provider（空=回退到 default）
lightweight_model: "gemma-4-e4b-it"  # 后台任务模型
project_dir: "~/.config/wukong/"     # 项目追踪数据目录
```

---

## 2. Providers (7 种)

```yaml
providers:
  - name: "openai"
    type: "openai"
    api_key: "${OPENAI_API_KEY}"
    base_url: "https://api.openai.com/v1"
    model: "gpt-4o"

  - name: "deepseek"
    type: "deepseek"
    api_key: "${DEEPSEEK_API_KEY}"
    base_url: "https://api.deepseek.com"
    model: "deepseek-chat"

  - name: "anthropic"
    type: "anthropic"
    api_key: "${ANTHROPIC_API_KEY}"
    base_url: "https://api.anthropic.com"
    model: "claude-sonnet-4-20250514"

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

**CLI**: `wukong provider list | test`

---

## 3. Agent

```yaml
agent:
  max_llm_calls: 50                  # 0 = unlimited
  max_tool_iterations: 30
  max_run_duration: "300s"
  parallel_tools: true
  streaming: true
  temperature: 0.7                   # 0.0-2.0
  max_tokens: 4096

  # Tool retry (exponential backoff)
  tool_retry_enabled: true
  tool_retry_max_attempts: 3
  tool_retry_initial_wait: "1s"
  tool_retry_backoff_factor: 2.0

  enable_post_tool_prompt: true

  # Planner: "" | "builtin" | "react"
  planner: ""
  # reasoning_effort: "medium"       # low | medium | high
  # thinking_enabled: true
  # thinking_tokens: 4096

  # Tool search (TopK filtering)
  tool_search_enabled: true
  tool_search_max_tools: 20

  # Context compaction
  context_compaction: true
  context_compaction_tool_result_max_tokens: 1024
  context_compaction_oversized_max_tokens: 8192
  context_compaction_keep_recent: 1
  # context_compaction_force_clean_tools: ["shell", "grep"]
  # context_compaction_keep_tools: ["memory_search"]

  # Session recall
  session_recall_enabled: false
  session_recall_limit: 5

  json_repair_enabled: false
  todo_tool_enabled: true
  todo_enforcer_enabled: true
  agent_tools_enabled: false
  agent_tools_stream: false

  system_prompt_dir: "~/.config/wukong/prompts/"
  recipe_dir: ".wukong/recipes/"
  recipe_enabled: true
  # inline_recipes: []
```

---

## 4. Security

```yaml
security:
  permission_mode: "smart"           # auto | smart | manual | chat_only
  require_approval: false
  malware_scan_enabled: true
  block_dangerous_commands: true
  blocked_commands:
    - "rm -rf /"
    - "dd if=/dev/zero"
    - "mkfs."
    - "> /dev/sda"
    - "fork bomb"
  default_timeout: "30s"
  max_timeout: "300s"
  allowlist: []
  denylist: []
  guardrail_enabled: false
  ignore_file_enabled: true
  ignore_file: ".wukongignore"
```

---

## 5. Session

```yaml
session:
  backend: "sqlite"                  # sqlite | memory | redis
  db_path: "wukong.db"
  event_limit: 500
  ttl: "0h"                          # 0 = no expiration
  enable_summary: true
  summary_trigger: 50
  # redis_url: "redis://localhost:6379/0"
```

**CLI**: `wukong session list/delete/info/export/resume`

---

## 6. Memory

```yaml
memory:
  backend: "sqlite"
  db_path: "wukong.db"
  max_memories: 100
  auto_extract: true
  extract_timeout: "60s"
  # extractor_provider: "lmstudio"
  # extractor_model: "gemma-4-e4b-it"
  # extractor_prompt: |

  # Smart cleanup (capacity-aware eviction)
  enable_smart_cleanup: true
  cleanup_trigger_threshold: 0.8     # evict at 80% capacity
  cleanup_target_threshold: 0.6      # evict down to 60%
  memory_ttl: "720h"                 # 30 days
```

**CLI**: `wukong memory list/search/delete/clear`

### SmartCleanup 策略

| 容量 | 行为 |
|------|------|
| < 80% | 仅删除过期记忆 (TTL > memory_ttl) |
| >= 80% | 按评分淘汰至 60% (70%时效 + 30%内容长度) |

---

## 7. Todo

```yaml
todo:
  backend: "sqlite"
  db_path: "wukong.db"
  enable_native_todo: true
  enable_enforcer: true
```

**CLI**: `wukong todo status`

---

## 8. Recall

```yaml
recall:
  enabled: true
  backend: "sqlite"
  db_path: "wukong.db"
  max_results: 10
  max_messages_per_session: 200
  search_mode: "fts5"                # fts5 | hybrid
  # embedding_provider: "openai"     # for hybrid mode
  # embedding_model: "text-embedding-3-small"
```

**CLI**: `wukong cortex status`

---

## 9. CortexDB 记忆栈

### Cortex (HNSW 向量 + FTS5)

```yaml
cortex:
  enabled: true
  db_path: "wukong.db"
  max_results: 10
  max_messages_per_session: 200
  embedding_base_url: "http://localhost:1234"
  embedding_api_key: "lmstudio"
  embedding_model: "qwen3-embedding-0.6b"
```

### MemoryFlow (转录 + 语义唤醒)

```yaml
memoryflow:
  enabled: true
  db_path: "wukong.db"
  namespace: "assistant"
  embedding_dimensions: 0            # 0 = auto-detect
  # planner_model: ""
  # extractor_model: ""
```

### GraphFlow (知识图谱 RDF + SPARQL)

```yaml
graphflow:
  enabled: true
  db_path: "wukong.db"
  max_chars_per_doc: 8000
  auto_extract: true
  # extractor_model: ""
```

### ImportFlow (结构化数据导入)

```yaml
importflow:
  enabled: true
  db_path: "wukong.db"
```

**CLI**: `wukong cortex status`

---

## 10. Revision (上下文管理)

```yaml
revision:
  enabled: true
  # revision_provider: "lmstudio"
  # revision_model: ""
  enable_llm_summarize: true
  summary_cooldown: "120s"
  summary_timeout: "30s"
  max_command_output: 8000
  enable_semantic_search: false
  search_strategy: "include_all"     # include_all | semantic
  max_context_tokens: 64000
  trim_ratio: 0.3
```

---

## 11. Browser

```yaml
browser:
  enabled: true
  browser_type: "chromium"
  headless: true
  browser_path: ""                   # empty = auto-detect
  stealth: true
  cache_dir: ".wukong/cache"
  max_download_size: 104857600       # 100 MB
  timeout: "60s"
  viewport_width: 1280
  viewport_height: 720
  search_backend: "duckduckgo"       # duckduckgo | searxng | tavily
  # search_backend_url: ""
  # search_api_key: ""
```

---

## 12. 功能工具

### Visualiser

```yaml
visualiser:
  enabled: true
  output_dir: ".wukong/visuals"
  max_width: 1200
  max_height: 800
```

### Tutorial

```yaml
tutorial:
  enabled: true
  language: "zh"                     # zh | en
```

### TopOfMind

```yaml
top_of_mind:
  enabled: true
  instruction_file: ".wukong/instructions.md"
  max_length: 2000
```

**CLI**: `wukong init`

### CodeMode

```yaml
code_mode:
  enabled: true
  timeout: "10s"
  max_memory_mb: 128
```

---

## 13. Apps — HTML 应用 + 网站克隆 + ZIM 打包

```yaml
apps:
  enabled: true
  app_dir: ".wukong/apps"

  # 网站克隆 — EnhancedCloner
  clone:
    # Crawl scope
    max_pages: 0                     # 0 = unlimited
    max_depth: 0                     # 0 = unlimited
    traversal: "bfs"                 # bfs | dfs
    subdomains: false
    scope_prefix: ""                 # restrict to URL paths

    # Rendering
    workers: 4
    asset_workers: 8
    browser_pages: 4                 # 0 = use workers
    timeout: 60                      # seconds
    render_timeout: 30               # seconds
    settle: 1500                     # milliseconds
    scroll: false
    user_agent: ""                   # empty = default

    # Asset policy
    asset_same_domain: true
    max_asset_bytes: 52428800        # 50 MB

    # Compliance
    respect_robots: true
    crawl_delay: 0                   # ms (0 = use robots.txt)
    no_sitemap: false

    # Optimizations
    dedup_content: true              # SHA-256 + hard links
    mobile_readable: true
    enable_resume: true
    persist: true
    incremental: false
    cache_max_age: 86400             # seconds

    # Anti-detection
    headless: true
    stealth: true
    chrome_profile: ".wukong/chrome/profile"
    chrome_path: ""
    antibot_enabled: true
    antibot_auto_escalate: true

    # Session
    cookie_file: ""                  # Netscape format

  # App packaging
  pack:
    compress: true                   # zstd codec 5 (ZIM v6)
    incremental: false
    language: "eng"                  # ISO 639-3
    creator: "Wukong"
    format: "html"                   # html | zim | binary | app
```

### apps clone CLI

```bash
wukong apps clone <url> [flags]
  -p, --max-pages      int    最大页面数 (0=无限制)
  -d, --max-depth      int    最大链接深度 (0=无限制)
      --traversal       string 遍历策略: bfs (默认) | dfs
  -w, --workers        int    并发数 (默认4)
      --asset-workers   int    资产并发数
      --timeout         int    渲染超时 (秒, 默认60)
      --settle          int    网络空闲等待 (毫秒, 默认1500)
      --subdomains            包含子域名
      --scroll                自动滚动懒加载
      --stealth               反反爬模式
      --asset-same-domain     仅下载同域资产
      --no-sitemap            禁用 Sitemap 发现
      --force                 强制删除已有克隆
      --refresh               刷新所有页面
      --incremental           ETag/Last-Modified 增量更新
      --chrome-path    string Chrome 可执行文件路径
```

### apps pack CLI

```bash
wukong apps pack <app-name> [flags]
  -f, --format        string  输出格式: html|zim|binary|app (默认zim)
  -o, --output        string  输出路径
       --compress             启用 zstd 压缩
       --incremental          增量构建(复用集群)
       --language      string  ZIM 语言代码 (默认eng)
       --title         string  ZIM 标题
       --description   string  ZIM 描述
       --date          string  日期 YYYY-MM-DD
       --creator       string  创建者 (默认Wukong)
       --base-binary   string  基础可执行文件(binary/app格式)
       --icon          string  图标路径(app格式)
```

---

## 14. ARD — Agentic Resource Discovery

```yaml
ard:
  enabled: false
  registry_url: ""                   # remote registry for federation
  catalog_path: ".wukong/ard/catalog.json"
  publish_enabled: false
  publish_port: 0                    # 0 = disabled
```

**CLI**: `wukong ard status/catalog`

---

## 15. Summon — Sub-agent delegation

```yaml
summon:
  enabled: true
  skills_dir: ".wukong/skills"
  max_concurrent: 5
  a2a_remotes: []                    # list of A2ARemoteConfig
```

---

## 16. Skill

```yaml
skill:
  enabled: true
  skills_dir: ".wukong/skills"       # shared with summon
  auto_load: true
  max_skills: 20
```

**CLI**: `wukong skill list/show`

---

## 17. Evolution

```yaml
evolution:
  enabled: false
  auto_patch: false
  analysis_provider: ""
  analysis_model: ""
  min_confidence: 0.7
  cooldown_period: "30m"
  max_patches_per_day: 10
  max_versions_kept: 10
  max_patch_size: 8192
  analysis_timeout: "60s"
```

**CLI**: `wukong evolution status`

---

## 18. Knowledge (RAG)

```yaml
knowledge:
  enabled: false
  embedder_provider: "lmstudio"
  embedder_model: "text-embedding-3-small"
  # sources: ["./docs"]
  # source_urls: ["https://example.com/doc"]
  vector_store: "inmemory"
  max_results: 5
  enable_source_sync: false
  reranker_enabled: false
  search_tool_name: "knowledge_search"
```

**CLI**: `wukong knowledge status`

---

## 19. Workflow

```yaml
workflow:
  mode: "single"                     # single|chain|parallel|cycle|graph|team_*|claude_code|codex|dify
  max_iterations: 10
  cycle_mode: "default"              # default | code_review
  stream_mode: "none"                # none | hub
  cache_enabled: false
  engine: "bsp"                      # bsp | dag
  sub_agents: []
  # team_members: []
  # claude_code_bin: "claude"
  # codex_bin: "codex"
```

---

## 20. Dify

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

## 21. 服务端点 (4 协议)

```yaml
a2a_server:                          # A2A :9090
  enabled: true
  address: ":9090"
  agent_name: "wukong"
  agent_description: "Wukong AI Agent — A2A endpoint"

agui:                                # AG-UI SSE :8080
  enabled: true
  address: ":8080"
  path: "/agui"

acp_server:                          # ACP :9091
  enabled: true
  address: ":9091"
  path: "/acp"
  enable_streaming: true
  auth_type: ""                      # "" | api_key | jwt
  # api_key: "${ACP_API_KEY}"

acp_mcp:                             # ACP MCP Bridge :3400
  enabled: true
  address: ":3400"
  path: "/mcp"
```

**CLI**: `wukong server`

---

## 22. 观测与扩展

```yaml
eval:
  enabled: false
  evalset_path: ".wukong/evals/default.evalset.json"
  results_path: ".wukong/evals/results.json"

extensions: []                       # MCP external servers

telemetry:
  enabled: false
  exporter_type: "console"           # console | grpc | http
  endpoint: "localhost:4317"
  service_name: "wukong"
  service_version: "1.0.0"
  environment: "development"
  sample_rate: 1.0

observability:
  langfuse_enabled: false
  # langfuse_host: "cloud.langfuse.com"
  # langfuse_public_key: "${LANGFUSE_PUBLIC_KEY}"
  # langfuse_secret_key: "${LANGFUSE_SECRET_KEY}"

artifact:
  backend: "inmemory"               # inmemory | cos
  # cos_bucket_url: "https://bucket.cos.region.myqcloud.com"
  # cos_secret_id: "${COS_SECRETID}"
  # cos_secret_key: "${COS_SECRETKEY}"
```

---

## 23. 推荐配置

### 最小配置

```yaml
default_provider: "lmstudio"
providers:
  - name: "lmstudio"
    type: "lmstudio"
    api_key: "lmstudio"
    base_url: "http://localhost:1234/v1"
    model: "google/gemma-4-26b-a4b"
```

### 云端模型

```yaml
default_provider: "deepseek"
providers:
  - name: "deepseek"
    type: "deepseek"
    api_key: "${DEEPSEEK_API_KEY}"
    base_url: "https://api.deepseek.com"
    model: "deepseek-chat"
memory:
  auto_extract: true
agent:
  todo_enforcer_enabled: true
  context_compaction: true
```

### 网站克隆

```yaml
apps:
  enabled: true
  clone:
    workers: 4
    respect_robots: true
    dedup_content: true
    mobile_readable: true
    enable_resume: true
```

### 完整记忆

```yaml
cortex:
  enabled: true
  embedding_model: "qwen3-embedding-0.6b"
memoryflow:
  enabled: true
graphflow:
  enabled: true
  auto_extract: true
importflow:
  enabled: true
recall:
  enabled: true
revision:
  enabled: true
  enable_llm_summarize: true
```

### 快速诊断

```bash
wukong config validate    # 配置校验
wukong system-check       # 系统就绪诊断
wukong health             # 运行健康检查
wukong stats              # 统计面板
wukong bench              # 模型性能基准
```

---

## 配置项完整索引

| 配置段 | 结构体 | 字段数 | CLI 入口 |
|--------|--------|--------|----------|
| `log_level` | — | 1 | — |
| `default_provider` | — | 1 | provider list |
| `lightweight_*` | — | 2 | config show |
| `project_dir` | — | 1 | project list |
| `providers` | `ProviderConfig` | 7 | provider list/test |
| `extensions` | `ExtensionConfig` | 14 | extension list/install |
| `agent` | `AgentConfig` | 35+ | config show |
| `security` | `SecurityConfig` | 13 | system-check |
| `session` | `SessionConfig` | 7 | session list/delete/info |
| `memory` | `MemoryConfig` | 13 | memory list/search/delete/clear |
| `todo` | `TodoConfig` | 4 | todo status |
| `recall` | `RecallConfig` | 8 | cortex status |
| `cortex` | `CortexConfig` | 8 | cortex status |
| `memoryflow` | `MemoryFlowConfig` | 6 | cortex status |
| `graphflow` | `GraphFlowConfig` | 5 | cortex status |
| `importflow` | `ImportFlowConfig` | 2 | cortex status |
| `revision` | `RevisionConfig` | 11 | config show |
| `browser` | `BrowserConfig` | 13 | — |
| `visualiser` | `VisualiserConfig` | 4 | — |
| `tutorial` | `TutorialConfig` | 2 | — |
| `top_of_mind` | `TopOfMindConfig` | 3 | init |
| `code_mode` | `CodeModeConfig` | 3 | — |
| `apps` | `AppsConfig` | 3 | apps clone/pack/list/view |
| `apps.clone` | `CloneDefaults` | 29 | apps clone |
| `apps.pack` | `PackDefaults` | 5 | apps pack |
| `ard` | `ARDConfig` | 5 | ard status/catalog |
| `summon` | `SummonConfig` | 4 | — |
| `summon.a2a_remotes` | `A2ARemoteConfig` | 11 | — |
| `skill` | `SkillConfig` | 4 | skill list/show |
| `evolution` | `EvolutionConfig` | 10 | evolution status |
| `knowledge` | `KnowledgeConfig` | 9 | knowledge status |
| `workflow` | `WorkflowConfig` | 9 | — |
| `workflow.sub_agents` | `WorkflowSubAgentConfig` | 4 | — |
| `workflow.team_members` | `TeamMemberConfig` | 4 | — |
| `dify` | `DifyConfig` | 6 | — |
| `a2a_server` | `A2AServerConfig` | 4 | server |
| `agui` | `AGUIConfig` | 3 | server |
| `acp_server` | `ACPServerConfig` | 5 | server |
| `acp_mcp` | `ACPMCPConfig` | 3 | server |
| `eval` | `EvalConfig` | 4 | eval |
| `eval.metrics` | `EvalMetricConfig` | 2 | — |
| `artifact` | `ArtifactConfig` | 4 | config show |
| `observability` | `ObservabilityConfig` | 4 | health |
| `telemetry` | `TelemetryConfig` | 7 | health |
