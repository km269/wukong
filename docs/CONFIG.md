# Wukong 配置参考

> **配置文件**: `config.yaml` | **加载器**: Viper + Cobra
> **结构体**: 45 · ~350 字段 | **配置代码**: 4 文件 (config.go + types.go + defaults.go + validate.go)
>
> Go: 1.26 | 文件: 233 `.go` (52 `_test.go`) | CLI: 28 顶层 + 55+ 子命令

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

---

## 4. Security

```yaml
security:
  permission_mode: "smart"
  block_dangerous_commands: true
  blocked_commands: ["rm -rf /", "dd if=/dev/zero", "mkfs."]
  ignore_file_enabled: true
  ignore_file: ".wukongignore"
```

---

## 5. Session

```yaml
session:
  backend: "sqlite"
  db_path: "wukong.db"
  event_limit: 500
  enable_summary: true
```

---

## 6. Memory

```yaml
memory:
  backend: "sqlite"
  db_path: "wukong.db"
  max_memories: 100
  auto_extract: true
  enable_smart_cleanup: true
  cleanup_trigger_threshold: 0.8
  cleanup_target_threshold: 0.6
  memory_ttl: "720h"
```

---

## 7. Todo

```yaml
todo:
  backend: "sqlite"
  enable_native_todo: true
  enable_enforcer: true
```

---

## 8. Recall

```yaml
recall:
  enabled: true
  search_mode: "fts5"
  max_results: 10
```

---

## 9. CortexDB 记忆栈

```yaml
cortex:
  enabled: true
  embedding_model: "qwen3-embedding-0.6b"

memoryflow:
  enabled: true
  namespace: "assistant"

graphflow:
  enabled: true
  auto_extract: true

importflow:
  enabled: true
```

---

## 10. Revision

```yaml
revision:
  enabled: true
  enable_llm_summarize: true
  max_context_tokens: 64000
```

---

## 11. Browser

```yaml
browser:
  enabled: true
  stealth: true
  cache_dir: ".wukong/cache"
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
    dedup_content: true
    enable_resume: true
  pack:
    compress: true
    format: "html"
```

---

## 14. ARD

```yaml
ard:
  enabled: false
  catalog_path: ".wukong/ard/catalog.json"
```

---

## 15. Summon

```yaml
summon:
  enabled: true
  skills_dir: ".wukong/skills"
  max_concurrent: 5
```

---

## 16. Skill

```yaml
skill:
  enabled: true
  skills_dir: ".wukong/skills"
  auto_load: true
```

---

## 17. Evolution

```yaml
evolution:
  enabled: false
  min_confidence: 0.7
  cooldown_period: "30m"
```

---

## 18. Knowledge (RAG)

```yaml
knowledge:
  enabled: false
  embedder_model: "text-embedding-3-small"
  vector_store: "inmemory"
  max_results: 5
```

---

## 19. OKF — Open Knowledge Format

```yaml
okf:
  enabled: false
  bundle_dir: ".wukong/okf"
  injector_enabled: false
  enrichment_enabled: false
  # enrichment_output_dir: ""     # empty = use bundle_dir
  auto_export: false
  register_in_ard: false
```

| 字段 | 说明 |
|------|------|
| `enabled` | 全局开关 OKF 子系统 |
| `bundle_dir` | OKF Bundle 默认存储/读取路径 |
| `injector_enabled` | 将 OKF index.md 注入 MemoryFlow 唤醒上下文 |
| `enrichment_enabled` | 启用 EnrichmentAgent 自动生成概念 |
| `auto_export` | 会话结束时自动导出知识包 |
| `register_in_ard` | 将 OKF Bundle 注册到 ARD 目录供联邦发现 |

---

## 20. Workflow

```yaml
workflow:
  mode: "single"
  max_iterations: 10
```

---

## 21. Dify

```yaml
dify:
  enabled: false
  timeout: "120s"
```

---

## 22. 服务端点

```yaml
a2a_server: { enabled: true, address: ":9090" }
agui: { enabled: true, address: ":8080", path: "/agui" }
acp_server: { enabled: true, address: ":9091", path: "/acp" }
acp_mcp: { enabled: true, address: ":3400", path: "/mcp" }
```

---

## 23. 观测与扩展

```yaml
eval: { enabled: false }
extensions: []
telemetry: { enabled: false, exporter_type: "console" }
observability: { langfuse_enabled: false }
artifact: { backend: "inmemory" }
```

---

## 24. 推荐配置

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

### 完整记忆 + OKF

```yaml
cortex: { enabled: true }
memoryflow: { enabled: true }
graphflow: { enabled: true, auto_extract: true }
importflow: { enabled: true }
recall: { enabled: true }
revision: { enabled: true }
okf: { enabled: true, injector_enabled: true }
```

---

## 配置项完整索引

| 配置段 | 结构体 | CLI 入口 |
|--------|--------|----------|
| `log_level` | — | — |
| `default_provider` | — | provider list |
| `lightweight_*` | — | config show |
| `project_dir` | — | project list |
| `providers` | `ProviderConfig` | provider list/test |
| `extensions` | `ExtensionConfig` | extension list/install |
| `agent` | `AgentConfig` | config show |
| `security` | `SecurityConfig` | system-check |
| `session` | `SessionConfig` | session list |
| `memory` | `MemoryConfig` | memory list |
| `todo` | `TodoConfig` | todo status |
| `recall` | `RecallConfig` | cortex status |
| `cortex` | `CortexConfig` | cortex status |
| `memoryflow` | `MemoryFlowConfig` | cortex status |
| `graphflow` | `GraphFlowConfig` | cortex status |
| `importflow` | `ImportFlowConfig` | cortex status |
| `revision` | `RevisionConfig` | config show |
| `browser` | `BrowserConfig` | — |
| `visualiser` | `VisualiserConfig` | — |
| `tutorial` | `TutorialConfig` | — |
| `top_of_mind` | `TopOfMindConfig` | init |
| `code_mode` | `CodeModeConfig` | — |
| `apps` | `AppsConfig` + `CloneDefaults` + `PackDefaults` | apps clone/pack |
| `ard` | `ARDConfig` | ard status |
| `summon` | `SummonConfig` | — |
| `skill` | `SkillConfig` | skill list |
| `evolution` | `EvolutionConfig` | evolution status |
| `knowledge` | `KnowledgeConfig` | knowledge status |
| `okf` | `OKFConfig` | config show |
| `workflow` | `WorkflowConfig` | — |
| `dify` | `DifyConfig` | — |
| `a2a_server` | `A2AServerConfig` | server |
| `agui` | `AGUIConfig` | server |
| `acp_server` | `ACPServerConfig` | server |
| `acp_mcp` | `ACPMCPConfig` | server |
| `eval` | `EvalConfig` | eval |
| `artifact` | `ArtifactConfig` | config show |
| `observability` | `ObservabilityConfig` | health |
| `telemetry` | `TelemetryConfig` | health |
