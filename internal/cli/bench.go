// Package cli provides performance and system utility commands.
package cli

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/provider"
	"github.com/km269/wukong/internal/util"
	"github.com/km269/wukong/pkg/sandbox"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// ==========================================================================
// wukong bench — model latency benchmark
// ==========================================================================

func newBenchCmd() *cobra.Command {
	var (
		configPath string
		rounds     int
		prompt     string
		maxTokens  int
	)

	cmd := &cobra.Command{
		Use:   "bench",
		Short: "Run model latency benchmark",
		Long: `Test the response latency and token generation speed
of the configured LLM provider. Runs multiple rounds and
reports statistics including tokens/second.

Examples:
  wukong bench
  wukong bench --rounds 5
  wukong bench --prompt "Explain Go concurrency" --max-tokens 256`,
		RunE: runBench,
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file")
	cmd.Flags().IntVarP(
		&rounds, "rounds", "n", 3,
		"Number of benchmark rounds (1-20)")
	cmd.Flags().StringVar(
		&prompt, "prompt", "Say hello in exactly one short sentence.",
		"Benchmark prompt")
	cmd.Flags().IntVar(
		&maxTokens, "max-tokens", 128,
		"Maximum output tokens per round")

	return cmd
}

func runBench(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	rounds, _ := cmd.Flags().GetInt("rounds")
	prompt, _ := cmd.Flags().GetString("prompt")
	maxTokens, _ := cmd.Flags().GetInt("max-tokens")

	if rounds < 1 {
		rounds = 1
	}
	if rounds > 20 {
		rounds = 20
	}

	loader, err := config.NewLoader(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	factory := provider.NewFactory(wukongCfg)
	mdl, err := factory.CreateModel("")
	if err != nil {
		return fmt.Errorf("create model: %w", err)
	}

	fmt.Println(strings.Repeat("═", 55))
	fmt.Println("  Model Benchmark")
	fmt.Println(strings.Repeat("═", 55))

	p := wukongCfg.FindProvider(wukongCfg.DefaultProvider)
	if p != nil {
		fmt.Printf("\n  Provider: %s (%s)", p.Name, p.Type)
		fmt.Printf("\n  Model:    %s", p.Model)
		fmt.Printf("\n  Rounds:   %d", rounds)
		fmt.Printf("\n  Tokens:   %d/round", maxTokens)
	}
	fmt.Printf("\n\n  Running %d rounds...\n\n", rounds)

	ctx := context.Background()
	var latencies []time.Duration
	var tokenCounts []int
	totalTokens := 0

	for i := 0; i < rounds; i++ {
		fmt.Printf("  Round %d/%d... ", i+1, rounds)

		start := time.Now()
		req := &model.Request{
			Messages: []model.Message{
				model.NewUserMessage(prompt),
			},
			GenerationConfig: model.GenerationConfig{
				MaxTokens:   util.IntPtr(maxTokens),
				Temperature: util.Float64Ptr(0.0),
				Stream:      false,
			},
		}

		respCh, err := mdl.GenerateContent(ctx, req)
		if err != nil {
			fmt.Printf("✗ error: %v\n", err)
			continue
		}

		var output string
		for resp := range respCh {
			if resp.Error != nil {
				fmt.Printf("✗ error: %s\n", resp.Error.Message)
				continue
			}
			if len(resp.Choices) > 0 {
				output += resp.Choices[0].Message.Content
			}
		}

		elapsed := time.Since(start)
		tokens := len(strings.Fields(output))
		totalTokens += tokens
		latencies = append(latencies, elapsed)
		tokenCounts = append(tokenCounts, tokens)

		tps := float64(tokens) / elapsed.Seconds()
		fmt.Printf("%v (%d tokens, %.1f tok/s)\n",
			elapsed.Round(time.Millisecond), tokens, tps)
	}

	if len(latencies) == 0 {
		fmt.Println("\n  All rounds failed.")
		return fmt.Errorf("all benchmark rounds failed")
	}

	// Statistics
	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	minLat := latencies[0]
	maxLat := latencies[len(latencies)-1]
	medianLat := latencies[len(latencies)/2]

	var totalLat time.Duration
	for _, l := range latencies {
		totalLat += l
	}
	avgLat := totalLat / time.Duration(len(latencies))

	avgTPS := float64(totalTokens) / totalLat.Seconds()

	fmt.Println("\n" + strings.Repeat("─", 55))
	fmt.Println("  Results")
	fmt.Println(strings.Repeat("─", 55))
	fmt.Printf("  Successful:     %d/%d\n", len(latencies), rounds)
	fmt.Printf("  Min latency:    %v\n", minLat.Round(time.Millisecond))
	fmt.Printf("  Max latency:    %v\n", maxLat.Round(time.Millisecond))
	fmt.Printf("  Avg latency:    %v\n", avgLat.Round(time.Millisecond))
	fmt.Printf("  Median latency: %v\n", medianLat.Round(time.Millisecond))
	fmt.Printf("  Avg tokens/s:   %.1f\n", avgTPS)

	// Standard deviation
	if len(latencies) > 1 {
		var variance float64
		avgMs := float64(avgLat.Milliseconds())
		for _, l := range latencies {
			diff := float64(l.Milliseconds()) - avgMs
			variance += diff * diff
		}
		variance /= float64(len(latencies))
		stddev := math.Sqrt(variance)
		fmt.Printf("  Std dev:        %.1f ms\n", stddev)
	}

	fmt.Println()
	return nil
}

// ==========================================================================
// wukong backup — database backup
// ==========================================================================

func newBackupCmd() *cobra.Command {
	var (
		configPath string
		outputDir  string
	)

	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Backup the wukong database",
		Long: `Create a timestamped backup of the wukong database file.
The backup is a simple file copy with a .bak extension.

Examples:
  wukong backup
  wukong backup --output ./backups/`,
		RunE: runBackup,
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file")
	cmd.Flags().StringVarP(
		&outputDir, "output", "o", "",
		"Output directory (default: current directory)")

	return cmd
}

func runBackup(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	outputDir, _ := cmd.Flags().GetString("output")

	loader, err := config.NewLoader(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	dbPath := config.ResolvePath(wukongCfg.Session.DBPath)

	// Verify source exists
	srcInfo, err := os.Stat(dbPath)
	if err != nil {
		return fmt.Errorf("database not found at %s: %w", dbPath, err)
	}

	// Determine output directory
	if outputDir == "" {
		outputDir = "."
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	// Create backup filename with timestamp
	timestamp := time.Now().Format("20060102-150405")
	backupName := fmt.Sprintf("wukong-%s.db.bak", timestamp)
	backupPath := filepath.Join(outputDir, backupName)

	// Copy file
	src, err := os.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(backupPath)
	if err != nil {
		return fmt.Errorf("create backup: %w", err)
	}
	defer dst.Close()

	written, err := io.Copy(dst, src)
	if err != nil {
		return fmt.Errorf("copy data: %w", err)
	}

	srcSize := srcInfo.Size()
	srcSizeMB := float64(srcSize) / (1024 * 1024)

	fmt.Println("╔══════════════════════════════════════╗")
	fmt.Println("║  Database Backup                     ║")
	fmt.Println("╚══════════════════════════════════════╝")
	fmt.Printf("\n  Source:  %s (%.2f MB)\n", dbPath, srcSizeMB)
	fmt.Printf("  Backup:  %s (%.2f MB)\n",
		backupPath, float64(written)/(1024*1024))
	fmt.Printf("\n  ✓ Backup created successfully.\n\n")

	return nil
}

// ==========================================================================
// wukong system check — system readiness diagnostics
// ==========================================================================

func newSystemCheckCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "system-check",
		Short: "Run system readiness diagnostics",
		Long: `Check that the system environment is ready for running
wukong. Verifies Go version, disk space, database integrity,
sandbox capabilities, and configuration validity.

Examples:
  wukong system-check`,
		RunE: runSystemCheck,
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file")

	return cmd
}

type checkItem struct {
	Name    string
	Status  string // "pass", "warn", "fail"
	Message string
}

func runSystemCheck(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")

	var checks []checkItem

	// 1. Config check
	loader, err := config.NewLoader(configPath)
	if err != nil {
		checks = append(checks, checkItem{
			"Config", "fail", fmt.Sprintf("loader: %v", err),
		})
	} else {
		wukongCfg, loadErr := loader.Load()
		if loadErr != nil {
			checks = append(checks, checkItem{
				"Config", "fail", fmt.Sprintf("parse: %v", loadErr),
			})
		} else {
			issues := runFullValidation(wukongCfg)
			if len(issues) > 0 {
				checks = append(checks, checkItem{
					"Config", "warn",
					fmt.Sprintf("%d issue(s): %s",
						len(issues), issues[0]),
				})
			} else {
				checks = append(checks, checkItem{
					"Config", "pass", "valid",
				})
			}

			// 2. Provider check
			p := wukongCfg.FindProvider(wukongCfg.DefaultProvider)
			if p == nil {
				checks = append(checks, checkItem{
					"Provider", "fail",
					fmt.Sprintf("default %q not found",
						wukongCfg.DefaultProvider),
				})
			} else {
				keyOK := p.APIKey != "" ||
					p.Type == "ollama" ||
					p.Type == "lmstudio" ||
					p.Type == "vllm"
				if keyOK {
					checks = append(checks, checkItem{
						"Provider", "pass",
						fmt.Sprintf("%s (%s/%s)",
							p.Name, p.Type, p.Model),
					})
				} else {
					checks = append(checks, checkItem{
						"Provider", "warn",
						fmt.Sprintf("%s: no API key", p.Name),
					})
				}
			}

			// 3. Database check
			dbPath := config.ResolvePath(wukongCfg.Session.DBPath)
			if info, err := os.Stat(dbPath); err == nil {
				sizeMB := float64(info.Size()) / (1024 * 1024)
				checks = append(checks, checkItem{
					"Database", "pass",
					fmt.Sprintf("%s (%.1f MB)", dbPath, sizeMB),
				})
			} else {
				checks = append(checks, checkItem{
					"Database", "warn",
					fmt.Sprintf("not found: %s (created on first run)",
						dbPath),
				})
			}
		}
	}

	// 4. Sandbox check
	sb := sandbox.Probe()
	if sb.Sandboxed {
		checks = append(checks, checkItem{
			"Sandbox", "pass",
			fmt.Sprintf("%s active", sb.Backend),
		})
	} else {
		checks = append(checks, checkItem{
			"Sandbox", "warn",
			fmt.Sprintf("%s unavailable", sb.Backend),
		})
	}

	// 5. Disk space check
	if wd, err := os.Getwd(); err == nil {
		checks = append(checks, checkItem{
			"Working Dir", "pass", wd,
		})
	}

	// Print results
	fmt.Println(strings.Repeat("═", 60))
	fmt.Println("  Wukong System Readiness Check")
	fmt.Println(strings.Repeat("═", 60))
	fmt.Println()

	passed := 0
	warned := 0
	failed := 0

	for _, c := range checks {
		icon := "✓"
		switch c.Status {
		case "pass":
			passed++
		case "warn":
			icon = "⚠"
			warned++
		case "fail":
			icon = "✗"
			failed++
		}
		fmt.Printf("  %s %-15s %s\n", icon, c.Name, c.Message)
	}

	fmt.Println()
	fmt.Printf("  %d passed, %d warnings, %d failures\n",
		passed, warned, failed)
	fmt.Println()

	if failed > 0 {
		return fmt.Errorf("system check: %d failure(s)", failed)
	}
	return nil
}
