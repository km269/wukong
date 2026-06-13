// Package config provides configuration management for wukong.
//
// It defines the complete configuration structure for all subsystems
// (providers, extensions, agent, security, storage, etc.) and provides
// a Viper-based Loader for YAML configuration files with environment
// variable override support.
//
// Configuration priority (highest to lowest):
//  1. CLI flags (--provider, --model, --temperature, --max-tokens, --no-stream)
//  2. Environment variables (WUKONG_ prefix, e.g. WUKONG_DEFAULT_PROVIDER)
//  3. YAML config file (--config flag or default search paths)
//  4. Built-in defaults (setDefaults())
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// ============================================================================
// Path Utilities
// ============================================================================

// ResolvePath converts a relative path to an absolute path.
// If the path is already absolute, it is returned as-is.
// This ensures all modules sharing the same file (e.g. wukong.db)
// resolve to the same absolute location regardless of the working directory.
func ResolvePath(rawPath string) string {
	if filepath.IsAbs(rawPath) {
		return rawPath
	}
	absPath, err := filepath.Abs(rawPath)
	if err != nil {
		return rawPath
	}
	return absPath
}

// ============================================================================
// Top-Level Configuration
// ============================================================================

// WukongConfig is the root configuration structure containing all
// subsystem configurations for the wukong AI agent platform.
type WukongConfig struct {
	// DefaultProvider is the name of the default LLM provider.
	// Must match a ProviderConfig.Name in the Providers list.
	DefaultProvider string `mapstructure:"default_provider"`
	// LogLevel controls the logging verbosity: "debug", "info",
	// "warn", "error". Overridden by --debug/--quiet CLI flags.
	// Default: "info".
	LogLevel string `mapstructure:"log_level"`

	// Providers lists all available LLM backend configurations.
	Providers []ProviderConfig `mapstructure:"providers"`

	// Extensions defines all MCP extensions (built-in and external).
	Extensions []ExtensionConfig `mapstructure:"extensions"`

	// Agent controls the core agent loop behavior and LLM parameters.
	Agent AgentConfig `mapstructure:"agent"`

	// Security defines tool execution permissions and safety policies.
	Security SecurityConfig `mapstructure:"security"`

	// Session configures conversation history storage.
	Session SessionConfig `mapstructure:"session"`

	// Memory configures long-term knowledge persistence.
	Memory MemoryConfig `mapstructure:"memory"`

	// Todo configures the task tracking subsystem.
	Todo TodoConfig `mapstructure:"todo"`

	// Recall configures cross-session chat history search.
	Recall RecallConfig `mapstructure:"recall"`

	// Revision configures context window management and token optimization.
	Revision RevisionConfig `mapstructure:"revision"`

	// Browser configures web automation and file caching.
	Browser BrowserConfig `mapstructure:"browser"`

	// Visualiser configures chart/diagram generation.
	Visualiser VisualiserConfig `mapstructure:"visualiser"`

	// Tutorial configures the interactive tutorial system.
	Tutorial TutorialConfig `mapstructure:"tutorial"`

	// TopOfMind configures persistent instruction injection.
	TopOfMind TopOfMindConfig `mapstructure:"top_of_mind"`

	// CodeMode configures the JavaScript code execution sandbox.
	CodeMode CodeModeConfig `mapstructure:"code_mode"`

	// Apps configures custom HTML standalone applications.
	Apps AppsConfig `mapstructure:"apps"`

	// Summon configures sub-agent delegation and A2A remotes.
	Summon SummonConfig `mapstructure:"summon"`

	// Skill configures the tRPC Agent Skill repository system.
	Skill SkillConfig `mapstructure:"skill"`

	// Knowledge configures the RAG knowledge retrieval system.
	Knowledge KnowledgeConfig `mapstructure:"knowledge"`

	// Dify configures the Dify AI platform integration.
	Dify DifyConfig `mapstructure:"dify"`

	// Workflow configures multi-mode agent orchestration.
	Workflow WorkflowConfig `mapstructure:"workflow"`

	// A2AServer configures the local A2A protocol server.
	A2AServer A2AServerConfig `mapstructure:"a2a_server"`

	// AGUI configures the AG-UI SSE server for web-based chat UIs.
	AGUI AGUIConfig `mapstructure:"agui"`

	// ACPServer configures the Agent Client Protocol server endpoint.
	ACPServer ACPServerConfig `mapstructure:"acp_server"`

	// ACPMCP configures the MCP bridge that exposes extensions
	// as an MCP Server for ACP agents.
	ACPMCP ACPMCPConfig `mapstructure:"acp_mcp"`

	// Telemetry configures OpenTelemetry observability.
	Telemetry TelemetryConfig `mapstructure:"telemetry"`

	// Eval configures the evaluation/regression testing system.
	Eval EvalConfig `mapstructure:"eval"`

	// Artifact configures artifact storage backend settings.
	ArtifactConfig ArtifactConfig `mapstructure:"artifact"`

	// Observability configures enhanced observability (Langfuse, etc.).
	Observability ObservabilityConfig `mapstructure:"observability"`

	// ProjectDir is the directory for project tracking data.
	// Default: ~/.config/wukong/ (resolved at runtime).
	ProjectDir string `mapstructure:"project_dir"`
}

// ============================================================================
// Provider Configuration
// ============================================================================

// ProviderConfig defines a connection to an LLM backend.
// Supported types: openai, anthropic, google, deepseek, ollama, lmstudio.
// API keys support ${ENV_VAR} expansion for secrets management.
type ProviderConfig struct {
	// Name is the unique identifier for this provider (referenced by default_provider).
	Name string `mapstructure:"name"`
	// Type is the provider backend type (openai, anthropic, google, deepseek, ollama, lmstudio, acp).
	Type string `mapstructure:"type"`
	// BaseURL is the API endpoint base URL (auto-filled for known providers if empty).
	BaseURL string `mapstructure:"base_url"`
	// APIKey is the authentication key (supports ${ENV_VAR} expansion).
	APIKey string `mapstructure:"api_key"`
	// Model is the default model name for this provider.
	Model string `mapstructure:"model"`

	// ---- ACP-specific fields (only when Type="acp") ----

	// AgentURL is the ACP agent server endpoint URL
	// (e.g., "http://localhost:4000").
	AgentURL string `mapstructure:"agent_url"`
	// MCPPort is the port where the ACP MCP Bridge listens
	// for incoming MCP tool calls from the ACP agent.
	MCPPort string `mapstructure:"mcp_port"`
	// AgentAuth is the authentication method for the ACP agent.
	AgentAuth string `mapstructure:"agent_auth"`
}

// ============================================================================
// Extension Configuration
// ============================================================================

// ExtensionConfig defines an MCP extension (built-in or external).
// Built-in extensions use type: builtin and are created via the factory.
// External extensions use type: external with MCP transport protocol.
// When mcp_broker is true, external tools are not directly exposed;
// instead, a broker tool (mcp_call/mcp_list_servers) is provided for
// on-demand discovery and invocation.
type ExtensionConfig struct {
	// Name uniquely identifies the extension.
	Name string `mapstructure:"name"`
	// Type is "builtin" or "external".
	Type string `mapstructure:"type"`
	// Transport is the MCP transport protocol: stdio, sse, streamable.
	Transport string `mapstructure:"transport"`
	// Command is the executable for stdio transport.
	Command string `mapstructure:"command"`
	// Args are the command-line arguments for stdio transport.
	Args []string `mapstructure:"args"`
	// URL is the server URL for sse/streamable transport.
	URL string `mapstructure:"url"`
	// Env defines additional environment variables for the extension process.
	Env map[string]string `mapstructure:"env"`
	// Enabled controls whether the extension is active.
	Enabled bool `mapstructure:"enabled"`
	// Timeout is the tool execution timeout for this extension.
	Timeout time.Duration `mapstructure:"timeout"`
	// Deeplink is a wukong://extension? URL for one-click installation.
	Deeplink string `mapstructure:"deeplink"`
	// Permissions defines fine-grained tool-level access control.
	Permissions []ToolPermission `mapstructure:"permissions"`
	// MCPBroker enables on-demand tool discovery via MCP broker tools
	// (mcp_list_servers, mcp_list_tools, mcp_call) instead of exposing
	// all remote tools directly. Useful when connecting to many MCP
	// servers to keep the tool list manageable.
	MCPBroker bool `mapstructure:"mcp_broker"`
	// MCPToolFilter specifies which tools to include from the MCP server.
	// When empty, all tools are included. Supports glob patterns.
	MCPToolFilter []string `mapstructure:"mcp_tool_filter"`
	// MCPToolExclude specifies which tools to exclude from the MCP server.
	// Supports glob patterns.
	MCPToolExclude []string `mapstructure:"mcp_tool_exclude"`
	// MCPSessionReconnect enables automatic session reconnection on
	// connection loss (SSE/streamable transports). Default: false.
	MCPSessionReconnect bool `mapstructure:"mcp_session_reconnect"`
	// MCPSessionReconnectAttempts is the max reconnection attempts.
	// Default: 3.
	MCPSessionReconnectAttempts int `mapstructure:"mcp_session_reconnect_attempts"`
}

// ToolPermission defines allow/deny for a specific tool within an extension.
type ToolPermission struct {
	// Tool is the tool name.
	Tool string `mapstructure:"tool"`
	// Allowed is true to allow, false to deny.
	Allowed bool `mapstructure:"allowed"`
}

// ============================================================================
// Agent Configuration
// ============================================================================

// AgentConfig controls the core agent loop behavior, LLM generation
// parameters, and tool execution retry policies.
type AgentConfig struct {
	// MaxLLMCalls is the maximum number of LLM API calls per run (0 = unlimited).
	MaxLLMCalls int `mapstructure:"max_llm_calls"`
	// MaxToolIterations is the maximum tool-calling iterations per run.
	MaxToolIterations int `mapstructure:"max_tool_iterations"`
	// ParallelTools enables concurrent execution of independent tool calls.
	ParallelTools bool `mapstructure:"parallel_tools"`
	// Streaming enables real-time token streaming in the TUI.
	Streaming bool `mapstructure:"streaming"`
	// MaxRunDuration is the wall-clock time limit for a single run.
	MaxRunDuration time.Duration `mapstructure:"max_run_duration"`
	// Temperature controls LLM sampling randomness (0.0-2.0).
	Temperature float64 `mapstructure:"temperature"`
	// MaxTokens is the maximum output tokens per LLM call.
	MaxTokens int `mapstructure:"max_tokens"`
	// ToolRetryEnabled enables automatic retry on transient tool failures.
	ToolRetryEnabled bool `mapstructure:"tool_retry_enabled"`
	// ToolRetryMaxAttempts is the maximum number of retry attempts.
	ToolRetryMaxAttempts int `mapstructure:"tool_retry_max_attempts"`
	// ToolRetryInitialWait is the initial delay before first retry.
	ToolRetryInitialWait time.Duration `mapstructure:"tool_retry_initial_wait"`
	// ToolRetryBackoffFactor is the exponential backoff multiplier.
	ToolRetryBackoffFactor float64 `mapstructure:"tool_retry_backoff_factor"`
	// EnablePostToolPrompt injects a reminder after each tool result.
	EnablePostToolPrompt bool `mapstructure:"enable_post_tool_prompt"`
	// Planner enables structured planning and reasoning.
	// Supported values: "builtin" (for thinking-capable models like Claude/Gemini),
	// "react" (for models without native thinking), "" (disabled).
	// BuiltinPlanner uses ReasoningEffort/ThinkingEnabled/ThinkingTokens params.
	// ReActPlanner guides the model with /*PLANNING*/ /*REASONING*/ /*ACTION*/ tags.
	Planner string `mapstructure:"planner"`
	// ReasoningEffort controls the reasoning level for BuiltinPlanner.
	// Typical values: "low", "medium", "high". Model-dependent.
	ReasoningEffort string `mapstructure:"reasoning_effort"`
	// ThinkingEnabled explicitly enables/disables thinking mode (for BuiltinPlanner).
	ThinkingEnabled *bool `mapstructure:"thinking_enabled"`
	// ThinkingTokens controls thinking length for Claude/Gemini.
	ThinkingTokens *int `mapstructure:"thinking_tokens"`
	// ToolSearchEnabled enables automatic tool filtering (TopK) before each model call.
	// When true, the toolsearch plugin compresses the candidate tool list to reduce
	// token consumption. Use ToolSearchMaxTools to set the TopK limit.
	ToolSearchEnabled bool `mapstructure:"tool_search_enabled"`
	// ToolSearchMaxTools is the maximum number of tools to expose per model call
	// when tool_search_enabled is true. Default: 20.
	ToolSearchMaxTools int `mapstructure:"tool_search_max_tools"`
	// ContextCompaction enables automatic truncation of tool results to prevent
	// context window overflow. When enabled, two-pass compaction is applied:
	//   Pass 1: Replace old (non-recent) oversized tool results with a placeholder.
	//   Pass 2: Truncate head+tail of any remaining oversized tool results.
	// Default: false.
	ContextCompaction bool `mapstructure:"context_compaction"`
	// ContextCompactionToolResultMaxTokens is the token threshold for Pass 1
	// (placeholder replacement of old tool results). Default: 1024.
	ContextCompactionToolResultMaxTokens int `mapstructure:"context_compaction_tool_result_max_tokens"`
	// ContextCompactionOversizedMaxTokens is the token threshold for Pass 2
	// (head+tail truncation of remaining large results). When 0, Pass 2 is
	// disabled. Recommended: 8192. Default: 0.
	ContextCompactionOversizedMaxTokens int `mapstructure:"context_compaction_oversized_max_tokens"`
	// ContextCompactionKeepRecentRequests controls how many recent requests
	// are protected from Pass 1 placeholder replacement. Default: 1.
	ContextCompactionKeepRecentRequests int `mapstructure:"context_compaction_keep_recent"`
	// ContextCompactionForceCleanTools lists tool names whose results are
	// always placeholderized (even in recent requests). Useful for noisy
	// tools like shell/grep that produce large output.
	ContextCompactionForceCleanTools []string `mapstructure:"context_compaction_force_clean_tools"`
	// ContextCompactionKeepTools lists tool names whose results are
	// NEVER placeholderized in Pass 1. Useful for memory/session tools
	// that contain critical information.
	ContextCompactionKeepTools []string `mapstructure:"context_compaction_keep_tools"`
	// SessionRecallEnabled enables cross-session context preloading.
	// When enabled, previous session context is injected into the system
	// prompt via tRPC's PreloadSessionRecall mechanism.
	SessionRecallEnabled bool `mapstructure:"session_recall_enabled"`
	// SessionRecallLimit is the maximum number of recalled sessions/events
	// to inject. Default: 5.
	SessionRecallLimit int `mapstructure:"session_recall_limit"`
	// JSONRepairEnabled enables automatic repair of non-standard JSON
	// in tool call arguments. Useful for models that occasionally produce
	// malformed JSON (e.g., single-quoted keys, trailing commas).
	JSONRepairEnabled bool `mapstructure:"json_repair_enabled"`
	// TodoToolEnabled enables the tRPC-native todo_write tool for
	// structured task tracking across conversation turns. Tasks are
	// persisted in Session state. Default: true.
	TodoToolEnabled bool `mapstructure:"todo_tool_enabled"`
	// TodoEnforcerEnabled enables the todo enforcer extension that
	// ensures all pending todos are completed before the agent
	// provides its final answer. Default: true.
	TodoEnforcerEnabled bool `mapstructure:"todo_enforcer_enabled"`
	// AgentToolsEnabled enables the AgentToolSet that wraps specialized
	// sub-agents (code-reviewer, summarizer, etc.) as tools callable
	// by the main agent. Default: true.
	AgentToolsEnabled bool `mapstructure:"agent_tools_enabled"`
	// AgentToolsStream enables streaming output from sub-agent tool
	// calls. When enabled, sub-agent responses are streamed to the
	// user in real-time. Default: false.
	AgentToolsStream bool `mapstructure:"agent_tools_stream"`
	// SystemPromptDir is the directory containing .md prompt template
	// files that are loaded and concatenated to form the agent's
	// system instruction. Files are sorted by filename. Variables
	// like {{.WorkingDir}} are substituted at runtime.
	// When the directory is empty or contains no .md files, the
	// built-in default instruction is used.
	// Default: ~/.config/wukong/prompts/
	SystemPromptDir string `mapstructure:"system_prompt_dir"`
	// RecipeDir is the directory containing YAML recipe definitions
	// for structured sub-agents. Each .yaml file defines a named
	// sub-agent with its own instruction, tool list, and model
	// configuration. Recipes are loaded at startup and registered
	// as callable tools.
	// Default: .wukong/recipes/
	RecipeDir string `mapstructure:"recipe_dir"`
	// RecipeEnabled controls whether YAML recipe sub-agents are
	// loaded and registered. Default: true.
	RecipeEnabled bool `mapstructure:"recipe_enabled"`
}

// ============================================================================
// Security Configuration
// ============================================================================

// PermissionMode defines the security permission level for tool execution.
type PermissionMode string

const (
	// PermissionAuto: all tools execute automatically without user approval.
	PermissionAuto PermissionMode = "auto"
	// PermissionSmart: only high-risk operations require user approval (recommended).
	PermissionSmart PermissionMode = "smart"
	// PermissionManual: every tool call requires explicit user approval.
	PermissionManual PermissionMode = "manual"
	// PermissionChatOnly: tools are disabled; text-only interaction.
	PermissionChatOnly PermissionMode = "chat_only"
)

// SecurityConfig defines tool execution safety policies, command blocking,
// and fine-grained access control.
type SecurityConfig struct {
	// MalwareScanEnabled scans external extensions for malicious patterns.
	MalwareScanEnabled bool `mapstructure:"malware_scan_enabled"`
	// DefaultTimeout is the fallback execution timeout for tools.
	DefaultTimeout time.Duration `mapstructure:"default_timeout"`
	// MaxTimeout is the hard upper limit for any tool timeout.
	MaxTimeout time.Duration `mapstructure:"max_timeout"`
	// BlockDangerousCommands enables blocking of known-dangerous shell commands.
	BlockDangerousCommands bool `mapstructure:"block_dangerous_commands"`
	// BlockedCommands lists shell command patterns that are always rejected.
	BlockedCommands []string `mapstructure:"blocked_commands"`
	// RequireApproval is a legacy flag; prefer PermissionMode.
	RequireApproval bool `mapstructure:"require_approval"`
	// PermissionMode is the tool execution permission level.
	PermissionMode PermissionMode `mapstructure:"permission_mode"`
	// Allowlist: when non-empty, ONLY listed tools may execute.
	// Empty means all tools are allowed (subject to denylist and PermissionMode).
	Allowlist []string `mapstructure:"allowlist"`
	// Denylist: tools that are always blocked regardless of allowlist.
	Denylist []string `mapstructure:"denylist"`
	// GuardrailEnabled enables prompt injection detection via the
	// tRPC guardrail plugin. Uses the default model for reviewing
	// user inputs. Adds latency to each request.
	GuardrailEnabled bool `mapstructure:"guardrail_enabled"`
	// IgnoreFileEnabled enables file-access blacklisting via
	// a .wukongignore file (gitignore-compatible syntax).
	// When enabled, file_read, file_write, file_replace, and
	// command_execute tools reject operations on paths matching
	// patterns in the ignore file. Default: true.
	IgnoreFileEnabled bool `mapstructure:"ignore_file_enabled"`
	// IgnoreFile is the path to the ignore rules file.
	// Default: .wukongignore (looked up in cwd and home dir).
	IgnoreFile string `mapstructure:"ignore_file"`
}

// ============================================================================
// Storage Configurations (Session, Memory, Todo, Recall)
// ============================================================================

// SessionConfig defines conversation history storage settings.
// Supports backends: sqlite (default), memory, redis.
type SessionConfig struct {
	// Backend is the storage backend: sqlite | memory | redis.
	Backend string `mapstructure:"backend"`
	// DBPath is the SQLite database file path (shared pool).
	// Only used when backend="sqlite".
	DBPath string `mapstructure:"db_path"`
	// EventLimit is the maximum events retained per session.
	EventLimit int `mapstructure:"event_limit"`
	// TTL is the session time-to-live duration (0 = no expiration).
	TTL time.Duration `mapstructure:"ttl"`
	// EnableSummary enables automatic conversation summarization.
	EnableSummary bool `mapstructure:"enable_summary"`
	// SummaryTrigger is the event count threshold to trigger summarization.
	SummaryTrigger int `mapstructure:"summary_trigger"`
	// RedisURL is the Redis connection URL for backend="redis".
	// Format: redis://[user:pass@]host:port[/db].
	// Example: "redis://localhost:6379/0".
	RedisURL string `mapstructure:"redis_url"`
}

// MemoryConfig defines long-term knowledge persistence settings.
type MemoryConfig struct {
	// Backend is the storage backend: sqlite | memory.
	Backend string `mapstructure:"backend"`
	// DBPath is the SQLite database file path.
	DBPath string `mapstructure:"db_path"`
	// MaxMemories is the maximum memories stored per user.
	MaxMemories int `mapstructure:"max_memories"`
	// AutoExtract enables automatic memory extraction from conversations.
	// Requires a working extractor model from the default provider.
	AutoExtract bool `mapstructure:"auto_extract"`
	// ExtractTimeout is the timeout for each memory extraction job.
	// Default: 60s. Increase if using slower models or long conversations.
	ExtractTimeout time.Duration `mapstructure:"extract_timeout"`
	// ExtractorProvider is an optional dedicated provider for memory
	// extraction. If empty, the default provider is used. Using a
	// smaller/faster model (e.g., deepseek-chat) for extraction is
	// recommended to reduce cost and latency.
	ExtractorProvider string `mapstructure:"extractor_provider"`
	// ExtractorModel is an optional dedicated model for memory extraction.
	// If empty, the provider's default model is used.
	ExtractorModel string `mapstructure:"extractor_model"`
	// ExtractorPrompt is a custom system prompt for memory extraction.
	// If empty, the framework default (280+ line detailed prompt) is used.
	// For local/smaller models (e.g., < 30B params via LMStudio/Ollama),
	// a shorter prompt (~40 lines) is recommended to keep extraction
	// focused and concise. See the example in config.yaml.
	ExtractorPrompt string `mapstructure:"extractor_prompt"`
}

// TodoConfig defines task tracking storage settings.
// Supports two modes:
//   - Custom SQLite-backed tools: todo_create, todo_update, todo_list,
//     todo_complete, todo_delete (for detailed task management).
//   - tRPC-native todo_write tool + todoenforcer: session-persisted,
//     enforces task completion before final answer (simpler, recommended).
// Both can run simultaneously.
type TodoConfig struct {
	// Backend is the storage backend for custom todo tools: sqlite | memory.
	Backend string `mapstructure:"backend"`
	// DBPath is the SQLite database file path for custom todo tools.
	DBPath string `mapstructure:"db_path"`
	// EnableNativeTodo enables the tRPC-native todo_write tool that
	// persists tasks in Session state. Default: true.
	EnableNativeTodo bool `mapstructure:"enable_native_todo"`
	// EnableEnforcer enables the todoenforcer extension that checks
	// all pending todos are complete before the agent gives its
	// final answer. Requires EnableNativeTodo=true. Default: true.
	EnableEnforcer bool `mapstructure:"enable_enforcer"`
}

// RecallConfig defines cross-session chat history search settings.
type RecallConfig struct {
	// Enabled enables cross-session chat recall (FTS5 full-text search).
	Enabled bool `mapstructure:"enabled"`
	// Backend is the storage backend: sqlite | memory.
	Backend string `mapstructure:"backend"`
	// DBPath is the SQLite database file path.
	DBPath string `mapstructure:"db_path"`
	// MaxResults is the maximum search results returned.
	MaxResults int `mapstructure:"max_results"`
	// MaxMessagesPerSession is the maximum stored messages per session for recall.
	MaxMessagesPerSession int `mapstructure:"max_messages_per_session"`
	// SearchMode is the search strategy: "fts5" (default), "hybrid".
	// Hybrid mode uses embedding similarity to re-rank FTS5 results
	// for better semantic matching. Requires embedding_provider.
	SearchMode string `mapstructure:"search_mode"`
	// EmbeddingProvider is the model provider used for generating
	// embeddings for semantic search. Uses the default provider if empty.
	// Requires an embedding-capable model (e.g., text-embedding-3-small).
	EmbeddingProvider string `mapstructure:"embedding_provider"`
	// EmbeddingModel is the specific embedding model name.
	// If empty, uses the provider's default model.
	EmbeddingModel string `mapstructure:"embedding_model"`
}

// ============================================================================
// Context Management Configuration
// ============================================================================

// RevisionConfig defines context window management and token optimization.
// When the conversation exceeds max_context_tokens, the revision system
// trims or summarizes earlier messages to stay within limits.
type RevisionConfig struct {
	// Enabled enables automatic context window management.
	Enabled bool `mapstructure:"enabled"`
	// RevisionProvider is an optional dedicated provider for cheaper/faster summaries.
	RevisionProvider string `mapstructure:"revision_provider"`
	// RevisionModel is an optional dedicated model for revision summaries.
	RevisionModel string `mapstructure:"revision_model"`
	// MaxCommandOutput is the maximum bytes retained from command execution output.
	MaxCommandOutput int `mapstructure:"max_command_output"`
	// EnableSemanticSearch enables semantic context retrieval (experimental).
	EnableSemanticSearch bool `mapstructure:"enable_semantic_search"`
	// SearchStrategy is the context retrieval strategy: include_all | semantic.
	SearchStrategy string `mapstructure:"search_strategy"`
	// MaxContextTokens is the soft limit on context window token count.
	MaxContextTokens int `mapstructure:"max_context_tokens"`
	// TrimRatio is the fraction of context to trim when exceeding limits (0.0-1.0).
	TrimRatio float64 `mapstructure:"trim_ratio"`
}

// ============================================================================
// Feature-Specific Configurations
// ============================================================================

// BrowserConfig defines web automation and file caching settings.
type BrowserConfig struct {
	// Enabled enables the browser automation feature.
	Enabled bool `mapstructure:"enabled"`
	// BrowserType is the browser engine: chromium, firefox, webkit.
	BrowserType string `mapstructure:"browser_type"`
	// Headless runs the browser without a visible window.
	Headless bool `mapstructure:"headless"`
	// CacheDir is the local directory for downloaded file caching.
	CacheDir string `mapstructure:"cache_dir"`
	// MaxDownloadSize is the maximum single download size in bytes.
	MaxDownloadSize int64 `mapstructure:"max_download_size"`
	// Timeout is the HTTP request timeout.
	Timeout time.Duration `mapstructure:"timeout"`
	// BrowserPath is the custom Chrome/Chromium executable path.
	// When empty, chromedp auto-discovers the browser.
	BrowserPath string `mapstructure:"browser_path"`
	// ViewportWidth sets the browser viewport width in pixels.
	ViewportWidth int `mapstructure:"viewport_width"`
	// ViewportHeight sets the browser viewport height in pixels.
	ViewportHeight int `mapstructure:"viewport_height"`
	// SearchBackend is the web search engine: duckduckgo (default),
	// searxng (URL required), tavily (API key required).
	SearchBackend string `mapstructure:"search_backend"`
	// SearchBackendURL is the URL for SearXNG instances.
	SearchBackendURL string `mapstructure:"search_backend_url"`
	// SearchAPIKey is the API key for Tavily.
	SearchAPIKey string `mapstructure:"search_api_key"`
}

// VisualiserConfig defines chart and diagram generation settings.
type VisualiserConfig struct {
	// Enabled enables the auto-visualiser feature.
	Enabled bool `mapstructure:"enabled"`
	// OutputDir is the directory for generated chart/diagram files.
	OutputDir string `mapstructure:"output_dir"`
	// MaxWidth is the maximum chart width in pixels.
	MaxWidth int `mapstructure:"max_width"`
	// MaxHeight is the maximum chart height in pixels.
	MaxHeight int `mapstructure:"max_height"`
}

// TutorialConfig defines interactive tutorial settings.
type TutorialConfig struct {
	// Enabled enables the tutorial feature.
	Enabled bool `mapstructure:"enabled"`
	// Language is the tutorial language: zh | en.
	Language string `mapstructure:"language"`
}

// TopOfMindConfig defines persistent instruction injection settings.
// Instructions from the configured file are injected into every prompt,
// allowing users to set standing orders that persist across sessions.
type TopOfMindConfig struct {
	// Enabled enables Top of Mind instruction injection.
	Enabled bool `mapstructure:"enabled"`
	// InstructionFile is the path to the persistent instructions file.
	InstructionFile string `mapstructure:"instruction_file"`
	// MaxLength is the maximum instruction length in characters.
	MaxLength int `mapstructure:"max_length"`
}

// CodeModeConfig defines JavaScript code execution sandbox settings.
// Uses the goja pure-Go JavaScript engine for safe, sandboxed execution.
type CodeModeConfig struct {
	// Enabled enables the Code Mode feature.
	Enabled bool `mapstructure:"enabled"`
	// Timeout is the execution timeout per script.
	Timeout time.Duration `mapstructure:"timeout"`
	// MaxMemoryMB is the memory limit for the JS engine in megabytes.
	MaxMemoryMB int `mapstructure:"max_memory_mb"`
}

// AppsConfig defines custom HTML standalone application settings.
type AppsConfig struct {
	// Enabled enables the Apps feature.
	Enabled bool `mapstructure:"enabled"`
	// AppDir is the storage directory for app HTML files.
	AppDir string `mapstructure:"app_dir"`
}

// ============================================================================
// Summon & Agent Skill Configuration
// ============================================================================

// SummonConfig defines sub-agent delegation and A2A remote agent settings.
type SummonConfig struct {
	// Enabled enables the Summon sub-agent delegation feature.
	Enabled bool `mapstructure:"enabled"`
	// SkillsDir is the directory containing skill/recipe .md files.
	SkillsDir string `mapstructure:"skills_dir"`
	// MaxConcurrent is the maximum number of concurrent sub-agents.
	MaxConcurrent int `mapstructure:"max_concurrent"`
	// A2ARemotes lists remote A2A agents available for delegation.
	A2ARemotes []A2ARemoteConfig `mapstructure:"a2a_remotes"`
}

// A2ARemoteConfig defines a remote A2A agent for sub-agent delegation.
type A2ARemoteConfig struct {
	// Name is the unique identifier for the remote agent.
	Name string `mapstructure:"name"`
	// Description explains what the remote agent does.
	Description string `mapstructure:"description"`
	// ServerURL is the A2A server endpoint URL.
	ServerURL string `mapstructure:"server_url"`
	// AuthType is the authentication method: jwt, api_key, oauth2.
	AuthType string `mapstructure:"auth_type"`
	// APIKey is the API key for api_key authentication.
	APIKey string `mapstructure:"api_key"`
	// APIKeyHeader is the custom header name for API key (default: X-API-Key).
	APIKeyHeader string `mapstructure:"api_key_header"`
	// JWTSecret is the shared secret for JWT authentication.
	JWTSecret string `mapstructure:"jwt_secret"`
	// JWTAudience is the expected JWT audience claim.
	JWTAudience string `mapstructure:"jwt_audience"`
	// JWTIssuer is the JWT issuer claim.
	JWTIssuer string `mapstructure:"jwt_issuer"`
	// OAuthTokenURL is the OAuth2 token endpoint URL.
	OAuthTokenURL string `mapstructure:"oauth_token_url"`
	// OAuthClientID is the OAuth2 client identifier.
	OAuthClientID string `mapstructure:"oauth_client_id"`
	// OAuthClientSecret is the OAuth2 client secret.
	OAuthClientSecret string `mapstructure:"oauth_client_secret"`
}

// SkillConfig defines the tRPC Agent Skill repository system settings.
// NOTE: Skill uses a separate directory from Summon's skills_dir:
//   - Skill expects directories containing SKILL.md files (FSRepository format)
//   - Summon expects individual .md files
type SkillConfig struct {
	// Enabled enables the Agent Skill system.
	Enabled bool `mapstructure:"enabled"`
	// SkillsDir is the directory containing SKILL.md files.
	SkillsDir string `mapstructure:"skills_dir"`
	// AutoLoad automatically loads skills at startup.
	AutoLoad bool `mapstructure:"auto_load"`
	// MaxSkills is the maximum number of skills to load.
	MaxSkills int `mapstructure:"max_skills"`
}

// ============================================================================
// Knowledge/RAG Configuration
// ============================================================================

// KnowledgeConfig defines settings for the Retrieval-Augmented Generation
// (RAG) knowledge base system. Supports document loading from local files
// and URLs, with vector-based semantic search.
type KnowledgeConfig struct {
	// Enabled enables the knowledge base system. Default: false.
	Enabled bool `mapstructure:"enabled"`
	// EmbedderProvider is the provider name for generating embeddings.
	// Uses an OpenAI-compatible API. Typical value: "openai".
	// If empty, the default provider is used (same as LLM).
	EmbedderProvider string `mapstructure:"embedder_provider"`
	// EmbedderModel is the embedding model name.
	// Default: "text-embedding-3-small" (1536 dimensions).
	EmbedderModel string `mapstructure:"embedder_model"`
	// Sources lists document source directories to load at startup.
	// Each path is recursively scanned for supported formats
	// (txt, md, pdf, csv, json, docx).
	Sources []string `mapstructure:"sources"`
	// SourceURLs lists document source URLs to fetch and index.
	SourceURLs []string `mapstructure:"source_urls"`
	// VectorStore is the vector store backend: "inmemory" (default).
	// Inmemory is suitable for small-to-medium knowledge bases.
	VectorStore string `mapstructure:"vector_store"`
	// MaxResults limits search results per query. Default: 5.
	MaxResults int `mapstructure:"max_results"`
	// EnableSourceSync enables automatic re-indexing when source files
	// change. Default: false (manual reload only).
	EnableSourceSync bool `mapstructure:"enable_source_sync"`
	// ReRanker enables result re-ranking. Default: false.
	ReRankerEnabled bool `mapstructure:"reranker_enabled"`
	// SearchToolName is the tool name registered to the agent.
	// Default: "knowledge_search".
	SearchToolName string `mapstructure:"search_tool_name"`
}

// ============================================================================
// Workflow & A2A Server Configuration
// ============================================================================

// DifyConfig defines settings for the Dify AI platform integration.
// Dify provides visual workflow orchestration, RAG pipelines, and
// multi-LLM support via a chat API.
type DifyConfig struct {
	// Enabled enables the Dify agent mode. Default: false.
	Enabled bool `mapstructure:"enabled"`
	// BaseURL is the Dify API endpoint (e.g., "https://api.dify.ai/v1").
	BaseURL string `mapstructure:"base_url"`
	// APISecret is the Dify API secret key for authentication.
	APISecret string `mapstructure:"api_secret"`
	// AgentName is the agent name exposed to the workflow. Default: "dify".
	AgentName string `mapstructure:"agent_name"`
	// EnableStreaming enables SSE streaming from Dify. Default: false.
	EnableStreaming bool `mapstructure:"enable_streaming"`
	// Timeout is the HTTP request timeout. Default: "120s".
	Timeout time.Duration `mapstructure:"timeout"`
}

// WorkflowConfig defines multi-mode agent orchestration settings.
// Supports: single (default), chain, parallel, cycle, graph,
//           team_coordinator, team_swarm, claude_code modes.
type WorkflowConfig struct {
	// Mode is the execution mode.
	Mode string `mapstructure:"mode"`
	// MaxIterations is the maximum iterations for cycle/graph modes.
	MaxIterations int `mapstructure:"max_iterations"`
	// CycleMode selects the cycle strategy: "default" (planner/executor)
	// or "code_review" (generator/reviewer loop).
	CycleMode string `mapstructure:"cycle_mode"`
	// StreamMode enables inter-node streaming via StreamHub ("none"/"hub").
	StreamMode string `mapstructure:"stream_mode"`
	// CacheEnabled enables node caching for graph workflows.
	CacheEnabled bool `mapstructure:"cache_enabled"`
	// Engine is the Graph execution engine: "bsp" (default) or "dag".
	Engine string `mapstructure:"engine"`
	// SubAgents defines custom sub-agent configurations.
	SubAgents []WorkflowSubAgentConfig `mapstructure:"sub_agents"`
	// TeamMembers defines team member agents for team_coordinator/team_swarm modes.
	TeamMembers []TeamMemberConfig `mapstructure:"team_members"`
	// ClaudeCodeBin is the claude CLI path for claude_code mode.
	ClaudeCodeBin string `mapstructure:"claude_code_bin"`
	// CodexBin is the codex CLI path for codex mode.
	CodexBin string `mapstructure:"codex_bin"`
}

// WorkflowSubAgentConfig defines a custom sub-agent for workflow modes.
type WorkflowSubAgentConfig struct {
	// Name is the sub-agent identifier (e.g., "planner", "code-reviewer").
	Name string `mapstructure:"name"`
	// Instruction is the system prompt for this sub-agent.
	Instruction string `mapstructure:"instruction"`
	// AllowedTools lists tool names this sub-agent may use (empty = all).
	AllowedTools []string `mapstructure:"allowed_tools"`
	// AllTools grants access to all available tools.
	AllTools bool `mapstructure:"all_tools"`
}

// TeamMemberConfig defines a team member for team_coordinator or team_swarm modes.
type TeamMemberConfig struct {
	// Name is the member identifier (e.g., "coder", "reviewer").
	Name string `mapstructure:"name"`
	// Instruction is the system prompt for this member.
	Instruction string `mapstructure:"instruction"`
	// AllowedTools lists tool names this member may use.
	AllowedTools []string `mapstructure:"allowed_tools"`
	// AllTools grants access to all tools.
	AllTools bool `mapstructure:"all_tools"`
}

// A2AServerConfig defines the local A2A protocol server for exposing
// the main agent as an A2A-compatible service to remote clients.
type A2AServerConfig struct {
	// Enabled starts the A2A server.
	Enabled bool `mapstructure:"enabled"`
	// Address is the listen address (e.g., ":9090").
	Address string `mapstructure:"address"`
	// AgentName is the name exposed via A2A protocol.
	AgentName string `mapstructure:"agent_name"`
	// AgentDescription describes the agent for A2A discovery.
	AgentDescription string `mapstructure:"agent_description"`
}

// ============================================================================
// AG-UI Server Configuration
// ============================================================================

// AGUIConfig defines settings for the AG-UI SSE server that exposes
// agent conversations to web-based chat UIs. Compatible with AG-UI
// protocol clients (CopilotKit, TDesign Chat, etc.).
type AGUIConfig struct {
	// Enabled starts the AG-UI SSE server. Default: false.
	Enabled bool `mapstructure:"enabled"`
	// Address is the listen address (e.g., ":8080").
	// Default: ":8080".
	Address string `mapstructure:"address"`
	// Path is the SSE chat endpoint path.
	// Default: "/agui".
	Path string `mapstructure:"path"`
}

// ============================================================================
// ACP Server Configuration
// ============================================================================

// ACPServerConfig defines settings for the Agent Client Protocol (ACP)
// server that exposes the agent to ACP-compatible client applications.
type ACPServerConfig struct {
	// Enabled starts the ACP server. Default: false.
	Enabled bool `mapstructure:"enabled"`
	// Address is the listen address (e.g., ":9091").
	// Default: ":9091".
	Address string `mapstructure:"address"`
	// Path is the ACP endpoint path prefix.
	// Default: "/acp".
	Path string `mapstructure:"path"`
	// EnableStreaming enables SSE streaming for ACP responses.
	// Default: true.
	EnableStreaming bool `mapstructure:"enable_streaming"`
	// AuthType is the authentication method:
	// "" (none), "api_key", "jwt".
	AuthType string `mapstructure:"auth_type"`
	// APIKey is the API key for api_key authentication.
	APIKey string `mapstructure:"api_key"`
}

// ACPMCPConfig defines settings for the MCP Bridge that exposes
// Wukong extensions as an MCP Server for ACP agents to call.
type ACPMCPConfig struct {
	// Enabled starts the MCP Bridge. Default: true.
	Enabled bool `mapstructure:"enabled"`
	// Address is the MCP Server listen address (e.g., ":3400").
	// Default: ":3400".
	Address string `mapstructure:"address"`
	// Path is the MCP endpoint path prefix.
	// Default: "/mcp".
	Path string `mapstructure:"path"`
}

// ============================================================================
// Evaluation Configuration
// ============================================================================

// EvalConfig defines settings for the agent evaluation and regression
// testing system.
type EvalConfig struct {
	// Enabled enables automatic evaluation runs. Default: false.
	Enabled bool `mapstructure:"enabled"`
	// EvalSetPath is the path to the JSON evalset file.
	// Default: ".wukong_evals/default.evalset.json".
	EvalSetPath string `mapstructure:"evalset_path"`
	// ResultsPath is the output path for evaluation results JSON.
	// Default: ".wukong_evals/results.json".
	ResultsPath string `mapstructure:"results_path"`
	// Metrics lists the evaluation metrics to apply.
	Metrics []EvalMetricConfig `mapstructure:"metrics"`
}

// EvalMetricConfig defines a single evaluation metric.
type EvalMetricConfig struct {
	// Name is the metric identifier: tool_trajectory_match,
	// response_contains_pattern, response_min_length,
	// response_not_empty.
	Name string `mapstructure:"name"`
	// Threshold is the minimum score (0.0-1.0) to pass.
	Threshold float64 `mapstructure:"threshold"`
}

// ============================================================================
// Artifact Configuration
// ============================================================================

// ArtifactConfig defines artifact storage settings for named, versioned
// binary data (images, documents, generated files).
type ArtifactConfig struct {
	// Backend is the storage backend: "inmemory" (default), "cos".
	Backend string `mapstructure:"backend"`
	// COSBucketURL is the Tencent COS bucket URL for backend="cos".
	// Format: "https://bucket.cos.region.myqcloud.com".
	COSBucketURL string `mapstructure:"cos_bucket_url"`
	// COSSecretID is the Tencent Cloud SecretId (or env COS_SECRETID).
	COSSecretID string `mapstructure:"cos_secret_id"`
	// COSSecretKey is the Tencent Cloud SecretKey
	// (or env COS_SECRETKEY).
	COSSecretKey string `mapstructure:"cos_secret_key"`
}

// ============================================================================
// Observability Configuration
// ============================================================================

// ObservabilityConfig defines enhanced observability settings including
// Langfuse LLM tracing and Prometheus metrics.
type ObservabilityConfig struct {
	// LangfuseEnabled enables Langfuse OTLP tracing. Default: false.
	LangfuseEnabled bool `mapstructure:"langfuse_enabled"`
	// LangfuseHost is the Langfuse host (without http://).
	// Default: from env LANGFUSE_HOST.
	LangfuseHost string `mapstructure:"langfuse_host"`
	// LangfusePublicKey is the Langfuse public key.
	// Default: from env LANGFUSE_PUBLIC_KEY.
	LangfusePublicKey string `mapstructure:"langfuse_public_key"`
	// LangfuseSecretKey is the Langfuse secret key.
	// Default: from env LANGFUSE_SECRET_KEY.
	LangfuseSecretKey string `mapstructure:"langfuse_secret_key"`
}

// ============================================================================
// Telemetry Configuration
// ============================================================================

// TelemetryConfig defines OpenTelemetry observability settings for
// distributed tracing and performance monitoring.
type TelemetryConfig struct {
	// Enabled enables distributed tracing.
	Enabled bool `mapstructure:"enabled"`
	// ExporterType is the OTLP exporter: grpc, http, console.
	ExporterType string `mapstructure:"exporter_type"`
	// Endpoint is the OTLP collector address (for grpc/http).
	Endpoint string `mapstructure:"endpoint"`
	// ServiceName is the service name for resource attribution.
	ServiceName string `mapstructure:"service_name"`
	// ServiceVersion is the service version tag.
	ServiceVersion string `mapstructure:"service_version"`
	// Environment is the deployment environment: development, staging, production.
	Environment string `mapstructure:"environment"`
	// SampleRate controls trace sampling (0.0-1.0, 1.0 = all traces).
	SampleRate float64 `mapstructure:"sample_rate"`
}

// ============================================================================
// Configuration Loader
// ============================================================================

// Loader handles loading configuration from YAML files using Viper.
// It supports environment variable overrides with the WUKONG_ prefix.
type Loader struct {
	v      *viper.Viper
	config *WukongConfig
}

// NewLoader creates a new configuration loader.
//
// configPath is an optional path to a custom config file.
// If empty, searches in order:
//  1. Current directory (./config.yaml)
//  2. ~/.config/wukong/config.yaml
//  3. /etc/wukong/config.yaml
func NewLoader(configPath string) (*Loader, error) {
	v := viper.New()
	l := &Loader{v: v}

	v.SetConfigName("config")
	v.SetConfigType("yaml")

	if configPath != "" {
		// Distinguish between a file path and a directory path.
		// If configPath is a directory, use AddConfigPath to search
		// for "config.yaml" inside it. If it is a file (or doesn't
		// exist yet, which SetConfigFile handles), use it directly.
		if info, err := os.Stat(configPath); err == nil && info.IsDir() {
			v.AddConfigPath(configPath)
		} else {
			v.SetConfigFile(configPath)
		}
	} else {
		v.AddConfigPath(".")
		homeDir, err := os.UserHomeDir()
		if err == nil {
			v.AddConfigPath(filepath.Join(homeDir, ".config", "wukong"))
		}
		v.AddConfigPath("/etc/wukong")
	}

	// Environment variable overrides: WUKONG_DEFAULT_PROVIDER, WUKONG_AGENT_TEMPERATURE, etc.
	v.SetEnvPrefix("WUKONG")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Set built-in defaults before reading config file
	l.setDefaults()

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("read config: %w", err)
		}
		// Config file not found is OK; use defaults
	}

	return l, nil
}

// Load parses the configuration into a WukongConfig.
// Results are cached; subsequent calls return the same instance.
func (l *Loader) Load() (*WukongConfig, error) {
	if l.config != nil {
		return l.config, nil
	}

	var cfg WukongConfig
	if err := l.v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	// Expand ${ENV_VAR} references in API keys
	for i := range cfg.Providers {
		cfg.Providers[i].APIKey = os.ExpandEnv(cfg.Providers[i].APIKey)
	}

	// Expand env vars in A2A remote API keys
	for i := range cfg.Summon.A2ARemotes {
		cfg.Summon.A2ARemotes[i].APIKey = os.ExpandEnv(cfg.Summon.A2ARemotes[i].APIKey)
		cfg.Summon.A2ARemotes[i].JWTSecret = os.ExpandEnv(cfg.Summon.A2ARemotes[i].JWTSecret)
	}

	l.config = &cfg
	return l.config, nil
}

// GetConfig returns the currently loaded configuration (may be nil if not loaded).
func (l *Loader) GetConfig() *WukongConfig {
	return l.config
}

// ============================================================================
// Configuration Query Helpers
// ============================================================================

// FindProvider returns the provider configuration by name.
// Returns nil if no provider with the given name exists.
func (c *WukongConfig) FindProvider(name string) *ProviderConfig {
	for i := range c.Providers {
		if c.Providers[i].Name == name {
			return &c.Providers[i]
		}
	}
	return nil
}

// DefaultProviderConfig returns the configuration for the default provider.
// Returns nil if the default provider is not found.
func (c *WukongConfig) DefaultProviderConfig() *ProviderConfig {
	return c.FindProvider(c.DefaultProvider)
}

// EnabledExtensions returns only the extensions that are enabled.
func (c *WukongConfig) EnabledExtensions() []ExtensionConfig {
	var result []ExtensionConfig
	for _, ext := range c.Extensions {
		if ext.Enabled {
			result = append(result, ext)
		}
	}
	return result
}

// FindExtension returns an extension configuration by name.
// Returns nil if no extension with the given name exists.
func (c *WukongConfig) FindExtension(name string) *ExtensionConfig {
	for i := range c.Extensions {
		if c.Extensions[i].Name == name {
			return &c.Extensions[i]
		}
	}
	return nil
}

// ============================================================================
// Default Values
// ============================================================================

// setDefaults registers all built-in default values with Viper.
// These are used when no config file or environment variable provides a value.
func (l *Loader) setDefaults() {
	// --- Global defaults ---
	l.v.SetDefault("log_level", "info")

	// --- Agent defaults ---
	l.v.SetDefault("agent.max_llm_calls", 50)
	l.v.SetDefault("agent.max_tool_iterations", 30)
	l.v.SetDefault("agent.parallel_tools", true)
	l.v.SetDefault("agent.streaming", true)
	l.v.SetDefault("agent.max_run_duration", "300s")
	l.v.SetDefault("agent.temperature", 0.7)
	l.v.SetDefault("agent.max_tokens", 4096)
	l.v.SetDefault("agent.tool_retry_enabled", true)
	l.v.SetDefault("agent.tool_retry_max_attempts", 3)
	l.v.SetDefault("agent.tool_retry_initial_wait", "1s")
	l.v.SetDefault("agent.tool_retry_backoff_factor", 2.0)
	l.v.SetDefault("agent.enable_post_tool_prompt", true)
	l.v.SetDefault("agent.planner", "")
	l.v.SetDefault("agent.tool_search_enabled", false)
	l.v.SetDefault("agent.tool_search_max_tools", 20)
	l.v.SetDefault("agent.context_compaction", false)
	l.v.SetDefault("agent.context_compaction_tool_result_max_tokens", 1024)
	l.v.SetDefault("agent.context_compaction_oversized_max_tokens", 0)
	l.v.SetDefault("agent.context_compaction_keep_recent", 1)
	l.v.SetDefault("agent.session_recall_enabled", false)
	l.v.SetDefault("agent.session_recall_limit", 5)
	l.v.SetDefault("agent.json_repair_enabled", false)
	l.v.SetDefault("agent.todo_tool_enabled", true)
	l.v.SetDefault("agent.todo_enforcer_enabled", true)
	l.v.SetDefault("agent.agent_tools_enabled", true)
	l.v.SetDefault("agent.agent_tools_stream", false)
	l.v.SetDefault("agent.system_prompt_dir", "~/.config/wukong/prompts/")
	l.v.SetDefault("agent.recipe_dir", ".wukong/recipes/")
	l.v.SetDefault("agent.recipe_enabled", true)

	// --- Security defaults ---
	l.v.SetDefault("security.malware_scan_enabled", true)
	l.v.SetDefault("security.default_timeout", "30s")
	l.v.SetDefault("security.max_timeout", "300s")
	l.v.SetDefault("security.block_dangerous_commands", true)
	l.v.SetDefault("security.blocked_commands",
		[]string{"rm -rf /", "dd if=/dev/zero", "mkfs.",
			"> /dev/sda", "fork bomb"})
	l.v.SetDefault("security.require_approval", false)
	l.v.SetDefault("security.permission_mode", "smart")
	l.v.SetDefault("security.guardrail_enabled", false)
	l.v.SetDefault("security.ignore_file_enabled", true)
	l.v.SetDefault("security.ignore_file", ".wukongignore")

	// --- Session defaults ---
	l.v.SetDefault("session.backend", "sqlite")
	l.v.SetDefault("session.db_path", "wukong.db")
	l.v.SetDefault("session.event_limit", 500)
	l.v.SetDefault("session.ttl", "0h")
	l.v.SetDefault("session.enable_summary", true)
	l.v.SetDefault("session.summary_trigger", 50)

	// --- Memory defaults ---
	l.v.SetDefault("memory.backend", "sqlite")
	l.v.SetDefault("memory.db_path", "wukong.db")
	l.v.SetDefault("memory.max_memories", 100)
	l.v.SetDefault("memory.auto_extract", true)
	l.v.SetDefault("memory.extract_timeout", "60s")

	// --- Todo defaults ---
	l.v.SetDefault("todo.backend", "sqlite")
	l.v.SetDefault("todo.db_path", "wukong.db")
	l.v.SetDefault("todo.enable_native_todo", true)
	l.v.SetDefault("todo.enable_enforcer", true)

	// --- Recall defaults ---
	l.v.SetDefault("recall.enabled", true)
	l.v.SetDefault("recall.backend", "sqlite")
	l.v.SetDefault("recall.db_path", "wukong.db")
	l.v.SetDefault("recall.max_results", 10)
	l.v.SetDefault("recall.max_messages_per_session", 200)
	l.v.SetDefault("recall.search_mode", "fts5")

	// --- Revision defaults ---
	l.v.SetDefault("revision.enabled", true)
	l.v.SetDefault("revision.max_command_output", 8000)
	l.v.SetDefault("revision.enable_semantic_search", false)
	l.v.SetDefault("revision.search_strategy", "include_all")
	l.v.SetDefault("revision.max_context_tokens", 64000)
	l.v.SetDefault("revision.trim_ratio", 0.3)

	// --- Browser defaults ---
	l.v.SetDefault("browser.enabled", true)
	l.v.SetDefault("browser.browser_type", "chromium")
	l.v.SetDefault("browser.headless", true)
	l.v.SetDefault("browser.cache_dir", ".wukong_cache")
	l.v.SetDefault("browser.max_download_size", 104857600)
	l.v.SetDefault("browser.timeout", "60s")
	l.v.SetDefault("browser.viewport_width", 1280)
	l.v.SetDefault("browser.viewport_height", 720)
	l.v.SetDefault("browser.search_backend", "duckduckgo")

	// --- Visualiser defaults ---
	l.v.SetDefault("visualiser.enabled", true)
	l.v.SetDefault("visualiser.output_dir", ".wukong_visuals")
	l.v.SetDefault("visualiser.max_width", 1200)
	l.v.SetDefault("visualiser.max_height", 800)

	// --- Tutorial defaults ---
	l.v.SetDefault("tutorial.enabled", true)
	l.v.SetDefault("tutorial.language", "zh")

	// --- Top of Mind defaults ---
	l.v.SetDefault("top_of_mind.enabled", true)
	l.v.SetDefault("top_of_mind.instruction_file", ".wukong_instructions.md")
	l.v.SetDefault("top_of_mind.max_length", 2000)

	// --- Code Mode defaults ---
	l.v.SetDefault("code_mode.enabled", true)
	l.v.SetDefault("code_mode.timeout", "10s")
	l.v.SetDefault("code_mode.max_memory_mb", 128)

	// --- Apps defaults ---
	l.v.SetDefault("apps.enabled", true)
	l.v.SetDefault("apps.app_dir", ".wukong_apps")

	// --- Summon defaults ---
	l.v.SetDefault("summon.enabled", true)
	l.v.SetDefault("summon.skills_dir", ".wukong_skills")
	l.v.SetDefault("summon.max_concurrent", 5)

	// --- Skill defaults ---
	l.v.SetDefault("skill.enabled", true)
	l.v.SetDefault("skill.skills_dir", ".wukong_agent_skills")
	l.v.SetDefault("skill.auto_load", true)
	l.v.SetDefault("skill.max_skills", 20)

	// --- Knowledge defaults ---
	l.v.SetDefault("knowledge.enabled", false)
	l.v.SetDefault("knowledge.embedder_model", "text-embedding-3-small")
	l.v.SetDefault("knowledge.vector_store", "inmemory")
	l.v.SetDefault("knowledge.max_results", 5)
	l.v.SetDefault("knowledge.enable_source_sync", false)
	l.v.SetDefault("knowledge.reranker_enabled", false)
	l.v.SetDefault("knowledge.search_tool_name", "knowledge_search")

	// --- Dify defaults ---
	l.v.SetDefault("dify.enabled", false)
	l.v.SetDefault("dify.agent_name", "dify")
	l.v.SetDefault("dify.enable_streaming", false)
	l.v.SetDefault("dify.timeout", "120s")

	// --- Workflow defaults ---
	l.v.SetDefault("workflow.mode", "single")
	l.v.SetDefault("workflow.max_iterations", 10)
	l.v.SetDefault("workflow.stream_mode", "none")
	l.v.SetDefault("workflow.cache_enabled", false)
	l.v.SetDefault("workflow.engine", "bsp")

	// --- A2A server defaults ---
	l.v.SetDefault("a2a_server.enabled", false)
	l.v.SetDefault("a2a_server.address", ":9090")
	l.v.SetDefault("a2a_server.agent_name", "wukong")
	l.v.SetDefault("a2a_server.agent_description",
		"Wukong AI Agent - A2A service endpoint")

	// --- AG-UI defaults ---
	l.v.SetDefault("agui.enabled", false)
	l.v.SetDefault("agui.address", ":8080")
	l.v.SetDefault("agui.path", "/agui")

	// --- ACP Server defaults ---
	l.v.SetDefault("acp_server.enabled", false)
	l.v.SetDefault("acp_server.address", ":9091")
	l.v.SetDefault("acp_server.path", "/acp")
	l.v.SetDefault("acp_server.enable_streaming", true)
	l.v.SetDefault("acp_server.auth_type", "")

	// --- ACP MCP Bridge defaults ---
	l.v.SetDefault("acp_mcp.enabled", true)
	l.v.SetDefault("acp_mcp.address", ":3400")
	l.v.SetDefault("acp_mcp.path", "/mcp")

	// --- Eval defaults ---
	l.v.SetDefault("eval.enabled", false)
	l.v.SetDefault("eval.evalset_path",
		".wukong_evals/default.evalset.json")
	l.v.SetDefault("eval.results_path",
		".wukong_evals/results.json")

	// --- Artifact defaults ---
	l.v.SetDefault("artifact.backend", "inmemory")

	// --- Observability defaults ---
	l.v.SetDefault("observability.langfuse_enabled", false)

	// --- Project defaults ---
	l.v.SetDefault("project_dir", "~/.config/wukong/")

	// --- Telemetry defaults ---
	l.v.SetDefault("telemetry.enabled", false)
	l.v.SetDefault("telemetry.exporter_type", "console")
	l.v.SetDefault("telemetry.endpoint", "localhost:4317")
	l.v.SetDefault("telemetry.service_name", "wukong")
	l.v.SetDefault("telemetry.service_version", "1.0.0")
	l.v.SetDefault("telemetry.environment", "development")
	l.v.SetDefault("telemetry.sample_rate", 1.0)
}
