# Wukong 部署运维指南

> 本指南基于 Wukong 真实源码（`internal/cli/`、`internal/health/`、`internal/telemetry/`、`internal/util/database.go`、`internal/cli/shutdown.go`、`pkg/sandbox/` 等）逐文件精读后产出，涵盖部署模式、构建链路、配置体系、存储引擎、健康检查体系、优雅关闭流水线、可观测性、安全加固与故障排查。

---

## 目录

1. [概述与部署形态](#1-概述与部署形态)
2. [构建与安装](#2-构建与安装)
3. [配置体系](#3-配置体系)
4. [路径约定与运行时目录](#4-路径约定与运行时目录)
5. [存储引擎](#5-存储引擎)
6. [服务端口与协议端点](#6-服务端口与协议端点)
7. [健康检查体系](#7-健康检查体系)
8. [优雅关闭](#8-优雅关闭)
9. [可观测性](#9-可观测性)
10. [安全加固](#10-安全加固)
11. [部署方案](#11-部署方案)
12. [故障排查](#12-故障排查)
13. [多实例与水平扩展](#13-多实例与水平扩展)
14. [相关文档](#14-相关文档)

---

## 1. 概述与部署形态

Wukong（module `github.com/km269/wukong`，Go 1.26）是一个**本地优先**（local-first）的扩展式 AI Agent 平台。它以单二进制形式交付，支持四种部署形态：

```
┌──────────────────────────────────────────────────────────────────────────┐
│                        Wukong 部署形态全景                                   │
├─────────────────┬──────────────────┬──────────────────┬───────────────────┤
│  CLI/TUI 单用户  │  Headless Server │     Docker       │   Binary Release  │
│  (默认形态)      │  (服务化)        │  (GHCR 多架构)   │  (.goreleaser)    │
├─────────────────┼──────────────────┼──────────────────┼───────────────────┤
│ wukong session  │ wukong server    │ ghcr.io/km269/   │ GitHub Releases   │
│ wukong run      │ 暴露多协议端口    │   wukong:latest  │ 6 目标 tar.xz/zip │
│                 │ +:8086 健康检查   │ debian+chrome     │                   │
├─────────────────┴──────────────────┴──────────────────┴───────────────────┤
│              存储：单 wukong.db (WAL 强制)  +  可选 Redis/COS              │
│              可观测：OpenTelemetry (OTLP)  +  Langfuse (LLM 专用)         │
│              安全：permission_mode / API Key / TLS / SSRF / OS Sandbox    │
└──────────────────────────────────────────────────────────────────────────┘
```

三种顶层命令共享 `bootstrapSession`（`internal/cli/session.go`）初始化路径——统一完成 9 阶段启动（配置加载→扩展初始化→数据库连接→Agent Loop 组装→协议服务器挂载）。

### 关键工程事实

| 属性 | 事实 |
|------|------|
| SQLite 驱动 | `modernc.org/sqlite`（纯 Go），**无 CGO 依赖** |
| 构建工具链 | Makefile + Taskfile.yaml + `.goreleaser.yaml`(v2) |
| 镜像仓库 | GHCR (`ghcr.io/km269/wukong`)，**无 Docker Hub 镜像** |
| 默认数据目录 | `~/.config/wukong/`（容器内 `/root/.config/wukong`） |
| 健康检查端口 | `:8086`（仅 `wukong server` 模式） |

---

## 2. 构建与安装

### 2.1 源码构建

```bash
# 方式 A：直接安装到 $GOPATH/bin
go install github.com/km269/wukong/cmd/wukong@latest

# 方式 B：克隆仓库后本地构建
git clone https://github.com/km269/wukong.git
cd wukong
make build                 # 输出 build/wukong
make build-all             # linux/darwin/windows × amd64/arm64 交叉编译
```

`make build` 通过 LDFLAGS 注入版本信息到 `internal/cli` 包：

```
-X github.com/km269/wukong/internal/cli.Version=...
-X github.com/km269/wukong/internal/cli.GitCommit=...
-X github.com/km269/wukong/internal/cli.BuildDate=...
```

> ⚠️ `.goreleaser.yaml` 的 ldflags 注入目标是 **`main` 包**，与 Makefile 注入 `internal/cli` 包不一致——发行版与本地构建的版本字段来源不同。

### 2.2 预编译二进制（`.goreleaser.yaml` v2）

```bash
wget https://github.com/km269/wukong/releases/latest/download/wukong-linux-amd64.tar.xz
tar -xJf wukong-linux-amd64.tar.xz
sudo mv wukong /usr/local/bin/
wukong --version
```

Release 产物覆盖 6 个目标（`CGO_ENABLED=0`）：

| GOOS | GOARCH |
|------|--------|
| linux | amd64, arm64 |
| darwin | amd64, arm64 |
| windows | amd64, arm64 |

### 2.3 Docker（GHCR 多架构）

```bash
docker pull ghcr.io/km269/wukong:latest
docker pull ghcr.io/km269/wukong:latest-arm64
```

镜像内容（`Dockerfile`）：

| 层 | 内容 |
|----|------|
| builder | `golang:1.26-alpine`，`CGO_ENABLED=0 go build -ldflags="-s -w"` |
| runtime（默认） | `debian:bookworm-slim` + google 官方源 `google-chrome-stable` + `fonts-liberation`（真实 Chrome JA3/编解码器/字体，见 docs/ANTIBOT_GUIDE.md §0.5） |
| runtime（slim 变体） | `--build-arg CHROME_FLAVOR=slim` → Debian + 发行版 `chromium`（更小，但属反爬降级模式） |
| 环境变量 | `CHROME_BIN=/usr/local/bin/wukong-browser`（flavor 无关软链）、`CHROMIUM_FLAGS="--disable-dev-shm-usage"` |
| 目录 | `/data`（数据）、`/out`（输出） |
| 入口 | `ENTRYPOINT ["wukong"]`、`CMD ["session"]` |

> ⚠️ 镜像**无 `EXPOSE` 指令**、**无 `USER` 指令（以 root 运行）**。

### 2.4 包管理器

```bash
# Homebrew (macOS/Linux)
brew tap km269/homebrew-tap
brew install wukong

# Scoop (Windows)
scoop bucket add km269/scoop-bucket
scoop install wukong
```

---

## 3. 配置体系

配置加载遵循 7 级优先级（`internal/config/config.go`）：CLI flags > 环境变量（`WUKONG_` 前缀自动映射）> `--config` 显式文件 > `./config.yaml` > `~/.config/wukong/config.yaml` > `/etc/wukong/config.yaml` > 内置默认值（`internal/config/defaults.go`）。密钥类字段支持 `${VAR}` / `${VAR:-default}` 展开，展开的键名会被收集用于日志脱敏。

> 加载优先级的完整规则、环境变量映射细节、20+ 支持展开的字段清单与完整校验规则（致命 + 警告）见 [CONFIG.md](./CONFIG.md) §1–§3，此处不重复。

运维常用命令：

```bash
wukong config validate     # 12 项检查：必填项、互斥项、路径可达性等（部署前自检）
wukong config show         # 打印合并后的最终配置（密钥脱敏）
```

---

## 4. 路径约定与运行时目录

部署时需要区分并分别持久化两组目录（完整路径解析规则见 [CONFIG.md](./CONFIG.md) §4 路径约定）：

```
~/.config/wukong/                    # 用户配置根目录（可持久化）
├── config.yaml                      # 主配置文件
├── prompts/                         # 自定义提示词模板
├── wukong.db                        # 共享 SQLite 数据库（WAL）
├── wukong.db-wal                    # WAL 日志文件（运行时生成）
└── wukong.db-shm                    # 共享内存索引（运行时生成）

.wukong/                             # 运行时工作目录（当前工作目录下）
├── apps/                            # 应用执行产物
├── cache/                           # 缓存（克隆/搜索等）
├── recipes/                         # Recipe 定义
├── skills/                          # 技能定义（SKILL.md）
├── visuals/                         # 可视化输出
├── okf/                             # OKF 知识 Bundle
└── evals/                           # 评估数据集

.wukongignore                        # Git 式忽略文件（控制 Agent 文件访问范围）
```

> **`.wukong/` 是运行时目录，`~/.config/wukong/` 是用户配置目录**——两者职责分离；容器/K8s 部署时前者随工作目录（如 `/data`）挂载，后者需独立卷。

---

## 5. 存储引擎

### 5.1 单文件 SQLite + WAL 强制开启

存储后端定义于 `internal/util/database.go`。核心设计是 **DatabasePool + MultiPool** 两层结构：

```
┌─────────────────────────────────┐     ┌────────────────────────────────────┐
│  DatabasePool                    │     │  MultiPool                          │
│  (单个 *sql.DB 连接)             │     │  pools map[string]*DatabasePool     │
│                                  │     │                                    │
│  ┌─────────────────────────────┐ │     │  ┌──────────────────────────────┐  │
│  │ path: "wukong.db"           │ │     │  │ "shared"  → wukong.db       │  │
│  │ DSN: ?_journal_mode=WAL     │ │     │  │ (session/memory/todo/        │  │
│  │     &_synchronous=NORMAL    │◄┼─────┼──│  recall/cortex/evolution     │  │
│  │     &_foreign_keys=ON       │ │     │  │  全部共享)                    │  │
│  │     &_busy_timeout=5000     │ │     │  └──────────────────────────────┘  │
│  └─────────────────────────────┘ │     │                                    │
│                                  │     │  ┌──────────────────────────────┐  │
│  SetMaxOpenConns(4)             │     │  │ "memory" → memory.db (可选)  │  │
│  SetMaxIdleConns(2)             │     │  │ GetOrCreate(name, path)      │  │
│  懒加载 / sync.Mutex 线程安全    │     │  └──────────────────────────────┘  │
│  └─ Close(): PRAGMA              │     │                                    │
│     wal_checkpoint(TRUNCATE)    │     │  └─ Close(): 逐池关闭             │
└─────────────────────────────────┘     └────────────────────────────────────┘
```

**关键事实**（逐行核实 `database.go`）：

- `NewMultiPool(sharedPath)` 创建名为 `"shared"` 的池，`bootstrapSession` 中 `session`/`memory`/`todo`/`recall`/`cortex`/`evolution` 全部通过 `MultiPool.Shared()` 获取同一个 `*sql.DB`。
- WAL 模式**硬编码在 DSN 和 PRAGMA 双重保障**中，不可通过配置关闭。
- `_busy_timeout=5000` 使锁等待最多 5 秒后返回 `SQLITE_BUSY`，而非立即报错。
- `SetMaxOpenConns(4)` 适配 WAL 的"多读单写"模型（WAL 允许并发读 + 一个写者）。
- `Close()` 先 `db.Ping()` 检查连接是否可用，再执行 `PRAGMA wal_checkpoint(TRUNCATE)` 刷新 WAL 到主数据库文件，保证优雅退出**零数据丢失**。

> 旧的 `session.wal_mode` 配置项**不存在**，是早期文档臆造。

### 5.2 可选外部后端

| 子系统 | 默认 | 可选 | 配置路径 |
|--------|------|------|----------|
| session | sqlite (`wukong.db`) | memory / redis（`go-redis v9.12.1`） | `session.backend` + `session.redis_url` |
| memory | sqlite (`wukong.db`) | memory（进程内）；`redis` 仅在配置校验中为合法值，**实际未实现** | `memory.backend` |
| 制品/产物 | 本地文件 | 对象存储 COS | `artifact.backend: cos` |

```yaml
session:
  backend: redis
  redis_url: "redis://localhost:6379/0"   # 留空时回退 redis://localhost:6379/0
```

> **注意**：`SessionConfig` 只有 `redis_url` 一个 Redis 连接字段（`internal/config/types_storage.go`），**没有** `redis.addr` / `redis.db` 之类的嵌套结构——`NewRedisSessionService` 通过 `redis.ParseURL` 解析该 URL。`MemoryConfig` 没有任何 Redis 连接字段，`internal/memory` 的 `createService` 仅实现 `sqlite` / `memory` 两种后端（配置 `backend: redis` 会在启动时报 unsupported memory backend）。

---

## 6. 服务端口与协议端点

以下端口来自 `internal/cli/session.go` 的 `bootstrapSession` 与 `internal/config/defaults.go`：

| 服务 | 默认端口 / 路径 | 配置路径 | 默认状态 |
|------|----------------|----------|----------|
| A2A Server | `:9090` | `a2a_server.address` | 禁用 |
| AG-UI SSE | `:8080` `/agui` | `agui.address` / `agui.path` | 禁用 |
| ACP Server | `:9091` `/acp` | `acp_server.address` / `acp_server.path` | 禁用 |
| **ACP MCP Bridge** | `:3400` `/mcp` | `acp_mcp.address` / `acp_mcp.path` | **默认启用** |
| Standalone MCP Server | 无内置默认（config.yaml 模板为 `:3401`，`mcp_server.enabled: true` 时 `address` 必填） | `mcp_server.address` | 禁用 |
| ANP Server | `:9092` | `anp.port` | 禁用 |
| ARD Registry | 动态 / `0` | `ard.publish_port` | 禁用 |
| Health HTTP | `:8086` | 硬编码（仅 server 模式） | server 模式自动启用 |
| Gateway | **无端口** | `gateway.*` | 禁用 |

> **Gateway 不监听任何 HTTP 端口**——它通过 WebSocket **出站**长连接到飞书等消息平台，没有入站监听。

---

## 7. 健康检查体系

### 7.1 架构（`internal/health/health.go`）

健康检查采用 **Registry + Checker** 模式：

```go
type Registry struct {
    checkers  map[string]Checker    // 按名称注册的检查函数
    startTime time.Time             // 进程启动时间（计算 uptime）
    version   string                // 版本号
}

type Checker func(ctx context.Context) ComponentHealth

type ComponentHealth struct {
    Name      string
    Status    HealthStatus          // healthy / degraded / unhealthy
    Message   string
    LatencyMs int64                 // 检查耗时
}
```

### 7.2 内置 Checker 类型

| Checker | 文件位置 | 行为 |
|---------|----------|------|
| `DBChecker(name, ping)` | `health.go:DBChecker` | 执行真实 `db.PingContext()`，返回延迟。失败→unhealthy |
| `A2AServerChecker(enabled, addr)` | `health.go:A2AServerChecker` | HTTP GET `/.well-known/agent.json`（2s 超时）。禁用→healthy；不可达→degraded |
| `ExtensionChecker(name, active, count)` | `health.go:ExtensionChecker` | 检查扩展是否激活及数量 |
| `ModelChecker` | `health.go` | 检查 LLM provider 配置是否可用 |
| 内联 Checker（session/memory/gateway） | `server.go:registerHealthCheckers` | 静态配置检查（backend 类型、运行状态等） |

### 7.3 HTTP 端点（仅 `wukong server` 模式）

只有 `wukong server` 会在 `:8086` 暴露 HTTP 探针（`internal/cli/server.go:119-139`）；`wukong session` 与 `wukong run` **不挂载任何 HTTP 监听**。

```
┌─────────────────────────────────────────────────────────────┐
│  server 模式 :8086 健康检查路由                              │
│                                                             │
│  /healthz ── Registry.HTTPHandler()                        │
│              ├─ 执行全部 Checker（10s 超时）                │
│              ├─ healthy/degraded → 200 OK                  │
│              ├─ unhealthy → 503 Service Unavailable        │
│              └─ 返回 CheckResult JSON（含 components 数组）  │
│                                                             │
│  /readyz  ── Registry.HTTPHandler() (同 /healthz)          │
│                                                             │
│  /livez   ── LivenessHandler()                             │
│              └─ 恒返回 {"alive":true} + 200 OK             │
│              (K8s liveness probe，永不失败)                 │
└─────────────────────────────────────────────────────────────┘
```

`CheckResult` JSON 结构：

```json
{
  "status": "healthy",
  "version": "v0.3.1",
  "uptime": "2h13m",
  "components": [
    { "name": "database",    "status": "healthy", "message": "database is reachable", "latency_ms": 2 },
    { "name": "a2a_server",  "status": "healthy", "message": "A2A server listening on :9090" },
    { "name": "session",     "status": "healthy", "message": "backend: sqlite" },
    { "name": "memory",      "status": "healthy", "message": "backend: sqlite, auto_extract: true" },
    { "name": "gateway",     "status": "healthy", "message": "running" }
  ],
  "timestamp": "2026-08-11T12:34:56Z"
}
```

所有响应携带 `Cache-Control: no-cache, no-store, must-revalidate` 头。

### 7.4 server 模式注册的检查项

`registerHealthCheckers()`（`server.go:258-310`）注册的检查项：

| 名称 | Checker | 检查内容 |
|------|---------|----------|
| `database` | `DBChecker` | 真实 `dbPool.Shared().GetDB().PingContext(ctx)` |
| `a2a_server` | `A2AServerChecker` | 仅 `a2a_server.enabled` 时注册，HTTP 探测 `/.well-known/agent.json` |
| `session` | 内联 | 报告 `session.backend`（sqlite/redis） |
| `memory` | 内联 | 报告 `memory.backend` + `auto_extract` 状态 |
| `gateway` | 内联 | 仅 `GatewayServer != nil` 时注册，检查 `IsRunning()` |

### 7.5 CLI 健康检查（`wukong health`）

`wukong health`（`internal/cli/health.go`）在任意模式下运行，检查项更多：

```
platform · sandbox · config · provider · session · memory · cortex(可选)
servers · security · evolution(可选)
```

退出码：`healthy` → `0`；`degraded` / `unhealthy` → `1`。支持表格与 JSON 输出。

---

## 8. 优雅关闭

### 8.1 设计目标

Wukong 的关闭流程（`internal/cli/shutdown.go`）解决了早期代码的三个问题：

1. **逻辑分散**：原先 shutdown 逻辑分散在 `runSession` 的信号处理器、defer 清理、`server.go` 的 `shutdownServers` 和 `run.go` 的 `cleanupBootstrap` 四处。
2. **资源泄漏**：`ANPServer` 和 `ARDRegistry` 在部分路径中未关闭。
3. **双重关闭风险**：`runSession` 可能从 goroutine 和 defer 两处停止同一服务器。

### 8.2 幂等保证（`sync.Once`）

```
shutdownBootstrap(ctx, state, loop)
  │
  ├─ 启动 watchdog goroutine（15s 硬超时 → os.Exit(0)）
  │   防止某个 .Close() 忽略 context 而无限阻塞
  │
  └─ state.do(ctx, loop, state)     ← shutdownState.once.Do()
       │
       └─ runShutdown(ctx, loop, state)
```

`shutdownState` 内嵌于 `BootstrapState`，其 `do()` 方法通过 `sync.Once` 保证无论从信号处理器还是 defer 调用，关闭逻辑**只执行一次**。

### 8.3 关闭顺序

```
runShutdown 按严格顺序执行（best-effort：单项失败不中断后续）：

  1. GatewayServer.Stop(ctx)           ← 先关入站消息，避免关闭期间触发新 Agent 运行
  2. A2AServer.Stop(ctx)              ← 协议服务器依次关闭
     AGUIServer.Stop(ctx)
     ACPServer.Stop(ctx)
     ACPMCPBridge.Stop()
     MCPServer.Shutdown(ctx)
     ARDRegistry.Shutdown(ctx)
     ANPServer.Shutdown(ctx)
  3. CredentialRotator.Stop()         ← 停止凭据轮换
  4. ExtMgr.Close()                   ← 关闭 MCP 子进程（必须在 CoreLoop 之前，
  5. KnowledgeMgr.Close()                避免进行中的 tool call 命中已关闭的 transport）
  6. CoreLoop.Close()                 ← 最后关闭，内部链式清理：
       ├─ waitWithTimeout(runWg, 5s)      等待同步后置写入完成
       ├─ waitWithTimeout(bgWg, 5s)       等待后台 goroutine 退出
       └─ closeFn():
            ├─ runner.Close()
            ├─ EvolutionClose()           停止进化引擎后台 Worker
            ├─ MemoryClose()              停止记忆提取 Worker
            ├─ SessionService.Close()
            ├─ GraphFlowService.Close()
            ├─ TelemetryShutdown(10s)     OTLP flush + Langfuse flush
            └─ DBPoolClose()              ← 数据库最后关闭
                 └─ PRAGMA wal_checkpoint(TRUNCATE)
```

### 8.4 超时保护

| 层级 | 超时 | 保护 |
|------|------|------|
| watchdog（全局） | 15s | `os.Exit(0)` 强制退出 |
| server 信号处理 | 15s | `shutdownCtx` |
| runWg（同步写入） | 5s | `waitWithTimeout` |
| bgWg（后台 goroutine） | 5s | `waitWithTimeout` |
| Telemetry flush | 10s | `TelemetryShutdown(10s)` |
| session defer | 10s | `shutdownCtx` |

> 信号处理器的注释明确指出：信号路径中**不使用 `os.Exit(0)`**——让主 goroutine 自然返回，以便 defer 清理和日志刷新完成。

---

## 9. 可观测性

> **Wukong 没有 Prometheus 集成，也不存在 `/metrics` 端点或 `wukong_llm_calls_total` 之类的指标名。** 可观测性的两条出口是 **OpenTelemetry** 与 **Langfuse**。

### 9.1 OpenTelemetry（`internal/telemetry/telemetry.go`）

基于 `sdktrace.TracerProvider`，支持三种 exporter：

| `telemetry.exporter_type` | 实现包 | 典型用途 |
|---------------------------|--------|----------|
| `grpc` | `otlptracegrpc` | 生产（OTel Collector） |
| `http` | `otlptracehttp` | 生产（HTTP 直传） |
| `console`（默认） | `stdout`（自定义 ConsoleExporter） | 本地调试 |

**初始化流程**（`Manager.Initialize`）：

```
NewManager(cfg)
  └─ Initialize(ctx)
       ├─ resource.New(                   ← Resource 属性
       │    ServiceName / ServiceVersion / DeploymentEnvironment)
       ├─ createExporter(ctx)             ← 按 exporter_type 选择
       ├─ sampler = ParentBased(          ← 采样策略
       │    TraceIDRatioBased(sample_rate))
       ├─ NewTracerProvider(               ← 创建 Provider
       │    WithBatcher(exp),
       │    WithResource(res),
       │    WithSampler(sampler))
       ├─ otel.SetTracerProvider(tp)       ← 全局注册
       └─ otel.SetTextMapPropagator(       ← W3C 传播
            TraceContext{} + Baggage{})
```

配置：

```yaml
telemetry:
  enabled: true                # 默认 false
  exporter_type: grpc          # 默认 console
  endpoint: localhost:4317     # OTel Collector 默认地址
  service_name: wukong
  service_version: v0.3.1
  environment: production
  sample_rate: 1.0             # 1.0 = 全采样
```

**ParentBased 采样**：根 span 按 `TraceIDRatioBased(sample_rate)` 采样；子 span 继承父 span 的采样决策——生产环境建议 `0.1`~`0.5`。

### 9.2 Langfuse（`internal/observability/langfuse.go`）

Langfuse 提供 LLM 专用追踪 UI（模型请求、token 用量、工具调用链），通过 `trpc-agent-go` 的 telemetry/langfuse 钩子，最终也走 OTLP HTTP 协议。

```yaml
observability:
  langfuse_enabled: true
  langfuse_host: https://cloud.langfuse.com
  langfuse_public_key: pk-lf-...
  langfuse_secret_key: sk-lf-...
```

凭据解析顺序：config 值 → 环境变量（`LANGFUSE_PUBLIC_KEY` / `LANGFUSE_SECRET_KEY` / `LANGFUSE_HOST` / `LANGFUSE_INSECURE`）。本地部署时自动设置 `LANGFUSE_INSECURE=true`。

**生命周期整合**：`bootstrapSession` 将 Langfuse cleanup 与 OTel shutdown 合并为 `combinedShutdown`，在 `CoreLoop.Close()` 的 `closeFn()` 中统一执行（TelemetryShutdown 阶段）。

### 9.3 日志

标准库 `log/slog`（结构化日志）。开启调试：

```bash
wukong session --debug
wukong server --debug
WUKONG_LOG_LEVEL=debug wukong server
```

---

## 10. 安全加固

### 10.1 权限模式（`permission_mode`）

Wukong 的 Guard 层（`internal/security/`）支持四种权限模式控制 Agent 行为：

| 模式 | 行为 |
|------|------|
| `auto` | 自动批准安全操作，拦截危险命令 |
| `smart` | 智能判断，对不确定操作请求确认 |
| `manual` | 所有操作均需用户确认 |
| `chat_only` | 仅允许纯对话，禁用所有工具 |

通过 `--debug` 可查看 Guard 决策日志。

### 10.2 认证

- **API Key 认证**：使用 `crypto/subtle.ConstantTimeCompare` 进行常数时间比较，防止时序攻击。
- **JWT 认证**：协议端点支持 JWT Bearer Token 验证。
- **凭据轮换**：`CredentialRotator`（`summon` 包）在后台定期轮换凭据，关闭时通过 `Stop()` 安全终止。

### 10.3 网络安全

| 措施 | 实现 |
|------|------|
| TLS | 强制 TLS 1.2+ ECDHE 密码套件 |
| CORS | 默认 localhost-only，生产环境需显式配置允许的 Origin |
| SSRF 防护 | Guard 层拦截内网元数据端点（如 `169.254.169.254`） |
| `.wukongignore` | Git 式语法控制 Agent 文件访问范围 |

### 10.4 OS 沙箱（`pkg/sandbox/`）

| 平台 | 机制 | 检测标识 | 要求 |
|------|------|----------|------|
| Linux | Landlock LSM | `landlock-abi<N>` | 内核 ≥ 5.13 |
| macOS | `sandbox-exec(1)` | `sandbox-exec` | 系统自带 |
| Windows | Low Integrity Level | `integrity-level`（SID `S-1-16-4096`） | Vista+ |
| FreeBSD / 其他 | **不支持** | — | 告警并以无沙箱运行 |

```bash
wukong env | grep -i sandbox     # 查看当前沙箱模式
wukong health                    # health 输出含 sandbox 检查项
```

不支持沙箱的平台打印告警后以**无沙箱**模式运行；此时应依赖容器/systemd 沙箱补足。

### 10.5 Docker 加固

镜像默认以 **root** 运行（无 `USER` 指令），生产环境请显式加固：

```bash
docker run -d --name wukong \
  --user 1000:1000 \
  --read-only --tmpfs /tmp \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  -v ~/.config/wukong:/root/.config/wukong \
  ghcr.io/km269/wukong:latest server
```

---

## 11. 部署方案

### 11.1 单机 systemd（Linux）

```ini
[Unit]
Description=Wukong Headless Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=wukong
Group=wukong
WorkingDirectory=/var/lib/wukong
EnvironmentFile=/etc/wukong/wukong.env
ExecStart=/usr/local/bin/wukong server --config /etc/wukong/config.yaml
Restart=on-failure
RestartSec=5s

Environment=GOMAXPROCS=4
Environment=GOMEMLIMIT=4GiB

NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/wukong /home/wukong/.config/wukong

[Install]
WantedBy=multi-user.target
```

### 11.2 Docker Compose

```yaml
version: "3.9"
services:
  wukong:
    image: ghcr.io/km269/wukong:latest
    container_name: wukong
    restart: unless-stopped
    command: ["server"]
    ports:
      - "8086:8086"     # 健康检查
      - "3400:3400"     # ACP MCP Bridge（默认启用）
    volumes:
      - ./config:/root/.config/wukong
      - ./data:/data
    environment:
      DEEPSEEK_API_KEY: ${DEEPSEEK_API_KEY}
      WUKONG_DEFAULT_PROVIDER: deepseek
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:8086/livez"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 15s
```

### 11.3 Kubernetes

```yaml
spec:
  containers:
    - name: wukong
      image: ghcr.io/km269/wukong:latest
      args: ["server"]
      ports:
        - name: health
          containerPort: 8086
        - name: mcp
          containerPort: 3400
      readinessProbe:
        httpGet: { path: /readyz, port: 8086 }
        initialDelaySeconds: 5
        periodSeconds: 10
      livenessProbe:
        httpGet: { path: /livez, port: 8086 }
        initialDelaySeconds: 15
        periodSeconds: 20
      resources:
        requests: { cpu: "200m", memory: "512Mi" }
        limits:   { cpu: "2",    memory: "2Gi" }
```

> SQLite 是**单文件嵌入式数据库**，`wukong.db` 必须挂载在 `ReadWriteOnce` 的 PVC 上；**不要**横向扩缩为多副本共享同一文件。

---

## 12. 故障排查

### 12.1 诊断命令

| 命令 | 作用 |
|------|------|
| `wukong health` | 系统健康检查（表格 / JSON），退出码反映状态 |
| `wukong env` | 运行时环境：版本、OS、路径、沙箱、配置摘要 |
| `wukong stats` | 统计面板：DB 大小、session 数、feature 状态 |
| `wukong backup` | 对 `wukong.db` 做带时间戳的 `.bak` 拷贝 |
| `wukong system-check` | 系统就绪诊断：config / provider / DB / sandbox / 工作目录 |
| `wukong bench` | 模型延迟基准（tokens/s、延迟统计） |
| `wukong config validate` / `config show` | 配置完整校验（与启动路径一致）与查看 |

### 12.2 常见问题

| 现象 | 排查 |
|------|------|
| 连不上 LLM | `wukong system-check` 看 provider 项；确认 `api_key`/`base_url`；`curl` 直连 provider |
| `token tailoring overflow` | 调大 provider `context_window` |
| `SQLITE_BUSY` 锁等待 | `busy_timeout=5000ms` 已兜底；若频繁，检查是否有第二个进程写同一 `wukong.db` |
| 健康检查 503 | `curl /healthz` 看 `components`，定位失败子系统 |
| 容器内 Chromium 崩溃 | 确认 `CHROMIUM_FLAGS="--no-sandbox --disable-gpu --disable-dev-shm-usage"`；增加 `/dev/shm` 或 tmpfs |
| 沙箱不生效 | 确认内核/平台支持（见 §10.4），不支持时告警并以无沙箱运行 |
| 浏览器检测（`needNosandbox`） | 容器环境（docker/K8s）自动检测，`isContainerized()` 决定是否加 `--no-sandbox` |
| 回环检测告警 | Agent 自引用检测——查看 `--debug` 日志中的 loopback 警告 |

### 12.3 Go 运行时调优

| 环境变量 | 作用 | 建议 |
|----------|------|------|
| `GOMAXPROCS` | 调度器 P 数 | 容器中显式设置 |
| `GOMEMLIMIT` | 软内存上限 | 设为容器 memory limit 的 ~90% |

---

## 13. 多实例与水平扩展

### 13.1 共享 WAL 的限制

SQLite WAL 模式支持多进程读但只允许单进程写。**不推荐多个 Wukong 进程共享同一个 `wukong.db` 文件**——`busy_timeout` 虽然能处理瞬态锁冲突，但高并发下写竞争会导致频繁的 `SQLITE_BUSY`。

### 13.2 Redis 后端实现水平扩展

当需要多实例部署时，将 session 切换到 Redis 后端（`session.backend: redis` + `session.redis_url`，见 §5.2；memory 的 redis 后端当前未实现，多实例下各实例仍使用本地 SQLite）：

```
                          ┌──────────────┐
    Wukong Instance A ────┤              │
    (wukong server)       │   Redis      │  ← session 共享
                          │   :6379      │
    Wukong Instance B ────┤              │
    (wukong server)       │              │
                          └──────────────┘
```

每个实例仍然维护自己的本地 `wukong.db`（用于 memory / todo / recall / cortex 等本地子系统），会话数据通过 Redis 共享。

### 13.3 备份恢复

```bash
# 内置备份
wukong backup    # 生成 ~/.config/wukong/wukong-YYYYMMDD-HHMMSS.bak

# 手动冷拷贝（推荐先停服）
cp ~/.config/wukong/wukong.db /backup/wukong-$(date +%F).db
cp ~/.config/wukong/wukong.db-wal /backup/ 2>/dev/null || true
cp ~/.config/wukong/wukong.db-shm /backup/ 2>/dev/null || true
```

---

## 14. 相关文档

| 主题 | 文档 |
|------|------|
| 配置项完整参考 | [CONFIG.md](./CONFIG.md) |
| 架构设计 | [ARCHITECTURE.md](./ARCHITECTURE.md)（含关键流程实现细节） |
| CLI 与 TUI 使用 | [CLI_TUI.md](./CLI_TUI.md) |
| 项目总览 | [README.md](../README.md) |
| 记忆系统 | [MEMORY_ARCHITECTURE.md](./MEMORY_ARCHITECTURE.md) |

---

> **版本**: v0.3.1 | **最后更新**: 2026-08-25
