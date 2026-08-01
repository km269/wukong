// Package cli provides the "wukong health" command for system
// health inspection and diagnostics.
package cli

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/health"
	"github.com/km269/wukong/pkg/sandbox"
)

// newHealthCmd creates the "wukong health" command for system
// health status reporting.
func newHealthCmd() *cobra.Command {
	var (
		configPath string
		jsonOutput bool
	)

	cmd := &cobra.Command{
		Use:   "health",
		Short: "Check system health and component status",
		Long: `Run health checks on all configured subsystems and report
their status. Useful for monitoring, debugging, and integration
with external monitoring tools.

Exits with code 0 when all components are healthy,
code 1 when any component is degraded or unhealthy.

Examples:
  wukong health
  wukong health --json    # JSON output for monitoring
  wukong health --config ./my-config.yaml`,
		RunE: runHealth,
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file (default: auto-discover)")
	cmd.Flags().BoolVar(
		&jsonOutput, "json", false,
		"Output health status as JSON")

	return cmd
}

func runHealth(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	jsonOutput, _ := cmd.Flags().GetBool("json")

	// Collect system info
	sysInfo := collectSystemInfo()

	// Try to load config if available
	reg := health.NewRegistry(Version)
	registerSystemHealth(reg, sysInfo)

	// Load config and register config-dependent checkers
	loader, err := config.NewLoader(configPath)
	if err == nil {
		wukongCfg, loadErr := loader.Load()
		if loadErr == nil {
			registerConfigHealth(reg, wukongCfg)
		} else {
			reg.Register("config", func(ctx context.Context) health.ComponentHealth {
				return health.ComponentHealth{
					Name:    "config",
					Status:  health.StatusUnhealthy,
					Message: fmt.Sprintf("config parse error: %v", loadErr),
				}
			})
		}
	} else {
		reg.Register("config", func(ctx context.Context) health.ComponentHealth {
			return health.ComponentHealth{
				Name:    "config",
				Status:  health.StatusDegraded,
				Message: fmt.Sprintf("config loader error: %v", err),
			}
		})
	}

	// Run checks
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result := reg.Check(ctx)

	if jsonOutput {
		printHealthJSON(result)
	} else {
		printHealthTable(result, sysInfo)
	}

	// Exit code based on health
	if result.Status != health.StatusHealthy {
		return fmt.Errorf("system health: %s", result.Status)
	}
	return nil
}

// systemInfo holds basic system and environment information.
type systemInfo struct {
	OS        string
	Arch      string
	GoVersion string
	CPUs      int
	Sandbox   string
	Version   string
}

func collectSystemInfo() systemInfo {
	sb := sandbox.Probe()
	sandboxStatus := sb.Backend
	if !sb.Sandboxed {
		sandboxStatus = sb.Backend + " (unsandboxed: " + sb.Warning + ")"
	}

	return systemInfo{
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		GoVersion: runtime.Version(),
		CPUs:      runtime.NumCPU(),
		Sandbox:   sandboxStatus,
		Version:   Version,
	}
}

// registerSystemHealth registers checks for system-level components
// that do not require configuration.
func registerSystemHealth(reg *health.Registry, info systemInfo) {
	reg.Register("platform", func(ctx context.Context) health.ComponentHealth {
		return health.ComponentHealth{
			Name:    "platform",
			Status:  health.StatusHealthy,
			Message: fmt.Sprintf("%s/%s, %d CPUs, Go %s",
				info.OS, info.Arch, info.CPUs, info.GoVersion),
		}
	})

	reg.Register("sandbox", func(ctx context.Context) health.ComponentHealth {
		sb := sandbox.Probe()
		if sb.Sandboxed {
			return health.ComponentHealth{
				Name:    "sandbox",
				Status:  health.StatusHealthy,
				Message: fmt.Sprintf("active (%s)", sb.Backend),
			}
		}
		return health.ComponentHealth{
			Name:    "sandbox",
			Status:  health.StatusDegraded,
			Message: fmt.Sprintf("%s: %s", sb.Backend, sb.Warning),
		}
	})
}

// registerConfigHealth registers checks that depend on the loaded
// configuration.
func registerConfigHealth(reg *health.Registry, cfg *config.WukongConfig) {
	// Config file status
	reg.Register("config", func(ctx context.Context) health.ComponentHealth {
		issues := runFullValidation(cfg)
		if len(issues) > 0 {
			// Report first issue as message
			return health.ComponentHealth{
				Name:    "config",
				Status:  health.StatusDegraded,
				Message: fmt.Sprintf("%d issue(s): %s", len(issues), issues[0]),
			}
		}
		return health.ComponentHealth{
			Name:    "config",
			Status:  health.StatusHealthy,
			Message: fmt.Sprintf("provider=%s, log_level=%s",
				cfg.DefaultProvider, cfg.LogLevel),
		}
	})

	// Default provider
	p := cfg.FindProvider(cfg.DefaultProvider)
	if p != nil {
		reg.Register("provider:"+cfg.DefaultProvider, func(ctx context.Context) health.ComponentHealth {
			status := health.StatusHealthy
			msg := fmt.Sprintf("type=%s, model=%s", p.Type, p.Model)
			if p.APIKey == "" && p.Type != "ollama" && p.Type != "lmstudio" && p.Type != "vllm" {
				status = health.StatusDegraded
				msg += " (no API key)"
			}
			return health.ComponentHealth{
				Name:    "provider:" + cfg.DefaultProvider,
				Status:  status,
				Message: msg,
			}
		})
	}

	// Session backend
	reg.Register("session", func(ctx context.Context) health.ComponentHealth {
		return health.ComponentHealth{
			Name:    "session",
			Status:  health.StatusHealthy,
			Message: fmt.Sprintf("backend=%s, path=%s",
				cfg.Session.Backend, cfg.Session.DBPath),
		}
	})

	// Memory backend
	reg.Register("memory", func(ctx context.Context) health.ComponentHealth {
		msg := fmt.Sprintf("backend=%s, auto_extract=%v, max=%d",
			cfg.Memory.Backend, cfg.Memory.AutoExtract, cfg.Memory.MaxMemories)
		return health.ComponentHealth{
			Name:    "memory",
			Status:  health.StatusHealthy,
			Message: msg,
		}
	})

	// CortexDB status
	if cfg.Cortex.Enabled {
		reg.Register("cortex", func(ctx context.Context) health.ComponentHealth {
			msg := "HNSW+FTS5 enabled"
			if cfg.Cortex.EmbeddingModel == "" {
				msg += " (no embedding model configured — FTS5 only)"
			}
			return health.ComponentHealth{
				Name:    "cortex",
				Status:  health.StatusHealthy,
				Message: msg,
			}
		})
	}

	// Protocol servers status
	reg.Register("servers", func(ctx context.Context) health.ComponentHealth {
		var endpoints []string
		enabled := 0
		if cfg.A2AServer.Enabled {
			enabled++
			endpoints = append(endpoints, "A2A")
		}
		if cfg.AGUI.Enabled {
			enabled++
			endpoints = append(endpoints, "AG-UI")
		}
		if cfg.ACPServer.Enabled {
			enabled++
			endpoints = append(endpoints, "ACP")
		}
		if cfg.ACPMCP.Enabled {
			enabled++
			endpoints = append(endpoints, "ACP MCP")
		}
		return health.ComponentHealth{
			Name:    "servers",
			Status:  health.StatusHealthy,
			Message: fmt.Sprintf("%d active: %s", enabled,
				strings.Join(endpoints, ", ")),
		}
	})

	// Security
	reg.Register("security", func(ctx context.Context) health.ComponentHealth {
		return health.ComponentHealth{
			Name:    "security",
			Status:  health.StatusHealthy,
			Message: fmt.Sprintf("mode=%s, guardrail=%v",
				cfg.Security.PermissionMode, cfg.Security.GuardrailEnabled),
		}
	})

	// Evolution
	if cfg.Evolution.Enabled {
		reg.Register("evolution", func(ctx context.Context) health.ComponentHealth {
			return health.ComponentHealth{
				Name:    "evolution",
				Status:  health.StatusHealthy,
				Message: fmt.Sprintf("min_confidence=%.1f, cooldown=%s",
					cfg.Evolution.MinConfidence, cfg.Evolution.CooldownPeriod),
			}
		})
	}
}

// printHealthTable prints the health check results as a formatted
// table with color-coded status indicators.
func printHealthTable(result health.CheckResult, info systemInfo) {
	barLen := 60

	fmt.Println(strings.Repeat("─", barLen))
	fmt.Printf("  Wukong Health Check  v%s\n", info.Version)
	fmt.Println(strings.Repeat("─", barLen))
	fmt.Printf("  Platform:  %s/%s  Go %s  %d CPUs\n",
		info.OS, info.Arch, info.GoVersion, info.CPUs)
	fmt.Printf("  Uptime:    %s\n", result.Uptime)
	fmt.Printf("  Timestamp: %s\n", result.Timestamp.Format("2006-01-02 15:04:05"))
	fmt.Println(strings.Repeat("─", barLen))

	// Overall status
	overallIcon := statusIcon(result.Status)
	fmt.Printf("\n  Overall Status: %s %s\n\n", overallIcon, result.Status)

	// Component table
	fmt.Printf("  %-30s %-10s %s\n", "COMPONENT", "STATUS", "MESSAGE")
	fmt.Println("  " + strings.Repeat("─", barLen-2))

	for _, comp := range result.Components {
		icon := statusIcon(comp.Status)
		fmt.Printf("  %-30s %s %-8s %s\n",
			comp.Name, icon, comp.Status, comp.Message)
	}

	fmt.Println(strings.Repeat("─", barLen))

	// Show total
	healthy := 0
	degraded := 0
	unhealthy := 0
	for _, comp := range result.Components {
		switch comp.Status {
		case health.StatusHealthy:
			healthy++
		case health.StatusDegraded:
			degraded++
		case health.StatusUnhealthy:
			unhealthy++
		}
	}

	fmt.Printf("  Total: %d components (%d healthy, %d degraded, %d unhealthy)\n\n",
		len(result.Components), healthy, degraded, unhealthy)
}

func statusIcon(s health.Status) string {
	switch s {
	case health.StatusHealthy:
		return "✓"
	case health.StatusDegraded:
		return "⚠"
	case health.StatusUnhealthy:
		return "✗"
	default:
		return "?"
	}
}

// printHealthJSON prints the health check results as indented JSON.
func printHealthJSON(result health.CheckResult) {
	// Use simple fmt for JSON output to avoid importing encoding/json
	// when we already have the structured result.
	fmt.Printf("{\n")
	fmt.Printf("  \"status\": \"%s\",\n", result.Status)
	fmt.Printf("  \"version\": \"%s\",\n", result.Version)
	fmt.Printf("  \"uptime\": \"%s\",\n", result.Uptime)
	fmt.Printf("  \"timestamp\": \"%s\",\n",
		result.Timestamp.Format(time.RFC3339))
	fmt.Printf("  \"components\": [\n")
	for i, comp := range result.Components {
		comma := ","
		if i == len(result.Components)-1 {
			comma = ""
		}
		msg := comp.Message
		if msg == "" {
			msg = "-"
		}
		fmt.Printf("    {\n")
		fmt.Printf("      \"name\": \"%s\",\n", comp.Name)
		fmt.Printf("      \"status\": \"%s\",\n", comp.Status)
		fmt.Printf("      \"message\": \"%s\",\n", msg)
		fmt.Printf("      \"latency_ms\": %d\n", comp.LatencyMs)
		fmt.Printf("    }%s\n", comma)
	}
	fmt.Printf("  ]\n")
	fmt.Printf("}\n")
}
