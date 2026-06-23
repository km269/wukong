// Package extension provides built-in extension factory.
package extension

import (
	"fmt"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/extension/builtin"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// CreateBuiltinToolSet creates the appropriate built-in tool set
// based on the extension name.
// Note: apps, code_mode, and top_of_mind require runtime dependencies
// (executor, manager) that are injected in bootstrapSession. For these,
// the factory returns a placeholder; the real toolset is created in
// session.go and replaces it during bootstrap.
func CreateBuiltinToolSet(
	name string, cfg *config.WukongConfig,
) (tool.ToolSet, error) {
	switch name {
	case "developer":
		return builtin.NewDeveloperToolSet(), nil
	case "computer_controller":
		return builtin.NewComputerControllerToolSet(cfg), nil
	case "memory":
		return builtin.NewMemoryToolSet(cfg), nil
	case "auto_visualiser":
		return builtin.NewVisualiserToolSet(cfg), nil
	case "tutorial":
		return builtin.NewTutorialToolSet(cfg), nil
	case "web":
		return builtin.NewWebToolSet(), nil
	case "agent_tools", "apps", "code_mode", "top_of_mind":
		// These are created in bootstrapSession with their
		// runtime dependencies. The manager will hold a nil
		// entry; session.go appends the real toolset.
		return nil, nil
	case "ard":
		return builtin.NewARDToolSet(
			cfg.ARD.RegistryURL,
			cfg.ARD.CatalogPath,
		)
	case "cortex":
		return builtin.NewCortexToolSet(cfg), nil
	default:
		return nil, fmt.Errorf(
			"unknown builtin extension: %s", name,
		)
	}
}
