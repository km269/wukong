package config

import "time"

// This file defines the protocol-server security configuration types.
// They lived in internal/server (as real definitions) while
// internal/config only aliased them — but the root WukongConfig
// embeds these types, which made internal/config depend on
// internal/server. The definitions now live here (dependency
// direction: server → config); the server package keeps type aliases
// so all existing references compile unchanged.

// ServerTLSConfig configures optional TLS for a protocol server.
type ServerTLSConfig struct {
	Enabled    bool   `mapstructure:"enabled"`
	CertFile   string `mapstructure:"cert_file"`
	KeyFile    string `mapstructure:"key_file"`
	CACertFile string `mapstructure:"ca_cert_file"`
}

// IsEnabled reports whether TLS should be negotiated (enabled with a
// certificate/key pair configured).
func (c ServerTLSConfig) IsEnabled() bool {
	return c.Enabled && c.CertFile != "" && c.KeyFile != ""
}

// ServerAuthConfig configures endpoint authentication
// (none / api_key / jwt).
type ServerAuthConfig struct {
	Type string `mapstructure:"type"`
	// APIKey and JWTSecret support ${ENV_VAR} expansion; the tag is
	// consumed by the config package's tag-driven expandSecrets walk.
	APIKey    string `mapstructure:"api_key" envexpand:"true"`
	JWTSecret string `mapstructure:"jwt_secret" envexpand:"true"`
}

// ServerRateLimitConfig configures per-endpoint rate limiting.
type ServerRateLimitConfig struct {
	Enabled      bool          `mapstructure:"enabled"`
	MaxPerMinute int           `mapstructure:"max_per_minute"`
	Window       time.Duration `mapstructure:"window"`
}

// ServerSecurityConfig bundles TLS, authentication, and rate-limiting
// settings for a protocol server.
type ServerSecurityConfig struct {
	TLS       ServerTLSConfig       `mapstructure:"tls"`
	Auth      ServerAuthConfig      `mapstructure:"auth"`
	RateLimit ServerRateLimitConfig `mapstructure:"rate_limit"`
}

// ============================================================================
// Service Endpoint Configuration
// ============================================================================

// A2AServerConfig configures the local A2A protocol server.
type A2AServerConfig struct {
	Enabled          bool   `mapstructure:"enabled"`
	Address          string `mapstructure:"address"`
	AgentName        string `mapstructure:"agent_name"`
	AgentDescription string `mapstructure:"agent_description"`
}

// AGUIConfig configures the AG-UI SSE server for web-based chat UIs.
type AGUIConfig struct {
	Enabled  bool                 `mapstructure:"enabled"`
	Address  string               `mapstructure:"address"`
	Path     string               `mapstructure:"path"`
	Security ServerSecurityConfig `mapstructure:"security"`
}

// ACPServerConfig configures the Agent Client Protocol server endpoint.
type ACPServerConfig struct {
	Enabled         bool                 `mapstructure:"enabled"`
	Address         string               `mapstructure:"address"`
	Path            string               `mapstructure:"path"`
	EnableStreaming bool                 `mapstructure:"enable_streaming"`
	Security        ServerSecurityConfig `mapstructure:"security"`
}

// ACPMCPConfig configures the MCP bridge that exposes extensions as an MCP Server.
type ACPMCPConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Address string `mapstructure:"address"`
	Path    string `mapstructure:"path"`
}

// MCPServerConfig configures the standalone MCP server that exposes
// Wukong extensions as a standards-compliant MCP server (JSON-RPC 2.0).
type MCPServerConfig struct {
	Enabled  bool                 `mapstructure:"enabled"`
	Address  string               `mapstructure:"address"`
	Security ServerSecurityConfig `mapstructure:"security"`
}
