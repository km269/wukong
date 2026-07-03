// Package cli provides the "wukong session" subcommands for session
// lifecycle management: list and delete.
package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/km269/wukong/internal/config"
	wksession "github.com/km269/wukong/internal/session"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// newSessionListCmd creates the "wukong session list" subcommand.
func newSessionListCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all sessions",
		Long: `List all saved conversation sessions with their metadata
including session ID, last access time, and event count.

Examples:
  wukong session list
  wukong session list --config ./my-config.yaml`,
		RunE: runSessionList,
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file (default: auto-discover)")

	return cmd
}

func runSessionList(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")

	loader, err := config.NewLoader(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	svc, cleanup, err := createSessionService(wukongCfg)
	if err != nil {
		return fmt.Errorf("create session service: %w", err)
	}
	defer cleanup()

	userID := resolveUserID()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// List sessions using tRPC session service
	sessions, err := svc.ListSessions(ctx, session.UserKey{
		AppName: "wukong-app",
		UserID:  userID,
	})
	if err != nil {
		// If ListSessions is not supported, provide helpful message
		return fmt.Errorf("list sessions: %w (backend: %s)",
			err, wukongCfg.Session.Backend)
	}

	if len(sessions) == 0 {
		fmt.Println("No sessions found.")
		fmt.Printf("User: %s\n", userID)
		return nil
	}

	fmt.Printf("Sessions for user %s:\n\n", userID)
	fmt.Printf("%-38s %-10s %-20s\n",
		"SESSION ID", "EVENTS", "LAST UPDATED")
	fmt.Println(strings.Repeat("-", 72))

	for _, s := range sessions {
		sid := s.ID
		if len(sid) > 36 {
			sid = sid[:36]
		}
		eventCount := len(s.Events)
		updated := "unknown"
		if !s.UpdatedAt.IsZero() {
			updated = s.UpdatedAt.Format("2006-01-02 15:04:05")
		}
		fmt.Printf("%-38s %-10d %-20s\n", sid, eventCount, updated)
	}

	fmt.Printf("\nTotal: %d session(s)\n", len(sessions))
	return nil
}

// newSessionDeleteCmd creates the "wukong session delete" subcommand.
func newSessionDeleteCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:     "delete <session-id>",
		Aliases: []string{"rm"},
		Short:   "Delete a session by ID",
		Long: `Delete a conversation session and all its events.
This operation cannot be undone.

Examples:
  wukong session delete abc12345
  wukong session rm abc12345`,
		RunE: runSessionDelete,
		Args: cobra.ExactArgs(1),
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file (default: auto-discover)")

	return cmd
}

func runSessionDelete(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	sessionID := args[0]

	loader, err := config.NewLoader(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	svc, cleanup, err := createSessionService(wukongCfg)
	if err != nil {
		return fmt.Errorf("create session service: %w", err)
	}
	defer cleanup()

	userID := resolveUserID()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Delete session
	if err := svc.DeleteSession(ctx, session.Key{
		AppName:   "wukong-app",
		UserID:    userID,
		SessionID: sessionID,
	}); err != nil {
		return fmt.Errorf("delete session %q: %w", sessionID, err)
	}

	fmt.Printf("Session %q deleted.\n", sessionID)
	return nil
}

// createSessionService creates a temporary session service for CLI
// management commands. Returns the service and a cleanup function.
func createSessionService(
	cfg *config.WukongConfig,
) (session.Service, func(), error) {
	svc, err := wksession.NewSessionService(
		&cfg.Session,
		util.NewDatabasePool(config.ResolvePath(cfg.Session.DBPath)),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create session service: %w", err)
	}

	cleanup := func() {
		_ = svc.Close()
	}

	return svc.Service, cleanup, nil
}
