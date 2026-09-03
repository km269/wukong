// Package capability provides the unified capability bus: a single
// addressable registry of everything the agent can invoke — built-in
// extension tools, external MCP tools, recipe sub-agents, and engine
// meta-operations.
//
// Every capability carries a stable, globally unique address of the
// form "<namespace>.<name>[.<tool>]", for example:
//
//	tools.web.aggregate_search    built-in extension tool
//	mcp.github.create_issue       external MCP server tool
//	mcp.broker.mcp_call           MCP Broker meta tool
//	tools.extension_manager.list  extension management meta tool
//	recipe.translator             recipe sub-agent (Phase B)
//	todo.write                    engine function tool (Phase B)
//
// The registry is the integration point for Guard policy (via the
// Descriptor scopes), the CLI (wukong caps), protocol endpoints, and
// the declarative flow DSL (roadmap P0-2).
//
// Phase A (read-only introduction): the registry mirrors what the
// extension manager loads; CoreLoop keeps its own tool aggregation
// and runtime behaviour is unchanged. See
// docs/YAO_COMPARISON_AND_ROADMAP.md §6 for the design.
package capability

import (
	"context"
	"encoding/json"
	"strings"
)

// Source classifies where a capability originates. The zero value ""
// means unspecified.
const (
	SourceBuiltin = "builtin" // built-in extension toolset
	SourceMCP     = "mcp"     // external MCP server tool (incl. broker)
	SourceManager = "manager" // extension-management meta tools
	SourceRecipe  = "recipe"  // YAML recipe sub-agent
	SourceFlow    = "flow"    // declarative YAML flow (P0-2)
	SourceScript  = "script"  // user script hook/tool (P1-4)
)

// Descriptor is the declarative metadata of a capability. It is
// consumed by the LLM tool manifest (Name/Description/Parameters),
// by `wukong caps list` (all fields), and later by the Guard policy
// engine (Scopes/Mutating).
type Descriptor struct {
	// Address is the globally unique bus address, e.g.
	// "tools.web.aggregate_search".
	Address string `json:"address"`

	// Name is the LLM-visible tool name. It intentionally stays
	// identical to the pre-bus tool name so tool-calling behaviour
	// and prompt caches are unaffected.
	Name string `json:"name"`

	// Description explains the capability to the LLM.
	Description string `json:"description"`

	// Parameters is the JSON Schema of the invocation arguments
	// (a marshalled tool.Schema). Empty when the tool declares
	// none.
	Parameters json.RawMessage `json:"parameters,omitempty"`

	// Scopes declares the permission domains the capability needs,
	// e.g. "shell", "network", "fs.write", "memory". Empty means
	// undeclared; consumers must treat that conservatively.
	Scopes []string `json:"scopes,omitempty"`

	// Mutating reports whether invoking the capability can produce
	// side effects. FromTool defaults this to true (ask rather
	// than assume) until per-tool declarations exist.
	Mutating bool `json:"mutating"`

	// Source classifies the origin (SourceBuiltin, SourceMCP, ...).
	Source string `json:"source"`
}

// InvokeFunc executes a capability. args is the raw JSON object the
// caller (LLM tool call, CLI, protocol endpoint) provided; the
// result is raw JSON, normalized by the adapter layer.
type InvokeFunc func(
	ctx context.Context, args json.RawMessage,
) (json.RawMessage, error)

// Capability is the smallest unit of the capability bus: a stable
// address, declarative metadata, and a uniform invoke entry point.
type Capability interface {
	// Address returns the globally unique bus address.
	Address() string

	// Descriptor returns the capability metadata. Implementations
	// should not mutate the returned struct afterwards.
	Descriptor() Descriptor

	// Invoke executes the capability with JSON arguments and
	// returns a JSON result.
	Invoke(
		ctx context.Context, args json.RawMessage,
	) (json.RawMessage, error)
}

// Namespace returns the first dot-separated segment of an address,
// e.g. "tools" for "tools.web.aggregate_search". An address without
// a dot is returned unchanged.
func Namespace(addr string) string {
	if i := strings.IndexByte(addr, '.'); i >= 0 {
		return addr[:i]
	}
	return addr
}
