# OKF (Open Knowledge Format) 技术与使用指南

> **版本**: 0.1 | **参考实现**: [Google OKF Spec](https://github.com/google/open-knowledge-format) | **Wukong 集成**: v0.2.8

---

## 目录

1. [概述](#1-概述)
2. [OKF 规范 v0.1](#2-okf-规范-v01)
   - 2.1 [文件结构](#21-文件结构)
   - 2.2 [Frontmatter 规范](#22-frontmatter-规范)
   - 2.3 [概念标识符](#23-概念标识符)
   - 2.4 [保留文件](#24-保留文件)
   - 2.5 [知识图谱与链接](#25-知识图谱与链接)
   - 2.6 [合规性要求](#26-合规性要求)
   - 2.7 [消费者容忍原则](#27-消费者容忍原则)
3. [核心数据模型](#3-核心数据模型)
   - 3.1 [Frontmatter](#31-frontmatter)
   - 3.2 [Concept](#32-concept)
   - 3.3 [Bundle](#33-bundle)
   - 3.4 [WriteOptions](#34-writeoptions)
4. [核心 API](#4-核心-api)
   - 4.1 [Bundle 加载](#41-bundle-加载)
   - 4.2 [概念解析](#42-概念解析)
   - 4.3 [Bundle 写入](#43-bundle-写入)
   - 4.4 [概念格式化](#44-概念格式化)
   - 4.5 [变更日志](#45-变更日志)
   - 4.6 [辅助方法](#46-辅助方法)
5. [配置](#5-配置)
   - 5.1 [配置项说明](#51-配置项说明)
   - 5.2 [配置示例](#52-配置示例)
   - 5.3 [验证规则](#53-验证规则)
6. [子系统集成](#6-子系统集成)
   - 6.1 [知识库 (knowledge)](#61-知识库-knowledge)
   - 6.2 [Cortex 记忆流 (MemoryFlow)](#62-cortex-记忆流-memoryflow)
   - 6.3 [Cortex 知识富化 (Enrichment)](#63-cortex-知识富化-enrichment)
   - 6.4 [技能系统 (skill)](#64-技能系统-skill)
   - 6.5 [进化系统 (evolution)](#65-进化系统-evolution)
   - 6.6 [ARD 资源发现 (ard)](#66-ard-资源发现-ard)
7. [CLI 命令](#7-cli-命令)
8. [完整示例](#8-完整示例)
   - 8.1 [创建知识 Bundle](#81-创建知识-bundle)
   - 8.2 [从 DDL 自动生成知识](#82-从-ddl-自动生成知识)
   - 8.3 [导入到知识库](#83-导入到知识库)
   - 8.4 [技能 OKF 互操作](#84-技能-okf-互操作)
   - 8.5 [进化系统变更追踪](#85-进化系统变更追踪)
   - 8.6 [ARD 联邦发布](#86-ard-联邦发布)
9. [最佳实践](#9-最佳实践)
10. [FAQ](#10-faq)

---

## 1. 概述

**OKF (Open Knowledge Format)** 是一种**供应商中立**、**AI 代理与人类友好**的开放知识格式标准。它使用 Markdown 文件配合 YAML frontmatter 来表示结构化知识，将每个知识单元抽象为 **"概念 (Concept)"**，通过文件系统路径作为标识符，通过 Markdown 链接形成知识图谱。

在 Wukong 中，OKF 是连接**知识库 (RAG)**、**技能系统 (Skill)**、**Agent 记忆 (MemoryFlow)**、**进化追踪 (Evolution)** 和**联邦发现 (ARD)** 五大模块的统一知识格式。

### 设计目标

| 目标 | 说明 |
|------|------|
| **人类可读** | 纯 Markdown，任何文本编辑器均可编辑 |
| **版本友好** | 纯文本文件，天然适合 Git 版本管理 |
| **AI 可消费** | 结构化 frontmatter 便于程序解析 |
| **可发现** | 文件路径即 ID，目录索引支持渐进式探索 |
| **可组合** | 概念间通过 Markdown 链接形成知识图谱 |
| **可移植** | 目录即 Bundle，复制即可共享 |

---

## 2. OKF 规范 v0.1

### 2.1 文件结构

一个 OKF Bundle 就是一个**目录**，其中包含多个 `.md` 概念文件，以及可选的 `index.md` 和 `log.md`：

```
my-knowledge-bundle/
├── index.md                    # 目录索引（自动生成）
├── log.md                      # 变更历史（自动生成）
├── tables/
│   ├── orders.md               # 概念文件
│   └── customers.md
├── api/
│   ├── checkout.md
│   └── payment.md
└── metrics/
    └── revenue.md
```

### 2.2 Frontmatter 规范

每个概念文件以 `---` 分隔的 YAML frontmatter 开头，后跟 Markdown 正文：

```markdown
---
type: table                          # 必填，概念类型
title: "Orders Table"                # 推荐，人类可读标题
description: "Customer order records" # 推荐，简短描述
resource: "ddl://ecommerce.orders"   # 可选，源资源 URI
tags: [ecommerce, transactional]     # 可选，标签分类
timestamp: 2026-08-02T12:00:00Z      # 推荐，最后修改时间
# 自定义字段（自动保留）
source: "BigQuery"
owner: "data-team"
---

# Orders Table

Contains all customer orders.

## Schema

| Column | Type |
|--------|------|
| id | INT64 |
| customer_id | INT64 |
| total | FLOAT64 |
| created_at | TIMESTAMP |

## Related

- [Customers](tables/customers.md)
```

### 2.3 概念标识符

OKF 使用**文件路径**作为概念的唯一标识符，无需独立的 ID 系统：

| 文件路径 | 概念 ID |
|----------|---------|
| `tables/orders.md` | `tables/orders` |
| `api/checkout.md` | `api/checkout` |
| `metrics/revenue.md` | `metrics/revenue` |

路径分隔符统一使用 `/`（正斜杠），跨平台兼容。

### 2.4 保留文件

| 文件名 | 类型 | 用途 |
|--------|------|------|
| `index.md` | `index` | Bundle 目录索引，列出所有概念按类型分组 |
| `log.md` | `changelog` | 变更历史，记录概念的添加、修改、删除 |

这两个文件由 `WriteBundle` 自动生成，也可手动提供。

### 2.5 知识图谱与链接

概念文件之间的 Markdown 链接形成知识图谱。链接解析基于相对路径：

```markdown
# 在 api/checkout.md 中引用 tables/orders.md
See [Orders](tables/orders.md)

# 在 tables/orders.md 中引用 tables/customers.md
See [Customers](customers.md)
```

链接解析规则：
- `ResolveLink(from, link)` 以 `from` 概念 ID 的目录为基准
- 例如 `ResolveLink("api/checkout", "tables/orders.md")` → `tables/orders`

### 2.6 合规性要求

1. 所有非保留的 `.md` 文件必须包含可解析的 YAML frontmatter
2. 每个 frontmatter 必须包含非空的 `type` 字段
3. `index.md` 和 `log.md`（如果存在）必须遵循其预定义结构

### 2.7 消费者容忍原则

OKF 规范要求消费者（Consumer）必须容忍以下情况：

| 容忍项 | 处理方式 |
|--------|----------|
| 未知的 `type` 值 | 正常加载，不报错 |
| 缺少可选字段 | 使用默认值 |
| 损坏的跨文件链接 | 忽略坏链接，不中断加载 |
| 单个不合规文件 | 跳过该文件，不影响整个 Bundle |

Wukong 的扩展容忍：
- 无 `type` 字段的文件 → 默认 `type: concept`
- 无 frontmatter 的文件 → 整个内容作为正文
- 这确保了 SKILL.md 等已有文件可以无需修改即可加载

---

## 3. 核心数据模型

### 3.1 Frontmatter

```go
type Frontmatter struct {
    Type        string            // 必填，概念类型（如 table, api, skill, document）
    Title       string            // 推荐，人类可读标题
    Description string            // 推荐，简短描述
    Resource    string            // 可选，源资源 URI
    Tags        []string          // 可选，标签
    Timestamp   string            // 推荐，ISO 8601/RFC 3339 格式
    Extra       map[string]any    // 自定义字段，round-trip 保留
}
```

### 3.2 Concept

```go
type Concept struct {
    ID          string      // 概念标识符，从文件路径派生（不含 .md 后缀）
    FilePath    string      // 相对路径（含 .md 后缀）
    Frontmatter Frontmatter // YAML 元数据
    Body        string      // Markdown 正文
    Links       []string    // 正文中提取的 Markdown 链接（指向 .md 文件）
}
```

### 3.3 Bundle

```go
type Bundle struct {
    RootDir  string     // Bundle 根目录的绝对路径
    Concepts []*Concept // 概念文件列表（不含 index.md 和 log.md）
    Index    *Concept   // 解析后的 index.md（可为 nil）
    Log      *Concept   // 解析后的 log.md（可为 nil）
    Version  string     // 从 index.md 提取的 OKF 版本号
}
```

### 3.4 WriteOptions

```go
type WriteOptions struct {
    CompressFrontmatter bool  // 压缩空可选字段（默认 true）
    GenerateIndex       bool  // 自动生成 index.md（默认 true）
    GenerateLog         bool  // 自动生成 log.md（默认 true）
    OKFVersion          string // 版本号（默认 "0.1"）
}
```

---

## 4. 核心 API

### 4.1 Bundle 加载

```go
func LoadBundle(rootDir string) (*Bundle, []string)
```

加载一个 OKF Bundle 目录，返回 Bundle 和一系列非致命警告。不合规文件被跳过（不中断加载）。

```go
bundle, warnings := okf.LoadBundle("./my-knowledge")
if len(warnings) > 0 {
    for _, w := range warnings {
        log.Printf("WARNING: %s", w)
    }
}
fmt.Printf("Loaded %d concepts\n", len(bundle.Concepts))
```

### 4.2 概念解析

```go
func ParseConcept(content []byte, relPath string) (*Concept, error)
```

解析单个 `.md` 文件内容为 Concept。`relPath` 用于派生概念 ID。

```go
content, _ := os.ReadFile("tables/orders.md")
concept, err := okf.ParseConcept(content, "tables/orders.md")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Type: %s, Title: %s\n", concept.Frontmatter.Type, concept.Frontmatter.Title)
```

### 4.3 Bundle 写入

```go
func WriteBundle(bundle *Bundle, outputDir string, opts WriteOptions) error
```

将 Bundle 写入磁盘。自动创建目录、自动生成 `index.md` 和 `log.md`。

```go
opts := okf.DefaultWriteOptions()
err := okf.WriteBundle(bundle, "./output", opts)
```

### 4.4 概念格式化

```go
func FormatConcept(concept *Concept, compress bool) string
```

将 Concept 渲染为完整的 `.md` 文件字符串（YAML frontmatter + Markdown 正文）。

### 4.5 变更日志

```go
func AppendLogEntry(bundleDir, action, filePath, reason string) error
```

向 OKF Bundle 的 `log.md` 追加变更条目。`action` 可选值：`Added`、`Modified`、`Removed`。

```go
okf.AppendLogEntry("./my-knowledge", "Added", "tables/orders.md", "initial import")
```

### 4.6 辅助方法

```go
func (b *Bundle) FindConcept(id string) *Concept         // 按 ID 查找概念
func (b *Bundle) ConceptsByType(typeName string) []*Concept // 按类型过滤
func (b *Bundle) AllTypes() []string                      // 获取所有类型
func ResolveLink(from, link string) string                // 解析链接到概念 ID
func FormatNow() string                                   // RFC 3339 时间戳
```

---

## 5. 配置

### 5.1 配置项说明

在 `config.yaml` 中通过 `okf` 小节配置：

| 配置项 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `okf.enabled` | bool | `false` | 是否启用 OKF 系统 |
| `okf.bundle_dir` | string | `.wukong/okf` | OKF Bundle 目录路径 |
| `okf.injector_enabled` | bool | `false` | 是否启用 Agent 唤醒上下文知识注入 |
| `okf.enrichment_enabled` | bool | `false` | 是否启用 LLM 驱动的知识自动富化 |
| `okf.enrichment_output_dir` | string | `""` | 富化输出目录（为空则使用 bundle_dir） |
| `okf.auto_export` | bool | `false` | 是否在 Agent 对话后自动导出知识到 OKF |
| `okf.register_in_ard` | bool | `false` | 是否将 Bundle 注册到 ARD 联邦发现目录 |

### 5.2 配置示例

```yaml
okf:
  enabled: true
  bundle_dir: ".wukong/okf"              # 默认路径
  injector_enabled: true                 # 启用知识注入到 Agent 上下文
  enrichment_enabled: true               # 启用自动知识富化
  enrichment_output_dir: ".wukong/okf"   # 富化输出路径
  auto_export: true                      # 自动导出
  register_in_ard: true                  # 注册到 ARD 发现目录
```

### 5.3 验证规则

| 条件 | 类型 | 信息 |
|------|------|------|
| `okf.enabled` 为 true 但 `bundle_dir` 为空 | 警告 | 将使用默认 `.wukong/okf` |
| `okf.injector_enabled` 为 true 但 `memoryflow.enabled` 为 false | 警告 | 注入无 MemoryFlow 无效 |
| `okf.enrichment_enabled` 为 true 但 `default_provider` 为空 | 警告 | 将使用确定性回退 |

---

## 6. 子系统集成

### 6.1 知识库 (knowledge)

**文件**: [internal/knowledge/okf.go](file:///e:/myVibeCoding/km269/wukong/internal/knowledge/okf.go)

将 OKF Bundle 导入到 RAG 知识库，或从知识库导出为 OKF Bundle。

**ImportBundle** 将 Bundle 目录中的每个概念文件注册为可搜索文档：
- Frontmatter 字段 → 文档元数据
- Markdown 正文 → 可搜索内容
- 文件路径 → 文档 ID
- 跨文件链接保留为知识图谱发现

**ExportBundle** 将知识库内容导出为 OKF Bundle：
- 从配置的知识源目录扫描
- 已 OKF 合规的文件保持原样
- 非 OKF 文件自动包装 frontmatter

```go
// 导入 OKF Bundle 到知识库
kb.ImportBundle("./my-knowledge")

// 导出知识库为 OKF Bundle
kb.ExportBundle("./output")
```

### 6.2 Cortex 记忆流 (MemoryFlow)

**文件**: [internal/cortex/okf_injector.go](file:///e:/myVibeCoding/km269/wukong/internal/cortex/okf_injector.go)

**KnowledgeIndexInjector** 在 Agent 唤醒上下文 (WakeUp Context) 中注入 OKF 知识概览。实现 **渐进式探索** 模式：Agent 先看到目录索引，再根据需要深入具体概念。

注入的内容格式：
```
[Available Knowledge]
This knowledge base contains 12 concepts across 3 types:
- table: 7 concept(s)
- api: 3 concept(s)
- document: 2 concept(s)
Index: <index.md 正文前 500 字符>
```

```go
injector := cortex.NewKnowledgeIndexInjector(".wukong/okf")

// 在 MemoryFlow 中包装 WakeUp
wakeCtx, err := memoryFlow.WakeUpWithKnowledgeIndex(
    ctx, identity, query, sessionID, userID, injector)
```

特性：
- **缓存**：基于文件修改时间，避免重复读取
- **回退**：无 `index.md` 时自动从 Bundle 生成摘要
- **无侵入**：未配置 Bundle 时返回原始上下文

### 6.3 Cortex 知识富化 (Enrichment)

**文件**: [internal/cortex/okf_enrichment.go](file:///e:/myVibeCoding/km269/wukong/internal/cortex/okf_enrichment.go)

**EnrichmentAgent** 从结构化数据源自动生成 OKF 概念文档，使用 LLM 驱动描述生成。

**EnrichFromDDL** — 从 SQL DDL 生成表概念文档：

```go
agent := cortex.NewEnrichmentAgent(llm, "./output")
count, err := agent.EnrichFromDDL(ctx, ddlString)
// 生成 tables/orders.md, tables/customers.md 等
```

生成的每个概念（`tables/orders.md`）：
```markdown
---
type: table
title: "orders"
description: "Database table orders with 5 columns"
resource: "ddl://orders"
tags: [database, table]
timestamp: 2026-08-02T12:00:00Z
---

# orders

## Schema

| Column | Type |
|--------|------|
| id | INT64 |
| customer_id | INT64 |
| total | FLOAT64 |
| created_at | TIMESTAMP |

## Foreign Keys

- customer_id -> [customers](tables/customers.md)
```

**EnrichFromDirectory** — 从目录扫描文件生成概念文档：

```go
count, err := agent.EnrichFromDirectory(ctx, "./source-docs")
// 支持 .md, .txt, .json, .yaml, .csv, .html, .xml
```

### 6.4 技能系统 (skill)

**文件**: [internal/skill/okf.go](file:///e:/myVibeCoding/km269/wukong/internal/skill/okf.go)

SKILL.md 文件在结构上天然与 OKF 兼容，只需添加 `type: skill` 字段。

**EnsureOKFType** — 确保 SKILL.md 包含 `type: skill`：

```go
changed, err := skill.EnsureOKFType("skills/code-reviewer/SKILL.md")
// 无 frontmatter → 自动添加
// 有 frontmatter 但无 type → 添加 type: skill
// 已有 type → 跳过
```

**ExportSkillsAsOKF** — 导出所有技能为 OKF Bundle：

```bash
wukong skill export-okf ./skills-bundle
```

输出结构：
```
skills-bundle/
├── index.md
├── log.md
└── skills/
    └── code-reviewer/
        └── SKILL.md
```

**ImportOKFSkills** — 从 OKF Bundle 导入技能（只导入 `type: skill` 的概念）：

```bash
wukong skill import-okf ./skills-bundle
```

**EnsureAllOKFCompliant** — 批量确保所有技能 OKF 合规：

```bash
wukong skill ensure-okf
```

### 6.5 进化系统 (evolution)

**文件**: [internal/evolution/okf.go](file:///e:/myVibeCoding/km269/wukong/internal/evolution/okf.go)

当技能自动进化时，变更记录自动写入 OKF Bundle 的 `log.md`。

```go
// 记录技能补丁
evolution.RecordSkillPatchAsKnowledge(
    bundleDir, "code-reviewer",
    "improved prompt clarity",
    1, 2,
)
// log.md 中追加:
// - Patched: skills/code-reviewer/SKILL.md (patched v1->v2: improved prompt clarity)
```

生成的 `log.md`：
```markdown
---
type: changelog
---

# Knowledge Bundle Changelog

## 2026-08-02

- Created: skills/code-reviewer/SKILL.md (initial creation)
- Patched: skills/code-reviewer/SKILL.md (patched v1->v2: improved prompt clarity)
- Added: tables/orders.md (imported from DDL)

## 2026-08-01

- Modified: api/checkout.md (updated schema)
```

查询变更历史：
```go
// 获取所有变更
changes, _ := evolution.GetChangeHistory(bundleDir)

// 获取最近 7 天变更
recent, _ := evolution.GetRecentChanges(bundleDir, 7)
```

### 6.6 ARD 资源发现 (ard)

**文件**: [internal/ard/okf.go](file:///e:/myVibeCoding/km269/wukong/internal/ard/okf.go)

OKF Bundle 可以注册到 ARD (Agentic Resource Discovery) 目录，实现联邦知识发现。

**Media Type**: `application/okf-bundle+json`
**URN 格式**: `urn:air:<publisher>:knowledge:<bundle-name>`

```go
// 创建 OKF Bundle 目录条目
entry := ard.NewOKFBundleEntry(
    "my-knowledge",           // 名称
    "My Knowledge Bundle",     // 显示名
    "业务知识库",              // 描述
    "https://github.com/org/repo", // Git URL
    []string{"knowledge", "business"}, // 标签
    ard.OKFBundleMetadata{
        OKFVersion:   "0.1",
        ConceptCount: 12,
        ConceptTypes: []string{"table", "api", "document"},
    },
)

// 注册到 ARD 目录
ts.Register(entry)

// 搜索 OKF Bundle
results := ard.SearchOKFBundles(ts, "knowledge")
```

---

## 7. CLI 命令

| 命令 | 功能 | 文件 |
|------|------|------|
| `wukong knowledge import <path>` | 导入 OKF Bundle 到知识库 | [knowledge/okf.go](file:///e:/myVibeCoding/km269/wukong/internal/knowledge/okf.go) |
| `wukong knowledge export <path>` | 导出知识库为 OKF Bundle | 同上 |
| `wukong skill ensure-okf` | 批量添加 `type: skill` 到 SKILL.md | [skill/okf.go](file:///e:/myVibeCoding/km269/wukong/internal/skill/okf.go) |
| `wukong skill export-okf <path>` | 导出技能为 OKF Bundle | 同上 |
| `wukong skill import-okf <path>` | 从 OKF Bundle 导入技能 | 同上 |
| `wukong evolution log [--json]` | 查看 OKF 变更日志 | [evolution/okf.go](file:///e:/myVibeCoding/km269/wukong/internal/evolution/okf.go) |

---

## 8. 完整示例

### 8.1 创建知识 Bundle

```go
package main

import (
    "fmt"
    "github.com/km269/wukong/internal/okf"
)

func main() {
    // 构建概念列表
    orders := &okf.Concept{
        ID:       "tables/orders",
        FilePath: "tables/orders.md",
        Frontmatter: okf.Frontmatter{
            Type:        "table",
            Title:       "Orders",
            Description: "订单表，包含所有客户订单",
            Resource:    "ddl://ecommerce.orders",
            Tags:        []string{"ecommerce", "transactional"},
            Timestamp:   okf.FormatNow(),
        },
        Body: `# Orders

## Schema

| Column | Type |
|--------|------|
| id | INT64 |
| customer_id | INT64 |
| total | FLOAT64 |

See [Customers](customers.md)
`,
    }

    customers := &okf.Concept{
        ID:       "tables/customers",
        FilePath: "tables/customers.md",
        Frontmatter: okf.Frontmatter{
            Type:        "table",
            Title:       "Customers",
            Description: "客户表",
            Resource:    "ddl://ecommerce.customers",
            Tags:        []string{"ecommerce"},
            Timestamp:   okf.FormatNow(),
        },
        Body: `# Customers

## Schema

| Column | Type |
|--------|------|
| id | INT64 |
| name | STRING |
| email | STRING |
`,
    }

    // 组装 Bundle
    bundle := &okf.Bundle{
        RootDir:  "./my-knowledge",
        Concepts: []*okf.Concept{orders, customers},
    }

    // 写入磁盘
    opts := okf.DefaultWriteOptions()
    if err := okf.WriteBundle(bundle, "./my-knowledge", opts); err != nil {
        panic(err)
    }

    fmt.Println("OKF Bundle created!")
    // 输出:
    //   ./my-knowledge/
    //   ├── index.md  (自动生成)
    //   ├── log.md    (自动生成)
    //   └── tables/
    //       ├── orders.md
    //       └── customers.md
}
```

### 8.2 从 DDL 自动生成知识

```go
// 假设有解析器提供 DDL 表结构
ddl := `
CREATE TABLE orders (
    id INT64 NOT NULL,
    customer_id INT64 NOT NULL,
    total FLOAT64,
    created_at TIMESTAMP,
    FOREIGN KEY (customer_id) REFERENCES customers(id)
);

CREATE TABLE customers (
    id INT64 NOT NULL,
    name STRING,
    email STRING
);
`

agent := cortex.NewEnrichmentAgent(llm, "./my-knowledge")
count, err := agent.EnrichFromDDL(ctx, ddl)
// 生成:
//   my-knowledge/tables/orders.md
//   my-knowledge/tables/customers.md
// 含外键引用: orders.md -> [customers](tables/customers.md)
```

### 8.3 导入到知识库

```yaml
# config.yaml
okf:
  enabled: true
  bundle_dir: ".wukong/okf"
  injector_enabled: true
  enrichment_enabled: true
  enrichment_output_dir: ".wukong/okf/enrichment"
  register_in_ard: true
```

```bash
# 导入 OKF Bundle 到知识库
wukong knowledge import .wukong/okf

# 导出知识库为 OKF Bundle
wukong knowledge export .wukong/okf/export
```

### 8.4 技能 OKF 互操作

```bash
# 1. 确保所有技能 OKF 合规
wukong skill ensure-okf
# 输出: "ensured OKF type compliance: modified_count=3"

# 2. 导出技能为 OKF Bundle
wukong skill export-okf ./skills-bundle

# 3. 从 OKF Bundle 导入技能
wukong skill import-okf ./skills-bundle
```

### 8.5 进化系统变更追踪

```go
// 创建技能补丁时自动记录变更
evolution.RecordSkillPatchAsKnowledge(
    ".wukong/okf",
    "code-reviewer",
    "improved code analysis prompt",
    1, 2,
)

// 记录知识库变更
evolution.RecordKnowledgeChange(".wukong/okf", evolution.KnowledgeChange{
    Action:   evolution.ChangeAdded,
    FilePath: "tables/orders.md",
    Reason:   "imported from DDL",
})

// 查询最近变更
changes, _ := evolution.GetRecentChanges(".wukong/okf", 7)
for _, c := range changes {
    fmt.Printf("[%s] %s: %s (%s)\n",
        c.Timestamp, c.Action, c.FilePath, c.Reason)
}
```

### 8.6 ARD 联邦发布

```go
// 将 OKF Bundle 注册到 ARD 目录
bundle, _ := okf.LoadBundle(".wukong/okf")
ard.RegisterOKFBundle(
    ardTS,
    "enterprise-knowledge",
    "Enterprise Knowledge Base",
    "企业业务知识库，包含数据库表、API 和指标定义",
    "https://git.company.com/knowledge/enterprise",
    []string{"knowledge", "enterprise", "business"},
    ard.OKFBundleMetadata{
        OKFVersion:   okf.OKFVersion,
        ConceptCount: len(bundle.Concepts),
        ConceptTypes: bundle.AllTypes(),
    },
)

// 远程 Agent 搜索发现
results := ard.SearchOKFBundles(ardTS, "enterprise")
for _, entry := range results {
    meta := ard.ExtractOKFMetadata(&entry)
    fmt.Printf("Found: %s (%d concepts)\n",
        entry.DisplayName, meta.ConceptCount)
}
```

---

## 9. 最佳实践

### 9.1 目录组织

```
bundle-root/
├── tables/       # 数据库表定义
├── api/          # API 端点文档
├── metrics/      # 业务指标
├── runbooks/     # 操作手册
├── skills/       # AI 技能（通过 skill export-okf 生成）
└── urls/         # 从 URL 导入的知识
```

### 9.2 类型命名约定

| 类型 | 用途 | 示例 |
|------|------|------|
| `table` | 数据库表 | `tables/orders.md` |
| `api` | API 端点 | `api/checkout.md` |
| `metric` | 业务指标 | `metrics/revenue.md` |
| `runbook` | 操作手册 | `runbooks/incident.md` |
| `skill` | AI 技能定义 | `skills/code-reviewer/SKILL.md` |
| `document` | 通用文档 | 导入的外部文件 |
| `web-doc` | 网页文档 | 从 URL 导入 |
| `concept` | 默认类型 | 无 type 字段的文件 |

### 9.3 文件命名

- 使用小写字母 + 连字符：`order-history.md` 而非 `OrderHistory.md`
- 使用路径反映分类：`tables/orders.md` 而非 `orders-table.md`
- 避免特殊字符和空格

### 9.4 链接最佳实践

- 始终使用相对路径链接：`[Customers](tables/customers.md)`
- 保持链接双向性：如果 A 引用 B，B 也应引用 A
- 不要链接到外部 `.md` 文件（链接解析只处理 Bundle 内部）

### 9.5 性能考虑

- `LoadBundle` 递归扫描整个目录，对大型 Bundle 可能较慢
- `KnowledgeIndexInjector` 缓存 index.md 内容，基于文件修改时间失效
- 建议将大型 Bundle 拆分为多个小 Bundle

### 9.6 版本控制

OKF Bundle 目录天然适合 Git 管理：

```bash
git add my-knowledge/
git commit -m "docs: update knowledge bundle"
```

`log.md` 自动追踪变更，配合 `evolution` 系统可查看完整的知识演进历史。

---

## 10. FAQ

### Q: OKF 和普通 Markdown 文件有什么区别？

A: OKF 要求每个 `.md` 文件包含 YAML frontmatter（至少 `type` 字段），而普通 Markdown 没有此要求。OKF 的 frontmatter 提供了结构化元数据，使程序可以理解知识类型、来源、标签等语义信息。

### Q: 没有 frontmatter 的文件会怎样？

A: 根据 OKF 消费者容忍原则，无 frontmatter 的文件会被跳过（加载时记录警告）。但 Wukong 的扩展容忍会将整个文件内容作为正文，`type` 默认为 `concept`。

### Q: SKILL.md 已经是 OKF 兼容的吗？

A: SKILL.md 文件结构上兼容（YAML frontmatter + Markdown 正文），但可能缺少 `type` 字段。运行 `wukong skill ensure-okf` 会自动添加 `type: skill` 使其完全合规。

### Q: 如何让 Agent 在对话中自动使用 OKF 知识？

A: 配置 `okf.injector_enabled: true` 和 `memoryflow.enabled: true`，KnowledgeIndexInjector 会在 Agent 每次唤醒时将知识概览注入上下文。Agent 通过 `cortex` 工具搜索具体概念。

### Q: OKF Bundle 可以跨网络共享吗？

A: 可以。通过 ARD 注册后，远程 Agent 可以通过联邦搜索发现 Bundle。Bundle 本身是纯文件目录，可以通过 Git、HTTP、S3 等任何方式分发。

### Q: 如何在代码中手动构建一个 Concept？

A:

```go
concept := &okf.Concept{
    ID:       "api/checkout",
    FilePath: "api/checkout.md",
    Frontmatter: okf.Frontmatter{
        Type:        "api",
        Title:       "Checkout API",
        Description: "结账 API 端点",
        Resource:    "https://api.example.com/checkout",
        Tags:        []string{"api", "checkout"},
        Timestamp:   okf.FormatNow(),
    },
    Body: "# Checkout API\n\nPOST /api/checkout\n\n...",
}
```

### Q: `log.md` 的格式是什么？

A: `log.md` 使用以下格式：

```markdown
---
type: changelog
---

# Knowledge Bundle Changelog

## 2026-08-02

- Added: tables/orders.md (initial import)
- Patched: skills/code-reviewer/SKILL.md (patched v1->v2: improved prompt)
- Modified: api/checkout.md (updated schema)

## 2026-08-01

- Removed: metrics/old-metric.md (deprecated)
```

### Q: 如何从 Bundle 中查找特定概念？

A:

```go
// 按 ID 查找
concept := bundle.FindConcept("tables/orders")

// 按类型过滤
tables := bundle.ConceptsByType("table")

// 获取所有类型
types := bundle.AllTypes()
```

### Q: 环境变量在 OKF 配置中如何使用？

A: 使用 `${ENV_VAR}` 格式：

```yaml
okf:
  bundle_dir: "${WUKONG_OKF_DIR:-.wukong/okf}"
  enrichment_output_dir: "${WUKONG_OKF_ENRICHMENT_DIR}"
```

---

> **参考**: [internal/okf/](file:///e:/myVibeCoding/km269/wukong/internal/okf/) — 核心包 | [internal/knowledge/okf.go](file:///e:/myVibeCoding/km269/wukong/internal/knowledge/okf.go) — 知识库集成 | [internal/cortex/okf_injector.go](file:///e:/myVibeCoding/km269/wukong/internal/cortex/okf_injector.go) — 注入器 | [internal/cortex/okf_enrichment.go](file:///e:/myVibeCoding/km269/wukong/internal/cortex/okf_enrichment.go) — 富化 | [internal/skill/okf.go](file:///e:/myVibeCoding/km269/wukong/internal/skill/okf.go) — 技能集成 | [internal/evolution/okf.go](file:///e:/myVibeCoding/km269/wukong/internal/evolution/okf.go) — 进化追踪 | [internal/ard/okf.go](file:///e:/myVibeCoding/km269/wukong/internal/ard/okf.go) — ARD 发现