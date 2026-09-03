// toolset.go — exposes the registry as a trpc-agent-go ToolSet so
// the agent framework can consume the capability bus exactly like
// the hand-assembled toolsets it replaces (roadmap P0-1 Phase B).
package capability

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// registryToolSet adapts a Registry to tool.ToolSet.
type registryToolSet struct {
	reg *Registry
}

// RegistryToolSet wraps reg as a tool.ToolSet whose Tools() is
// AsTools(): every registered capability, sorted by address for a
// deterministic LLM tool manifest. Each returned tool implements
// tool.CallableTool.
func RegistryToolSet(reg *Registry) tool.ToolSet {
	return registryToolSet{reg: reg}
}

// Tools implements tool.ToolSet.
func (s registryToolSet) Tools(_ context.Context) []tool.Tool {
	return s.reg.AsTools()
}

// Close implements tool.ToolSet. The registry holds no resources of
// its own (ownership stays with the registered toolsets).
func (s registryToolSet) Close() error { return nil }

// Name implements tool.ToolSet.
func (s registryToolSet) Name() string { return "capability_registry" }
