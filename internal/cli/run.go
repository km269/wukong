// Package cli provides the "wukong run" subcommand for non-interactive
// single-shot agent execution from the terminal or pipeline.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/km269/wukong/internal/agent"
	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// newRunCmd creates the "wukong run" command for terminal integration.
// It supports --message flag, stdin pipe, positional args, and
// --dialogue mode for multi-turn shell conversations.
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
		dialogue    bool
	)

	cmd := &cobra.Command{
		Use:   "run [flags] [message...]",
		Short: "Execute an agent request (single-shot or dialogue mode)",
		Long: `Run the AI agent on a prompt and print the response to stdout.

Single-shot mode (default):
  wukong run -m "explain this function"
  echo "optimize app.go" | wukong run

Dialogue mode (-d / --dialogue):
  Start a multi-turn shell conversation with auto-generated
  session ID. Type messages line by line. Ctrl+D or /exit to quit.

  wukong run -d
  wukong run -d -p deepseek --model deepseek-chat

Examples:
  wukong run -m "explain this function"
  wukong run -d                                     # start dialogue
  wukong run -d -s my-task                          # dialogue with custom session
  wukong run -d -p deepseek --model deepseek-chat   # dialogue with specific model`,
		RunE: func(cmd *cobra.Command, args []string) error {
			input := resolveInput(message, args)

			if sessionID == "" {
				sessionID = resolveSessionID()
			}

			// Resolve userID for the runner and subsystems.
			runUserID := resolveUserID()

			// Bootstrap the full agent stack.
			wukongCfg, loop, state, err := bootstrapSession(
				configPath, runUserID, sessionID,
				provider, modelName,
				temperature, maxTokens, noStream,
			)
			if err != nil {
				return fmt.Errorf(
					"bootstrap failed: %w", err)
			}
			defer func() {
				// shutdownBootstrap closes both the servers (in
				// state) and the agent loop, and is idempotent.
				shutdownCtx, cancel := context.WithTimeout(
					context.Background(), 10*time.Second)
				defer cancel()
				_ = shutdownBootstrap(shutdownCtx, state, loop)
			}()

			// Track working directory for project recovery.
			workingDir, _ := os.Getwd()
			if state.ProjectMgr != nil && workingDir != "" {
				state.ProjectMgr.TrackProject(
					workingDir, sessionID, input)
			}

			// Dialogue mode: multi-turn REPL.
			if dialogue {
				// If -m or args were also given, process them
				// as the first turn.
				if input != "" {
					if printErr := runOneShot(
						wukongCfg, coreRunner{loop},
						runUserID, sessionID,
						input, noStream,
					); printErr != nil {
						fmt.Fprintln(os.Stderr,
							"agent error:", printErr)
					}
				}
				return runDialogue(
					wukongCfg, loop,
					runUserID, sessionID, noStream,
				)
			}

			// Single-shot mode: -m / args / stdin required.
			if input == "" {
				return fmt.Errorf(
					"no input: use --message, positional args, pipe stdin, or --dialogue")
			}
			return runOneShot(
				wukongCfg, coreRunner{loop},
				runUserID, sessionID,
				input, noStream,
			)
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
		"Session ID for multi-turn context (auto-generated if not specified)")

	cmd.Flags().BoolVarP(
		&dialogue, "dialogue", "d", false,
		"Enter multi-turn dialogue mode in the shell (Ctrl+D or /exit to quit)")

	return cmd
}

// ==========================================================================
// Single-shot execution
// ==========================================================================

// runner is the minimal agent-loop surface runOneShot depends on.
// It is an interface (rather than *agent.CoreLoop) so tests can inject
// a fake to exercise streaming output and redaction without booting
// the full agent stack.
type runner interface {
	RunStream(
		ctx context.Context,
		userID string,
		sessionID string,
		message model.Message,
		onEvent func(evt *event.Event) error,
	) (string, error)
}

// coreRunner adapts *agent.CoreLoop to the runner interface.
type coreRunner struct {
	loop *agent.CoreLoop
}

// RunStream forwards to the underlying loop.
func (c coreRunner) RunStream(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	onEvent func(evt *event.Event) error,
) (string, error) {
	return c.loop.RunStream(
		ctx, userID, sessionID, message, onEvent)
}

// runOneShot executes a single prompt and prints the response.
func runOneShot(
	cfg *config.WukongConfig,
	loop runner,
	userID, sessionID, input string,
	noStream bool,
) error {
	msg := model.NewUserMessage(input)
	ctx := context.Background()

	if !noStream && cfg.Agent.Streaming {
		// Print a separator newline before streaming output so the
		// response isn't glued to bootstrap/wake-up log lines on stderr.
		fmt.Println()
		response, err := loop.RunStream(
			ctx, userID, sessionID, msg,
			func(evt *event.Event) error {
				return streamToStdoutWith(
					evt, os.Stdout, cfg.SecretValues())
			},
		)
		fmt.Println() // final newline
		_ = response
		return err
	}

	response, err := loop.RunStream(
		ctx, userID, sessionID, msg, nil)
	fmt.Println(util.RedactSecrets(response, cfg.SecretValues()))
	return err
}

// Response streaming into runOneShot now uses streamToStdout from
// repl.go (tool-render aware).

// ==========================================================================
// Dialogue mode (REPL in shell)
// ==========================================================================

// runDialogue starts a read-eval-print loop for multi-turn conversation.
func runDialogue(
	cfg *config.WukongConfig,
	loop *agent.CoreLoop,
	userID, sessionID string,
	noStream bool,
) error {
	// Per-session command history for the line editor.
	// C2: persisted to disk (~/.wukong/history) so Up/Down recall
	// and Ctrl+R search survive across processes.
	hist := newHistoryWithFile(100, historyFilePath(cfg))

	displaySession := sessionID
	if len(displaySession) > 8 {
		displaySession = displaySession[:8]
	}

	fmt.Printf(`
╔══════════════════════════════════════════════╗
║  Wukong Dialogue Mode                       ║
║  Session: %s                          ║
║  Type your message, Ctrl+D or /exit to quit ║
╚══════════════════════════════════════════════╝
`, displaySession)

	for {
		fmt.Print("\n> ")

		line, err := editLine(os.Stdin, os.Stdout, hist)
		if err != nil {
			if err == io.EOF {
				fmt.Println("\nGoodbye.")
				return nil
			}
			return fmt.Errorf("read input: %w", err)
		}

		input := strings.TrimSpace(line)

		// Exit conditions.
		if input == "" {
			continue
		}
		if input == "/exit" || input == "/quit" {
			fmt.Println("Goodbye.")
			return nil
		}
		if input == "/session" {
			fmt.Printf("Session ID: %s\n", sessionID)
			continue
		}
		if input == "/clear" {
			// Note: session context persists server-side.
			// /clear just gives visual separation.
			fmt.Print("\033[2J\033[H") // ANSI clear screen
			continue
		}
		if input == "/history-clear" {
			// C2: wipe the persisted + in-memory dialogue history.
			hist.entries = nil
			hist.deleteHistoryFile()
			fmt.Println("History cleared.")
			continue
		}
		if input == "/help" {
			fmt.Println(`
Commands:
  /exit, /quit           Exit dialogue mode
  /session               Show current session ID
  /clear                 Clear terminal screen
  /history-clear         Clear the saved history file
  /help                  Show this help

Session ID: ` + sessionID + `
To resume later: wukong run -d -s ` + sessionID)
			continue
		}

		fmt.Println()

		// Record the executed command in history for Up/Down recall.
		hist.add(input)

		// Execute the agent call within this dialogue turn.
		printErr := runOneShot(
			cfg, loop, userID, sessionID, input, noStream,
		)
		if printErr != nil {
			fmt.Fprintf(os.Stderr,
				"\nagent error: %v\n", printErr)
		}
	}
}

// ==========================================================================
// Input resolution
// ==========================================================================

// resolveInput determines the prompt text from flag, args, or stdin.
func resolveInput(flagMsg string, args []string) string {
	if flagMsg != "" {
		return flagMsg
	}
	if len(args) > 0 {
		return strings.Join(args, " ")
	}
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		data, err := io.ReadAll(os.Stdin)
		if err == nil && len(data) > 0 {
			return strings.TrimSpace(string(data))
		}
	}
	return ""
}

// ==========================================================================
// Helpers
// ==========================================================================

// resolveUserID determines a reasonably unique user identifier.
func resolveUserID() string {
	userID := os.Getenv("USER")
	if userID == "" {
		userDomain := os.Getenv("USERDOMAIN")
		userName := os.Getenv("USERNAME")
		if userDomain != "" && userName != "" {
			userID = userDomain + "\\" + userName
		} else if userName != "" && userName != "SYSTEM" {
			userID = userName
		}
	}
	if userID == "" || userID == "SYSTEM" {
		if hostname, err := os.Hostname(); err == nil {
			userID = hostname
		}
	}
	if userID == "" {
		userID = "default"
	}
	return userID
}

// resolveSessionID returns a fresh session ID when the caller did not
// provide one.
func resolveSessionID() string {
	return uuid.New().String()
}

// resolveWorkingDir returns the current working directory, or "" when
// it cannot be determined.
func resolveWorkingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

// cleanupBootstrap has been replaced by the unified shutdownBootstrap
// (see shutdown.go), which covers all BootstrapState fields (including
// ARDRegistry and ANPServer) and is idempotent via sync.Once.
