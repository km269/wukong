// Package extension provides the MCP extension management system for wukong.
// It manages built-in extensions and external MCP server integrations,
// providing a unified tool interface for the agent.
package extension

import (
	"context"
	"fmt"
	"sync"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/extension/builtin"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	agentmcp "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

// Manager handles the lifecycle of MCP extensions.
type Manager struct {
	mu       sync.RWMutex
	toolSets map[string]tool.ToolSet
	cfg      *config.WukongConfig
}

// NewManager creates a new extension manager.
func NewManager(cfg *config.WukongConfig) *Manager {
	return &Manager{
		toolSets: make(map[string]tool.ToolSet),
		cfg:      cfg,
	}
}

// Initialize loads and initializes all enabled extensions.
func (m *Manager) Initialize(ctx context.Context) error {
	enabled := m.cfg.EnabledExtensions()
	for _, ext := range enabled {
		if err := m.registerExtension(ctx, ext); err != nil {
			return fmt.Errorf(
				"register extension %q: %w", ext.Name, err,
			)
		}
	}
	return nil
}

// ToolSets returns all active tool sets.
func (m *Manager) ToolSets() []tool.ToolSet {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]tool.ToolSet, 0, len(m.toolSets))
	for _, ts := range m.toolSets {
		result = append(result, ts)
	}
	return result
}

// Close shuts down all extensions and releases resources.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, ts := range m.toolSets {
		if err := ts.Close(); err != nil {
			return fmt.Errorf(
				"close extension %q: %w", name, err,
			)
		}
		delete(m.toolSets, name)
	}
	return nil
}

// registerExtension registers a single extension based on its type.
func (m *Manager) registerExtension(
	ctx context.Context, ext config.ExtensionConfig,
) error {
	switch ext.Type {
	case "builtin":
		return m.registerBuiltin(ctx, ext)
	case "external":
		return m.registerExternal(ctx, ext)
	default:
		return fmt.Errorf(
			"unknown extension type: %s", ext.Type,
		)
	}
}

// registerBuiltin registers a built-in extension.
func (m *Manager) registerBuiltin(
	ctx context.Context, ext config.ExtensionConfig,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch ext.Name {
	case "developer":
		ts := builtin.NewDeveloperToolSet()
		if err := ts.Init(ctx); err != nil {
			return fmt.Errorf(
				"init developer extension: %w", err,
			)
		}
		m.toolSets[ext.Name] = ts
		return nil
	default:
		return fmt.Errorf(
			"unknown builtin extension: %s", ext.Name,
		)
	}
}

// registerExternal registers an external MCP server extension.
func (m *Manager) registerExternal(
	ctx context.Context, ext config.ExtensionConfig,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	connCfg := agentmcp.ConnectionConfig{
		Transport: ext.Transport,
	}

	switch ext.Transport {
	case "stdio":
		connCfg.Command = ext.Command
		connCfg.Args = ext.Args
	case "sse", "streamable":
		connCfg.ServerURL = ext.URL
	default:
		return fmt.Errorf(
			"unsupported transport: %s", ext.Transport,
		)
	}

	ts := agentmcp.NewMCPToolSet(connCfg)
	if err := ts.Init(ctx); err != nil {
		return fmt.Errorf(
			"init external MCP server %q: %w", ext.Name, err,
		)
	}

	m.toolSets[ext.Name] = ts
	return nil
}
