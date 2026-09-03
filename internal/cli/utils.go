// Package cli provides utility commands for wukong including
// documentation access, session resume, and system statistics.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/km269/wukong/internal/config"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ==========================================================================
// wukong docs — open documentation
// ==========================================================================

func newDocsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "docs",
		Short: "Open wukong documentation in browser",
		Long: `Open the wukong project documentation in your default browser.
If a local documentation directory exists, it opens there first.

Examples:
  wukong docs              # Opens GitHub docs`,
		RunE: runDocs,
	}
}

func runDocs(cmd *cobra.Command, args []string) error {
	// Try to find local docs first
	localDocs := findLocalDocs()
	url := "https://github.com/km269/wukong"

	if localDocs != "" {
		url = "file://" + localDocs + "/README.md"
		fmt.Printf("Opening local documentation: %s\n", localDocs)
	} else {
		fmt.Println("Opening online documentation...")
	}

	if err := openBrowser(url); err != nil {
		fmt.Printf("Could not open browser automatically.\n")
		fmt.Printf("Visit: %s\n", url)
	} else {
		fmt.Println("Documentation opened in browser.")
	}

	return nil
}

func findLocalDocs() string {
	candidates := []string{
		"docs",
		"../docs",
	}

	// Also try from the binary location
	if exePath, err := os.Executable(); err == nil {
		dir := strings.TrimSuffix(exePath, "wukong.exe")
		dir = strings.TrimSuffix(dir, "wukong")
		candidates = append(candidates, dir+"docs")
	}

	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			if abs, err := os.Getwd(); err == nil {
				return abs + "/" + c
			}
		}
	}

	return ""
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "linux":
		return exec.Command("xdg-open", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32",
			"url.dll,FileProtocolHandler", url).Start()
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

// ==========================================================================
// wukong session resume — quick resume last session
// ==========================================================================

func newSessionResumeCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "resume",
		Short: "Quickly resume the most recent session",
		Long: `Find and display the session ID of the most recently
updated session, along with the command to resume it.

Examples:
  wukong session resume`,
		RunE: runSessionResume,
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file")

	return cmd
}

func runSessionResume(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")

	svc, cleanup, err := createSessionServiceForExport(configPath)
	if err != nil {
		return err
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	userID := resolveUserID()
	sessions, err := svc.ListSessions(ctx, session.UserKey{
		AppName: "wukong-app",
		UserID:  userID,
	})
	if err != nil {
		return fmt.Errorf("list sessions: %w (backend: %s)",
			err, "sqlite")
	}

	if len(sessions) == 0 {
		fmt.Println("No sessions found.")
		return nil
	}

	// Find most recent
	var recent *session.Session
	for _, s := range sessions {
		if recent == nil || s.UpdatedAt.After(recent.UpdatedAt) {
			recent = s
		}
	}

	if recent == nil {
		fmt.Println("No recent session found.")
		return nil
	}

	fmt.Println(strings.Repeat("─", 55))
	fmt.Println("  Resume Session")
	fmt.Println(strings.Repeat("─", 55))

	fmt.Printf("\n  Session ID:    %s\n", recent.ID[:8])
	fmt.Printf("  Events:        %d\n", len(recent.Events))
	fmt.Printf("  Last updated:  %s\n",
		recent.UpdatedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("\n  To resume:\n")
	fmt.Printf("    wukong session --session-id %s\n", recent.ID)

	return nil
}

// ==========================================================================
// wukong stats — system statistics dashboard
// ==========================================================================

func newStatsCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show system statistics dashboard",
		Long: `Display a comprehensive statistics dashboard including
database size, session counts, memory usage, and disk info.

Examples:
  wukong stats`,
		RunE: runStats,
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file")

	return cmd
}

func runStats(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")

	loader, err := config.NewLoader(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	fmt.Println(strings.Repeat("═", 60))
	fmt.Println("  Wukong Statistics Dashboard")
	fmt.Println(strings.Repeat("═", 60))

	// System info
	fmt.Println("\n  [System]")
	fmt.Printf("  Go version:     %s\n", runtime.Version())
	fmt.Printf("  OS/Arch:        %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("  CPUs:           %d\n", runtime.NumCPU())
	fmt.Printf("  Goroutines:     %d\n", runtime.NumGoroutine())

	// Config
	fmt.Println("\n  [Configuration]")
	fmt.Printf("  Provider:       %s\n", wukongCfg.DefaultProvider)
	fmt.Printf("  Providers:      %d configured\n", len(wukongCfg.Providers))
	fmt.Printf("  Log level:      %s\n", wukongCfg.LogLevel)
	fmt.Printf("  Permission:     %s\n", wukongCfg.Security.PermissionMode)

	// Database stats
	dbPath := config.ResolvePath(wukongCfg.Session.DBPath)
	if info, err := os.Stat(dbPath); err == nil {
		sizeMB := float64(info.Size()) / (1024 * 1024)
		fmt.Println("\n  [Database]")
		fmt.Printf("  File:           %s\n", dbPath)
		fmt.Printf("  Size:           %.2f MB\n", sizeMB)
		fmt.Printf("  Modified:       %s\n",
			info.ModTime().Format("2006-01-02 15:04:05"))
	}

	// Session stats
	svc, cleanup, _ := createSessionServiceForExport(configPath)
	if svc != nil {
		defer cleanup()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		sessions, err := svc.ListSessions(ctx, session.UserKey{
			AppName: "wukong-app",
			UserID:  resolveUserID(),
		})
		if err == nil {
			fmt.Println("\n  [Sessions]")
			fmt.Printf("  Total:          %d\n", len(sessions))
			if len(sessions) > 0 {
				totalEvents := 0
				for _, s := range sessions {
					totalEvents += len(s.Events)
				}
				fmt.Printf("  Total events:   %d\n", totalEvents)
				fmt.Printf("  Avg events:     %d\n",
					totalEvents/len(sessions))

				// Sort by update time
				sort.Slice(sessions, func(i, j int) bool {
					return sessions[i].UpdatedAt.After(
						sessions[j].UpdatedAt)
				})

				fmt.Println("\n  [Recent Sessions]")
				showN := len(sessions)
				if showN > 5 {
					showN = 5
				}
				fmt.Printf("  %-10s %-10s %-20s\n",
					"ID", "EVENTS", "UPDATED")
				for i := 0; i < showN; i++ {
					s := sessions[i]
					id := s.ID
					if len(id) > 8 {
						id = id[:8]
					}
					fmt.Printf("  %-10s %-10d %-20s\n",
						id, len(s.Events),
						s.UpdatedAt.Format("2006-01-02 15:04"))
				}
			}
		}
	}

	// Feature status
	fmt.Println("\n  [Feature Status]")
	fmt.Printf("  Memory (auto_extract): %v\n",
		wukongCfg.Memory.AutoExtract)
	fmt.Printf("  CortexDB:              %v\n",
		wukongCfg.Cortex.Enabled)
	fmt.Printf("  Knowledge (RAG):       %v\n",
		wukongCfg.Knowledge.Enabled)
	fmt.Printf("  ARD:                   %v\n",
		wukongCfg.ARD.Enabled)
	fmt.Printf("  Evolution:             %v\n",
		wukongCfg.Evolution.Enabled)

	fmt.Println()
	return nil
}
