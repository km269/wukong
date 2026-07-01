# Wukong 配置参考

> 配置文件: config.yaml | 加载器: Viper + Cobra
> 配置结构: A-O 共 15 组 (34 结构体) | 配置代码: 5 文件 (config.go + types.go + defaults.go + validate.go + config_test.go)

---

## 加载优先级 (4 级)

```
1. CLI 参数 (--provider, --model, --temperature, --max-tokens, --config)
2. 环境变量 (WUKONG_ 前缀, 如 WUKONG_DEFAULT_PROVIDER)
3. YAML 配置文件 (--config 指定或默认搜索路径)
4. 内置默认值 (internal/config/defaults.go)
```

配置文件搜索路径: `./config.yaml` -> `~/.config/wukong/config.yaml` -> `/etc/wukong/config.yaml` (非Windows)

---

## env var 展开

`${ENV_VAR}` 语法, 运行时自动展开。覆盖 8 类敏感字段:

| 类别 | 字段 |
|------|------|
| Providers | api_key |
| A2A Remotes | api_key, jwt_secret |
| Gateway Feishu | app_secret, encrypt_key, verification_token |
| Gateway WeCom | secret, token, encoding_aes_key |
| CortexDB | embedding_api_key |
| Dify | api_secret |
| Observability (Langfuse) | public_key, secret_key |
| Artifact (COS) | cos_secret_id, cos_secret_key |
| ACP Server | api_key |

---

## 配置验证

`Validate()` 方法检查致命错误:

| 检查项 | 类型 |
|--------|------|
| default_provider 在 providers 列表中存在 | 致命 |
| agent.temperature 在 [0.0, 2.0] 范围内 | 致命 |
| agent.max_tokens >= 0 | 致命 |
| security.permission_mode 为有效值 | 致命 |
| memory.cleanup_target_threshold < cleanup_trigger_threshold | 致命 |
| memory.cleanup 阈值在 [0.0, 1.0] 范围 | 致命 |
| evolution.min_confidence 在 [0.0, 1.0] 范围 | 致命 |
| telemetry.sample_rate 在 [0.0, 1.0] 范围 | 致命 |
| anp.port 在 [0, 65535] 范围 | 致命 |
| anp.meta_protocol_enabled 但 port <= 0 | 致命 |

`Warnings()` 非致命警告:
- 无 providers 配置
- memory.auto_extract 启用但无 default_provider
- cortex.enabled 但无 embedding_model
- recall.search_mode 为 hybrid 但无 embedding provider
- context_compaction 启用但 Pass 2 为 0
- okf.enabled 但 bundle_dir 为空
- okf.injector_enabled 但 memoryflow 未启用
- okf.enrichment_enabled 但无 default_provider
- anp.enabled 但 did_domain 为空
- anp.e2ee_enabled 但 meta_protocol 未启用

---

## 配置段概览

| 组 | 标签 | 内容 |
|----|------|------|
| A | Global | log_level, default_provider, lightweight_* |
| B | Providers | 7 种 LLM Provider 配置 |
| C | Agent | 核心循环、生成参数、工具重试、规划器、上下文压缩 |
| D | Security | 工具执行安全、命令阻止、权限模式 |
| E | Storage | Session / Memory / Todo / Recall (4 子系统) |
| F | CortexDB Stack | Cortex / MemoryFlow / GraphFlow / ImportFlow |
| G | Context Mgmt | Revision (上下文窗口管理) |
| H | Feature Tools | Browser / Visualiser / Tutorial / TopOfMind / CodeMode / Apps |
| I | Extensions | MCP 外部扩展列表 |
| J | Service Endpoints | A2A / AG-UI / ACP / ACP MCP / Gateway |
| K | Agent-to-Agent | Summon / ANP / ARD / Dify |
| L | Knowledge & Skill | Knowledge / OKF / Skill / Evolution |
| M | Orchestration | Workflow (10 种模式) |
| N | Observability | Telemetry / Langfuse / Eval / Artifact |
| O | Project | project_dir |

---

## A. 全局设置

```yaml
log_level: "info"                    # debug | info | warn | error
default_provider: "lmstudio"         # 必须匹配 providers[].name
lightweight_provider: "lmstudio"     # 后台任务 (空 = default_provider)
lightweight_model: "gemma-4-e4b-it"  # 后台轻量模型
```

---

## B. Providers (7 种)

```yaml
providers:
  - name: "openai"
    type: "openai"                   # openai|anthropic|google|deepseek|ollama|lmstudio|acp
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

---

## C. Agent

```yaml
agent:
  # LLM 调用限制
  max_llm_calls: 50                  # 0 = 无限
  max_tool_iterations: 30
  max_run_duration: "300s"

  # 生成参数
  parallel_tools: true
  streaming: true
  temperature: 0.7                   # [0.0, 2.0]
  max_tokens: 4096

  # 工具重试 (指数退避)
  tool_retry_enabled: true
  tool_retry_max_attempts: 3
  tool_retry_initial_wait: "1s"
  tool_retry_backoff_factor: 2.0
  enable_post_tool_prompt: true

  # 规划器: "" | "builtin" | "react"
  planner: ""

  # 工具搜索
  tool_search_enabled: true
  tool_search_max_tools: 20

  # 上下文压缩
  context_compaction: true
  context_compaction_tool_result_max_tokens: 1024
  context_compaction_oversized_max_tokens: 8192
  context_compaction_keep_recent: 1

  # 会话召回
  session_recall_enabled: false
  session_recall_limit: 5

  # JSON 修复
  json_repair_enabled: false

  # Todo 系统
  todo_tool_enabled: true
  todo_enforcer_enabled: true

  # Agent 工具
  agent_tools_enabled: false
  agent_tools_stream: false

  # 系统提示 & Recipe
  system_prompt_dir: "~/.config/wukong/prompts/"
  recipe_dir: ".wukong/recipes/"
  recipe_enabled: true
```

---

## D. Security

```yaml
security:
  permission_mode: "smart"           # auto | smart | manual | chat_only
  require_approval: false            # 遗留字段; 优先使用 permission_mode
  malware_scan_enabled: true
  block_dangerous_commands: true
  blocked_commands: ["rm -rf /", "dd if=/dev/zero", "mkfs."]
  default_timeout: "30s"
  max_timeout: "300s"
  allowlist: []
  denylist: []
  guardrail_enabled: false           # prompt 注入检测
  ignore_file_enabled: true
  ignore_file: ".wukongignore"
```

---

## E. Storage — SQLite-backed persistence

### E1. Session

```yaml
session:
  backend: "sqlite"                  # sqlite | memory | redis
  db_path: "wukong.db"
  event_limit: 500
  ttl: "0h"                          # 0 = 无过期
  enable_summary: true
  summary_trigger: 50
```

### E2. Memory

```yaml
memory:
  backend: "sqlite"
  db_path: "wukong.db"
  max_memories: 100
  auto_extract: true
  extract_timeout: "60s"
  # extractor_provider: ""           # 专用提取 Provider
  # extractor_model: ""              # 专用提取模型
  # extractor_prompt: ""             # 自定义提取 Prompt

  # Smart cleanup
  enable_smart_cleanup: true
  cleanup_trigger_threshold: 0.8     # 80% 触发淘汰
  cleanup_target_threshold: 0.6      # 淘汰到 60%
  memory_ttl: "720h"                 # 30 天
```

### E3. Todo

```yaml
todo:
  backend: "sqlite"
  db_path: "wukong.db"
  enable_native_todo: true
  enable_enforcer: true
```

### E4. Recall

```yaml
recall:
  enabled: true
  backend: "sqlite"
  db_path: "wukong.db"
  max_results: 10
  max_messages_per_session: 200
  search_mode: "fts5"                # fts5 | hybrid
```

---

## F. CortexDB Memory Stack

### F1. Cortex — HNSW 向量 + FTS5 召回

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

### F2. MemoryFlow — 转录 + 唤醒

```yaml
memoryflow:
  enabled: true
  db_path: "wukong.db"
  namespace: "assistant"
  embedding_dimensions: 0            # 0 = auto-detect
```

### F3. GraphFlow — 知识图谱

```yaml
graphflow:
  enabled: true
  db_path: "wukong.db"
  max_chars_per_doc: 8000
  auto_extract: true
```

### F4. ImportFlow — 结构化数据导入

```yaml
importflow:
  enabled: true
  db_path: "wukong.db"
```

---

## G. Context Management — Revision

```yaml
revision:
  enabled: true
  # revision_provider: ""           # 专用修订 Provider
  # revision_model: ""              # 专用修订模型
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

## H. Feature Tools

### H1. Browser

```yaml
browser:
  enabled: true
  browser_type: "chromium"
  headless: true
  stealth: true
  cache_dir: ".wukong/cache"
  max_download_size: 104857600       # 100 MB
  timeout: "60s"
  viewport_width: 1280
  viewport_height: 720
  search_backend: "duckduckgo"       # duckduckgo | searxng | tavily
```

### H2. Visualiser

```yaml
visualiser:
  enabled: true
  output_dir: ".wukong/visuals"
  max_width: 1200
  max_height: 800
```

### H3. Tutorial

```yaml
tutorial:
  enabled: true
  language: "zh"                     # zh | en
```

### H4. TopOfMind

```yaml
top_of_mind:
  enabled: true
  instruction_file: ".wukong/instructions.md"
  max_length: 2000
```

### H5. CodeMode

```yaml
code_mode:
  enabled: true
  timeout: "10s"
  max_memory_mb: 128
```

### H6. Apps — 网站克隆 + ZIM 打包

```yaml
apps:
  enabled: true
  app_dir: ".wukong/apps"

  clone:
    workers: 4
    asset_workers: 8
    stealth: true
    antibot_enabled: true
    antibot_auto_escalate: true
    dedup_content: true
    enable_resume: true
    mobile_readable: true
    # 完整 30 字段见 config.yaml 组 H6

  pack:
    compress: true
    format: "html"                   # html | zim | binary | app
    language: "eng"
    creator: "Wukong"
```

---

## I. Extensions

```yaml
extensions: []
# 示例外部 MCP 扩展:
#   - name: "filesystem"
#     type: "external"
#     transport: "stdio"
#     command: "npx"
#     args: ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
#     enabled: true
#     timeout: "30s"
#     mcp_broker: false
```

---

## J. Service Endpoints

| 端点 | 默认端口 | 路径 | 用途 |
|------|----------|------|------|
| A2A Server | :9090 | -- | Agent-to-Agent 协议 |
| AG-UI SSE | :8080 | /agui | Web UI 实时对话 |
| ACP Server | :9091 | /acp | Agent Client Protocol |
| ACP MCP | :3400 | /mcp | MCP 工具桥接 |
| Gateway | :9093 | / | 多平台消息通道 |

### Gateway 详细配置

```yaml
gateway:
  enabled: false
  address: ":9093"
  default_timeout: "120s"
  max_concurrent_sessions: 100
  message_dedup_ttl: "5m"            # 消息去重窗口
  rate_limit_per_user: 10            # 每用户限流
  rate_limit_window: "10s"           # 限流滑动窗口

  feishu:
    enabled: false
    app_id: ""
    app_secret: "${FEISHU_APP_SECRET}"
    encrypt_key: "${FEISHU_ENCRYPT_KEY}"
    verification_token: "${FEISHU_VERIFICATION_TOKEN}"
    stream_card_enabled: true
    stream_card_update_interval: "500ms"
    max_message_length: 4096
    enable_file_receive: false

  wecom:
    enabled: false
    corpid: ""
    secret: "${WECOM_SECRET}"
    token: "${WECOM_TOKEN}"
    encoding_aes_key: "${WECOM_ENCODING_AES_KEY}"
    stream_enabled: true
    stream_update_interval: "1s"
    max_message_length: 2048
    enable_card_reply: true
```

---

## K. Agent-to-Agent Communication

### K1. Summon

```yaml
summon:
  enabled: true
  skills_dir: ".wukong/skills"
  max_concurrent: 5
  a2a_remotes: []
```

### K2. ANP — Agent Network Protocol

```yaml
anp:
  enabled: false
  # did_domain: "wukong.example.com" # 默认: os.Hostname()
  port: 9092
  discovery_enabled: true
  meta_protocol_enabled: true
  http_sign_enabled: true
  e2ee_enabled: true
  a2a_enabled: true                  # 自动创建 A2A 接口卡
  mcp_enabled: true                  # 自动创建 MCP 接口卡
  agui_enabled: true                 # 自动创建 AG-UI 接口卡
```

依赖: E2EE 需要 meta_protocol_enabled (密钥交换)

### K3. ARD

```yaml
ard:
  enabled: false
  registry_url: ""
  catalog_path: ".wukong/ard/catalog.json"
  publish_enabled: false
  publish_port: 0
```

### K4. Dify

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

## L. Knowledge & Skill Management

### L1. Knowledge (RAG)

```yaml
knowledge:
  enabled: true
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

### L2. OKF — Open Knowledge Format

```yaml
okf:
  enabled: true
  bundle_dir: ".wukong/okf"
  injector_enabled: false            # 注入 OKF index 到 MemoryFlow
  enrichment_enabled: false          # 自动生成概念文档
  auto_export: false                 # 会话结束自动导出
  register_in_ard: false             # ARD 联邦发现
```

### L3. Skill

```yaml
skill:
  enabled: true
  skills_dir: ".wukong/skills"
  auto_load: true
  max_skills: 20
```

### L4. Evolution

```yaml
evolution:
  enabled: true
  auto_patch: false                  # false = 仅记录建议
  analysis_provider: ""
  analysis_model: ""
  min_confidence: 0.7
  cooldown_period: "30m"
  max_patches_per_day: 10
  max_versions_kept: 10
  max_patch_size: 8192
  analysis_timeout: "60s"
```

---

## M. Workflow Orchestration

```yaml
workflow:
  mode: "single"
  # 模式: single|chain|parallel|cycle|graph|team_coordinator|team_swarm|claude_code|codex|dify
  max_iterations: 10
  cycle_mode: "default"
  stream_mode: "none"
  cache_enabled: false
  engine: "bsp"
  sub_agents: []
```

---

## N. Observability & Evaluation

```yaml
telemetry:
  enabled: false
  exporter_type: "console"
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

eval:
  enabled: false
  evalset_path: ".wukong/evals/default.evalset.json"
  results_path: ".wukong/evals/results.json"

artifact:
  backend: "inmemory"                # inmemory | cos
  # cos_bucket_url: "https://bucket.cos.region.myqcloud.com"
  # cos_secret_id: "${COS_SECRETID}"
  # cos_secret_key: "${COS_SECRETKEY}"
```

---

## O. Project Directory

```yaml
project_dir: "~/.config/wukong/"
```

---

## 推荐配置

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

### ANP Agent 互通

```yaml
anp:
  enabled: true
  did_domain: "wukong.example.com"
  port: 9092
  discovery_enabled: true
  meta_protocol_enabled: true
  http_sign_enabled: true
  e2ee_enabled: true
```

### OKF 知识管理

```yaml
okf:
  enabled: true
  bundle_dir: ".wukong/okf"
  injector_enabled: true
  enrichment_enabled: true
  auto_export: true
  register_in_ard: true
```

### 完整记忆 + OKF + ANP + Gateway

```yaml
cortex: { enabled: true }
memoryflow: { enabled: true }
graphflow: { enabled: true, auto_extract: true }
importflow: { enabled: true }
recall: { enabled: true }
revision: { enabled: true }
okf: { enabled: true, injector_enabled: true }
ard: { enabled: true, publish_enabled: true, publish_port: 9000 }
anp: { enabled: true, did_domain: "wukong.example.com", e2ee_enabled: true }
a2a_server: { enabled: true }
gateway: { enabled: true }
```

---

## 配置结构体索引

| 组 | 配置段 | 结构体 | 字段数 |
|----|--------|--------|--------|
| A | global | -- | 4 |
| B | providers | ProviderConfig | 8 |
| C | agent | AgentConfig | 32 |
| D | security | SecurityConfig | 13 |
| E1 | session | SessionConfig | 8 |
| E2 | memory | MemoryConfig | 14 |
| E3 | todo | TodoConfig | 4 |
| E4 | recall | RecallConfig | 8 |
| F1 | cortex | CortexConfig | 6 |
| F2 | memoryflow | MemoryFlowConfig | 6 |
| F3 | graphflow | GraphFlowConfig | 5 |
| F4 | importflow | ImportFlowConfig | 2 |
| G | revision | RevisionConfig | 11 |
| H1 | browser | BrowserConfig | 14 |
| H2 | visualiser | VisualiserConfig | 4 |
| H3 | tutorial | TutorialConfig | 2 |
| H4 | top_of_mind | TopOfMindConfig | 3 |
| H5 | code_mode | CodeModeConfig | 3 |
| H6 | apps | AppsConfig + CloneDefaults(31) + PackDefaults(5) | 39 |
| I | extensions | ExtensionConfig | 18 |
| J1 | gateway | GatewayConfig + FeishuChannel(8) + WeComChannel(8) | 25 |
| J2 | a2a_server | A2AServerConfig | 4 |
| J3 | agui | AGUIConfig | 3 |
| J4 | acp_server | ACPServerConfig | 6 |
| J5 | acp_mcp | ACPMCPConfig | 3 |
| K1 | summon | SummonConfig + A2ARemoteConfig(12) | 15 |
| K2 | anp | ANPConfig | 11 |
| K3 | ard | ARDConfig | 5 |
| K4 | dify | DifyConfig | 6 |
| L1 | knowledge | KnowledgeConfig | 10 |
| L2 | okf | OKFConfig | 7 |
| L3 | skill | SkillConfig | 4 |
| L4 | evolution | EvolutionConfig | 10 |
| M | workflow | WorkflowConfig + SubAgent(4) + TeamMember(4) | 14 |
| N1 | telemetry | TelemetryConfig | 8 |
| N2 | observability | ObservabilityConfig | 4 |
| N3 | eval | EvalConfig + EvalMetricConfig(2) | 5 |
| N4 | artifact | ArtifactConfig | 4 |
| O | project_dir | -- | 1 |
