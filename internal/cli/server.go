// Package cli provides the "wukong server" command for running
// wukong in headless server mode (A2A, ACP, AG-UI, ACP MCP).
package cli

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/health"
	"github.com/km269/wukong/internal/util"
)

// newServerCmd creates the "wukong server" command for headless
// server mode with all four protocol endpoints.
func newServerCmd() *cobra.Command {
	var (
		configPath  string
		sessionID   string
		provider    string
		modelName   string
		temperature float64
		maxTokens   int
		noStream    bool
	)

	cmd := &cobra.Command{
		Use:   "server",
		Short: "Start wukong in headless server mode",
		Long: `Start wukong as a background server without the TUI interface.
All configured protocol servers (A2A, ACP, AG-UI, ACP MCP)
will be started and the process will run until terminated.

Protocol endpoints:
  A2A     — Agent-to-Agent communication on :9090
  ACP     — Agent Client Protocol on :9091
  AG-UI   — Web UI SSE streaming on :8080/agui
  ACP MCP — Cross-protocol tool bridge on :3400/mcp

Examples:
  wukong server
  wukong server --provider deepseek --model deepseek-chat
  wukong server --session-id my-server`,
		RunE: runServer,
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file (default: auto-discover)")
	cmd.Flags().StringVarP(
		&sessionID, "session-id", "s", "",
		"Session ID for the server instance")
	cmd.Flags().StringVarP(
		&provider, "provider", "p", "",
		"Model provider (overrides config)")
	cmd.Flags().StringVar(
		&modelName, "model", "",
		"Model name (overrides config)")
	cmd.Flags().Float64Var(
		&temperature, "temperature", -1,
		"Model temperature (-1 = use config)")
	cmd.Flags().IntVar(
		&maxTokens, "max-tokens", 0,
		"Max output tokens (0 = use config)")
	cmd.Flags().BoolVar(
		&noStream, "no-stream", false,
		"Disable streaming output")

	return cmd
}

func runServer(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	sessionID, _ := cmd.Flags().GetString("session-id")
	provider, _ := cmd.Flags().GetString("provider")
	modelName, _ := cmd.Flags().GetString("model")
	temperature, _ := cmd.Flags().GetFloat64("temperature")
	maxTokens, _ := cmd.Flags().GetInt("max-tokens")
	noStream, _ := cmd.Flags().GetBool("no-stream")

	userID := resolveUserID()

	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║  Wukong Server Mode                      ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Printf("Session: %s\nUser: %s\n\n", sessionID[:8], userID)
	fmt.Println("Bootstrapping subsystems...")

	// Bootstrap full system
	wukongCfg, loop, bootstrapState, err := bootstrapSession(
		configPath, userID, sessionID, provider, modelName,
		temperature, maxTokens, noStream,
	)
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}

	// Build health registry
	healthReg := health.NewRegistry(util.Version)
	registerHealthCheckers(healthReg, wukongCfg, bootstrapState)

	// Expose the health registry over HTTP so it is actually reachable
	// (previously it was built but never attached to a listener).
	// /healthz serves the full CheckResult JSON; /livez and /readyz are
	// lightweight k8s-style liveness/readiness probes.
	healthMux := http.NewServeMux()
	// /healthz and /readyz both run the full check (503 when
	// unhealthy); /livez is an always-200 liveness probe.
	healthMux.Handle("/healthz", healthReg.HTTPHandler())
	healthMux.Handle("/readyz", healthReg.HTTPHandler())
	healthMux.Handle("/livez", health.LivenessHandler())
	healthSrv := &http.Server{
		Addr:         ":8086",
		Handler:      healthMux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}
	go func() {
		util.Logger.Info("health: HTTP endpoint starting on :8086",
			slog.String("paths", "/healthz /livez /readyz"))
		if err := healthSrv.ListenAndServe(); err != nil &&
			err != http.ErrServerClosed {
			util.Logger.Warn("health: HTTP server error",
				slog.String("error", err.Error()))
		}
	}()

	// Print startup summary
	printServerStartup(wukongCfg)

	// Set up OS signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)

	// Block until shutdown signal
	sig := <-sigCh
	fmt.Printf("\nReceived signal %v, shutting down gracefully...\n", sig)

	// Graceful shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.Background(), 15*time.Second)
	defer shutdownCancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Stop the health HTTP server first (best-effort).
		_ = healthSrv.Shutdown(shutdownCtx)
		// shutdownBootstrap stops servers AND closes the loop
		// (idempotent via sync.Once).
		_ = shutdownBootstrap(shutdownCtx, bootstrapState, loop)
	}()

	select {
	case <-done:
		fmt.Println("Server stopped.")
	case <-shutdownCtx.Done():
		fmt.Println("Shutdown timed out, forcing exit.")
	}

	return nil
}

// printServerStartup prints the server startup summary with
// all protocol endpoints and their status.
func printServerStartup(cfg *config.WukongConfig) {
	fmt.Println("\n=== Server Endpoints ===")

	if cfg.A2AServer.Enabled {
		addr := cfg.A2AServer.Address
		if addr == "" {
			addr = ":9090"
		}
		fmt.Printf("  ✓ A2A      http://localhost%s  (Agent-to-Agent)\n", addr)
	} else {
		fmt.Println("  - A2A      disabled")
	}

	if cfg.AGUI.Enabled {
		addr := cfg.AGUI.Address
		if addr == "" {
			addr = ":8080"
		}
		path := cfg.AGUI.Path
		if path == "" {
			path = "/agui"
		}
		fmt.Printf("  ✓ AG-UI    http://localhost%s%s  (Web UI SSE)\n",
			addr, path)
	} else {
		fmt.Println("  - AG-UI    disabled")
	}

	if cfg.ACPServer.Enabled {
		addr := cfg.ACPServer.Address
		if addr == "" {
			addr = ":9091"
		}
		path := cfg.ACPServer.Path
		if path == "" {
			path = "/acp"
		}
		fmt.Printf("  ✓ ACP      http://localhost%s%s  (Agent Client)\n",
			addr, path)
	} else {
		fmt.Println("  - ACP      disabled")
	}

	if cfg.ACPMCP.Enabled {
		addr := cfg.ACPMCP.Address
		if addr == "" {
			addr = ":3400"
		}
		path := cfg.ACPMCP.Path
		if path == "" {
			path = "/mcp"
		}
		fmt.Printf("  ✓ ACP MCP  http://localhost%s%s  (Tool Bridge)\n",
			addr, path)
	} else {
		fmt.Println("  - ACP MCP  disabled")
	}

	if cfg.ARD.PublishEnabled {
		fmt.Printf("  ✓ ARD      http://localhost:%d  (Resource Discovery)\n",
			cfg.ARD.PublishPort)
	}

	if cfg.Gateway.Enabled {
		channels := ""
		if cfg.Gateway.Feishu.Enabled {
			channels += " Feishu"
		}
		fmt.Printf("  ✓ Gateway  ws:// outbound  (Messaging:%s)\n",
			channels)
	} else {
		fmt.Println("  - Gateway  disabled")
	}

	fmt.Printf("\nProvider: %s\n", cfg.DefaultProvider)
	fmt.Printf("Model:    %s\n", resolveEffectiveModel(cfg))
	fmt.Println("  Health    http://localhost:8086/healthz  (/livez /readyz)")
	fmt.Println("\nServer is running. Press Ctrl+C to stop.")
}

// resolveEffectiveModel determines the effective model name from config.
func resolveEffectiveModel(cfg *config.WukongConfig) string {
	p := cfg.FindProvider(cfg.DefaultProvider)
	if p != nil && p.Model != "" {
		return p.Model
	}
	return "(not configured)"
}

// registerHealthCheckers registers all subsystem health checkers.
// state provides live handles (e.g. DBPing); cfg drives static config.
func registerHealthCheckers(
	reg *health.Registry,
	cfg *config.WukongConfig,
	state *BootstrapState,
) {
	// Database health — a real ping (no longer a no-op). Falls back to
	// a healthy-with-note result when no DB pool is configured (e.g.
	// pure in-memory test setups).
	dbPing := func(ctx context.Context) error { return nil }
	if state != nil && state.DBPing != nil {
		dbPing = state.DBPing
	}
	reg.Register("database", health.DBChecker("database", dbPing))

	// A2A server health
	if cfg.A2AServer.Enabled {
		reg.Register("a2a_server", health.A2AServerChecker(
			cfg.A2AServer.Enabled, cfg.A2AServer.Address))
	}

	// Session backend
	reg.Register("session", func(ctx context.Context) health.ComponentHealth {
		return health.ComponentHealth{
			Name:    "session",
			Status:  health.StatusHealthy,
			Message: fmt.Sprintf("backend: %s", cfg.Session.Backend),
		}
	})

	// Memory backend
	reg.Register("memory", func(ctx context.Context) health.ComponentHealth {
		return health.ComponentHealth{
			Name:   "memory",
			Status: health.StatusHealthy,
			Message: fmt.Sprintf("backend: %s, auto_extract: %v",
				cfg.Memory.Backend, cfg.Memory.AutoExtract),
		}
	})

	// Gateway health — reports the live running state so operators
	// can tell whether the messaging channels are connected.
	if state != nil && state.GatewayServer != nil {
		reg.Register("gateway", func(ctx context.Context) health.ComponentHealth {
			st := health.StatusHealthy
			msg := "running"
			if !state.GatewayServer.IsRunning() {
				st = health.StatusUnhealthy
				msg = "not running"
			}
			return health.ComponentHealth{
				Name: "gateway", Status: st, Message: msg,
			}
		})
	}
}

// shutdownServers has been replaced by the unified shutdownBootstrap
// (see shutdown.go), which covers all BootstrapState fields including
// ANPServer and is idempotent via sync.Once.
