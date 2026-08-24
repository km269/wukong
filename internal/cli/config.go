// Package cli provides the "wukong config validate" and
// "wukong config show" subcommands for configuration management.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
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

	// Try to locate the config file
	resolvedPath := resolveConfigPath(configPath)
	if resolvedPath != "" {
		if _, err := os.Stat(resolvedPath); err != nil {
			fmt.Printf("⚠ config file not found: %s\n", resolvedPath)
		} else {
			fmt.Printf("📄 config file: %s\n", resolvedPath)
		}
	}

	// Load configuration and run the full validation rules —
	// the same path as startup (bootstrapSession →
	// loader.LoadAndValidate), so todo/mcp_server/sandbox/port
	// conflict and all other fatal checks in validate.go apply
	// here too.
	loader, err := config.NewLoader(configPath)
	if err != nil {
		fmt.Printf("✗ failed to create config loader: %v\n", err)
		return fmt.Errorf("config load: %w", err)
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
func runFullValidation(cfg *config.WukongConfig) []string {
	var issues []string

	// 1. Default provider must be set
	if cfg.DefaultProvider == "" {
		issues = append(issues,
			"default_provider is not set — "+
				"use --provider flag or set in config.yaml")
		return issues // Can't validate further without provider
	}

	// 2. Default provider must exist in providers list
	p := cfg.FindProvider(cfg.DefaultProvider)
	if p == nil {
		issues = append(issues,
			fmt.Sprintf("default_provider %q not found in providers list",
				cfg.DefaultProvider))
		return issues
	}

	// 3. Provider must have a model configured
	if p.Model == "" {
		issues = append(issues,
			fmt.Sprintf("provider %q has no model configured", p.Name))
	}

	// 4. API key required for cloud providers
	if p.APIKey == "" && p.Type != "ollama" && p.Type != "lmstudio" && p.Type != "vllm" {
		issues = append(issues,
			fmt.Sprintf("provider %q (type=%s) has no API key configured; "+
				"set %s.api_key or ${%s_API_KEY}",
				p.Name, p.Type, p.Name, strings.ToUpper(p.Name)))
	}

	// 5. Provider type must be valid
	validTypes := map[string]bool{
		"openai": true, "anthropic": true, "google": true,
		"deepseek": true, "ollama": true, "lmstudio": true, "vllm": true, "acp": true,
	}
	for _, prov := range cfg.Providers {
		if !validTypes[prov.Type] {
			issues = append(issues,
				fmt.Sprintf("provider %q has unknown type %q; "+
					"valid types: openai, anthropic, google, "+
					"deepseek, ollama, lmstudio, vllm, acp",
					prov.Name, prov.Type))
		}
		if prov.Type == "acp" && prov.AgentURL == "" {
			issues = append(issues,
				fmt.Sprintf("ACP provider %q requires agent_url",
					prov.Name))
		}
	}

	// 6. Planner validation
	if cfg.Agent.Planner != "" {
		validPlanners := map[string]bool{"builtin": true, "react": true}
		if !validPlanners[cfg.Agent.Planner] {
			issues = append(issues,
				fmt.Sprintf("unknown planner %q; "+
					"supported: builtin, react",
					cfg.Agent.Planner))
		}
	}

	// 7. Lightweight model fallback chain check
	if cfg.LightweightProvider != "" &&
		cfg.FindProvider(cfg.LightweightProvider) == nil {
		issues = append(issues,
			fmt.Sprintf("lightweight_provider %q not found in providers list; "+
				"background tasks will use default_provider",
				cfg.LightweightProvider))
	}

	// 8. Session backend validation
	validSessionBackends := map[string]bool{
		"sqlite": true, "memory": true, "redis": true,
	}
	if !validSessionBackends[cfg.Session.Backend] {
		issues = append(issues,
			fmt.Sprintf("unknown session backend %q; "+
				"supported: sqlite, memory, redis",
				cfg.Session.Backend))
	}

	// 9. Memory backend validation
	validMemoryBackends := map[string]bool{
		"sqlite": true, "redis": true,
	}
	if !validMemoryBackends[cfg.Memory.Backend] {
		issues = append(issues,
			fmt.Sprintf("unknown memory backend %q; "+
				"supported: sqlite, redis",
				cfg.Memory.Backend))
	}

	// 10. Security permission mode validation
	validPermModes := map[string]bool{
		"auto": true, "smart": true, "manual": true, "chat_only": true,
	}
	if !validPermModes[string(cfg.Security.PermissionMode)] {
		issues = append(issues,
			fmt.Sprintf("unknown permission_mode %q; "+
				"supported: auto, smart, manual, chat_only",
				cfg.Security.PermissionMode))
	}

	// 11. Workflow mode validation
	validWorkflowModes := map[string]bool{
		"single": true, "chain": true, "parallel": true,
		"cycle": true, "graph": true, "team_coordinator": true,
		"team_swarm": true, "claude_code": true, "codex": true,
		"dify": true,
	}
	if !validWorkflowModes[cfg.Workflow.Mode] {
		issues = append(issues,
			fmt.Sprintf("unknown workflow mode %q; "+
				"supported: single, chain, parallel, cycle, graph, "+
				"team_coordinator, team_swarm, claude_code, codex, dify",
				cfg.Workflow.Mode))
	}

	// 12. Artifact backend validation
	validArtifactBackends := map[string]bool{
		"inmemory": true, "cos": true,
	}
	if !validArtifactBackends[cfg.Artifact.Backend] {
		issues = append(issues,
			fmt.Sprintf("unknown artifact backend %q; "+
				"supported: inmemory, cos",
				cfg.Artifact.Backend))
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

	// Show config file location
	resolvedPath := resolveConfigPath(configPath)
	if resolvedPath != "" {
		if _, err := os.Stat(resolvedPath); err == nil {
			fmt.Printf("# Config file: %s\n", resolvedPath)
		}
	}

	// Load configuration
	loader, err := config.NewLoader(configPath)
	if err != nil {
		return fmt.Errorf("create config loader: %w", err)
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

// ==========================================================================
// Helpers
// ==========================================================================

// resolveConfigPath resolves the effective config file path from the
// given user-specified path or auto-discovery. Returns empty string
// if the config file cannot be determined.
func resolveConfigPath(userPath string) string {
	if userPath != "" {
		if info, err := os.Stat(userPath); err == nil {
			if info.IsDir() {
				return filepath.Join(userPath, "config.yaml")
			}
			return userPath
		}
		return userPath
	}

	// Auto-discovery: same priority as Viper
	candidates := []string{
		"config.yaml",
	}

	if homeDir, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(homeDir, ".config", "wukong", "config.yaml"))
	}

	candidates = append(candidates, "/etc/wukong/config.yaml")

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}

	return ""
}
