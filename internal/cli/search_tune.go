// Package cli provides the "wukong search tune" command group for
// search strategy tuning, inspired by volcengine/SearchCLI.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/search"
	"github.com/km269/wukong/internal/search/tune"
	"github.com/km269/wukong/internal/util"
)

// newSearchCmd creates the "wukong search" command group.
func newSearchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search",
		Short: "Search strategy tuning and evaluation",
		Long: `Search strategy tuning commands for optimising retrieval
parameters (recall mode, keyword/semantic weights, candidate size)
using batch evaluation and LLM-based relevance judging.

Inspired by volcengine/SearchCLI's Agent-driven search self-iteration.`,
	}

	cmd.AddCommand(newSearchTuneCmd())

	return cmd
}

func newSearchTuneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tune",
		Short: "Tune search strategies",
		Long: `Tune search retrieval strategies by evaluating candidate
parameter sets against a query dataset.

Workflow:
  validate → plan → run → report → compare → apply

Each step has structured input/output and checkpoints for resume.`,
	}

	cmd.AddCommand(newSearchTuneValidateCmd())
	cmd.AddCommand(newSearchTunePlanCmd())
	cmd.AddCommand(newSearchTuneRunCmd())
	cmd.AddCommand(newSearchTuneReportCmd())
	cmd.AddCommand(newSearchTuneCompareCmd())

	return cmd
}

// ---- validate ----

func newSearchTuneValidateCmd() *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "validate --queries <path>",
		Short: "Validate a query set for tuning",
		Long: `Check query set for empty queries, duplicates, type skew,
and label coverage before spending evaluation budget.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			queriesPath, _ := cmd.Flags().GetString("queries")
			if queriesPath == "" {
				return fmt.Errorf("--queries is required")
			}

			cases, err := search.LoadEvalCases(queriesPath)
			if err != nil {
				return fmt.Errorf("load queries: %w", err)
			}

			report := search.ValidateCases(cases)

			if jsonOut {
				data, _ := json.MarshalIndent(report, "", "  ")
				fmt.Println(string(data))
			} else {
				printValidationReport(report)
			}
			return nil
		},
	}

	cmd.Flags().String("queries", "", "Path to JSONL query file")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")

	return cmd
}

// ---- plan ----

func newSearchTunePlanCmd() *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "plan --queries <path> [flags]",
		Short: "Predict tuning budget before running",
		Long: `Calculate the number of search requests, maximum labels,
and estimated LLM calls before executing a tuning run.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			queriesPath, _ := cmd.Flags().GetString("queries")
			if queriesPath == "" {
				return fmt.Errorf("--queries is required")
			}

			cases, err := search.LoadEvalCases(queriesPath)
			if err != nil {
				return fmt.Errorf("load queries: %w", err)
			}

			strategyCount, _ := cmd.Flags().GetInt("strategies")
			topK, _ := cmd.Flags().GetInt("top-k")
			silver, _ := cmd.Flags().GetBool("silver-filter")

			plan := search.ComputePlan(
				strategyCount, len(cases), topK, silver)

			if jsonOut {
				data, _ := json.MarshalIndent(plan, "", "  ")
				fmt.Println(string(data))
			} else {
				printPlan(plan)
			}
			return nil
		},
	}

	cmd.Flags().String("queries", "", "Path to JSONL query file")
	cmd.Flags().Int("strategies", 8, "Number of candidate strategies")
	cmd.Flags().Int("top-k", 10, "Top-K results per query")
	cmd.Flags().Bool("silver-filter", true, "Enable silver-label fast pass")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")

	return cmd
}

// ---- run ----

func newSearchTuneRunCmd() *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "run --queries <path> [flags]",
		Short: "Execute a tuning run",
		Long: `Execute batch evaluation of candidate strategies against
the query dataset. Supports checkpoint/resume and multi-fidelity
evaluation (fast/middle/confirm passes).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSearchTune(cmd, jsonOut)
		},
	}

	cmd.Flags().String("queries", "", "Path to JSONL query file")
	cmd.Flags().String("config", "", "Path to wukong config file")
	cmd.Flags().String("label-source", "prelabelled",
		"Label source: prelabelled | llm | silver")
	cmd.Flags().String("profile", "similarity-only",
		"Tuning profile: similarity-only")
	cmd.Flags().Int("concurrency", 4, "Parallel search requests")
	cmd.Flags().Int("top-k", 10, "Top-K results per query")
	cmd.Flags().Int("iterations", 3, "Optimiser iterations")
	cmd.Flags().Int("population", 8, "Population size per iteration")
	cmd.Flags().Bool("multi-fidelity", false,
		"Enable three-pass multi-fidelity evaluation")
	cmd.Flags().Bool("resume", false, "Resume an interrupted run")
	cmd.Flags().String("run-id", "", "Run ID (auto-generated if empty)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")

	return cmd
}

// ---- report ----

func newSearchTuneReportCmd() *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "report --run-id <id>",
		Short: "Show tuning run report",
		Long:  `Display the metrics and recommended strategy from a completed run.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			runID, _ := cmd.Flags().GetString("run-id")
			if runID == "" {
				return fmt.Errorf("--run-id is required")
			}

			cfg, err := loadTuneConfig(cmd)
			if err != nil {
				return err
			}

			store, err := tune.NewSQLiteCheckpointStore(
				searchTuneDBPath(cfg))
			if err != nil {
				return fmt.Errorf("open checkpoint: %w", err)
			}
			defer store.Close()

			run, err := store.LoadRun(runID)
			if err != nil {
				return fmt.Errorf("load run: %w", err)
			}
			if run == nil {
				return fmt.Errorf("run %q not found", runID)
			}

			report := tune.GenerateReport(run)

			if jsonOut {
				data, _ := json.MarshalIndent(report, "", "  ")
				fmt.Println(string(data))
			} else {
				printReport(report)
			}
			return nil
		},
	}

	cmd.Flags().String("run-id", "", "Run ID to report")
	cmd.Flags().String("config", "", "Path to wukong config file")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")

	return cmd
}

// ---- compare ----

func newSearchTuneCompareCmd() *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "compare --run-id-a <a> --run-id-b <b>",
		Short: "Compare two tuning runs",
		RunE: func(cmd *cobra.Command, args []string) error {
			runA, _ := cmd.Flags().GetString("run-id-a")
			runB, _ := cmd.Flags().GetString("run-id-b")
			if runA == "" || runB == "" {
				return fmt.Errorf("--run-id-a and --run-id-b are required")
			}

			cfg, err := loadTuneConfig(cmd)
			if err != nil {
				return err
			}

			store, err := tune.NewSQLiteCheckpointStore(
				searchTuneDBPath(cfg))
			if err != nil {
				return fmt.Errorf("open checkpoint: %w", err)
			}
			defer store.Close()

			ra, err := store.LoadRun(runA)
			if err != nil {
				return fmt.Errorf("load run A: %w", err)
			}
			rb, err := store.LoadRun(runB)
			if err != nil {
				return fmt.Errorf("load run B: %w", err)
			}

			if ra == nil || rb == nil {
				return fmt.Errorf("one or both runs not found")
			}

			reportA := tune.GenerateReport(ra)
			reportB := tune.GenerateReport(rb)

			if jsonOut {
				out := map[string]tune.TuneReport{
					"run_a": reportA,
					"run_b": reportB,
				}
				data, _ := json.MarshalIndent(out, "", "  ")
				fmt.Println(string(data))
			} else {
				printComparison(reportA, reportB)
			}
			return nil
		},
	}

	cmd.Flags().String("run-id-a", "", "First run ID")
	cmd.Flags().String("run-id-b", "", "Second run ID")
	cmd.Flags().String("config", "", "Path to wukong config file")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")

	return cmd
}

// ----------------------------------------------------------------------------
// Run execution
// ----------------------------------------------------------------------------

func runSearchTune(cmd *cobra.Command, jsonOut bool) error {
	queriesPath, _ := cmd.Flags().GetString("queries")
	if queriesPath == "" {
		return fmt.Errorf("--queries is required")
	}

	cases, err := search.LoadEvalCases(queriesPath)
	if err != nil {
		return fmt.Errorf("load queries: %w", err)
	}

	// Validate query set.
	valReport := search.ValidateCases(cases)
	if len(valReport.Errors) > 0 {
		return fmt.Errorf("query validation failed: %v",
			valReport.Errors)
	}

	cfg, err := loadTuneConfig(cmd)
	if err != nil {
		return err
	}

	// Create checkpoint store.
	checkpoint, err := tune.NewSQLiteCheckpointStore(
		searchTuneDBPath(cfg))
	if err != nil {
		return fmt.Errorf("open checkpoint: %w", err)
	}
	defer checkpoint.Close()

	labelSource, _ := cmd.Flags().GetString("label-source")
	topK, _ := cmd.Flags().GetInt("top-k")
	concurrency, _ := cmd.Flags().GetInt("concurrency")
	multiFidelity, _ := cmd.Flags().GetBool("multi-fidelity")
	resume, _ := cmd.Flags().GetBool("resume")
	runID, _ := cmd.Flags().GetString("run-id")

	// Generate initial population.
	optCfg := tune.OptimizerConfig{
		Profile:  "similarity-only",
		TopK:     topK,
		Baseline: search.DefaultGenome(),
	}
	if cfg.Cortex.SearchStrategy != nil {
		optCfg.Baseline = search.SearchGenome{
			RecallMode:          cfg.Cortex.SearchStrategy.RecallMode,
			DenseWeight:         cfg.Cortex.SearchStrategy.DenseWeight,
			TextWeight:          cfg.Cortex.SearchStrategy.TextWeight,
			KeywordMatchPercent: cfg.Cortex.SearchStrategy.KeywordMatchPercent,
			MaxRetrievedNum:     cfg.Cortex.SearchStrategy.MaxRetrievedNum,
			FTS5PoolSize:        cfg.Cortex.SearchStrategy.FTS5PoolSize,
		}
	}

	strategies := tune.GenerateInitialPopulation(optCfg)

	util.Logger.Info("search tune: starting",
		"strategies", len(strategies),
		"queries", len(cases),
		"label_source", labelSource,
		"multi_fidelity", multiFidelity)

	// Create searcher (placeholder — needs real store integration).
	// In production, this would create a CortexSearcher or RecallSearcher
	// from the config. For now, we return an error if no backend is available.
	searcher, judge, err := createSearcherAndJudge(cfg, labelSource)
	if err != nil {
		return fmt.Errorf("create searcher: %w", err)
	}

	cache := tune.NewInMemoryLabelCache()
	tuner := tune.NewTuner(
		searcher, judge, cache, checkpoint, util.Logger)

	ctx := context.Background()

	var result *tune.TuneRun
	if multiFidelity {
		mfResult, err := tuner.RunMultiFidelity(
			ctx, strategies, cases,
			tune.MultiFidelityConfig{},
			labelSource,
		)
		if err != nil {
			return fmt.Errorf("multi-fidelity run: %w", err)
		}
		result = mfResult.ConfirmPass
	} else {
		result, err = tuner.Run(ctx, tune.TuneRequest{
			RunID:       runID,
			Strategies:  strategies,
			Cases:       cases,
			TopK:        topK,
			Concurrency: concurrency,
			LabelSource: labelSource,
			Resume:      resume,
		})
		if err != nil {
			return fmt.Errorf("run: %w", err)
		}
	}

	report := tune.GenerateReport(result)

	if jsonOut {
		data, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(data))
	} else {
		printReport(report)
	}
	return nil
}

// createSearcherAndJudge creates a searcher and judge from config.
// This is a simplified version — in production, it would create
// proper CortexStore/RecallStore-backed searchers.
func createSearcherAndJudge(
	cfg *config.WukongConfig,
	labelSource string,
) (tune.Searcher, tune.Judge, error) {
	// For prelabelled mode, no judge is needed.
	if labelSource == "prelabelled" {
		// Return a nil searcher — caller should provide one.
		// This is a placeholder; real implementation would create
		// a CortexSearcher from the config.
		return nil, nil, fmt.Errorf(
			"searcher creation requires runtime store " +
				"(use 'wukong search tune run' from within a session)")
	}

	// For LLM mode, create LLM Judge.
	if labelSource == "llm" {
		dp := cfg.DefaultProviderConfig()
		if dp == nil {
			return nil, nil, fmt.Errorf(
				"no provider configured for LLM judge")
		}
		// Judge creation would need provider.Factory.
		// This is a placeholder.
		return nil, nil, fmt.Errorf(
			"LLM judge requires provider factory " +
				"(use 'wukong search tune run' from within a session)")
	}

	// Silver mode: no judge needed.
	return nil, nil, nil
}

// ----------------------------------------------------------------------------
// Config helper
// ----------------------------------------------------------------------------

func loadTuneConfig(
	cmd *cobra.Command,
) (*config.WukongConfig, error) {
	configPath, _ := cmd.Flags().GetString("config")
	loader, err := config.NewLoader(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	cfg, err := loader.Load()
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}

// SearchTuneDBPath returns the SQLite path for tune checkpoints.
func searchTuneDBPath(cfg *config.WukongConfig) string {
	// Use the same directory as the cortex DB.
	dir := filepath.Dir(
		config.ResolvePath(cfg.Cortex.DBPath))
	return filepath.Join(dir, "search_tune.db")
}

// ----------------------------------------------------------------------------
// Print helpers
// ----------------------------------------------------------------------------

func printValidationReport(r search.ValidationReport) {
	fmt.Println("══════════════════════════════════════")
	fmt.Println("  Query Set Validation Report")
	fmt.Println("══════════════════════════════════════")
	fmt.Printf("  Total cases:     %d\n", r.TotalCases)
	fmt.Printf("  Valid cases:     %d\n", r.ValidCases)
	fmt.Printf("  Duplicates:      %d\n", r.DuplicateQueries)
	fmt.Printf("  Empty queries:   %d\n", r.EmptyQueries)
	fmt.Printf("  Label coverage:  %.1f%%\n", r.LabelCoverage*100)
	fmt.Printf("  Source coverage: %.1f%%\n", r.SourceCoverage*100)
	fmt.Println()
	fmt.Println("  Type distribution:")
	for typ, count := range r.TypeDistribution {
		fmt.Printf("    %s: %d\n", typ, count)
	}
	if len(r.Warnings) > 0 {
		fmt.Println("\n  Warnings:")
		for _, w := range r.Warnings {
			fmt.Printf("    ! %s\n", w)
		}
	}
	if len(r.Errors) > 0 {
		fmt.Println("\n  Errors:")
		for _, e := range r.Errors {
			fmt.Printf("    X %s\n", e)
		}
	}
	fmt.Println("══════════════════════════════════════")
}

func printPlan(p search.TunePlan) {
	fmt.Println("══════════════════════════════════════")
	fmt.Println("  Tune Budget Plan")
	fmt.Println("══════════════════════════════════════")
	fmt.Printf("  Strategies:        %d\n", p.StrategyCount)
	fmt.Printf("  Queries:           %d\n", p.QueryCount)
	fmt.Printf("  Search requests:   %d\n", p.SearchRequests)
	fmt.Printf("  Max labels:        %d\n", p.MaxLabels)
	fmt.Printf("  Top-K:             %d\n", p.TopK)
	fmt.Printf("  Est. LLM calls:    %d\n", p.EstimatedLLMCalls)
	fmt.Println("══════════════════════════════════════")
}

func printReport(r tune.TuneReport) {
	fmt.Println("══════════════════════════════════════")
	fmt.Println("  Tune Run Report")
	fmt.Println("══════════════════════════════════════")
	fmt.Printf("  Run ID:          %s\n", r.RunID)
	fmt.Printf("  Total searches:  %d\n", r.TotalSearches)
	fmt.Printf("  Total queries:   %d\n", r.TotalQueries)
	fmt.Printf("  Duration:        %s\n", r.Duration)
	fmt.Printf("  Best score:      %.4f\n", r.BestScore)
	fmt.Printf("  Improvement:     %.1f%%\n", r.Improvement*100)
	fmt.Println()
	fmt.Println("  Best strategy:")
	fmt.Printf("    recall_mode:          %s\n", r.BestStrategy.RecallMode)
	fmt.Printf("    dense_weight:         %.2f\n", r.BestStrategy.DenseWeight)
	fmt.Printf("    text_weight:          %.2f\n", r.BestStrategy.TextWeight)
	fmt.Printf("    keyword_match_pct:    %.1f\n", r.BestStrategy.KeywordMatchPercent)
	fmt.Printf("    max_retrieved_num:    %d\n", r.BestStrategy.MaxRetrievedNum)
	fmt.Printf("    fts5_pool_size:       %d\n", r.BestStrategy.FTS5PoolSize)
	fmt.Println()
	fmt.Println("  All strategies:")
	fmt.Printf("    %-6s %-8s %-8s %-8s %-8s %-8s %-8s\n",
		"Idx", "NDCG20", "NDCG10", "MRR10", "P@10", "ZeroR", "Lat(ms)")
	for i, m := range r.AllMetrics {
		fmt.Printf("    %-6d %.4f   %.4f   %.4f   %.4f   %.2f    %.0f\n",
			i, m.NDCG20, m.NDCG10, m.MRR10,
			m.Precision10, m.ZeroResultRate, m.AvgLatencyMs)
	}
	fmt.Println("══════════════════════════════════════")
}

func printComparison(a, b tune.TuneReport) {
	fmt.Println("══════════════════════════════════════")
	fmt.Println("  Run Comparison")
	fmt.Println("══════════════════════════════════════")
	fmt.Printf("  Run A: %s\n", a.RunID)
	fmt.Printf("  Run B: %s\n", b.RunID)
	fmt.Println()
	fmt.Printf("  %-20s %-12s %-12s\n", "Metric", "Run A", "Run B")
	fmt.Printf("  %-20s %-12.4f %-12.4f\n", "Best Score", a.BestScore, b.BestScore)
	fmt.Printf("  %-20s %-12.1f%% %-12.1f%%\n", "Improvement", a.Improvement*100, b.Improvement*100)
	if len(a.AllMetrics) > 0 && len(b.AllMetrics) > 0 {
		ma := a.AllMetrics[0]
		mb := b.AllMetrics[0]
		fmt.Printf("  %-20s %-12.4f %-12.4f\n", "NDCG@20", ma.NDCG20, mb.NDCG20)
		fmt.Printf("  %-20s %-12.4f %-12.4f\n", "NDCG@10", ma.NDCG10, mb.NDCG10)
		fmt.Printf("  %-20s %-12.4f %-12.4f\n", "MRR@10", ma.MRR10, mb.MRR10)
		fmt.Printf("  %-20s %-12.4f %-12.4f\n", "Precision@10", ma.Precision10, mb.Precision10)
		fmt.Printf("  %-20s %-12.2f %-12.2f\n", "Zero Result Rate", ma.ZeroResultRate, mb.ZeroResultRate)
		fmt.Printf("  %-20s %-12.0f %-12.0f\n", "Avg Latency (ms)", ma.AvgLatencyMs, mb.AvgLatencyMs)
	}
	fmt.Println("══════════════════════════════════════")
}
