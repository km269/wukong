# Wukong — 记忆优先、编排驱动、安全纵深的 AI Agent 平台

> **版本**: v0.9.0 | **Go**: 1.26 | **文件**: 174+ `.go` | **包**: 28+ | **许可证**: GNU AGPL-3.0
>
> 基于 [tRPC-Agent-Go v1.10.0](https://github.com/trpc-group/trpc-agent-go) · [tRPC-MCP-Go v0.0.16](https://github.com/trpc-group/trpc-mcp-go) · [tRPC-A2A-Go v0.2.5](https://github.com/trpc-group/trpc-a2a-go) · [CortexDB v2.25.0](https://github.com/liliang-cn/cortexdb)

---

## 1. 架构哲学

| 哲学 | 核心信念 | 关键实现 |
|------|----------|----------|
| **记忆优先** | Agent 智能来源于跨会话知识积累 | 双引擎三层记忆：tRPC Memory + CortexDB Stack (HNSW+FTS5+RDF) |
| **框架组装** | 任何组件都应可替换 | CoreLoop 依赖注入，12 个子系统解耦 |
| **多 Agent 原生** | 编排是第一公民 | 10 种显式编排模式 + HITL 人机协同 |
| **进化智能** | 技能应从失败中学习 | LLM 分析 → 自动补丁 → 版本管理 → 热重载 |
| **纵深防御** | 安全是多层协同 | 5 层防御：Guard → goja JS沙箱 → OS沙箱 → .wukongignore → OS权限 |

---

## 2. 系统架构全景

```
┌──────────────────────────────────────────────────────────────────┐
│                     Wukong AI Agent Platform                      │
├──────────────────────────────────────────────────────────────────┤
│ Core Engine: CoreLoop — 中央编排器 (12 子系统)                    │
│   WorkflowBuilder(10模式) · TeamBuilder · ContextManager(3层)    │
│   Security Guard(5层) · HITL(中断-恢复) · TodoEnforcer(强制完成)  │
├──────────────────────────────────────────────────────────────────┤
│ Memory Stack: 双引擎三层记忆                                      │
│   短期: MemoryFlow — 转录 + WakeUp (身份/回忆/会话3层唤醒)        │
│   中期: CortexStore — HNSW向量 + FTS5全文 + 余弦相似度本地排序    │
│   长期: tRPC Memory — KV持久化 + AutoExtract + SmartCleanup       │
│   结构化: GraphFlow — RDF知识图谱 + SPARQL + auto_extract         │
├──────────────────────────────────────────────────────────────────┤
│ Recipe System: 14 项功能 (P0-P4)                                  │
│   参数化 · 结构化输出 · 子配方 · 重试 · 继承 · 内联 · 模型覆盖    │
│   超时 · 发现 · 热重载 · 指令模板 · 执行指标                       │
├──────────────────────────────────────────────────────────────────┤
│ Capability: 12内置扩展 · Evolution · Summon(A2A) · ARD(资源发现)  │
│   CodeMode(goja JS) · Browser(Chromedp) · Knowledge(RAG)          │
│   Apps(Clone/Pack/Sanitize/Server/History) · ZIM库 · OS沙箱       │
├──────────────────────────────────────────────────────────────────┤
│ Infrastructure: 7 LLM backends · OpenTelemetry · Langfuse         │
│   MultiPool(SQLite WAL) · fsnotify · text/template                │
├──────────────────────────────────────────────────────────────────┤
│ Storage: wukong.db — 单文件承载 session/memory/recall/cortex/todo  │
│   apps_history · evolution · projects · FTS5 · HNSW · vectors      │
└──────────────────────────────────────────────────────────────────┘
```

---

## 3. 核心特性矩阵

### 3.1 多 Agent 编排 (10 种模式)

| 模式 | 拓扑 | 场景 |
|------|------|------|
| `single` | 单Agent | 日常对话(默认) |
| `chain` | planner→executor→reviewer | 多步流水线 |
| `parallel` | 3视角并发 | 多角度分析 |
| `cycle` | planner↔executor | 自我改进 |
| `graph` | 条件路由DAG | 复杂决策 |
| `team_coordinator` | Leader委派 | 团队协作 |
| `team_swarm` | 自动transfer | 自主委派 |
| `claude_code` | Claude CLI | 本地Claude |
| `codex` | Codex CLI | 本地Codex |
| `dify` | Dify平台 | 低代码 |

### 3.2 Recipe 系统 (14 项功能)

| 阶段 | 功能 | YAML字段 |
|------|------|----------|
| P0 | 参数化模板 + 结构化输出 | `prompt`, `parameters`, `response.json_schema` |
| P1 | 子配方组合 + 重试与校验 | `tools: [recipe-xxx]`, `retry` |
| P2 | 内联配方 + 配方继承 | `inline_recipes`, `extends` |
| P3 | 模型覆盖 + 超时 + 发现 + 热重载 | `model`, `timeout`, `list_recipes`, `reload_recipes` |
| P4 | 指令模板 + 执行指标 + 统计工具 | `instruction: "{{.var}}"`, `recipe_stats` |

### 3.3 记忆系统 (双引擎三层)

| 层级 | 引擎 | 核心机制 |
|------|------|----------|
| 短期 | MemoryFlow | IngestTurn转录 + WakeUp 3层语义唤醒 |
| 中期 | CortexStore | HNSW向量搜索 + 余弦相似度本地排序 |
| 长期 | tRPC Memory | AutoExtract异步LLM + SmartCleanup容量淘汰 |
| 结构化 | GraphFlow | auto_extract每轮对话 → RDF知识图谱 |

### 3.4 安全纵深模型

```
Layer 5: Guard      → auto/smart/manual/chat_only + 命令拦截 + Prompt注入检测
Layer 4: goja JS    → API白名单 + 128MB + 5并发 + ReDoS防护 + 1MB输入限制
Layer 3: OS沙箱     → Landlock(linux)/Seatbelt(macOS)/Low IL(Windows)
Layer 2: .wukongignore → gitignore兼容文件访问黑名单
Layer 1: OS权限     → 非root + ulimit
```

---

## 4. 快速开始

```bash
go install github.com/km269/wukong/cmd/wukong@latest
wukong configure                    # 交互式配置
wukong session                      # 启动交互会话
wukong session --provider deepseek --model deepseek-chat
wukong run --prompt "分析项目结构"
wukong extension list               # 查看扩展
```

---

## 5. LLM Provider 支持

| Provider | type | 说明 |
|----------|------|------|
| OpenAI | `openai` | GPT-4o, GPT-4 |
| Anthropic | `anthropic` | Claude Sonnet/Opus |
| Google | `google` | Gemini (OpenAI API兼容) |
| DeepSeek | `deepseek` | DeepSeek-Chat/Reasoner |
| Ollama | `ollama` | 本地开源模型 |
| LMStudio | `lmstudio` | 本地GPU加速 |
| ACP | `acp` | 远程ACP Agent |

---

## 6. 文档索引

| 文档 | 说明 |
|------|------|
| [架构分析](ARCHITECTURE.md) | 18章完整架构、28个ADR、模块依赖图、数据流 |
| [配置手册](CONFIG.md) | 38配置段参考、加载优先级、4种推荐方案 |

---

## 7. 许可证

GNU AGPL-3.0 — 见 [LICENSE](LICENSE)
