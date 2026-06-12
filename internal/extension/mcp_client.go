// Package extension provides native MCP client integration using
// trpc-mcp-go. This wraps the MCP protocol client for fine-grained
// control over tool discovery, session management, and lifecycle.
package extension

import (
	"context"
	"fmt"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	agentmcp "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
	"trpc.group/trpc-go/trpc-mcp-go"
)

// MCPClient wraps the trpc-mcp-go native client for external MCP
// server communication. It provides tool discovery, session
// management, and lifecycle control with improved observability
// compared to the legacy agentmcp.NewMCPToolSet approach.
type MCPClient struct {
	client   *mcp.Client
	toolSet  tool.ToolSet
	name     string
	connCfg  config.ExtensionConfig
}

// NewMCPClient creates a new MCP client for the given extension
// configuration. It supports stdio, sse, and streamable HTTP
// transport modes.
func NewMCPClient(
	ctx context.Context, ext config.ExtensionConfig,
) (*MCPClient, error) {
	var ts tool.ToolSet

	switch ext.Transport {
	case "stdio":
		ts = createStdioToolSet(ctx, ext)
	case "sse", "streamable":
		ts = createStreamableToolSet(ctx, ext)
	default:
		return nil, fmt.Errorf(
			"unsupported MCP transport: %s", ext.Transport,
		)
	}

	if ts == nil {
		return nil, fmt.Errorf(
			"failed to create MCP toolset for %q", ext.Name,
		)
	}

	return &MCPClient{
		toolSet: ts,
		name:    ext.Name,
		connCfg: ext,
	}, nil
}

// ToolSet returns the agent-compatible tool set for the MCP server.
func (c *MCPClient) ToolSet() tool.ToolSet {
	return c.toolSet
}

// Close releases the MCP client resources.
func (c *MCPClient) Close() error {
	if c.toolSet != nil {
		return c.toolSet.Close()
	}
	return nil
}

// createStdioToolSet creates a tool set for a stdio-transport MCP
// server using the agent framework's MCP integration.
func createStdioToolSet(
	ctx context.Context, ext config.ExtensionConfig,
) tool.ToolSet {
	connCfg := agentmcp.ConnectionConfig{
		Transport: ext.Transport,
		Command:   ext.Command,
		Args:      ext.Args,
	}

	ts := agentmcp.NewMCPToolSet(connCfg)
	if err := ts.Init(ctx); err != nil {
		util.Logger.Warn("stdio MCP init failed",
			"extension", ext.Name,
			"error", err.Error(),
		)
		return nil
	}
	return ts
}

// createStreamableToolSet creates a tool set for an HTTP-based MCP
// server (SSE or Streamable HTTP transport).
func createStreamableToolSet(
	ctx context.Context, ext config.ExtensionConfig,
) tool.ToolSet {
	connCfg := agentmcp.ConnectionConfig{
		Transport: ext.Transport,
		ServerURL: ext.URL,
	}

	ts := agentmcp.NewMCPToolSet(connCfg)
	if err := ts.Init(ctx); err != nil {
		util.Logger.Warn("streamable MCP init failed",
			"extension", ext.Name,
			"error", err.Error(),
		)
		return nil
	}
	return ts
}
