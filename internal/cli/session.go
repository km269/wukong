package cli

import (
	"context"
	"fmt"
	"github.com/km269/wukong/internal/ard"
	"github.com/km269/wukong/internal/capability"
	"github.com/km269/wukong/internal/cli/tui"
	"github.com/km269/wukong/internal/extension"
	"github.com/km269/wukong/internal/gateway"
	"github.com/km269/wukong/internal/knowledge"
	"github.com/km269/wukong/internal/project"
	"github.com/km269/wukong/internal/server"
	wksession "github.com/km269/wukong/internal/session"
	"github.com/km269/wukong/internal/summon"
	"github.com/km269/wukong/internal/util"
	"github.com/spf13/cobra"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func newSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Start an interactive agent session",
		Long: `Start an interactive session with the AI agent.
The agent can call tools, browse the web, execute commands,
and complete tasks autonomously.

Subcommands:
  list    List all saved sessions
  delete  Delete a session by ID
		
Examples:
  wukong session
  wukong session --provider openai
  wukong session --model gpt-4o
  wukong session --session-id resume-123
  wukong session list
  wukong session delete abc12345`,
		RunE: runSession,
	}

	cmd.Flags().StringP("provider", "p", "",
		"Model provider to use (overrides config default)")
	cmd.Flags().StringP("session-id", "s", "",
		"Session ID to resume (creates new if not specified)")
	cmd.Flags().StringP("model", "m", "",
		"Model name to use (overrides provider default)")
	cmd.Flags().StringP("config", "c", "",
		"Path to config file (default: ~/.config/wukong/config.yaml)")
	cmd.Flags().Float64("temperature", -1,
		"Model temperature (0.0-2.0, overrides config)")
	cmd.Flags().Int("max-tokens", 0,
		"Maximum output tokens per LLM call (overrides config)")
	cmd.Flags().Bool("no-stream", false,
		"Disable streaming output")

	cmd.AddCommand(newSessionListCmd())
	cmd.AddCommand(newSessionDeleteCmd())
	cmd.AddCommand(newSessionExportCmd())
	cmd.AddCommand(newSessionInfoCmd())
	cmd.AddCommand(newSessionResumeCmd())

	return cmd
}

func runSession(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	sessionID, _ := cmd.Flags().GetString("session-id")
	provider, _ := cmd.Flags().GetString("provider")
	modelName, _ := cmd.Flags().GetString("model")
	temperature, _ := cmd.Flags().GetFloat64("temperature")
	maxTokens, _ := cmd.Flags().GetInt("max-tokens")
	noStream, _ := cmd.Flags().GetBool("no-stream")

	// An explicit workingDir of "" falls back to os.Getwd().
	return startInteractiveSession(configPath, provider, modelName,
		temperature, maxTokens, noStream, sessionID, "")
}

// startInteractiveSession bootstraps the full agent stack and launches
// the TUI. It is shared by `wukong session` (runSession) and the
// project selector ([r]ecover) so both go through the same
// bootstrap/cleanup/signal-handling path.
// startInteractiveSession bootstraps the full agent stack and launches
// the TUI. It is shared by `wukong session` (runSession) and the
// project selector ([r]ecover) so both go through the same
// bootstrap/cleanup/signal-handling path.
func startInteractiveSession(
	configPath, provider, modelName string,
	temperature float64, maxTokens int, noStream bool,
	sessionID, workingDir string,
) error {

	// Build a reasonably unique user identifier.
	// Priority: USER env var (Unix), USERDOMAIN\USERNAME (Windows),
	// hostname fallback, "default" last resort. Shared with other CLI
	// commands via resolveUserID (run.go).
	userID := resolveUserID()

	if sessionID == "" {
		sessionID = resolveSessionID()
	}

	// Get current working directory for project tracking.
	// An explicit workingDir (project selector recover) takes
	// precedence; caller is responsible for it existing.
	if workingDir == "" {
		workingDir = resolveWorkingDir()
	}

	// Report model overrides if any
	if provider != "" || modelName != "" {
		parts := []string{}
		if provider != "" {
			parts = append(parts, "provider="+provider)
		}
		if modelName != "" {
			parts = append(parts, "model="+modelName)
		}
		if util.DebugEnabled {
			fmt.Printf("Overrides: %s\n", strings.Join(parts, ", "))
		}
	}

	// === Quick pre-load: show session info BEFORE full bootstrap ===
	// This gives the user immediate feedback while subsystems load.
	quickCfg := quickLoadConfig(configPath, provider, modelName)
	if util.DebugEnabled {
		fmt.Printf(
			"Session: %s\nProject: %s\nProvider: %s\nModel: %s\n",
			sessionID[:8],
			workingDir,
			quickCfg.provider,
			quickCfg.model,
		)
		fmt.Println("Initializing subsystems...")
	}

	// Bootstrap the full system
	wukongCfg, loop, bootstrapState, err := bootstrapSession(
		configPath, userID, sessionID, provider, modelName,
		temperature, maxTokens, noStream,
	)
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}

	// Set up OS signal handling for graceful shutdown.
	// On SIGINT/SIGTERM, the loop is closed and all resources
	// (session, memory, telemetry, A2A server, database pool)
	// are released via the defer cleanup below.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		if util.DebugEnabled {
			fmt.Printf("\nReceived signal %v, shutting down...\n", sig)
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// shutdownBootstrap is idempotent (sync.Once): the deferred
		// call below will no-op if this signal handler already ran.
		_ = shutdownBootstrap(shutdownCtx, bootstrapState, loop)
		// Do NOT use os.Exit(0) here — let the main goroutine
		// return naturally so defer cleanup and log flushing
		// can complete.
	}()

	// Ensure cleanup on return. This is a safety net for the normal
	// (non-signal) return path; if the signal handler already shut
	// things down, shutdownBootstrap's sync.Once makes this a no-op.
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = shutdownBootstrap(shutdownCtx, bootstrapState, loop)
	}()

	// Track the working directory for session recovery.
	if bootstrapState.ProjectMgr != nil && workingDir != "" {
		bootstrapState.ProjectMgr.TrackProject(
			workingDir, sessionID, "")
	}

	// Start TUI — pass projectMgr for instruction tracking and session
	// service for the multi-session tab.
	return tui.StartTUI(
		wukongCfg, loop, userID, sessionID,
		workingDir, bootstrapState.ProjectMgr,
		bootstrapState.SessionSvc, "")
}

// BootstrapState holds resources created during bootstrap that need
// cleanup beyond the agent loop's scope (e.g., A2A server, AG-UI server).
// BootstrapState holds resources created during bootstrap that need
// cleanup beyond the agent loop's scope (e.g., A2A server, AG-UI server).
type BootstrapState struct {
	shutdownState // idempotent shutdown guard (see shutdown.go)

	A2AServer         *summon.A2AServer
	AGUIServer        *server.AGUIServer
	ACPServer         *server.ACPServer
	ACPMCPBridge      *extension.ACPMCPBridge
	MCPServer         *extension.MCPServer
	ARDRegistry       *ard.RegistryServer
	ANPServer         *http.Server
	ANPMeta           *summon.MetaProtocol
	ANPMessenger      *summon.E2EEMessenger
	CredentialRotator *summon.CredentialRotator
	ExtMgr            *extension.Manager

	// Caps is the unified capability registry (roadmap P0-1 Phase
	// A): every extension-sourced tool registered under a stable
	// bus address. Read-only in this phase — consumed by the caps
	// CLI and tests; CoreLoop keeps its own tool aggregation.
	Caps          *capability.Registry
	KnowledgeMgr  *knowledge.Manager
	ProjectMgr    *project.Manager
	GatewayServer *gateway.GatewayServer

	// SessionSvc is the session store service used by the agent loop;
	// shared with the TUI's multi-session tab (C1). Nil when no
	// session backend is configured.
	SessionSvc *wksession.SessionService

	// DBPing probes the shared database pool for liveness; wired by
	// bootstrapSession and consumed by health checks. Nil when no DB
	// pool is in use.
	DBPing func(ctx context.Context) error
}

// bootstrapSession initializes all components needed for a session.
