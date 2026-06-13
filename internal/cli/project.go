// Package cli provides the CLI command implementations for wukong.
package cli

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/project"
)

// newProjectCmd creates the "wukong project" command for interactive
// project selection and session recovery.
func newProjectCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "project",
		Short: "Recover or manage tracked project sessions",
		Long: `List recently tracked working directories and choose to
recover a previous session, start a new session in that
directory, or clear all tracked projects.

Tracked projects are automatically recorded every time you run
"wukong session". Each entry stores:
  - Working directory path
  - Last session ID (for recovery)
  - Last access timestamp
  - Last instruction (first user message)

Commands:
  wukong project        — interactive project selection
  wukong project clear  — remove all tracked projects
  wukong projects       — list all tracked projects (alias)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectInteractive(configPath)
		},
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file (default: auto-discover)",
	)

	// Subcommand: project clear
	cmd.AddCommand(&cobra.Command{
		Use:   "clear",
		Short: "Remove all tracked project records",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectClear(configPath)
		},
	})

	return cmd
}

// newProjectsCmd creates the "wukong projects" alias command.
func newProjectsCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:     "projects",
		Aliases: []string{"ls-projects"},
		Short:   "List all tracked project directories",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectsList(configPath)
		},
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file (default: auto-discover)",
	)

	return cmd
}

// runProjectInteractive presents a numbered list of tracked projects,
// lets the user pick one, and then choose to recover/resume/clear.
func runProjectInteractive(configPath string) error {
	loader, err := config.NewLoader(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	mgr, err := project.NewManager(wukongCfg)
	if err != nil {
		return fmt.Errorf("create project manager: %w", err)
	}

	records := mgr.ListProjects()
	if len(records) == 0 {
		fmt.Println("No tracked projects found.")
		fmt.Println("\nStart a 'wukong session' in any " +
			"directory to begin tracking.")
		return nil
	}

	printProjectTable(records)
	fmt.Printf("\n[1-%d] select project, [c]lear all, [q]uit\n",
		len(records))
	fmt.Print("> ")

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return nil
	}
	choice := strings.TrimSpace(scanner.Text())

	switch strings.ToLower(choice) {
	case "q", "quit", "":
		return nil
	case "c", "clear":
		if err := mgr.Clear(); err != nil {
			return fmt.Errorf("clear projects: %w", err)
		}
		fmt.Println("All tracked projects cleared.")
		return nil
	}

	idx, err := strconv.Atoi(choice)
	if err != nil || idx < 1 || idx > len(records) {
		fmt.Println("Invalid selection. " +
			"Choose a number from the list.")
		return nil
	}

	selected := records[idx-1]

	fmt.Printf("\nSelected: %s\n", selected.Path)
	fmt.Printf("Last session: %s\n", selected.SessionID[:8])
	if selected.LastInstruction != "" {
		inst := selected.LastInstruction
		if len(inst) > 80 {
			inst = inst[:77] + "..."
		}
		fmt.Printf("Last instruction: %s\n", inst)
	}

	fmt.Println("\nActions:")
	fmt.Println("  [r]ecover  — resume the last session in this project")
	fmt.Println("  [n]ew      — start a fresh session in this project")
	fmt.Println("  [q]uit")
	fmt.Print("> ")

	if !scanner.Scan() {
		return nil
	}
	action := strings.TrimSpace(scanner.Text())

	switch strings.ToLower(action) {
	case "r", "recover":
		fmt.Printf(
			"\nTo recover, run:\n"+
				"  cd %s && wukong session --session-id %s\n",
			selected.Path, selected.SessionID)
		// Auto-cd prompt for convenience.
		currentDir, _ := os.Getwd()
		if currentDir != selected.Path {
			fmt.Printf("\nCurrent directory differs. "+
				"Change first:\n  cd %s\n",
				selected.Path)
		}
	case "n", "new":
		fmt.Printf(
			"\nTo start a fresh session:\n"+
				"  cd %s && wukong session\n",
			selected.Path)
	default:
		fmt.Println("Cancelled.")
	}

	return nil
}

// runProjectsList simply prints the tracked project table.
func runProjectsList(configPath string) error {
	loader, err := config.NewLoader(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	mgr, err := project.NewManager(wukongCfg)
	if err != nil {
		return fmt.Errorf("create project manager: %w", err)
	}

	records := mgr.ListProjects()
	if len(records) == 0 {
		fmt.Println("No tracked projects found.")
		return nil
	}

	printProjectTable(records)
	return nil
}

// runProjectClear removes all tracked project records.
func runProjectClear(configPath string) error {
	loader, err := config.NewLoader(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	mgr, err := project.NewManager(wukongCfg)
	if err != nil {
		return fmt.Errorf("create project manager: %w", err)
	}

	if err := mgr.Clear(); err != nil {
		return fmt.Errorf("clear projects: %w", err)
	}

	fmt.Println("All tracked projects cleared.")
	return nil
}

// printProjectTable displays project records as a tab-aligned table.
func printProjectTable(records []project.ProjectRecord) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w,
		"#\tPATH\tSESSION\tLAST ACCESSED\tINSTRUCTION")

	for i, r := range records {
		sID := ""
		if len(r.SessionID) >= 8 {
			sID = r.SessionID[:8]
		}
		accessed := r.LastAccessed
		if t, err := time.Parse(time.RFC3339, accessed); err == nil {
			accessed = t.Format("2006-01-02 15:04")
		}

		inst := r.LastInstruction
		if len(inst) > 50 {
			inst = inst[:47] + "..."
		}
		if inst == "" {
			inst = "-"
		}

		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n",
			i+1, r.Path, sID, accessed, inst)
	}

	_ = w.Flush() //nolint:errcheck
}
