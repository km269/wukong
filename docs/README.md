# Wukong 文档中心

> 本目录包含 Wukong AI Agent 平台的所有技术文档。
>
> 最后更新：2026-09-03 | 当前版本：v0.3.3
>
> **版本约定**：各文档尾注的"版本"统一跟随项目版本（权威源为
> [internal/util/version.go](../internal/util/version.go) 与
> [CHANGELOG.md](../CHANGELOG.md)）；OKF 等跟随外部规范的文档在尾注中
> 同时标注规范版本。统计口径约定：内置扩展数以
> `internal/extension/builtin/registry.go` 注册数为准（12 个）。

---

## 文档索引

### 核心架构

| 文档 | 说明 | 关键词 |
|------|------|--------|
| [系统架构](ARCHITECTURE.md) | 系统架构与各子系统技术实现细节 | CoreLoop、记忆系统、Evolution、ANP、ARD、Gateway |
| [配置手册](CONFIG.md) | 35 个配置段全字段说明 | providers、agent、security、cortex、apps |
| [CLI & TUI 架构](CLI_TUI.md) | 命令树与终端 UI 架构 | Cobra、Bubble Tea、启动序列、流式传输 |
| [API 参考](API_REFERENCE.md) | 内部接口签名与用法 | CoreLoop API、Provider Factory、Guard |
| [开发者指南](DEVELOPER_GUIDE.md) | 环境搭建与开发任务 | Go 1.26、项目结构、测试、调试 |
| [部署运维](DEPLOYMENT.md) | 部署、健康检查、故障排查 | Docker、GHCR、二进制、WAL |
| [Gateway 渠道开发指南](../internal/gateway/README.md) | Gateway 渠道开发指南，包内文档 | Channel 接口、飞书 WebSocket、dispatch 流水线 |

### 专题指南

| 文档 | 说明 | 关键词 |
|------|------|--------|
| [网站克隆技术指南](CLONE_GUIDE.md) | 克隆引擎与 ZIM 打包详解 | EnhancedCloner、分页、反反爬、ZIM 打包 |
| [Web 操作深度分析](WEB_OPERATIONS_ANALYSIS.md) | 浏览器/克隆/反爬/检索/HTTP 全链路剖析与优化建议 | 浏览器、EnhancedCloner、Antibot、aggregate_search、httpclient |
| [反反爬技术详解](ANTIBOT_GUIDE.md) | 5 级反爬升级体系详解 | Antibot、Escalator、Stealth、Proxy Pool |
| [记忆系统架构](MEMORY_ARCHITECTURE.md) | 双引擎三层记忆详解 | MemoryFlow、CortexDB、GraphFlow、SmartCleanup |
| [OKF 知识格式](OKF_GUIDE.md) | OKF v0.1 规范与集成 | Bundle、Concept、Skill、Knowledge、Evolution |
| [配置体系重构记录](REFACTOR_NOTES.md) | v0.3.3 配置重构变更明细与操作基线 | envexpand、defaults.go、偏差标注、口径速查 |
| [Yao 对比与优化路线图](YAO_COMPARISON_AND_ROADMAP.md) | 与 YaoApp/yao 的深度对比分析、优化路线图与能力总线设计草案 | Yao、能力总线、DSL、路线图 |

### 其他

| 文档 | 说明 |
|------|------|
| [许可证](LICENSE) | GNU AGPL-3.0 许可证文本 |

---

## 快速导航

### 我想了解...

| 问题 | 推荐文档 |
|------|---------|
| 整体系统是怎么设计的？ | [系统架构](ARCHITECTURE.md) |
| 有哪些配置项？怎么配置？ | [配置手册](CONFIG.md) |
| 命令行工具有哪些命令？ | [CLI & TUI 架构](CLI_TUI.md) |
| 网站克隆怎么用？原理是什么？ | [网站克隆技术指南](CLONE_GUIDE.md) |
| 反爬是怎么处理的？ | [反反爬技术详解](ANTIBOT_GUIDE.md) |
| 记忆系统是怎么工作的？ | [记忆系统架构](MEMORY_ARCHITECTURE.md) |
| 和 Yao 等同类项目相比异同在哪？优化方向是什么？ | [Yao 对比与优化路线图](YAO_COMPARISON_AND_ROADMAP.md) |

---

## 架构概览

```
+======================================================================+
|                        Wukong AI Agent Platform                       |
+======================================================================+
|  接入层  | CLI / TUI / Gateway / A2A / ACP / AG-UI / ANP / MCP        |
+----------+-----------------------------------------------------------+
|  编排层  | CoreLoop · WorkflowBuilder · ContextManager · Security    |
+----------+-----------------------------------------------------------+
|  能力层  | Evolution · OKF · ANP · ARD · Gateway · Extension         |
|          | Browser · Apps · Knowledge · Recall · Code Mode          |
+----------+-----------------------------------------------------------+
|  框架层  | tRPC-Agent-Go · tRPC-MCP-Go · tRPC-A2A-Go                |
+----------+-----------------------------------------------------------+
|  记忆层  | MemoryFlow (短期) · CortexStore (中期) · tRPC (长期)      |
|          | GraphFlow (结构化)                                        |
+----------+-----------------------------------------------------------+
|  存储层  | SQLite WAL (wukong.db) · Redis (可选) · COS (可选)        |
+======================================================================+
```

---

## 核心特性速览

### 七大架构哲学

1. **记忆优先** — 双引擎三层记忆架构
2. **框架组装** — 所有组件可替换、可注入
3. **多 Agent 原生** — 10 种编排模式 + HITL
4. **进化智能** — 技能从失败中自动学习
5. **双向发现** — ARD 联邦搜索 + 注册发布
6. **开放互通** — ANP 协议栈 (DID + E2EE)
7. **知识标准化** — OKF v0.1 开放知识格式

### 关键数字

| 指标 | 数值 |
|------|------|
| 内部包 | 56 个（含子包，`go list ./internal/...` 实测） |
| 公共包 | 5 个 |
| 配置结构体 | 54 个（分布在 11 个 `types_*.go` 文件） |
| WukongConfig 配置段 | 35 个配置段 + 2 个列表段 + 5 个顶层标量（共 42 个 mapstructure 字段） |
| CLI 顶层命令 | 32 个（root.go 注册；另有 56 个子命令） |
| CLI 命令定义 | 88 条（`Use:` 字段） |
| 编排模式 | 10 种 |
| LLM Provider | 8 种（openai/anthropic/google/deepseek/ollama/lmstudio/vllm/acp） |
| 内置扩展 | 12 个（`web` 扩展内含 5 个搜索后端工具） |
| 能力注册表 | 统一地址 `<ns>.<name>[.<tool>]`（tools.* / mcp.* / recipe.* / flow.* 等），`wukong caps list/run` 查看，为工具聚合单一来源 |
| 直接依赖 | 36 个（go.mod require 块） |
| 测试文件 | 约 130 个 |
| 反爬升级体系 | 5 级 |
| 安全防御层 | 5 层 |
| 服务端点 | 7 个协议（6 监听 + 飞书 Gateway 出站 WS） |
| 记忆层级 | 4 层 |
| 克隆层分页检测 | 3 种检测模式 + 游标兜底 |
| 浏览器 API 发现层 | 5 种 kind |
| 资源下载回退 | 4 层 |

---

## 相关资源

### 外部依赖

- [tRPC-Agent-Go](https://trpc.group/) — Agent 编排框架
- [tRPC-MCP-Go](https://trpc.group/) — MCP 协议实现
- [tRPC-A2A-Go](https://trpc.group/) — A2A 协议实现
- [CortexDB](https://github.com/liliang-cn/cortexdb) — 向量 + 全文 + 图谱数据库
- [OKF 规范](https://github.com/google/open-knowledge-format) — 开放知识格式
- [ZIM 格式规范](https://wiki.openzim.org/wiki/ZIM_file_format) — ZIM 文件格式
- [RFC 9421](https://www.rfc-editor.org/rfc/rfc9421) — HTTP 消息签名标准

### 项目结构

```
wukong/
├── cmd/                      # 可执行入口
│   ├── wukong/              # 主 CLI 应用
│   ├── zim-check/           # ZIM 校验工具
│   └── zim-ls/              # ZIM 列表工具
├── internal/                 # 内部包 (56 个)
│   ├── agent/               # ★ CoreLoop 核心引擎（10 种编排 + Recipe + Context）
│   ├── apps/                # 应用管理 (clone 克隆/pack 打包/sanitize 消毒/mcpapps/server)
│   ├── browser/             # ★ 浏览器引擎 (rod+chromedp) + 反反爬 (antibot/stealth/renderkit)
│   ├── cli/                 # ★ CLI (Cobra) + TUI (Bubbletea) — Composition Root
│   │   └── tui/             #   Bubble Tea 终端 UI
│   ├── config/              # 配置体系 (11 个类型文件, 54 个 Config 结构体)
│   ├── cortex/              # CortexDB 记忆栈 (MemoryFlow/GraphFlow/ImportFlow)
│   ├── evolution/           # LLM 驱动技能自进化引擎
│   ├── extension/           # ★ MCP 扩展管理 (builtin 12 个 + 外部 MCP + ACP 桥接)
│   ├── gateway/             # 消息网关 (飞书 WebSocket)
│   ├── ...                  # 更多子系统（共 56 个含子包）
├── pkg/                      # 公共包 (5 个)
│   ├── capability/          # fs/shell 能力接口层
│   ├── httpclient/          # HTTP 客户端 (DNS 缓存/限流/uTLS)
│   ├── logutil/             # 日志工具
│   ├── sandbox/             # 跨平台沙箱 (Landlock/Seatbelt/Low IL)
│   └── zim/                 # ZIM 格式读写 (v6)
└── docs/                     # 文档 (本目录)
```

---

[← 返回项目 README](../README.md)
