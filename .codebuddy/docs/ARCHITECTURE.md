# Wukong 系统架构

> **Go**: 1.26 | **文件**: 224 `.go` (51 `_test.go`) | **内部包**: 28 | **公共包**: 2
> **Config**: 44 结构体 · ~340 字段 (config.go + types.go + defaults.go + validate.go)
> **CLI**: 28 顶层 + 55+ 子命令 | **依赖**: 29 direct + 105 indirect
>
> 基于 [tRPC-Agent-Go v1.10.0](https://github.com/trpc-group/trpc-agent-go) · [tRPC-MCP-Go v0.0.16](https://github.com/trpc-group/trpc-mcp-go) · [tRPC-A2A-Go v0.2.5](https://github.com/trpc-group/trpc-a2a-go) · [CortexDB v2.25.0](https://github.com/liliang-cn/cortexdb)

---

## 1. 架构哲学

| 哲学 | 核心信念 | 关键工程决策 |
|------|----------|-------------|
| **记忆优先** | Agent 智能源于跨会话积累 | 双引擎三层记忆：tRPC Memory + CortexDB Stack |
| **框架组装** | 任何组件可替换 | CoreLoop 依赖注入，34 子系统接口隔离 |
| **多 Agent 原生** | 编排是第一公民 | 10 种编排模式 + HITL + 子Agent委派 |
| **进化智能** | 技能自我改进 | LLM分析→补丁→版本→热重载 |
| **双向发现** | 发现与被发现 | ARD 联邦搜索 + RegistryServer |

---

## 2. 系统全景

```
┌──────────────────────────────────────────────────────────────────────┐
│                       Wukong AI Agent Platform                        │
├──────────────────────────────────────────────────────────────────────┤
│ Entry: CLI(28cmd+55sub) │ TUI │ A2A:9090 │ ACP:9091 │ AG-UI:8080 │ MCP:3400│
├──────────────────────────────────────────────────────────────────────┤
│ Core Engine: CoreLoop (agent/, 21 files)                               │
│   WorkflowBuilder(10 modes) · TeamBuilder · ContextManager(3-tier)    │
│   Security Guard(5-tier) · HITL · TodoEnforcer · PromptTemplate       │
├──────────────────────────────────────────────────────────────────────┤
│ Agent Framework: tRPC-Agent-Go v1.10.0                                │
│   LLMAgent · ChainAgent · ParallelAgent · CycleAgent · GraphAgent      │
│   Planner · ToolSearch · ContextCompaction · Skill · Recipe           │
├──────────────────────────────────────────────────────────────────────┤
│ Memory Stack (Dual-Engine, 3-Tier):                                    │
│   Short: MemoryFlow — IngestTurn → WakeUp(3 layers) → PromoteFacts    │
│   Mid:   CortexStore — HNSW + FTS5                                    │
│   Long:  tRPC Memory — AutoExtract + SmartCleanup                      │
│   Graph: GraphFlow — auto_extract → RDF → SPARQL                      │
├──────────────────────────────────────────────────────────────────────┤
│ Capability Layer:                                                       │
│   Recipe(14) · 12内置扩展 · ARD(双向发现+7工具)                        │
│   Evolution · Summon(A2A) · CodeMode(goja) · Knowledge(RAG)            │
│   Browser(Chromedp) · Apps(8子命令:克隆+打包+预览)                     │
│   pkg/sandbox(10) · pkg/zim(5)                                          │
├──────────────────────────────────────────────────────────────────────┤
│ Infrastructure: 7 LLM · OpenTelemetry · Langfuse · MultiPool(SQLite)  │
├──────────────────────────────────────────────────────────────────────┤
│ Storage: wukong.db — all data in single SQLite WAL file                │
└──────────────────────────────────────────────────────────────────────┘
```

---

## 3. 目录结构

| 目录 | 文件数 | 用途 |
|------|--------|------|
| `cmd/wukong/` | 1 | 应用入口 |
| `internal/cli/` | 30 | CLI 命令 (Cobra) + TUI |
| `internal/agent/` | 21 | Agent 循环、Recipe、工作流、HITL |
| `internal/apps/` | 3 | 应用管理器、版本历史 |
| `internal/apps/clone/` | 18 | **网站克隆引擎** (Chromedp + Stealth + Antibot) |
| `internal/apps/browser/` | 2 | 无头 Chrome 浏览器池 |
| `internal/apps/pack/` | 4 | 多格式打包 (HTML/ZIM/Binary/App) |
| `internal/apps/sanitize/` | 3 | DOM 级 HTML 安全清理 |
| `internal/apps/mcpapps/` | 4 | MCP Apps 协议桥接 |
| `internal/apps/server/` | 1 | 应用预览 HTTP 服务 |
| `internal/browser/` | 2 + 8 sub | 通用浏览器控制 + Stealth + Antibot + Settle |
| `internal/extension/` | 10 | 扩展管理器 + MCP Broker |
| `internal/extension/builtin/` | 15 | 13 内置工具集 |
| `internal/config/` | 4 | 配置结构 + Viper 加载 + 验证 |
| `internal/ard/` | 16 | ARD 服务/客户端/注册表/联邦 |
| `internal/cortex/` | 12 | CortexDB 记忆栈 |
| `internal/` (其他) | ~40 | 安全/会话/技能/进化/知识等 |
| `pkg/sandbox/` | 10 | 跨平台沙箱隔离 |
| `pkg/zim/` | 6 | ZIM 格式读写 (Kiwix 兼容) |

### 配置包文件拆分

| 文件 | 职责 |
|------|------|
| `config.go` | 包文档 · `ResolvePath` · `WukongConfig` · `Loader` · 查询方法 |
| `types.go` | 43 个子配置结构体 (Provider/Agent/Security/Storage/Cortex/...) |
| `defaults.go` | `setDefaults` 按子系统拆分为 11 个方法 |
| `validate.go` | `Validate()` 致命错误检查 + `Warnings()` 非致命警告 |

---

## 4. CoreLoop 中央编排

`internal/agent/` (21 文件)

### 执行循环 (4 阶段)

```
Phase 1: Prepare — ContextManager + Recall/Cortex + WakeUp + ReadMemories + KG
Phase 2: Execute — runner.Run → LLM → Tool Calls → Guard.Check
Phase 3: Finalize — StoreMessage + IngestTurn + PromoteFacts + auto_extract
Phase 4: Return — contextMgr.AfterRun (token stats)
```

### 优雅关闭 (6 步)

`bgWg.Wait → runner.Close → evolution.Close → memory.Close(5s) → session+graphFlow → telemetry.Shutdown(10s)+dbPool.Close`

---

## 5. 多 Agent 编排 (10 模式)

| 模式 | 拓扑 | 底层 | 场景 |
|------|------|------|------|
| `single` | 单体 | LLMAgent | 日常对话 |
| `chain` | planner→executor→reviewer | ChainAgent | 流水线 |
| `parallel` | 3视角并发 | ParallelAgent | 多角度分析 |
| `cycle` | planner↔executor | CycleAgent | 自我迭代 |
| `graph` | 条件DAG | GraphAgent | 复杂决策 |
| `team_coordinator` | Leader委派 | TeamAgent | 团队协作 |
| `team_swarm` | 自动transfer | TeamAgent(swarm) | 自主委派 |
| `claude_code` | CLI进程 | exec.Cmd | 本地Claude |
| `codex` | CLI进程 | exec.Cmd | 本地Codex |
| `dify` | HTTP API | HTTP Client | 低代码 |

---

## 6. Recipe 子 Agent (14 功能)

工具链: `agenttool.NewTool → recipeTool → retryTool → timeoutTool`

| 功能 | 说明 |
|------|------|
| 参数化 | `${param}` 模板变量 + 类型校验 |
| 结构化输出 | JSON Schema 约束 |
| 子配方 | 嵌套组合 |
| 重试 | 指数退避 |
| 继承 | extends 属性链 |
| 内联 | config.yaml 直接定义 |
| 模型覆盖 | 指定 provider/model |
| 超时 | context.WithTimeout |
| 热重载 | fsnotify 监控 |

---

## 7. 记忆系统 (双引擎三层)

| 层级 | 引擎 | 机制 |
|------|------|------|
| 短期 | MemoryFlow | 转录 + 3层唤醒上下文 |
| 中期 | CortexStore | HNSW向量 + FTS5全文 |
| 长期 | tRPC Memory | AutoExtract + SmartCleanup |
| 结构化 | GraphFlow | RDF知识图谱 + SPARQL |

### SmartCleanup 容量淘汰策略

- **80% 容量以下**: 仅删除过期记忆（TTL > 30天）
- **80% 容量以上**: 按重要性评分淘汰至 60% 容量
- **评分公式**: 70% 时效性 + 30% 内容长度
- **配置项**: `memory.enable_smart_cleanup` / `cleanup_trigger_threshold` / `cleanup_target_threshold` / `memory_ttl`

---

## 8. 安全防御 (5 层)

```
Layer 5: Guard — auto/smart/manual/chat_only + blocked_commands + Prompt注入
Layer 4: goja  — API白名单 + 128MB + 5并发 + ReDoS + 1MB限制
Layer 3: OS沙箱 — Landlock(Linux) / Seatbelt(macOS) / LowIL(Windows)
Layer 2: .wukongignore — gitignore兼容文件黑名单
Layer 1: OS权限 — 非root + ulimit
```

---

## 9. LLM Provider (7 种)

| Provider | type | SDK | 特点 |
|----------|------|-----|------|
| OpenAI | `openai` | openai-go | GPT 系列 |
| Anthropic | `anthropic` | openai-go | Claude 系列 |
| Google | `google` | openai-go | Gemini 系列 |
| DeepSeek | `deepseek` | openai-go | 国产性价比 |
| Ollama | `ollama` | openai-go | 本地开源 |
| LMStudio | `lmstudio` | openai-go | 本地服务 |
| ACP | `acp` | HTTP | 远程代理 |

---

## 10. 服务端点 (4 协议)

| 协议 | 端口 | 用途 |
|------|------|------|
| A2A | 9090 | Agent-to-Agent 通信 |
| ACP | 9091 | Agent Client Protocol |
| AG-UI SSE | 8080 | Web UI 实时对话 |
| ACP MCP | 3400 | 跨协议工具桥接 |

---

## 11. CLI 命令体系

`internal/cli/` (30 文件): 28 顶层命令 + 55+ 子命令

```
wukong
├── session (5子命令: list/delete/info/export/resume)
├── run (单次/多轮)
├── configure / version / completion
├── extension (6子命令)
├── project / projects / eval
├── config (validate/show)
├── server / health
├── memory (4子命令)
├── provider (list/test)
├── env / skill (list/show)
├── recipe (list/show/validate)
├── init / knowledge (status)
├── apps                 # HTML 应用 + 网站克隆
│   ├── list             # 列出应用
│   ├── show <name>      # 应用详情 + 预览
│   ├── create           # 创建（空白/模板/导入）
│   ├── clone <url>      # 网站克隆（Headless Chrome）
│   ├── pack <name>      # 打包 (zim/binary/app)
│   ├── delete <name>    # 删除
│   ├── history <name>   # 版本历史
│   └── export <name>    # 导出单文件
├── ard (status/catalog)
├── evolution/cortex/todo (status)
├── bench / backup / system-check
├── docs / stats
└── tui
```

---

## 12. 网站克隆系统

`internal/apps/clone/` (18 文件) — 完整网站离线镜像引擎

### 架构

```
种子 URL → Headless Chrome(浏览器池) → DOM 快照 → 安全清理(去JS)
    → 链接重写(DOM相对路径) → 资源下载(独立池+CSS重写)
    → 内容去重(SHA256+硬链接) → 磁盘写入
```

### 核心模块

| 模块 | 文件 | 功能 |
|------|------|------|
| `EnhancedCloner` | `enhanced_cloner.go` | 主引擎，集成所有优化 |
| 浏览器池 | `browser/pool.go` | 单浏览器多Tab，信号量并发 |
| URL 映射 | `clone/urlx.go` | 确定性 URL→本地路径 |
| 爬取前沿 | `clone/frontier.go` | 去重 + 状态持久化(断点续抓) |
| 资源下载 | `clone/asset.go` | 独立下载器(重试+限流) |
| CSS 重写 | `clone/css.go` | url()/@import 引用重写 |
| 内容去重 | `clone/dedup.go` | SHA-256 + 硬链接 |
| HTML 重写 | `clone/rewrite.go` | DOM 级链接重写(相对路径) |
| 安全清理 | `sanitize/enhanced.go` | 去JS+死链接+移动CSS+报告 |
| 合规爬取 | `clone/robots.go` | robots.txt + Sitemap + 限速 |
| 增量缓存 | `clone/cache.go` | ETag/Last-Modified |

### Agent 工具 (13 tools)

`app_create / app_create_with_template / app_template_list / app_list / app_get / app_update / app_update_status / app_import / app_delete / app_clone / app_pack / app_preview / app_export`

---

## 13. ZIM 打包系统

`internal/apps/pack/` (4 文件) + `pkg/zim/` (6 文件)

### 输出格式

| 格式 | 描述 |
|------|------|
| `html` | 自包含 HTML 目录 |
| `zim` | ZIM 归档 (Kiwix 兼容，含元数据+图标+计数器) |
| `binary` | 自包含可执行文件 |
| `app` | 桌面应用 (.app/.AppDir/.exe) |

### ZIM 特性

- 完整 v6 格式 (Header + URL/Title Pointer Lists + Clusters + MD5)
- zstd 压缩 (文本集群) / 无压缩 (二进制集群)
- 增量缓存 (`.wukongcache` 集群复用)
- 富元数据: Title/Name/Language/Description/Creator/Publisher/Date/Source/Counter
- 图标嵌入 (48×48 PNG → `Illustrator_48x48@1`)
- W/mainPage 重定向 (Kiwix 兼容)

---

## 14. 扩展体系 (12 内置)

| 扩展 | 工具数 | 启用条件 |
|------|--------|----------|
| developer | 多 | 始终 |
| computer_controller | 多 | browser.enabled |
| memory | 6 | 始终 |
| auto_visualiser | 多 | visualiser.enabled |
| tutorial | 多 | tutorial.enabled |
| top_of_mind | 0 | top_of_mind.enabled |
| code_mode | 多 | code_mode.enabled |
| apps | 13 | apps.enabled |
| web | 多 | 始终 |
| agent_tools | 多 | 始终 |
| ard | 7 | ard.enabled |
| cortex | 多 | cortex.enabled |

---

## 15. 技术栈

| 类别 | 技术 | 版本 |
|------|------|------|
| Agent 框架 | tRPC-Agent-Go | v1.10.0 |
| MCP 协议 | tRPC-MCP-Go | v0.0.16 |
| A2A 协议 | tRPC-A2A-Go | v0.2.5 |
| 智能记忆 | CortexDB | v2.25.0 |
| CLI | Cobra + Viper | v1.9.1 / v1.20.1 |
| TUI | Bubbletea + LipGloss | latest |
| 浏览器 | Chromedp | v0.15.1 |
| JS 引擎 | goja | latest |
| LLM SDK | openai-go | v1.12.0 |
| 数据库 | modernc.org/sqlite | v1.38.2 |
| 缓存 | go-redis | v9.12.1 |
| 文件监控 | fsnotify | v1.8.0 |
| 可观测性 | OpenTelemetry | v1.43.0 |
| 压缩 | klauspost/compress | v1.18.6 |

---

## 16. 关键设计决策 (ADRs)

| # | 决策 | 理由 |
|---|------|------|
| 1 | SQLite WAL 共享 MultiPool | 单文件部署 |
| 2 | 双引擎记忆 | tRPC 存事实，CortexDB 存语义/图谱 |
| 3 | 轻量模型分工 | 主模型对话，轻量模型后台提取 |
| 4 | CoreLoop 依赖注入 | 所有子系统可替换/测试 |
| 5 | YAML Recipe + 热重载 | 文件变更即生效 |
| 6 | HITL 融入编排循环 | 决策点原生暂停 |
| 7 | SmartCleanup 容量淘汰 | 70%新鲜度+30%长度 |
| 8 | ACP + AG-UI 双协议 | ACP 客户端，AG-UI 浏览器 |
| 9 | MCP Broker 批量管理 | 外部 MCP 统一暴露 |
| 10 | goja 5层JS沙箱 | API白名单+内存+并发+ReDoS+代码长度 |
| 11 | OS级跨平台沙箱 | Landlock/Seatbelt/LowIL |
| 12 | ARD 双向发现 | 联邦搜索 + RegistryServer |
| 13 | Evolution 版本管理 | 每补丁保留版本 |
| 14 | 单文件 wukong.db | 简化部署 |
| 15 | Chrome 真实渲染克隆引擎 | Chrome渲染 + 资源本地化 + ZIM打包 + 反反爬 |
| 16 | 浏览器标签池复用 | 单进程多Tab，信号量控制并发 |
| 17 | 配置代码按职责拆分 | types/defaults/validate 分离，提升可维护性 |
