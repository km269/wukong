# Changelog

All changes after v0.1.14 baseline.

---

## [Unreleased] — 2026-06-30

### OKF Integration — Open Knowledge Format v0.1

- **新增 `internal/okf/` 包** (3 文件): OKF v0.1 核心实现
  - `bundle.go` — Bundle 加载、Concept 解析、Frontmatter 处理、Markdown 链接提取
  - `writer.go` — Bundle 写入、概念格式化、index.md/log.md 自动生成
  - `bundle_test.go` — 7 个单元测试
- **ARD OKF 联邦发现** (`internal/ard/okf.go`): OKF Bundle 注册为 CatalogEntry
  - 新增 MediaType: `application/okf-bundle+json`
  - URN 格式: `urn:air:wukong.ai:knowledge:<bundle-name>`
- **Cortex OKF EnrichmentAgent** (`internal/cortex/okf_enrichment.go`): 从 DDL/目录自动生成 OKF 概念文档
- **Cortex OKF KnowledgeIndexInjector** (`internal/cortex/okf_injector.go`): OKF 知识索引注入 MemoryFlow 唤醒上下文
- **Evolution OKF 变更追踪** (`internal/evolution/okf.go`): 通过 log.md 追踪知识文件变更
- **Knowledge OKF 导入/导出** (`internal/knowledge/okf.go`): RAG 知识库与 OKF Bundle 互操作
- **Skill OKF 兼容层** (`internal/skill/okf.go`): SKILL.md 文件 OKF 合规 (`type: skill`)

### Configuration System — OKF Config

- **新增 `OKFConfig` 结构体** (`internal/config/types.go`): 7 个字段 (enabled/bundle_dir/injector_enabled/enrichment_enabled/enrichment_output_dir/auto_export/register_in_ard)
- **新增 `setOKFDefaults()`** (`internal/config/defaults.go`): OKF 默认值注册
- **新增 OKF 校验** (`internal/config/validate.go`): 3 条 OKF 相关警告检查
- **新增 `okf:` 配置段** (`config.yaml`): 第 24 节，含详细注释
- **配置结构体总数**: 44 → 45

### Documentation Refactor

- **README.md**: 统计更新 (233 .go / 52 _test.go / 29 包 / 45 结构体)，新增 OKF 章节，新增"知识标准化"哲学
- **docs/README.md**: 统计更新，新增 OKF 特性章节 (2.3)，数据流图新增 OKF 注入步骤，新增第六大哲学
- **docs/ARCHITECTURE.md**: 统计更新，系统全景图新增 OKF 知识层，目录结构新增 `internal/okf/`，新增第 6 章 OKF 知识格式系统，新增 ADR #18
- **docs/CONFIG.md**: 统计更新，新增第 19 节 OKF 配置，配置索引新增 OKFConfig，新增 OKF 推荐配置
- **CHANGELOG.md**: 记录 OKF 融合和配置系统更新

### Statistics Update

| 指标 | 之前 | 现在 |
|------|------|------|
| `.go` 文件 | 224 | 233 (+9 OKF 文件) |
| `_test.go` 文件 | 51 | 52 (+1 OKF 测试) |
| 内部包 | 28 | 29 (+1 `internal/okf/`) |
| 配置结构体 | 44 | 45 (+1 OKFConfig) |
| ADR 数 | 17 | 18 (+1 OKF 决策) |
| 架构哲学 | 5 | 6 (+1 知识标准化) |

---

## [Previous] — 2026-06-30 (Config Refactor)

### Configuration System Refactor

- **config.go 拆分**: 1613 行单文件拆分为 4 个按职责分离的文件
  - `config.go` — 包文档、`WukongConfig` 根结构体、`Loader`、查询方法
  - `types.go` — 43 个子配置结构体定义
  - `defaults.go` — `setDefaults` 按子系统拆分为 11 个方法
  - `validate.go` — `Validate()` 致命错误检查 + `Warnings()` 非致命警告
- **新增 MemoryConfig 字段**: `enable_smart_cleanup`、`cleanup_trigger_threshold`、`cleanup_target_threshold`、`memory_ttl`
- **新增 CloneDefaults 字段**: `scope_prefix`
- **新增 PackDefaults 结构体**: `apps.pack` 配置段
- **新增配置验证**: `Validate()` + `LoadAndValidate()`
- **config.yaml 重构**: 统一路径约定、替换硬编码 IP、补齐缺失字段

---

## [Previous] — 2026-06-27

### Clone Engine — Chrome 渲染快照

- Settle 网络空闲等待、autoScroll 动态高度、ErrNotHTML 路由
- BFS/DFS 遍历、external robots.txt、AssetWorkers 4→8

### 反反爬体系 — 5 层深度防御

- Stealth + Preflight CF 检测 + 5 级自动升级 + cf_clearance + 161 UA 池

### ZIM 修复

- 集群缓存 key 匹配修复

### CI/CD

- GoReleaser 6 平台 + Homebrew + Scoop + Docker multi-arch
- GitHub Actions: Lint + Test(race) + Cross-build
