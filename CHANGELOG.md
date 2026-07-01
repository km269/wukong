# Changelog

All changes after v0.1.14 baseline.

---

## [Unreleased] — 2026-07-01

### ANP Integration — Agent Network Protocol

- **新增 8 个 ANP 源文件**: 完整的 Agent Network Protocol 协议栈实现
  - `internal/ard/adp.go` — Agent Description Protocol (ADP) 文档生成器
  - `internal/ard/anp_types.go` — ANP-07 规范类型定义 (CollectionPage, InterfaceType)
  - `internal/ard/anp_discovery.go` — ANP 发现端点 (/.well-known/agent-descriptions, /agents/{name}/ad.json)
  - `internal/ard/did.go` — did:wba 方法实现 (Ed25519 + X25519, DataIntegrityProof)
  - `internal/ard/http_sign.go` — RFC 9421 HTTP 消息签名实现
  - `internal/summon/anp_adapter.go` — ANP 适配器 (JSON-RPC 2.0 → A2A 桥接)
  - `internal/summon/e2ee.go` — E2EE Messenger (X25519 + ChaCha20-Poly1305)
  - `internal/summon/meta_protocol.go` — Meta-Protocol 引擎 (能力协商 + 接口卡)
- **运行时集成** (`internal/cli/session.go`): BootstrapState 新增 ANP 运行时字段，启动时创建 DID/MetaProtocol/E2EE/ANP HTTP Server
- **服务端点**: ANP HTTP Server 在 `anp.port` (默认 9092) 上注册 `/anp/meta-protocol` (JSON-RPC 2.0) 和 `/anp/capabilities` (GET)

### Configuration System — ANP Config

- **新增 ANPConfig 字段** (`internal/config/types.go`): 10 个字段 (enabled/did_domain/did_path/port/discovery_enabled/meta_protocol_enabled/http_sign_enabled/e2ee_enabled/a2a_enabled/mcp_enabled/agui_enabled)
- **新增 ANP 默认值** (`internal/config/defaults.go`): 9 个默认值注册
- **新增 ANP 校验** (`internal/config/validate.go`): 端口范围检查 + 2 条警告 (DID 域名缺失 / E2EE 无 meta-protocol)
- **扩展 ANP 配置段** (`config.yaml`): 第 21 节，新增 a2a_enabled/mcp_enabled/agui_enabled 字段和详细注释
- **配置结构体总数**: 45 → 45 (ANPConfig 新增 3 字段，总数不变)

### Configuration Refactor

- **types.go**: 修复 ANPConfig 与 WukongConfig 中组件的缩进一致性（tab → 2-space）
- **defaults.go**: 修复 ANP 默认值缩进一致性
- **validate.go**: 新增 ANP 端口验证 + 2 条警告规则

### Documentation Refactor

- **README.md**: 更新统计 (241 .go / 52 _test.go / 29 包 / 45 结构体)，新增 ANP 章节
- **docs/README.md**: 更新统计，新增 ANP 协议特性章节 (2.8)
- **docs/ARCHITECTURE.md**: 更新统计，系统全景图补全 ANP 协议栈层，目录结构补全，新增 ADR #19
- **docs/CONFIG.md**: 更新统计，新增第 21 节 ANP 配置，配置索引新增 ANPConfig
- **CHANGELOG.md**: 记录 ANP 融合和配置系统重构

### Statistics Update

| 指标 | 之前 | 现在 |
|------|------|------|
| `.go` 文件 | 233 | 241 (+8 ANP 文件) |
| `_test.go` 文件 | 52 | 52 |
| 内部包 | 29 | 29 |
| 配置结构体 | 45 | 45 |
| 配置结构体字段 | ~350 | ~358 |
| 服务端点 (协议) | 4 | 5 (+1 ANP) |
| ADR 数 | 18 | 19 (+1 ANP 决策) |

---

## [Previous] — 2026-06-30

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
- **新增 `okf:` 配置段** (`config.yaml`): 第 25 节，含详细注释
- **配置结构体总数**: 44 → 45

### Documentation Refactor

- **README.md**: 统计更新 (233 .go / 52 _test.go / 29 包 / 45 结构体)，新增 OKF 章节，新增"知识标准化"哲学
- **docs/README.md**: 统计更新，新增 OKF 特性章节 (2.3)，数据流图新增 OKF 注入步骤，新增第六大哲学
- **docs/ARCHITECTURE.md**: 统计更新，系统全景图新增 OKF 知识层，目录结构新增 `internal/okf/`，新增第 6 章 OKF 知识格式系统，新增 ADR #18
- **docs/CONFIG.md**: 统计更新，新增第 19 节 OKF 配置，配置索引新增 OKFConfig，新增 OKF 推荐配置
- **CHANGELOG.md**: 记录 OKF 融合和配置系统更新

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
