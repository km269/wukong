// Package cli provides the "wukong session" subcommands for export,
// info, and resume.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/km269/wukong/internal/config"
	wksession "github.com/km269/wukong/internal/session"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// newSessionExportCmd creates the "wukong session export" subcommand.
func newSessionExportCmd() *cobra.Command {
	var (
		configPath string
		format     string
		outputFile string
	)

	cmd := &cobra.Command{
		Use:   "export <session-id>",
		Short: "Export a session to a file",
		Long: `Export a conversation session to a file in the specified
format. Supports Markdown and JSON formats.

Examples:
  wukong session export abc12345
  wukong session export abc12345 --format json
  wukong session export abc12345 --output session.md`,
		RunE: runSessionExport,
		Args: cobra.ExactArgs(1),
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file")
	cmd.Flags().StringVarP(
		&format, "format", "f", "markdown",
		"Output format: markdown | json")
	cmd.Flags().StringVarP(
		&outputFile, "output", "o", "",
		"Output file path (default: session-<id>.<format>)")

	return cmd
}

func runSessionExport(cmd *cobra.Command, args []string) error {
	sessionID := args[0]
	configPath, _ := cmd.Flags().GetString("config")
	format, _ := cmd.Flags().GetString("format")
	outputFile, _ := cmd.Flags().GetString("output")

	svc, cleanup, err := createSessionServiceForExport(configPath)
	if err != nil {
		return err
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	sess, err := svc.GetSession(ctx, session.Key{
		AppName:   "wukong-app",
		UserID:    resolveUserID(),
		SessionID: sessionID,
	})
	if err != nil {
		return fmt.Errorf("get session %q: %w", sessionID, err)
	}

	// Determine output file
	if outputFile == "" {
		switch format {
		case "json":
			outputFile = "session-" + sessionID[:8] + ".json"
		default:
			outputFile = "session-" + sessionID[:8] + ".md"
		}
	}

	var content []byte
	switch format {
	case "json":
		content, err = exportSessionJSON(sess)
	default:
		content, err = exportSessionMarkdown(sess)
	}
	if err != nil {
		return fmt.Errorf("format session: %w", err)
	}

	if err := os.WriteFile(outputFile, content, 0644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	fmt.Printf("Session exported to: %s\n", outputFile)
	return nil
}

// exportSessionMarkdown exports a session as Markdown.
func exportSessionMarkdown(sess *session.Session) ([]byte, error) {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("# Session: %s\n\n", sess.ID[:8]))
	b.WriteString(fmt.Sprintf("**Exported**: %s\n\n",
		time.Now().Format("2006-01-02 15:04:05")))
	b.WriteString("---\n\n")

	turn := 0
	for _, evt := range sess.Events {
		if evt.Response == nil {
			continue
		}

		for _, choice := range evt.Response.Choices {
			msg := choice.Message
			role := string(msg.Role)
			content := msg.Content

			if role == "" || content == "" {
				continue
			}

			turn++
			switch role {
			case "user":
				b.WriteString(fmt.Sprintf(
					"### 👤 User\n\n%s\n\n", content))
			case "assistant":
				b.WriteString(fmt.Sprintf(
					"### 🤖 Assistant\n\n%s\n\n", content))
			case "tool":
				b.WriteString(fmt.Sprintf(
					"#### 🔧 Tool Result\n\n```\n%s\n```\n\n", content))
			case "system":
				continue
			default:
				b.WriteString(fmt.Sprintf(
					"### %s\n\n%s\n\n", role, content))
			}
		}
	}

	b.WriteString(fmt.Sprintf("\n---\n*%d message turns exported*\n", turn))

	return []byte(b.String()), nil
}

// exportSessionJSON exports a session as formatted JSON.
func exportSessionJSON(sess *session.Session) ([]byte, error) {
	type exportEvent struct {
		Index   int    `json:"index"`
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	var events []exportEvent
	idx := 0
	for _, evt := range sess.Events {
		if evt.Response == nil {
			continue
		}
		for _, choice := range evt.Response.Choices {
			msg := choice.Message
			role := string(msg.Role)
			if role == "" || msg.Content == "" || role == "system" {
				continue
			}
			events = append(events, exportEvent{
				Index:   idx,
				Role:    role,
				Content: msg.Content,
			})
			idx++
		}
	}

	output := map[string]any{
		"session_id":   sess.ID,
		"exported_at":  time.Now().Format(time.RFC3339),
		"event_count":  idx,
		"events":       events,
	}

	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal json: %w", err)
	}

	return data, nil
}

// ==========================================================================
// session info
// ==========================================================================

func newSessionInfoCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "info <session-id>",
		Short: "Show detailed session information",
		Long: `Display comprehensive details about a specific session
including event count, message distribution, and tool usage.

Examples:
  wukong session info abc12345`,
		RunE: runSessionInfo,
		Args: cobra.ExactArgs(1),
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file")

	return cmd
}

func runSessionInfo(cmd *cobra.Command, args []string) error {
	sessionID := args[0]
	configPath, _ := cmd.Flags().GetString("config")

	svc, cleanup, err := createSessionServiceForExport(configPath)
	if err != nil {
		return err
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sess, err := svc.GetSession(ctx, session.Key{
		AppName:   "wukong-app",
		UserID:    resolveUserID(),
		SessionID: sessionID,
	})
	if err != nil {
		return fmt.Errorf("get session %q: %w", sessionID, err)
	}

	stats := analyzeSession(sess)

	fmt.Println(strings.Repeat("─", 55))
	fmt.Printf("  Session: %s\n", sessionID)
	fmt.Println(strings.Repeat("─", 55))

	fmt.Printf("\n  Full ID:      %s\n", sess.ID)
	fmt.Printf("  User:         %s\n", sess.UserID)
	fmt.Printf("  Updated:      %s\n",
		sess.UpdatedAt.Format("2006-01-02 15:04:05"))

	fmt.Println("\n  [Messages]")
	fmt.Printf("  User messages:      %d\n", stats.userMsgs)
	fmt.Printf("  Assistant messages: %d\n", stats.assistantMsgs)
	fmt.Printf("  Tool results:       %d\n", stats.toolResults)
	fmt.Printf("  Total events:       %d\n", len(sess.Events))

	if stats.totalChars > 0 {
		msgCount := stats.userMsgs + stats.assistantMsgs
		if msgCount == 0 {
			msgCount = 1
		}
		fmt.Printf("\n  [Content]")
		fmt.Printf("\n  Total chars:        %d\n", stats.totalChars)
		fmt.Printf("  Avg per message:    %d chars\n",
			stats.totalChars/msgCount)
	}

	if len(stats.toolNames) > 0 {
		fmt.Println("\n  [Tools Used]")
		for name, count := range stats.toolNames {
			fmt.Printf("  %-20s %d call(s)\n", name, count)
		}
	}

	if len(sess.Summaries) > 0 {
		fmt.Println("\n  [Auto Summaries]")
		for filter, s := range sess.Summaries {
			fmt.Printf("  %s: %d chars\n", filter, len(s.Summary))
		}
	}

	fmt.Println()
	return nil
}

type sessionStats struct {
	userMsgs      int
	assistantMsgs int
	toolResults   int
	totalChars    int
	toolNames     map[string]int
}

func analyzeSession(sess *session.Session) sessionStats {
	stats := sessionStats{
		toolNames: make(map[string]int),
	}

	for _, evt := range sess.Events {
		if evt.Response == nil {
			continue
		}
		for _, choice := range evt.Response.Choices {
			msg := choice.Message
			if msg.Content == "" && len(msg.ToolCalls) == 0 {
				continue
			}

			role := string(msg.Role)
			content := msg.Content

			switch role {
			case "user":
				stats.userMsgs++
				stats.totalChars += len(content)
			case "assistant":
				stats.assistantMsgs++
				stats.totalChars += len(content)
			case "tool":
				stats.toolResults++
			}

			for _, tc := range msg.ToolCalls {
				stats.toolNames[tc.Function.Name]++
			}
		}
	}

	return stats
}

// ==========================================================================
// shared helpers
// ==========================================================================

func createSessionServiceForExport(
	configPath string,
) (session.Service, func(), error) {
	loader, err := config.NewLoader(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("parse config: %w", err)
	}

	svc, err := wksession.NewSessionService(
		&wukongCfg.Session,
		util.NewDatabasePool(config.ResolvePath(wukongCfg.Session.DBPath)),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create session service: %w", err)
	}

	cleanup := func() { _ = svc.Close() }
	return svc.Service, cleanup, nil
}
