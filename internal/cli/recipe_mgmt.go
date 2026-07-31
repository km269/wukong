// Package cli provides the "wukong recipe" command for recipe
// definition management.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/km269/wukong/internal/agent"
	"github.com/km269/wukong/internal/config"
)

// newRecipeCmd creates the "wukong recipe" command group.
func newRecipeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recipe",
		Short: "Manage agent recipe definitions",
		Long: `View and manage agent recipe definitions stored in the
recipes directory. Recipes are YAML-defined sub-agent
configurations that can be called as tools.

Subcommands:
  list      List all available recipes
  show      Display a recipe's full configuration
  validate  Validate recipe YAML files`,
	}

	cmd.AddCommand(newRecipeListCmd())
	cmd.AddCommand(newRecipeShowCmd())
	cmd.AddCommand(newRecipeValidateCmd())

	return cmd
}

// ==========================================================================
// recipe list
// ==========================================================================

func newRecipeListCmd() *cobra.Command {
	var (
		configPath string
		recipeDir  string
	)

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all available recipes",
		Long: `List all recipe definitions discovered from the
recipes directory and inline definitions in config.yaml.

Each recipe is defined as a YAML file (*.yaml) with
fields: name, description, instruction, tools, model, etc.

Examples:
  wukong recipe list
  wukong recipe ls --dir .wukong/recipes`,
		RunE: runRecipeList,
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file")
	cmd.Flags().StringVarP(
		&recipeDir, "dir", "d", "",
		"Recipes directory (overrides config)")

	return cmd
}

func runRecipeList(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	recipeDir, _ := cmd.Flags().GetString("dir")

	// Resolve recipes directory
	dir := resolveRecipeDir(configPath, recipeDir)
	if dir == "" {
		fmt.Println("No recipes directory configured.")
		fmt.Println("")
		fmt.Println("Create recipes in .wukong/recipes/:")
		fmt.Println("  mkdir -p .wukong/recipes")
		fmt.Println("  # Add .yaml recipe files there")
		return nil
	}

	cfg, err := loadRecipeConfig(configPath)
	if err != nil {
		return err
	}

	total := 0

	// List file-based recipes
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("Recipes directory not found: %s\n", dir)
		} else {
			return fmt.Errorf("read recipes dir: %w", err)
		}
	} else {
		fmt.Printf("File-based recipes (%s):\n\n", dir)

		for _, entry := range entries {
			if entry.IsDir() ||
				(!strings.HasSuffix(entry.Name(), ".yaml") &&
					!strings.HasSuffix(entry.Name(), ".yml")) {
				continue
			}

			recipePath := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(recipePath)
			if err != nil {
				fmt.Printf("  %-25s (read error: %v)\n",
					entry.Name(), err)
				continue
			}

			var rc agent.RecipeConfig
			if err := yaml.Unmarshal(data, &rc); err != nil {
				fmt.Printf("  %-25s (parse error: %v)\n",
					entry.Name(), err)
				continue
			}

			total++
			tools := formatRecipeTools(rc)
			fmt.Printf("  %2d. %-22s %s\n", total,
				rc.Name, rc.Description)
			if tools != "" {
				fmt.Printf("      tools: %s\n", tools)
			}
		}
	}

	// List inline recipes from config
	if cfg != nil && len(cfg.Agent.InlineRecipes) > 0 {
		fmt.Printf("\nInline recipes (config.yaml):\n\n")
		for _, rc := range cfg.Agent.InlineRecipes {
			total++
			name := getMapStr(rc, "name")
			desc := getMapStr(rc, "description")
			fmt.Printf("  %2d. %-22s %s\n", total, name, desc)
		}
	}

	if total == 0 {
		fmt.Println("No recipes found.")
	} else {
		fmt.Printf("\nTotal: %d recipe(s)\n", total)
	}

	return nil
}

// ==========================================================================
// recipe show
// ==========================================================================

func newRecipeShowCmd() *cobra.Command {
	var (
		configPath string
		recipeDir  string
	)

	cmd := &cobra.Command{
		Use:   "show <recipe-name>",
		Short: "Show a recipe's full YAML configuration",
		Long: `Display the complete YAML configuration of a recipe
including its instruction, tools, parameters, and settings.

Examples:
  wukong recipe show code-reviewer
  wukong recipe show my-reviewer --dir .wukong/recipes`,
		RunE: runRecipeShow,
		Args: cobra.ExactArgs(1),
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file")
	cmd.Flags().StringVarP(
		&recipeDir, "dir", "d", "",
		"Recipes directory (overrides config)")

	return cmd
}

func runRecipeShow(cmd *cobra.Command, args []string) error {
	name := args[0]
	configPath, _ := cmd.Flags().GetString("config")
	recipeDir, _ := cmd.Flags().GetString("dir")

	dir := resolveRecipeDir(configPath, recipeDir)
	if dir == "" {
		return fmt.Errorf("no recipes directory configured")
	}

	// Search for the recipe file
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read recipes dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() ||
			(!strings.HasSuffix(entry.Name(), ".yaml") &&
				!strings.HasSuffix(entry.Name(), ".yml")) {
			continue
		}

		recipePath := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(recipePath)
		if err != nil {
			continue
		}

		var rc agent.RecipeConfig
		if err := yaml.Unmarshal(data, &rc); err != nil {
			continue
		}

		if rc.Name == name {
			fmt.Println(strings.Repeat("─", 60))
			fmt.Printf("  Recipe: %s\n", name)
			fmt.Printf("  File:   %s\n", entry.Name())
			fmt.Println(strings.Repeat("─", 60))
			fmt.Println(string(data))
			return nil
		}
	}

	// Check inline recipes
	cfg, _ := loadRecipeConfig(configPath)
	if cfg != nil {
		for _, rc := range cfg.Agent.InlineRecipes {
			rcName := getMapStr(rc, "name")
			if rcName == name {
				fmt.Println(strings.Repeat("─", 60))
				fmt.Printf("  Recipe: %s (inline)\n", name)
				fmt.Println(strings.Repeat("─", 60))

				out, _ := yaml.Marshal(rc)
				fmt.Println(string(out))
				return nil
			}
		}
	}

	return fmt.Errorf("recipe %q not found in %s", name, dir)
}

// ==========================================================================
// recipe validate
// ==========================================================================

func newRecipeValidateCmd() *cobra.Command {
	var recipeDir string

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate recipe YAML files",
		Long: `Parse all recipe YAML files in the recipes directory
and report any syntax errors or validation issues.

Examples:
  wukong recipe validate
  wukong recipe validate --dir .wukong/recipes`,
		RunE: runRecipeValidate,
	}

	cmd.Flags().StringVarP(
		&recipeDir, "dir", "d", "",
		"Recipes directory (default: .wukong/recipes)")

	return cmd
}

func runRecipeValidate(cmd *cobra.Command, args []string) error {
	recipeDir, _ := cmd.Flags().GetString("dir")

	if recipeDir == "" {
		recipeDir = ".wukong/recipes"
	}

	entries, err := os.ReadDir(recipeDir)
	if err != nil {
		return fmt.Errorf("read recipes dir %q: %w", recipeDir, err)
	}

	valid := 0
	failed := 0

	for _, entry := range entries {
		if entry.IsDir() ||
			(!strings.HasSuffix(entry.Name(), ".yaml") &&
				!strings.HasSuffix(entry.Name(), ".yml")) {
			continue
		}

		recipePath := filepath.Join(recipeDir, entry.Name())
		data, err := os.ReadFile(recipePath)
		if err != nil {
			fmt.Printf("  ✗ %s — read error: %v\n",
				entry.Name(), err)
			failed++
			continue
		}

		var rc agent.RecipeConfig
		if err := yaml.Unmarshal(data, &rc); err != nil {
			fmt.Printf("  ✗ %s — YAML parse error: %v\n",
				entry.Name(), err)
			failed++
			continue
		}

		// Validate required fields
		issues := validateRecipe(&rc)
		if len(issues) > 0 {
			fmt.Printf("  ✗ %s (%s):\n", entry.Name(), rc.Name)
			for _, issue := range issues {
				fmt.Printf("      - %s\n", issue)
			}
			failed++
			continue
		}

		fmt.Printf("  ✓ %s (%s)\n", entry.Name(), rc.Name)
		valid++
	}

	fmt.Printf("\nResults: %d valid, %d failed, %d total\n",
		valid, failed, valid+failed)

	if failed > 0 {
		return fmt.Errorf("%d recipe(s) have validation errors", failed)
	}
	return nil
}

// ==========================================================================
// Helpers
// ==========================================================================

// resolveRecipeDir resolves the recipe directory from config or
// command-line override or defaults.
func resolveRecipeDir(configPath, dirOverride string) string {
	if dirOverride != "" {
		return dirOverride
	}

	loader, err := config.NewLoader(configPath)
	if err != nil {
		return ""
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return ""
	}

	if wukongCfg.Agent.RecipeDir != "" {
		return wukongCfg.Agent.RecipeDir
	}

	// Default location
	return ".wukong/recipes"
}

// loadRecipeConfig loads config for recipe commands.
func loadRecipeConfig(configPath string) (*config.WukongConfig, error) {
	loader, err := config.NewLoader(configPath)
	if err != nil {
		return nil, err
	}
	return loader.Load()
}

// validateRecipe checks a recipe config for common issues.
func validateRecipe(rc *agent.RecipeConfig) []string {
	var issues []string

	if rc.Name == "" {
		issues = append(issues, "missing required field: name")
	}
	if rc.Instruction == "" && rc.Description == "" {
		issues = append(issues,
			"at least one of instruction or description is required")
	}
	if len(rc.Tools) > 0 {
		for _, t := range rc.Tools {
			if t == "" {
				issues = append(issues,
					"tools list contains empty entry")
				break
			}
		}
	}

	return issues
}

// formatRecipeTools formats the tools list for display.
func formatRecipeTools(rc agent.RecipeConfig) string {
	if len(rc.Tools) == 0 {
		return ""
	}
	display := make([]string, len(rc.Tools))
	copy(display, rc.Tools)
	return strings.Join(display, ", ")
}

// getMapStr safely extracts a string value from a map[string]any.
func getMapStr(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}
