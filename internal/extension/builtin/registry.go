// Package builtin provides the registry for built-in MCP extensions.
package builtin

import "github.com/km269/wukong/internal/config"

// RegisterBuiltins registers all built-in extensions in the configuration.
// This ensures built-in extensions are available even if not explicitly
// listed in the YAML config.
func RegisterBuiltins(cfg *config.WukongConfig) {
	builtins := []config.ExtensionConfig{
		{
			Name:    "developer",
			Type:    "builtin",
			Enabled: true,
		},
	}

	existing := make(map[string]bool)
	for _, ext := range cfg.Extensions {
		existing[ext.Name] = true
	}

	for _, builtin := range builtins {
		if !existing[builtin.Name] {
			cfg.Extensions = append(cfg.Extensions, builtin)
		}
	}
}
