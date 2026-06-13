// Package cli provides the "wukong run" subcommand for non-interactive
// single-shot agent execution from the terminal or pipeline.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// newRunCmd creates the "wukong run" command for terminal integration.
// It supports both --message flag and stdin pipe, enabling patterns
// like: echo "refactor X" | wukong run  or  wukong run -m "fix Y".
func newRunCmd() *cobra.Command {
	var (
		configPath  string
		message     string
		provider    string
		modelName   string
		temperature float64
		maxTokens   int
		noStream    bool
		sessionID   string
	)

	cmd := &cobra.Command{
		Use:   "run [flags]",
		Short: "Execute a single-shot agent request (no TUI)",
		Long: `Run the AI agent on a single prompt and print the
response to stdout. Ideal for terminal integration,
shell pipelines, and scripting.

Examples:
  wukong run -m "explain this function"
  echo "optimize app.go" | wukong run
  wukong run --message "add comments" --model gpt-4o`,
		RunE: func(cmd *cobra.Command, args []string) error {
			input := resolveInput(message, args)
			if input == "" {
				return fmt.Errorf(
					"no input: use --message or pipe stdin")
			}

			if sessionID == "" {
				sessionID = uuid.New().String()
			}

			// Bootstrap the full agent stack (same as interactive
			// session but without TUI).
			wukongCfg, loop, state, err := bootstrapSession(
				configPath, "", sessionID,
				provider, modelName,
				temperature, maxTokens, noStream,
			)
			if err != nil {
				return fmt.Errorf(
					"bootstrap failed: %w", err)
			}
			defer func() {
				// Close the agent loop first (triggers memory →
				// runner → session → telemetry → dbpool chain).
				if loop != nil {
					loop.Close()
				}
				cleanupBootstrap(state)
			}()

			// Track working directory for project recovery.
			workingDir, _ := os.Getwd()
			if state.ProjectMgr != nil && workingDir != "" {
				state.ProjectMgr.TrackProject(
					workingDir, sessionID, input)
			}

			// Execute the agent call.
			msg := model.NewUserMessage(input)
			ctx := context.Background()

			// Stream to stdout if streaming is enabled.
			if !noStream && wukongCfg.Agent.Streaming {
				onEvent := func(evt *event.Event) error {
					if evt.Response != nil &&
						len(evt.Response.Choices) > 0 {
						content := evt.Response.Choices[0].
							Delta.Content
						if content != "" {
							fmt.Print(content)
						}
					}
					return nil
				}
				response, err := loop.RunStream(
					ctx, "", sessionID, msg, onEvent)
				fmt.Println() // final newline
				if err != nil {
					return fmt.Errorf(
						"agent error: %w", err)
				}
				_ = response
			} else {
				// Non-streaming: collect and print at end.
				response, err := loop.RunStream(
					ctx, "", sessionID, msg, nil)
				if err != nil {
					return fmt.Errorf(
						"agent error: %w", err)
				}
				fmt.Println(response)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file (default: auto-discover)")

	cmd.Flags().StringVarP(
		&message, "message", "m", "",
		"Prompt to send to the agent")

	cmd.Flags().StringVarP(
		&provider, "provider", "p", "",
		"Model provider to use (overrides config default)")

	cmd.Flags().StringVar(
		&modelName, "model", "",
		"Model name to use (overrides provider default)")

	cmd.Flags().Float64Var(
		&temperature, "temperature", -1,
		"Model temperature (0.0-2.0, -1 = use config)")

	cmd.Flags().IntVar(
		&maxTokens, "max-tokens", 0,
		"Maximum output tokens (0 = use config)")

	cmd.Flags().BoolVar(
		&noStream, "no-stream", false,
		"Disable streaming output")

	cmd.Flags().StringVarP(
		&sessionID, "session-id", "s", "",
		"Session ID (auto-generated if not specified)")

	return cmd
}

// resolveInput determines the prompt text from flag, args, or stdin.
func resolveInput(flagMsg string, args []string) string {
	// Priority 1: --message flag
	if flagMsg != "" {
		return flagMsg
	}

	// Priority 2: positional args concatenated
	if len(args) > 0 {
		return strings.Join(args, " ")
	}

	// Priority 3: stdin pipe (check if data is available)
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		data, err := io.ReadAll(os.Stdin)
		if err == nil && len(data) > 0 {
			return strings.TrimSpace(string(data))
		}
	}

	return ""
}

// cleanupBootstrap shuts down the bootstrapped resources.
// For single-shot execution, we only need to stop the A2A server.
// The CoreLoop.Close() triggers the full cleanup chain (memory,
// runner, session, telemetry, dbpool).
func cleanupBootstrap(state *BootstrapState) {
	if state == nil {
		return
	}
	if state.A2AServer != nil {
		_ = state.A2AServer.Stop(context.Background())
	}
	if state.KnowledgeMgr != nil {
		_ = state.KnowledgeMgr.Close()
	}
}
