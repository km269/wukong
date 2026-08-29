# Wukong 配置参考手册

> 配置文件: `config.yaml`（项目根目录，完整模板） | 加载器: Viper + Cobra
> 配置代码: `internal/config/`（16 文件 = 3 个 `*_test.go` 测试 + 13 个核心文件：`config.go`、`defaults.go`、`validate.go` 及 10 个 `types_*.go`）
> 配置结构: `WukongConfig` 根结构体（`config.go:97`）含 35+ 子配置段
> 验证规则: 致命错误（`Validate()`）+ 非致命警告（`Warnings()`）| 环境变量展开: 20+ 类敏感字段
>
> **编号说明**：本文的 A–AM 双字母编号仅用于文档导航，按子系统细分；
> `config.yaml` 模板内采用更粗的 **A–O 15 组**分组注释（A 全局 / B Providers /
> C Agent / D Security / E 存储 / F Cortex 栈 / G Revision / H 功能工具 /
> I Extensions / J 服务端点 / K Agent 互通 / L 知识与技能 / M 编排 /
> N 可观测 / O 项目目录）。按 YAML 分组定位时请以 config.yaml 注释为准，
> 按字段查询时以本文目录为准。

---

## 目录

1. [加载优先级](#1-加载优先级7-级)
2. [环境变量展开](#2-环境变量展开)
3. [配置验证](#3-配置验证)
4. [路径约定](#4-路径约定)
5. [A. 全局配置](#a-全局配置)
6. [B. Providers 配置](#b-providers-配置)
7. [C. Agent 配置](#c-agent-配置)
8. [D. Security 配置](#d-security-配置)
9. [E. Session 配置](#e-session-配置)
10. [F. Memory 配置](#f-memory-配置)
11. [G. Todo 配置](#g-todo-配置)
12. [H. Recall 配置](#h-recall-配置)
13. [I. Cortex 配置](#i-cortex-配置)
14. [J. MemoryFlow 配置](#j-memoryflow-配置)
15. [K. GraphFlow 配置](#k-graphflow-配置)
16. [L. ImportFlow 配置](#l-importflow-配置)
17. [M. Revision 配置](#m-revision-配置)
18. [N. Browser 配置](#n-browser-配置)
19. [O. Visualiser 配置](#o-visualiser-配置)
20. [P. Tutorial 配置](#p-tutorial-配置)
21. [Q. TopOfMind 配置](#q-topofmind-配置)
22. [R. CodeMode 配置](#r-codemode-配置)
23. [S. Apps 配置](#s-apps-配置)
24. [T. Extensions 配置](#t-extensions-配置)
25. [U. A2A Server 配置](#u-a2a-server-配置)
26. [V. AGUI 配置](#v-agui-配置)
27. [W. ACP Server 配置](#w-acp-server-配置)
28. [X. ACP-MCP 配置](#x-acp-mcp-配置)
29. [Y. MCP Server 配置](#y-mcp-server-配置)
30. [Z. Gateway 配置](#z-gateway-配置)
31. [AA. Summon 配置](#aa-summon-配置)
32. [AB. ANP 配置](#ab-anp-配置)
33. [AC. ARD 配置](#ac-ard-配置)
34. [AD. Dify 配置](#ad-dify-配置)
35. [AE. Knowledge 配置](#ae-knowledge-配置)
36. [AF. OKF 配置](#af-okf-配置)
37. [AG. Skill 配置](#ag-skill-配置)
38. [AH. Evolution 配置](#ah-evolution-配置)
39. [AI. Workflow 配置](#ai-workflow-配置)
40. [AJ. Telemetry 配置](#aj-telemetry-配置)
41. [AK. Observability 配置](#ak-observability-配置)
42. [AL. Eval 配置](#al-eval-配置)
43. [AM. Artifact 配置](#am-artifact-配置)
44. [完整配置示例](#完整配置示例)

---

## 1. 加载优先级（7 级）

配置按以下优先级从高到低解析，高优先级覆盖低优先级。源码定义于 `config.go` 包文档注释及 `NewLoader()`（`config.go:287`）。

```
优先级 1 — CLI 参数（最高）
   ├── --provider, --model, --temperature, --max-tokens
   ├── --config（指定配置文件路径，可为目录或文件）
   └── --debug, --quiet（日志级别覆盖）

优先级 2 — 环境变量
   └── WUKONG_ 前缀，点号转下划线，e.g. WUKONG_DEFAULT_PROVIDER、WUKONG_AGENT_TEMPERATURE
       （SetEnvPrefix("WUKONG") + SetEnvKeyReplacer("." → "_") + AutomaticEnv()）

优先级 3 — --config 指定的配置文件

优先级 4 — 当前目录配置文件
   └── ./config.yaml

优先级 5 — 用户目录配置文件
   └── ~/.config/wukong/config.yaml

优先级 6 — 系统级配置文件（非 Windows）
   └── /etc/wukong/config.yaml

优先级 7 — 内置默认值（最低）
   └── internal/config/defaults.go（setDefaults() 注册到 Viper）
```

**配置文件搜索路径**（未指定 `--config` 时，`NewLoader` 依次 `AddConfigPath`）：

| 平台 | 搜索路径 |
|------|---------|
| 全部 | `.`（当前目录） |
| 全部 | `~/.config/wukong/` |
| Linux/macOS | `/etc/wukong/`（Windows 上此路径被跳过） |

> `--config` 参数既可指向文件也可指向目录：若为目录则在其下搜索 `config.yaml`。

---

## 2. 环境变量展开

支持 `${ENV_VAR}` 和 `${VAR:-default}` 两种语法，运行时由加载器自动展开。展开逻辑基于 `os.Expand`，支持 bash 风格的 `:-` 默认值回退。

### 2.1 机制：`envexpand` 标签驱动（v0.3.3 起）

可展开字段由结构体标签声明，加载器通过反射递归遍历整棵 `WukongConfig` 配置树，对带 `envexpand:"true"` 标签的 string 字段执行展开：

```go
// internal/config/types_provider.go
type ProviderConfig struct {
    APIKey  string `mapstructure:"api_key"  envexpand:"true"`
    BaseURL string `mapstructure:"base_url" envexpand:"true"`
    Model   string `mapstructure:"model"    envexpand:"true"`
}
```

新增可展开字段只需在对应 `types_*.go` 中打标签，无需改动展开逻辑；数组元素（如 `providers[]`）按索引（有 `name` 字段时按名称）生成偏差路径，用于未解析变量警告。

### 2.2 语法

```yaml
# 直接引用环境变量
api_key: ${OPENAI_API_KEY}

# 带默认值的引用（VAR 为空或未设时使用默认值）
base_url: ${OPENAI_BASE_URL:-https://api.openai.com/v1}
```

### 2.3 支持展开的字段（20+ 类）

未解析的 `${VAR}`（无 `:-default` 且 `VAR` 未设）会被记录到 `unresolvedEnvVars`，并通过 `Warnings()` 输出，便于发现拼写错误（如 `${OEPNAI_API_KEY}`）。

| 类别 | 字段 | 标签位置 |
|------|------|---------|
| **Providers** | `api_key`, `base_url`, `model` | `types_provider.go` |
| **A2A Remotes** | `api_key`, `jwt_secret`, `oauth_client_secret` | `types_orchestration.go` |
| **Dify** | `base_url`, `api_secret` | `types_orchestration.go` |
| **Gateway Feishu** | `app_secret`, `encrypt_key`, `verification_token` | `internal/gateway/config.go` |
| **Observability (Langfuse)** | `langfuse_public_key`, `langfuse_secret_key` | `types_observability.go` |
| **Artifact (COS)** | `cos_secret_id`, `cos_secret_key` | `types_observability.go` |
| **ACP / MCP Server** | `security.auth.api_key`, `jwt_secret` | `internal/server/security.go` |
| **Cortex Embedding** | `embedding_api_key`, `embedding_base_url`, `embedding_model` | `types_cortex.go` |
| **Cortex Reranker** | `reranker_api_key`, `reranker_base_url`, `reranker_model` | `types_cortex.go` |
| **Cortex Vertical Routing** | `github_api_key` | `types_cortex.go` |
| **MemoryFlow / GraphFlow / ImportFlow** | `planner_model`, `extractor_model` | `types_cortex.go` |
| **Memory (tRPC)** | `extractor_model` 等 3 字段 | `types_storage.go` |
| **Session** | `redis_url` | `types_storage.go` |
| **Browser Search (SearXNG)** | `url`, `api_key` | `types_browser.go` |
| **Browser Search (Tavily)** | `api_key` | `types_browser.go` |
| **Browser Search (Google)** | `api_key`, `cse_id` | `types_browser.go` |
| **Browser Search (Bing)** | `api_key` | `types_browser.go` |

---

## 3. 配置验证

配置加载后自动执行验证（`validate.go`），分为**致命错误**（`Validate()` 返回 error）和**非致命警告**（`Warnings()` 返回 `[]string`）。

> **验证时机说明**：完整规则在两条路径中执行——**启动路径**（`session`/`server`/`run` 等 → `bootstrapSession()` → `loader.LoadAndValidate()`，警告随后打印到日志）和 **`wukong config validate` 命令**（同样调用 `loader.LoadAndValidate()` 并在终端列出全部非致命警告，致命错误时退出码 1）。两条路径行为一致；`internal/cli/config.go` 中另有一个轻量咨询性校验函数 `runFullValidation`（枚举/区间规则直接委托 `Validate()`，另加 provider model/api_key、ACP agent_url、planner、lightweight_provider 等咨询项），仅供 `bench`/`health` 命令使用。

### 3.1 致命错误（Validate，阻止启动）

| 检查项 | 有效值/范围 | 源码位置 |
|--------|------------|---------|
| `default_provider` 存在性 | 设置后必须在 `providers[]` 中存在 | `validate.go:55-63` |
| `agent.temperature` | [0.0, 2.0] | `validate.go:65-71` |
| `security.permission_mode` | `auto`/`smart`/`manual`/`chat_only`（空串合法，运行时回退 smart） | `validate.go:73-86` |
| `providers[].type` 有效性 | `openai`/`anthropic`/`google`/`deepseek`/`ollama`/`lmstudio`/`vllm`/`acp` | `validate.go:88-103` |
| `browser.backend` | `chromedp` / `rod` | `validate.go:105-115` |
| `workflow.mode` | 10 种有效模式（见 AI 节） | `validate.go:117-131` |
| `agent.max_tokens` | >= 0 | `validate.go:133-139` |
| `evolution.min_confidence` | [0.0, 1.0]（启用时） | `validate.go:141-150` |
| `telemetry.sample_rate` | [0.0, 1.0]（启用时） | `validate.go:152-161` |
| `anp.port` | [0, 65535]（启用时） | `validate.go:163-170` |
| `anp.meta_protocol_enabled` + `port<=0` | 不允许 | `validate.go:171-176` |
| `session.backend` | `sqlite`/`memory`/`redis` | `validate.go:179-188` |
| `memory.backend` | `sqlite`/`redis` | `validate.go:190-199` |
| `memory.cleanup_*_threshold` | [0.0, 1.0]，且 target < trigger（启用 smart_cleanup 时） | `validate.go:201-225` |
| `recall.search_mode` | `fts5`/`hybrid` | `validate.go:227-236` |
| `artifact.backend` | `inmemory`/`cos` | `validate.go:238-247` |
| `todo.backend` | **仅 `sqlite`**（其他值 fatal） | `validate.go:249-258` |
| `agent.max_llm_calls` / `agent.max_tool_iterations` | >= 0 | `validate.go:260-274` |
| `memory` 评分权重 | `recency`/`reference`/`importance`/`length` 各 [0.0, 1.0] | `validate.go:276-287` |
| `memory.max_memories` | >= 0 | `validate.go:289-295` |
| `revision.trim_ratio` | [0.0, 1.0] | `validate.go:297-303` |
| `apps.clone.workers` / `apps.clone.asset_workers` | >= 1（`apps.enabled` 时） | `validate.go:305-319` |
| `mcp_server.address` | `mcp_server.enabled` 时非空（内置默认 `:3401`，校验保留为安全网） | `validate.go:321-328` |
| `summon.max_concurrent` | >= 0（启用时） | `validate.go:330-337` |
| `summon.a2a_remotes[].name` / `.server_url` | 必填（启用时） | `validate.go:338-348` |
| `summon.a2a_remotes[].auth_type` | `""`/`api_key`/`jwt`/`oauth2` | `validate.go:349-358` |
| `browser.search.searxng.url` | searxng 启用时必填 | `validate.go:364-371` |
| `browser.search.tavily.api_key` | tavily 启用时必填 | `validate.go:372-378` |
| `browser.search.google.api_key` + `cse_id` | google 启用时两者均必填 | `validate.go:379-387` |
| `browser.search.bing.api_key` | bing 启用时必填 | `validate.go:388-394` |
| `cortex.search_strategy` 权重 | `dense_weight`/`text_weight`/`mmr_lambda` 各 [0.0, 1.0]（cortex 启用且配置了 search_strategy 时） | `validate.go:398-417` |
| `cortex.search_strategy.reranker_top_n` | <= `fts5_pool_size`（两者均 > 0 时） | `validate.go:418-426` |
| `security.sandbox.limits.max_memory_bytes` | 非零时 >= 1 MiB（1048576） | `validate.go:429-455` |
| `security.sandbox.limits.max_file_bytes` | 非零时 >= 512（一个标准块） | `validate.go:456-465` |
| 服务端口冲突 | `a2a_server`/`agui`/`acp_server`/`acp_mcp`/`mcp_server`/`anp` 中任意两个已启用服务不得绑定同一端口 | `validate.go:467-505` |

### 3.2 非致命警告（Warnings，不阻止启动）

| 警告项 | 说明 |
|--------|------|
| 无 providers 配置 | 无法进行 LLM 对话（`validate.go:543-546`） |
| `memory.auto_extract` 启用但无 `default_provider` | 记忆提取无法执行 |
| `cortex.enabled` 但无 `embedding_model` | 向量搜索不可用 |
| `recall.search_mode` 为 `hybrid` 且无 `embedding_model`、无 `default_provider` | hybrid 召回缺向量能力 |
| `agent.context_compaction` 启用且 `context_compaction_oversized_max_tokens = 0` | 仅执行 Pass 1（占位符清理），Pass 2（截断）被禁用 |
| `okf.enabled` 但 `bundle_dir` 为空 | 回退默认目录 `.wukong/okf` |
| `okf.injector_enabled` 但 `memoryflow.enabled = false` | 知识索引注入无效（依赖 MemoryFlow） |
| `okf.enrichment_enabled` 但无 `default_provider` | LLM 丰富退化为确定性回退 |
| `anp.enabled` 但 `did_domain` 为空 | DID 身份回退 `os.Hostname()` |
| `anp.e2ee_enabled` 但 `meta_protocol_enabled = false` | E2EE 密钥交换依赖元协议能力协商，不可用 |
| `gateway.enabled` 但无 channel 激活 | 消息网关无可用通道 |
| `gateway.feishu.enabled` 但 `app_id` 为空 | 飞书通道可能无法认证 |
| `mcp_server` 启用 + 非回环地址 + 空 `security.auth.type` | **Critical**：`tools/call` 无鉴权，可经 `developer_command_execute` 执行任意命令。回环地址仅认 `127.0.0.1`/`localhost`/`::1`；裸 `:port` 绑定 0.0.0.0 视为非回环 |
| `acp_server` 启用 + 非回环地址 + 空 `security.auth.type` | **Critical**：无鉴权可绕过 agent guard 链 |
| `revision.max_context_tokens` 超过默认 Provider 有效上下文窗口 | prompt 超过模型 `n_ctx` 时 LLM 请求返回 400 |
| URL 格式检查（缺 scheme/host 或解析失败） | 覆盖 `session.redis_url`、`cortex.embedding_base_url`、`ard.registry_url`、`dify.base_url`、`providers[].base_url`、`summon.a2a_remotes[].server_url`（仅检查非空值） |
| `providers[].base_url` 为空且 `type != acp` | LLM 请求将失败 |
| 本地推理 Provider（`vllm`/`ollama`/`lmstudio`）未设 `context_window` | 使用保守默认 8000，可能过早截断或超窗口触发 400 |
| `cortex.enabled` 但 `embedding_base_url` 为空 | 语义搜索不可用 |
| 未解析的环境变量 `${VAR}` | 拼写错误或环境缺失（`unresolvedEnvVars`） |
| `apps.clone` 与 `browser` 反爬字段不一致 | `stealth`/`headless`/`browser_backend` 任一分歧即告警（项目硬约束：两者反爬措施必须一致） |
| `security.sandbox.limits.max_memory_bytes` 非零且 < 16 MiB | shell 启动内存占用因平台而异，可能误杀所有命令 |
| `security.sandbox.limits.max_processes = 1` | shell 自身占用唯一槽位，无法 fork 管道辅助进程（`ls \| head` 等），建议 0 或 >= 2 |
| sandbox 平台差异 | Windows 设 `max_file_bytes` 不生效（无 RLIMIT_FSIZE 等价物）；macOS 设任何非零 limit 均为 no-op（sandbox-exec 只做文件系统写保护） |

---

## 4. 路径约定

| 路径模式 | 用途 | 示例 |
|---------|------|------|
| `.wukong/` | 运行时数据（应用、缓存、技能、可视化、OKF、评测） | `.wukong/apps/`、`.wukong/cache/`、`.wukong/skills/` |
| `~/.config/` | 用户级配置与项目数据 | `~/.config/wukong/config.yaml`、`~/.config/wukong/prompts/` |
| `wukong.db` | 单一 SQLite WAL 文件，被所有存储子系统共享 | Session、Memory、Todo、Recall、Cortex 默认均指向此文件 |

> **共享 SQLite 约定**：`DatabasePool`（`internal/util`）管理单一 `*sql.DB` 连接，避免多连接对同一文件产生事务冲突。所有 `db_path` 默认为 `wukong.db`，由 `ResolvePath()`（`config.go:77`）解析为绝对路径。路径解析在 `NewStore()`/`NewSessionService()` 等构造函数中统一完成。

---

## A. 全局配置

**源码**: `config.go:97-115`（`WukongConfig` 顶层字段） | 默认值: `defaults.go:31-34`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `log_level` | string | `info` | 日志级别: `debug`/`info`/`warn`/`error`。被 `--debug`/`--quiet` CLI 参数覆盖 |
| `default_provider` | string | - | 默认 LLM Provider 名称，必须匹配 `providers[].name`。`Validate()` 校验存在性 |
| `lightweight_provider` | string | - | 后台任务（记忆提取、摘要、图谱构建）所用 Provider。为空时回退到 `default_provider`（`EffectiveLightweightProvider()`） |
| `lightweight_model` | string | - | 后台任务轻量模型名。为空时各子系统使用各自默认 |
| `project_dir` | string | `~/.config/wukong/` | 项目数据目录 |

### 轻量模型自动继承

当设置 `lightweight_model` 时，以下字段如未显式配置将自动继承该值：
- `memory.extractor_model`
- `memoryflow.planner_model` / `memoryflow.extractor_model`
- `graphflow.extractor_model`
- `revision.revision_model`

> `EffectiveLightweightModel()`（`config.go:622`）在 `lightweight_model` 为空时回退到默认 Provider 的 model。

---

## B. Providers 配置

**源码**: `types_provider.go:26-41`（`ProviderConfig`） | 类型常量: `types_provider.go:12-21`

### Provider 类型（8 种）

| 类型 | 常量 | 说明 | 默认 Base URL |
|------|------|------|--------------|
| `openai` | `ProviderOpenAI` | OpenAI 兼容 API（含硅基流动/OpenRouter/Groq/Moonshot/智谱等） | `https://api.openai.com/v1` |
| `anthropic` | `ProviderAnthropic` | Anthropic Claude | `https://api.anthropic.com/v1` |
| `google` | `ProviderGoogle` | Google Gemini（OpenAI 兼容端点） | `https://generativelanguage.googleapis.com/v1beta/openai` |
| `deepseek` | `ProviderDeepSeek` | DeepSeek | `https://api.deepseek.com/v1` |
| `ollama` | `ProviderOllama` | 本地 Ollama | `http://localhost:11434/v1` |
| `lmstudio` | `ProviderLMStudio` | LM Studio | `http://localhost:1234/v1` |
| `vllm` | `ProviderVLLM` | vLLM 本地推理（无需 api_key） | `http://localhost:8000/v1` |
| `acp` | `ProviderACP` | Agent Client Protocol | - |

> **关键**: `openai`/`anthropic`/`google`/`deepseek`/`ollama`/`lmstudio`/`vllm` 七类在 Factory 中全部走 `createOpenAI`（OpenAI 兼容客户端）；仅 `acp` 走 `createACP`。见 `factory.go:63-73`。

### ProviderConfig 字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | string | Provider 名称（唯一标识，被 `default_provider` 引用） |
| `type` | string | Provider 类型，见上表 |
| `base_url` | string | API 基础 URL（支持 `${ENV}` 展开）。留空时按 type 填充默认值（`fillDefaultBaseURL`） |
| `api_key` | string | API 密钥（支持 `${ENV}` 展开）。`vllm` 等本地服务可留空 |
| `model` | string | 默认模型名称（支持 `${ENV}` 展开） |
| `agent_url` | string | ACP Agent URL（仅 `acp` 类型使用） |
| `mcp_port` | string | ACP MCP 端口（仅 `acp` 类型） |
| `context_window` | int | 模型实际上下文窗口大小（如 32768、128000）。未设置时 `EffectiveContextWindow()` 按 type 取保守默认（`config.go:563-589`） |

### ContextWindow 保守默认值

| Type | 默认窗口 | 说明 |
|------|---------|------|
| `openai` | 16000 | gpt-4o 为 128K，取保守 16K |
| `anthropic` | 100000 | claude-sonnet-4 为 200K，取保守 100K |
| `google` | 32000 | gemini-2.0-flash 为 1M，取保守 32K |
| `deepseek` | 64000 | deepseek-chat 为 64K |
| `ollama`/`lmstudio`/`vllm` | 8000 | 本地模型差异大，取地板值 |
| `acp` | 32000 | ACP Agent 差异大 |

> **作用**: `ContextRevisionEngine` 取 `min(revision.max_context_tokens, EffectiveContextWindow())` 作为真实截断阈值，防止超过模型 `n_ctx` 触发 HTTP 400。设置 `context_window` 后还会启用 `openai.WithContextWindow` + `openai.WithEnableTokenTailoring(true)`。

---

## C. Agent 配置

**源码**: `types_agent.go:11-50`（`AgentConfig`） | 默认值: `defaults.go:37-86`

### 核心行为

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `max_llm_calls` | int | 50 | 单次 Run 最大 LLM 调用次数。0 = 无限 |
| `max_tool_iterations` | int | 30 | 单次 Run 最大工具迭代次数 |
| `max_run_duration` | duration | `900s` | 单次 Run 墙钟时间上限 |
| `tool_call_timeout` | duration | `120s` | 单次工具调用截止时间，防止慢工具耗尽 Run 预算。0 = 不限 |
| `parallel_tools` | bool | true | 是否并行执行独立工具调用 |
| `streaming` | bool | true | 是否启用 TUI 实时 token 流式输出 |

### 生成参数

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `temperature` | float64 | 0.7 | 采样温度 [0.0, 2.0] |
| `max_tokens` | int | 4096 | 最大生成 token 数（>= 0） |
| `reasoning_effort` | string | - | 推理努力程度: `low`/`medium`/`high`（builtin planner） |
| `thinking_enabled` | *bool | - | 是否启用思考模式（指针类型，区分未设与 false） |
| `thinking_tokens` | *int | - | 思考 token 预算 |

### 工具重试

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `tool_retry_enabled` | bool | true | 是否启用工具重试 |
| `tool_retry_max_attempts` | int | 3 | 最大重试次数 |
| `tool_retry_initial_wait` | duration | `1s` | 初始等待时间 |
| `tool_retry_backoff_factor` | float64 | 2.0 | 指数退避因子 |
| `enable_post_tool_prompt` | bool | true | 工具执行后是否追加推理提示 |

### 规划器与工具搜索

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `planner` | string | `""` | 规划器: `""`（禁用）/ `builtin` / `react` |
| `tool_search_enabled` | bool | false | 是否启用工具自动过滤（TopK 筛选） |
| `tool_search_max_tools` | int | 20 | TopK 工具数量上限 |

### 上下文压缩

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `context_compaction` | bool | false | 是否启用上下文压缩 |
| `context_compaction_tool_result_max_tokens` | int | 1024 | 工具结果截断后的最大 token |
| `context_compaction_oversized_max_tokens` | int | 0 | 超大消息截断阈值（0 = 不截断） |
| `context_compaction_keep_recent` | int | 1 | 压缩时保留最近 N 轮完整对话 |
| `context_compaction_force_clean_tools` | []string | - | 强制清理结果的工具名列表 |
| `context_compaction_keep_tools` | []string | - | 保留结果的工具名列表 |

### 会话召回与其他

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `session_recall_enabled` | bool | false | 是否启用跨会话历史召回 |
| `session_recall_limit` | int | 5 | 召回历史会话数 |
| `json_repair_enabled` | bool | false | 是否启用 JSON 修复 |
| `agent_tools_enabled` | bool | true | 是否启用 Agent 工具（子 Agent 委派工具） |
| `agent_tools_stream` | bool | false | Agent 工具是否流式输出 |
| `system_prompt_dir` | string | `~/.config/wukong/prompts/` | 系统提示词目录 |
| `recipe_dir` | string | `.wukong/recipes/` | Recipe YAML 定义目录 |
| `recipe_enabled` | bool | true | 是否启用 Recipe 系统 |
| `inline_recipes` | []map[string]any | - | config.yaml 内联 Recipe 定义 |

---

## D. Security 配置

**源码**: `types_agent.go:66-86`（`SecurityConfig`，含 `sandbox` 字段） | 默认值: `defaults.go:89-112`

### 权限模式（PermissionMode）

定义于 `types_agent.go:57-64`：

| 模式 | 常量 | 说明 |
|------|------|------|
| `auto` | `PermissionAuto` | 自动批准所有工具调用 |
| `smart` | `PermissionSmart` | 高风险操作需用户批准（默认） |
| `manual` | `PermissionManual` | 所有工具调用需用户批准 |
| `chat_only` | `PermissionChatOnly` | 禁止所有工具调用 |

> `NeedsApproval()` 分支逻辑（`guard.go`）：`Auto`→始终 false；`Manual`→始终 true；`ChatOnly`→始终 true（且 `CheckToolPermission` 拒绝所有工具）；`Smart`→仅高风险返回 true。

### 字段

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `permission_mode` | PermissionMode | `smart` | 权限模式，见上表 |
| `require_approval` | bool | false | 遗留字段，推荐使用 `permission_mode` |
| `malware_scan_enabled` | bool | true | 是否启用外部扩展恶意软件扫描 |
| `block_dangerous_commands` | bool | true | 是否拦截危险命令（token 级分析） |
| `blocked_commands` | []string | 见下 | 危险命令列表 |
| `default_timeout` | duration | `30s` | 工具执行默认超时 |
| `max_timeout` | duration | `300s` | 工具执行最大超时 |
| `allowlist` | []string | - | 工具白名单 |
| `denylist` | []string | - | 工具黑名单 |
| `guardrail_enabled` | bool | false | 是否启用 Prompt 注入检测 |
| `ignore_file_enabled` | bool | true | 是否启用 `.wukongignore` 文件屏蔽 |
| `ignore_file` | string | `.wukongignore` | 忽略文件名 |
| `sandbox` | SandboxConfig | 全零 | 进程级沙箱（资源上限 + 生命周期绑定），见下节 |

### Sandbox 进程级沙箱（SandboxConfig / SandboxLimitsConfig）

**源码**: `types_agent.go:92-95`（`SandboxConfig`）、`types_agent.go:110-115`（`SandboxLimitsConfig`） | 默认值: `defaults.go:103-111`（全 0 = 不限制，保持遗留行为）

对 developer 工具集执行的 shell 命令施加进程级资源上限与生命周期绑定：

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `sandbox.kill_on_parent_exit` | bool | false | 子进程生命周期与 wukong 主进程绑定（父进程退出即终止子进程） |
| `sandbox.limits.max_cpu_seconds` | uint64 | 0 | 每命令 CPU 时间上限（0 = 不限制）。Windows: `JOB_OBJECT_LIMIT_PROCESS_TIME`；Linux: `RLIMIT_CPU` |
| `sandbox.limits.max_memory_bytes` | uint64 | 0 | 每命令内存上限（0 = 不限制）。Windows: `JOB_OBJECT_LIMIT_PROCESS_MEMORY`；Linux: `RLIMIT_AS` |
| `sandbox.limits.max_file_bytes` | uint64 | 0 | 单文件写入上限（0 = 不限制）。仅 Linux（`RLIMIT_FSIZE`）；Windows 无等价物，设值仅产生警告 |
| `sandbox.limits.max_processes` | uint64 | 0 | 每命令进程数上限（0 = 不限制）。Windows: `JOB_OBJECT_LIMIT_ACTIVE_PROCESS`；Linux: `RLIMIT_NPROC` |

**校验与平台语义**：
- 非零 `max_memory_bytes` < 1 MiB（1048576）或非零 `max_file_bytes` < 512 为**致命错误**（shell 无法启动，`validate.go:429-465`）
- 非零 `max_memory_bytes` < 16 MiB 产生警告（各平台 shell 启动内存占用不同，可能误杀所有命令）
- `max_processes = 1` 产生警告（shell 自身占用唯一进程槽位，无法 fork `ls | head` 等管道辅助进程）
- macOS：`sandbox-exec` 只做文件系统写保护，不执行任何资源上限——设任何非零 limit 均为 no-op（产生警告）
- 启用沙箱需要平台支持：Windows Job Object（无需管理员）；Linux setrlimit + Landlock（内核 5.13+）；macOS sandbox-exec（limits 不生效）

### 默认拦截的危险命令

```yaml
blocked_commands:
  - "rm -rf /"
  - "dd if=/dev/zero"
  - "mkfs."
  - "> /dev/sda"
  - "fork bomb"
```

> 危险命令检测走 `command_tokens.go` 的 `dangerousRule` 表（token 级分析），覆盖 `rm`/`sudo`/`chmod`/`chown`/`dd`/`mkfs`/`format`/`git push --force`/`docker`/`curl|wget 管道到 shell` 等。

---

## E. Session 配置

**源码**: `types_storage.go:11-26`（`SessionConfig`） | 默认值: `defaults.go:117-128`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `backend` | string | `sqlite` | 存储后端: `sqlite`/`memory`/`redis` |
| `db_path` | string | `wukong.db` | SQLite 数据库文件路径 |
| `event_limit` | int | 500 | 每会话事件数限制 |
| `ttl` | duration | `0h` | 会话过期时间（0 = 永不过期） |
| `enable_summary` | bool | true | 是否启用会话摘要 |
| `summary_trigger` | int | 50 | 摘要触发事件数阈值 |
| `redis_url` | string | - | Redis 连接 URL（支持 `${ENV}` 展开），仅 `redis` 后端 |
| `enable_model_event_log` | bool | true | 是否启用模型可见事件日志（写入 `wukong_model_events` 表，`internal/session/eventlog.go`）。记录上下文富化（唤醒/召回/持久记忆注入）**之后**模型实际看到的消息，强制 "model-visible means logged" 不变式；区别于框架 session 服务自身的事件日志 |

> 三种后端（`session/store.go:33-47`）：`sqlite`→`sessionsqlite.NewService`；`memory`→`sessioninmemory`；`redis`→本仓库 `newRedisService`。`SessionService` 嵌入 tRPC 框架 `session.Service` 接口，无自定义接口。

---

## F. Memory 配置

**源码**: `types_storage.go:22-40`（`MemoryConfig`） | 默认值: `defaults.go:116-135`

### 基本字段

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `backend` | string | `sqlite` | 存储后端: `sqlite`/`redis` |
| `db_path` | string | `wukong.db` | 数据库文件路径 |
| `max_memories` | int | 100 | 最大记忆条数 |
| `auto_extract` | bool | true | 是否自动从对话提取记忆 |
| `extract_timeout` | duration | `300s` | 提取超时时间 |
| `extractor_provider` | string | - | 提取用 Provider（空 = 默认） |
| `extractor_model` | string | - | 提取用模型（空 = `lightweight_model`） |
| `extractor_prompt` | string | - | 自定义提取提示词 |

### 评分权重（SmartCleanup 评分模型）

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `recency_weight` | float64 | 0.4 | 新鲜度权重 |
| `reference_weight` | float64 | 0.3 | 引用频率权重 |
| `importance_weight` | float64 | 0.2 | 重要性权重 |
| `length_weight` | float64 | 0.1 | 长度权重 |

> 评分公式: `40% recency + 30% reference + 20% importance + 10% length`。权重值范围 [0.0, 1.0]。

### 智能清理与 TTL

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `dynamic_ttl` | bool | true | 是否启用动态 TTL |
| `enable_smart_cleanup` | bool | true | 是否启用智能清理 |
| `cleanup_trigger_threshold` | float64 | 0.8 | 触发清理的容量阈值 [0.0, 1.0] |
| `cleanup_target_threshold` | float64 | 0.6 | 清理目标容量阈值 [0.0, 1.0] |
| `memory_ttl` | duration | `720h` | 记忆过期时间（30 天） |

> **约束**: `cleanup_target_threshold` 必须小于 `cleanup_trigger_threshold`（验证）。当容量超过 80% 触发淘汰，直到降到 60%。

---

## G. Todo 配置

**源码**: `types_storage.go:50-55`（`TodoConfig`） | 默认值: `defaults.go:153-156`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `backend` | string | `sqlite` | 存储后端：**仅支持 `sqlite`**。配置 `memory` 等其他值会触发致命错误（`validate.go:249-258`：`todo.backend %q is invalid; use sqlite`） |
| `db_path` | string | `wukong.db` | 数据库文件路径 |
| `enable_native_todo` | bool | true | 是否启用原生 Todo 工具 |
| `enable_enforcer` | bool | true | 是否启用 Todo 强制执行器 |

---

## H. Recall 配置

**源码**: `types_storage.go:51-62`（`RecallConfig`） | 默认值: `defaults.go:144-149`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | true | 是否启用跨会话召回 |
| `backend` | string | `sqlite` | 存储后端 |
| `db_path` | string | `wukong.db` | 数据库文件路径 |
| `max_results` | int | 10 | 最大召回结果数 |
| `max_messages_per_session` | int | 200 | 每会话最大存储消息数 |
| `search_mode` | string | `fts5` | 搜索模式: `fts5`/`hybrid` |
| `embedding_model` | string | - | 向量嵌入模型（hybrid 模式） |
| `search_strategy` | *SearchStrategyConfig | - | 搜索策略覆盖（nil 时使用 `search_mode`） |

> 全文检索基于 SQLite **FTS5（BM25）**，FTS5 不可用时降级为 `LIKE`。

---

## I. Cortex 配置

**源码**: `types_cortex.go:11-36`（`CortexConfig`） | 默认值: `defaults.go:170-176`

### 主配置

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | false | 是否启用 CortexDB |
| `db_path` | string | `wukong.db` | 数据库路径 |
| `max_results` | int | 10 | 最大搜索结果数 |
| `max_messages_per_session` | int | 200 | 每会话最大消息数 |
| `embedding_base_url` | string | - | Embedding API URL（支持 `${ENV}`） |
| `embedding_api_key` | string | - | Embedding API Key（支持 `${ENV}`） |
| `embedding_model` | string | `text-embedding-3-small` | Embedding 模型（支持 `${ENV}`） |
| `reranker_base_url` | string | - | Cross-Encoder Reranker URL（支持 `${ENV}`，空时复用 embedding 值） |
| `reranker_api_key` | string | - | Reranker API Key（支持 `${ENV}`，空时复用 embedding 值） |
| `reranker_model` | string | - | Reranker 模型（支持 `${ENV}`，空 = 禁用 reranker） |
| `search_strategy` | *SearchStrategyConfig | - | 检索参数空间（nil 时默认 hybrid 70/30） |
| `vertical_routing` | *VerticalRoutingConfig | - | 垂直域搜索路由（可选） |
| `chunking` | *ChunkingConfig | - | 语义分块配置（nil 时默认启用） |

### SearchStrategyConfig（`types_cortex.go:63-76`）

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `recall_mode` | string | `hybrid` | 召回模式: `lexical`/`vector`/`hybrid` |
| `dense_weight` | float64 | 0.7 | 向量检索权重 [0, 1] |
| `text_weight` | float64 | 0.3 | 词法检索权重 [0, 1] |
| `keyword_match_percent` | float64 | 0.0 | 关键词命中率过滤阈值 [0, 1]：0 = 不过滤；0.3 要求文档命中 30% 的查询关键词才保留（语义见 `internal/search/genome.go:42-44`） |
| `fts5_pool_size` | int | 50 | FTS5 候选池大小（rerank 前） |
| `max_retrieved_num` | int | 10 | 最终 Top-K |
| `fusion_method` | string | `rrf` | 融合方法: `weighted`/`rrf`（推荐 RRF） |
| `rrf_k` | float64 | 60 | RRF 平滑常数 |
| `reranker_enabled` | bool | false | 是否启用 Cross-Encoder reranker |
| `reranker_top_n` | int | 20 | 送入 reranker 的候选数 |
| `mmr_enabled` | bool | false | 是否在 Top-K 中启用 MMR 多样性 |
| `mmr_lambda` | float64 | 0.7 | MMR 权衡 [0, 1]：1=相关性，0=多样性 |

> **Hybrid 五阶段流水线**: FTS5 + HNSW 双路检索 → RRF/加权融合 → Cross-Encoder reranker → MMR 去重 → TopK。

### VerticalRoutingConfig（`types_cortex.go:50-59`）

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | false | 是否启用查询意图路由 |
| `top_n` | int | 5 | 每个后端最大结果数 |
| `timeout` | duration | `10s` | 每后端截止时间 |
| `merge_mode` | string | `prepend` | 合并模式: `prepend`/`append`/`replace` |
| `github_api_key` | string | - | GitHub API Key（支持 `${ENV}`） |

> 支持的后端: arXiv、GitHub、Wikipedia、Reddit。根据检测到的意图路由到专门后端。

### ChunkingConfig（`types_cortex.go:39-47`）

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | true | 是否启用语义分块 |
| `max_size` | int | 1200 | 每块最大 rune 数 |
| `overlap` | int | 200 | 相邻块重叠 rune 数 |
| `min_size` | int | 100 | 最小块大小（更小的块合并） |

---

## J. MemoryFlow 配置

**源码**: `types_cortex.go:81-88`（`MemoryFlowConfig`） | 默认值: `defaults.go:164-167`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | false | 是否启用 MemoryFlow |
| `db_path` | string | `wukong.db` | 数据库路径 |
| `namespace` | string | `assistant` | 命名空间 |
| `embedding_dimensions` | int | 0 | 向量维度（0 = 自动） |
| `planner_model` | string | - | 规划器模型（支持 `${ENV}`） |
| `extractor_model` | string | - | 提取器模型（支持 `${ENV}`） |

> MemoryFlow 提供对话转录记录、唤醒上下文生成（`[Context from past conversations]`）与事实提升（`PromoteFacts` → tRPC Memory）。

---

## K. GraphFlow 配置

**源码**: `types_cortex.go:92-98`（`GraphFlowConfig`） | 默认值: `defaults.go:170-173`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | false | 是否启用 GraphFlow |
| `db_path` | string | `wukong.db` | 数据库路径 |
| `extractor_model` | string | - | 提取器模型（支持 `${ENV}`） |
| `max_chars_per_doc` | int | 8000 | 每文档最大字符数 |
| `auto_extract` | bool | false | 是否在每轮对话后自动抽取实体/关系 |

> GraphFlow 执行 SPARQL 查询与知识图谱构建。`AutoExtract` 启用时在每轮对话后异步执行 `ExtractFromTranscript` + `BuildGraph`。

---

## L. ImportFlow 配置

**源码**: `types_cortex.go:102-105`（`ImportFlowConfig`） | 默认值: `defaults.go:176-177`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | false | 是否启用 ImportFlow |
| `db_path` | string | `wukong.db` | 数据库路径 |

> ImportFlow 支持结构化数据导入（DDL→KG 映射、CSV→RAG+KG）。

---

## M. Revision 配置

**源码**: `types_cortex.go:111-124`（`RevisionConfig`） | 默认值: `defaults.go:181-191`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | true | 是否启用上下文优化 |
| `revision_provider` | string | - | 摘要用 Provider |
| `revision_model` | string | - | 摘要用模型 |
| `enable_llm_summarize` | bool | false | 是否启用 LLM 摘要 |
| `summary_cooldown` | duration | `120s` | 摘要冷却时间 |
| `summary_timeout` | duration | `30s` | 摘要超时时间 |
| `max_command_output` | int | 8000 | 命令输出最大长度 |
| `enable_semantic_search` | bool | false | 是否启用语义搜索 |
| `search_strategy` | string | `include_all` | 搜索策略 |
| `max_context_tokens` | int | 64000 | 上下文 token 软上限 |
| `trim_ratio` | float64 | 0.3 | 裁剪比例 [0.0, 1.0] |

> **ContextRevisionEngine** 实际取 `min(max_context_tokens, EffectiveContextWindowForDefault())` 作为真实截断阈值。触发条件（满足任一）：`estimatedTokens > maxTokens × (1 - trim_ratio)`、`messageCount > 100`、距上次摘要超 5min。`CreateRevisionModel` 解析顺序：`revision_provider/revision_model` → `lightweight_provider/model` → `default_provider`。

---

## N. Browser 配置

**源码**: `types_browser.go:18-36`（`BrowserConfig`） | 默认值: `defaults.go:211-233`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | true | 是否启用浏览器自动化 |
| `browser_type` | string | `chromium` | 浏览器类型 |
| `backend` | BrowserBackendType | `rod` | 自动化后端: `chromedp`/`rod`（rod 失败时降级到 chromedp） |
| `headless` | bool | true | 是否无头模式 |
| `browser_path` | string | - | 浏览器可执行文件路径 |
| `stealth` | bool | false | 是否启用隐身模式 |
| `cache_dir` | string | `.wukong/cache` | 缓存目录 |
| `max_download_size` | int64 | 104857600 | 最大下载大小（100MB） |
| `timeout` | duration | `60s` | 操作超时 |
| `viewport_width` | int | 1280 | 视口宽度 |
| `viewport_height` | int | 720 | 视口高度 |
| `scroll` | bool | false | 是否自动滚动 |
| `control_url` | string | - | 远程调试控制 URL |
| `workers` | int | 4（运行时兜底） | Worker 数量（<=0 时取 4） |
| `global_render_slots` | int | 0 | 进程级全局渲染并发预算（0.3.0+）。`0` = 自动 `max(4, NumCPU)`；负值禁用全局上限；与各池 `workers` 独立（池大小为局部并发，本值为全进程渲染总量） |
| `profile_dir` | string | - | 浏览器配置文件目录 |
| `proxy` | ProxyConfig | - | 代理配置 |
| `search` | SearchConfig | - | 搜索引擎配置 |

### ProxyConfig（`types_browser.go:39-44`）

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | false | 是否启用代理 |
| `pool` | []string | - | 代理池 URL 列表 |
| `rotate_every` | int | 10 | 每 N 次请求轮换代理 |

### SearchConfig（`types_browser.go:49-55`）

每个后端通过各自 `enabled` 字段独立激活。默认值注册于 `defaults.go:226-233`：

| 后端 | 字段前缀 | 默认 `enabled` | 支持展开的字段 |
|------|---------|---------------|---------------|
| DuckDuckGo | `browser.search.duckduckgo` | **true**（`url` 默认 `https://api.duckduckgo.com/`） | `url` |
| SearXNG | `browser.search.searxng` | false（`url` 默认 `http://localhost:8080/`） | `url`, `api_key` |
| Tavily | `browser.search.tavily` | false | `api_key` |
| Google | `browser.search.google` | false（无显式默认，bool 零值） | `api_key`, `cse_id` |
| Bing | `browser.search.bing` | false（无显式默认，bool 零值） | `api_key` |

> 默认仅 DuckDuckGo 启用（无需密钥）。注意 config.yaml 模板中的取值（禁用 duckduckgo、启用 searxng）是模板示例，不是内置默认。任一后端启用时其必填字段缺失为致命错误（见 §3.1）。

---

## O. Visualiser 配置

**源码**: `types_features.go:10-15`（`VisualiserConfig`） | 默认值: `defaults.go:221-224`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | true | 是否启用自动可视化 |
| `output_dir` | string | `.wukong/visuals` | 图表输出目录 |
| `max_width` | int | 1200 | 最大宽度（px） |
| `max_height` | int | 800 | 最大高度（px） |

---

## P. Tutorial 配置

**源码**: `types_features.go:18-21`（`TutorialConfig`） | 默认值: `defaults.go:227-228`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | true | 是否启用教程引导 |
| `language` | string | `zh` | 教程语言 |

---

## Q. TopOfMind 配置

**源码**: `types_features.go:24-28`（`TopOfMindConfig`） | 默认值: `defaults.go:231-234`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | true | 是否启用置顶指令 |
| `instruction_file` | string | `.wukong/instructions.md` | 指令文件路径 |
| `max_length` | int | 2000 | 指令最大长度 |

> 非空时注入系统提示词的 `TopOfMindInstructions` 字段。

---

## R. CodeMode 配置

**源码**: `types_features.go:31-34`（`CodeModeConfig`） | 默认值: `defaults.go:237-239`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | true | 是否启用 JS 代码沙箱 |
| `timeout` | duration | `10s` | JS 执行超时 |
| `max_memory_mb` | int | 128 | 内存限制（MB） |

---

## S. Apps 配置

**源码**: `types_apps.go:8-13`（`AppsConfig`） | 默认值: `defaults.go:243-290`

### 通用

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | true | 是否启用应用系统 |
| `app_dir` | string | `.wukong/apps` | 应用存储目录 |

### Clone 克隆默认值（`types_apps.go:29-71`，35+ 字段）

| 字段 | 类型 | 默认值 | 单位/说明 |
|------|------|--------|----------|
| `max_pages` | int | 0 | 最大页面数（0 = 无限） |
| `max_depth` | int | 0 | 最大爬取深度（0 = 无限） |
| `traversal` | string | `bfs` | 遍历策略: `bfs`/`dfs` |
| `subdomains` | bool | false | 是否包含子域名 |
| `scope_prefix` | string | - | 作用域 URL 前缀 |
| `workers` | int | 4 | 爬取 Worker 数（>= 1） |
| `asset_workers` | int | 8 | 资源下载 Worker 数 |
| `timeout` | int | 300 | 页面导航超时（**秒**） |
| `render_timeout` | int | 120 | 单次渲染等待（**秒**） |
| `settle` | int | 1500 | 网络空闲等待（**毫秒**） |
| `scroll` | bool | false | 是否自动滚动 |
| `respect_robots` | bool | true | 是否遵守 robots.txt |
| `crawl_delay` | int | 0 | 请求间延迟（**毫秒**） |
| `rate_limit_whitelist` | []string | [] | 资产限速豁免域名（完全信任的 CDN/内网/dev server）：跳过 per-host token bucket 且免疫 429/503 动态降速；大小写不敏感，无端口条目匹配任意端口（`localhost` ⊃ `localhost:3000`） |
| `rate_limit_ip_segment` | bool | true | IP 段惩罚传播：429/503 惩罚时解析违规 host 的 IP，记录到段级（v4 /24、v6 /64），同段其他 host（CDN 别名）下次等待时继承该最小间隔；正常路径零 DNS 开销 |
| `rate_limit_ip_prefix_v4` | int | 24 | IPv4 段前缀长度（0-32，越短惩罚范围越宽） |
| `rate_limit_ip_prefix_v6` | int | 64 | IPv6 段前缀长度（0-128） |
| `no_sitemap` | bool | false | 是否忽略 sitemap |
| `dedup_content` | bool | true | 是否内容去重 |
| `mobile_readable` | bool | true | 是否移动端可读 |
| `enable_resume` | bool | true | 是否启用断点续抓 |
| `persist` | bool | true | 是否持久化状态 |
| `incremental` | bool | false | 是否增量爬取 |
| `cache_max_age` | int | 86400 | 缓存 TTL（**秒**） |
| `headless` | bool | true | 是否无头模式 |
| `stealth` | bool | true | 是否启用隐身 |
| `chrome_profile` | string | `.wukong/chrome/profile` | Chrome 配置文件目录 |
| `chrome_path` | string | - | Chrome 可执行文件路径 |
| `antibot_enabled` | bool | true | 是否启用反反爬 |
| `antibot_auto_escalate` | bool | true | 是否自动升级反爬等级 |
| `asset_same_domain` | bool | true | 是否只下载同域资源 |
| `max_asset_bytes` | int64 | 52428800 | 资源最大字节数（50MB） |
| `cookie_file` | string | - | Cookie 文件路径 |
| `user_agent` | string | - | User-Agent |
| `browser_backend` | BrowserBackendType | `rod` | 浏览器后端: `rod`/`chromedp` |
| `proxy_enabled` | bool | false | 是否启用代理 |
| `proxy_pool` | []string | - | 代理池 URL 列表 |
| `proxy_rotate_every` | int | 10 | 每 N 次轮换代理 |
| `insecure_tls` | bool | false | 是否跳过 TLS 证书校验（仅内网/.mil/.gov） |
| `tls_ca_cert_path` | string | - | PEM 格式 CA 证书包路径（严格校验下信任特定根） |

> **单位注意**: `timeout`/`render_timeout`/`cache_max_age` 为秒；`settle`/`crawl_delay` 为毫秒。转换因子见 `internal/apps/manager.go:applyConfigDefaults`。

### Pack 打包默认值（`types_apps.go:74-80`）

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `compress` | bool | true | 是否压缩 |
| `incremental` | bool | false | 是否增量打包 |
| `language` | string | `eng` | 语言代码 |
| `creator` | string | `Wukong` | 创建者 |
| `format` | string | `html` | 打包格式: `html`/`zim`/`binary`/`app` |

---

## T. Extensions 配置

**源码**: `types_provider.go:44-61`（`ExtensionConfig`）

Extensions 为 MCP 外部服务器数组。每个扩展实现 tRPC-agent-go 的 `tool.ToolSet` 接口（`Tools(ctx) []tool.Tool` + `Close() error`），**没有自定义 Extension 接口**。

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | string | 扩展名称（唯一标识） |
| `type` | string | 扩展类型: `builtin`/`external`/`mcp_broker` |
| `transport` | string | 传输方式: `stdio`/`sse`/`http` |
| `command` | string | 启动命令（stdio） |
| `args` | []string | 命令参数 |
| `url` | string | 服务 URL（sse/http） |
| `env` | map[string]string | 环境变量 |
| `enabled` | bool | 是否启用 |
| `timeout` | duration | 超时时间 |
| `deeplink` | string | 深度链接模板 |
| `permissions` | []ToolPermission | 工具权限 |
| `mcp_broker` | bool | 是否作为 MCP Broker（聚合为 4 个工具） |
| `mcp_tool_filter` | []string | MCP 工具白名单 |
| `mcp_tool_exclude` | []string | MCP 工具排除列表 |
| `mcp_session_reconnect` | bool | 会话重连 |
| `mcp_session_reconnect_attempts` | int | 重连尝试次数 |

### ToolPermission（`types_provider.go:64-67`）

| 字段 | 类型 | 说明 |
|------|------|------|
| `tool` | string | 工具名称 |
| `allowed` | bool | 是否允许 |

### 12 个内置扩展（`RegisterBuiltins`）

| 序号 | 名称 | 默认启用条件 |
|----|------|------------|
| 1 | `developer` | 始终启用 |
| 2 | `computer_controller` | `cfg.Browser.Enabled` |
| 3 | `memory` | 始终启用 |
| 4 | `auto_visualiser` | `cfg.Visualiser.Enabled` |
| 5 | `tutorial` | `cfg.Tutorial.Enabled` |
| 6 | `top_of_mind` | `cfg.TopOfMind.Enabled` |
| 7 | `code_mode` | `cfg.CodeMode.Enabled` |
| 8 | `apps` | `cfg.Apps.Enabled` |
| 9 | `web` | 始终启用（含 duckduckgo/searxng/tavily/google/bing 子工具） |
| 10 | `agent_tools` | 始终启用 |
| 11 | `ard` | `cfg.ARD.Enabled` |
| 12 | `cortex` | `cfg.Cortex.Enabled` |

---

## U. A2A Server 配置

**源码**: `types_server.go:22-27`（`A2AServerConfig`） | 默认值: `defaults.go:362-366`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | false | 是否启用 A2A 协议服务器 |
| `address` | string | `:9090` | 监听地址 |
| `agent_name` | string | `wukong` | Agent 名称 |
| `agent_description` | string | `Wukong AI Agent - A2A service endpoint` | Agent 描述 |

---

## V. AGUI 配置

**源码**: `types_server.go:30-35`（`AGUIConfig`） | 默认值: `defaults.go:369-371`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | false | 是否启用 AG-UI SSE 服务器 |
| `address` | string | `:8080` | 监听地址 |
| `path` | string | `/agui` | SSE 路径 |
| `security` | ServerSecurityConfig | - | 安全配置 |

---

## W. ACP Server 配置

**源码**: `types_server.go:38-44`（`ACPServerConfig`） | 默认值: `defaults.go:374-384`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | false | 是否启用 ACP 服务器 |
| `address` | string | `:9091` | 监听地址 |
| `path` | string | `/acp` | API 路径 |
| `enable_streaming` | bool | true | 是否启用流式 |
| `security.auth.type` | string | `""` | 认证类型: `""`/`api_key`/`jwt` |
| `security.auth.api_key` | string | - | API Key（支持 `${ENV}`） |
| `security.auth.jwt_secret` | string | - | JWT 密钥（支持 `${ENV}`） |

> **关键**: 认证必须嵌套在 `security.auth.*` 下。顶层 `auth_type`/`api_key` 因无 mapstructure tag 会被 Viper 静默丢弃，导致认证失效。

### ServerSecurityConfig / ServerAuthConfig（`internal/server/security.go`）

```go
type ServerAuthConfig struct {
    Type      string `mapstructure:"type"`       // "" | "api_key" | "jwt"
    APIKey    string `mapstructure:"api_key"`
    JWTSecret string `mapstructure:"jwt_secret"`
}
type ServerSecurityConfig struct {
    TLS       ServerTLSConfig       `mapstructure:"tls"`
    Auth      ServerAuthConfig      `mapstructure:"auth"`
    RateLimit ServerRateLimitConfig `mapstructure:"rate_limit"`
}
```

---

## X. ACP-MCP 配置

**源码**: `types_server.go:47-51`（`ACPMCPConfig`） | 默认值: `defaults.go:387-389`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | true | 是否启用 ACP MCP 桥接 |
| `address` | string | `:3400` | 监听地址 |
| `path` | string | `/mcp` | MCP 路径 |

> ACP-MCP 桥接将扩展暴露为 MCP Server，供 ACP Agent 调用。

---

## Y. MCP Server 配置

**源码**: `types_server.go:53-59`（`MCPServerConfig`） | 默认值: `defaults.go:409-416`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | false | 是否启用独立 MCP 服务器（将扩展暴露为 JSON-RPC 2.0 MCP Server） |
| `address` | string | `:3401` | 监听地址（0.3.1 起补齐内置默认值，与其他服务端点对齐）。`validate.go:321-328` 仍保留启用时非空校验作为安全网 |
| `security.auth.type` | string | `""` | 认证类型: `""`/`api_key`/`jwt` |
| `security.auth.api_key` | string | - | API Key（支持 `${ENV}`） |

> **安全警告**: 暴露在非回环地址（`127.0.0.1`/`localhost`/`::1` 之外；裸 `:port` 绑定 0.0.0.0 视为非回环）时**必须**设置 `security.auth.type`，否则 `tools/call` 无鉴权、可经 `developer_command_execute` 执行任意命令（Critical 警告）。

---

## Z. Gateway 配置

**源码**: `internal/gateway/config.go:21-49`（`GatewayConfig`） | 默认值: `gateway/config.go:106-122`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | false | 是否启用消息网关 |
| `default_timeout` | duration | `900s` | Agent Run 最大时长 |
| `max_concurrent_sessions` | int | 100 | 跨通道最大并发会话数 |
| `message_dedup_ttl` | duration | `5m` | 消息去重窗口 |
| `rate_limit_per_user` | int | 20 | 每用户在窗口内最大 Run 次数 |
| `rate_limit_window` | duration | `60s` | 滑动窗口时长 |
| `feishu` | FeishuChannelConfig | - | 飞书通道配置 |

### Feishu 通道（`gateway/config.go:54-102`）

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | false | 是否启用飞书通道 |
| `app_id` | string | - | 飞书应用 ID（`cli_xxx`） |
| `app_secret` | string | - | 应用密钥（支持 `${ENV}`），用于 WebSocket 长连接与 token 获取 |
| `api_base` | string | `https://open.feishu.cn/open-apis` | API 基础 URL（Lark 国际版用 `https://open.larksuite.com/open-apis`） |
| `encrypt_key` | string | - | 事件加密密钥（支持 `${ENV}`） |
| `verification_token` | string | - | 遗留验证 token（支持 `${ENV}`，长连接模式下已废弃） |
| `stream_card_enabled` | bool | true | 是否启用流式卡片回复 |
| `stream_card_update_interval` | duration | `500ms` | 流式卡片更新间隔 |
| `max_message_length` | int | 4096 | 单条消息最大字符数（超出截断） |
| `enable_file_receive` | bool | false | 是否接收用户文件消息 |

> 飞书通道通过 WebSocket 长连接接收消息（无需公网回调 URL），通过 Lark Open API 回复。

---

## AA. Summon 配置

**源码**: `types_orchestration.go:19-24`（`SummonConfig`） | 默认值: `defaults.go:318-321`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | true | 是否启用子 Agent 委派 |
| `delegates_dir` | string | `.wukong/skills` | 委派定义目录（.md 文件，每个成为一个 `Delegate`） |
| `max_concurrent` | int | 5 | 最大并行子 Agent 执行数（>= 0） |
| `a2a_remotes` | []A2ARemoteConfig | - | 远程 A2A Agent 列表（OAuth2 类型自动刷新 token） |

> **字段区分**: `summon.delegates_dir` 服务于子 Agent 委派（`internal/summon/delegate.go`，加载 .md 委派定义）；`skill.skills_dir` 服务于技能仓库与自演化（`internal/skill/`、`internal/evolution/`，加载可演化技能包）。两者默认均为 `.wukong/skills`，但可分别配置。

### A2ARemoteConfig（`types_orchestration.go:27-40`）

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | string | 远程 Agent 名称 |
| `description` | string | 描述 |
| `server_url` | string | A2A 服务器 URL |
| `auth_type` | string | 认证类型: `""`/`api_key`/`jwt`/`oauth2`（`validate.go:349-358` 校验枚举；其他值 fatal） |
| `api_key` | string | API Key（支持 `${ENV}`） |
| `api_key_header` | string | API Key 请求头名称 |
| `jwt_secret` | string | JWT 密钥（支持 `${ENV}`） |
| `jwt_audience` | string | JWT 受众 |
| `jwt_issuer` | string | JWT 签发者 |
| `oauth_token_url` | string | OAuth Token URL |
| `oauth_client_id` | string | OAuth 客户端 ID |
| `oauth_client_secret` | string | OAuth 客户端密钥（支持 `${ENV}`） |

---

## AB. ANP 配置

**源码**: `types_orchestration.go:43-55`（`ANPConfig`） | 默认值: `defaults.go:313-321`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | false | 是否启用 ANP |
| `did_domain` | string | - | DID 域名（W3C DID 身份） |
| `did_path` | string | - | DID 路径 |
| `port` | int | 9092 | 监听端口 [0, 65535] |
| `discovery_enabled` | bool | true | 是否启用发现 |
| `meta_protocol_enabled` | bool | true | 是否启用能力协商（驱动 `summon.NewMetaProtocol`） |
| `http_sign_enabled` | bool | true | 是否启用 RFC 9421 HTTP 签名 |
| `e2ee_enabled` | bool | true | 是否启用端到端加密（驱动 `summon.NewE2EEMessenger`） |
| `a2a_enabled` | bool | true | 是否启用 A2A 桥接 |
| `agui_enabled` | bool | true | 是否启用 AG-UI |
| `mcp_enabled` | bool | true | 是否启用 MCP |

> **约束**: `meta_protocol_enabled` 启用时 `port` 必须 > 0。

---

## AC. ARD 配置

**源码**: `types_orchestration.go:10-16`（`ARDConfig`） | 默认值: `defaults.go:297-301`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | false | 是否启用 ARD |
| `registry_url` | string | - | 注册中心 URL |
| `catalog_path` | string | `.wukong/ard/catalog.json` | 本地 Catalog 路径 |
| `publish_enabled` | bool | false | 是否发布到注册中心 |
| `publish_port` | int | 0 | 发布端口 |

---

## AD. Dify 配置

**源码**: `types_orchestration.go:103-110`（`DifyConfig`） | 默认值: `defaults.go:352-356`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | false | 是否启用 Dify 集成 |
| `base_url` | string | - | Dify 平台 URL |
| `api_secret` | string | - | API 密钥（支持 `${ENV}`） |
| `agent_name` | string | `dify` | Agent 名称 |
| `enable_streaming` | bool | false | 是否启用流式 |
| `timeout` | duration | `120s` | 超时时间 |

---

## AE. Knowledge 配置

**源码**: `types_orchestration.go:79-89`（`KnowledgeConfig`） | 默认值: `defaults.go:337-344`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | false | 是否启用知识库 RAG |
| `embedder_provider` | string | - | 嵌入用 Provider（空 → 回退 cortex.embedding_*，再回退 default_provider） |
| `embedder_model` | string | `text-embedding-3-small` | 嵌入用模型 |
| `sources` | []string | - | 知识源目录列表 |
| `source_urls` | []string | - | 知识源 URL 列表 |
| `vector_store` | string | `inmemory` | 向量存储后端 |
| `max_results` | int | 5 | 最大检索结果数 |
| `enable_source_sync` | bool | false | 是否启用源同步 |
| `search_tool_name` | string | `knowledge_search` | 搜索工具名称 |

---

## AF. OKF 配置

**源码**: `types_orchestration.go:92-100`（`OKFConfig`） | 默认值: `defaults.go:434-441`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | false | 是否启用 OKF |
| `bundle_dir` | string | `.wukong/okf` | OKF Bundle 目录 |
| `injector_enabled` | bool | false | 是否启用知识注入 |
| `enrichment_enabled` | bool | false | 是否启用知识丰富 |
| `enrichment_output_dir` | string | - | 丰富输出目录 |
| `auto_export` | bool | false | 是否自动导出 |
| `register_in_ard` | bool | false | 是否注册到 ARD |

---

## AG. Skill 配置

**源码**: `types_orchestration.go:58-61`（`SkillConfig`） | 默认值: `defaults.go:309-310`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | true | 是否启用技能系统 |
| `skills_dir` | string | `.wukong/skills` | 可演化技能包目录 |

> 服务于技能仓库（`internal/skill/manager.go`）与自演化引擎（`internal/evolution/engine.go`），加载带版本控制的可演化技能包。

---

## AH. Evolution 配置

**源码**: `types_orchestration.go:64-76`（`EvolutionConfig`） | 默认值: `defaults.go:324-334`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | false | 是否启用进化引擎 |
| `auto_patch` | bool | false | 是否自动应用补丁 |
| `analysis_provider` | string | - | 分析用 Provider |
| `analysis_model` | string | - | 分析用模型 |
| `min_confidence` | float64 | 0.7 | 最小置信度 [0.0, 1.0] |
| `cooldown_period` | duration | `30m` | 冷却周期 |
| `max_patches_per_day` | int | 10 | 每日最大补丁数 |
| `max_versions_kept` | int | 10 | 保留版本数 |
| `max_patch_size` | int | 8192 | 最大补丁大小 |
| `analysis_timeout` | duration | `60s` | 分析超时 |
| `export_json` | bool | false | 是否导出 JSON 日志 |

---

## AI. Workflow 配置

**源码**: `types_orchestration.go:129-137`（`WorkflowConfig`） | 默认值: `defaults.go:347-349`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `mode` | string | `single` | 编排模式（10 种，见下） |
| `max_iterations` | int | 10 | 最大迭代次数 |
| `cycle_mode` | string | `default` | 循环模式子类型 |
| `sub_agents` | []SubAgentConfig | - | 自定义子 Agent 配置 |
| `team_members` | []TeamMemberConfig | - | 团队成员配置 |
| `claude_code_bin` | string | - | Claude Code 二进制路径 |
| `codex_bin` | string | - | Codex 二进制路径 |

### 10 种编排模式（WorkflowMode，`workflow.go:29-40`）

| 模式 | 说明 |
|------|------|
| `single` | 单体 Agent（默认） |
| `chain` | 链式: planner → executor → reviewer |
| `parallel` | 并行: 多视角并发 |
| `cycle` | 循环: planner ↔ executor 迭代 |
| `graph` | 图: 条件 DAG |
| `team_coordinator` | 团队: Leader 通过 AgentTool 委派给成员 |
| `team_swarm` | 蜂群: 代理间直接转移控制，无中央协调者 |
| `claude_code` | Claude Code 子进程集成 |
| `codex` | Codex 子进程集成 |
| `dify` | Dify 平台 HTTP 集成（`/chat-messages`） |

---

## AJ. Telemetry 配置

**源码**: `types_observability.go:8-16`（`TelemetryConfig`） | 默认值: `defaults.go:416-422`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | false | 是否启用 OpenTelemetry 遥测 |
| `exporter_type` | string | `console` | 导出器类型: `console`/`grpc`/`http` |
| `endpoint` | string | `localhost:4317` | 导出端点 |
| `service_name` | string | `wukong` | 服务名 |
| `service_version` | string | `1.0.0` | 服务版本 |
| `environment` | string | `development` | 环境标识 |
| `sample_rate` | float64 | 1.0 | 采样率 [0.0, 1.0] |

---

## AK. Observability 配置

**源码**: `types_observability.go:19-24`（`ObservabilityConfig`） | 默认值: `defaults.go:413`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `langfuse_enabled` | bool | false | 是否启用 Langfuse |
| `langfuse_host` | string | - | Langfuse 服务地址 |
| `langfuse_public_key` | string | - | Public Key（支持 `${ENV}`） |
| `langfuse_secret_key` | string | - | Secret Key（支持 `${ENV}`） |

---

## AL. Eval 配置

**源码**: `types_observability.go:27-38`（`EvalConfig`） | 默认值: `defaults.go:404-407`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enabled` | bool | false | 是否启用评测/回归测试 |
| `evalset_path` | string | `.wukong/evals/default.evalset.json` | 评测集路径 |
| `results_path` | string | `.wukong/evals/results.json` | 结果输出路径 |
| `metrics` | []EvalMetricConfig | - | 评测指标 |

---

## AM. Artifact 配置

**源码**: `types_observability.go:41-46`（`ArtifactConfig`） | 默认值: `defaults.go:410`

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `backend` | string | `inmemory` | 存储后端: `inmemory`/`cos` |
| `cos_bucket_url` | string | - | COS 桶 URL（仅 `cos` 后端） |
| `cos_secret_id` | string | - | COS Secret ID（支持 `${ENV}`） |
| `cos_secret_key` | string | - | COS Secret Key（支持 `${ENV}`） |

---

## 完整配置示例

以下为最小化生产配置示例，完整模板见项目根目录 `config.yaml`。

```yaml
# ===== 全局设置 =====
log_level: info
default_provider: vllm
lightweight_provider: vllm
lightweight_model: deepseek-v4-flash-0731

# ===== Providers =====
providers:
  - name: vllm
    type: vllm
    api_key: ""
    base_url: "http://localhost:8000/v1"
    model: "deepseek-v4-flash-0731"
    context_window: 131072
  - name: openai
    type: openai
    api_key: ${OPENAI_API_KEY}
    base_url: ${OPENAI_BASE_URL:-https://api.openai.com/v1}
    model: gpt-4o

# ===== Agent =====
agent:
  max_llm_calls: 50
  max_tool_iterations: 50
  max_run_duration: "3600s"
  tool_call_timeout: "120s"
  parallel_tools: true
  streaming: true
  temperature: 0.7
  max_tokens: 4096
  tool_retry_enabled: true
  context_compaction: true
  context_compaction_keep_recent: 1
  planner: ""
  recipe_enabled: true
  recipe_dir: ".wukong/recipes/"

# ===== Security =====
security:
  permission_mode: smart
  block_dangerous_commands: true
  ignore_file_enabled: true
  ignore_file: .wukongignore
  sandbox:
    kill_on_parent_exit: false   # true = 父进程退出时终止子进程
    limits:                      # 全 0 = 不限制（默认）
      max_cpu_seconds: 0         # Windows: Job Object / Linux: RLIMIT_CPU
      max_memory_bytes: 0        # 非零须 >= 1 MiB
      max_file_bytes: 0          # 仅 Linux 生效；非零须 >= 512
      max_processes: 0           # 建议留空或 >= 2，勿设 1

# ===== Storage =====
session:
  backend: sqlite
  db_path: wukong.db
  event_limit: 500
  enable_summary: true

memory:
  backend: sqlite
  db_path: wukong.db
  auto_extract: true
  enable_smart_cleanup: true
  cleanup_trigger_threshold: 0.8
  cleanup_target_threshold: 0.6
  recency_weight: 0.4
  reference_weight: 0.3
  importance_weight: 0.2
  length_weight: 0.1

recall:
  enabled: true
  search_mode: fts5
  max_results: 10

# ===== CortexDB =====
cortex:
  enabled: true
  db_path: wukong.db
  embedding_base_url: ${EMBEDDING_BASE_URL:-http://localhost:8082/v1}
  embedding_api_key: ${EMBEDDING_API_KEY:-vllm}
  embedding_model: ${EMBEDDING_MODEL:-bge-m3-Q8_0}
  search_strategy:
    recall_mode: hybrid
    dense_weight: 0.7
    text_weight: 0.3
    fusion_method: rrf

# ===== Revision =====
revision:
  enabled: true
  enable_llm_summarize: true
  max_context_tokens: 24000
  trim_ratio: 0.3

# ===== Service Endpoints =====
a2a_server:
  enabled: true
  address: ":9090"
agui:
  enabled: true
  address: ":8080"
acp_server:
  enabled: true
  address: ":9091"
acp_mcp:
  enabled: true
  address: ":3400"

# ===== Apps =====
apps:
  enabled: true
  app_dir: ".wukong/apps"
  clone:
    workers: 4
    traversal: bfs
    headless: true
    stealth: true
    antibot_enabled: true
  pack:
    format: html
```

---

## 附录

### 相关文档

| 文档 | 说明 |
|------|------|
| [ARCHITECTURE.md](./ARCHITECTURE.md) | 系统架构详解 |
| [API_REFERENCE.md](./API_REFERENCE.md) | 内部 API/接口参考 |
| [CLI_TUI.md](./CLI_TUI.md) | CLI & TUI 架构 |
| [README.md](../README.md) | 项目主页 |

---

> **最后更新**: 2026-08-23