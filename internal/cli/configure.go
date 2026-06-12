package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

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

Examples:
  wukong configure
  wukong configure --output ~/.config/wukong/config.yaml`,
		RunE: runConfigure,
	}

	cmd.Flags().StringP("output", "o", "",
		"Output config file path (default: ~/.config/wukong/config.yaml)")

	return cmd
}

func runConfigure(cmd *cobra.Command, args []string) error {
	outputPath, _ := cmd.Flags().GetString("output")

	fmt.Println("=== Wukong Configuration Wizard ===")
	fmt.Println()

	cfg := defaultConfig()

	reader := bufio.NewReader(os.Stdin)

	// Step 1: Default provider
	fmt.Println("Step 1: Default Model Provider")
	fmt.Print("Enter provider name [lmstudio]: ")
	providerName := readLine(reader, "lmstudio")
	cfg.DefaultProvider = providerName

	// Step 2: Provider configuration
	fmt.Println("\nStep 2: Configure Provider")
	fmt.Println("You can add multiple providers. Press Enter on name to finish.")

	for {
		fmt.Print("\nProvider name (or Enter to finish): ")
		name := readLine(reader, "")
		if name == "" {
			break
		}

		fmt.Print("Provider type [openai]: ")
		pType := readLine(reader, "openai")

		fmt.Print("Base URL: ")
		baseURL := readLine(reader, "")

		fmt.Print("API Key (or env var like ${OPENAI_API_KEY}): ")
		apiKey := readLine(reader, "")

		fmt.Print("Model name: ")
		modelName := readLine(reader, "")

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

	for _, ext := range builtinExts {
		fmt.Printf("Enable %s (%s)? [Y/n]: ", ext.name, ext.desc)
		answer := readLine(reader, "y")
		enabled := strings.ToLower(answer) != "n"
		cfg.Extensions = append(cfg.Extensions, config.ExtensionConfig{
			Name:    ext.name,
			Type:    "builtin",
			Enabled: enabled,
		})
	}

	// Step 4: Agent settings
	fmt.Println("\nStep 4: Agent Settings")
	fmt.Print("Max LLM calls per run [50]: ")
	maxCallsStr := readLine(reader, "50")
	maxCalls, err := strconv.Atoi(maxCallsStr)
	if err != nil || maxCalls <= 0 {
		maxCalls = 50
	}
	cfg.Agent.MaxLLMCalls = maxCalls

	fmt.Print("Enable parallel tool execution? [Y/n]: ")
	parallel := readLine(reader, "y")
	cfg.Agent.ParallelTools = strings.ToLower(parallel) != "n"

	fmt.Print("Enable streaming output? [Y/n]: ")
	streaming := readLine(reader, "y")
	cfg.Agent.Streaming = strings.ToLower(streaming) != "n"

	// Step 5: Security
	fmt.Println("\nStep 5: Security")
	fmt.Print("Block dangerous commands (rm -rf / etc)? [Y/n]: ")
	blockDangerous := readLine(reader, "y")
	cfg.Security.BlockDangerousCommands = strings.ToLower(blockDangerous) != "n"

	fmt.Print("Require user approval for destructive operations? [y/N]: ")
	requireApproval := readLine(reader, "n")
	cfg.Security.RequireApproval = strings.ToLower(requireApproval) == "y"

	// Determine output path
	if outputPath == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			outputPath = "config.yaml"
		} else {
			configDir := filepath.Join(homeDir, ".config", "wukong")
			if err := os.MkdirAll(configDir, 0755); err != nil {
				return fmt.Errorf("create config dir: %w", err)
			}
			outputPath = filepath.Join(configDir, "config.yaml")
		}
	}

	// Write config
	data, err := yaml.Marshal(cfg)
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

// defaultConfig returns a configuration with sensible defaults.
func defaultConfig() *config.WukongConfig {
	return &config.WukongConfig{
		DefaultProvider: "lmstudio",
		Session: config.SessionConfig{
			Backend:        "sqlite",
			DBPath:         "wukong.db",
			EventLimit:     500,
			TTL:            0, // unlimited
			EnableSummary:  true,
			SummaryTrigger: 50,
		},
		Memory: config.MemoryConfig{
			Backend:     "sqlite",
			DBPath:      "wukong.db",
			MaxMemories: 100,
			AutoExtract: true,
		},
		Todo: config.TodoConfig{
			Backend: "sqlite",
			DBPath:  "wukong.db",
		},
		Agent: config.AgentConfig{
			MaxLLMCalls:           50,
			MaxToolIterations:     30,
			ParallelTools:         true,
			Streaming:             true,
			MaxRunDuration:        300 * 1000000000, // 300s in ns
			Temperature:           0.7,
			MaxTokens:             4096,
			ToolRetryEnabled:      true,
			ToolRetryMaxAttempts:  3,
			ToolRetryInitialWait:  1 * 1000000000, // 1s in ns
			ToolRetryBackoffFactor: 2.0,
			EnablePostToolPrompt:  true,
		},
		Security: config.SecurityConfig{
			MalwareScanEnabled:     true,
			BlockDangerousCommands: true,
			DefaultTimeout:         30 * 1000000000, // 30s in ns
			MaxTimeout:             300 * 1000000000, // 300s in ns
			BlockedCommands: []string{
				"rm -rf /", "dd if=/dev/zero",
				"mkfs.", "> /dev/sda", "fork bomb",
			},
		},
		Revision: config.RevisionConfig{
			Enabled:              true,
			RevisionProvider:     "",
			RevisionModel:        "",
			MaxCommandOutput:     8000,
			EnableSemanticSearch: false,
			SearchStrategy:       "include_all",
			MaxContextTokens:     64000,
			TrimRatio:            0.3,
		},
		Browser: config.BrowserConfig{
			Enabled:         true,
			BrowserType:     "chromium",
			Headless:        true,
			CacheDir:        ".wukong_cache",
			MaxDownloadSize: 104857600, // 100MB
			Timeout:         60 * 1000000000, // 60s in ns
		},
		Recall: config.RecallConfig{
			Enabled:              true,
			Backend:              "sqlite",
			DBPath:               "wukong.db",
			MaxResults:           10,
			MaxMessagesPerSession: 200,
		},
		Visualiser: config.VisualiserConfig{
			Enabled:   true,
			OutputDir: ".wukong_visuals",
			MaxWidth:  1200,
			MaxHeight: 800,
		},
		Tutorial: config.TutorialConfig{
			Enabled:  true,
			Language: "zh",
		},
		TopOfMind: config.TopOfMindConfig{
			Enabled:         true,
			InstructionFile: ".wukong_instructions.md",
			MaxLength:       2000,
		},
		CodeMode: config.CodeModeConfig{
			Enabled:     true,
			Timeout:     10 * 1000000000, // 10s default
			MaxMemoryMB: 128,
		},
		Apps: config.AppsConfig{
			Enabled: true,
			AppDir:  ".wukong_apps",
		},
		Summon: config.SummonConfig{
			Enabled:       true,
			SkillsDir:     ".wukong_skills",
			MaxConcurrent: 5,
		},
	}
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
