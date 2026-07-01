// Package config type definitions.
//
// This file contains all sub-configuration struct types used by
// WukongConfig. Types are organized by functional category:
//   - Provider & Extension
//   - Agent
//   - Security
//   - Storage (Session/Memory/Todo/Recall)
//   - CortexDB Memory Stack
//   - Context Management
//   - Feature Tools
//   - Apps (Clone & Pack)
//   - Orchestration & Discovery
//   - Service Endpoints
//   - Observability & Evaluation
package config

import "time"

// ============================================================================
// Gateway & Multi-Platform Channel Configuration
// ============================================================================

// GatewayConfig defines settings for the multi-platform messaging
// gateway server. The gateway serves as a unified entry point for
// external IM platforms (Feishu, WeCom, Slack, etc.).
type GatewayConfig struct {
	// Enabled enables the Gateway server.
	// Default: false.
	Enabled bool `mapstructure:"enabled"`

	// Address is the HTTP listen address for the gateway server.
	// Default: ":9093".
	Address string `mapstructure:"address"`

	// DefaultTimeout is the maximum duration for an agent run
	// triggered by a platform message. Default: "120s".
	DefaultTimeout time.Duration `mapstructure:"default_timeout"`

	// MaxConcurrentSessions limits concurrent agent sessions
	// across all channels. Default: 100.
	MaxConcurrentSessions int `mapstructure:"max_concurrent_sessions"`

	// MessageDedupTTL is the deduplication window for platform
	// messages. Messages with the same MessageID within this
	// window are silently dropped. Default: "5m".
	MessageDedupTTL time.Duration `mapstructure:"message_dedup_ttl"`

	// RateLimitPerUser is the maximum number of agent runs per
	// user within the rate limit window. Default: 10.
	RateLimitPerUser int `mapstructure:"rate_limit_per_user"`

	// RateLimitWindow is the sliding window duration for per-user
	// rate limiting. Default: "10s".
	RateLimitWindow time.Duration `mapstructure:"rate_limit_window"`

	// Feishu contains the Feishu/Lark channel configuration.
	Feishu FeishuChannelConfig `mapstructure:"feishu"`

	// WeCom contains the WeCom channel configuration.
	WeCom WeComChannelConfig `mapstructure:"wecom"`
}

// FeishuChannelConfig defines settings for the Feishu/Lark channel.
type FeishuChannelConfig struct {
	// Enabled enables the Feishu channel. Default: false.
	Enabled bool `mapstructure:"enabled"`

	// AppID is the Feishu application ID (cli_xxx).
	AppID string `mapstructure:"app_id"`

	// AppSecret is the Feishu application secret.
	// Supports ${ENV_VAR} expansion.
	AppSecret string `mapstructure:"app_secret"`

	// EncryptKey is the event encryption key from Feishu app
	// settings. Supports ${ENV_VAR} expansion.
	EncryptKey string `mapstructure:"encrypt_key"`

	// VerificationToken is the event subscription verification
	// token. Supports ${ENV_VAR} expansion.
	VerificationToken string `mapstructure:"verification_token"`

	// StreamCardEnabled enables streaming card replies for
	// real-time display. When disabled, a single text reply is
	// sent after the agent completes.
	// Default: true.
	StreamCardEnabled bool `mapstructure:"stream_card_enabled"`

	// StreamCardUpdateInterval controls how often the streaming
	// card content is updated. Default: "500ms".
	StreamCardUpdateInterval time.Duration `mapstructure:"stream_card_update_interval"`

	// MaxMessageLength is the maximum characters per message
	// sent to Feishu. Messages exceeding this are truncated.
	// Default: 4096.
	MaxMessageLength int `mapstructure:"max_message_length"`

	// EnableFileReceive controls whether file messages from
	// users are accepted. Default: false.
	EnableFileReceive bool `mapstructure:"enable_file_receive"`
}

// WeComChannelConfig defines settings for the WeCom channel.
type WeComChannelConfig struct {
	// Enabled enables the WeCom channel. Default: false.
	Enabled bool `mapstructure:"enabled"`

	// CorpID is the WeCom enterprise ID (wwxxxx).
	CorpID string `mapstructure:"corpid"`

	// Secret is the application secret for API calls.
	// Supports ${ENV_VAR} expansion.
	Secret string `mapstructure:"secret"`

	// Token is the callback verification token.
	// Supports ${ENV_VAR} expansion.
	Token string `mapstructure:"token"`

	// EncodingAESKey is the Base64-encoded AES key for message
	// encryption. Supports ${ENV_VAR} expansion.
	EncodingAESKey string `mapstructure:"encoding_aes_key"`

	// StreamEnabled enables streaming message replies.
	// Default: true.
	StreamEnabled bool `mapstructure:"stream_enabled"`

	// StreamUpdateInterval controls the streaming update cadence.
	// Default: "1s".
	StreamUpdateInterval time.Duration `mapstructure:"stream_update_interval"`

	// MaxMessageLength is the maximum characters per message
	// sent to WeCom. Default: 2048.
	MaxMessageLength int `mapstructure:"max_message_length"`

	// EnableCardReply enables template card replies.
	// Default: true.
	EnableCardReply bool `mapstructure:"enable_card_reply"`
}

// ============================================================================
// Provider & Extension Configuration
// ============================================================================

// ProviderConfig defines a connection to an LLM backend.
// Supported types: openai, anthropic, google, deepseek, ollama, lmstudio, acp.
// API keys support ${ENV_VAR} expansion for secrets management.
type ProviderConfig struct {
	// Name is the unique identifier for this provider
	// (referenced by default_provider).
	Name string `mapstructure:"name"`
	// Type is the provider backend type.
	// Valid: openai, anthropic, google, deepseek, ollama,
	// lmstudio, acp.
	Type string `mapstructure:"type"`
	// BaseURL is the API endpoint base URL (auto-filled for
	// known providers if empty).
	BaseURL string `mapstructure:"base_url"`
	// APIKey is the authentication key.
	// Supports ${ENV_VAR} expansion.
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
	// Env defines additional environment variables for the
	// extension process.
	Env map[string]string `mapstructure:"env"`
	// Enabled controls whether the extension is active.
	Enabled bool `mapstructure:"enabled"`
	// Timeout is the tool execution timeout for this extension.
	Timeout time.Duration `mapstructure:"timeout"`
	// Deeplink is a wukong://extension? URL for one-click
	// installation.
	Deeplink string `mapstructure:"deeplink"`
	// Permissions defines fine-grained tool-level access control.
	Permissions []ToolPermission `mapstructure:"permissions"`
	// MCPBroker enables on-demand tool discovery via MCP broker
	// tools (mcp_list_servers, mcp_list_tools, mcp_call) instead
	// of exposing all remote tools directly. Useful when connecting
	// to many MCP servers to keep the tool list manageable.
	MCPBroker bool `mapstructure:"mcp_broker"`
	// MCPToolFilter specifies which tools to include from the MCP
	// server. When empty, all tools are included. Supports glob
	// patterns.
	MCPToolFilter []string `mapstructure:"mcp_tool_filter"`
	// MCPToolExclude specifies which tools to exclude from the MCP
	// server. Supports glob patterns.
	MCPToolExclude []string `mapstructure:"mcp_tool_exclude"`
	// MCPSessionReconnect enables automatic session reconnection
	// on connection loss (SSE/streamable transports).
	// Default: false.
	MCPSessionReconnect bool `mapstructure:"mcp_session_reconnect"`
	// MCPSessionReconnectAttempts is the max reconnection
	// attempts. Default: 3.
	MCPSessionReconnectAttempts int `mapstructure:"mcp_session_reconnect_attempts"`
}

// ToolPermission defines allow/deny for a specific tool within
// an extension.
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
	// MaxLLMCalls is the maximum number of LLM API calls per run
	// (0 = unlimited).
	MaxLLMCalls int `mapstructure:"max_llm_calls"`
	// MaxToolIterations is the maximum tool-calling iterations per
	// run.
	MaxToolIterations int `mapstructure:"max_tool_iterations"`
	// ParallelTools enables concurrent execution of independent
	// tool calls.
	ParallelTools bool `mapstructure:"parallel_tools"`
	// Streaming enables real-time token streaming in the TUI.
	Streaming bool `mapstructure:"streaming"`
	// MaxRunDuration is the wall-clock time limit for a single
	// run.
	MaxRunDuration time.Duration `mapstructure:"max_run_duration"`
	// Temperature controls LLM sampling randomness (0.0-2.0).
	Temperature float64 `mapstructure:"temperature"`
	// MaxTokens is the maximum output tokens per LLM call.
	MaxTokens int `mapstructure:"max_tokens"`
	// ToolRetryEnabled enables automatic retry on transient tool
	// failures.
	ToolRetryEnabled bool `mapstructure:"tool_retry_enabled"`
	// ToolRetryMaxAttempts is the maximum number of retry
	// attempts.
	ToolRetryMaxAttempts int `mapstructure:"tool_retry_max_attempts"`
	// ToolRetryInitialWait is the initial delay before first
	// retry.
	ToolRetryInitialWait time.Duration `mapstructure:"tool_retry_initial_wait"`
	// ToolRetryBackoffFactor is the exponential backoff
	// multiplier.
	ToolRetryBackoffFactor float64 `mapstructure:"tool_retry_backoff_factor"`
	// EnablePostToolPrompt injects a reminder after each tool
	// result.
	EnablePostToolPrompt bool `mapstructure:"enable_post_tool_prompt"`
	// Planner enables structured planning and reasoning.
	// Supported values: "builtin" (for thinking-capable models
	// like Claude/Gemini), "react" (for models without native
	// thinking), "" (disabled).
	Planner string `mapstructure:"planner"`
	// ReasoningEffort controls the reasoning level for
	// BuiltinPlanner. Typical values: "low", "medium", "high".
	ReasoningEffort string `mapstructure:"reasoning_effort"`
	// ThinkingEnabled explicitly enables/disables thinking mode
	// (for BuiltinPlanner).
	ThinkingEnabled *bool `mapstructure:"thinking_enabled"`
	// ThinkingTokens controls thinking length for
	// Claude/Gemini.
	ThinkingTokens *int `mapstructure:"thinking_tokens"`
	// ToolSearchEnabled enables automatic tool filtering (TopK)
	// before each model call.
	ToolSearchEnabled bool `mapstructure:"tool_search_enabled"`
	// ToolSearchMaxTools is the maximum number of tools to expose
	// per model call when tool_search_enabled is true.
	// Default: 20.
	ToolSearchMaxTools int `mapstructure:"tool_search_max_tools"`
	// ContextCompaction enables automatic truncation of tool
	// results to prevent context window overflow.
	ContextCompaction bool `mapstructure:"context_compaction"`
	// ContextCompactionToolResultMaxTokens is the token threshold
	// for Pass 1 (placeholder replacement of old tool results).
	// Default: 1024.
	ContextCompactionToolResultMaxTokens int `mapstructure:"context_compaction_tool_result_max_tokens"`
	// ContextCompactionOversizedMaxTokens is the token threshold
	// for Pass 2 (head+tail truncation). When 0, Pass 2 is
	// disabled. Recommended: 8192. Default: 0.
	ContextCompactionOversizedMaxTokens int `mapstructure:"context_compaction_oversized_max_tokens"`
	// ContextCompactionKeepRecentRequests controls how many
	// recent requests are protected from Pass 1 placeholder
	// replacement. Default: 1.
	ContextCompactionKeepRecentRequests int `mapstructure:"context_compaction_keep_recent"`
	// ContextCompactionForceCleanTools lists tool names whose
	// results are always placeholderized.
	ContextCompactionForceCleanTools []string `mapstructure:"context_compaction_force_clean_tools"`
	// ContextCompactionKeepTools lists tool names whose results
	// are NEVER placeholderized in Pass 1.
	ContextCompactionKeepTools []string `mapstructure:"context_compaction_keep_tools"`
	// SessionRecallEnabled enables cross-session context
	// preloading.
	SessionRecallEnabled bool `mapstructure:"session_recall_enabled"`
	// SessionRecallLimit is the maximum number of recalled
	// sessions/events to inject. Default: 5.
	SessionRecallLimit int `mapstructure:"session_recall_limit"`
	// JSONRepairEnabled enables automatic repair of non-standard
	// JSON in tool call arguments.
	JSONRepairEnabled bool `mapstructure:"json_repair_enabled"`
	// TodoToolEnabled enables the tRPC-native todo_write tool.
	// Default: true.
	TodoToolEnabled bool `mapstructure:"todo_tool_enabled"`
	// TodoEnforcerEnabled enables the todo enforcer extension.
	// Default: true.
	TodoEnforcerEnabled bool `mapstructure:"todo_enforcer_enabled"`
	// AgentToolsEnabled enables the AgentToolSet that wraps
	// specialized sub-agents as tools. Default: true.
	AgentToolsEnabled bool `mapstructure:"agent_tools_enabled"`
	// AgentToolsStream enables streaming output from sub-agent
	// tool calls. Default: false.
	AgentToolsStream bool `mapstructure:"agent_tools_stream"`
	// SystemPromptDir is the directory containing .md prompt
	// template files. Default: ~/.config/wukong/prompts/.
	SystemPromptDir string `mapstructure:"system_prompt_dir"`
	// RecipeDir is the directory containing YAML recipe
	// definitions. Default: .wukong/recipes/.
	RecipeDir string `mapstructure:"recipe_dir"`
	// RecipeEnabled controls whether YAML recipe sub-agents are
	// loaded. Default: true.
	RecipeEnabled bool `mapstructure:"recipe_enabled"`
	// InlineRecipes defines recipe sub-agent configurations
	// directly in the config file.
	InlineRecipes []map[string]any `mapstructure:"inline_recipes"`
}

// ============================================================================
// Security Configuration
// ============================================================================

// PermissionMode defines the security permission level for tool
// execution.
type PermissionMode string

const (
	// PermissionAuto: all tools execute automatically without user
	// approval.
	PermissionAuto PermissionMode = "auto"
	// PermissionSmart: only high-risk operations require user
	// approval (recommended).
	PermissionSmart PermissionMode = "smart"
	// PermissionManual: every tool call requires explicit user
	// approval.
	PermissionManual PermissionMode = "manual"
	// PermissionChatOnly: tools are disabled; text-only
	// interaction.
	PermissionChatOnly PermissionMode = "chat_only"
)

// SecurityConfig defines tool execution safety policies, command
// blocking, and fine-grained access control.
type SecurityConfig struct {
	// MalwareScanEnabled scans external extensions for malicious
	// patterns.
	MalwareScanEnabled bool `mapstructure:"malware_scan_enabled"`
	// DefaultTimeout is the fallback execution timeout for tools.
	DefaultTimeout time.Duration `mapstructure:"default_timeout"`
	// MaxTimeout is the hard upper limit for any tool timeout.
	MaxTimeout time.Duration `mapstructure:"max_timeout"`
	// BlockDangerousCommands enables blocking of known-dangerous
	// shell commands.
	BlockDangerousCommands bool `mapstructure:"block_dangerous_commands"`
	// BlockedCommands lists shell command patterns that are always
	// rejected.
	BlockedCommands []string `mapstructure:"blocked_commands"`
	// RequireApproval is a legacy flag; prefer PermissionMode.
	RequireApproval bool `mapstructure:"require_approval"`
	// PermissionMode is the tool execution permission level.
	PermissionMode PermissionMode `mapstructure:"permission_mode"`
	// Allowlist: when non-empty, ONLY listed tools may execute.
	Allowlist []string `mapstructure:"allowlist"`
	// Denylist: tools that are always blocked regardless of
	// allowlist.
	Denylist []string `mapstructure:"denylist"`
	// GuardrailEnabled enables prompt injection detection.
	GuardrailEnabled bool `mapstructure:"guardrail_enabled"`
	// IgnoreFileEnabled enables file-access blacklisting via
	// a .wukongignore file. Default: true.
	IgnoreFileEnabled bool `mapstructure:"ignore_file_enabled"`
	// IgnoreFile is the path to the ignore rules file.
	// Default: .wukongignore.
	IgnoreFile string `mapstructure:"ignore_file"`
}

// ============================================================================
// Storage Configurations
// ============================================================================

// SessionConfig defines conversation history storage settings.
// Supports backends: sqlite (default), memory, redis.
type SessionConfig struct {
	// Backend is the storage backend: sqlite | memory | redis.
	Backend string `mapstructure:"backend"`
	// DBPath is the SQLite database file path (shared pool).
	DBPath string `mapstructure:"db_path"`
	// EventLimit is the maximum events retained per session.
	EventLimit int `mapstructure:"event_limit"`
	// TTL is the session time-to-live duration (0 = no
	// expiration).
	TTL time.Duration `mapstructure:"ttl"`
	// EnableSummary enables automatic conversation
	// summarization.
	EnableSummary bool `mapstructure:"enable_summary"`
	// SummaryTrigger is the event count threshold to trigger
	// summarization.
	SummaryTrigger int `mapstructure:"summary_trigger"`
	// RedisURL is the Redis connection URL for backend="redis".
	// Format: redis://[user:pass@]host:port[/db].
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
	// AutoExtract enables automatic memory extraction from
	// conversations.
	AutoExtract bool `mapstructure:"auto_extract"`
	// ExtractTimeout is the timeout for each memory extraction
	// job. Default: 60s.
	ExtractTimeout time.Duration `mapstructure:"extract_timeout"`
	// ExtractorProvider is an optional dedicated provider for
	// memory extraction.
	ExtractorProvider string `mapstructure:"extractor_provider"`
	// ExtractorModel is an optional dedicated model for memory
	// extraction.
	ExtractorModel string `mapstructure:"extractor_model"`
	// ExtractorPrompt is a custom system prompt for memory
	// extraction.
	ExtractorPrompt string `mapstructure:"extractor_prompt"`
	// EnableSmartCleanup enables capacity-aware memory eviction.
	// When enabled, memories are evicted based on importance
	// scoring (recency + content length) when nearing capacity.
	// Default: true.
	EnableSmartCleanup bool `mapstructure:"enable_smart_cleanup"`
	// CleanupTriggerThreshold is the capacity fraction that
	// triggers eviction (0.0-1.0). Default: 0.8 (80%).
	CleanupTriggerThreshold float64 `mapstructure:"cleanup_trigger_threshold"`
	// CleanupTargetThreshold is the target capacity fraction
	// after eviction (0.0-1.0). Default: 0.6 (60%).
	CleanupTargetThreshold float64 `mapstructure:"cleanup_target_threshold"`
	// MemoryTTL is the time-to-live for memories. Memories older
	// than this are eligible for cleanup. Default: 720h (30 days).
	MemoryTTL time.Duration `mapstructure:"memory_ttl"`
}

// TodoConfig defines task tracking storage settings.
type TodoConfig struct {
	// Backend is the storage backend: sqlite | memory.
	Backend string `mapstructure:"backend"`
	// DBPath is the SQLite database file path.
	DBPath string `mapstructure:"db_path"`
	// EnableNativeTodo enables the tRPC-native todo_write tool.
	// Default: true.
	EnableNativeTodo bool `mapstructure:"enable_native_todo"`
	// EnableEnforcer enables the todoenforcer extension.
	// Default: true.
	EnableEnforcer bool `mapstructure:"enable_enforcer"`
}

// RecallConfig defines cross-session chat history search settings.
type RecallConfig struct {
	// Enabled enables cross-session chat recall (FTS5).
	Enabled bool `mapstructure:"enabled"`
	// Backend is the storage backend: sqlite | memory.
	Backend string `mapstructure:"backend"`
	// DBPath is the SQLite database file path.
	DBPath string `mapstructure:"db_path"`
	// MaxResults is the maximum search results returned.
	MaxResults int `mapstructure:"max_results"`
	// MaxMessagesPerSession is the maximum stored messages per
	// session for recall.
	MaxMessagesPerSession int `mapstructure:"max_messages_per_session"`
	// SearchMode is the search strategy: "fts5" (default),
	// "hybrid".
	SearchMode string `mapstructure:"search_mode"`
	// EmbeddingProvider is the model provider used for generating
	// embeddings for semantic search.
	EmbeddingProvider string `mapstructure:"embedding_provider"`
	// EmbeddingModel is the specific embedding model name.
	EmbeddingModel string `mapstructure:"embedding_model"`
}

// ============================================================================
// CortexDB Memory Stack Configuration
// ============================================================================

// CortexConfig defines the CortexDB-based intelligent recall and
// knowledge storage settings.
type CortexConfig struct {
	// Enabled enables CortexDB-backed recall and knowledge
	// storage.
	Enabled bool `mapstructure:"enabled"`
	// DBPath is the path to the CortexDB database file.
	// Default: "wukong.db".
	DBPath string `mapstructure:"db_path"`
	// MaxResults is the maximum search results returned.
	// Default: 10.
	MaxResults int `mapstructure:"max_results"`
	// MaxMessagesPerSession is the maximum stored messages per
	// session. Default: 200.
	MaxMessagesPerSession int `mapstructure:"max_messages_per_session"`
	// EmbeddingBaseURL is the OpenAI-compatible API base URL for
	// the embedding model.
	EmbeddingBaseURL string `mapstructure:"embedding_base_url"`
	// EmbeddingAPIKey is the API key for the embedding service.
	// Supports ${ENV_VAR} expansion.
	EmbeddingAPIKey string `mapstructure:"embedding_api_key"`
	// EmbeddingModel is the specific embedding model name.
	// Default: "text-embedding-3-small".
	EmbeddingModel string `mapstructure:"embedding_model"`
}

// MemoryFlowConfig defines the CortexDB MemoryFlow settings for
// conversation transcript recording, wake-up context generation,
// and fact promotion.
type MemoryFlowConfig struct {
	// Enabled enables MemoryFlow-based context enrichment.
	Enabled bool `mapstructure:"enabled"`
	// DBPath is the path to the CortexDB database file.
	// Default: "wukong.db".
	DBPath string `mapstructure:"db_path"`
	// Namespace groups memories under a logical domain.
	// Default: "assistant".
	Namespace string `mapstructure:"namespace"`
	// EmbeddingDimensions specifies the vector dimension for
	// semantic search. Use 0 for auto-detection.
	// Default: 0 (auto).
	EmbeddingDimensions int `mapstructure:"embedding_dimensions"`
	// PlannerModel is the model used for retrieval strategy
	// planning.
	PlannerModel string `mapstructure:"planner_model"`
	// ExtractorModel is the model used for transcript fact
	// extraction.
	ExtractorModel string `mapstructure:"extractor_model"`
}

// GraphFlowConfig defines the CortexDB GraphFlow settings for
// entity/relationship extraction and knowledge graph construction.
type GraphFlowConfig struct {
	// Enabled enables knowledge graph construction.
	Enabled bool `mapstructure:"enabled"`
	// DBPath is the path to the CortexDB file for GraphFlow.
	// Default: "wukong.db".
	DBPath string `mapstructure:"db_path"`
	// ExtractorModel is the model for LLM-driven entity
	// extraction.
	ExtractorModel string `mapstructure:"extractor_model"`
	// MaxCharsPerDoc is the maximum characters per document for
	// extraction. Default: 8000.
	MaxCharsPerDoc int `mapstructure:"max_chars_per_doc"`
	// AutoExtract enables automatic entity extraction after each
	// conversation turn.
	AutoExtract bool `mapstructure:"auto_extract"`
}

// ImportFlowConfig defines the CortexDB ImportFlow settings for
// structured data import (DDL -> KG mapping, CSV -> RAG+KG).
type ImportFlowConfig struct {
	// Enabled enables structured data import.
	Enabled bool `mapstructure:"enabled"`
	// DBPath is the path to the CortexDB file for ImportFlow.
	// Default: "wukong.db".
	DBPath string `mapstructure:"db_path"`
}

// ============================================================================
// Context Management Configuration
// ============================================================================

// RevisionConfig defines context window management and token
// optimization.
type RevisionConfig struct {
	// Enabled enables automatic context window management.
	Enabled bool `mapstructure:"enabled"`
	// RevisionProvider is an optional dedicated provider for
	// cheaper/faster summaries.
	RevisionProvider string `mapstructure:"revision_provider"`
	// RevisionModel is an optional dedicated model for revision
	// summaries.
	RevisionModel string `mapstructure:"revision_model"`
	// EnableLLMSummarize enables intelligent LLM-based
	// summarization.
	EnableLLMSummarize bool `mapstructure:"enable_llm_summarize"`
	// MaxCommandOutput is the maximum bytes retained from command
	// execution output.
	MaxCommandOutput int `mapstructure:"max_command_output"`
	// EnableSemanticSearch enables semantic context retrieval
	// (experimental).
	EnableSemanticSearch bool `mapstructure:"enable_semantic_search"`
	// SearchStrategy is the context retrieval strategy:
	// include_all | semantic.
	SearchStrategy string `mapstructure:"search_strategy"`
	// MaxContextTokens is the soft limit on context window token
	// count.
	MaxContextTokens int `mapstructure:"max_context_tokens"`
	// TrimRatio is the fraction of context to trim when exceeding
	// limits (0.0-1.0).
	TrimRatio float64 `mapstructure:"trim_ratio"`
	// SummaryCooldown is the minimum interval between progressive
	// summarizations.
	SummaryCooldown time.Duration `mapstructure:"summary_cooldown"`
	// SummaryTimeout is the maximum time allowed for a single
	// summarization call.
	SummaryTimeout time.Duration `mapstructure:"summary_timeout"`
}

// ============================================================================
// Feature Tool Configurations
// ============================================================================

// BrowserConfig defines web automation and file caching settings.
type BrowserConfig struct {
	// Enabled enables the browser automation feature.
	Enabled bool `mapstructure:"enabled"`
	// BrowserType is the browser engine: chromium, firefox,
	// webkit.
	BrowserType string `mapstructure:"browser_type"`
	// Headless runs the browser without a visible window.
	Headless bool `mapstructure:"headless"`
	// CacheDir is the local directory for downloaded file
	// caching.
	CacheDir string `mapstructure:"cache_dir"`
	// MaxDownloadSize is the maximum single download size in
	// bytes.
	MaxDownloadSize int64 `mapstructure:"max_download_size"`
	// Timeout is the HTTP request timeout.
	Timeout time.Duration `mapstructure:"timeout"`
	// BrowserPath is the custom Chrome/Chromium executable path.
	BrowserPath string `mapstructure:"browser_path"`
	// ViewportWidth sets the browser viewport width in pixels.
	ViewportWidth int `mapstructure:"viewport_width"`
	// ViewportHeight sets the browser viewport height in pixels.
	ViewportHeight int `mapstructure:"viewport_height"`
	// Stealth enables anti-detection mode.
	Stealth bool `mapstructure:"stealth"`
	// SearchBackend is the web search engine: duckduckgo
	// (default), searxng, tavily.
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
	// OutputDir is the directory for generated chart/diagram
	// files.
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
type TopOfMindConfig struct {
	// Enabled enables Top of Mind instruction injection.
	Enabled bool `mapstructure:"enabled"`
	// InstructionFile is the path to the persistent instructions
	// file.
	InstructionFile string `mapstructure:"instruction_file"`
	// MaxLength is the maximum instruction length in characters.
	MaxLength int `mapstructure:"max_length"`
}

// CodeModeConfig defines JavaScript code execution sandbox settings.
type CodeModeConfig struct {
	// Enabled enables the Code Mode feature.
	Enabled bool `mapstructure:"enabled"`
	// Timeout is the execution timeout per script.
	Timeout time.Duration `mapstructure:"timeout"`
	// MaxMemoryMB is the memory limit for the JS engine in
	// megabytes.
	MaxMemoryMB int `mapstructure:"max_memory_mb"`
}

// ============================================================================
// Apps Configuration (Clone & Pack)
// ============================================================================

// AppsConfig defines custom HTML standalone application settings.
type AppsConfig struct {
	// Enabled enables the Apps feature.
	Enabled bool `mapstructure:"enabled"`
	// AppDir is the storage directory for app HTML files.
	AppDir string `mapstructure:"app_dir"`
	// Clone defines defaults for website cloning.
	Clone CloneDefaults `mapstructure:"clone"`
	// Pack defines defaults for app packaging.
	Pack PackDefaults `mapstructure:"pack"`
}

// CloneDefaults holds default values for website cloning operations.
type CloneDefaults struct {
	// MaxPages limits the total number of pages (0 = unlimited).
	MaxPages int `mapstructure:"max_pages"`
	// MaxDepth limits the link depth from the seed URL
	// (0 = unlimited).
	MaxDepth int `mapstructure:"max_depth"`
	// Traversal sets the crawl strategy: "bfs" or "dfs".
	Traversal string `mapstructure:"traversal"`
	// Subdomains includes subdomains of the seed host in scope.
	Subdomains bool `mapstructure:"subdomains"`
	// ScopePrefix restricts crawling to paths starting with this
	// prefix.
	ScopePrefix string `mapstructure:"scope_prefix"`
	// Workers is the number of concurrent page renderers.
	Workers int `mapstructure:"workers"`
	// AssetWorkers is the number of concurrent asset downloaders.
	AssetWorkers int `mapstructure:"asset_workers"`
	// BrowserPages is the Chrome tab pool size
	// (0 = same as Workers).
	BrowserPages int `mapstructure:"browser_pages"`
	// Timeout is the per-page HTTP request timeout in seconds.
	Timeout int `mapstructure:"timeout"`
	// RenderTimeout is the hard timeout for page rendering in
	// seconds.
	RenderTimeout int `mapstructure:"render_timeout"`
	// Settle is the network-idle settle wait in milliseconds.
	Settle int `mapstructure:"settle"`
	// Scroll enables auto-scrolling to trigger lazy loading.
	Scroll bool `mapstructure:"scroll"`
	// RespectRobots controls whether to obey robots.txt rules.
	RespectRobots bool `mapstructure:"respect_robots"`
	// CrawlDelay overrides the robots.txt crawl-delay in
	// milliseconds (0 = use robots.txt).
	CrawlDelay int `mapstructure:"crawl_delay"`
	// NoSitemap disables sitemap-based URL discovery.
	NoSitemap bool `mapstructure:"no_sitemap"`
	// DedupContent enables SHA-256 content deduplication with
	// hard links.
	DedupContent bool `mapstructure:"dedup_content"`
	// MobileReadable injects responsive CSS for mobile viewing.
	MobileReadable bool `mapstructure:"mobile_readable"`
	// EnableResume saves frontier state for resuming interrupted
	// crawls.
	EnableResume bool `mapstructure:"enable_resume"`
	// Persist controls whether frontier state is written to
	// disk on completion.
	Persist bool `mapstructure:"persist"`
	// Incremental enables ETag/Last-Modified caching.
	Incremental bool `mapstructure:"incremental"`
	// CacheMaxAge is the maximum age of cached content in
	// seconds.
	CacheMaxAge int `mapstructure:"cache_max_age"`
	// Headless runs the browser without a visible window.
	Headless bool `mapstructure:"headless"`
	// Stealth injects anti-detection scripts.
	Stealth bool `mapstructure:"stealth"`
	// ChromeProfile is the persistent browser profile directory.
	ChromeProfile string `mapstructure:"chrome_profile"`
	// ChromePath is the custom Chrome executable path.
	ChromePath string `mapstructure:"chrome_path"`
	// AntibotEnabled enables anti-bot detection and escalation.
	AntibotEnabled bool `mapstructure:"antibot_enabled"`
	// AntibotAutoEscalate enables 5-level automatic escalation.
	AntibotAutoEscalate bool `mapstructure:"antibot_auto_escalate"`
	// AssetSameDomain skips downloading assets from 3rd-party
	// domains.
	AssetSameDomain bool `mapstructure:"asset_same_domain"`
	// MaxAssetBytes is the per-asset size cap in bytes.
	MaxAssetBytes int64 `mapstructure:"max_asset_bytes"`
	// CookieFile is the path to a Netscape-format cookie file.
	CookieFile string `mapstructure:"cookie_file"`
	// UserAgent overrides the default User-Agent string.
	UserAgent string `mapstructure:"user_agent"`
}

// PackDefaults holds default values for app packaging operations.
type PackDefaults struct {
	// Compress enables zstd compression for ZIM output.
	// Default: true.
	Compress bool `mapstructure:"compress"`
	// Incremental enables incremental ZIM building (reuses
	// unchanged clusters from cache). Default: false.
	Incremental bool `mapstructure:"incremental"`
	// Language is the ISO 639-3 language code for ZIM metadata.
	// Default: "eng".
	Language string `mapstructure:"language"`
	// Creator is the archive creator for ZIM metadata.
	// Default: "Wukong".
	Creator string `mapstructure:"creator"`
	// Format is the default output format: html | zim | binary |
	// app. Default: "html".
	Format string `mapstructure:"format"`
}

// ============================================================================
// Orchestration & Discovery Configuration
// ============================================================================

// ARDConfig defines Agentic Resource Discovery settings.
type ARDConfig struct {
	// Enabled enables the ARD feature. Default: false.
	Enabled bool `mapstructure:"enabled"`
	// RegistryURL is the remote ARD registry URL for federation
	// search.
	RegistryURL string `mapstructure:"registry_url"`
	// CatalogPath is the local ARD catalog file path.
	CatalogPath string `mapstructure:"catalog_path"`
	// PublishPort is the port for Wukong's own ARD registry
	// server. Default: 0 (disabled).
	PublishPort int `mapstructure:"publish_port"`
	// PublishEnabled controls whether Wukong exposes itself as a
	// discoverable ARD registry. Default: false.
	PublishEnabled bool `mapstructure:"publish_enabled"`
}

// SummonConfig defines sub-agent delegation and A2A remote agent
// settings.
type SummonConfig struct {
	// Enabled enables the Summon sub-agent delegation feature.
	Enabled bool `mapstructure:"enabled"`
	// SkillsDir is the directory containing skill/recipe .md
	// files.
	SkillsDir string `mapstructure:"skills_dir"`
	// MaxConcurrent is the maximum number of concurrent
	// sub-agents.
	MaxConcurrent int `mapstructure:"max_concurrent"`
	// A2ARemotes lists remote A2A agents available for
	// delegation.
	A2ARemotes []A2ARemoteConfig `mapstructure:"a2a_remotes"`
}

// A2ARemoteConfig defines a remote A2A agent for sub-agent
// delegation.
type A2ARemoteConfig struct {
	// Name is the unique identifier for the remote agent.
	Name string `mapstructure:"name"`
	// Description explains what the remote agent does.
	Description string `mapstructure:"description"`
	// ServerURL is the A2A server endpoint URL.
	ServerURL string `mapstructure:"server_url"`
	// AuthType is the authentication method: jwt, api_key,
	// oauth2.
	AuthType string `mapstructure:"auth_type"`
	// APIKey is the API key for api_key authentication.
	// Supports ${ENV_VAR} expansion.
	APIKey string `mapstructure:"api_key"`
	// APIKeyHeader is the custom header name for API key
	// (default: X-API-Key).
	APIKeyHeader string `mapstructure:"api_key_header"`
	// JWTSecret is the shared secret for JWT authentication.
	// Supports ${ENV_VAR} expansion.
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

// ANPConfig defines Agent Network Protocol (ANP) integration
// settings. ANP enables W3C DID-based identity, multi-protocol
// capability discovery, E2EE messaging, and JSON-RPC 2.0
// agent-to-agent communication.
//
// When enabled, Wukong exposes:
//   - ADP (Agent Description Protocol) documents for unified
//     capability description
//   - Meta-protocol endpoints for capability negotiation
//     (JSON-RPC 2.0 over HTTP on port)
//   - DID documents for cryptographic identity
//     (did:wba:domain:path, Ed25519 + X25519)
//   - HTTP message signing for request integrity (RFC 9421)
//   - E2EE encrypted agent-to-agent messaging
//     (X25519 + ChaCha20-Poly1305)
type ANPConfig struct {
	// Enabled enables ANP protocol support. Default: false.
	Enabled bool `mapstructure:"enabled"`
	// DIDDomain is the domain part of the did:wba identifier
	// (e.g. "wukong.example.com"). Default: os.Hostname().
	DIDDomain string `mapstructure:"did_domain"`
	// DIDPath is the optional path suffix for the DID
	// (e.g. "/agents/wukong").
	DIDPath string `mapstructure:"did_path"`
	// Port is the listen port for the ANP meta-protocol HTTP
	// server that serves capability negotiation and E2EE relay
	// endpoints. Default: 9092.
	Port int `mapstructure:"port"`
	// DiscoveryEnabled exposes .well-known/agent-descriptions
	// and /agents/{name}/ad.json endpoints. Default: true.
	DiscoveryEnabled bool `mapstructure:"discovery_enabled"`
	// MetaProtocolEnabled enables JSON-RPC 2.0 meta-protocol
	// for capability negotiation. Default: true.
	MetaProtocolEnabled bool `mapstructure:"meta_protocol_enabled"`
	// HTTPSignEnabled enables RFC 9421 HTTP message signing
	// for outgoing agent requests. Default: true.
	HTTPSignEnabled bool `mapstructure:"http_sign_enabled"`
	// E2EEEnabled enables X25519+ChaCha20-Poly1305 end-to-end
	// encryption for agent-to-agent messages. Default: true.
	E2EEEnabled bool `mapstructure:"e2ee_enabled"`
	// A2AEnabled autocreates an A2A Server card for ANP
	// discovery when the A2A server is enabled. Default: true.
	A2AEnabled bool `mapstructure:"a2a_enabled"`
	// MCPEnabled autocreates an MCP Server card for ANP
	// discovery when the ACP MCP bridge is enabled.
	// Default: true.
	MCPEnabled bool `mapstructure:"mcp_enabled"`
	// AGUIEnabled autocreates an AG-UI Server card for ANP
	// discovery when the AG-UI server is enabled.
	// Default: true.
	AGUIEnabled bool `mapstructure:"agui_enabled"`
}

// SkillConfig defines the tRPC Agent Skill repository system
// settings.
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

// EvolutionConfig defines the skill self-evolution system settings.
type EvolutionConfig struct {
	// Enabled enables the skill evolution system. Default: false.
	Enabled bool `mapstructure:"enabled"`
	// AutoPatch automatically applies patches without user
	// confirmation. Default: false.
	AutoPatch bool `mapstructure:"auto_patch"`
	// AnalysisProvider is the provider name for the evolution
	// analysis model.
	AnalysisProvider string `mapstructure:"analysis_provider"`
	// AnalysisModel is the model name for evolution analysis.
	AnalysisModel string `mapstructure:"analysis_model"`
	// MinConfidence is the minimum confidence (0.0-1.0) required
	// to accept a patch suggestion. Default: 0.7.
	MinConfidence float64 `mapstructure:"min_confidence"`
	// CooldownPeriod is the minimum interval between two patches
	// for the same skill. Default: "30m".
	CooldownPeriod time.Duration `mapstructure:"cooldown_period"`
	// MaxPatchesPerDay limits the total patches applied per skill
	// within 24 hours. Default: 10.
	MaxPatchesPerDay int `mapstructure:"max_patches_per_day"`
	// MaxVersionsKept is the maximum number of historical versions
	// to retain per skill. Default: 10.
	MaxVersionsKept int `mapstructure:"max_versions_kept"`
	// MaxPatchSize limits the maximum patch size in bytes.
	// Default: 8192 (8KB).
	MaxPatchSize int `mapstructure:"max_patch_size"`
	// AnalysisTimeout is the timeout for each evolution analysis
	// job. Default: "60s".
	AnalysisTimeout time.Duration `mapstructure:"analysis_timeout"`
}

// KnowledgeConfig defines settings for the RAG knowledge base
// system.
type KnowledgeConfig struct {
	// Enabled enables the knowledge base system. Default: false.
	Enabled bool `mapstructure:"enabled"`
	// EmbedderProvider is the provider name for generating
	// embeddings.
	EmbedderProvider string `mapstructure:"embedder_provider"`
	// EmbedderModel is the embedding model name.
	// Default: "text-embedding-3-small" (1536 dimensions).
	EmbedderModel string `mapstructure:"embedder_model"`
	// Sources lists document source directories to load at
	// startup.
	Sources []string `mapstructure:"sources"`
	// SourceURLs lists document source URLs to fetch and index.
	SourceURLs []string `mapstructure:"source_urls"`
	// VectorStore is the vector store backend: "inmemory"
	// (default).
	VectorStore string `mapstructure:"vector_store"`
	// MaxResults limits search results per query. Default: 5.
	MaxResults int `mapstructure:"max_results"`
	// EnableSourceSync enables automatic re-indexing when source
	// files change. Default: false.
	EnableSourceSync bool `mapstructure:"enable_source_sync"`
	// ReRankerEnabled enables result re-ranking. Default: false.
	ReRankerEnabled bool `mapstructure:"reranker_enabled"`
	// SearchToolName is the tool name registered to the agent.
	// Default: "knowledge_search".
	SearchToolName string `mapstructure:"search_tool_name"`
}

// DifyConfig defines settings for the Dify AI platform integration.
type DifyConfig struct {
	// Enabled enables the Dify agent mode. Default: false.
	Enabled bool `mapstructure:"enabled"`
	// BaseURL is the Dify API endpoint.
	BaseURL string `mapstructure:"base_url"`
	// APISecret is the Dify API secret key for authentication.
	APISecret string `mapstructure:"api_secret"`
	// AgentName is the agent name exposed to the workflow.
	// Default: "dify".
	AgentName string `mapstructure:"agent_name"`
	// EnableStreaming enables SSE streaming from Dify.
	// Default: false.
	EnableStreaming bool `mapstructure:"enable_streaming"`
	// Timeout is the HTTP request timeout. Default: "120s".
	Timeout time.Duration `mapstructure:"timeout"`
}

// WorkflowConfig defines multi-mode agent orchestration settings.
type WorkflowConfig struct {
	// Mode is the execution mode.
	Mode string `mapstructure:"mode"`
	// MaxIterations is the maximum iterations for cycle/graph
	// modes.
	MaxIterations int `mapstructure:"max_iterations"`
	// CycleMode selects the cycle strategy: "default"
	// (planner/executor) or "code_review".
	CycleMode string `mapstructure:"cycle_mode"`
	// StreamMode enables inter-node streaming via StreamHub
	// ("none"/"hub").
	StreamMode string `mapstructure:"stream_mode"`
	// CacheEnabled enables node caching for graph workflows.
	CacheEnabled bool `mapstructure:"cache_enabled"`
	// Engine is the Graph execution engine: "bsp" (default) or
	// "dag".
	Engine string `mapstructure:"engine"`
	// SubAgents defines custom sub-agent configurations.
	SubAgents []WorkflowSubAgentConfig `mapstructure:"sub_agents"`
	// TeamMembers defines team member agents for team_coordinator
	// /team_swarm modes.
	TeamMembers []TeamMemberConfig `mapstructure:"team_members"`
	// ClaudeCodeBin is the claude CLI path for claude_code mode.
	ClaudeCodeBin string `mapstructure:"claude_code_bin"`
	// CodexBin is the codex CLI path for codex mode.
	CodexBin string `mapstructure:"codex_bin"`
}

// WorkflowSubAgentConfig defines a custom sub-agent for workflow
// modes.
type WorkflowSubAgentConfig struct {
	// Name is the sub-agent identifier.
	Name string `mapstructure:"name"`
	// Instruction is the system prompt for this sub-agent.
	Instruction string `mapstructure:"instruction"`
	// AllowedTools lists tool names this sub-agent may use
	// (empty = all).
	AllowedTools []string `mapstructure:"allowed_tools"`
	// AllTools grants access to all available tools.
	AllTools bool `mapstructure:"all_tools"`
}

// TeamMemberConfig defines a team member for team_coordinator or
// team_swarm modes.
type TeamMemberConfig struct {
	// Name is the member identifier.
	Name string `mapstructure:"name"`
	// Instruction is the system prompt for this member.
	Instruction string `mapstructure:"instruction"`
	// AllowedTools lists tool names this member may use.
	AllowedTools []string `mapstructure:"allowed_tools"`
	// AllTools grants access to all tools.
	AllTools bool `mapstructure:"all_tools"`
}

// ============================================================================
// Service Endpoint Configuration
// ============================================================================

// A2AServerConfig defines the local A2A protocol server.
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

// AGUIConfig defines settings for the AG-UI SSE server.
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

// ACPServerConfig defines settings for the Agent Client Protocol
// (ACP) server.
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

// ACPMCPConfig defines settings for the MCP Bridge.
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
// Observability & Evaluation Configuration
// ============================================================================

// EvalConfig defines settings for the agent evaluation system.
type EvalConfig struct {
	// Enabled enables automatic evaluation runs. Default: false.
	Enabled bool `mapstructure:"enabled"`
	// EvalSetPath is the path to the JSON evalset file.
	// Default: ".wukong/evals/default.evalset.json".
	EvalSetPath string `mapstructure:"evalset_path"`
	// ResultsPath is the output path for evaluation results JSON.
	// Default: ".wukong/evals/results.json".
	ResultsPath string `mapstructure:"results_path"`
	// Metrics lists the evaluation metrics to apply.
	Metrics []EvalMetricConfig `mapstructure:"metrics"`
}

// EvalMetricConfig defines a single evaluation metric.
type EvalMetricConfig struct {
	// Name is the metric identifier.
	Name string `mapstructure:"name"`
	// Threshold is the minimum score (0.0-1.0) to pass.
	Threshold float64 `mapstructure:"threshold"`
}

// ArtifactConfig defines artifact storage settings.
type ArtifactConfig struct {
	// Backend is the storage backend: "inmemory" (default),
	// "cos".
	Backend string `mapstructure:"backend"`
	// COSBucketURL is the Tencent COS bucket URL.
	COSBucketURL string `mapstructure:"cos_bucket_url"`
	// COSSecretID is the Tencent Cloud SecretId
	// (or env COS_SECRETID).
	COSSecretID string `mapstructure:"cos_secret_id"`
	// COSSecretKey is the Tencent Cloud SecretKey
	// (or env COS_SECRETKEY).
	COSSecretKey string `mapstructure:"cos_secret_key"`
}

// ObservabilityConfig defines enhanced observability settings.
type ObservabilityConfig struct {
	// LangfuseEnabled enables Langfuse OTLP tracing.
	// Default: false.
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

// TelemetryConfig defines OpenTelemetry observability settings.
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
	// Environment is the deployment environment: development,
	// staging, production.
	Environment string `mapstructure:"environment"`
	// SampleRate controls trace sampling (0.0-1.0,
	// 1.0 = all traces).
	SampleRate float64 `mapstructure:"sample_rate"`
}

// ============================================================================
// OKF (Open Knowledge Format) Configuration
// ============================================================================

// OKFConfig defines settings for the Open Knowledge Format (OKF)
// interoperability system. OKF is a vendor-neutral standard for
// representing knowledge as Markdown files with YAML frontmatter.
//
// When enabled, Wukong can:
//   - Import/export OKF Bundles (knowledge packages)
//   - Inject OKF knowledge index into MemoryFlow wake-up context
//   - Auto-generate OKF concepts from structured data sources
//   - Track knowledge file changes via log.md convention
//   - Register OKF Bundles for ARD federated discovery
//
// See: internal/okf/ for the core implementation.
type OKFConfig struct {
	// Enabled enables the OKF subsystem globally.
	// When false, individual OKF functions can still be called
	// programmatically, but declarative configuration and
	// auto-injection are disabled. Default: false.
	Enabled bool `mapstructure:"enabled"`

	// BundleDir is the default directory for OKF Bundle storage
	// and retrieval. Used by skill export/import, knowledge
	// export/import, and the cortex knowledge index injector.
	// Default: ".wukong/okf".
	BundleDir string `mapstructure:"bundle_dir"`

	// InjectorEnabled enables OKF knowledge overview injection
	// into MemoryFlow wake-up context. When enabled, the agent
	// reads the bundle's index.md on each wake-up and prepends
	// a concise knowledge overview. Default: false.
	InjectorEnabled bool `mapstructure:"injector_enabled"`

	// EnrichmentEnabled enables the EnrichmentAgent for
	// automatic OKF concept generation from structured data
	// sources (DDL, directories). Default: false.
	EnrichmentEnabled bool `mapstructure:"enrichment_enabled"`

	// EnrichmentOutputDir is the output directory for generated
	// OKF concepts. When empty, defaults to BundleDir.
	EnrichmentOutputDir string `mapstructure:"enrichment_output_dir"`

	// AutoExport enables automatic OKF Bundle export of the
	// knowledge base on session end. Default: false.
	AutoExport bool `mapstructure:"auto_export"`

	// RegisterInARD controls whether OKF Bundles are automatically
	// registered in the ARD catalog for federated discovery by
	// other agents. Default: false.
	RegisterInARD bool `mapstructure:"register_in_ard"`
}
