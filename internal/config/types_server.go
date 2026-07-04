package config

import "github.com/km269/wukong/internal/gateway"

// ============================================================================
// Service Endpoint Configuration
// ============================================================================

// A2AServerConfig configures the local A2A protocol server.
type A2AServerConfig struct {
	Enabled         bool   `mapstructure:"enabled"`
	Address         string `mapstructure:"address"`
	AgentName       string `mapstructure:"agent_name"`
	AgentDescription string `mapstructure:"agent_description"`
}

// AGUIConfig configures the AG-UI SSE server for web-based chat UIs.
type AGUIConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Address string `mapstructure:"address"`
	Path    string `mapstructure:"path"`
}

// ACPServerConfig configures the Agent Client Protocol server endpoint.
type ACPServerConfig struct {
	Enabled         bool   `mapstructure:"enabled"`
	Address         string `mapstructure:"address"`
	Path            string `mapstructure:"path"`
	EnableStreaming bool   `mapstructure:"enable_streaming"`
	AuthType        string `mapstructure:"auth_type"`
	APIKey          string `mapstructure:"api_key"`
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