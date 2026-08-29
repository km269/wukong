// Package cli provides the "wukong config validate" and
// "wukong config show" subcommands for configuration management.
package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/km269/wukong/internal/config"
)

// newConfigCmd creates the "wukong config" parent command with
// validate and show subcommands.
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage wukong configuration",
		Long: `Validate, view, and manage the wukong configuration.

Subcommands:
  validate  Check configuration validity and report issues
  show      Display the merged effective configuration`,
	}

	cmd.AddCommand(newConfigValidateCmd())
	cmd.AddCommand(newConfigShowCmd())

	return cmd
}

// ==========================================================================
// config validate
// ==========================================================================

func newConfigValidateCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate the wukong configuration file",
		Long: `Load and validate the wukong configuration file, reporting
any errors, warnings, or configuration issues.

Exits with code 0 on success, 1 on validation errors.

Examples:
  wukong config validate
  wukong config validate --config ./my-config.yaml`,
		RunE: runConfigValidate,
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file (default: auto-discover)")

	return cmd
}

func runConfigValidate(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")

	// Load configuration and run the full validation rules —
	// the same path as startup (bootstrapSession →
	// loader.LoadAndValidate), so todo/mcp_server/sandbox/port
	// conflict, planner and all other fatal checks in validate.go
	// apply here too.
	loader, err := config.NewLoader(configPath)
	if err != nil {
		fmt.Printf("✗ failed to create config loader: %v\n", err)
		return fmt.Errorf("config load: %w", err)
	}

	// The loader reports the file it actually read, so the
	// search-path priority lives in exactly one place.
	if used := loader.ConfigFileUsed(); used != "" {
		if _, err := os.Stat(used); err != nil {
			fmt.Printf("⚠ config file not found: %s\n", used)
		} else {
			fmt.Printf("📄 config file: %s\n", used)
		}
	}

	wukongCfg, err := loader.LoadAndValidate()
	if err != nil {
		fmt.Printf("✗ validation failed: %v\n", err)
		return err // already wrapped with "config validation:" by LoadAndValidate
	}

	// Surface non-fatal warnings the same way startup does
	// (these do not block, but indicate suboptimal or risky
	// configuration).
	warnings := wukongCfg.Warnings()
	if wukongCfg.DefaultProvider == "" {
		warnings = append(warnings,
			"default_provider is not set — "+
				"use --provider flag or set in config.yaml")
	}

	fmt.Println()

	if len(warnings) > 0 {
		fmt.Printf("⚠ configuration is valid with %d warning(s):\n", len(warnings))
		for i, w := range warnings {
			fmt.Printf("  %d. %s\n", i+1, w)
		}
		return nil
	}

	fmt.Println("✓ configuration is valid")
	return nil
}

// runFullValidation performs comprehensive config validation and
// returns a list of error messages. An empty list means the config
// is valid.
//
// Enum and range rules are delegated to the canonical
// config.WukongConfig.Validate() so this advisory path (used by
// bench/health) and the startup path can never drift apart. Only
// advisory checks that Validate() deliberately does not treat as
// fatal (missing model, missing API key, ACP agent_url,
// lightweight_provider fallback) are implemented here.
func runFullValidation(cfg *config.WukongConfig) []string {
	var issues []string

	// 1. Delegate enum/range/fatal rules to the canonical validator.
	// This always runs — a missing default_provider must not mask
	// other fatal problems (port conflicts, invalid enums, ...).
	if err := cfg.Validate(); err != nil {
		issues = append(issues, err.Error())
	}

	// 2. Default provider must be set (advisory here; startup can
	// still be launched with --provider).
	if cfg.DefaultProvider == "" {
		issues = append(issues,
			"default_provider is not set — "+
				"use --provider flag or set in config.yaml")
	}

	// 3. Default provider must have a model configured
	p := cfg.FindProvider(cfg.DefaultProvider)
	if p != nil && p.Model == "" {
		issues = append(issues,
			fmt.Sprintf("provider %q has no model configured", p.Name))
	}

	// 4. API key required for cloud providers
	if p != nil && p.APIKey == "" && p.Type != "ollama" && p.Type != "lmstudio" && p.Type != "vllm" {
		issues = append(issues,
			fmt.Sprintf("provider %q (type=%s) has no API key configured; "+
				"set %s.api_key or ${%s_API_KEY}",
				p.Name, p.Type, p.Name, strings.ToUpper(p.Name)))
	}

	// 5. ACP providers require an agent_url
	for _, prov := range cfg.Providers {
		if prov.Type == "acp" && prov.AgentURL == "" {
			issues = append(issues,
				fmt.Sprintf("ACP provider %q requires agent_url",
					prov.Name))
		}
	}

	// 6. Lightweight model fallback chain check
	if cfg.LightweightProvider != "" &&
		cfg.FindProvider(cfg.LightweightProvider) == nil {
		issues = append(issues,
			fmt.Sprintf("lightweight_provider %q not found in providers list; "+
				"background tasks will use default_provider",
				cfg.LightweightProvider))
	}

	return issues
}

// ==========================================================================
// config show
// ==========================================================================

func newConfigShowCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Display the merged effective configuration",
		Long: `Load and display the final merged configuration after
applying all sources (config files, environment variables,
and built-in defaults).

Examples:
  wukong config show
  wukong config show --config ./my-config.yaml`,
		RunE: runConfigShow,
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file (default: auto-discover)")

	return cmd
}

func runConfigShow(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")

	// Load configuration
	loader, err := config.NewLoader(configPath)
	if err != nil {
		return fmt.Errorf("create config loader: %w", err)
	}

	// The loader reports the file it actually read; empty means the
	// output below is pure built-in defaults + env overrides.
	if used := loader.ConfigFileUsed(); used != "" {
		if _, err := os.Stat(used); err == nil {
			fmt.Printf("# Config file: %s\n", used)
		}
	}

	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	// Marshal to YAML for display
	data, err := yaml.Marshal(wukongCfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	fmt.Println(string(data))
	return nil
}
