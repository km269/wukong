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
	ProviderACP       ProviderType = "acp"
)

// ProviderConfig defines a connection to an LLM backend.
// Supported types: openai, anthropic, google, deepseek, ollama, lmstudio, acp.
// API keys support ${ENV_VAR} expansion for secrets management.
type ProviderConfig struct {
	Name     string `mapstructure:"name"`
	Type     string `mapstructure:"type"`
	BaseURL  string `mapstructure:"base_url"`
	APIKey   string `mapstructure:"api_key"`
	Model    string `mapstructure:"model"`
	AgentURL string `mapstructure:"agent_url"`
	MCPPort  string `mapstructure:"mcp_port"`
}

// ExtensionConfig defines an MCP extension (built-in or external).
type ExtensionConfig struct {
	Name                        string            `mapstructure:"name"`
	Type                        string            `mapstructure:"type"`
	Transport                   string            `mapstructure:"transport"`
	Command                     string            `mapstructure:"command"`
	Args                        []string          `mapstructure:"args"`
	URL                         string            `mapstructure:"url"`
	Env                         map[string]string `mapstructure:"env"`
	Enabled                     bool              `mapstructure:"enabled"`
	Timeout                     time.Duration     `mapstructure:"timeout"`
	Deeplink                    string            `mapstructure:"deeplink"`
	Permissions                 []ToolPermission  `mapstructure:"permissions"`
	MCPBroker                   bool              `mapstructure:"mcp_broker"`
	MCPToolFilter               []string          `mapstructure:"mcp_tool_filter"`
	MCPToolExclude              []string          `mapstructure:"mcp_tool_exclude"`
	MCPSessionReconnect         bool              `mapstructure:"mcp_session_reconnect"`
	MCPSessionReconnectAttempts int               `mapstructure:"mcp_session_reconnect_attempts"`
}

// ToolPermission defines allow/deny for a specific tool within an extension.
type ToolPermission struct {
	Tool    string `mapstructure:"tool"`
	Allowed bool   `mapstructure:"allowed"`
}
