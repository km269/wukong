# Wukong 配置参考手册

> 配置文件: `config.yaml` | 加载器: Viper + Cobra
> 配置结构: 15 组 (A-O) | 配置代码: 13 文件 | 34 配置结构体
> 验证规则: 致命错误 + 非致命警告 | 环境变量展开: 15 类敏感字段

---

## 目录

1. [加载优先级](#1-加载优先级-7-级)
2. [环境变量展开](#2-环境变量展开)
3. [配置验证](#3-配置验证)
4. [全局配置 (A 组)](#4-全局配置-a-组)
5. [Providers 配置 (B 组)](#5-providers-配置-b-组)
6. [Agent 配置 (C 组)](#6-agent-配置-c-组)
7. [Security 配置 (D 组)](#7-security-配置-d-组)
8. [Storage 配置 (E 组)](#8-storage-配置-e-组)
9. [CortexDB 配置 (F 组)](#9-cortexdb-配置-f-组)
10. [Context 配置 (G 组)](#10-context-配置-g-组)
11. [Feature Tools 配置 (H 组)](#11-feature-tools-配置-h-组)
12. [Extensions 配置 (I 组)](#12-extensions-配置-i-组)
13. [Service Endpoints 配置 (J 组)](#13-service-endpoints-配置-j-组)
14. [Agent Communication 配置 (K 组)](#14-agent-communication-配置-k-组)
15. [Knowledge & Skill 配置 (L 组)](#15-knowledge--skill-配置-l-组)
16. [Orchestration 配置 (M 组)](#16-orchestration-配置-m-组)
17. [Observability 配置 (N 组)](#17-observability-配置-n-组)
18. [Apps 配置 (O 组)](#18-apps-配置-o-组)
19. [完整配置示例](#19-完整配置示例)

---

## 1. 加载优先级 (7 级)

配置按以下优先级从高到低解析，高优先级覆盖低优先级：

```
优先级 1 — CLI 参数 (最高)
   ├── --provider, --model, --temperature, --max-tokens
   ├── --config (指定配置文件路径)
   └── --debug, --quiet (日志级别)

优先级 2 — 环境变量
   └── WUKONG_ 前缀，下划线分隔，e.g. WUKONG_DEFAULT_PROVIDER

优先级 3 — --config 指定的配置文件

优先级 4 — 当前目录配置文件
   └── ./config.yaml

优先级 5 — 用户目录配置文件
   └── ~/.config/wukong/config.yaml

优先级 6 — 系统级配置文件 (非 Windows)
   └── /etc/wukong/config.yaml

优先级 7 — 内置默认值 (最低)
   └── internal/config/defaults.go
```

**配置文件搜索路径**（未指定 `--config` 时）：

| 平台 | 搜索路径 |
|------|---------|
| 全部 | `./config.yaml` (当前目录) |
| 全部 | `~/.config/wukong/config.yaml` |
| Linux/macOS | `/etc/wukong/config.yaml` |

---

## 2. 环境变量展开

支持 `${ENV_VAR}` 和 `${VAR:-default}` 两种语法，运行时自动展开。

### 2.1 语法

```yaml
# 直接引用环境变量
api_key: ${OPENAI_API_KEY}

# 带默认值的引用
base_url: ${OPENAI_BASE_URL:-https://api.openai.com/v1}
```

### 2.2 支持展开的字段 (15 类)

| 类别 | 字段 |
|------|------|
| **Providers** | `api_key`, `base_url`, `model` |
| **A2A Remotes** | `api_key`, `jwt_secret`, `oauth_client_secret` |
| **Gateway Feishu** | `app_secret`, `encrypt_key`, `verification_token` |
| **CortexDB** | `embedding_api_key`, `embedding_base_url`, `embedding_model` |
| **MemoryFlow** | `planner_model`, `extractor_model` |
| **GraphFlow** | `extractor_model` |
| **Dify** | `api_secret` |
| **Observability (Langfuse)** | `public_key`, `secret_key` |
| **Artifact (COS)** | `cos_secret_id`, `cos_secret_key` |
| **ACP Server** | `api_key` |
| **Session** | `redis_url` |
| **Browser Search (SearXNG)** | `url`, `api_key` |
| **Browser Search (Tavily)** | `api_key` |
| **Browser Search (Google)** | `api_key`, `cse_id` |
| **Browser Search (Bing)** | `api_key` |

---

## 3. 配置验证

配置加载后自动执行验证，分为**致命错误**和**非致命警告**两类。

### 3.1 致命错误 (Validate)

触发以下任一错误将导致程序启动失败：

| 检查项 | 有效值/范围 |
|--------|------------|
| `default_provider` 存在性 | 必须在 `providers` 列表中存在 |
| `providers[].type` 有效性 | `openai` / `anthropic` / `google` / `deepseek` / `ollama` / `lmstudio` / `acp` |
| `agent.temperature` 范围 | [0.0, 2.0] |
| `agent.max_tokens` | >= 0 |
| `agent.max_llm_calls` | >= 0 |
| `agent.max_tool_iterations` | >= 0 |
| `security.permission_mode` | `auto` / `smart` / `manual` / `chat_only` |
| `memory.cleanup_trigger_threshold` | [0.0, 1.0] |
| `memory.cleanup_target_threshold` | [0.0, 1.0] |
| `memory.cleanup_target_threshold` < `cleanup_trigger_threshold` | 必须满足 |
| `memory.scoring_weights.*` | [0.0, 1.0] |
| `memory.extract_timeout` | 有效持续时间 |
| `todo.backend` | `sqlite` / `memory` / 空 |
| `evolution.min_confidence` | [0.0, 1.0] |
| `apps.clone.workers` | >= 1 |
| `apps.pack.workers` | >= 1 |
| `revision.trim_ratio` | [0.0, 1.0] |
| `orchestration.workflow.mode` | 10 种有效模式 |
| `telemetry.sample_rate` | [0.0, 1.0] |
| `anp.port` | [0, 65535] |
| `anp.meta_protocol_enabled` 但 `port <= 0` | 不允许 |
| `session.backend` | `sqlite` / `memory` / `redis` / 空 |
| `memory.backend` | `sqlite` / `redis` / 空 |
| `recall.search_mode` | `fts5` / `hybrid` / 空 |
| `artifact.backend` | `inmemory` / `cos` / 空 |

### 3.2 非致命警告 (Warnings)

以下问题仅记录警告，不阻止启动：

| 警告项 | 说明 |
|--------|------|
| 无 providers 配置 | 无法进行 LLM 对话 |
| `memory.auto_extract` 启用但无 `default_provider` | 记忆提取无法执行 |
| `cortex.enabled` 但无 `embedding_model` | 向量搜索不可用 |
| `okf.enabled` 但 `bundle_dir` 为空 | OKF 注入无效 |
| `anp.enabled` 但 `did_domain` 为空 | DID 身份无法生成 |
| `gateway.enabled` 但无 channel 激活 | 消息网关无可用通道 |

---

## 4. 全局配置 (A 组)

### 4.1 顶层字段

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `default_provider` | string | - | 默认 LLM Provider 名称，必须匹配 `providers[].name` |
| `log_level` | string | `info` | 日志级别: `debug` / `info` / `warn` / `error` |
| `lightweight_provider` | string | - | 后台任务轻量 Provider，为空时使用 default_provider |
| `lightweight_model` | string | - | 后台任务轻量模型，为空时使用各子系统默认 |
| `project_dir` | string | `~/.config/wukong/` | 项目数据目录 |

### 4.2 轻量模型自动应用

当设置 `lightweight_model` 时，以下字段如未显式配置将自动继承：
- `memory.extractor_model`
- `memoryflow.planner_model`
- `memoryflow.extractor_model`
- `graphflow.extractor_model`
- `revision.revision_model`

---

## 5. Providers 配置 (B 组)

### 5.1 Provider 类型

| 类型 | 常量 | 说明 |
|------|------|------|
| `openai` | `ProviderOpenAI` | OpenAI 兼容 API |
| `anthropic` | `ProviderAnthropic` | Anthropic Claude |
| `google` | `ProviderGoogle` | Google Gemini |
| `deepseek` | `ProviderDeepSeek` | DeepSeek |
| `ollama` | `ProviderOllama` | 本地 Ollama |
| `lmstudio` | `ProviderLMStudio` | LM Studio |
| `acp` | `ProviderACP` | Agent Client Protocol |

### 5.2 ProviderConfig 字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | string | Provider 名称 (唯一标识) |
| `type` | string | Provider 类型，见上表 |
| `base_url` | string | API 基础 URL |
| `api_key` | string | API 密钥 (支持 env 展开) |
| `model` | string | 默认模型名称 |
| `agent_url` | string | ACP Agent URL |
| `mcp_port` | string | ACP MCP 端口 |

### 5.3 配置示例

```yaml
providers:
  - name: openai-main
    type: openai
    base_url: ${OPENAI_BASE_URL:-https://api.openai.com/v1}
    api_key: ${OPENAI_API_KEY}
    model: gpt-4o

  - name: local-ollama
    type: ollama
    base_url: http://localhost:11434
    model: qwen2.5:7b
```

---

## 6. Agent 配置 (C 组)

### 6.1 核心行为

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `max_llm_calls` | int | 50 | 单次 Run 最大 LLM 调用次数 |
| `max_tool_iterations` | int | 30 | 单次 Run 最大工具迭代次数 |
| `max_run_duration` | duration | 900s | 单次 Run 最大执行时长 |
| `parallel_tools` | bool | true | 是否并行执行工具调用 |
| `streaming` | bool | true | 是否启用流式输出 |

### 6.2 生成参数

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `temperature` | float | 0.7 | 采样温度 [0.0, 2.0] |
| `max_tokens` | int | 4096 | 最大生成 token 数 |
| `reasoning_effort` | string | - | 推理努力程度 (Claude) |
| `thinking_enabled` | *bool | - | 是否启用思考模式 |
| `thinking_tokens` | *int | - | 思考 token 预算 |

### 6.3 工具重试

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `tool_retry_enabled` | bool | true | 是否启用工具重试 |
| `tool_retry_max_attempts` | int | 3 | 最大重试次数 |
| `tool_retry_initial_wait` | duration | 1s | 初始等待时间 |
| `tool_retry_backoff_factor` | float | 2.0 | 退避因子 |

### 6.4 规划器与工具搜索

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `planner` | string | 空 | 规划器类型: `builtin` / `react` |
| `tool_search_enabled` | bool | false | 是否启用工具自动过滤 |
| `tool_search_max_tools` | int | 20 | TopK 工具数量 |

### 6.5 上下文压缩

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `context_compaction` | bool | false | 是否启用上下文压缩 |
| `context_compaction_tool_result_max_tokens` | int | 1024 | 工具结果最大 token |
| `context_compaction_oversized_max_tokens` | int | 0 | 超限消息最大 token |
| `context_compaction_keep_recent` | int | 1 | 保留最近 N 轮 |
| `context_compaction_force_clean_tools` | []string | - | 强制清理结果的工具 |
| `context_compaction_keep_tools` | []string | - | 保留结果的工具 |

### 6.6 其他

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `session_recall_enabled` | bool | false | 是否启用会话召回 |
| `session_recall_limit` | int | 5 | 召回历史会话数 |
| `json_repair_enabled` | bool | false | 是否启用 JSON 修复 |
| `agent_tools_enabled` | bool | true | 是否启用 Agent 工具 |
| `agent_tools_stream` | bool | false | Agent 工具是否流式 |
| `enable_post_tool_prompt` | bool | true | 工具后是否追加提示 |
| `system_prompt_dir` | string | `~/.config/wukong/prompts/` | 系统提示词目录 |
| `recipe_dir` | string | `.wukong/recipes/` | Recipe 目录 |
| `recipe_enabled` | bool | true | 是否启用 Recipe |
| `inline_recipes` | []map | - | 内联 Recipe 定义 |

---

## 7. Security 配置 (D 组)

### 7.1 权限模式

| 模式 | 常量 | 说明 |
|------|------|------|
| `auto` | `PermissionAuto` | 自动批准所有工具调用 |
| `smart` | `PermissionSmart` | 高风险操作需用户批准 (默认) |
| `manual` | `PermissionManual` | 所有工具调用需用户批准 |
| `chat_only` | `PermissionChatOnly` | 禁止所有工具调用 |

### 7.2 安全配置字段

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `permission_mode` | string | `smart` | 权限模式，见上表 |
| `malware_scan_enabled` | bool | true | 是否启用恶意软件扫描 |
| `default_timeout` | duration | 30s | 工具执行默认超时 |
| `max_timeout` | duration | 300s | 工具执行最大超时 |
| `block_dangerous_commands` | bool | true | 是否拦截危险命令 |
| `blocked_commands` | []string | 见下 | 危险命令列表 |
| `require_approval` | bool | false | 是否要求审批 |
| `allowlist` | []string | - | 工具白名单 |
| `denylist` | []string | - | 工具黑名单 |
| `guardrail_enabled` | bool | false | 是否启用 Prompt 注入检测 |
| `ignore_file_enabled` | bool | true | 是否启用 .wukongignore |
| `ignore_file` | string | `.wukongignore` | 忽略文件名 |

### 7.3 默认拦截的危险命令

```
rm -rf /
dd if=/dev/zero
mkfs.
> /dev/sda
fork bomb
```

---

## 8. Storage 配置 (E 组)

### 8.1 Session 配置

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `session.backend` | string | `sqlite` | 存储后端: `sqlite` / `memory` / `redis` |
| `session.db_path` | string | - | 数据库文件路径 |
| `session.event_limit` | int | - | 每会话事件数限制 |
| `session.ttl` | duration | - | 会话过期时间 |
| `session.enable_summary` | bool | - | 是否启用会话摘要 |
| `session.summary_trigger` | int | - | 摘要触发阈值 |
| `session.redis_url` | string | - | Redis URL (支持 env 展开) |

### 8.2 Memory 配置

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `memory.backend` | string | `sqlite` | 存储后端: `sqlite` / `redis` |
| `memory.db_path` | string | - | 数据库文件路径 |
| `memory.max_memories` | int | - | 最大记忆条数 |
| `memory.auto_extract` | bool | - | 是否自动提取记忆 |
| `memory.extract_timeout` | duration | - | 提取超时时间 |
| `memory.extractor_provider` | string | - | 提取用 Provider |
| `memory.extractor_model` | string | - | 提取用模型 |
| `memory.extractor_prompt` | string | - | 提取提示词 |

#### 记忆评分权重

| 字段 | 类型 | 说明 |
|------|------|------|
| `memory.recency_weight` | float | 新鲜度权重 |
| `memory.reference_weight` | float | 引用频率权重 |
| `memory.importance_weight` | float | 重要性权重 |
| `memory.length_weight` | float | 长度权重 |

#### 智能清理

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `memory.dynamic_ttl` | bool | - | 是否动态 TTL |
| `memory.enable_smart_cleanup` | bool | - | 是否启用智能清理 |
| `memory.cleanup_trigger_threshold` | float | 0.8 | 触发清理阈值 [0.0, 1.0] |
| `memory.cleanup_target_threshold` | float | 0.6 | 清理目标阈值 [0.0, 1.0] |
| `memory.memory_ttl` | duration | - | 记忆过期时间 |

> **注意**: `cleanup_target_threshold` 必须小于 `cleanup_trigger_threshold`

### 8.3 Todo 配置

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `todo.backend` | string | `sqlite` | 存储后端: `sqlite` / `memory` |
| `todo.db_path` | string | - | 数据库文件路径 |
| `todo.enable_native_todo` | bool | - | 是否启用原生 Todo |
| `todo.enable_enforcer` | bool | - | 是否启用 Todo 强制执行 |

### 8.4 Recall 配置

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `recall.enabled` | bool | - | 是否启用跨会话召回 |
| `recall.backend` | string | `sqlite` | 存储后端 |
| `recall.db_path` | string | - | 数据库文件路径 |
| `recall.max_results` | int | - | 最大召回结果数 |
| `recall.max_messages_per_session` | int | - | 每会话最大消息数 |
| `recall.search_mode` | string | `fts5` | 搜索模式: `fts5` / `hybrid` |
| `recall.embedding_model` | string | - | 向量嵌入模型 |

---

## 9. CortexDB 配置 (F 组)

### 9.1 Cortex 主配置

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `cortex.enabled` | bool | false | 是否启用 CortexDB |
| `cortex.db_path` | string | - | 数据库路径 |
| `cortex.max_results` | int | - | 最大搜索结果数 |
| `cortex.max_messages_per_session` | int | - | 每会话最大消息数 |
| `cortex.embedding_base_url` | string | - | Embedding API URL (支持 env 展开) |
| `cortex.embedding_api_key` | string | - | Embedding API Key (支持 env 展开) |
| `cortex.embedding_model` | string | - | Embedding 模型 (支持 env 展开) |

### 9.2 MemoryFlow 配置

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `memoryflow.enabled` | bool | false | 是否启用 MemoryFlow |
| `memoryflow.db_path` | string | - | 数据库路径 |
| `memoryflow.namespace` | string | - | 命名空间 |
| `memoryflow.embedding_dimensions` | int | - | 向量维度 |
| `memoryflow.planner_model` | string | - | 规划器模型 (支持 env 展开) |
| `memoryflow.extractor_model` | string | - | 提取器模型 (支持 env 展开) |

### 9.3 GraphFlow 配置

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `graphflow.enabled` | bool | false | 是否启用 GraphFlow |
| `graphflow.db_path` | string | - | 数据库路径 |
| `graphflow.extractor_model` | string | - | 提取器模型 (支持 env 展开) |
| `graphflow.max_chars_per_doc` | int | - | 每文档最大字符数 |
| `graphflow.auto_extract` | bool | false | 是否自动抽取实体/关系 |

### 9.4 ImportFlow 配置

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `importflow.enabled` | bool | false | 是否启用 ImportFlow |
| `importflow.db_path` | string | - | 数据库路径 |

---

## 10. Context 配置 (G 组)

### 10.1 Revision 上下文管理

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `revision.enabled` | bool | false | 是否启用上下文优化 |
| `revision.revision_provider` | string | - | 摘要用 Provider |
| `revision.revision_model` | string | - | 摘要用模型 |
| `revision.enable_llm_summarize` | bool | false | 是否启用 LLM 摘要 |
| `revision.max_command_output` | int | - | 命令输出最大长度 |
| `revision.enable_semantic_search` | bool | false | 是否启用语义搜索 |
| `revision.search_strategy` | string | - | 搜索策略 |
| `revision.max_context_tokens` | int | - | 上下文 token 上限 |
| `revision.trim_ratio` | float | - | 裁剪比例 [0.0, 1.0] |
| `revision.summary_cooldown` | duration | - | 摘要冷却时间 |
| `revision.summary_timeout` | duration | - | 摘要超时时间 |

---

## 11. Feature Tools 配置 (H 组)

### 11.1 Visualiser 可视化

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `visualiser.enabled` | bool | - | 是否启用自动可视化 |
| `visualiser.output_dir` | string | - | 图表输出目录 |
| `visualiser.max_width` | int | - | 最大宽度 (px) |
| `visualiser.max_height` | int | - | 最大高度 (px) |

### 11.2 Tutorial 教程

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `tutorial.enabled` | bool | - | 是否启用教程引导 |
| `tutorial.language` | string | - | 教程语言 |

### 11.3 TopOfMind 置顶指令

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `topofmind.enabled` | bool | - | 是否启用置顶指令 |
| `topofmind.instruction_file` | string | - | 指令文件路径 |
| `topofmind.max_length` | int | - | 指令最大长度 |

### 11.4 Code Mode 代码沙箱

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `codemode.enabled` | bool | - | 是否启用 Code Mode |
| `codemode.timeout` | duration | - | JS 执行超时 |
| `codemode.max_memory_mb` | int | - | 内存限制 (MB) |

---

## 12. Extensions 配置 (I 组)

### 12.1 ExtensionConfig 字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | string | 扩展名称 |
| `type` | string | 扩展类型: `builtin` / `external` / `mcp_broker` |
| `transport` | string | 传输方式: `stdio` / `sse` / `http` |
| `command` | string | 启动命令 (stdio) |
| `args` | []string | 命令参数 |
| `url` | string | 服务 URL (sse/http) |
| `env` | map[string]string | 环境变量 |
| `enabled` | bool | 是否启用 |
| `timeout` | duration | 超时时间 |
| `deeplink` | string | 深度链接模板 |
| `permissions` | []ToolPermission | 工具权限 |
| `mcp_broker` | bool | 是否作为 MCP Broker |
| `mcp_tool_filter` | []string | MCP 工具白名单 |
| `mcp_tool_exclude` | []string | MCP 工具排除列表 |
| `mcp_session_reconnect` | bool | 会话重连 |
| `mcp_session_reconnect_attempts` | int | 重连尝试次数 |

### 12.2 ToolPermission 结构

| 字段 | 类型 | 说明 |
|------|------|------|
| `tool` | string | 工具名称 |
| `allowed` | bool | 是否允许 |

### 12.3 13 个内置扩展

| 扩展 | 说明 |
|------|------|
| `developer` | 开发工具集 (文件/命令/搜索) |
| `memory` | 记忆管理工具 |
| `browser` | 浏览器工具 |
| `apps` | 应用管理工具 |
| `ard` | ARD 发现工具 |
| `cortex` | CortexDB 工具 |
| `codemode` | Code Mode 执行器 |
| `aggregate_search` | 聚合搜索 |
| `google` / `bing` / `searxng` / `tavily` | 搜索引擎集成 |
| `topofmind` | 置顶指令 |
| `tutorial` | 教程引导 |
| `auto_visualiser` | 自动可视化 |

---

## 13. Service Endpoints 配置 (J 组)

### 13.1 服务端口一览

| 服务 | 默认端口 | 配置路径 |
|------|---------|---------|
| A2A Server | 9090 | `a2a_server.address` |
| ACP Server | 9091 | `acp_server.address` |
| ANP Server | 9092 | `anp.port` |
| Gateway | 9093 | `gateway.*` |
| AG-UI SSE | 8080 | `agui.address` |
| ACP MCP | 3400 | `acp_mcp.address` |

### 13.2 A2A Server

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `a2a_server.enabled` | bool | - | 是否启用 |
| `a2a_server.address` | string | `:9090` | 监听地址 |
| `a2a_server.agent_name` | string | - | Agent 名称 |
| `a2a_server.agent_description` | string | - | Agent 描述 |

### 13.3 AG-UI SSE

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `agui.enabled` | bool | - | 是否启用 |
| `agui.address` | string | `:8080` | 监听地址 |
| `agui.path` | string | - | SSE 路径 |

### 13.4 ACP Server

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `acp_server.enabled` | bool | - | 是否启用 |
| `acp_server.address` | string | `:9091` | 监听地址 |
| `acp_server.path` | string | - | API 路径 |
| `acp_server.enable_streaming` | bool | - | 是否启用流式 |
| `acp_server.auth_type` | string | - | 认证类型 |
| `acp_server.api_key` | string | - | API Key (支持 env 展开) |

### 13.5 ACP MCP Bridge

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `acp_mcp.enabled` | bool | - | 是否启用 |
| `acp_mcp.address` | string | `:3400` | 监听地址 |
| `acp_mcp.path` | string | - | MCP 路径 |

---

## 14. Agent Communication 配置 (K 组)

### 14.1 ARD 双向发现

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `ard.enabled` | bool | false | 是否启用 ARD |
| `ard.registry_url` | string | - | 注册中心 URL |
| `ard.catalog_path` | string | - | 本地 Catalog 路径 |
| `ard.publish_port` | int | - | 发布端口 |
| `ard.publish_enabled` | bool | false | 是否发布到注册中心 |

### 14.2 Summon 子 Agent 委派

> **字段命名说明**：`summon.delegates_dir` 与 [15.2 节](#152-skill-技能管理) 的 `skill.skills_dir` 语义不同，分属独立子系统：
>
> | 字段 | 子系统 | 消费者 | 加载内容 |
> |------|--------|--------|----------|
> | `summon.delegates_dir` | 子 Agent 委派 | `internal/summon/delegate.go` | 委派代理行为定义（.md 文件，每个实例化为一个 `Delegate`） |
> | `skill.skills_dir` | 技能仓库 + 自演化 | `internal/skill/manager.go`、`internal/evolution/engine.go` | 可演化技能包（带版本控制） |
>
> 两者默认均为 `.wukong/skills`，但可分别指向不同目录。

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `summon.enabled` | bool | false | 是否启用 Summon |
| `summon.delegates_dir` | string | - | 子 Agent 委派定义目录（.md 文件） |
| `summon.max_concurrent` | int | - | 最大并发子 Agent |
| `summon.a2a_remotes` | []A2ARemoteConfig | - | 远程 A2A Agent 列表 |

#### A2ARemoteConfig 字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | string | 远程 Agent 名称 |
| `description` | string | 描述 |
| `server_url` | string | 服务器 URL |
| `auth_type` | string | 认证类型: `apikey` / `jwt` / `oauth` |
| `api_key` | string | API Key (支持 env 展开) |
| `api_key_header` | string | API Key 头名称 |
| `jwt_secret` | string | JWT 密钥 (支持 env 展开) |
| `jwt_audience` | string | JWT 受众 |
| `jwt_issuer` | string | JWT 签发者 |
| `oauth_token_url` | string | OAuth Token URL |
| `oauth_client_id` | string | OAuth 客户端 ID |
| `oauth_client_secret` | string | OAuth 客户端密钥 (支持 env 展开) |

### 14.3 ANP Agent 互通协议

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `anp.enabled` | bool | false | 是否启用 ANP |
| `anp.did_domain` | string | - | DID 域名 |
| `anp.did_path` | string | - | DID 路径 |
| `anp.port` | int | 9092 | 监听端口 [0, 65535] |
| `anp.discovery_enabled` | bool | false | 是否启用发现 |
| `anp.meta_protocol_enabled` | bool | false | 是否启用能力协商 |
| `anp.e2ee_enabled` | bool | false | 是否启用端到端加密 |
| `anp.a2a_enabled` | bool | false | 是否启用 A2A 桥接 |
| `anp.agui_enabled` | bool | false | 是否启用 AG-UI |
| `anp.http_sign_enabled` | bool | false | 是否启用 HTTP 签名 |
| `anp.mcp_enabled` | bool | false | 是否启用 MCP |

> **注意**: 启用 `meta_protocol_enabled` 时必须设置有效的 `port` (> 0)

---

## 15. Knowledge & Skill 配置 (L 组)

### 15.1 Knowledge RAG

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `knowledge.enabled` | bool | false | 是否启用知识库 |
| `knowledge.embedder_provider` | string | - | 嵌入用 Provider |
| `knowledge.embedder_model` | string | - | 嵌入用模型 |
| `knowledge.sources` | []string | - | 知识源目录列表 |
| `knowledge.source_urls` | []string | - | 知识源 URL 列表 |
| `knowledge.vector_store` | string | - | 向量存储后端 |
| `knowledge.max_results` | int | - | 最大检索结果数 |
| `knowledge.enable_source_sync` | bool | false | 是否启用源同步 |
| `knowledge.reranker_enabled` | bool | false | 是否启用重排序 |
| `knowledge.search_tool_name` | string | - | 搜索工具名称 |

### 15.2 Skill 技能管理

> **字段命名说明**：`skill.skills_dir` 与 [14.2 节](#142-summon-子-agent-委派) 的 `summon.delegates_dir` 语义不同。本字段服务于技能仓库与自演化引擎（加载可演化技能包），前者服务于子 Agent 委派（加载委派代理定义）。详见 14.2 节的对照表。

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `skill.enabled` | bool | false | 是否启用技能系统 |
| `skill.skills_dir` | string | - | 可演化技能包目录 |
| `skill.auto_load` | bool | false | 是否自动加载 |
| `skill.max_skills` | int | - | 最大技能数 |

### 15.3 OKF 知识格式

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `okf.enabled` | bool | false | 是否启用 OKF |
| `okf.bundle_dir` | string | - | OKF Bundle 目录 |
| `okf.injector_enabled` | bool | false | 是否启用知识注入 |
| `okf.enrichment_enabled` | bool | false | 是否启用知识丰富 |
| `okf.enrichment_output_dir` | string | - | 丰富输出目录 |

---

## 16. Orchestration 配置 (M 组)

### 16.1 Evolution 技能进化

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `evolution.enabled` | bool | false | 是否启用进化引擎 |
| `evolution.auto_patch` | bool | false | 是否自动应用补丁 |
| `evolution.analysis_provider` | string | - | 分析用 Provider |
| `evolution.analysis_model` | string | - | 分析用模型 |
| `evolution.min_confidence` | float | - | 最小置信度 [0.0, 1.0] |
| `evolution.cooldown_period` | duration | - | 冷却周期 |
| `evolution.max_patches_per_day` | int | - | 每日最大补丁数 |
| `evolution.max_versions_kept` | int | - | 保留版本数 |
| `evolution.max_patch_size` | int | - | 最大补丁大小 |
| `evolution.analysis_timeout` | duration | - | 分析超时时间 |
| `evolution.export_json` | bool | false | 是否导出 JSON 日志 |

### 16.2 Workflow 编排模式

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `orchestration.workflow.mode` | string | `single` | 编排模式 (10 种) |

#### 10 种编排模式

| 模式 | 说明 |
|------|------|
| `single` | 单体 Agent |
| `chain` | 链式: planner → executor → reviewer |
| `parallel` | 并行: 多视角并发 |
| `cycle` | 循环: planner ↔ executor 迭代 |
| `graph` | 图: 条件 DAG |
| `team_coordinator` | 团队: Leader 委派 |
| `team_swarm` | 蜂群: 自动 transfer |
| `claude_code` | Claude Code 子进程 |
| `codex` | Codex 子进程 |
| `dify` | Dify 平台集成 |

---

## 17. Observability 配置 (N 组)

### 17.1 Telemetry 遥测

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `telemetry.enabled` | bool | false | 是否启用遥测 |
| `telemetry.sample_rate` | float | - | 采样率 [0.0, 1.0] |

### 17.2 Langfuse 可观测性

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `observability.langfuse_enabled` | bool | false | 是否启用 Langfuse |
| `observability.langfuse_public_key` | string | - | Public Key (支持 env 展开) |
| `observability.langfuse_secret_key` | string | - | Secret Key (支持 env 展开) |
| `observability.langfuse_host` | string | - | Langfuse 服务地址 |

---

## 18. Apps 配置 (O 组)

### 18.1 Apps 通用

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `apps.enabled` | bool | false | 是否启用应用系统 |
| `apps.app_dir` | string | - | 应用存储目录 |

### 18.2 Clone 克隆默认值

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `apps.clone.max_pages` | int | - | 最大页面数 |
| `apps.clone.max_depth` | int | - | 最大爬取深度 |
| `apps.clone.traversal` | string | - | 遍历策略: `bfs` / `dfs` |
| `apps.clone.subdomains` | bool | false | 是否包含子域名 |
| `apps.clone.scope_prefix` | string | - | 作用域前缀 |
| `apps.clone.workers` | int | - | 爬取 Worker 数 (>=1) |
| `apps.clone.asset_workers` | int | - | 资源下载 Worker 数 |
| `apps.clone.browser_pages` | int | - | 浏览器标签池大小 |
| `apps.clone.timeout` | int | - | 页面超时 (秒) |
| `apps.clone.render_timeout` | int | - | 渲染超时 (秒) |
| `apps.clone.settle` | int | - | 网络空闲等待 (毫秒) |
| `apps.clone.scroll` | bool | false | 是否自动滚动 |
| `apps.clone.respect_robots` | bool | true | 是否遵守 robots.txt |
| `apps.clone.crawl_delay` | int | - | 爬取延迟 (毫秒) |
| `apps.clone.no_sitemap` | bool | false | 是否忽略 sitemap |
| `apps.clone.dedup_content` | bool | false | 是否内容去重 |
| `apps.clone.mobile_readable` | bool | false | 是否移动端可读 |
| `apps.clone.enable_resume` | bool | false | 是否启用断点续抓 |
| `apps.clone.persist` | bool | false | 是否持久化状态 |
| `apps.clone.incremental` | bool | false | 是否增量爬取 |
| `apps.clone.cache_max_age` | int | - | 缓存最大年龄 (秒) |
| `apps.clone.headless` | bool | true | 是否无头模式 |
| `apps.clone.stealth` | bool | false | 是否启用隐身 |
| `apps.clone.chrome_profile` | string | - | Chrome 配置文件 |
| `apps.clone.chrome_path` | string | - | Chrome 路径 |
| `apps.clone.antibot_enabled` | bool | false | 是否启用反反爬 |
| `apps.clone.antibot_auto_escalate` | bool | false | 是否自动升级反爬等级 |
| `apps.clone.asset_same_domain` | bool | false | 是否只下载同域资源 |
| `apps.clone.max_asset_bytes` | int64 | - | 资源最大字节数 |
| `apps.clone.cookie_file` | string | - | Cookie 文件路径 |
| `apps.clone.user_agent` | string | - | User-Agent |
| `apps.clone.browser_backend` | string | `rod` | 浏览器后端: `rod` / `chromedp` |
| `apps.clone.proxy_enabled` | bool | false | 是否启用代理 |
| `apps.clone.proxy_pool` | []string | - | 代理池 URL 列表 |
| `apps.clone.proxy_rotate_every` | int | - | 每 N 次轮换代理 |

### 18.3 Pack 打包默认值

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `apps.pack.compress` | bool | - | 是否压缩 |
| `apps.pack.incremental` | bool | - | 是否增量打包 |
| `apps.pack.language` | string | - | 语言代码 |
| `apps.pack.creator` | string | - | 创建者 |
| `apps.pack.format` | string | - | 打包格式: `zim` / `zip` |

---

## 19. 完整配置示例

```yaml
# ===== 全局设置 =====
default_provider: openai-main
log_level: info
lightweight_provider: openai-main
lightweight_model: gpt-4o-mini
project_dir: ~/.config/wukong/

# ===== Providers =====
providers:
  - name: openai-main
    type: openai
    base_url: ${OPENAI_BASE_URL:-https://api.openai.com/v1}
    api_key: ${OPENAI_API_KEY}
    model: gpt-4o

  - name: local-ollama
    type: ollama
    base_url: http://localhost:11434
    model: qwen2.5:7b

# ===== Agent =====
agent:
  temperature: 0.7
  max_tokens: 4096
  max_llm_calls: 50
  max_tool_iterations: 30
  parallel_tools: true
  streaming: true
  tool_retry_enabled: true
  tool_retry_max_attempts: 3
  context_compaction: false
  recipe_enabled: true
  recipe_dir: .wukong/recipes/

# ===== Security =====
security:
  permission_mode: smart
  block_dangerous_commands: true
  guardrail_enabled: false
  ignore_file_enabled: true
  ignore_file: .wukongignore

# ===== Storage =====
session:
  backend: sqlite
  db_path: ~/.config/wukong/wukong.db

memory:
  backend: sqlite
  auto_extract: true
  enable_smart_cleanup: true
  cleanup_trigger_threshold: 0.8
  cleanup_target_threshold: 0.6
  recency_weight: 0.7
  length_weight: 0.3

todo:
  backend: sqlite
  enable_enforcer: true

recall:
  enabled: true
  search_mode: fts5
  max_results: 5

# ===== CortexDB =====
cortex:
  enabled: false
  embedding_model: text-embedding-3-small
  embedding_base_url: ${OPENAI_BASE_URL:-https://api.openai.com/v1}
  embedding_api_key: ${OPENAI_API_KEY}

memoryflow:
  enabled: false
  extractor_model: gpt-4o-mini

graphflow:
  enabled: false
  auto_extract: false

# ===== Browser =====
browser:
  enabled: true
  backend: rod
  headless: true
  stealth: true
  workers: 3
  viewport_width: 1920
  viewport_height: 1080
  search:
    backends: [duckduckgo]

# ===== Extensions =====
extensions:
  - name: developer
    type: builtin
    enabled: true
  - name: memory
    type: builtin
    enabled: true
  - name: browser
    type: builtin
    enabled: true

# ===== Service Endpoints =====
a2a_server:
  enabled: false
  address: ":9090"
  agent_name: "Wukong Agent"

agui:
  enabled: false
  address: ":8080"

acp_server:
  enabled: false
  address: ":9091"

# ===== Communication =====
ard:
  enabled: false
  publish_enabled: false

summon:
  enabled: false
  max_concurrent: 3

anp:
  enabled: false
  port: 9092
  discovery_enabled: true
  meta_protocol_enabled: true

# ===== Knowledge & Skill =====
knowledge:
  enabled: false
  max_results: 5

skill:
  enabled: false
  auto_load: true

okf:
  enabled: false
  injector_enabled: true

# ===== Orchestration =====
evolution:
  enabled: false
  auto_patch: false
  min_confidence: 0.8

orchestration:
  workflow:
    mode: single

# ===== Observability =====
telemetry:
  enabled: false
  sample_rate: 0.1

# ===== Apps =====
apps:
  enabled: true
  app_dir: .wukong/apps/
  clone:
    max_pages: 100
    max_depth: 3
    workers: 3
    traversal: bfs
    headless: true
    stealth: true
    antibot_enabled: true
    antibot_auto_escalate: true
    enable_resume: true
    dedup_content: true
  pack:
    compress: true
    format: zim
    language: zh
    creator: Wukong
```

---

## 附录

### 相关文档

| 文档 | 说明 |
|------|------|
| [ARCHITECTURE.md](./ARCHITECTURE.md) | 系统架构详解 |
| [CLI_TUI.md](./CLI_TUI.md) | CLI & TUI 架构 |
| [README.md](../README.md) | 项目主页 |
