# Wukong

> **本地优先、可扩展的 AI Agent 平台** | Go 1.26 | tRPC 生态 | CortexDB 智能记忆

Wukong 是一个本地优先的 AI Agent 平台，基于 **[tRPC-Agent-Go](https://github.com/trpc-group/trpc-agent-go)** v1.10.0、**[tRPC-MCP-Go](https://github.com/trpc-group/trpc-mcp-go)** v0.0.16 和 **[CortexDB](https://github.com/liliang-cn/cortexdb)** v2.25.0 构建。提供 CLI 交互体验、50+ 工具、智能双引擎记忆系统、知识图谱、自动任务编排。

## 核心特性

| 特性 | 说明 |
|------|------|
| **7 种 LLM Provider** | OpenAI / Anthropic / Google / DeepSeek / Ollama / LMStudio / ACP |
| **50+ 内置工具** | 文件操作、浏览器自动化、代码执行、记忆、知识图谱 |
| **双引擎记忆** | tRPC Memory (键值) + CortexDB MemoryFlow (转录+唤醒) |
| **知识图谱** | RDF/SPARQL + 实体关系提取 + HNSW 向量搜索 |
| **10 种工作流** | single / sequential / parallel / loop / if / while / map / reduce / human / branch |
| **12 个内置扩展** | 开发工具、计算机控制、记忆、可视化、教程等 |
| **多协议** | ACP / A2A / MCP (stdio/sse/streamable) |
| **技能自进化** | LLM 驱动的技能分析和自动补丁 |

## 文档

| 文档 | 说明 |
|------|------|
| [架构文档](ARCHITECTURE.md) | 系统架构、子系统设计、数据流、ADR |
| [配置手册](CONFIG.md) | 30 个配置段完整参考 |

## 快速开始

```bash
# 安装
go install github.com/km269/wukong/cmd/wukong@latest

# 交互模式
wukong session

# 配置向导
wukong configure
```

## 依赖

| 依赖 | 版本 | 用途 |
|------|------|------|
| tRPC-Agent-Go | v1.10.0 | Agent 框架 |
| tRPC-MCP-Go | v0.0.16 | MCP 协议 |
| CortexDB | v2.25.0 | 智能记忆+知识图谱 |
| BubbleTea | v1.3.10 | 终端 TUI |
| Chromedp | v0.15.1 | 浏览器自动化 |
| Viper + Cobra | v1.20.1 / v1.9.1 | 配置+CLI |

## 许可证

GNU AGPL-3.0
