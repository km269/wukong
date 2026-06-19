# Wukong 配置参考手册

> **配置文件**: `config.yaml` | **加载器**: Viper | **配置段**: 30 | **CLI+ENV 覆盖支持**

---

## 配置加载机制

```
优先级（从高到低）:
1. CLI 命令行参数 (--provider, --model, --temperature, --max-tokens, --no-stream, --session-id, --config)
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
default_provider: "lmstudio"   # 默认 LLM Provider
```

---

## 2. Providers

```yaml
providers:
  - name: "lmstudio"
    type: "lmstudio"
    base_url: "http://192.168.50.97:1234/v1"
    api_key: "lm-studio"
    model: "google/gemma-4-26b-a4b"
    max_tokens: 16384
```

| 字段 | 说明 |
|------|------|
| `name` | 唯一标识（被 default_provider/revision_provider 引用） |
| `type` | openai / anthropic / google / deepseek / ollama / lmstudio / acp |
| `base_url` | API 端点（已知厂商自动填充） |
| `api_key` | 认证密钥（支持 `${ENV_VAR}`） |
| `model` | 默认模型名 |

---

## 3. Agent

```yaml
agent:
  temperature: 0.7
  max_tokens: 16384
  stream: true
  tool_search_enabled: false
  tool_search_max_tools: 20
  context_compaction: true
  context_compaction_tool_result_max_tokens: 1024
  context_compaction_oversized_max_tokens: 8192
  session_recall_enabled: false
  json_repair_enabled: true
  todo_tool_enabled: true
  todo_enforcer_enabled: true
  agent_tools_enabled: true
  agent_tools_stream: false
  system_prompt_dir: ""
  recipe_dir: ".wukong/recipes"
  recipe_enabled: true
```

| 关键字段 | 默认 | 说明 |
|----------|------|------|
| `temperature` | 0.7 | 生成温度 (0-2) |
| `max_tokens` | 16384 | 最大输出 token |
| `stream` | true | 流式输出 |
| `todo_tool_enabled` | true | tRPC 原生 todo_write 工具 |
| `json_repair_enabled` | true | 自动修复工具调用 JSON |

---

## 4. Security

```yaml
security:
  guardrail_enabled: false
  allowed_tools: []
  denied_tools: []
  max_tool_calls: 100
  tool_timeout_seconds: 300
```

**4 级权限**：allowed_tools (白名单) → denied_tools (黑名单) → max_tool_calls (限频) → tool_timeout (超时)

---

## 5. Session

```yaml
session:
  backend: "sqlite"
  db_path: "wukong.db"
  event_limit: 500
  ttl: "0h"
  enable_summary: true
  summary_trigger: 50
```

| 字段 | 说明 |
|------|------|
| `backend` | sqlite / memory / redis |
| `event_limit` | 每会话最大事件数 |
| `enable_summary` | 启用框架级异步摘要 |
| `summary_trigger` | 触发摘要的事件数阈值 |

---

## 6. Memory

```yaml
memory:
  backend: "sqlite"
  db_path: "wukong.db"
  max_memories: 100
  auto_extract: true
  extract_timeout: "120s"
  extractor_provider: "lmstudio"
  extractor_model: "qwen/qwen3.5-9b"
  extractor_prompt: |
    You are a Memory Manager. Extract concise memories...
```

| 字段 | 说明 |
|------|------|
| `auto_extract` | 启用异步记忆提取 |
| `extractor_provider` | 提取模型 Provider（推荐轻量模型） |
| `extractor_model` | 提取模型名（9B 模型速度快 10 倍） |
| `extract_timeout` | 单次提取超时 |
| `max_memories` | 每用户最大记忆数 |

**记忆工具**：6 个 (memory_add / search / update / delete / load / clear)

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
  max_results: 10
  max_messages_per_session: 200
  search_mode: "fts5"
```

| 搜索模式 | 说明 |
|----------|------|
| `fts5` | 纯 FTS5 全文搜索（默认，零配置） |
| `hybrid` | 语义搜索 + FTS5 混合排序（需要 embedding provider） |

**回溯工具**：2 个 (recall_search / recall_sessions)

---

## 9. Cortex — 智能回溯

```yaml
cortex:
  enabled: false
  db_path: "cortex.db"
  max_results: 10
  max_messages_per_session: 200
  embedding_base_url: ""
  embedding_api_key: ""
  embedding_model: "text-embedding-3-small"
```

启用后替换原生 recall 为 CortexDB 向量+FTS5 混合搜索。

---

## 10. MemoryFlow — 代理记忆工作流

```yaml
memoryflow:
  enabled: true
  db_path: "memoryflow.db"
  namespace: "assistant"
  embedding_dimensions: 0
  planner_model: ""
  extractor_model: ""
```

| 能力 | 说明 |
|------|------|
| **IngestTurn** | 每轮对话自动记录转录 |
| **WakeUp** | 运行前召回历史上下文注入 message 前缀 |
| **PromoteFacts** | 将重要事实提升到 tRPC Memory |

---

## 11. GraphFlow — 知识图谱

```yaml
graphflow:
  enabled: true
  db_path: "graphflow.db"
  max_chars_per_doc: 8000
  auto_extract: false
  extractor_model: ""
```

| 能力 | 说明 |
|------|------|
| **Entity Extraction** | 启发式或 LLM 实体/关系提取 |
| **SPARQL Query** | 知识图谱查询 |
| **Graph Analysis** | 图谱统计和模式分析 |

**KG 工具**：2 个 (knowledge_graph_query / knowledge_graph_analyze)

---

## 12. ImportFlow — 结构化数据导入

```yaml
importflow:
  enabled: true
  db_path: "importflow.db"
```

**导入工具**：4 个 (importflow_ddl_parse / importflow_ddl_plan / importflow_ddl_plan_ai / importflow_csv)

---

## 13. Revision — 上下文压缩

```yaml
revision:
  enabled: true
  revision_provider: "lmstudio"
  revision_model: "qwen/qwen3.5-9b"
  enable_llm_summarize: false
  summary_cooldown: 120s
  summary_timeout: 30s
  max_command_output: 8000
  enable_semantic_search: false
  search_strategy: "include_all"
  max_context_tokens: 64000
  trim_ratio: 0.3
```

**三层压缩策略**：

| 层级 | 策略 | 触发条件 |
|------|------|----------|
| 1 | **LLM 智能摘要** | enable_llm_summarize=true + revision_model 已配置 |
| 2 | **渐进式压缩** | 已有摘要 + 新消息增量合并 |
| 3 | **算法截断** | LLM 不可用时回退 |

**3 种触发条件**：
1. 估算 Token 超过 `max_context_tokens × (1 - trim_ratio)`
2. 消息数超过 100 条
3. 距上次修订超过 5 分钟

---

## 14-20. 其他配置段

| 段 | 说明 |
|----|------|
| `browser` | Chromedp 浏览器自动化 |
| `visualiser` | Mermaid/ECharts 可视化 |
| `tutorial` | 交互式教程 |
| `top_of_mind` | 持久指令 CRUD |
| `code_mode` | goja JS 沙箱 |
| `apps` | HTML 应用管理 |
| `summon` | 子代理调度 |

---

## 21-30. 高级配置

| 段 | 说明 |
|----|------|
| `skill` | 技能管理 |
| `evolution` | 技能自进化引擎 |
| `knowledge` | RAG 知识检索 |
| `artifact` | 制品存储 (inmemory/COS) |
| `project` | 项目追踪 |
| `observability` | OpenTelemetry + Langfuse |
| `workflow` | 多模式编排 (10 modes) |
| `eval` | 回归测试评估 |
| `extensions` | 外部 MCP 扩展配置 |

---

## 推荐的记忆系统配置

```yaml
# 最小化记忆配置（开箱即用）
memory:
  auto_extract: true
  extractor_provider: "lmstudio"       # 与主模型同 provider
  extractor_model: "qwen/qwen3.5-9b"   # 轻量模型做提取

memoryflow:
  enabled: true                         # 对话转录 + 唤醒

graphflow:
  enabled: true                         # 知识图谱

importflow:
  enabled: true                         # 结构化数据导入

revision:
  enabled: true
  revision_provider: "lmstudio"
  revision_model: "qwen/qwen3.5-9b"     # 轻量模型做摘要
```

首次运行后，下次对话即自动拥有记忆上下文。
