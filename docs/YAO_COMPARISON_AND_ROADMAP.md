# Wukong × Yao 对比分析与优化路线图

> 本文档是对 [YaoApp/yao](https://github.com/YaoApp/yao)（当前 main：v1.0.0-rc17，约 7.9k★ / 703 fork / 4200+ commits）的深度研读结论，与 Wukong v0.3.3 源码的逐维度对比分析，以及由此导出的优化路线图。文末附 P0-1「统一能力总线」接口设计草案，供评审后再动代码。
> 最后更新：2026-09-02

---

## 目录

1. [Yao 是什么：演进史与现状](#1-yao-是什么演进史与现状)
2. [Yao v1.0 架构要点](#2-yao-v10-架构要点)
3. [Yao 的四个"灵魂设计"](#3-yao-的四个灵魂设计)
4. [Wukong 与 Yao 的异同](#4-wukong-与-yao-的异同)
5. [优化路线图（P0 / P1 / P2）](#5-优化路线图p0--p1--p2)
6. [P0-1 能力总线接口设计草案](#6-p0-1-能力总线接口设计草案)
7. [主要来源](#7-主要来源)

---

## 1. Yao 是什么：演进史与现状

### 1.1 两代形态：同一个仓库，两种产品

| 代际 | 时间 | 定位 | 核心机制 |
|------|------|------|----------|
| 经典 Yao（0.10.x） | 2022–2024 | Go 低代码应用引擎："用 JSON DSL 描述 90% 的接口与页面" | gou 框架（process + connector）+ v8go（V8 运行时）；DSL 覆盖 model / api / flow / script / widget；内置管理后台 widgets；Neo AI 助手 |
| Yao Agents（v1.0.0-rc） | 2025–2026 | **自主智能体运行时**："The App Runtime for the AI Era"，事件驱动、主动式、可自我调度 | 保留 process 中枢；新增 agent 框架（21 个子模块）、SUI/CUI 双界面引擎、workspace/task board/知识库、DSH runner、多端 App（桌面 / Android / 浏览器） |

v1.0 的品牌口号与 Wukong 高度同赛道："All your agents and workspaces in one place, on every device you own"，self-hosted、local-first、单二进制。**这意味着对比不是"低代码平台 vs Agent CLI"的错位比较，而是两个收敛中的 Agent 运行时的正面对比。**

### 1.2 现状快照（2026-08-22，v1.0.0-rc17）

- 发布节奏：约每 1–2 周一个 rc（rc8 → rc17，2026-07 至 2026-08）。
- 近期主题：DeepSeek V4 配置与 vision 消息（rc17）、DSH 多模型配置与 lifetime 逻辑（rc16）、超算互联网 Token Plan（rc15）、session/聊天消息重构（rc14）、新 dsh runners（rc13）、任务式 inbox 通知与技能发现（rc9）。
- 许可证：Yao Open Source License（自定义）；Android beta（cui-android 0.6.37 APK）已可下载。

---

## 2. Yao v1.0 架构要点

### 2.1 启动与 CLI

- `cmd/start.go` + `engine/load.go` 的 **14 步顺序装载**（组件逐个注册以满足依赖）；开发模式带热重载。
- Cobra CLI：`yao start` / `yao run <process>` / `yao migrate` / `yao version` / `yao sui` / `yao agent`。
- 配置极简：`Root / Mode(production|development) / Host / Port(默认 5099) / DB / Runtime(V8) / Session`。

### 2.2 Process 系统（一切能力的中枢）

Process 是 Yao 的统一函数寻址机制，REST API、CLI、DSL、UI 全部调用同一地址空间：

| 命名空间 | 提供方 | 示例 |
|---|---|---|
| `models.*` | Model DSL + xun QueryDSL | `models.user.Find` |
| `flows.*` | Flow DSL | `flows.auth.Login` |
| `scripts.*` | TS/JS（V8） | `scripts.lib.Utils` |
| `plugins.*` | Go 插件 | `plugins.custom.Method` |
| `widgets.*` | UI Widget 动作 | `widgets.table.user.Search` |
| `sui.*` | SUI 页面操作 | `sui.page.Build` |
| `agent.*` | AI Agent | `agent.assistant.Stream` |

CLI 可直接调用：`yao run <process>`。

### 2.3 Agent 框架（agent/ 下 21 个子模块）

子模块：`assistant / board / caller / computer / config / content / context / docs / eval / i18n / inbox / llm / memory / output / robot / sandbox(v2) / search / store / task / types / testutils`。

- **助手即目录**：一个助手 = `package.yao`（JSON：connector、DB search 模型、MCP servers）+ `prompts.yml`（系统提示词）+ `locales/*.yml`（i18n）+ `src/index.ts`（hooks）。
- **Hook 模型**（跑在 v8go，基于 `@yao/runtime`）：
  - `Create` hook：LLM 调用前预处理消息、改写 LLM 配置、或 **delegate 给其他助手**（如含"退款"的消息路由给 refund-specialist）；
  - `Next` hook：后处理 LLM 响应，可循环回 LLM 续跑。
- **工具 = MCP**：工具服务以 "process" transport 注册，工具名映射 Yao Process（如 `models.order.Paginate`），入参 JSON Schema + `x-process-args` 传参。
- **DSH（DeepSeek Harness）runner**：v1.0 已集成（rc13/rc16），支持多模型配置与 lifetime 管理；对 DeepSeek 生态（V4 / vision / Token Plan）是第一公民。
- **搜索**：Web / 知识库 / 数据库三路（数据库搜索由 agent 生成 QueryDSL 自动完成）。
- **侧边栏页面**：助手可在会话侧边栏渲染 SUI 页面（`yao sui build agent`），hook 发送 action 消息导航。
- **评测**：`yao agent test -i "..."` 或 `-i tests/inputs.jsonl -v`，`yao agent extract output-*.jsonl` 提取结果。

### 2.4 双界面引擎

- **SUI**（服务端渲染模板引擎）：build / compile / JIT 流水线（`sui/core/` 下 build.go、compile.go、jit.go、parser.go、token.go、translate.go、injections.go…），goquery 解析 HTML、esbuild 编译 TS，数据绑定 + 脚本注入；`yao sui` 命令行与 `sui.page.Build` process。
- **CUI**（[YaoApp/cui](https://github.com/YaoApp/cui)）：面向 AI 原生应用的聊天界面框架，覆盖桌面 / Android / 浏览器。

### 2.5 开放 API 与依赖栈

- API（`/v1` 前缀）：`POST /v1/chat/completions`、`GET /v1/chat/sessions(/:id/messages)`、`GET /v1/agent/assistants(/:id)`、`POST /v1/file/:uploaderID`；SSE + WebSocket 双通道。
- 核心依赖：gou（process + connector）、xun（QueryDSL / DB 抽象）、kun（日志 / 异常）、rogchap/v8go（V8）、mark3labs/mcp-go、qdrant（向量）、neo4j（图）、gin、gorilla/websocket。

### 2.6 工程化亮点

- **dsl-schema 独立仓库**：给 JSON DSL 提供机读 schema，使 LLM 能"有 schema 可依地"生成应用描述——AI 创作闭环的基建。
- **评测 CLI 闭环**：JSONL 输入 → 运行 → 提取，测试框架内建于产品。
- **i18n 内建**：助手名称/描述/UI 文案全部 `locales/*.yml`。

---

## 3. Yao 的四个"灵魂设计"

1. **Process 统一能力总线** — 所有后端能力（数据/脚本/流/插件/agent/界面）都是可寻址函数。这是它一切声明式能力、可编程性（`yao run`）、与 AI 生成闭环的地基。
2. **DSL-first** — 应用 = 一组声明文件（package.yao / prompts.yml / SUI 模板 / flow），引擎装载生效；dsl-schema 让 AI 生成 DSL 可校验。用户不改 Go 代码即可改变系统行为。
3. **双界面引擎** — 同一套后端能力同时交付 Web 应用（SUI）与对话界面（CUI），一次开发、跨设备产品化。
4. **Runner/Hook 分层** — Create/Next 钩子 + 可插拔 runner（DSH 等）+ agent 间 delegate，让"agent 编排 agent"成为一等能力。

---

## 4. Wukong 与 Yao 的异同

### 4.1 收敛点（两者已高度同赛道）

1. **赛道收敛**：Yao v1.0 从低代码引擎转型为"本地优先、单二进制、自托管"的 Agent 运行时，与 Wukong 定位正面重叠。
2. **单二进制 + 常驻多协议服务**：Wukong 六监听端点 + Feishu 出站（A2A :9090 / ACP :9091 / AG-UI SSE :8080 / ACP-MCP :3400 / MCP :3401 / ANP :9092 / Gateway 出站 WS）；Yao 为 `/v1` REST + SSE + WebSocket。Wukong 协议广度更高。
3. **Hook 设计同源**：Wukong 的 `PreStepHook` / `PreToolExecuteHook` 注释明示借鉴 deepseek-harness 的 agent/pre-step 与 tools/pre-execute 事件（`internal/agent/hooks.go:1-20`）；Yao 则直接集成 dsh runner 与 Create/Next hooks——双方在"步界拦截 + 拒绝即关闭持久回合"这一设计语言上已趋同。
4. **声明式子代理**：Wukong recipe YAML（extends 继承、子配方拓扑解析、JSON Schema 响应约束、重试/超时、fsnotify 热重载，`internal/agent/recipe.go:198-253`）≈ Yao 助手目录（package.yao / prompts.yml / TS hooks）。Wukong 热重载领先；Yao 的 delegate 与 i18n 领先。
5. **记忆/知识栈**：双方都有向量 + 图。Wukong 纵深更大（HNSW + FTS5 + RRF/MMR 融合 + Cross-Encoder 重排 + 三层记忆 + GraphFlow SPARQL）；Yao 胜在组件标准化（qdrant / neo4j 外置）。
6. **评测意识**：`yao agent test`（JSONL 闭环）vs `internal/eval`（trajectory / pattern / length 打分）——双方都有，但 Yao 已产品化为 CLI 工作流。

### 4.2 本质差异

| 维度 | Yao v1.0 | Wukong v0.3.3 | 影响 |
|---|---|---|---|
| 能力寻址 | process 统一总线，CLI/API/DSL/UI 共用同一地址空间 | 分散：MCP 工具名 + recipe 名 + 编译期注册（`internal/extension/factory.go:21-70` 硬编码 switch；`internal/extension/builtin/registry.go:9-83` 固定 12 项） | Wukong 没有"可编程地基"，新增内置能力必须改源码 |
| 声明面 | DSL-first：应用=文件；dsl-schema 支撑 AI 生成 | config.yaml + recipe + SKILL.md；10 种编排模式是硬编码枚举（`internal/agent/workflow.go:29-40`，`Build` 为 switch `workflow.go:91-108`），无用户可写 flow DSL | Yao 无代码可改行为；Wukong 行为拓扑不可用户定义 |
| 界面 | SUI（SSR + esbuild/JIT）+ CUI，桌面/Android/浏览器 | 纯终端 TUI（Bubble Tea）+ CUI（REPL/向导），对外仅 SSE 端点，无第一方 Web UI | Yao 能交付"产品"，Wukong 交付"工具" |
| 脚本运行时 | v8go 完整 TS 运行时：scripts、hooks、助手逻辑全可脚本化 | goja 仅是 `code_execute` 沙箱工具（`internal/codemode/executor.go:5-24`：128MB / 5 并发 / API 白名单）；hook 只能 Go 编译期实现 | Wukong 用户无法用脚本写 hook/工具 |
| 模型接入 | gou connector 抽象；DeepSeek 一等公民（V4 / vision / Token Plan） | 7 种 provider 类型全部映射到 OpenAI 兼容层（`internal/provider/factory.go:63-74`：`openai, anthropic, google, deepseek, ollama, lmstudio, vllm → createOpenAI`） | 原生 Anthropic tool blocks / thinking / prompt cache、Gemini grounding 等特性不可达 |
| 数据层 | model DSL + xun QueryDSL + `yao migrate` 版本迁移 | 无迁移体系：`CREATE TABLE IF NOT EXISTS` 散落（`internal/recall/store.go:605-629`、`internal/evolution/store.go:36-63`、`internal/session/eventlog.go:87-99`、`internal/todo/tool.go:150`），部分表归 tRPC 框架所有 | schema 演进、多端同步受限 |
| Agent 形态 | 自主智能体：事件驱动 / 主动 / inbox / task board / 跨设备 | 会话式 Agent + 10 编排模式 + HITL 中断恢复 | Wukong 编排多样性更强；Yao 自主性与产品化更强 |
| 生态 | dsl-schema / Studio / 多端 App / i18n / 双语文档 / 自定义开源许可 | 零依赖单机、中文文档为主、AGPL-3.0 | 外部贡献门槛与分发面差异 |

### 4.3 Wukong 领先、应当守住的点

- **记忆纵深**：双引擎三层 + 图谱 + 混合检索重排，远超 Yao 的 scope。
- **安全纵深**：Guard 4 权限模式 + ApprovalBroker（`internal/security/approval.go:101-296`）+ 命令令牌分析 + SSRF 防护 + `.wukongignore` + goja 沙箱 + OS 沙箱（Landlock/Seatbelt/LowIL）+ 全输出面脱敏。
- **浏览器自动化与 5 级反爬**：双后端（rod/chromedp）渲染池、session 稳定指纹、CDP 审计——Yao 完全没有。
- **协议广度**：A2A / ACP / ANP（DID + E2EE + RFC 9421 签名）/ AG-UI / MCP broker / ARD 双向发现。
- **工程细节**：recipe 热重载、纯 Go 无 CGO + 单文件 SQLite WAL（关闭时 `wal_checkpoint(TRUNCATE)`，`internal/util/database.go:85-110`）、自省式文档体系。

---

## 5. 优化路线图（P0 / P1 / P2）

**总原则：不抄 Yao 的产品路线（不做低代码/Web 应用引擎、不做任务看板），而是借它的"引擎化"设计补 Wukong 的"工具化"短板；同时守住并放大 4.3 节的差异化优势。**

### P0 · 架构地基（1–2 个版本）

1. **统一能力总线（Wukong 版 process）** — 把内置扩展、recipe、MCP 工具、todo 等统一注册为可寻址能力 `<namespace>.<name>[.<action>]`；替换 `internal/extension/factory.go` 硬编码 switch；CLI 增加 `wukong run <capability> --args '{...}'`（对齐 `yao run`）；hook / Guard / ToolSearch 全走总线。设计草案见第 6 节。
2. **声明式 flow DSL** — ✅ **v1 已落地（2026-09-03）**：用户可写 YAML flow（`.wukong/flows/*.yaml`，`agent.flow_enabled` 开启）声明 **agent 节点**（LLM 步骤：instruction + prompt 模板 + 可选 model/temperature/max_tokens/retry/timeout，经 recipe 同款 agenttool 包装）与 **capability 节点**（引用能力总线地址 + `{{.node.field}}` 模板参数），**edges** 支持条件路由（`when` 模板）与 DAG 校验（拓扑排序 + 环检测），无 edges 时按声明顺序线性执行。每条 flow 编译为 `flow-<name>` 工具 + `flow.<name>` 能力（`SyncFlowCapabilities` 热重载同步），fsnotify 热重载与 recipe 同源。执行引擎为 Go 侧 DAG 解释器（`internal/agent/flow.go` schema/校验、`flow_exec.go` 引擎、`flow_toolset.go` 编译/工具集）；`wukong caps run flow.<name>` 可直接调用（冒烟实测：flow → 模板 → 能力总线 → 工具执行全链路）。**v1 未含**：HITL interrupt 节点、extends 继承、inline flows（config 内嵌）、流式节点输出、循环/子图——按需排期；10 种硬编码编排模式保留为预设，与用户 flow 互补。
3. **SQLite 迁移版本化** — ✅ **已落地（2026-09-03）**：新增 `internal/migration` 包（`Apply`/`Applied`，事务化应用 + `wukong_schema_migrations` 版本簿记表，`Optional` 标记承载 FTS5 类"构建可能不支持"的宽松语义）。**8 处**散建表（原记录 4 处，实盘 8 处）全部收敛为各子系统 `migrations.go` 声明式列表：recall（base + FTS5 可选集）、evolution、session/eventlog、todo、cortex/lexical（含 vec 表）、memory/metadata、gateway/sessions、search-tune。迁移 SQL 原样保留 `IF NOT EXISTS`，对存量数据库是无害 no-op 并补记版本——升级零干预。运行时各 init 惰性应用保持不变；新增 **`wukong migrate`**（对齐 `yao migrate`）供部署钩子/CI 预检与状态巡检，幂等（冒烟实测：legacy DB 首跑补记 7 项、二跑跳过）。

### P1 · 扩展性与生态（2–3 个版本）

4. **JS 脚本 hook** — ✅ **已落地（2026-09-03）**：新增 `internal/scripthook` 包。`.wukong/hooks/*.js`（`agent.script_hooks_enabled` 开启，超时 `script_hooks_timeout` 默认 5s）可定义 **`beforeStep`**（改写 `ctx.message.content` / `{reject, reason}` 关闭回合，接入 PreStepHook 瀑布）与 **`beforeTool`**（按 `{tool_name, args}` 拦截工具调用），并经全局 **`tool({name, description, parameters}, handler)`** 注册脚本工具——LLM 名为声明名、能力地址 `script.<name>`（SourceScript），`wukong caps run script.x` 可直接调用。沙箱与 codemode 同源：goja 每次调用全新 runtime + `Interrupt` 超时 + panic 恢复，纯 ECMAScript 无 IO API。**有意偏差**：hook 失败（异常/超时）**fail-open**（记日志跳过）——脚本属用户自身信任域的增强件，不应击穿 agent 可用性；脚本工具失败照常报错给 LLM。v1 未含：hook 热重载（改脚本需重启）、`require`/跨脚本共享。
5. **Provider 原生化** — ⚠️ **按上游现实调整为"能力矩阵 + 逃生舱"（2026-09-03）**：实盘确认 trpc-agent-go（v1.10.0 与最新 v1.11.2）的 model/ 仅有 OpenAI 原生实现（+hunyuan/deepseek/qwen Variant 特化），**无 anthropic/gemini 原生包**——自研协议客户端违背"框架组装"哲学且需独立维护流式/工具块/缓存语义。本轮落地：① `docs/PROVIDERS.md` 能力矩阵（对齐 Yao 的 connector matrix）；② factory 增强：`gemini` 类型别名（google 同路由）、deepseek 显式启用框架 VariantDeepSeek（reasoning content 特化此前未被使用）、`extra_headers`/`extra_fields` 配置逃生舱（provider 特有 header/请求体字段免改源码透传）；③ 升级路径记录在案：上游出现 model/anthropic 或 model/gemini 包时，切换成本为 factory 两处 switch 各一行。原生 Anthropic tool blocks/prompt cache、Gemini grounding 的缺失清单见矩阵文档 §3。
6. **AG-UI 参考客户端** — ✅ **已落地（2026-09-03）**：`internal/server/agui.html`（go:embed，`internal/server/agui_static.go` 挂载于 AG-UI 服务器 `/`）。自包含单文件控制台（无构建步骤/无外部资源/无 CDN，离线可用）：text_delta 流式渲染、tool_calls 工具调用展示、error 呈现、**done 事件回填 session_id 实现会话连续**、可选 X-API-Key（鉴权模式下配合 `?api_key=` 打开页面）、端点可配置（默认同源 `/agui`，零 CORS 摩擦）。同轮补齐 AG-UI 端点自身的测试空白：fake runner 驱动的 SSE 帧序列契约测试（text_delta→tool_calls→done 全链路断言）、请求校验 400、runner 错误的 SSE error 事件、静态页标记/404 兜底/health，共 5 个用例；页面 JS 经 Node 语法校验。定位是协议的"活文档"与最小可用前端，不是产品级 Web UI。
7. **评测闭环** — ⚠️ **实盘修正 + 测试债清偿（2026-09-03）**：实查发现 `wukong eval` 命令已存在（`internal/cli/eval.go`：evalset JSON 输入 + results 输出，本项的 CLI 部分在早期版本已落地，原计划低估了现状）；本项剩余的 `internal/cortex` 零测试缺口已开始清偿——新增 `cortex_test.go` 8 个用例（lexical store 端到端：FTS5 检索/会话隔离/per-session 上限/向量余弦检索（同步覆盖 P0-3 迁移路径）、VectorCache 计算缓存/逐出/命中统计、Reranker 降级）。**测试驱动出真实缺陷修复**：`Reranker.Rerank` 注释承诺"API 失败回退原始顺序"但实现直接返回错误——已按注释与仓库渐进式降级原则（ARCHITECTURE §15.5）修正。待办：memoryflow/graphflow/extractor 等依赖 cortexdb 或 LLM 的路径仍无单测，eval CLI 与 `yao agent test` 的 JSONL 逐条输入格式差异待对齐。

### P2 · 工程健康（持续）

8. **拆组合根** — `internal/cli/session.go`（约 1717 行）/ `internal/agent/loop.go`（约 2127 行）/ `internal/cli/tui/model.go`（约 1935 行）按 bootstrap 阶段拆分；解决 config 包反向依赖（config→gateway/server 嵌入结构体导致回调 workaround，见 `docs/ARCHITECTURE.md` §接口解耦）。
9. **i18n 起步** — README / docs 英文版，降低外部贡献门槛。
10. **发布卫生** — 清理仓库根 `github.com/` 词目录 hack（`.gitignore:9-12`，改构建期下载或 embed）、删 `_probe/`、版本号统一走 git tag（当前 `internal/util/version.go:10` 硬编码 0.3.3 而仅存在 v0.3.0 tag）、恢复 Homebrew/Scoop 发布（`.goreleaser.yaml:138-161` 已注释）。

### 产品取舍（明确不做）

Yao 的自主 Agent 形态（inbox / task board / 跨设备 workspace）**不照抄**——与 Wukong"本地单机深度工具"定位冲突。只吸收低成本的"主动式"子集：定时 / 文件变化 / webhook 触发器 → 自动发起 run（MemoryFlow WakeUp 已有雏形）。

### 如果只做三件事

**能力总线（1）→ flow DSL（2）→ 迁移体系（3）。** 三者共同把 Wukong 从"配置良好的工具"推进为"可编程引擎"——这正是 Yao 多年积累中最值得移植的资产。

---

## 6. P0-1 能力总线接口设计草案

> 本节为设计草案，供评审；获认可后再进入实现。

### 6.1 现状与问题（证据）

- 内置扩展经 `CreateBuiltinToolSet` 的 **switch 硬编码**（`internal/extension/factory.go:21-70`），12 个 builtin 名单固化在 `RegisterBuiltins`（`internal/extension/builtin/registry.go:9-83`）。
- CoreLoop 手工聚合工具：FunctionTools + RecipeToolSet + todo + ToolSets（`internal/agent/loop.go:155-198`），聚合顺序与来源分散在 3 个包。
- 命令识别靠**工具名/参数键字符串启发式**（`internal/agent/loop.go:1953-1983`：`isCommandTool` 名单 + `command/cmd/shell/script` 键名匹配）——改名或嵌套键即可绕过（有 Guard 权限模式兜底，但本质脆弱）。
- ToolSearch 压缩候选工具（`loop.go:260+`）只能看到 LLM 工具清单，无法被 CLI / 协议端点复用。

### 6.2 目标

- 一切可调用能力有**全局唯一地址**，可被 LLM 工具调用、CLI、协议端点（MCP server / ACP / AG-UI）与未来的 flow DSL、JS hook 共同消费。
- Guard 依据**声明式元数据**（scopes / mutating）判定权限，替代字符串启发式。
- 新增 builtin 能力 = 实现接口 + 自注册，不再改 factory。

### 6.3 地址规范

```
<namespace>.<name>[.<action>]

tools.<builtin>.<tool>     内置扩展工具     tools.web.aggregate_search
mcp.<server>.<tool>        外部 MCP 工具    mcp.github.create_issue
recipe.<name>              recipe 子代理    recipe.translator
todo.write                 内置任务工具     todo.write
agent.session.<action>     引擎元能力       agent.session.summarize
```

规则：全小写、`.` 分层、地址即身份（重复注册报错）；`Descriptor.Name` 才是 LLM 可见工具名（保持现有命名不受影响）。

### 6.4 核心接口（新包 `internal/capability`）

```go
// Package capability 提供统一能力总线：一切可调用能力的注册、寻址与发现。
package capability

// Capability 是能力总线的最小单元。
type Capability interface {
    // Address 返回全局唯一地址，如 "tools.web.aggregate_search"。
    Address() string
    // Descriptor 返回声明式元数据（LLM 清单、caps list、Guard 共用）。
    Descriptor() Descriptor
    // Invoke 统一入口；args 为 JSON 对象（与 tool.Declaration.Parameters 兼容）。
    Invoke(ctx context.Context, args json.RawMessage) (json.RawMessage, error)
}

// Descriptor 是能力的声明式元数据。
type Descriptor struct {
    Address     string          `json:"address"`
    Name        string          `json:"name"`              // LLM 可见工具名
    Description string          `json:"description"`
    Parameters  json.RawMessage `json:"parameters"`        // JSON Schema
    Scopes      []string        `json:"scopes,omitempty"`  // "shell"/"network"/"fs.read"/"fs.write"/"memory"...
    Mutating    bool            `json:"mutating"`          // 是否产生副作用（Guard 默认策略依据）
    Source      string          `json:"source"`            // builtin|mcp|recipe|script
}

// Registry 是进程内能力注册表（并发安全）。
type Registry struct{ /* mu + caps + byNS */ }

func NewRegistry() *Registry
func (r *Registry) Register(c Capability) error      // 地址冲突 → error
func (r *Registry) Resolve(addr string) (Capability, bool)
func (r *Registry) List(namespace string) []Descriptor
func (r *Registry) Search(query string, topK int) []Descriptor // 供 ToolSearch 复用

// AsTools 将注册表能力适配为 trpc-agent-go 的 []tool.Tool，
// CoreLoop 聚合处直接消费；tool.Call 内部转回 Invoke 并携带地址上下文。
func (r *Registry) AsTools() []tool.Tool
```

反向适配器（现有能力零改动接入）：

```go
// 从现有 tool.Tool（builtin / MCP / todo / timeout 包装后）包装为 Capability。
func FromTool(addr string, src string, t tool.Tool) Capability
// 从 recipe 包装（Descriptor.Parameters 由 recipe JSON Schema 生成）。
func FromRecipe(rc *Recipe) Capability
```

### 6.5 与现有子系统的对接点

| 现有代码 | 接入方式 |
|---|---|
| `internal/extension/builtin/registry.go:9-83` | 每个 builtin 包提供 `RegisterCapabilities(*capability.Registry, *config.WukongConfig)`，自描述注册；`RegisterBuiltins` 退化为声明默认启停 |
| `internal/extension/factory.go:21-70` | 过渡期保留为 shim（内部改调 registry），Phase B 删除 switch |
| `internal/extension/manager.go` | 装载 builtin / MCP 后逐工具 `FromTool` 注册（MCP 地址 `mcp.<server>.<tool>`） |
| `internal/agent/loop.go:155-198` | `allTools = cfg.FunctionTools + registry.AsTools()`；recipe/todo/timeout 包装全部迁入注册路径 |
| `internal/agent/loop.go:1823-1983`（Guard 回调 + isCommandTool 启发式） | Guard 改读 `Descriptor.Scopes/Mutating`：命令域能力显式声明 `shell` scope；字符串启发式删除 |
| `loop.go:260+` ToolSearch | 改调 `registry.Search` |
| CLI（`internal/cli/`） | 新增 `wukong caps list [namespace]` 与 `wukong run <capability> --args '{...}'`（复用现有 `run` 命令的 provider/session 初始化） |
| 协议端点（`mcp_server.go` / `acp.go` / `agui.go`） | 可选：直接 expose registry（tools/list ↔ `List`），端点实现大幅简化 |

### 6.6 Guard / 安全集成

- `Descriptor.Scopes` 是唯一权限事实源；Guard 权限模式（auto/smart/manual/chat_only）映射到 scope 策略表（如 `chat_only` 放行只读、`mutating` 一律走 ApprovalBroker）。
- `FromTool` 包装时对存量工具给出**保守默认**：无法判定时 `Mutating=true` + 空 scopes（宁可多问），后续逐工具补声明。
- 审计日志记录 capability 地址而非工具名，事件日志（`wukong_model_events`）可按 namespace 聚合。

### 6.7 迁移路径

- **Phase A（只读引入）— ✅ 已落地（2026-09-03）**：
  - 新增 `internal/capability` 包：`Capability` / `Descriptor` / `InvokeFunc` 接口，并发安全 `Registry`（Register/Resolve/List/Search/Len + `AsTools()`），`Adapter` / `FromTool` / `ToolFromCapability` 适配层。测试覆盖：注册校验与地址冲突、并发读写（-race）、声明往返等价（InputSchema 经 `tool.Schema` 字节级一致）、调用结果归一化（nil/[]byte/RawMessage/不可序列化降级）、排序确定性。
  - 新增 `internal/extension/caps.go`：`Manager.RegisterCapabilities`（遍历 toolSets，nil 占位跳过，broker 特判 `mcp.broker`，重复地址 warn-跳过先到先得）+ 导出 `RegisterToolSet` / `RegisterTools` 与地址前缀常量。
  - bootstrap 接入（`internal/cli/session.go`）：toolset 聚合完成后创建 registry，注册 extMgr + extension_manager + top_of_mind + code_mode + apps + agent_tools 六个来源，注入 `BootstrapState.Caps`；CoreLoop 聚合保持不变。
  - 新增 `wukong caps list [namespace] [--json]`（`internal/cli/caps.go`）：与 bootstrap 同构的注册路径（含 `RegisterBuiltins` 对齐——修复了 `extension list` 路径不含内置扩展的同类缺口）。实测本机注册 46 项能力。
  - 与原草案的偏差：① `FromRecipe` 不放入 capability 包（与 internal/agent 会形成 import 环），改为通用 `Adapter`，recipe 适配器随 Phase B 落在 agent 包；② Source 枚举增加 `manager`；③ 引擎 function tools（todo/recall/kg/import/summon）Phase A 不注册，随 Phase B；④ 已知外观问题：builtin 工具声明名自带扩展前缀，地址出现重复段（如 `tools.web.web_search`），Phase B 可选去重。
- **Phase B（聚合切换）— ✅ 已落地（2026-09-03）**：
  - **builtin 自注册**：新增 `internal/extension/builtin/constructors.go`（构造器注册表 `RegisterConstructor` / `CreateBuiltinToolSet`），`extension/factory.go` 退化为 3 行委托——新增内置扩展不再需要改 factory。`RegisterBuiltins`（默认启停声明）与构造器表的同步由测试 `TestBuiltinConstructors` 保证。
  - **CoreLoop 聚合切换**：`CoreLoopConfig.Capabilities` 注入后，`effectiveToolSets`（`internal/agent/loop.go`）以 `capability.RegistryToolSet` 快照作为扩展工具的唯一聚合来源（单代理 `WithToolSets` 与 WorkflowBuilder 子代理两路同时生效）；`Capabilities == nil` 时保留旧路径逐字节不变（回滚开关）。工具清单从"map 随机序"变为"按地址确定性排序"。
  - **Guard 描述符优先接缝**：`commandToolNeedsValidation` 先查 `Registry.ResolveByName` 的 `Descriptor.Scopes`（声明即权威，声明非 shell 的工具豁免启发式），无声明时回退 `isCommandTool` 名单启发式；scope 声明落地于 `builtin/scopes.go`（developer：`shell` / `fs.write`）。
  - **与原计划的偏差**（安全与功能权衡，均有意为之）：
    1. `isCommandTool` 启发式**未删除**，降级为无声明工具的回退——立即删除会使外部 MCP 工具与未声明工具的命令校验全部失效（安全回归）；完全移除移至 Phase C，待外部工具声明机制就绪。
    2. **ToolSearch 未改走 registry**：实查后发现它是框架级 runner 插件（trpc-agent-go `plugin/toolsearch`，LLM 压缩候选清单），与本注册表正交；替换属行为变更无收益。`Registry.Search` 供 CLI 与 Phase C 消费方使用。
    3. **recipe 未注册为能力**：`RecipeToolSet.Reload()` 热重载在 toolset 内部换工具，registry 快照无法感知，会造成旧工具残留；待 registry 具备增量更新 API（Phase C）后再注册，当前 `effectiveToolSets` 显式保留 RecipeToolSet。
    4. MCP 工具经 `capabilityTool` 包装后走 `CallableTool.Call`，若框架曾对其使用 `StreamableCall` 流式优化则会退化为整块返回（结果内容不变）。
- **Phase C（消费方升级）— ✅ 核心已落地（2026-09-03）**：
  - **`wukong caps run <address> --args '{...}'`**：按总线地址直接调用能力并输出 JSON 结果（`internal/cli/caps.go`）。注册路径与 live session 同构（含 recipe.*：配置启用 recipe 时经 `agent.NewRecipeToolSet` 离线构建注册）。冒烟实测 `caps run tools.developer.developer_directory_list` 真实执行成功。
  - **registry 增量更新 API**：`Registry.Unregister` / `UnregisterNamespace`；`agent.SyncRecipeCapabilities`（导出）按 `recipe.<name>` 地址同步（"recipe-" 前缀剥离），并保持 per-tool 超时包装语义；`RecipeToolSet.SetReloadCallback` 让热重载后命名空间自动重同步——Phase B 的"recipe 不注册"偏差就此消除。
  - **recipe 注册为能力**：CoreLoop 在 Capabilities 就绪时同步注册，`effectiveToolSets` 收敛为 registry 单一来源（recipe 不再以独立 toolset 进入聚合，消除此前 WithTools/WithToolSets 双通道的重复注册）。
  - **外部 MCP 工具声明机制**：`extensions[].tool_scopes`（map[工具名][]scope）→ 注册时注入描述符（`internal/config/types_provider.go`、`internal/extension/caps.go`）。
  - **启发式退役（配置门控）**：`agent.command_validation_mode`（默认 `hybrid`，`Validate()` 枚举校验）：`hybrid` = 声明权威 + 启发式回退（默认行为不变）；`descriptor` = 纯声明（完全声明部署可退役启发式）；`heuristic` = 仅启发式。直接删除启发式会使用未声明工具静默失去命令校验，故以此门控替代硬删除。
  - **遗留**：flow DSL（P0-2）与 JS hook（P1-4）的注册面即 `RegisterTools`/`Adapter`/`SyncRecipeCapabilities`，随各自专题落地。

### 6.8 非目标

- 不做远程/跨进程能力调度（MCP 已覆盖外部场景）。
- 不做 UI widget 寻址（那是 Yao 的低代码路线，见第 5 节取舍）。
- 不改变对 LLM 的 tool-calling 协议与现有工具命名（`Descriptor.Name` 兼容层保证）。

---

## 7. 主要来源

### Yao 侧（2026-09-02 抓取）

- [YaoApp/yao](https://github.com/YaoApp/yao) — README（Yao Agents 定位 / DeepSeek Harness / workspace）、[releases](https://github.com/YaoApp/yao/releases)（rc8–rc17 变更）、[agent/ 目录结构](https://github.com/YaoApp/yao/tree/main/agent)
- [Yao Agent Framework README](https://raw.githubusercontent.com/YaoApp/yao/main/agent/README.md) — 助手目录结构 / Create-Next hooks / MCP process transport / 评测 CLI / /v1 API
- [DeepWiki: YaoApp/yao](https://deepwiki.com/YaoApp/yao) — 14 步装载、process 命名空间、SUI 流水线、依赖栈（gou/xun/kun/v8go/qdrant/neo4j）
- [yaoapps.com](https://yaoapps.com/)（"The App Runtime for the AI Era"）、[yaoagents.com](https://yaoagents.com/)（多端桌面/移动）
- [YaoApp/cui](https://github.com/YaoApp/cui)（CUI 框架）、[YaoApp/dsl-schema](https://github.com/YaoApp/dsl-schema)

### Wukong 侧（本仓库源码，v0.3.3）

关键证据行号已内联于正文（如 `internal/agent/hooks.go:1-20`、`internal/provider/factory.go:63-74`、`internal/agent/workflow.go:29-40` 等）；整体架构参见 [系统架构](ARCHITECTURE.md) 与 [API 参考](API_REFERENCE.md)。

---

> **最后更新**: 2026-09-03
