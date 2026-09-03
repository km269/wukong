package config

import "time"

// ============================================================================
// Provider & Extension Configuration
// ============================================================================

// ProviderType defines the type of LLM provider.
type ProviderType string

const (
	ProviderOpenAI    ProviderType = "openai"
	ProviderAnthropic ProviderType = "anthropic"
	ProviderGoogle    ProviderType = "google"
	ProviderDeepSeek  ProviderType = "deepseek"
	ProviderOllama    ProviderType = "ollama"
	ProviderLMStudio  ProviderType = "lmstudio"
	ProviderVLLM      ProviderType = "vllm"
	ProviderACP       ProviderType = "acp"
)

// ProviderConfig defines a connection to an LLM backend.
// Supported types: openai, anthropic, google, deepseek, ollama, lmstudio, vllm, acp.
// API keys support ${ENV_VAR} expansion for secrets management.
type ProviderConfig struct {
	Name     string `mapstructure:"name"`
	Type     string `mapstructure:"type"`
	BaseURL  string `mapstructure:"base_url" envexpand:"true"`
	APIKey   string `mapstructure:"api_key" envexpand:"true"`
	Model    string `mapstructure:"model" envexpand:"true"`
	AgentURL string `mapstructure:"agent_url"`
	MCPPort  string `mapstructure:"mcp_port"`
	// ContextWindow is the model's maximum context token count
	// (e.g. 32768 for qwen3-27b, 128000 for gpt-4o). When set,
	// EffectiveContextWindow() returns this value; otherwise it
	// falls back to a per-type default. Used by ContextRevisionEngine
	// to clamp prompts before sending — prevents 400 Bad Request
	// when revision.max_context_tokens exceeds the actual model limit.
	ContextWindow int `mapstructure:"context_window"`
}

// ExtensionConfig defines an MCP extension (built-in or external).
type ExtensionConfig struct {
	Name        string            `mapstructure:"name"`
	Type        string            `mapstructure:"type"`
	Transport   string            `mapstructure:"transport"`
	Command     string            `mapstructure:"command"`
	Args        []string          `mapstructure:"args"`
	URL         string            `mapstructure:"url"`
	Env         map[string]string `mapstructure:"env"`
	Enabled     bool              `mapstructure:"enabled"`
	Timeout     time.Duration     `mapstructure:"timeout"`
	Deeplink    string            `mapstructure:"deeplink"`
	Permissions []ToolPermission  `mapstructure:"permissions"`
	// ToolScopes declares permission domains per tool for external
	// MCP tools, consulted by the capability registry descriptors
	// and the Guard command-validation seam. Example:
	//   tool_scopes: { run_cmd: ["shell"] }
	// Declared tools are exempt from the legacy name heuristic under
	// agent.command_validation_mode: hybrid/descriptor.
	ToolScopes                  map[string][]string `mapstructure:"tool_scopes"`
	MCPBroker                   bool                `mapstructure:"mcp_broker"`
	MCPToolFilter               []string            `mapstructure:"mcp_tool_filter"`
	MCPToolExclude              []string            `mapstructure:"mcp_tool_exclude"`
	MCPSessionReconnect         bool                `mapstructure:"mcp_session_reconnect"`
	MCPSessionReconnectAttempts int                 `mapstructure:"mcp_session_reconnect_attempts"`
}

// ToolPermission defines allow/deny for a specific tool within an extension.
type ToolPermission struct {
	Tool    string `mapstructure:"tool"`
	Allowed bool   `mapstructure:"allowed"`
}
