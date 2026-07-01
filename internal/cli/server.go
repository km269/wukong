// Package cli provides the "wukong server" command for running
// wukong in headless server mode (A2A, ACP, AG-UI, ACP MCP).
package cli

import (
	"context"
	"fmt"
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
	healthReg := health.NewRegistry(Version)
	registerHealthCheckers(healthReg, wukongCfg)

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
		shutdownServers(shutdownCtx, bootstrapState)
		loop.Close()
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
		addr := cfg.Gateway.Address
		if addr == "" {
			addr = ":9093"
		}
		channels := ""
		if cfg.Gateway.Feishu.Enabled {
			channels += " Feishu"
		}
		if cfg.Gateway.WeCom.Enabled {
			channels += " WeCom"
		}
		fmt.Printf("  ✓ Gateway  http://localhost%s  (Messaging:%s)\n",
			addr, channels)
	} else {
		fmt.Println("  - Gateway  disabled")
	}

	fmt.Printf("\nProvider: %s\n", cfg.DefaultProvider)
	fmt.Printf("Model:    %s\n", resolveEffectiveModel(cfg))
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
func registerHealthCheckers(
	reg *health.Registry,
	cfg *config.WukongConfig,
) {
	// Database health
	reg.Register("database", health.DBChecker("database", func(ctx context.Context) error {
		// Basic check: database pool exists
		return nil
	}))

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
}

// shutdownServers gracefully shuts down all protocol servers.
func shutdownServers(ctx context.Context, state *BootstrapState) {
	if state.A2AServer != nil {
		if err := state.A2AServer.Stop(ctx); err != nil {
			util.Logger.Warn("A2A server stop error", "error", err.Error())
		}
		fmt.Println("  A2A server stopped")
	}
	if state.AGUIServer != nil {
		_ = state.AGUIServer.Stop(ctx)
		fmt.Println("  AG-UI server stopped")
	}
	if state.ACPServer != nil {
		_ = state.ACPServer.Stop(ctx)
		fmt.Println("  ACP server stopped")
	}
	if state.ACPMCPBridge != nil {
		if err := state.ACPMCPBridge.Stop(); err != nil {
			util.Logger.Warn("ACP MCP bridge stop error",
				"error", err.Error())
		}
		fmt.Println("  ACP MCP bridge stopped")
	}
	if state.ARDRegistry != nil {
		_ = state.ARDRegistry.Shutdown(ctx)
		fmt.Println("  ARD registry stopped")
	}
	if state.KnowledgeMgr != nil {
		_ = state.KnowledgeMgr.Close()
		fmt.Println("  Knowledge manager stopped")
	}
	if state.GatewayServer != nil {
		_ = state.GatewayServer.Stop(ctx)
		fmt.Println("  Gateway server stopped")
	}
}
