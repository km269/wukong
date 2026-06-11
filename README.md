# Wukong 🐵

**Wukong** is a local-first, extensible AI Agent platform built in Go, inspired by [Goose](https://github.com/aaif-goose/goose) and powered by the [tRPC](https://github.com/trpc-group) ecosystem:

| Framework | Purpose |
|-----------|---------|
| [tRPC-Agent-Go](https://github.com/trpc-group/trpc-agent-go) | Core Agent framework: Runner, Session, Memory, Tool system |
| [tRPC-MCP-Go](https://github.com/trpc-group/trpc-mcp-go) | MCP protocol implementation for tool extensions |
| [tRPC-A2A-Go](https://github.com/trpc-group/trpc-a2a-go) | Agent-to-Agent communication protocol |

## Features

- **Interactive Agent Loop** — Human Request → LLM Thinking → Tool Call → Result → Response
- **MCP Extension System** — Built-in developer tools + external MCP server support
- **Multi-Provider** — OpenAI-compatible APIs, Ollama, DeepSeek, and more
- **Session & Memory** — SQLite-persisted conversation history with long-term memory
- **Todo Tracking** — Agent can create, update, and complete tasks
- **Summon Delegation** — Sub-agent task delegation for complex workflows
- **Context Optimization** — Token management, summarization, and history pruning
- **Modern CLI/TUI** — Bubbletea-powered terminal interface with streaming output

## Architecture

```
┌──────────────────────────────────────────────┐
│          CLI / TUI (Cobra + Bubbletea)        │
├──────────────────────────────────────────────┤
│              Wukong Core Engine               │
├──────────────────────────────────────────────┤
│ Agent Loop  │ Context Mgr │ Extension Mgr     │
├──────────────────────────────────────────────┤
│         tRPC-Agent-Go Runner                  │
├──────┬──────┬──────┬───────┬─────────────────┤
│ LLM  │Session│Memory│ Tool  │ Callbacks       │
│Agent │Service│Service│System │                 │
├──────┴──────┴──────┼───────┴─────────────────┤
│   tRPC-MCP-Go      │   tRPC-A2A-Go           │
│   (MCP Client)     │   (Agent-to-Agent)      │
└────────────────────┴─────────────────────────┘
```

## Quick Start

### Prerequisites

- Go 1.24+
- An LLM API key (OpenAI, DeepSeek, or Ollama)

### Installation

```bash
git clone https://github.com/km269/wukong.git
cd wukong
go build -o wukong ./cmd/wukong/
```

### Configuration

Create `~/.config/wukong/config.yaml` or copy from the project root:

```yaml
default_provider: openai
providers:
  - name: openai
    type: openai
    base_url: https://api.openai.com/v1
    api_key: ${OPENAI_API_KEY}
    model: gpt-4o
extensions:
  - name: developer
    type: builtin
    enabled: true
session:
  backend: sqlite
  db_path: wukong.db
```

### Usage

```bash
# Start an interactive session
./wukong session

# Use a specific provider
./wukong session --provider deepseek

# Resume a previous session
./wukong session --session-id <session-id>

# Configure interactively
./wukong configure
```

### TUI Commands

| Key/Command | Action |
|-------------|--------|
| `Ctrl+D` | Send message |
| `Ctrl+C` | Quit |
| `/new` | Start new session |
| `/clear` | Clear screen |
| `/help` | Show help |
| `/exit` | Quit |

## Built-in Developer Tools

| Tool | Description |
|------|-------------|
| `file_read` | Read file contents |
| `file_write` | Write content to file |
| `command_execute` | Run shell commands |
| `code_search` | Search code patterns (requires `rg`) |
| `directory_list` | List directory contents |
| `todo_create` | Create a task |
| `todo_update` | Update a task |
| `todo_complete` | Complete a task |
| `todo_list` | List all tasks |

## Project Structure

```
wukong/
├── cmd/wukong/main.go              # Entry point
├── internal/
│   ├── agent/
│   │   ├── loop.go                 # Core agent loop engine
│   │   ├── context.go              # Context/token manager
│   │   └── loop_test.go            # Integration tests
│   ├── cli/
│   │   ├── root.go                 # Cobra root command
│   │   ├── session.go              # Session command + bootstrap
│   │   ├── configure.go            # Configure command
│   │   └── tui/
│   │       ├── model.go            # Bubbletea TUI model
│   │       ├── view.go             # View rendering + styles
│   │       └── update.go           # Event processing
│   ├── config/
│   │   └── config.go               # Viper config loader
│   ├── extension/
│   │   ├── manager.go              # Extension lifecycle manager
│   │   └── builtin/
│   │       ├── developer.go        # Built-in developer tools
│   │       └── registry.go         # Built-in extension registry
│   ├── memory/
│   │   └── store.go                # Memory service wrapper
│   ├── provider/
│   │   └── factory.go              # Model provider factory
│   ├── session/
│   │   └── store.go                # Session service wrapper
│   ├── summon/
│   │   └── delegate.go             # Sub-agent delegation
│   └── todo/
│       └── tool.go                 # Todo system (tools + store)
├── config.yaml                     # Default configuration
├── go.mod
└── go.sum
```

## Comparison with Goose

| Feature | Goose (Rust) | Wukong (Go) |
|---------|-------------|-------------|
| Language | Rust | Go |
| Agent Engine | Custom Rust loop | tRPC-Agent-Go Runner |
| MCP Client | goose-mcp crate | tRPC-MCP-Go + agent/mcp |
| Session | Custom implementation | tRPC-Agent-Go Session |
| Memory | Built-in extension | tRPC-Agent-Go Memory |
| Configuration | ~/.config/goose/config.yaml | ~/.config/wukong/config.yaml |
| TUI | Custom (Electron + Rust) | Bubbletea (pure Go) |
| Providers | 15+ (Rust providers crate) | OpenAI-compatible |

## License

This project is for educational purposes and research.

## Acknowledgments

- [Goose](https://github.com/aaif-goose/goose) — The original AI Agent that inspired this project
- [tRPC-Agent-Go](https://github.com/trpc-group/trpc-agent-go) — Core Go Agent framework
- [tRPC-MCP-Go](https://github.com/trpc-group/trpc-mcp-go) — MCP protocol implementation
- [tRPC-A2A-Go](https://github.com/trpc-group/trpc-a2a-go) — A2A protocol implementation
