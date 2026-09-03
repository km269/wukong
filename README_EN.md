# Wukong — Memory-First · Orchestration-Driven · Defense-in-Depth · Bidirectional Discovery

> A local-first, framework-composed, deeply extensible open-source AI Agent platform
>
> [English](README_EN.md) | [简体中文](README.md)
>
> Go 1.26 | 30+ internal packages | 5 public packages | 35 config sections
> CLI: 32 top-level commands + 60+ subcommands | Dependencies: 29 direct + 105 indirect

**Wukong** (named after the Monkey King) is a new-generation AI Agent platform built in Go. It is not just a chat bot — it is a complete **local-first, memory-driven, multi-mode orchestration** AI Agent development framework.

---

## Table of Contents

1. [Design Philosophy](#design-philosophy)
2. [Core Values](#core-values)
3. [System Architecture](#system-architecture)
4. [Core Capabilities](#core-capabilities)
5. [Quick Start](#quick-start)
6. [Technology Stack](#technology-stack)
7. [Subsystem Highlights](#subsystem-highlights)
8. [Documentation Index](#documentation-index)
9. [License](#license)

---

## Design Philosophy

Wukong's design follows seven core principles, each reflected in concrete engineering decisions:

| Principle | Belief | Key Engineering Decision |
|------|----------|-------------|
| **Memory-first** | Agent intelligence comes from cross-session knowledge accumulation | Dual-engine three-tier memory: tRPC Memory + CortexDB Stack |
| **Framework composition** | Every component should be replaceable | CoreLoop dependency injection; interface isolation across subsystems |
| **Multi-agent native** | Orchestration is a first-class citizen | 10 explicit orchestration modes + HITL |
| **Evolving intelligence** | Skills should learn from failures | LLM analysis → auto patch → versioning → hot reload |
| **Bidirectional discovery** | Discover others, be discovered | ARD: federated search + RegistryServer publishing |
| **Open interop** | Standard protocols enable ecosystem interoperability | ANP: DID identity + capability negotiation + E2EE |
| **Standardized knowledge** | Knowledge should have a standard shape | OKF v0.1: Markdown + YAML frontmatter knowledge bundles |

---

## Core Values

### 1. Memory-First Intelligence

A dual-engine three-tier memory system gives the agent cross-session knowledge accumulation:

- **Short-term**: MemoryFlow session transcript + 3-layer wake-up (Identity / Recalled memories / Session context)
- **Mid-term**: CortexStore HNSW vectors + FTS5 full-text + RRF/MMR fusion + Cross-Encoder re-ranking
- **Long-term**: tRPC Memory auto-extraction (AutoExtract) + SmartCleanup
- **Structured**: GraphFlow knowledge-graph construction (SPARQL queries)

### 2. Native Multi-Agent Orchestration

10 native orchestration modes cover everything from a single agent to complex team workflows:

- `single`: single agent
- `chain`: planner → executor → reviewer
- `parallel`: concurrent multi-agent execution
- `cycle`: iterative refinement loop
- `graph`: conditionally-routed DAG
- `team_coordinator`: leader delegation
- `team_swarm`: automatic transfer mode
- `claude_code` / `codex` / `dify`: Claude Code CLI / OpenAI Codex / Dify platform integrations

### 3. Skill Self-Evolution

An LLM-driven closed-loop self-learning mechanism (confidence gating + cooldown windows + daily limits + safe patching) lets the agent learn from failures and keep improving.

### 4. Bidirectional Discovery and Open Interop

The ARD protocol implements bidirectional agent discovery (federated search + RegistryServer publishing), with native support for open protocols — MCP, A2A, ANP — secured by DID identity + HTTP signatures + E2EE encryption.

### 5. Defense-in-Depth Security

A 5-layer defense-in-depth stack:

- Guard security checker (4 permission modes + token-level command analysis)
- JS sandbox isolation (goja)
- OS sandboxes (Landlock / Seatbelt / Low IL)
- `.wukongignore` file denylist
- SSRF protection + constant-time API key comparison

---

## System Architecture

```
User input → Access layer (CLI/TUI/Gateway) → CoreLoop orchestration engine
                                           ↓
                        ┌──────────────────┴──────────────────┐
                        │                                      │
                    Prepare phase                          Execute phase
                  (4-way context injection)              (task execution)
                        │                                      │
                  • ContextRevision                      • Runner
                  • MemoryFlow.WakeUp                    • LLM calls
                  • Cortex/Recall search                 • Tool calls
                  • Memory injection                     • Security checks
                        │                                      │
                        └──────────────────┬──────────────────┘
                                           ↓
                                     Finalize phase
                                     (result handling)
                                           │
                                     • Message storage
                                     • Fact promotion
                                     • Knowledge-graph extraction
                                     • Evolution records
                                           ↓
                                     Return phase
                                     (results returned)
```

---

## Core Capabilities

### Orchestration & Agents

| Dimension | Solution |
|------|------|
| **Orchestration modes** | 10: `single` / `chain` / `parallel` / `cycle` / `graph` / `team_coordinator` / `team_swarm` / `claude_code` / `codex` / `dify` |
| **LLM backends** | 8: OpenAI / Anthropic / Google / DeepSeek / Ollama / LMStudio / vLLM / ACP (OpenAI-compatible clouds via `type: openai`: SiliconFlow / OpenRouter / Groq / Moonshot / Zhipu, etc.) |
| **CoreLoop** | Four-phase execution: Prepare → Execute → Finalize → Return |
| **Recipe system** | YAML-defined sub-agents + hot reload + version evolution |
| **Flow DSL** | User-writable YAML flows (agent + capability nodes, conditional edges) |
| **HITL** | Human-in-the-loop with native pause at decision points |

### Memory & Knowledge

| Dimension | Solution |
|------|------|
| **Memory system** | Dual-engine three-tier: tRPC Memory × CortexDB (HNSW + FTS5 + RDF) |
| **Short-term** | MemoryFlow: session transcript + 3-layer wake-up + OKF injection |
| **Mid-term** | CortexStore: HNSW vector index + FTS5 full-text |
| **Long-term** | tRPC Memory: AutoExtract + SmartCleanup |
| **Structured** | GraphFlow: entity extraction → RDF graph → SPARQL |
| **Knowledge format** | OKF v0.1: 6 packages, 7 integration points (okf/ard/cortex×2/evolution/knowledge/skill) |

### Security & Sandbox

| Dimension | Solution |
|------|------|
| **5-layer defense** | Guard → goja JS sandbox → OS sandbox → .wukongignore → OS permissions |
| **Permission modes** | 4: `auto` / `smart` / `manual` / `chat_only` |
| **JS sandbox** | goja: API whitelist + 128MB memory cap + 5 concurrency + ReDoS protection |
| **OS sandbox** | Cross-platform: Linux Landlock / macOS Seatbelt / Windows LowIL |
| **Prompt injection** | Guardrail review mode |

### Website Cloning & Packaging

| Dimension | Solution |
|------|------|
| **Clone engine** | Chrome render → Settle wait → DOM cleanup → single-pass rewrite+discovery → asset filtering → dedup → resumable crawl |
| **Anti-anti-bot** | 10 layers: Stealth / Preflight / Antibot 5-level escalation / cf_clearance / 161-UA pool / sec-ch-ua / Referer / ErrNotHTML routing / Settle network-idle wait / Proxy pool |
| **Pagination** | 6 kinds: query param / path-style / offset-limit / cursor / seek / token |
| **Asset download** | 4-layer fallback: HTTP direct → CDP Network.loadNetworkResource → img tag → fetch API |
| **ZIM packaging** | Kiwix-compatible (ZIM v6, zstd codec 5): metadata + icons + counters + incremental cluster cache |

### Extensions & Interop

| Dimension | Solution |
|------|------|
| **Built-in extensions** | 12: developer / computer_controller / memory / auto_visualiser / tutorial / top_of_mind / code_mode / apps / web / agent_tools / ard / cortex (`web` bundles five search backends: aggregate_search / bing / google / searxng / tavily) |
| **MCP extensions** | MCP Broker + standalone MCP Server (:3401) + ACP-MCP Bridge (:3400) |
| **Protocol endpoints** | 7 (6 listening + 1 outbound): A2A (:9090) / ACP (:9091) / AG-UI SSE (:8080, with a built-in web console at `/`) / ACP-MCP (:3400) / MCP Server (:3401) / ANP (:9092) / Gateway (Feishu WS outbound) |
| **Message gateway** | Plugin-style Channel architecture: Feishu WebSocket long connection (internal goroutine, no dedicated HTTP port) |
| **Agent interop** | ANP protocol stack: DID identity + capability negotiation + E2EE + HTTP signatures |
| **Bidirectional discovery** | ARD: federated search + local Catalog + RegistryServer publishing |

### Configuration & Storage

| Dimension | Solution |
|------|------|
| **Configuration** | 35+ sections · layered loading precedence · validation · env var expansion (20+ secret field classes) |
| **Storage** | Single-file `wukong.db` (SQLite WAL mode), versioned schema migrations (`wukong migrate`) |
| **Optional backends** | Redis (session/memory) / COS (artifacts) |

---

## Quick Start

### Install

```bash
go install github.com/km269/wukong/cmd/wukong@latest
```

### Initial Configuration

```bash
# Interactive configuration wizard
wukong configure

# Validate configuration
wukong config validate
```

### Everyday Use

```bash
# Interactive session (TUI)
wukong session
wukong session --provider deepseek --model deepseek-chat

# One-shot execution
wukong run --prompt "Analyze the project structure"

# Show effective configuration
wukong config show
```

### Website Cloning

```bash
# Clone a website
wukong apps clone https://example.com --max-pages 50 --max-depth 2

# Preview the cloned result
wukong apps view example.com

# Package as ZIM (Kiwix-compatible)
wukong apps pack example.com --format zim --compress
```

### Skill Evolution

```bash
# Evolution engine status
wukong evolution status

# Evolution history and version list for a skill
wukong evolution history <skill-name>
wukong evolution versions <skill-name>

# Diff two versions / roll back
wukong evolution diff <skill-name> <version1> <version2>
wukong evolution rollback <skill-name> <version>

# Evolution log (log.json)
wukong evolution log <skill-name>
```

### Server Mode

```bash
# Start the headless server
wukong server

# Pin provider / model and inference parameters
wukong server --provider deepseek --model deepseek-chat --temperature 0.7 --max-tokens 8192

# Attach to an existing session / custom config / disable streaming
wukong server --session-id my-server --config wukong.yaml --no-stream
```

---

## Technology Stack

### Core Frameworks

| Category | Choice | Version | Purpose |
|------|------|------|------|
| Agent framework | tRPC-Agent-Go | v1.11.2 | Agent orchestration, tool calling, session management |
| MCP protocol | tRPC-MCP-Go | v0.0.16 | Model Context Protocol |
| A2A protocol | tRPC-A2A-Go (indirect) | v0.2.6-pre | Agent-to-Agent communication |
| Memory engine | CortexDB | v2.25.0 | HNSW vectors + FTS5 full-text + RDF graph |
| Knowledge format | OKF | v0.1 | Open Knowledge Format |
| CLI framework | Cobra + Viper | v1.9.1 / v1.20.1 | Command line + configuration |
| TUI framework | Bubble Tea + Bubbles | v1.3.10 / v0.21.0 | Terminal UI |

### Browser & Automation

| Category | Choice | Version | Purpose |
|------|------|------|------|
| Browser driver | Rod | v0.116.2 | Headless Chrome control (primary backend) |
| Browser driver | Chromedp | v0.15.1 | Fallback CDP client |
| Anti-fingerprinting | UTLS | v1.5.0 | TLS fingerprint forging |
| robots.txt | robotstxt | v1.1.2 | robots.txt parsing |

### Data Storage

| Category | Choice | Version | Purpose |
|------|------|------|------|
| Database | SQLite (modernc) | v1.38.2 | Pure-Go SQLite, no CGO |
| Vector index | CortexDB HNSW | v2.25.0 | Hierarchical navigable small-world graph |
| Full-text search | FTS5 | - | SQLite full-text extension |
| Knowledge graph | RDF / SPARQL | - | Resource Description Framework + query language |
| Cache | VectorCache | - | Incremental vector cache |
| Redis | go-redis | v9.12.1 | Optional session/memory backend |

### Security & Sandbox

| Category | Choice | Version | Purpose |
|------|------|------|------|
| JS sandbox | goja | - | Pure-Go JavaScript interpreter |
| Linux sandbox | Landlock | - | Linux kernel security module |
| macOS sandbox | Seatbelt | - | macOS sandbox framework |
| Windows sandbox | Low Integrity Level | - | Windows low-integrity level |
| Cryptography | x/crypto | v0.51.0 | Ed25519 / X25519 / ChaCha20 |
| HTTP signatures | RFC 9421 | - | HTTP message signing standard |

### Observability

| Category | Choice | Version | Purpose |
|------|------|------|------|
| Tracing | OpenTelemetry | v1.43.0 | Distributed tracing standard |
| Observability platform | Langfuse | - | LLM application observability |
| Logging | slog | - | Go standard structured logging |

---

## Subsystem Highlights

### CoreLoop Orchestration Engine

A four-phase execution loop coordinating every subsystem:

```
User message
    │
    ├─ Phase 1: Prepare (context preparation)
    │   ├─ MemoryFlow.IngestTurn
    │   ├─ MemoryFlow.WakeUp (3 layers)
    │   ├─ Recall/Cortex.Search
    │   ├─ tRPC Memory.ReadMemories
    │   ├─ OKF KnowledgeIndexInjector
    │   └─ GraphFlow (optional)
    │
    ├─ Phase 2: Execute
    │   ├─ runner.Run()
    │   ├─ LLM → Tool Calls
    │   ├─ Guard.Check (security checks)
    │   ├─ ToolSearch (tool filtering)
    │   └─ EvolutionTracker (trajectory capture)
    │
    ├─ Phase 3: Finalize
    │   ├─ StoreMessage
    │   ├─ IngestTurn
    │   ├─ PromoteFacts
    │   ├─ GraphFlow.AutoExtract
    │   └─ Evolution Record
    │
    └─ Phase 4: Return
        └─ contextMgr.AfterRun
```

### Evolution Skill-Evolution Engine

An event-driven skill self-evolution system:

| Component | Location | Purpose |
|------|------|------|
| **EvolutionTracker** | `internal/agent/evolution_tracker.go` | Runner event plugin; async trajectory capture |
| **EvolutionEngine** | `internal/evolution/engine.go` | Async analysis scheduling (cooldown, daily limits) |
| **EvolutionAnalyzer** | `internal/evolution/analyzer.go` | LLM trajectory analysis, patch suggestions |
| **EvolutionPatcher** | `internal/evolution/patcher.go` | Patch application (dedup, version backup, concurrency-safe) |
| **VersionStore** | `internal/evolution/store.go` | SQLite version persistence and history |
| **OKF Log** | `internal/evolution/patcher.go` | Dual-format change log (Markdown + JSON) |

**Key properties**: event-driven tracking without intruding on the main loop; hash dedup prevents unbounded patch growth; at most 5 patch sections retained; concurrency-safe (sync.Mutex); JSON log export for external consumers.

### Dual-Engine Three-Tier Memory

```
┌─────────────────────────────────────────────────────────────┐
│  Short-term — MemoryFlow                                     │
│  Session transcript + 3-layer wake-up + OKF | Lifetime: session │
├─────────────────────────────────────────────────────────────┤
│  Mid-term — CortexStore                                      │
│  HNSW vectors + FTS5 | Lifetime: cross-session, recallable   │
├─────────────────────────────────────────────────────────────┤
│  Long-term — tRPC Memory                                     │
│  AutoExtract + SmartCleanup | Lifetime: until cleanup        │
├─────────────────────────────────────────────────────────────┤
│  Structured (Graph) — GraphFlow                              │
│  Entity extraction → RDF → SPARQL | Lifetime: permanent      │
└─────────────────────────────────────────────────────────────┘
```

**SmartCleanup four-dimension scoring**: recency 40% + references 30% + importance 20% + length 10%; dynamic TTL (≥5 references → TTL×2, ≥2 → ×1.5, 0 → ×0.5); triggered at 80% capacity, cleaning down to 60%.

### ANP — Agent Network Protocol

A complete agent-interop protocol stack:

| Layer | Component | Location | Purpose |
|------|------|------|------|
| Bridge | ANPAdapter | `internal/summon/anp_adapter.go` | JSON-RPC 2.0 ↔ A2A bridging |
| Security | E2EE + HTTP Sign | `internal/summon/e2ee.go` + `internal/ard/http_sign.go` | X25519 + ChaCha20-Poly1305 / RFC 9421 |
| Negotiation | Meta-Protocol | `internal/summon/meta_protocol.go` | JSON-RPC 2.0 capability negotiation |
| Discovery | ADP | `internal/ard/adp.go` | /.well-known/agent-descriptions |
| Identity | DID | `internal/ard/did.go` | did:wba (Ed25519 + X25519) |

### OKF — Open Knowledge Format v0.1

A complete implementation of the Google OKF specification with 6 system integrations:

| Integration | Location | Purpose |
|--------|------|------|
| **OKF core** | `internal/okf/` | Bundle load/write, Concept parsing |
| **Skill compat** | `internal/skill/` | SKILL.md with type: skill |
| **Knowledge interop** | `internal/knowledge/` | RAG knowledge base ↔ OKF bundles |
| **Index injection** | `internal/cortex/` | OKF index.md injected into MemoryFlow wake-up context |
| **Change tracking** | `internal/evolution/` | log.md + log.json dual format |
| **Federated discovery** | `internal/ard/` | OKF bundles registered as ARD CatalogEntries |

### Gateway Message Gateway

A plugin-style Channel architecture with a unified pipeline:

```
Platform Channels (Feishu WebSocket)
    │
    ▼
GatewayServer (transport-agnostic)
    ├─ Signature verification
    ├─ URL verification (echostr)
    ├─ Message parsing
    ├─ Dedup (MessageID + TTL)
    ├─ Identity mapping
    ├─ RateLimiter (sliding window + concurrency control)
    ├─ SessionStore
    ├─ CoreLoop.Run
    └─ Reply / streaming push
```

---

## Documentation Index

The full documentation set is currently maintained in Chinese (paths below); an English translation effort is in progress (this file is its front door).

### Core Documents

| Document | Description |
|------|------|
| [Architecture](docs/ARCHITECTURE.md) | System architecture, subsystem implementations, module dependencies, data flows *(Chinese)* |
| [Configuration Manual](docs/CONFIG.md) | 35 config sections, field-level reference *(Chinese)* |
| [CLI & TUI Architecture](docs/CLI_TUI.md) | Command tree · TUI architecture · startup sequence · event pipeline *(Chinese)* |
| [API Reference](docs/API_REFERENCE.md) | CoreLoop · Provider · Extension · Security Go interfaces *(Chinese)* |
| [Developer Guide](docs/DEVELOPER_GUIDE.md) | Environment setup · project layout · common tasks · debugging *(Chinese)* |
| [Deployment](docs/DEPLOYMENT.md) | Docker · binaries · configuration · health checks · troubleshooting *(Chinese)* |

### Topic Guides

| Document | Description |
|------|------|
| [Provider Capability Matrix](docs/PROVIDERS.md) | LLM provider protocols, capabilities, escape hatches *(Chinese, mostly tabular)* |
| [Website Cloning Guide](docs/CLONE_GUIDE.md) | Clone engine architecture · pagination · asset download *(Chinese)* |
| [Web Operations Deep Dive](docs/WEB_OPERATIONS_ANALYSIS.md) | Browser/clone/anti-bot/search/HTTP full-chain analysis *(Chinese)* |
| [Anti-Anti-Bot Deep Dive](docs/ANTIBOT_GUIDE.md) | 5-level escalation · WAF fingerprints · detection techniques *(Chinese)* |
| [Memory Architecture](docs/MEMORY_ARCHITECTURE.md) | Three-tier memory · CortexDB internals · SmartCleanup *(Chinese)* |
| [OKF Knowledge Format](docs/OKF_GUIDE.md) | OKF v0.1 spec · bundle structure · integrations *(Chinese)* |
| [Yao Comparison & Roadmap](docs/YAO_COMPARISON_AND_ROADMAP.md) | Comparison with YaoApp/yao · optimization direction · capability-bus design *(Chinese)* |

---

## License

[GNU AGPL-3.0](docs/LICENSE)
