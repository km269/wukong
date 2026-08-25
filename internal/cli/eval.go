package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/eval"
	"github.com/km269/wukong/internal/util"
)

func newEvalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Run agent evaluation tests",
		Long: `Run evaluation tests against the configured agent to check
for regression in behavior, tool usage, and response quality.

Examples:
  wukong eval                              # Run default evalset
  wukong eval --evalset my_evals.json     # Custom evalset
  wukong eval --results output.json        # Custom results path`,
		RunE: runEval,
	}

	cmd.Flags().StringP("config", "c", "",
		"Path to config file")
	cmd.Flags().String("evalset", "",
		"Path to evalset JSON file (overrides config)")
	cmd.Flags().String("results", "",
		"Path to write results JSON (overrides config)")
	cmd.Flags().StringP("provider", "p", "",
		"Model provider to use")

	return cmd
}

func runEval(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	evalsetPath, _ := cmd.Flags().GetString("evalset")
	resultsPath, _ := cmd.Flags().GetString("results")

	// Load configuration.
	loader, err := config.NewLoader(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Determine evalset path.
	if evalsetPath == "" {
		evalsetPath = wukongCfg.Eval.EvalSetPath
	}
	if evalsetPath == "" {
		evalsetPath = ".wukong/evals/default.evalset.json"
	}

	// Load evalset.
	evalSet, err := eval.LoadEvalSet(evalsetPath)
	if err != nil {
		return fmt.Errorf("load evalset %q: %w", evalsetPath, err)
	}

	fmt.Printf("Evaluation set: %s v%s\n", evalSet.Name, evalSet.Version)
	fmt.Printf("Test cases: %d\n\n", len(evalSet.TestCases))

	// Bootstrap session to get runner (minimal bootstrap).
	providerOverride, _ := cmd.Flags().GetString("provider")
	_, loop, _, err := bootstrapSession(
		configPath, "eval-user", "eval-session",
		providerOverride, "", 0, 0, false,
	)
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	defer loop.Close()

	// Build metrics from config.
	metrics := buildMetrics(wukongCfg)

	// Run evaluation.
	evaluator := eval.NewEvaluator(loop.GetRunner(), metrics)
	results, err := evaluator.Run(context.Background(), evalSet)
	if err != nil {
		return fmt.Errorf("evaluation failed: %w", err)
	}

	// Print summary.
	fmt.Println(evaluator.Summary())

	// Save results.
	if resultsPath == "" {
		resultsPath = wukongCfg.Eval.ResultsPath
	}
	if resultsPath == "" {
		resultsPath = ".wukong/evals/results.json"
	}

	if err := evaluator.SaveResults(resultsPath); err != nil {
		util.Logger.Warn("failed to save results",
			"path", resultsPath,
			"error", err.Error())
	} else {
		fmt.Printf("\nResults saved to: %s\n", resultsPath)
	}

	// Count failures.
	failures := 0
	for _, r := range results {
		if !r.Passed {
			failures++
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d of %d eval cases failed", failures, len(results))
	}
	return nil
}

// buildMetrics converts config MetricConfigs to eval.EvalMetric.
func buildMetrics(cfg any) []eval.EvalMetric {
	// Default metrics if not configured.
	return []eval.EvalMetric{
		{Name: "tool_trajectory_match", Threshold: 0.6},
		{Name: "response_contains_pattern", Threshold: 0.8},
		{Name: "response_min_length", Threshold: 0.5},
		{Name: "response_not_empty", Threshold: 1.0},
	}
}
