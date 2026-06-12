// Package cli provides the extension deeplink install command.
package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/extension"
	"github.com/km269/wukong/internal/provider"
	"github.com/km269/wukong/internal/security"
	"github.com/km269/wukong/internal/util"
)

func newExtensionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "extension",
		Short: "Manage MCP extensions",
		Long: `Manage MCP extensions for wukong. Supports installing extensions
from deeplink URLs, listing registered extensions, enabling/disabling
extensions, and removing extensions.

Deeplink format:
  wukong://extension?name=xxx&type=external&transport=stdio&command=npx&args=-y&args=@modelcontextprotocol/server-github
  https://wukong.ai/extension?name=...

Examples:
  # Install an extension from a deeplink URL
  wukong extension install "wukong://extension?name=github&type=external&transport=stdio&command=npx&args=-y&args=@modelcontextprotocol/server-github"

  # Install from a config file
  wukong extension install --config extensions.yaml

  # List all extensions
  wukong extension list

  # Enable/disable an extension
  wukong extension enable github
  wukong extension disable github

  # Show extension details
  wukong extension show github`,
	}

	cmd.AddCommand(newExtensionInstallCmd())
	cmd.AddCommand(newExtensionListCmd())
	cmd.AddCommand(newExtensionEnableCmd())
	cmd.AddCommand(newExtensionDisableCmd())
	cmd.AddCommand(newExtensionShowCmd())

	return cmd
}

// newExtensionInstallCmd creates the "extension install" subcommand.
func newExtensionInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install [deeplink-url]",
		Short: "Install an extension from a deeplink URL or config file",
		Long: `Install an MCP extension from a wukong://extension deeplink URL
or from a YAML configuration file.

The deeplink URL format supports:
  - stdio transport: requires command and args parameters
  - sse/streamable transport: requires url parameter
  - Environment variables: env.VAR_NAME=value
  - Permission control: allow=* or block=tool_name

If a config file is provided, it should contain a list of extension
configurations in YAML format.

Examples:
  wukong extension install "wukong://extension?name=github&type=external&transport=stdio&command=npx&args=-y&args=@modelcontextprotocol/server-github"
  wukong extension install --config extensions.yaml`,
		RunE: runExtensionInstall,
		Args: cobra.MaximumNArgs(1),
	}

	cmd.Flags().StringP("config", "c", "",
		"Path to extension config YAML file")
	cmd.Flags().Bool("skip-scan", false,
		"Skip malware scanning for the extension")
	cmd.Flags().Bool("dry-run", false,
		"Parse the deeplink but do not install")

	return cmd
}

func runExtensionInstall(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	skipScan, _ := cmd.Flags().GetBool("skip-scan")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	// Load configuration
	loader, err := config.NewLoader("")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	// Create extension manager
	extMgr := extension.NewManager(wukongCfg)
	if err := extMgr.Initialize(context.Background()); err != nil {
		return fmt.Errorf("init extension manager: %w", err)
	}
	defer extMgr.Close()

	ctx := context.Background()

	// Install from config file
	if configPath != "" {
		return installFromConfigFile(ctx, extMgr, configPath, skipScan)
	}

	// Install from deeplink URL
	if len(args) == 0 {
		return fmt.Errorf(
			"either provide a deeplink URL or use --config flag")
	}

	deeplinkURL := args[0]

	// Validate URL scheme
	if !strings.HasPrefix(deeplinkURL, "wukong://") &&
		!strings.HasPrefix(deeplinkURL, "https://") {
		return fmt.Errorf(
			"invalid deeplink URL: must start with wukong:// or https://")
	}

	// Parse the deeplink first for dry-run mode
	parsedExt, err := extension.ParseDeeplink(deeplinkURL)
	if err != nil {
		return fmt.Errorf("parse deeplink: %w", err)
	}

	if dryRun {
		fmt.Println("=== Deeplink Parse Result (dry-run) ===")
		fmt.Printf("Name:      %s\n", parsedExt.Name)
		fmt.Printf("Type:      %s\n", parsedExt.Type)
		fmt.Printf("Transport: %s\n", parsedExt.Transport)
		if parsedExt.Command != "" {
			fmt.Printf("Command:   %s\n", parsedExt.Command)
		}
		if len(parsedExt.Args) > 0 {
			fmt.Printf("Args:      %v\n", parsedExt.Args)
		}
		if parsedExt.URL != "" {
			fmt.Printf("URL:       %s\n", parsedExt.URL)
		}
		if len(parsedExt.Env) > 0 {
			fmt.Println("Environment:")
			for k, v := range parsedExt.Env {
				masked := v
				if len(v) > 8 {
					masked = v[:4] + "****"
				}
				fmt.Printf("  %s=%s\n", k, masked)
			}
		}
		fmt.Println("\nDry-run: extension was NOT installed.")
		return nil
	}

	// Security scan
	if !skipScan && parsedExt.Type == "external" {
		guard := security.NewGuard(&wukongCfg.Security)
		if err := guard.ScanExtension(
			parsedExt.Command, parsedExt.Args,
		); err != nil {
			return fmt.Errorf("security scan failed: %w", err)
		}
		fmt.Println("Security scan passed.")
	}

	// Prompt for confirmation
	fmt.Printf("\nAbout to install extension:\n")
	fmt.Printf("  Name:      %s\n", parsedExt.Name)
	fmt.Printf("  Type:      %s\n", parsedExt.Type)
	fmt.Printf("  Transport: %s\n", parsedExt.Transport)
	if parsedExt.Command != "" {
		fmt.Printf("  Command:   %s %s\n",
			parsedExt.Command, strings.Join(parsedExt.Args, " "))
	}
	fmt.Print("\nProceed with installation? [y/N]: ")

	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read input: %w", err)
	}
	response = strings.TrimSpace(strings.ToLower(response))
	if response != "y" && response != "yes" {
		fmt.Println("Installation cancelled.")
		return nil
	}

	// Register the extension
	if err := extMgr.RegisterFromDeeplink(ctx, deeplinkURL); err != nil {
		return fmt.Errorf("register extension: %w", err)
	}

	fmt.Printf("\nExtension %q installed successfully!\n", parsedExt.Name)
	fmt.Println("It will be available in your next session.")

	return nil
}

// installFromConfigFile installs extensions from a YAML config file.
func installFromConfigFile(
	ctx context.Context,
	extMgr *extension.Manager,
	configPath string,
	skipScan bool,
) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config file: %w", err)
	}

	extensions, err := extension.ParseExtensionsYAML(data)
	if err != nil {
		return fmt.Errorf("parse extensions config: %w", err)
	}

	if len(extensions) == 0 {
		fmt.Println("No extensions found in config file.")
		return nil
	}

	fmt.Printf("Found %d extension(s) in config file.\n\n", len(extensions))

	successCount := 0
	for _, ext := range extensions {
		fmt.Printf("Installing %s (%s)... ", ext.Name, ext.Type)

		// Skip security scan for external extensions when --skip-scan is set
		if ext.Type == "external" && !skipScan {
			fmt.Printf("SKIPPED: use --skip-scan to install external extensions\n")
			continue
		}

		// Attempt installation via the extension manager
		// Build a deeplink URL from the config for registration
		deeplinkURL := extension.ConfigToDeeplink(ext)
		if err := extMgr.RegisterFromDeeplink(ctx, deeplinkURL); err != nil {
			fmt.Printf("FAILED: %v\n", err)
			continue
		}

		successCount++
		fmt.Println("OK")
	}

	fmt.Printf("\n%d/%d extension(s) installed successfully.\n",
		successCount, len(extensions))
	return nil
}

// newExtensionListCmd creates the "extension list" subcommand.
func newExtensionListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all registered extensions",
		Long:  `List all registered MCP extensions with their status and details.`,
		RunE: runExtensionList,
	}
	return cmd
}

func runExtensionList(cmd *cobra.Command, args []string) error {
	loader, err := config.NewLoader("")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	extMgr := extension.NewManager(wukongCfg)
	if err := extMgr.Initialize(context.Background()); err != nil {
		return fmt.Errorf("init extension manager: %w", err)
	}
	defer extMgr.Close()

	extensions := extMgr.ListExtensions()
	if len(extensions) == 0 {
		fmt.Println("No extensions registered.")
		return nil
	}

	fmt.Printf("%-25s %-12s %-12s %-10s\n",
		"NAME", "TYPE", "STATUS", "TOOLS")
	fmt.Println(strings.Repeat("-", 65))
	for _, ext := range extensions {
		status := string(ext.Status)
		statusIcon := "●"
		switch ext.Status {
		case extension.StatusEnabled:
			statusIcon = "●"
		case extension.StatusDisabled:
			statusIcon = "○"
		case extension.StatusError:
			statusIcon = "✗"
		case extension.StatusLoading:
			statusIcon = "◌"
		}
		fmt.Printf("%-25s %-12s %s %-11s %-10d\n",
			ext.Name, ext.Type,
			statusIcon, status, ext.ToolCount,
		)
		if ext.Error != "" {
			fmt.Printf("  Error: %s\n", ext.Error)
		}
	}

	return nil
}

// newExtensionEnableCmd creates the "extension enable" subcommand.
func newExtensionEnableCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "enable <name>",
		Short: "Enable an extension",
		Long:  `Enable a previously disabled MCP extension by name.`,
		RunE:  runExtensionEnable,
		Args:  cobra.ExactArgs(1),
	}
	return cmd
}

func runExtensionEnable(cmd *cobra.Command, args []string) error {
	name := args[0]

	loader, err := config.NewLoader("")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	extMgr := extension.NewManager(wukongCfg)
	if err := extMgr.Initialize(context.Background()); err != nil {
		return fmt.Errorf("init extension manager: %w", err)
	}
	defer extMgr.Close()

	if err := extMgr.EnableExtension(context.Background(), name); err != nil {
		return fmt.Errorf("enable extension: %w", err)
	}

	fmt.Printf("Extension %q enabled.\n", name)
	return nil
}

// newExtensionDisableCmd creates the "extension disable" subcommand.
func newExtensionDisableCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disable <name>",
		Short: "Disable an extension",
		Long:  `Disable an active MCP extension by name.`,
		RunE:  runExtensionDisable,
		Args:  cobra.ExactArgs(1),
	}
	return cmd
}

func runExtensionDisable(cmd *cobra.Command, args []string) error {
	name := args[0]

	loader, err := config.NewLoader("")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	extMgr := extension.NewManager(wukongCfg)
	if err := extMgr.Initialize(context.Background()); err != nil {
		return fmt.Errorf("init extension manager: %w", err)
	}
	defer extMgr.Close()

	if err := extMgr.DisableExtension(name); err != nil {
		return fmt.Errorf("disable extension: %w", err)
	}

	fmt.Printf("Extension %q disabled.\n", name)
	return nil
}

// newExtensionShowCmd creates the "extension show" subcommand.
func newExtensionShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show extension details",
		Long:  `Show detailed information about a specific extension.`,
		RunE:  runExtensionShow,
		Args:  cobra.ExactArgs(1),
	}
	return cmd
}

func runExtensionShow(cmd *cobra.Command, args []string) error {
	name := args[0]

	loader, err := config.NewLoader("")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	extMgr := extension.NewManager(wukongCfg)
	if err := extMgr.Initialize(context.Background()); err != nil {
		return fmt.Errorf("init extension manager: %w", err)
	}
	defer extMgr.Close()

	info, ok := extMgr.GetStatus(name)
	if !ok {
		// Check config for uninitialized extensions
		extCfg := wukongCfg.FindExtension(name)
		if extCfg == nil {
			return fmt.Errorf("extension %q not found", name)
		}
		fmt.Printf("Name:      %s\n", extCfg.Name)
		fmt.Printf("Type:      %s\n", extCfg.Type)
		fmt.Printf("Status:    %s\n",
			map[bool]string{true: "enabled", false: "disabled"}[extCfg.Enabled])
		if extCfg.Transport != "" {
			fmt.Printf("Transport: %s\n", extCfg.Transport)
		}
		if extCfg.Command != "" {
			fmt.Printf("Command:   %s\n", extCfg.Command)
		}
		if len(extCfg.Args) > 0 {
			fmt.Printf("Args:      %v\n", extCfg.Args)
		}
		if extCfg.URL != "" {
			fmt.Printf("URL:       %s\n", extCfg.URL)
		}
		if extCfg.Deeplink != "" {
			fmt.Printf("Deeplink:  %s\n", extCfg.Deeplink)
		}
		return nil
	}

	fmt.Printf("Name:          %s\n", info.Name)
	fmt.Printf("Type:          %s\n", info.Type)
	fmt.Printf("Status:        %s\n", info.Status)
	fmt.Printf("Transport:     %s\n", info.Transport)
	fmt.Printf("Tool Count:    %d\n", info.ToolCount)
	fmt.Printf("Registered At: %s\n",
		info.RegisteredAt.Format("2006-01-02 15:04:05"))
	if info.Error != "" {
		fmt.Printf("Error:         %s\n", info.Error)
	}
	if len(info.Permissions) > 0 {
		fmt.Println("Permissions:")
		for _, p := range info.Permissions {
			allowed := "deny"
			if p.Allowed {
				allowed = "allow"
			}
			fmt.Printf("  %s: %s\n", p.Tool, allowed)
		}
	}

	return nil
}

// Ensure unused import warning is suppressed for provider package.
var _ = provider.NewFactory
var _ = util.Logger
