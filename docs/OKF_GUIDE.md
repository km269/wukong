# OKF (Open Knowledge Format) 技术与使用指南

> 本指南基于 `internal/okf/bundle.go`、`internal/okf/writer.go`、`internal/knowledge/okf.go`、`internal/cortex/okf_injector.go`、`internal/cortex/okf_enrichment.go`、`internal/skill/`、`internal/evolution/okf.go`、`internal/ard/okf.go` 逐文件精读后产出，涵盖 OKF v0.1 规范、核心解析引擎、Writer 管线、7 大子系统集成。

---

## 目录

1. [概述](#1-概述)
2. [OKF v0.1 规范](#2-okf-v01-规范)
3. [核心数据模型](#3-核心数据模型)
4. [Bundle 加载引擎](#4-bundle-加载引擎)
5. [Bundle 写入引擎（Writer）](#5-bundle-写入引擎writer)
6. [7 大子系统集成](#6-7-大子系统集成)
7. [配置](#7-配置)
8. [CLI 命令](#8-cli-命令)
9. [完整示例](#9-完整示例)
10. [最佳实践](#10-最佳实践)
11. [FAQ](#11-faq)

---

## 1. 概述

**OKF (Open Knowledge Format)** 是一种**供应商中立**、**AI 代理与人类友好**的开放知识格式标准。它使用 Markdown 文件配合 YAML frontmatter 来表示结构化知识，将每个知识单元抽象为 **"概念 (Concept)"**，通过文件系统路径作为标识符，通过 Markdown 链接形成知识图谱。

在 Wukong 中，OKF 是连接以下 **7 个子系统** 的统一知识格式：

```
                         ┌──────────────────────────┐
                         │   OKF Bundle (目录)       │
                         │   .md files + frontmatter │
                         └───────────┬──────────────┘
                                     │
           ┌────────────┬────────────┼────────────┬────────────┬────────────┐
           ▼            ▼            ▼            ▼            ▼            ▼
    ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐
    │ ① OKF    │ │ ② Skill  │ │ ③ 知识库 │ │ ④ 注入器 │ │ ⑤ 进化   │ │ ⑥ ARD    │
    │  核心    │ │  兼容    │ │  互操作  │ │  Cortex  │ │  追踪    │ │  联邦    │
    │  解析    │ │          │ │  RAG    │ │  Memory  │ │  log.md  │ │  发现    │
    │  引擎    │ │SKILL.md  │ │Import/  │ │  Flow    │ │  变更    │ │  urn:air │
    │bundle.go │ │type:skill│ │Export   │ │  注入    │ │  历史    │ │  注册    │
    └──────────┘ └──────────┘ └──────────┘ └──────────┘ └──────────┘ └──────────┘
                                                                │
                                                         ┌──────┴──────┐
                                                         │ ⑦ 富化代理  │
                                                         │ DDL→概念    │
                                                         │ 目录→概念   │
                                                         └─────────────┘
```

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

## 2. OKF v0.1 规范

### 2.1 常量定义（`internal/okf/bundle.go:42-56`）

```go
const OKFVersion  = "0.1"       // 当前支持的规范版本
const IndexFile   = "index.md"  // 保留文件：目录索引
const LogFile     = "log.md"    // 保留文件：变更历史
const DefaultType = "concept"   // 缺省类型（向后兼容 SKILL.md）
```

### 2.2 文件结构

一个 OKF Bundle 就是一个**目录**，递归包含多个 `.md` 概念文件，以及可选的 `index.md` 和 `log.md`：

```
my-knowledge-bundle/
├── index.md                    # 目录索引（自动生成，type: index）
├── log.md                      # 变更历史（自动生成，type: changelog）
├── tables/
│   ├── orders.md               # 概念文件（type: table）
│   └── customers.md
├── api/
│   ├── checkout.md             # 概念文件（type: api）
│   └── payment.md
└── metrics/
    └── revenue.md
```

### 2.3 Frontmatter 规范

每个概念文件以 `---` 分隔的 YAML frontmatter 开头，后跟 Markdown 正文：

```markdown
---
type: table                          # 必填，概念类型（唯一强制字段）
title: "Orders Table"                # 推荐，人类可读标题
description: "Customer order records" # 推荐，简短描述
resource: "ddl://ecommerce.orders"   # 可选，源资源 URI
tags: [ecommerce, transactional]     # 可选，标签分类
timestamp: 2026-08-02T12:00:00Z      # 推荐，最后修改时间（RFC 3339）
# 自定义字段（通过 Extra map 保留，yaml:",inline"）
source: "BigQuery"
owner: "data-team"
---

# Orders Table

Contains all customer orders.
```

### 2.4 Frontmatter 结构体

```go
// bundle.go:61-86
type Frontmatter struct {
    Type        string         `yaml:"type"`           // 必填
    Title       string         `yaml:"title,omitempty"`
    Description string         `yaml:"description,omitempty"`
    Resource    string         `yaml:"resource,omitempty"`
    Tags        []string       `yaml:"tags,omitempty"`
    Timestamp   string         `yaml:"timestamp,omitempty"`
    Extra       map[string]any `yaml:",inline"`        // 关键：保留所有未知字段
}
```

> **`yaml:",inline"` 是 OKF 规范消费者容错要求的关键实现**：任何未知的 frontmatter 字段（如 `source`、`owner`、自定义元数据）自动收集到 `Extra` map 中，在写入时原样输出，保证 round-trip 不丢失数据。

### 2.5 概念标识符

OKF 使用**文件路径**作为概念的唯一标识符，无需独立的 ID 系统：

| 文件路径 | 概念 ID |
|----------|---------|
| `tables/orders.md` | `tables/orders` |
| `api/checkout.md` | `api/checkout` |

ID 派生规则（`ParseConcept`，`bundle.go:246-249`）：去掉 `.md` 后缀 + `filepath.ToSlash()` 统一路径分隔符，跨平台一致。

### 2.6 保留文件

| 文件名 | frontmatter type | 用途 |
|--------|-----------------|------|
| `index.md` | `index` | Bundle 目录索引，列出所有概念按类型分组 |
| `log.md` | `changelog` | 变更历史，记录概念的添加、修改、删除 |

### 2.7 合规性要求（OKF spec v0.1）

1. 所有非保留的 `.md` 文件必须包含可解析的 YAML frontmatter
2. 每个 frontmatter 必须包含非空的 `type` 字段
3. `index.md` 和 `log.md`（如果存在）必须遵循其预定义结构

### 2.8 消费者容忍原则

OKF 规范要求消费者（Consumer）**必须容忍**以下情况：

| 容忍项 | 处理方式 | 实现位置 |
|--------|----------|----------|
| 未知的 `type` 值 | 正常加载，不报错 | `LoadBundle` 不校验 type 值 |
| 缺少可选字段 | 使用默认值 | `Frontmatter` 字段 omitempty |
| 损坏的跨文件链接 | 忽略坏链接，不中断加载 | `extractLinks` 只收集不验证 |
| 单个不合规文件 | 跳过该文件，不影响整个 Bundle | `loadDir` append warning 后 continue |
| **缺少 `type` 字段** | 默认 `type: concept`（`DefaultType`） | `ParseConcept:242-244` |
| **缺少 frontmatter** | 整个内容作为正文 | `splitFrontmatter:271-277` |

最后两项是 Wukong 的**扩展容忍**——确保 SKILL.md 等已有文件无需修改即可加载。

---

## 3. 核心数据模型

### 3.1 Concept（`bundle.go:90-110`）

```go
type Concept struct {
    ID          string      // 概念标识符：文件路径减去 .md，如 "tables/orders"
    FilePath    string      // 相对路径（含 .md），如 "tables/orders.md"
    Frontmatter Frontmatter // 解析后的 YAML 元数据
    Body        string      // frontmatter 之后的 Markdown 正文
    Links       []string    // 从正文中提取的 Markdown 链接（指向 .md 文件）
}
```

### 3.2 Bundle（`bundle.go:114-130`）

```go
type Bundle struct {
    RootDir  string     // Bundle 根目录路径
    Concepts []*Concept // 概念文件列表（不含 index.md 和 log.md）
    Index    *Concept   // 解析后的 index.md（可为 nil）
    Log      *Concept   // 解析后的 log.md（可为 nil）
    Version  string     // 从 index.md frontmatter 的 okf_version 字段提取
}
```

---

## 4. Bundle 加载引擎

### 4.1 LoadBundle 递归加载（`bundle.go:139-167`）

```go
func LoadBundle(rootDir string) (*Bundle, []string)
```

加载流程：

```
LoadBundle(rootDir)
  │
  ├─ os.ReadDir(rootDir)           ← 验证根目录可达
  │   失败 → 返回空 Bundle + 单条 warning
  │
  ├─ loadDir(rootDir, "", ...)     ← 递归扫描
  │   │
  │   ├─ 对每个 entry:
  │   │   ├─ 目录 → 递归 loadDir(rootDir, relPath, ...)
  │   │   ├─ 非 .md → 跳过
  │   │   ├─ os.ReadFile → ParseConcept
  │   │   │   失败 → append warning, continue    ← 不合规文件跳过
  │   │   └─ 按 filename 分类:
  │   │        ├─ "index.md" → bundle.Index
  │   │        ├─ "log.md"   → bundle.Log
  │   │        └─ 其他       → bundle.Concepts
  │   │
  │   └─ 返回 warnings（非致命）
  │
  ├─ sort.Slice(Concepts, by ID)   ← 确定性排序
  │
  └─ 提取 Version（从 Index.Frontmatter.Extra["okf_version"]）
```

### 4.2 ParseConcept 单文件解析（`bundle.go:233-261`）

```go
func ParseConcept(content []byte, relPath string) (*Concept, error)
```

解析步骤：
1. `splitFrontmatter(content)` → 分离 YAML 头和 Markdown 正文
2. 缺 `type` → 默认 `DefaultType ("concept")`
3. ID = 去后缀 + `filepath.ToSlash` 标准化
4. `extractLinks(body)` → 提取正文中的 Markdown 链接

### 4.3 splitFrontmatter 状态机（`bundle.go:265-307`）

```
输入：raw .md 文件内容（字节）

检查前缀：
  ├─ "---\n"   → Unix frontmatter 开始
  ├─ "---\r\n" → Windows frontmatter 开始
  └─ 其他      → 无 frontmatter，整个内容作为 body 返回（容忍）

有 frontmatter 时：
  ├─ 跳过开头的 "---\n" 或 "---\r\n"
  ├─ 搜索 "\n---" 定位结束分隔符
  │   未找到 → 返回错误 "missing closing frontmatter delimiter"
  ├─ 提取 yamlPart = 中间部分
  ├─ 跳过 "\n---" 和后续换行符（\n 或 \r\n）
  ├─ yaml.Unmarshal(yamlPart, &fm)
  │   失败 → 返回错误 "parse YAML: ..."
  └─ 返回 (fm, body)
```

> 同时处理 `\n---`（Unix）和 `\r\n---`（Windows），确保跨平台兼容。

### 4.4 extractLinks 状态机（`bundle.go:315-359`）

提取正文中的 `[text](path.md)` 格式链接：

```
状态机遍历 body 字符串：
  i = 0
  loop:
    1. 从 body[i] 搜索 '['  → bracketStart
       未找到 → break
    2. 从 bracketStart 搜索 ']' → bracketEnd
       未找到 → break
    3. 检查 bracketEnd+1 是否为 '('
       否 → i = bracketEnd+1, continue
    4. 从 bracketEnd+2 搜索 ')' → parenEnd
       未找到 → break
    5. url = body[bracketEnd+2 : parenEnd]
       HasSuffix(".md") && !seen[url] → append links
    6. i = parenEnd + 1
```

只收集 `.md` 结尾的链接，用 `seen map` 去重。

### 4.5 辅助方法

```go
func (b *Bundle) FindConcept(id string) *Concept           // 线性搜索，O(n)
func (b *Bundle) ConceptsByType(typeName string) []*Concept // 类型过滤
func (b *Bundle) AllTypes() []string                        // 去重 + 排序
func ResolveLink(from, link string) string                  // 相对路径解析
func FormatNow() string                                     // RFC 3339 时间戳
```

`ResolveLink` 示例：`ResolveLink("api/checkout", "tables/orders.md")` → `tables/orders`

---

## 5. Bundle 写入引擎（Writer）

### 5.1 WriteOptions（`writer.go:19-36`）

```go
type WriteOptions struct {
    CompressFrontmatter bool   // 压缩空可选字段（默认 true）
    GenerateIndex       bool   // 自动生成 index.md（默认 true）
    GenerateLog         bool   // 自动生成 log.md（默认 true）
    OKFVersion          string // 写入 index.md 的版本号（默认 "0.1"）
}
```

### 5.2 WriteBundle 流程（`writer.go:53-107`）

```
WriteBundle(bundle, outputDir, opts)
  │
  ├─ os.MkdirAll(outputDir, 0755)
  │
  ├─ 遍历 bundle.Concepts:
  │   ├─ MkdirAll(parentDir)
  │   ├─ FormatConcept(concept, compress) → content string
  │   └─ WriteFile(fullPath, content, 0644)
  │
  ├─ 写 index.md:
  │   ├─ bundle.Index != nil → FormatConcept(Index) 写入
  │   └─ GenerateIndex=true → GenerateIndexContent(bundle, version) 写入
  │
  └─ 写 log.md:
      ├─ bundle.Log != nil → FormatConcept(Log) 写入
      └─ GenerateLog=true → GenerateLogContent() 写入
```

### 5.3 FormatConcept 渲染（`writer.go:111-167`）

```go
func FormatConcept(concept *Concept, compress bool) string
```

渲染规则：
- `type` 始终写入（必填字段）
- `compress=true` 时，空值可选字段（Title/Description/Resource/Tags/Timestamp）省略
- `compress=false` 时，所有字段都写入（Tags 空时写 `tags: []`）
- `Extra` map 通过 `yaml.Marshal` 原样追加（保留自定义字段）
- 正文末尾确保换行符

### 5.4 GenerateIndexContent 分组索引（`writer.go:171-216`）

```markdown
---
type: index
title: my-knowledge Knowledge Bundle
okf_version: 0.1
---

# Knowledge Bundle Index

> OKF Version: 0.1

This bundle contains 5 concepts.

## table

- [Orders](tables/orders.md) — Customer order records
- [Customers](tables/customers.md) — Customer table

## api

- [Checkout](api/checkout.md) — Checkout API endpoint
```

按 `AllTypes()` 返回的类型分组，每组下按 `ConceptsByType` 列出概念。

### 5.5 AppendLogEntry 变更追加（`writer.go:241-274`）

```go
func AppendLogEntry(bundleDir, action, filePath, reason string) error
```

追加逻辑：
- `log.md` 不存在 → 创建新文件（含 frontmatter + 当日日期头 + 条目）
- 当日日期头 `## YYYY-MM-DD` 已存在 → 在其下追加条目
- 日期头不存在 → 在文件末尾添加新日期节

条目格式：`- {Action}: {FilePath} ({Reason})`

---

## 6. 7 大子系统集成

### 6.1 OKF 核心引擎（`internal/okf/`）

核心包提供 Bundle 加载、概念解析、Bundle 写入、变更日志的全部基础设施。所有其他子系统均依赖此包。

| 文件 | 职责 |
|------|------|
| `bundle.go` | `Frontmatter`/`Concept`/`Bundle` 类型、`LoadBundle`/`ParseConcept`/`splitFrontmatter`/`extractLinks` |
| `writer.go` | `WriteBundle`/`FormatConcept`/`GenerateIndexContent`/`AppendLogEntry` |

### 6.2 Skill 兼容（`internal/skill/`）

SKILL.md 文件在结构上天然与 OKF 兼容，只需添加 `type: skill` 字段。

```go
const SkillOKFType = "skill"
```

**EnsureOKFType** — 确保 SKILL.md 包含 `type: skill`：

```
EnsureOKFType(skillFile) → (changed bool, err error)
  ├─ 无 frontmatter → 自动添加（含 type: skill）
  ├─ 有 frontmatter 但无 type → 添加 type: skill
  └─ 已有 type → 跳过（changed=false）
```

**批量操作**（`skill.Manager` 方法，库函数）：
- `ExportSkillsAsOKF(dir)` — 导出所有技能为 OKF Bundle
- `ImportOKFSkills(dir)` — 从 OKF Bundle 导入（只导入 `type: skill`）
- `EnsureAllOKFCompliant()` — 批量确保所有技能合规

```go
// 批量合规化：输出日志 "okf: ensured OKF type compliance, modified_count=N"
count, _ := mgr.EnsureAllOKFCompliant()

// 导出 / 导入
_ = mgr.ExportSkillsAsOKF("./skills-bundle")
imported, _ := mgr.ImportOKFSkills("./skills-bundle")
```

> **注意**：`wukong skill` 命令组当前仅提供 `list` / `show`（见 §8），上述 OKF 能力尚无对应 CLI 子命令，需通过库函数调用。

### 6.3 知识库互操作（`internal/knowledge/okf.go`）

将 OKF Bundle 导入到 RAG 知识库，或从知识库导出为 OKF Bundle。

**ImportBundle**（`okf.go:44-66`）：

```
ImportBundle(bundlePath) → (count int, err error)
  │
  ├─ okf.LoadBundle(bundlePath)
  │   非合规文件跳过，记录 warning
  │
  ├─ 将 bundlePath 加入 cfg.Sources（去重）
  └─ tRPC knowledge 模块的 dir source 递归扫描 .md 文件
      ├─ Frontmatter 字段 → 文档元数据
      ├─ Markdown 正文 → 可搜索内容
      ├─ 文件路径 → 文档 ID
      └─ 跨文件链接 → 知识图谱发现
```

**ExportBundle** — 从知识源目录导出：
- 已 OKF 合规的文件（有 frontmatter 且 type 非默认）→ 保持原样
- 非 OKF 文件 → 自动包装 frontmatter（type: "document"）
- 支持导出的扩展名：`.md .txt .json .yaml .yml .csv .html .xml`

### 6.4 知识索引注入（`internal/cortex/okf_injector.go`）

**KnowledgeIndexInjector** 在 Agent 唤醒上下文（WakeUp Context）中注入 OKF 知识概览，实现**渐进式探索**模式：Agent 先看到目录索引，再根据需要深入具体概念。

```
WakeUpWithKnowledgeIndex(ctx, identity, query, sessionID, userID, injector)
  │
  ├─ m.WakeUp(...)                    ← 标准三层唤醒（Identity → Recalled → Session）
  │
  └─ injector.Inject(wakeCtx)
       │
       ├─ getOverview()
       │   ├─ 检查 index.md 是否存在
       │   │   ├─ 存在 → 检查 modtime 缓存
       │   │   │   ├─ 未变 → 返回缓存
       │   │   │   └─ 已变 → 读取 + 解析 body + 截断 500 字符 + 缓存
       │   │   └─ 不存在 → generateOverviewFromBundle()
       │   │        └─ LoadBundle → 概念数/类型统计
       │   └─ 返回 overview 字符串
       │
       └─ prepend overview + "\n\n" + wakeCtx
```

注入格式：
```
[Available Knowledge]
This knowledge base contains 12 concepts across 3 types:
- table: 7 concept(s)
- api: 3 concept(s)
- document: 2 concept(s)
Index: <index.md 正文前 500 字符>
```

特性：
- **缓存**：基于文件 `modtime`（`cachedModTime int64`），避免重复读取
- **回退**：无 `index.md` 时自动从 Bundle 生成摘要
- **无侵入**：未配置 Bundle 时返回原始上下文
- **动态重配**：`SetBundlePath()` 支持运行时切换知识源

### 6.5 变更追踪（`internal/evolution/okf.go`）

当技能自动进化或知识库变更时，变更记录自动写入 OKF Bundle 的 `log.md`。

```go
// 变更动作
type ChangeAction string
const (
    ChangeAdded    ChangeAction = "Added"
    ChangeModified ChangeAction = "Modified"
    ChangeRemoved  ChangeAction = "Removed"
    ChangePatched  ChangeAction = "Patched"   // 技能补丁专用
)

// 变更记录
type KnowledgeChange struct {
    Action    ChangeAction
    FilePath  string
    Reason    string
    Timestamp string
}
```

**核心函数**：

| 函数 | 用途 |
|------|------|
| `RecordKnowledgeChange(bundleDir, change)` | 向 log.md 追加变更条目（委托 `okf.AppendLogEntry`） |
| `RecordSkillPatchAsKnowledge(bundleDir, skillName, reason, vFrom, vTo)` | 记录技能补丁：`Patched: skills/xxx/SKILL.md (patched v1->v2: reason)` |
| `GetChangeHistory(bundleDir)` | 读取 log.md 解析为 `[]KnowledgeChange` |
| `GetRecentChanges(bundleDir, days)` | 过滤最近 N 天的变更 |

`parseLogEntries` 解析器：按 `## YYYY-MM-DD` 分节，每条 `- Action: path (reason)` 解析为 `KnowledgeChange`。

### 6.6 ARD 联邦发现（`internal/ard/okf.go`）

OKF Bundle 可注册到 ARD (Agentic Resource Discovery) 目录，实现跨网络联邦知识发现。

| 属性 | 值 |
|------|-----|
| **Media Type** | `application/okf-bundle+json` |
| **URN 格式** | `urn:air:<publisher>:knowledge:<bundle-name>` |

```go
// OKF Bundle 目录条目元数据
type OKFBundleMetadata struct {
    OKFVersion   string   // 如 "0.1"
    ConceptCount int      // 概念总数
    ConceptTypes []string // 类型列表
    GitURL       string   // Git 仓库地址
    BundlePath   string   // Bundle 路径
}
```

注册后，远程 Agent 通过 `SearchOKFBundles` 联邦搜索发现 Bundle，通过 `ExtractOKFMetadata` 提取元数据。

### 6.7 知识富化（`internal/cortex/okf_enrichment.go`）

**EnrichmentAgent** 从结构化数据源自动生成 OKF 概念文档，使用 LLM 驱动描述生成。

**EnrichFromDDL** — 从 SQL DDL 生成表概念文档：

```
EnrichFromDDL(ctx, ddlString) → (count int, err error)
  │
  ├─ importflow.ParseDDL(ddl)        ← 解析 CREATE TABLE 语句
  │   返回 []DDLTable{Name, Columns[], ForeignKeys[]}
  │
  ├─ 对每个 table:
  │   └─ tableToConcept(ctx, table)
  │       ├─ Body: "# {Name}\n## Schema\n| Column | Type |\n..."
  │       ├─ ForeignKeys → Markdown 链接 "[refTable](../tables/refTable.md)"
  │       └─ Frontmatter{Type:"table", Resource:"ddl://{name}", Tags:[database,table]}
  │
  └─ okf.WriteBundle(bundle, outputDir, DefaultWriteOptions())
```

生成的概念示例：
```markdown
---
type: table
title: "orders"
description: "Database table orders with 5 columns"
resource: "ddl://orders"
tags: [database, table]
timestamp: 2026-08-11T12:00:00Z
---

# orders

## Schema

| Column | Type |
|--------|------|
| id | INT64 |
| customer_id | INT64 |
| total | FLOAT64 |

## Foreign Keys

- customer_id -> [customers](../tables/customers.md)
```

**EnrichFromDirectory** — 从目录扫描文件生成概念文档：

支持扩展名：`.md .txt .json .yaml .yml .csv .html .xml`

每个文件转换为 `type: "document"` 概念，正文为文件原始内容，Resource 为文件路径。

---

## 7. 配置

以下示例展示**显式启用全部 OKF 功能**的配置（示例值非默认；各项默认值见下表，布尔项默认均为 `false`）：

```yaml
okf:
  enabled: true                  # 主开关（默认 false）
  bundle_dir: ".wukong/okf"      # 默认路径
  injector_enabled: true         # 启用知识注入到 Agent 上下文（默认 false）
  enrichment_enabled: true       # 启用自动知识富化（默认 false）
  enrichment_output_dir: ".wukong/okf"   # 富化输出路径（默认 ""，空则用 bundle_dir）
  auto_export: true              # 自动导出对话知识到 OKF（默认 false）
  register_in_ard: true          # 注册到 ARD 发现目录（默认 false）
```

| 配置项 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `okf.enabled` | bool | `false` | 是否启用 OKF 系统 |
| `okf.bundle_dir` | string | `.wukong/okf` | Bundle 目录路径 |
| `okf.injector_enabled` | bool | `false` | Agent 唤醒上下文知识注入 |
| `okf.enrichment_enabled` | bool | `false` | LLM 驱动的知识自动富化 |
| `okf.enrichment_output_dir` | string | `""` | 富化输出目录 |
| `okf.auto_export` | bool | `false` | 对话后自动导出知识 |
| `okf.register_in_ard` | bool | `false` | 注册到 ARD 联邦目录 |

**验证规则**：
- `injector_enabled=true` 但 `memoryflow.enabled=false` → 警告（注入无效）
- `enrichment_enabled=true` 但 `default_provider` 为空 → 警告（使用确定性回退）

---

## 8. CLI 命令

与 OKF / 知识相关的实际存在的 CLI 命令（`internal/cli/`）：

| 命令 | 功能 |
|------|------|
| `wukong knowledge status` | 查看知识库（RAG）配置与状态（knowledge 组仅此一个子命令） |
| `wukong skill list` | 列出技能目录中的所有 SKILL.md |
| `wukong skill show <name>` | 查看某个技能的完整内容 |
| `wukong evolution log <skill-name> [--limit N]` | 查看技能进化日志——读取技能目录的 `log.json`（技能补丁版本记录），**不是** OKF Bundle 的 `log.md`；支持 `--limit`，无 `--json` 旗标 |

> **注意**：OKF Bundle 导入/导出（`internal/knowledge/okf.go` 的 `ImportBundle`/`ExportBundle`）、技能合规化与 OKF 技能导入导出（`internal/skill/okf.go` 的 `EnsureOKFType`/`ExportSkillsAsOKF`/`ImportOKFSkills`/`EnsureAllOKFCompliant`）目前均为库函数，**尚无对应 CLI 子命令**。OKF `log.md` 的读取通过 `evolution.GetChangeHistory()`/`GetRecentChanges()` 库函数完成。

---

## 9. 完整示例

### 9.1 创建知识 Bundle

```go
bundle := &okf.Bundle{
    RootDir: "./my-knowledge",
    Concepts: []*okf.Concept{
        {
            ID: "tables/orders", FilePath: "tables/orders.md",
            Frontmatter: okf.Frontmatter{
                Type: "table", Title: "Orders",
                Description: "订单表",
                Resource: "ddl://ecommerce.orders",
                Tags: []string{"ecommerce"},
                Timestamp: okf.FormatNow(),
            },
            Body: "# Orders\n\n## Schema\n\n| Col | Type |\n|-----|------|\n| id | INT64 |\n\nSee [Customers](customers.md)\n",
        },
    },
}

opts := okf.DefaultWriteOptions()
okf.WriteBundle(bundle, "./my-knowledge", opts)
// 输出:
//   my-knowledge/
//   ├── index.md   (自动生成，type: index)
//   ├── log.md     (自动生成，type: changelog)
//   └── tables/
//       └── orders.md
```

### 9.2 从 DDL 自动生成

```go
ddl := `CREATE TABLE orders (id INT64, customer_id INT64, total FLOAT64,
       FOREIGN KEY (customer_id) REFERENCES customers(id));`

agent := cortex.NewEnrichmentAgent(llm, "./my-knowledge")
count, _ := agent.EnrichFromDDL(ctx, ddl)
// 生成: my-knowledge/tables/orders.md（含外键链接到 customers.md）
```

### 9.3 变更追踪

```go
// 记录变更
evolution.RecordKnowledgeChange(".wukong/okf", evolution.KnowledgeChange{
    Action:   evolution.ChangeAdded,
    FilePath: "tables/orders.md",
    Reason:   "imported from DDL",
})

// 查询最近 7 天变更
changes, _ := evolution.GetRecentChanges(".wukong/okf", 7)
```

---

## 10. 最佳实践

### 10.1 类型命名约定

| 类型 | 用途 | 示例路径 |
|------|------|----------|
| `table` | 数据库表 | `tables/orders.md` |
| `api` | API 端点 | `api/checkout.md` |
| `metric` | 业务指标 | `metrics/revenue.md` |
| `runbook` | 操作手册 | `runbooks/incident.md` |
| `skill` | AI 技能（自动） | `skills/code-reviewer/SKILL.md` |
| `document` | 通用文档（导入） | 自动生成 |
| `concept` | 默认类型 | 无 type 字段的文件 |

### 10.2 性能考虑

- `LoadBundle` 递归扫描整个目录，大型 Bundle 可能较慢——建议拆分为多个小 Bundle
- `KnowledgeIndexInjector` 缓存 `index.md` 内容，基于 `modtime` 失效
- `FormatConcept` 的 `CompressFrontmatter=true` 可减少约 30% 文件体积

### 10.3 版本控制

OKF Bundle 目录天然适合 Git 管理，`log.md` 自动追踪变更，配合 `evolution` 系统可查看完整的知识演进历史。

---

## 11. FAQ

**Q: 没有 frontmatter 的文件会怎样？**
A: `splitFrontmatter` 检测不到 `---\n` / `---\r\n` 前缀时，整个内容作为 body 返回，`type` 默认为 `"concept"`——这是 Wukong 的扩展容忍，允许 CLAUDE.md / AGENTS.md 等文件直接加载。

**Q: SKILL.md 已经是 OKF 兼容的吗？**
A: 结构上兼容（YAML frontmatter + Markdown），但可能缺少 `type` 字段。调用 `skill.EnsureOKFType(path)`（单个文件）或 `skill.Manager.EnsureAllOKFCompliant()`（批量）自动添加 `type: skill`——这些是库函数，当前无 CLI 子命令（见 §8）。

**Q: 自定义 frontmatter 字段会丢失吗？**
A: 不会。`Frontmatter.Extra` 使用 `yaml:",inline"` 标签，所有未知字段自动收集到 `Extra map`，写入时通过 `yaml.Marshal` 原样输出——这就是 OKF 规范的消费者容错要求。

**Q: 如何让 Agent 在对话中自动使用 OKF 知识？**
A: 配置 `okf.injector_enabled: true` + `memoryflow.enabled: true`，`KnowledgeIndexInjector` 在 Agent 每次唤醒时将知识概览注入上下文。

---

> **版本**: 0.1 | **最后更新**: 2026-08-23 | **源码**: `internal/okf/` · `internal/knowledge/okf.go` · `internal/cortex/okf_*.go` · `internal/skill/` · `internal/evolution/okf.go` · `internal/ard/okf.go`
