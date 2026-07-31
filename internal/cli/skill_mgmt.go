// Package cli provides the "wukong skill" command for agent
// skill management.
package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/skill"
)

// newSkillCmd creates the "wukong skill" command group.
func newSkillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage agent skills",
		Long: `View and manage agent skills defined in the skills directory.
Skills are reusable, composable agent workflows defined in
SKILL.md files.

Subcommands:
  list    List all available skills
  show    Display a skill's full content`,
	}

	cmd.AddCommand(newSkillListCmd())
	cmd.AddCommand(newSkillShowCmd())

	return cmd
}

// ==========================================================================
// skill list
// ==========================================================================

func newSkillListCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all available skills",
		Long: `List all agent skills discovered from the skills directory.

Each skill is defined in a SKILL.md file within its own
directory under the configured skills root.

Examples:
  wukong skill list
  wukong skill ls`,
		RunE: runSkillList,
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file")

	return cmd
}

func runSkillList(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")

	mgr, err := createSkillManager(configPath)
	if err != nil {
		return err
	}

	summaries := mgr.ListSummaries()
	if len(summaries) == 0 {
		fmt.Println("No skills found.")
		fmt.Println("")
		fmt.Println("Create a skill by adding a SKILL.md file:")
		fmt.Println("  mkdir -p .wukong/skills/my-skill")
		fmt.Println("  echo '# My Skill' > .wukong/skills/my-skill/SKILL.md")
		return nil
	}

	fmt.Printf("Available skills (%d):\n\n", len(summaries))

	for i, s := range summaries {
		desc := s.Description
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Printf("  %2d. %s\n", i+1, s.Name)
		fmt.Printf("      %s\n", desc)
	}

	return nil
}

// ==========================================================================
// skill show
// ==========================================================================

func newSkillShowCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "show <skill-name>",
		Short: "Show a skill's full content",
		Long: `Display the complete content of a specific skill,
including its description, body text, and any associated
document files.

Examples:
  wukong skill show my-skill`,
		RunE: runSkillShow,
		Args: cobra.ExactArgs(1),
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file")

	return cmd
}

func runSkillShow(cmd *cobra.Command, args []string) error {
	skillName := args[0]
	configPath, _ := cmd.Flags().GetString("config")

	mgr, err := createSkillManager(configPath)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sk, err := mgr.GetSkill(ctx, skillName)
	if err != nil {
		return fmt.Errorf("get skill %q: %w", skillName, err)
	}

	fmt.Println(strings.Repeat("─", 60))
	fmt.Printf("  Skill: %s\n", sk.Summary.Name)
	fmt.Println(strings.Repeat("─", 60))

	if sk.Summary.Description != "" {
		fmt.Printf("\n  Description:\n    %s\n", sk.Summary.Description)
	}

	fmt.Println("\n  Content:")
	fmt.Println("  " + strings.Repeat("─", 56))

	// Print skill body with indentation
	for _, line := range strings.Split(sk.Body, "\n") {
		if line != "" {
			fmt.Printf("  %s\n", line)
		}
	}

	fmt.Println()

	return nil
}

// createSkillManager creates a skill manager for CLI use.
func createSkillManager(configPath string) (*skill.Manager, error) {
	loader, err := config.NewLoader(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	mgr := skill.NewManager(wukongCfg.Skill)

	// Resolve skills directory
	skillsDir := wukongCfg.Skill.SkillsDir
	if skillsDir == "" {
		skillsDir = ".wukong/skills"
	}

	if _, err := os.Stat(skillsDir); os.IsNotExist(err) {
		// .wukong/skills does not exist; create under
		// ~/.config/wukong/skills as fallback
		homeDir, _ := os.UserHomeDir()
		if homeDir != "" {
			skillsDir = filepath.Join(homeDir, ".config",
				"wukong", "skills")
		}
	}

	wukongCfg.Skill.SkillsDir = skillsDir
	wukongCfg.Skill.Enabled = true

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := mgr.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("initialize skills: %w", err)
	}

	return mgr, nil
}
