// Package extension provides the MCP extension management system for wukong.
// It manages built-in extensions and external MCP server integrations,
// providing a unified tool interface for the agent.
// This implements Goose's Extension Manager: dynamic discovery,
// enable/disable, fine-grained tool permission control.
package extension

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"

	mcpcfg "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
	"trpc.group/trpc-go/trpc-agent-go/tool/mcpbroker"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Manager handles the lifecycle of MCP extensions with dynamic
// enable/disable and fine-grained permission control.
type Manager struct {
	mu       sync.RWMutex
	toolSets map[string]tool.ToolSet
	status   map[string]ExtensionInfo
	cfg      *config.WukongConfig
}

// NewManager creates a new extension manager.
func NewManager(cfg *config.WukongConfig) *Manager {
	return &Manager{
		toolSets: make(map[string]tool.ToolSet),
		status:   make(map[string]ExtensionInfo),
		cfg:      cfg,
	}
}

// Initialize loads and initializes all enabled extensions.
func (m *Manager) Initialize(ctx context.Context) error {
	enabled := m.cfg.EnabledExtensions()

	// Collect broker-enabled external extensions for MCP Broker.
	// These servers are registered into a single broker toolset
	// instead of exposing all their tools individually.
	var brokerServers map[string]mcpcfg.ConnectionConfig
	for _, ext := range enabled {
		if ext.Type == "external" && ext.MCPBroker {
			if brokerServers == nil {
				brokerServers = make(map[string]mcpcfg.ConnectionConfig)
			}
			brokerServers[ext.Name] = toMCPConnectionConfig(ext)
			continue // Skip individual registration
		}
		if err := m.registerExtension(ctx, ext); err != nil {
			return fmt.Errorf(
				"register extension %q: %w", ext.Name, err,
			)
		}
	}

	// Register MCP Broker if any broker-enabled servers exist.
	// The broker exposes 4 tools: mcp_list_servers, mcp_list_tools,
	// mcp_inspect_tools, mcp_call — allowing on-demand discovery.
	if len(brokerServers) > 0 {
		broker := mcpbroker.New(
			mcpbroker.WithServers(brokerServers),
		)
		m.mu.Lock()
		m.toolSets["mcp_broker"] = &brokerToolSet{broker: broker}
		m.status["mcp_broker"] = ExtensionInfo{
			Name:         "mcp_broker",
			Type:         "external",
			Status:       StatusEnabled,
			ToolCount:    4,
			RegisteredAt: time.Now(),
		}
		m.mu.Unlock()
		util.Logger.Info("MCP broker enabled",
			"server_count", len(brokerServers))
	}

	return nil
}

// brokerToolSet wraps an mcpbroker.Broker to satisfy the
// tool.ToolSet interface.
type brokerToolSet struct {
	broker *mcpbroker.Broker
}

func (b *brokerToolSet) Tools(_ context.Context) []tool.Tool {
	return b.broker.Tools()
}

func (b *brokerToolSet) Name() string { return "mcp_broker" }

func (b *brokerToolSet) Close() error { return nil }

// toMCPConnectionConfig converts an ExtensionConfig to an MCP
// connection configuration used by the MCP broker.
func toMCPConnectionConfig(ext config.ExtensionConfig) mcpcfg.ConnectionConfig {
	cc := mcpcfg.ConnectionConfig{
		Transport: ext.Transport,
		Timeout:   ext.Timeout,
	}
	switch ext.Transport {
	case "stdio":
		cc.Command = ext.Command
		cc.Args = ext.Args
	default:
		cc.ServerURL = ext.URL
	}
	return cc
}

// ToolSets returns all active tool sets.
func (m *Manager) ToolSets() []tool.ToolSet {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]tool.ToolSet, 0, len(m.toolSets))
	for _, ts := range m.toolSets {
		// Skip nil placeholders for extensions that are created
		// during bootstrap (apps, code_mode, top_of_mind).
		if ts == nil {
			continue
		}
		result = append(result, ts)
	}
	return result
}

// EnableExtension dynamically enables an extension by name.
// If the extension is not in config, it returns an error.
func (m *Manager) EnableExtension(
	ctx context.Context, name string,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ext := m.cfg.FindExtension(name)
	if ext == nil {
		return fmt.Errorf("extension %q not found in config", name)
	}

	if info, ok := m.status[name]; ok && info.Status == StatusEnabled {
		return fmt.Errorf("extension %q is already enabled", name)
	}

	ext.Enabled = true
	return m.registerExtensionLocked(ctx, *ext)
}

// DisableExtension dynamically disables an extension by name.
func (m *Manager) DisableExtension(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ts, ok := m.toolSets[name]
	if !ok {
		return fmt.Errorf("extension %q is not active", name)
	}

	if err := ts.Close(); err != nil {
		return fmt.Errorf("close extension %q: %w", name, err)
	}

	delete(m.toolSets, name)
	m.status[name] = ExtensionInfo{
		Name:   name,
		Status: StatusDisabled,
	}

	// Update config
	ext := m.cfg.FindExtension(name)
	if ext != nil {
		ext.Enabled = false
	}

	return nil
}

// GetStatus returns the status of a specific extension.
func (m *Manager) GetStatus(name string) (ExtensionInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	info, ok := m.status[name]
	return info, ok
}

// ListExtensions returns all registered extensions with their status.
func (m *Manager) ListExtensions() []ExtensionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]ExtensionInfo, 0, len(m.status))
	for _, info := range m.status {
		result = append(result, info)
	}
	return result
}

// RegisterFromDeeplink parses a deeplink URL and registers the extension.
// Deeplink format: wukong://extension?name=xxx&type=external&transport=stdio&command=npx&args=-y&args=@modelcontextprotocol/server-github
func (m *Manager) RegisterFromDeeplink(
	ctx context.Context, deeplinkURL string,
) error {
	ext, err := parseDeeplink(deeplinkURL)
	if err != nil {
		return fmt.Errorf("parse deeplink: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if already registered
	if _, exists := m.status[ext.Name]; exists {
		return fmt.Errorf(
			"extension %q already registered", ext.Name,
		)
	}

	// Add to config
	m.cfg.Extensions = append(m.cfg.Extensions, ext)

	// Register
	return m.registerExtensionLocked(ctx, ext)
}

// SetMemoryService injects the memory service into the memory toolset
// if it's registered. Must be called after Initialize.
func (m *Manager) SetMemoryService(svc any, appName, userID string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ts, ok := m.toolSets["memory"]
	if !ok {
		return
	}

	// Use a dynamic method call since we can't import the memory package
	// directly without a circular dependency. The MemoryToolSet implements
	// SetMemoryService(memory.Service, string, string).
	type memorySvcSetter interface {
		SetMemoryService(svc any, appName, userID string)
	}
	if setter, ok := ts.(memorySvcSetter); ok {
		setter.SetMemoryService(svc, appName, userID)
	}
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
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.registerExtensionLocked(ctx, ext)
}

// registerExtensionLocked registers without acquiring lock.
func (m *Manager) registerExtensionLocked(
	ctx context.Context, ext config.ExtensionConfig,
) error {
	m.status[ext.Name] = ExtensionInfo{
		Name:         ext.Name,
		Type:         ext.Type,
		Status:       StatusLoading,
		Transport:    ext.Transport,
		Permissions:  ext.Permissions,
		RegisteredAt: time.Now(),
	}

	var err error
	switch ext.Type {
	case "builtin":
		err = m.registerBuiltinLocked(ctx, ext)
	case "external":
		err = m.registerExternalLocked(ctx, ext)
	default:
		err = fmt.Errorf(
			"unknown extension type: %s", ext.Type,
		)
	}

	if err != nil {
		m.status[ext.Name] = ExtensionInfo{
			Name:         ext.Name,
			Type:         ext.Type,
			Status:       StatusError,
			Error:        err.Error(),
			RegisteredAt: time.Now(),
		}
		return err
	}

	info := m.status[ext.Name]
	info.Status = StatusEnabled
	if ts, ok := m.toolSets[ext.Name]; ok && ts != nil {
		info.ToolCount = len(ts.Tools(ctx))
	}
	m.status[ext.Name] = info

	return nil
}

// registerBuiltinLocked registers a built-in extension.
func (m *Manager) registerBuiltinLocked(
	ctx context.Context, ext config.ExtensionConfig,
) error {
	ts, err := CreateBuiltinToolSet(ext.Name, m.cfg)
	if err != nil {
		return err
	}
	// Some builtins (apps, code_mode, top_of_mind) return nil
	// because they require runtime dependencies injected later.
	// These are registered as nil entries; session.go replaces
	// them with the real toolset during bootstrap.
	if ts != nil {
		if initable, ok := ts.(interface{ Init(context.Context) error }); ok {
			if err := initable.Init(ctx); err != nil {
				return fmt.Errorf(
					"init builtin extension %q: %w",
					ext.Name, err,
				)
			}
		}
	}
	m.toolSets[ext.Name] = ts
	return nil
}

// registerExternalLocked registers an external MCP server extension.
// It uses the MCPClient wrapper for improved observability and
// lifecycle management via trpc-mcp-go native APIs.
func (m *Manager) registerExternalLocked(
	ctx context.Context, ext config.ExtensionConfig,
) error {
	// Apply custom environment variables for the MCP server subprocess.
	restoreFn := applyEnvOverrides(ext.Env)
	defer restoreFn()

	mcpClient, err := NewMCPClient(ctx, ext)
	if err != nil {
		return fmt.Errorf(
			"create MCP client for %q: %w", ext.Name, err,
		)
	}

	m.toolSets[ext.Name] = mcpClient.ToolSet()
	return nil
}

// applyEnvOverrides temporarily sets environment variables for the MCP
// server subprocess. Returns a function that restores the original values.
func applyEnvOverrides(env map[string]string) func() {
	if len(env) == 0 {
		return func() {}
	}
	// Save original values
	originals := make(map[string]*string, len(env))
	for k, v := range env {
		if orig, ok := os.LookupEnv(k); ok {
			copy := orig
			originals[k] = &copy
		} else {
			originals[k] = nil
		}
		os.Setenv(k, v)
	}
	// Return restore function
	return func() {
		for k, orig := range originals {
			if orig == nil {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, *orig)
			}
		}
	}
}
