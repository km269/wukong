// constructors.go — self-registration table for built-in extension
// toolsets (roadmap P0-1 Phase B).
//
// Adding a built-in extension no longer requires touching the
// extension factory: register a constructor here (one map entry in
// this package, which owns the toolsets) and add the default
// enabled/disabled entry in registry.go. The extension.Manager
// resolves constructors through CreateBuiltinToolSet.
package builtin

import (
	"fmt"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/pkg/capability"
	"github.com/km269/wukong/pkg/sandbox"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// BuiltinConstructor builds a built-in extension toolset from the
// effective configuration.
type BuiltinConstructor func(cfg *config.WukongConfig) (tool.ToolSet, error)

// constructors maps extension name → constructor. Populated once at
// package init; read-only afterwards.
var constructors = map[string]BuiltinConstructor{}

// RegisterConstructor registers (or replaces) the constructor for a
// built-in extension name. Intended for package init.
func RegisterConstructor(name string, fn BuiltinConstructor) {
	constructors[name] = fn
}

func init() {
	registerConstructors()
}

// registerConstructors declares every built-in extension. Names must
// stay in sync with RegisterBuiltins (registry.go), which owns the
// default enabled/disabled configuration entries.
func registerConstructors() {
	ctor := func(name string, fn BuiltinConstructor) {
		RegisterConstructor(name, fn)
	}

	ctor("developer", func(cfg *config.WukongConfig) (tool.ToolSet, error) {
		// Wire security.sandbox (process-level resource caps and
		// lifecycle binding) into the developer toolset's shell
		// execution seam. With zero-value defaults (see defaults.go)
		// this is equivalent to the legacy NewSandboxShellService().
		sb := cfg.Security.Sandbox
		shell := capability.NewSandboxShellServiceWithLimits(
			sandbox.ResourceLimits{
				MaxCPUSeconds:  sb.Limits.MaxCPUSeconds,
				MaxMemoryBytes: sb.Limits.MaxMemoryBytes,
				MaxFileBytes:   sb.Limits.MaxFileBytes,
				MaxProcesses:   sb.Limits.MaxProcesses,
			},
			sb.KillOnParentExit,
		)
		return NewDeveloperToolSet(WithShellService(shell)), nil
	})
	ctor("computer_controller", func(cfg *config.WukongConfig) (tool.ToolSet, error) {
		return NewComputerControllerToolSet(cfg), nil
	})
	ctor("memory", func(cfg *config.WukongConfig) (tool.ToolSet, error) {
		return NewMemoryToolSet(cfg), nil
	})
	ctor("auto_visualiser", func(cfg *config.WukongConfig) (tool.ToolSet, error) {
		return NewVisualiserToolSet(cfg), nil
	})
	ctor("tutorial", func(cfg *config.WukongConfig) (tool.ToolSet, error) {
		return NewTutorialToolSet(cfg), nil
	})
	ctor("web", func(cfg *config.WukongConfig) (tool.ToolSet, error) {
		return NewWebToolSet(cfg), nil
	})
	ctor("ard", func(cfg *config.WukongConfig) (tool.ToolSet, error) {
		return NewARDToolSet(cfg.ARD.RegistryURL, cfg.ARD.CatalogPath)
	})
	ctor("cortex", func(cfg *config.WukongConfig) (tool.ToolSet, error) {
		return NewCortexToolSet(cfg), nil
	})

	// Placeholder constructors: these toolsets need runtime
	// dependencies (executor, manager) injected during
	// bootstrapSession. The manager stores the nil entry; session.go
	// replaces it with the real toolset afterwards.
	for _, name := range []string{"agent_tools", "apps", "code_mode", "top_of_mind"} {
		ctor(name, func(*config.WukongConfig) (tool.ToolSet, error) {
			return nil, nil
		})
	}
}

// CreateBuiltinToolSet resolves the constructor registered for name
// and builds the toolset.
func CreateBuiltinToolSet(
	name string, cfg *config.WukongConfig,
) (tool.ToolSet, error) {
	fn, ok := constructors[name]
	if !ok {
		return nil, fmt.Errorf("unknown builtin extension: %s", name)
	}
	return fn(cfg)
}
