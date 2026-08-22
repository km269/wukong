# Wukong 系统概述

> **记忆优先 · 编排驱动 · 安全纵深 · 双向发现**
>
> 本地优先、框架组装、可深度扩展的开源 AI Agent 平台

---

## 项目简介

**Wukong（悟空）** 是一个基于 Go 语言构建的新一代 AI Agent 平台，名字取自中国神话中的齐天大圣孙悟空，寓意着智能、灵活和强大的能力。

Wukong 不仅仅是一个简单的 AI 聊天机器人，而是一个**本地优先、记忆驱动、多模式编排**的完整 AI Agent 开发框架。

---

## 核心价值

### 1. 记忆优先的智能系统

双引擎三层记忆系统，让 Agent 具备跨会话的知识积累能力：

- **短期记忆**: MemoryFlow 会话级别转录和 3 层唤醒（Identity / Recalled memories / Session context）
- **中期记忆**: CortexStore HNSW 向量 + FTS5 全文检索 + RRF/MMR 融合 + Cross-Encoder 重排
- **长期记忆**: tRPC Memory 自动提取和智能清理（四维评分：近度 40% + 引用 30% + 重要度 20% + 长度 10%）
- **结构化记忆**: GraphFlow 知识图谱构建（SPARQL 查询）

### 2. 多 Agent 编排原生支持

10 种原生编排模式，支持复杂工作流自动化：

- `single`: 单体 Agent
- `chain`: 规划者 → 执行者 → 审查者
- `parallel`: 多 Agent 并发执行
- `cycle`: 迭代优化循环
- `graph`: 条件路由 DAG
- `team_coordinator`: Leader 委派模式
- `team_swarm`: 自动 transfer 模式
- `claude_code`: Claude Code CLI 集成
- `codex`: OpenAI Codex 集成
- `dify`: Dify 平台集成

### 3. 技能自我进化

LLM 驱动的闭环自我学习机制（置信度门控 + 冷却周期 + 每日限制 + 安全验证），让 Agent 能够从失败中学习并持续改进。

### 4. 双向发现与开放互通

ARD 协议实现 Agent 的双向发现（联邦搜索 + RegistryServer 发布），支持 MCP、A2A、ANP 等开放协议，DID 身份 + HTTP 签名 + E2EE 加密保障安全互通。

### 5. 深度安全防御

5 层纵深防御体系：
- Guard 安全检查器（4 种权限模式 + Token 级命令分析）
- JS 沙箱隔离（goja）
- OS 沙箱（Landlock / Seatbelt / Low IL）
- .wukongignore 文件黑名单
- SSRF 防护 + API Key 时序安全

---

## 技术栈

| 类别 | 技术 | 版本 |
|------|------|------|
| Agent 框架 | tRPC-Agent-Go | v1.10.0 |
| MCP 协议 | tRPC-MCP-Go | v0.0.16 |
| A2A 协议 | tRPC-A2A-Go | v0.2.5 |
| 记忆引擎 | CortexDB | v2.25.0 |
| CLI 框架 | Cobra | v1.9.1 |
| 配置管理 | Viper | v1.20.1 |
| TUI 框架 | Bubble Tea | v1.3.10 |
| 浏览器自动化 | go-rod | v0.116.2 |
| 浏览器自动化 | chromedp | v0.15.1 |
| 数据库 | SQLite (modernc) | v1.38.2 |
| Redis | go-redis | v9.12.1 |
| 追踪 | OpenTelemetry | v1.43.0 |
| 密码学 | x/crypto | v0.51.0 |
| TLS 指纹 | utls | v1.5.0 |

---

## 系统架构

```
用户输入 → 接入层 (CLI/TUI/Gateway) → CoreLoop 编排引擎
                                           ↓
                        ┌──────────────────┴──────────────────┐
                        │                                      │
                    Prepare 阶段                          Execute 阶段
                  (4重上下文注入)                        (任务执行)
                        │                                      │
                  • ContextRevision                      • Runner
                  • MemoryFlow.WakeUp                    • LLM 调用
                  • Cortex/Recall 搜索                   • 工具调用
                  • Memory 记忆注入                      • 安全检查
                        │                                      │
                        └──────────────────┬──────────────────┘
                                           ↓
                                     Finalize 阶段
                                     (结果处理)
                                           │
                                     • 消息存储
                                     • 事实晋升
                                     • 知识图谱抽取
                                     • 进化记录
                                           ↓
                                     Return 阶段
                                     (返回结果)
```

---

## 核心能力

### 网站克隆引擎

- 双后端浏览器（go-rod / chromedp）+ Settle 网络空闲等待
- 反反爬 5 级升级体系 + 15 项 stealth 注入 + 22 个 WAF 签名识别
- 单遍 DOM 遍历（链接重写 + 资源发现）+ 蜜罐检测
- 断点续抓 + 内容去重（SHA-256 硬链接）+ 增量缓存
- 5 大平台 API 快捷路径 + Wayback Machine 归档回退
- ZIM 打包（Kiwix 兼容）/ Binary / App 跨平台桌面应用

### 扩展系统

17 个内置扩展 + MCP 服务器集成（stdio/sse/streamable）

### 多协议端点

- A2A Server (:9090)
- ACP Server (:9091)
- AG-UI SSE (:8080)
- ACP-MCP Bridge (:3400)
- 独立 MCP Server (:3401)
- ANP Server (:9092)
- Gateway（飞书 WebSocket，无独立 HTTP 端口）

---

## 快速开始

### 安装

```bash
# 从源码构建
go install github.com/km269/wukong/cmd/wukong@latest

# 或下载预编译二进制
# https://github.com/km269/wukong/releases
```

### 配置

```bash
# 交互式配置向导
wukong configure

# 验证配置
wukong config validate
```

### 使用

```bash
# 交互式会话
wukong session

# 单次执行
wukong run --prompt "分析项目结构"

# 网站克隆
wukong apps clone https://example.com --max-pages 50
```

---

## 项目规模

```
语言版本:    Go 1.26
内部包:      35+ 个核心模块
公共包:      4 个可复用组件
CLI 命令:    30 个顶层命令 + 60+ 子命令
依赖:        29 个直接依赖 + 105+ 间接依赖
```

---

## 相关文档

- [系统架构文档](docs/ARCHITECTURE.md)
- [技术实现详解](docs/TECHNICAL_IMPLEMENTATION.md)
- [CLI & TUI 架构](docs/CLI_TUI.md)
- [配置手册](docs/CONFIG.md)
- [开发者指南](docs/DEVELOPER_GUIDE.md)
- [API 参考文档](docs/API_REFERENCE.md)
- [部署运维指南](docs/DEPLOYMENT.md)
- [记忆系统架构](docs/MEMORY_ARCHITECTURE.md)
- [网站克隆技术指南](docs/CLONE_GUIDE.md)
- [Web 操作深度分析](docs/WEB_OPERATIONS_ANALYSIS.md)
- [反反爬技术详解](docs/ANTIBOT_GUIDE.md)
- [OKF 知识格式](docs/OKF_GUIDE.md)

---

**Wukong** — 让 AI Agent 更智能、更安全、更开放
