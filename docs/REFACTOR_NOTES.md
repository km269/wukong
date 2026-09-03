# Wukong 配置体系重构变更记录

> 本文档记录配置体系专项重构（v0.3.3，2026-08-29）的完整变更内容，
> 作为后续同类重构的对照基线与操作手册。
> 相关参考: [CONFIG.md](CONFIG.md)（字段级配置手册） | [CHANGELOG.md](../CHANGELOG.md)（版本日志）

---

## v0.3.3 — 配置体系重构（2026-08-29）

**范围**：配置加载链路、配置模板、全量文档
**影响面**：向后兼容，无破坏性变更；所有功能值保持不变

### 一、配置代码重构（internal/config/）

#### 1. 环境变量展开机制重写：隐式约定 → tag 驱动

| 项目 | 重构前 | 重构后 |
|---|---|---|
| 展开范围 | 按硬编码键名列表展开，覆盖不全 | `envexpand:"true"` 结构体标签显式声明，反射递归遍历（`expandEnvFields`） |
| 语法 | 仅 `${VAR}` | `${VAR}` + `${VAR:-default}`（带默认值回退） |
| 可观测性 | 未解析变量静默为空 | `expandEnvTracked` 收集未解析路径，供校验告警 |

**标签落点**（8 文件 37 处）：

| 文件 | 处数 | 覆盖字段 |
|---|---|---|
| internal/config/types_cortex.go | 10 | embedding 3 + reranker 3 + GitHubAPIKey + MemoryFlow 2 + GraphFlow 1 |
| internal/config/types_browser.go | 6 | 搜索引擎 API key 等 |
| internal/config/types_orchestration.go | 5 | A2A APIKey/JWTSecret/OAuthClientSecret + Dify BaseURL/APISecret |
| internal/config/types_storage.go | 4 | RedisURL + memory extractor 3 |
| internal/config/types_observability.go | 4 | Langfuse 2 + COS 2 |
| internal/config/types_provider.go | 3 | Provider API key 等 |
| internal/gateway/config.go | 3 | 飞书凭据等 |
| internal/server/security.go | 2 | ACP/MCP auth api_key |

**修复的直接 bug**：`cortex.embedding_base_url`、`reranker_*` 等字段因未进展开列表，
运行时拿到未展开的 `${VAR:-...}` 字面量，导致请求打到错误地址。

**机制要点**：

- `expandEnvFields` 按 `mapstructure` 标签递归遍历结构体（含指针与切片），
  遇到 `envexpand:"true"` 的字符串字段执行 `expandEnvTracked`
- 展开基于 `os.Expand` 实现 `${VAR:-default}` 解析
- `expandSecrets` 保留，负责顶层敏感字段聚合并触发遍历

#### 2. Viper AutomaticEnv 盲区修复：defaults.go 补齐 17 键

Viper 的 `WUKONG_*` 环境变量覆盖**仅对 `SetDefault` 注册过的键生效**。
本轮补齐 17 个"文档化但未注册"的键，使环境变量覆盖全量生效：

```text
session.redis_url
memory.extractor_provider / extractor_model / extractor_prompt
memoryflow.planner_model / extractor_model
graphflow.extractor_model
revision.revision_provider / revision_model
apps.clone.browser_backend / insecure_tls / tls_ca_cert_path
knowledge.embedder_provider
workflow.claude_code_bin / codex_bin
dify.base_url / api_secret
```

**确立加载优先级口径**：

```text
CLI flags  >  WUKONG_* 环境变量  >  YAML 文件  >  defaults.go 内置默认值（单一事实源）
```

指针子块（`search_strategy` / `vertical_routing` / `chunking`）保持 nil = opt-in
语义，**刻意不注册** Viper 默认值，避免空指针块被误实例化。

#### 3. 校验去重

`Validate()` / `Warnings()` 与展开告警的重复项合并，告警一次只报一条。

#### 4. 调试代码清理

- 移除排障期间的 `fv0Kind` 函数与 3 处 `println DEBUG` 块（grep 验证零残留）
- 删除临时复现测试 `internal/config/debug_expansion_test.go`

### 二、config.yaml 模板重构

- **37 处 `≠` 偏差标注**：模板值与 defaults.go 内置默认不一致处全部显式标注，
  用户可一眼识别哪些是模板主动开启的。样例：

```yaml
cortex:
  enabled: true                      # ≠ default false (template enables the full CortexDB stack)
revision:
  max_context_tokens: 24000         # ≠ default 64000. Conservative ceiling ...
apps:
  clone:
    timeout: 60                    # seconds — page navigation deadline; ≠ default 300
```

- 保留全部功能值不变，仅追加注释
- 环境变量引用统一经 tag 展开机制生效

### 三、文档重构（11 个文档 + CHANGELOG）

| 文档 | 主要修复 |
|---|---|
| README.md | 删除失实的"24 ADR"（docs/adr/ 不存在）；OKF 集成表 6→7 行（新增 cortex 知识富化 `okf_enrichment`）；"6 包集成"→"6 包 7 集成点"；端点口径统一为"7 个（6 监听 + 飞书 Gateway 出站 WS）" |
| docs/README.md | 服务端点口径统一"7 个协议（6 监听 + 1 出站）"；版本 v0.3.3 |
| docs/ARCHITECTURE.md | vllm 端口 8888→8000；日期 2026-08-29 |
| docs/CONFIG.md | §2 重写为三小节：2.1 机制（tag 驱动 + ProviderConfig 代码示例）、2.2 语法、2.3 字段表 18 类（源码位置列改为 types_*.go / gateway / server）；头部"环境变量展开: tag 驱动" |
| docs/DEPLOYMENT.md | 修正 MCP Server "无内置默认"的错误声称（defaults.go 实有 `:3401`）；端口表补 Standalone MCP Server 行（默认禁用）；版本 v0.3.3 |
| docs/DEVELOPER_GUIDE.md | pkg/ 补全 5 个包（capability/httpclient/logutil/sandbox/zim）及能力描述（命令能力分析、Shell 沙箱逃逸检测）；版本 0.3.3 |
| docs/WEB_OPERATIONS_ANALYSIS.md | SearchGenome 13→12 参数 |
| docs/MEMORY_ARCHITECTURE.md | SearchGenome 13→12 参数（3 处） |
| 其余文档 | 版本尾注统一 v0.3.3 / 2026-08-29 |
| CHANGELOG.md | 新增 0.3.3 条目，完整记录本轮配置体系重构 |

**统一口径速查**：

- 监听端口 6 个：9090（A2A）/ 9091（ACP）/ 8080（AG-UI）/ 3400（ACP-MCP）/
  3401（Standalone MCP，默认禁用）/ 9092（ANP）；另有飞书 Gateway 出站 WS，
  合计 7 个协议端点
- OKF：6 个 Go 包、7 个集成点（第 7 个为 cortex 富化代理）
- vllm 默认地址 `:8000`（internal/provider/factory.go）
- SearchGenome 12 参数（internal/search/genome.go）

### 四、验证结果（全绿）

| 检查项 | 命令 | 结果 |
|---|---|---|
| 编译 | `go build ./...` | 通过 |
| 静态检查 | `go vet ./...` | 通过 |
| CI 等价测试 | `go test -short -race -count=1 ./internal/... ./pkg/...` | 44 包全过 |
| 配置校验 | `go run ./cmd/wukong config validate` | valid（14 条警告均为预期项） |

展开修复已实测生效：`config validate` 中 `embedding_base_url` 等字段不再出现
未展开字面量；未设置的外部 API key 正常产生告警。

### 五、遗留注意事项

1. **ACP 安全警告**：`acp_server` 绑定 `:9091` 非回环且无 auth——生产部署需设
   `security.auth.type=api_key`（校验器会持续提醒）
2. **本地推理 context_window**：ollama / lmstudio 未显式设置，默认保守 8000，
   建议按 `--max-model-len` / `num_ctx` 显式配置
3. **新增配置项三处同步**：类型定义（含 `envexpand` 标签）→ defaults.go
   `SetDefault` → 文档字段表。缺任一处将重现本轮修复的两类盲区
   （展开失效 / 环境变量覆盖失效）

### 六、工程教训

- **同一文件的多处编辑必须串行执行**：并行编辑会导致部分编辑静默丢失，
  关键编辑后需用 grep 计数验证持久化
- **事实核查优先于文档措辞**：本轮发现两处文档声称与源码不符
  （"24 ADR"、MCP "无内置默认"），均以 defaults.go / 目录实况为准纠正

---

*Wukong v0.3.3 · 2026-08-29*
