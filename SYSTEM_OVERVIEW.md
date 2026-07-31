# Wukong 系统概述

> **记忆优先 · 编排驱动 · 安全纵深 · 双向发现**
>
> 本地优先、框架组装、可深度扩展的开源 AI Agent 平台

---

## 项目简介

**Wukong (悟空)** 是一个基于 Go 语言构建的新一代 AI Agent 平台，名字取自中国神话中的齐天大圣孙悟空，寓意着智能、灵活和强大的能力。

Wukong 不仅仅是一个简单的 AI 聊天机器人，而是一个**本地优先、记忆驱动、多模式编排**的完整 AI Agent 开发框架。

---

## 核心价值

### 1. 记忆优先的智能系统

双引擎三层记忆系统，让 Agent 具备跨会话的知识积累能力：

- **短期记忆**: MemoryFlow 会话级别转录和 3 层唤醒
- **中期记忆**: CortexStore HNSW 向量 + FTS5 全文检索
- **长期记忆**: tRPC Memory 自动提取和智能清理
- **结构化记忆**: GraphFlow 知识图谱构建

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

LLM 驱动的自我学习和优化机制，让 Agent 能够从失败中学习并持续改进。

### 4. 双向发现与开放互通

ARD 协议实现 Agent 的双向发现，支持 MCP、A2A、ANP 等开放协议。

### 5. 深度安全防御

5 层纵深防御体系：
- Guard 安全检查器
- JS 沙箱隔离
- OS 沙箱（Landlock/Seatbelt/Low IL）
- .wukongignore 文件黑名单
- OS 权限控制

---

## 技术栈

| 类别 | 技术 | 版本 |
|------|------|------|
| Agent 框架 | tRPC-Agent-Go | v1.10.0 |
| MCP 协议 | tRPC-MCP-Go | v0.0.16 |
| A2A 协议 | tRPC-A2A-Go | v0.2.5 |
| 记忆引擎 | CortexDB | v2.25.0 |
| CLI 框架 | Cobra | v1.9.1 |
| TUI 框架 | Bubble Tea | v1.3.10 |
| 浏览器自动化 | chromedp / rod | v0.15.1 |

---

## 系统架构

```
用户输入 → 接入层 (CLI/TUI/Gateway) → CoreLoop 编排引擎
                                           ↓
                        ┌──────────────────┴──────────────────┐
                        │                                      │
                    Prepare 阶段                          Execute 阶段
                  (上下文准备)                           (任务执行)
                        │                                      │
                  • MemoryFlow                           • Runner
                  • Cortex 搜索                           • LLM 调用
                  • 持久记忆注入                          • 工具调用
                  • 知识注入                              • 安全检查
                        │                                      │
                        └──────────────────┬──────────────────┘
                                           ↓
                                     Finalize 阶段
                                     (结果处理)
                                           │
                                     • 消息存储
                                     • 事实提升
                                     • 进化记录
                                           ↓
                                     Return 阶段
                                     (返回结果)
```

---

## 核心能力

### 网站克隆引擎

- Chrome 渲染 + Settle 等待
- 反反爬 10 层防御
- 断点续抓和内容去重
- ZIM 打包 (Kiwix 兼容)

### 扩展系统

13 个内置扩展 + MCP 服务器集成

### 多协议端点

- A2A Server (:9090)
- ACP Server (:9091)
- AG-UI SSE (:8080)
- ANP Server (:9092)
- Gateway (:9093)

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
内部包:      30+ 个核心模块
公共包:      4 个可复用组件
CLI 命令:    29 个顶层命令 + 60+ 子命令
依赖:        29 个直接依赖 + 105+ 间接依赖
```

---

## 相关文档

- [技术架构文档](docs/ARCHITECTURE.md)
- [技术实现详解](docs/TECHNICAL_IMPLEMENTATION.md)
- [开发者指南](docs/DEVELOPER_GUIDE.md)
- [API 参考文档](docs/API_REFERENCE.md)
- [部署运维指南](docs/DEPLOYMENT.md)

---

**Wukong** — 让 AI Agent 更智能、更安全、更开放
