package config

import (
	"github.com/km269/wukong/internal/gateway"
	"github.com/km269/wukong/internal/server"
)

// ServerSecurityConfig is a type alias to the server package's
// ServerSecurityConfig. This keeps the dependency direction clean
// (config → server) while avoiding circular imports.
type ServerSecurityConfig = server.ServerSecurityConfig

type ServerTLSConfig = server.ServerTLSConfig
type ServerAuthConfig = server.ServerAuthConfig
type ServerRateLimitConfig = server.ServerRateLimitConfig

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

// GatewayConfig embeds the gateway package's GatewayConfig.
// The concrete type lives in internal/gateway/config.go.
type GatewayConfig = gateway.GatewayConfig
