# Wukong 配置参考

> **配置文件**: `config.yaml` | **加载器**: Viper + Cobra
> **结构体**: 45 · ~358 字段 | **配置代码**: 4 文件 (config.go + types.go + defaults.go + validate.go)
>
> Go: 1.26 | 文件: 241 `.go` (52 `_test.go`) | CLI: 28 顶层 + 55+ 子命令

---

## 加载优先级 (4 级)

```
1. CLI 参数 (--provider, --model, --temperature, --max-tokens, --config)
2. 环境变量 (WUKONG_ 前缀，如 WUKONG_DEFAULT_PROVIDER)
3. YAML 配置文件 (--config 指定或默认搜索路径)
4. 内置默认值 (internal/config/defaults.go)
```

**配置文件搜索路径**: `./config.yaml` → `~/.config/wukong/config.yaml` → `/etc/wukong/config.yaml`(非Windows)

**环境变量展开**: `${ENV_VAR}` 语法，运行时自动展开。

---

## 配置验证

加载后可通过 `Validate()` 方法检查致命错误：

| 检查项 | 类型 |
|--------|------|
| `default_provider` 在 providers 列表中存在 | 致命 |
| `agent.temperature` 在 [0.0, 2.0] 范围内 | 致命 |
| `security.permission_mode` 为有效值 | 致命 |
| `memory.cleanup_target_threshold` < `cleanup_trigger_threshold` | 致命 |
| `anp.port` 在 [0, 65535] 范围内 | 致命 |
| `anp.meta_protocol_enabled` 但 `anp.port` ≤ 0 | 致命 |
| `anp.enabled` 但 `did_domain` 为空 | 警告 |
| `anp.e2ee_enabled` 但 `meta_protocol_enabled` 为 false | 警告 |
| `okf.injector_enabled` 但 `memoryflow.enabled` 为 false | 警告 |
| `okf.enrichment_enabled` 但无 `default_provider` | 警告 |

**CLI**: `wukong config validate`

---

## 1. 全局设置

```yaml
log_level: "info"
default_provider: "lmstudio"
lightweight_provider: "lmstudio"
lightweight_model: "gemma-4-e4b-it"
project_dir: "~/.config/wukong/"
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
  # ... deepseek, anthropic, ollama, lmstudio, acp
```

---

## 3. Agent

```yaml
agent:
  max_llm_calls: 50
  max_tool_iterations: 30
  temperature: 0.7
  max_tokens: 4096
  parallel_tools: true
  streaming: true
  tool_search_enabled: true
  context_compaction: true
  todo_tool_enabled: true
  todo_enforcer_enabled: true
```

| 核心字段 | 默认值 | 说明 |
|----------|--------|------|
| `max_llm_calls` | 50 | 单次运行最大 LLM API 调用数 (0 = 无限) |
| `max_tool_iterations` | 30 | 单次运行最大工具调用迭代数 |
| `temperature` | 0.7 | LLM 采样温度 [0.0, 2.0] |
| `max_tokens` | 4096 | 单次 LLM 调用最大输出 token 数 |
| `parallel_tools` | true | 独立工具调用并发执行 |
| `streaming` | true | TUI 实时 token 流输出 |
| `planner` | "" | "" / "builtin" / "react" |
| `tool_search_enabled` | false | TopK 工具过滤 |
| `tool_search_max_tools` | 20 | 每次模型调用最多暴露工具数 |
| `context_compaction` | false | 上下文压缩防溢出 |
| `session_recall_enabled` | false | 跨会话上下文预加载 |
| `json_repair_enabled` | false | 自动修复非标准 JSON |
| `todo_tool_enabled` | true | tRPC 原生 todo_write 工具 |
| `todo_enforcer_enabled` | true | 强制完成 todo 后才能最终回复 |
| `agent_tools_enabled` | true | AgentToolSet 子代理工具包装 |
| `recipe_enabled` | true | YAML 配方子代理系统 |

---

## 4. Security

```yaml
security:
  permission_mode: "smart"          # auto | smart | manual | chat_only
  require_approval: false           # 遗留字段
  malware_scan_enabled: true
  block_dangerous_commands: true
  blocked_commands: ["rm -rf /", "dd if=/dev/zero", "mkfs."]
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
  backend: "sqlite"                 # sqlite | memory | redis
  db_path: "wukong.db"
  event_limit: 500
  ttl: "0h"
  enable_summary: true
  summary_trigger: 50
  # redis_url: "redis://localhost:6379/0"
```

---

## 6. Memory

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
  enable_smart_cleanup: true
  cleanup_trigger_threshold: 0.8
  cleanup_target_threshold: 0.6
  memory_ttl: "720h"                # 30 天
```

---

## 7. Todo

```yaml
todo:
  backend: "sqlite"
  db_path: "wukong.db"
  enable_native_todo: true
  enable_enforcer: true
```

---

## 8. Recall

```yaml
recall:
  enabled: true
  backend: "sqlite"
  db_path: "wukong.db"
  search_mode: "fts5"               # fts5 | hybrid
  max_results: 10
  max_messages_per_session: 200
  # embedding_provider: ""          # hybrid 模式需要
  # embedding_model: ""
```

---

## 9. CortexDB 记忆栈

```yaml
cortex:
  enabled: true
  db_path: "wukong.db"
  max_results: 10
  max_messages_per_session: 200
  embedding_base_url: "http://localhost:1234"
  embedding_api_key: "lmstudio"
  embedding_model: "qwen3-embedding-0.6b"

memoryflow:
  enabled: true
  db_path: "wukong.db"
  namespace: "assistant"
  embedding_dimensions: 0           # 0 = 自动检测
  # planner_model: ""
  # extractor_model: ""

graphflow:
  enabled: true
  db_path: "wukong.db"
  max_chars_per_doc: 8000
  auto_extract: true
  # extractor_model: ""

importflow:
  enabled: true
  db_path: "wukong.db"
```

---

## 10. Revision

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
  search_strategy: "include_all"    # include_all | semantic
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
  browser_path: ""
  stealth: true
  cache_dir: ".wukong/cache"
  max_download_size: 104857600      # 100 MB
  timeout: "60s"
  viewport_width: 1280
  viewport_height: 720
  search_backend: "duckduckgo"      # duckduckgo | searxng | tavily
  # search_backend_url: ""          # SearXNG URL
  # search_api_key: ""              # Tavily API Key
```

---

## 12. 功能工具

```yaml
visualiser: { enabled: true, output_dir: ".wukong/visuals" }
tutorial: { enabled: true, language: "zh" }
top_of_mind: { enabled: true, instruction_file: ".wukong/instructions.md" }
code_mode: { enabled: true, timeout: "10s", max_memory_mb: 128 }
```

---

## 13. Apps — 网站克隆 + ZIM 打包

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
    # 完整字段见 config.yaml section 18
  pack:
    compress: true
    format: "html"                  # html | zim | binary | app
    language: "eng"
    creator: "Wukong"
```

---

## 14. ARD

```yaml
ard:
  enabled: false
  registry_url: ""                  # 远程 Registry URL
  catalog_path: ".wukong/ard/catalog.json"
  publish_enabled: false            # 发布自身为可发现 Registry
  publish_port: 0                   # 0 = 禁用
```

---

## 15. Summon

```yaml
summon:
  enabled: true
  skills_dir: ".wukong/skills"
  max_concurrent: 5
  a2a_remotes: []                   # A2A 远程 Agent 列表
```

---

## 16. Skill

```yaml
skill:
  enabled: true
  skills_dir: ".wukong/skills"
  auto_load: true
  max_skills: 20
```

---

## 17. Evolution

```yaml
evolution:
  enabled: false
  auto_patch: false
  analysis_provider: ""             # 空 = default_provider
  analysis_model: ""                # 空 = provider 默认模型
  min_confidence: 0.7
  cooldown_period: "30m"
  max_patches_per_day: 10
  max_versions_kept: 10
  max_patch_size: 8192
  analysis_timeout: "60s"
```

---

## 18. Knowledge (RAG)

```yaml
knowledge:
  enabled: false
  embedder_provider: ""             # 空 = default_provider
  embedder_model: "text-embedding-3-small"
  # sources: ["./docs"]             # 本地文档目录
  # source_urls: ["https://example.com/doc"]
  vector_store: "inmemory"
  max_results: 5
  enable_source_sync: false
  reranker_enabled: false
  search_tool_name: "knowledge_search"
```

---

## 19. OKF — Open Knowledge Format

```yaml
okf:
  enabled: false
  bundle_dir: ".wukong/okf"
  injector_enabled: false           # 注入 OKF index 到 MemoryFlow 唤醒上下文
  enrichment_enabled: false         # 自动生成概念文档
  # enrichment_output_dir: ""       # 空 = 使用 bundle_dir
  auto_export: false                # 会话结束时自动导出知识包
  register_in_ard: false            # 注册到 ARD 目录供联邦发现
```

| 字段 | 默认值 | 说明 |
|------|--------|------|
| `enabled` | false | 全局开关 OKF 子系统 |
| `bundle_dir` | `.wukong/okf` | OKF Bundle 默认存储/读取路径 |
| `injector_enabled` | false | 将 OKF index.md 注入 MemoryFlow 唤醒上下文 |
| `enrichment_enabled` | false | 启用 EnrichmentAgent 自动生成概念 |
| `auto_export` | false | 会话结束时自动导出知识包 |
| `register_in_ard` | false | 将 OKF Bundle 注册到 ARD 目录供联邦发现 |

---

## 20. Workflow

```yaml
workflow:
  mode: "single"                    # single|chain|parallel|cycle|graph|team_coordinator|team_swarm|claude_code|codex|dify
  max_iterations: 10
  cycle_mode: "default"             # default | code_review
  stream_mode: "none"               # none | hub
  cache_enabled: false
  engine: "bsp"                     # bsp | dag
  sub_agents: []
  # team_members: []                # team_coordinator/team_swarm 模式
  # claude_code_bin: "claude"       # claude_code 模式
  # codex_bin: "codex"              # codex 模式
```

---

## 21. Dify

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

## 22. ANP — Agent Network Protocol

```yaml
anp:
  enabled: false
  # did_domain: "wukong.example.com"   # DID 域名 (默认: os.Hostname())
  # did_path: "/agents/wukong"         # DID 路径后缀
  port: 9092                           # ANP HTTP 服务器端口
  discovery_enabled: true              # ADP 发现端点
  meta_protocol_enabled: true          # JSON-RPC 2.0 能力协商
  http_sign_enabled: true              # RFC 9421 HTTP 签名
  e2ee_enabled: true                   # X25519+ChaCha20-Poly1305 E2EE
  a2a_enabled: true                    # 自动创建 A2A Server 卡
  mcp_enabled: true                    # 自动创建 MCP Server 卡
  agui_enabled: true                   # 自动创建 AG-UI Server 卡
```

| 字段 | 默认值 | 说明 |
|------|--------|------|
| `enabled` | false | 启用 ANP 协议栈 |
| `did_domain` | `os.Hostname()` | did:wba 标识符的域名部分 |
| `did_path` | `/agents/<agent_name>` | DID 可选路径后缀 |
| `port` | 9092 | ANP HTTP 服务器监听端口 |
| `discovery_enabled` | true | 暴露 `.well-known/agent-descriptions` 端点 |
| `meta_protocol_enabled` | true | 启用 JSON-RPC 2.0 能力协商引擎 |
| `http_sign_enabled` | true | 对出站请求启用 RFC 9421 HTTP 签名 |
| `e2ee_enabled` | true | 启用 X25519+ChaCha20-Poly1305 E2EE 加密 |
| `a2a_enabled` | true | A2A Server 启用时自动创建 ANP 接口卡 |
| `mcp_enabled` | true | ACP MCP Bridge 启用时自动创建 ANP 接口卡 |
| `agui_enabled` | true | AG-UI Server 启用时自动创建 ANP 接口卡 |

**依赖关系**:
- E2EE 密钥交换依赖 Meta-Protocol (`e2ee_enabled` → `meta_protocol_enabled`)
- HTTP 签名独立运行 (`http_sign_enabled` 可独立启用)
- 接口卡自动创建依赖对应的服务端点启用状态

---

## 23. 服务端点

```yaml
a2a_server: { enabled: true, address: ":9090", agent_name: "wukong" }
agui: { enabled: true, address: ":8080", path: "/agui" }
acp_server: { enabled: true, address: ":9091", path: "/acp", enable_streaming: true }
acp_mcp: { enabled: true, address: ":3400", path: "/mcp" }
```

---

## 24. 观测与扩展

```yaml
eval: { enabled: false }
extensions: []                       # MCP 外部扩展列表
telemetry: { enabled: false, exporter_type: "console", endpoint: "localhost:4317" }
observability: { langfuse_enabled: false }
artifact: { backend: "inmemory" }    # inmemory | cos
```

---

## 25. 推荐配置

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
  a2a_enabled: true
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

### 完整记忆 + OKF + ANP

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
```

---

## 配置项完整索引

| 配置段 | 结构体 | 字段数 | CLI 入口 |
|--------|--------|--------|----------|
| `log_level` | — | 1 | — |
| `default_provider` | — | 1 | provider list |
| `lightweight_*` | — | 2 | config show |
| `project_dir` | — | 1 | project list |
| `providers` | `ProviderConfig` | 8 | provider list/test |
| `extensions` | `ExtensionConfig` | 16 | extension list/install |
| `agent` | `AgentConfig` | 32 | config show |
| `security` | `SecurityConfig` | 13 | system-check |
| `session` | `SessionConfig` | 8 | session list |
| `memory` | `MemoryConfig` | 13 | memory list |
| `todo` | `TodoConfig` | 4 | todo status |
| `recall` | `RecallConfig` | 8 | cortex status |
| `cortex` | `CortexConfig` | 6 | cortex status |
| `memoryflow` | `MemoryFlowConfig` | 6 | cortex status |
| `graphflow` | `GraphFlowConfig` | 5 | cortex status |
| `importflow` | `ImportFlowConfig` | 2 | cortex status |
| `revision` | `RevisionConfig` | 11 | config show |
| `browser` | `BrowserConfig` | 14 | — |
| `visualiser` | `VisualiserConfig` | 4 | — |
| `tutorial` | `TutorialConfig` | 2 | — |
| `top_of_mind` | `TopOfMindConfig` | 3 | init |
| `code_mode` | `CodeModeConfig` | 3 | — |
| `apps` | `AppsConfig` + `CloneDefaults` + `PackDefaults` | 28 | apps clone/pack |
| `ard` | `ARDConfig` | 5 | ard status |
| `summon` | `SummonConfig` + `A2ARemoteConfig` | 14 | — |
| `skill` | `SkillConfig` | 4 | skill list |
| `evolution` | `EvolutionConfig` | 10 | evolution status |
| `knowledge` | `KnowledgeConfig` | 10 | knowledge status |
| `okf` | `OKFConfig` | 7 | config show |
| `anp` | `ANPConfig` | 11 | config show |
| `workflow` | `WorkflowConfig` + `WorkflowSubAgentConfig` + `TeamMemberConfig` | 12 | — |
| `dify` | `DifyConfig` | 6 | — |
| `a2a_server` | `A2AServerConfig` | 4 | server |
| `agui` | `AGUIConfig` | 3 | server |
| `acp_server` | `ACPServerConfig` | 6 | server |
| `acp_mcp` | `ACPMCPConfig` | 3 | server |
| `eval` | `EvalConfig` + `EvalMetricConfig` | 4 | eval |
| `artifact` | `ArtifactConfig` | 4 | config show |
| `observability` | `ObservabilityConfig` | 4 | health |
| `telemetry` | `TelemetryConfig` | 8 | health |
