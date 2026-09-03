// Package extension provides built-in extension factory.
package extension

import (
	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/extension/builtin"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// CreateBuiltinToolSet creates the appropriate built-in tool set
// based on the extension name. Construction is owned by the builtin
// package's self-registration table (builtin/constructors.go, Phase
// B of the capability roadmap): adding a built-in extension no
// longer requires changes here.
//
// Note: apps, code_mode, and top_of_mind require runtime dependencies
// (executor, manager) that are injected in bootstrapSession. For these,
// the factory returns a placeholder; the real toolset is created in
// session.go and replaces it during bootstrap.
func CreateBuiltinToolSet(
	name string, cfg *config.WukongConfig,
) (tool.ToolSet, error) {
	return builtin.CreateBuiltinToolSet(name, cfg)
}
