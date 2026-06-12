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

	// Workflow configures multi-mode agent orchestration.
	Workflow WorkflowConfig `mapstructure:"workflow"`

	// A2AServer configures the local A2A protocol server.
	A2AServer A2AServerConfig `mapstructure:"a2a_server"`

	// Telemetry configures OpenTelemetry observability.
	Telemetry TelemetryConfig `mapstructure:"telemetry"`
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
	// Type is the provider backend type (openai, anthropic, google, deepseek, ollama, lmstudio).
	Type string `mapstructure:"type"`
	// BaseURL is the API endpoint base URL (auto-filled for known providers if empty).
	BaseURL string `mapstructure:"base_url"`
	// APIKey is the authentication key (supports ${ENV_VAR} expansion).
	APIKey string `mapstructure:"api_key"`
	// Model is the default model name for this provider.
	Model string `mapstructure:"model"`
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
	// context window overflow. Frame-aware for structured results.
	ContextCompaction bool `mapstructure:"context_compaction"`
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
}

// ============================================================================
// Storage Configurations (Session, Memory, Todo, Recall)
// ============================================================================

// SessionConfig defines conversation history storage settings.
type SessionConfig struct {
	// Backend is the storage backend: sqlite | memory.
	Backend string `mapstructure:"backend"`
	// DBPath is the SQLite database file path (shared pool).
	DBPath string `mapstructure:"db_path"`
	// EventLimit is the maximum events retained per session.
	EventLimit int `mapstructure:"event_limit"`
	// TTL is the session time-to-live duration (0 = no expiration).
	TTL time.Duration `mapstructure:"ttl"`
	// EnableSummary enables automatic conversation summarization.
	EnableSummary bool `mapstructure:"enable_summary"`
	// SummaryTrigger is the event count threshold to trigger summarization.
	SummaryTrigger int `mapstructure:"summary_trigger"`
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
type TodoConfig struct {
	// Backend is the storage backend: sqlite | memory.
	Backend string `mapstructure:"backend"`
	// DBPath is the SQLite database file path.
	DBPath string `mapstructure:"db_path"`
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
// Workflow & A2A Server Configuration
// ============================================================================

// WorkflowConfig defines multi-mode agent orchestration settings.
// Supports: single (default), chain, parallel, cycle, graph modes.
type WorkflowConfig struct {
	// Mode is the execution mode: single, chain, parallel, cycle, graph.
	Mode string `mapstructure:"mode"`
	// MaxIterations is the maximum iterations for cycle/graph modes.
	MaxIterations int `mapstructure:"max_iterations"`
	// CycleMode selects the cycle strategy: "default" (planner/executor)
	// or "code_review" (generator/reviewer loop).
	CycleMode string `mapstructure:"cycle_mode"`
	// SubAgents defines custom sub-agent configurations.
	// When empty, default agents are used (planner/executor/reviewer, etc.).
	SubAgents []WorkflowSubAgentConfig `mapstructure:"sub_agents"`
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
	l.v.SetDefault("agent.session_recall_enabled", false)
	l.v.SetDefault("agent.session_recall_limit", 5)
	l.v.SetDefault("agent.json_repair_enabled", false)

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

	// --- Workflow defaults ---
	l.v.SetDefault("workflow.mode", "single")
	l.v.SetDefault("workflow.max_iterations", 10)

	// --- A2A server defaults ---
	l.v.SetDefault("a2a_server.enabled", false)
	l.v.SetDefault("a2a_server.address", ":9090")
	l.v.SetDefault("a2a_server.agent_name", "wukong")
	l.v.SetDefault("a2a_server.agent_description",
		"Wukong AI Agent - A2A service endpoint")

	// --- Telemetry defaults ---
	l.v.SetDefault("telemetry.enabled", false)
	l.v.SetDefault("telemetry.exporter_type", "console")
	l.v.SetDefault("telemetry.endpoint", "localhost:4317")
	l.v.SetDefault("telemetry.service_name", "wukong")
	l.v.SetDefault("telemetry.service_version", "1.0.0")
	l.v.SetDefault("telemetry.environment", "development")
	l.v.SetDefault("telemetry.sample_rate", 1.0)
}
