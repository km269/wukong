# Wukong 文档中心

> 本目录包含 Wukong AI Agent 平台的所有技术文档。

---

## 文档索引

### 核心架构

| 文档 | 说明 | 关键词 |
|------|------|--------|
| [系统架构](ARCHITECTURE.md) | 20 章系统全景详解 | CoreLoop、记忆系统、Evolution、ANP、ARD、Gateway |
| [配置手册](CONFIG.md) | 15 组配置全字段说明 | providers、agent、security、cortex、apps |
| [CLI & TUI 架构](CLI_TUI.md) | 命令树与终端 UI 架构 | Cobra、Bubble Tea、启动序列、流式传输 |

### 专题指南

| 文档 | 说明 | 关键词 |
|------|------|--------|
| [涉网应用深度分析](NETWORK_ANALYSIS.md) | 10 涉网子系统分析 + 分级升级建议 | HTTP Client、浏览器引擎、ANP、Gateway、ARD、反反爬 |
| [网站克隆技术指南](CLONE_GUIDE.md) | 克隆引擎与 ZIM 打包详解 | EnhancedCloner、分页、反反爬、ZIM 打包 |
| [反反爬技术详解](ANTIBOT_GUIDE.md) | 10 层反爬体系详解 | Antibot、Escalator、Stealth、Proxy Pool |
| [记忆系统架构](MEMORY_ARCHITECTURE.md) | 双引擎三层记忆详解 | MemoryFlow、CortexDB、GraphFlow、SmartCleanup |

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
| 涉网应用有哪些？怎么升级？ | [涉网应用深度分析](NETWORK_ANALYSIS.md) |
| 网站克隆怎么用？原理是什么？ | [网站克隆技术指南](CLONE_GUIDE.md) |
| 反爬是怎么处理的？ | [反反爬技术详解](ANTIBOT_GUIDE.md) |
| 记忆系统是怎么工作的？ | [记忆系统架构](MEMORY_ARCHITECTURE.md) |

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
| 内部包 | 30+ |
| 公共包 | 3 |
| 配置结构体 | 34+ |
| CLI 顶层命令 | 29 |
| CLI 子命令 | 60+ |
| 编排模式 | 10 种 |
| LLM Provider | 7 种 |
| 内置扩展 | 14 个 |
| 反反爬层级 | 10 层 |
| 安全防御层 | 5 层 |
| 服务端点 | 6 个协议 |
| 记忆层级 | 4 层 |
| 分页模式 | 6 种 |
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
├── internal/                 # 内部包 (30+)
│   ├── agent/               # CoreLoop 核心引擎
│   ├── apps/                # 应用管理 (克隆/打包)
│   ├── browser/             # 浏览器引擎 + 反反爬
│   ├── cli/                 # CLI + TUI
│   ├── config/              # 配置管理
│   ├── cortex/              # CortexDB 记忆栈
│   ├── evolution/           # 技能进化引擎
│   ├── extension/           # MCP 扩展管理
│   ├── gateway/             # 消息网关
│   └── ...                  # 更多子系统
├── pkg/                      # 公共包
│   ├── httpclient/          # HTTP 客户端
│   ├── sandbox/             # 跨平台沙箱
│   └── zim/                 # ZIM 格式读写
└── docs/                     # 文档 (本目录)
```
