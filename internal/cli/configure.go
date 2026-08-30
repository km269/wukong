package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/km269/wukong/internal/config"
)

func newConfigureCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "configure",
		Short: "Configure wukong settings interactively",
		Long: `Configure model providers, extensions, and other settings.
This command opens an interactive configuration wizard that helps you
set up your LLM providers, enable/disable extensions, and customize
agent behavior.

If a config file already exists at the target path, its values are
used as the starting point and pressing Enter on any prompt keeps
the current value — re-running the wizard edits in place instead of
rebuilding from scratch. Use --edit to force this incremental mode
(also the default when an existing file is present).

Examples:
  wukong configure
  wukong configure --edit
  wukong configure --output ~/.config/wukong/config.yaml`,
		RunE: runConfigure,
	}

	cmd.Flags().StringP("output", "o", "",
		"Output config file path (default: ~/.config/wukong/config.yaml)")
	cmd.Flags().Bool("edit", false,
		"Edit the existing config incrementally (default when the file exists)")

	return cmd
}

func runConfigure(cmd *cobra.Command, args []string) error {
	outputPath, _ := cmd.Flags().GetString("output")
	editMode, _ := cmd.Flags().GetBool("edit")

	// Resolve the target path up front so baseConfig can check
	// existence even when --output is used with --edit.
	if outputPath == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot resolve home dir: %w", err)
		}
		configDir := filepath.Join(homeDir, ".config", "wukong")
		if err := os.MkdirAll(configDir, 0755); err != nil {
			return fmt.Errorf("create config dir: %w", err)
		}
		outputPath = filepath.Join(configDir, "config.yaml")
	}

	fmt.Println("=== Wukong Configuration Wizard ===")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)

	// Baseline: --edit or an existing file loads the on-disk config
	// (unexpanded, so ${ENV} secrets round-trip untouched); otherwise
	// start from the single source of truth for built-in defaults.
	cfg, existing, err := baseConfig(outputPath, editMode)
	if err != nil {
		return err
	}
	if existing {
		fmt.Println("Loaded existing config — Enter keeps the current value.")
		fmt.Println()
	} else {
		cfg.DefaultProvider = ""
		cfg.Providers = nil
		cfg.Extensions = nil
	}

	// Step 1: Default provider
	fmt.Println("Step 1: Default Model Provider")
	cur := cfg.DefaultProvider
	if cur == "" {
		cur = "lmstudio"
	}
	cfg.DefaultProvider = promptValue(reader, "Enter provider name", cur)

	// Step 2: Provider configuration
	fmt.Println("\nStep 2: Configure Provider")
	fmt.Println("You can add multiple providers. Press Enter on name to finish.")

	// Edit mode: walk existing providers so their fields are kept
	// (Enter keeps each field unchanged).
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		fmt.Printf("\nProvider %d: %s (type %s)\n", i+1, p.Name, orDefault(p.Type, "openai"))
		if p.Type != "" {
			p.Type = promptValue(reader, "  Type", p.Type)
		} else {
			p.Type = "openai"
		}
		p.BaseURL = promptValue(reader, "  Base URL", p.BaseURL)
		p.APIKey = promptValue(reader, "  API Key (or ${ENV_VAR})", p.APIKey)
		p.Model = promptValue(reader, "  Model", p.Model)
	}

	for {
		fmt.Print("\nAdd provider name (or Enter to finish): ")
		name := readLine(reader, "")
		if name == "" {
			break
		}
		if cfg.FindProvider(name) != nil {
			fmt.Printf("Provider %q already exists — skipped.\n", name)
			continue
		}

		pType := promptValue(reader, "Provider type", "openai")
		baseURL := promptValue(reader, "Base URL", "")
		apiKey := promptValue(reader, "API Key (or ${ENV_VAR})", "")
		modelName := promptValue(reader, "Model name", "")

		cfg.Providers = append(cfg.Providers, config.ProviderConfig{
			Name:    name,
			Type:    pType,
			BaseURL: baseURL,
			APIKey:  apiKey,
			Model:   modelName,
		})
	}

	// Step 3: Extensions
	fmt.Println("\nStep 3: Extensions")
	fmt.Println("Enable or disable built-in extensions:")

	builtinExts := []struct {
		name string
		desc string
	}{
		{"developer", "File operations, commands, code search"},
		{"computer_controller", "Web fetch, browser automation"},
		{"memory", "User preference and knowledge storage"},
		{"auto_visualiser", "Charts, diagrams, tables"},
		{"tutorial", "Interactive tutorials"},
		{"top_of_mind", "Persistent instruction injection"},
		{"code_mode", "JavaScript code execution sandbox"},
		{"apps", "Custom HTML app management"},
	}

	// Edit mode: preserve extensions not in the builtin list so the
	// wizard never silently deletes externally managed entries.
	kept := make([]config.ExtensionConfig, 0, len(cfg.Extensions))
	for _, ext := range cfg.Extensions {
		if ext.Type == "builtin" && isBuiltinExt(builtinExts, ext.Name) {
			continue // handled by the loop below
		}
		kept = append(kept, ext)
	}

	for _, ext := range builtinExts {
		enabled := true
		for _, e := range cfg.Extensions {
			if e.Name == ext.name {
				enabled = e.Enabled
				break
			}
		}
		fmt.Printf("Enable %s (%s)? [Y/n] (current %s): ",
			ext.name, ext.desc, yn(enabled))
		ans := readLine(reader, yn(enabled))
		enabled = strings.ToLower(ans) != "n"
		kept = append(kept, config.ExtensionConfig{
			Name:    ext.name,
			Type:    "builtin",
			Enabled: enabled,
		})
	}
	cfg.Extensions = kept

	// Step 4: Agent settings
	fmt.Println("\nStep 4: Agent Settings")
	cur = strconv.Itoa(cfg.Agent.MaxLLMCalls)
	if cur == "0" {
		cur = "50"
	}
	maxCallsStr := promptValue(reader, "Max LLM calls per run", cur)
	maxCalls, err := strconv.Atoi(maxCallsStr)
	if err != nil || maxCalls <= 0 {
		maxCalls, _ = strconv.Atoi(cur)
	}
	cfg.Agent.MaxLLMCalls = maxCalls

	ans := promptValue(reader, "Enable parallel tool execution", yn(cfg.Agent.ParallelTools))
	cfg.Agent.ParallelTools = strings.ToLower(ans) != "n"

	ans = promptValue(reader, "Enable streaming output", yn(cfg.Agent.Streaming))
	cfg.Agent.Streaming = strings.ToLower(ans) != "n"

	// Step 5: Security
	fmt.Println("\nStep 5: Security")
	ans = promptValue(reader, "Block dangerous commands (rm -rf / etc)", yn(cfg.Security.BlockDangerousCommands))
	cfg.Security.BlockDangerousCommands = strings.ToLower(ans) != "n"

	ans = promptValue(reader, "Require approval for destructive operations", yn(cfg.Security.RequireApproval))
	cfg.Security.RequireApproval = strings.ToLower(ans) == "y"

	// Write config using the snake_case keys the loader reads back.
	data, err := config.MarshalYAML(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	fmt.Printf("\nConfiguration saved to: %s\n", outputPath)
	fmt.Println("Run 'wukong session' to start using wukong!")

	return nil
}

// baseConfig returns the wizard starting point: the on-disk config
// loaded unexpanded when it exists (or --edit is set), otherwise
// built-in defaults. The bool reports whether an existing file was
// loaded.
func baseConfig(path string, edit bool) (*config.WukongConfig, bool, error) {
	info, err := os.Stat(path)
	exists := err == nil && !info.IsDir()

	if edit && !exists {
		// --edit without a file: fall back to defaults.
		return config.Defaults(), false, nil
	}
	if !exists {
		return config.Defaults(), false, nil
	}

	loader, err := config.NewLoader(path)
	if err != nil {
		return nil, false, fmt.Errorf("load existing config: %w", err)
	}
	cfg, err := loader.LoadUnresolved()
	if err != nil {
		return nil, false, fmt.Errorf("parse existing config: %w", err)
	}
	return cfg, true, nil
}

// promptValue prints "label [current]: " and returns the entered
// value, keeping the current value when the user presses Enter.
func promptValue(reader *bufio.Reader, label, current string) string {
	fmt.Printf("%s [%s]: ", label, current)
	return readLine(reader, current)
}

// orDefault returns def when v is empty.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// yn renders a bool as the "y"/"n" prompt default.
func yn(v bool) string {
	if v {
		return "y"
	}
	return "n"
}

// isBuiltinExt reports whether name is in the builtin list.
func isBuiltinExt(exts []struct{ name, desc string }, name string) bool {
	for _, e := range exts {
		if e.name == name {
			return true
		}
	}
	return false
}

// readLine reads a line from the reader, returning a default if empty.
func readLine(reader *bufio.Reader, defaultVal string) string {
	line, err := reader.ReadString('\n')
	if err != nil {
		return defaultVal
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultVal
	}
	return line
}
