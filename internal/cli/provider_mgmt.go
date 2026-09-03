// Package cli provides the "wukong provider" command for LLM
// provider listing and connectivity testing.
package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/provider"
)

// newProviderCmd creates the "wukong provider" command group.
func newProviderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provider",
		Short: "List and test AI model providers",
		Long: `Manage and verify AI model provider configurations.

Subcommands:
  list    List all configured providers
  test    Test connectivity to a specific provider`,
	}

	cmd.AddCommand(newProviderListCmd())
	cmd.AddCommand(newProviderTestCmd())

	return cmd
}

// ==========================================================================
// provider list
// ==========================================================================

func newProviderListCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all configured providers",
		Long: `Display all configured AI model providers with their
type, base URL, and default model.

Examples:
  wukong provider list
  wukong provider ls --config ./my-config.yaml`,
		RunE: runProviderList,
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file")

	return cmd
}

func runProviderList(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")

	loader, err := config.NewLoader(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	if len(wukongCfg.Providers) == 0 {
		fmt.Println("No providers configured.")
		fmt.Println("Run 'wukong configure' to set up providers.")
		return nil
	}

	fmt.Printf("Configured providers (default: %s):\n\n",
		wukongCfg.DefaultProvider)

	fmt.Printf("  %-20s %-12s %-30s %s\n",
		"NAME", "TYPE", "BASE URL", "MODEL")
	fmt.Println("  " + strings.Repeat("-", 76))

	for _, p := range wukongCfg.Providers {
		isDefault := ""
		if p.Name == wukongCfg.DefaultProvider {
			isDefault = " ★"
		}
		baseURL := p.BaseURL
		if baseURL == "" {
			baseURL = resolveDefaultBaseURL(p.Type)
		}
		if len(baseURL) > 28 {
			baseURL = baseURL[:25] + "..."
		}

		apiKeyStatus := "✓"
		if p.APIKey == "" && p.Type != "ollama" && p.Type != "lmstudio" {
			apiKeyStatus = "✗"
		}

		fmt.Printf("  %-20s %-12s %-30s %s%s [key:%s]\n",
			p.Name, p.Type, baseURL, p.Model, isDefault, apiKeyStatus)
	}

	if wukongCfg.LightweightProvider != "" {
		fmt.Printf("\nLightweight model: %s / %s\n",
			wukongCfg.LightweightProvider,
			wukongCfg.LightweightModel)
	}

	return nil
}

func resolveDefaultBaseURL(providerType string) string {
	switch providerType {
	case "openai":
		return provider.OpenAIBaseURL
	case "anthropic":
		return provider.AnthropicBaseURL
	case "google":
		return provider.GoogleBaseURL
	case "deepseek":
		return provider.DeepSeekBaseURL
	case "ollama":
		return provider.OllamaBaseURL
	case "lmstudio":
		return provider.LMStudioBaseURL
	case "vllm":
		return provider.VLLMBaseURL
	default:
		return "(not set)"
	}
}

// ==========================================================================
// provider test
// ==========================================================================

func newProviderTestCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "test <provider-name>",
		Short: "Test connectivity to a provider",
		Long: `Test the connection to a specific AI model provider by
sending a simple health-check request.

Examples:
  wukong provider test openai
  wukong provider test deepseek`,
		RunE: runProviderTest,
		Args: cobra.ExactArgs(1),
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file")

	return cmd
}

func runProviderTest(cmd *cobra.Command, args []string) error {
	providerName := args[0]
	configPath, _ := cmd.Flags().GetString("config")

	loader, err := config.NewLoader(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	p := wukongCfg.FindProvider(providerName)
	if p == nil {
		return fmt.Errorf("provider %q not found in configuration. "+
			"Available: %s",
			providerName, strings.Join(listProviderNames(wukongCfg), ", "))
	}

	fmt.Printf("Testing provider: %s (type: %s)\n", p.Name, p.Type)

	// Resolve base URL
	baseURL := p.BaseURL
	if baseURL == "" {
		baseURL = resolveDefaultBaseURL(p.Type)
	}
	fmt.Printf("  Base URL: %s\n", baseURL)
	fmt.Printf("  Model:    %s\n", p.Model)

	// API key check
	if p.APIKey == "" && p.Type != "ollama" && p.Type != "lmstudio" {
		fmt.Printf("  ⚠ No API key configured for provider %q\n", p.Name)
		fmt.Println("    Set it in config.yaml or via environment variable.")
		fmt.Println("    Example: export " +
			strings.ToUpper(p.Name) + "_API_KEY=sk-...")
	}

	// Try to create a model instance
	factory := provider.NewFactory(wukongCfg)
	model, err := factory.CreateModel(p.Name)
	if err != nil {
		fmt.Printf("  ✗ Failed to create model: %v\n", err)
		return fmt.Errorf("provider test failed: %w", err)
	}

	fmt.Printf("  ✓ Model instance created successfully\n")
	fmt.Printf("  Model interface: %T\n", model)
	fmt.Println("\nProvider configuration looks valid.")

	return nil
}

func listProviderNames(cfg *config.WukongConfig) []string {
	names := make([]string, len(cfg.Providers))
	for i, p := range cfg.Providers {
		names[i] = p.Name
	}
	return names
}
