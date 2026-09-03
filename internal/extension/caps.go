// caps.go — capability bus registration (roadmap P0-1, Phase A).
//
// Bridges the extension manager's toolsets into the unified
// capability registry so every extension tool becomes addressable
// ("tools.<ext>.<tool>" / "mcp.<server>.<tool>") alongside the
// toolsets that bootstrapSession assembles at runtime.
package extension

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/km269/wukong/internal/capability"
	"github.com/km269/wukong/internal/extension/builtin"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Address prefixes for extension-sourced capabilities. The scheme is
// documented in docs/YAO_COMPARISON_AND_ROADMAP.md §6.3.
const (
	// AddrPrefixBuiltin namespaces built-in extension tools as
	// "tools.<extension>.<tool>".
	AddrPrefixBuiltin = "tools."

	// AddrPrefixMCP namespaces external MCP server tools as
	// "mcp.<server>.<tool>".
	AddrPrefixMCP = "mcp."

	// AddrMCPBroker is the address prefix of the MCP Broker meta
	// tools (mcp_list_servers, mcp_list_tools, mcp_inspect_tools,
	// mcp_call). The broker is a meta-server over many backends, so
	// it is not addressed as a server named "mcp_broker".
	AddrMCPBroker = "mcp.broker"

	// AddrExtensionMgr is the address prefix of the extension
	// management meta tools (NewManagerToolSet).
	AddrExtensionMgr = "tools.extension_manager"

	// Address prefixes of the toolsets that factory.go returns as
	// placeholders and bootstrapSession assembles at runtime
	// (agent_tools, apps, code_mode, top_of_mind).
	AddrTopOfMind  = "tools.top_of_mind"
	AddrCodeMode   = "tools.code_mode"
	AddrApps       = "tools.apps"
	AddrAgentTools = "tools.agent_tools"
)

// extensionAddressPrefix resolves the address prefix for a toolset
// held by the manager under extName with manager status extType.
func extensionAddressPrefix(extName, extType string) string {
	if extName == "mcp_broker" {
		return AddrMCPBroker
	}
	if extType == "builtin" {
		return AddrPrefixBuiltin + extName
	}
	return AddrPrefixMCP + extName
}

// RegisterOption customizes RegisterTools/RegisterToolSet.
type RegisterOption func(*registerOpts)

type registerOpts struct {
	scopeLookup func(toolName string) []string
}

// WithScopeLookup declares per-tool permission scopes consulted
// when building descriptors (e.g. builtin.ToolScopes for built-in
// extensions). Declared scopes feed the Guard descriptor seam;
// tools without declarations keep the conservative defaults.
func WithScopeLookup(fn func(toolName string) []string) RegisterOption {
	return func(o *registerOpts) { o.scopeLookup = fn }
}

// RegisterCapabilities walks every active toolset held by the
// manager and registers each tool in reg under its extension
// address. Built-in extensions contribute their declared scopes
// (builtin.ToolScopes) to the descriptors. Nil placeholders
// (agent_tools, apps, code_mode, top_of_mind — assembled in
// bootstrapSession) are skipped; register those via RegisterToolSet
// after construction.
//
// Duplicate addresses are warned and skipped instead of failing the
// whole registration (first registration wins). Returns the number
// of newly registered capabilities.
func (m *Manager) RegisterCapabilities(
	reg *capability.Registry, ctx context.Context,
) (int, error) {
	if reg == nil {
		return 0, errors.New("capability registry is nil")
	}

	m.mu.RLock()
	names := make([]string, 0, len(m.toolSets))
	for name := range m.toolSets {
		names = append(names, name)
	}
	m.mu.RUnlock()
	sort.Strings(names) // deterministic registration/log order

	registered := 0
	for _, name := range names {
		m.mu.RLock()
		ts := m.toolSets[name]
		info := m.status[name]
		m.mu.RUnlock()
		if ts == nil {
			continue
		}
		prefix := extensionAddressPrefix(name, info.Type)
		source := capability.SourceMCP
		opts := []RegisterOption{}
		if info.Type == "builtin" {
			source = capability.SourceBuiltin
			if scopes := builtin.ToolScopes(name); scopes != nil {
				opts = append(opts, WithScopeLookup(
					func(toolName string) []string {
						return scopes[toolName]
					},
				))
			}
		} else if extCfg := m.cfg.FindExtension(name); extCfg != nil &&
			len(extCfg.ToolScopes) > 0 {
			// External MCP tools: scopes come from the declarative
			// tool_scopes map in the extension config (Phase C).
			scopes := extCfg.ToolScopes
			opts = append(opts, WithScopeLookup(
				func(toolName string) []string {
					return scopes[toolName]
				},
			))
		}
		n, err := RegisterToolSet(reg, prefix, source, ts, ctx, opts...)
		if err != nil {
			return registered, fmt.Errorf(
				"register capabilities for %q: %w", name, err)
		}
		registered += n
	}
	return registered, nil
}

// RegisterToolSet registers every tool exposed by ts in reg under
// the given address prefix ("prefix" + "." + tool declaration name).
func RegisterToolSet(
	reg *capability.Registry,
	prefix, source string,
	ts tool.ToolSet,
	ctx context.Context,
	opts ...RegisterOption,
) (int, error) {
	if reg == nil {
		return 0, errors.New("capability registry is nil")
	}
	if ts == nil {
		return 0, errors.New("toolset is nil")
	}
	if prefix == "" || capability.Namespace(prefix) == prefix {
		return 0, fmt.Errorf(
			"address prefix %q lacks a namespace (<ns>.<name>)",
			prefix,
		)
	}
	return RegisterTools(reg, prefix, source, ts.Tools(ctx), opts...)
}

// RegisterTools registers a plain tool slice in reg under the given
// address prefix. Tools with a missing or nameless declaration are
// warned and skipped; an already-taken address is also warned and
// skipped (first registration wins).
func RegisterTools(
	reg *capability.Registry,
	prefix, source string,
	tools []tool.Tool,
	opts ...RegisterOption,
) (int, error) {
	if reg == nil {
		return 0, errors.New("capability registry is nil")
	}
	var o registerOpts
	for _, opt := range opts {
		opt(&o)
	}
	registered := 0
	for _, t := range tools {
		if t == nil {
			continue
		}
		decl := t.Declaration()
		if decl == nil || decl.Name == "" {
			util.Logger.Warn("capability: skipping unnamed tool",
				"prefix", prefix)
			continue
		}
		addr := prefix + "." + decl.Name
		meta := capability.ToolMeta{}
		if o.scopeLookup != nil {
			meta.Scopes = o.scopeLookup(decl.Name)
		}
		cap, err := capability.FromToolMeta(addr, source, t, meta)
		if err != nil {
			util.Logger.Warn("capability: adapter creation failed",
				"address", addr, "error", err.Error())
			continue
		}
		if err := reg.Register(cap); err != nil {
			util.Logger.Warn("capability: registration skipped",
				"address", addr, "error", err.Error())
			continue
		}
		registered++
	}
	return registered, nil
}
