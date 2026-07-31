package tune

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/km269/wukong/internal/search"
)

// ============================================================================
// Multi-Fidelity Evaluation
//
// Inspired by SearchCLI's three-pass evaluation:
//   1. Fast Pass: small query subset + silver labels → eliminate bad strategies
//   2. Middle Pass: expanded query set → identify locally good strategies
//   3. Confirm Pass: full query set + LLM Judge → high-confidence selection
//
// This dramatically reduces LLM judging cost by eliminating hopeless
// strategies early.
// ============================================================================

// FidelityLevel controls evaluation thoroughness.
type FidelityLevel string

const (
	FidelityFast    FidelityLevel = "fast"    // 20% queries, silver labels
	FidelityMiddle  FidelityLevel = "middle"  // 50% queries, partial LLM
	FidelityConfirm FidelityLevel = "confirm" // 100% queries, full LLM
)

// MultiFidelityConfig controls the three-pass evaluation.
type MultiFidelityConfig struct {
	// FastPassFraction: fraction of queries for fast pass (default 0.2).
	FastPassFraction float64
	// MiddlePassFraction: fraction of queries for middle pass (default 0.5).
	MiddlePassFraction float64
	// EliminateFraction: fraction of strategies eliminated after fast pass (default 0.4).
	EliminateFraction float64
	// MiddleEliminateFraction: fraction eliminated after middle pass (default 0.3).
	MiddleEliminateFraction float64
	// Seed for reproducible subsetting.
	Seed int64
}

func (c MultiFidelityConfig) WithDefaults() MultiFidelityConfig {
	out := c
	if out.FastPassFraction <= 0 {
		out.FastPassFraction = 0.2
	}
	if out.MiddlePassFraction <= 0 {
		out.MiddlePassFraction = 0.5
	}
	if out.EliminateFraction <= 0 {
		out.EliminateFraction = 0.4
	}
	if out.MiddleEliminateFraction <= 0 {
		out.MiddleEliminateFraction = 0.3
	}
	if out.Seed == 0 {
		out.Seed = time.Now().UnixNano()
	}
	return out
}

// MultiFidelityResult holds the output of three-pass evaluation.
type MultiFidelityResult struct {
	FastPass    *TuneRun                 `json:"fast_pass"`
	MiddlePass  *TuneRun                 `json:"middle_pass,omitempty"`
	ConfirmPass *TuneRun                 `json:"confirm_pass"`
	Survivors   []search.SearchGenome    `json:"survivors"`
	BestMetrics search.StrategyMetrics   `json:"best_metrics"`
	Report      TuneReport               `json:"report"`
}

// RunMultiFidelity executes the three-pass evaluation pipeline.
// It starts with all strategies on a small query subset, eliminates
// the worst performers, expands the query set, and finally runs the
// survivors on the full query set with LLM judging.
func (t *Tuner) RunMultiFidelity(
	ctx context.Context,
	strategies []search.SearchGenome,
	cases []search.EvalCase,
	cfg MultiFidelityConfig,
	labelSource string,
) (*MultiFidelityResult, error) {
	cfg = cfg.WithDefaults()
	rng := rand.New(rand.NewSource(cfg.Seed))

	result := &MultiFidelityResult{}

	// ---- Fast Pass: 20% queries, silver labels ----
	fastQueries := subsetQueries(cases, cfg.FastPassFraction, rng)
	t.logger.Info("multi-fidelity: fast pass",
		slog.Int("strategies", len(strategies)),
		slog.Int("queries", len(fastQueries)),
		slog.String("fidelity", string(FidelityFast)))

	fastRun, err := t.Run(ctx, TuneRequest{
		RunID:       fmt.Sprintf("fast_%d", time.Now().Unix()),
		Strategies:  strategies,
		Cases:       fastQueries,
		LabelSource: "silver",
		Concurrency: 4,
	})
	if err != nil {
		return nil, fmt.Errorf("fast pass: %w", err)
	}
	result.FastPass = fastRun

	// Eliminate worst strategies after fast pass.
	survivors := eliminateWorst(
		strategies, fastRun.Metrics,
		cfg.EliminateFraction,
	)
	t.logger.Info("multi-fidelity: fast pass complete",
		slog.Int("survivors", len(survivors)))

	if len(survivors) <= 1 {
		// Skip middle/confirm if only one survivor.
		result.Survivors = survivors
		result.ConfirmPass = fastRun
		if len(fastRun.Metrics) > 0 {
			result.BestMetrics = fastRun.Metrics[0]
		}
		result.Report = GenerateReport(fastRun)
		return result, nil
	}

	// ---- Middle Pass: 50% queries ----
	middleQueries := subsetQueries(cases, cfg.MiddlePassFraction, rng)
	t.logger.Info("multi-fidelity: middle pass",
		slog.Int("strategies", len(survivors)),
		slog.Int("queries", len(middleQueries)),
		slog.String("fidelity", string(FidelityMiddle)))

	middleRun, err := t.Run(ctx, TuneRequest{
		RunID:       fmt.Sprintf("middle_%d", time.Now().Unix()),
		Strategies:  survivors,
		Cases:       middleQueries,
		LabelSource: labelSource,
		Concurrency: 4,
	})
	if err != nil {
		return nil, fmt.Errorf("middle pass: %w", err)
	}
	result.MiddlePass = middleRun

	// Eliminate again.
	survivors = eliminateWorst(
		survivors, middleRun.Metrics,
		cfg.MiddleEliminateFraction,
	)
	t.logger.Info("multi-fidelity: middle pass complete",
		slog.Int("survivors", len(survivors)))

	// ---- Confirm Pass: full query set + LLM Judge ----
	t.logger.Info("multi-fidelity: confirm pass",
		slog.Int("strategies", len(survivors)),
		slog.Int("queries", len(cases)),
		slog.String("fidelity", string(FidelityConfirm)))

	confirmRun, err := t.Run(ctx, TuneRequest{
		RunID:       fmt.Sprintf("confirm_%d", time.Now().Unix()),
		Strategies:  survivors,
		Cases:       cases,
		LabelSource: labelSource,
		Concurrency: 4,
	})
	if err != nil {
		return nil, fmt.Errorf("confirm pass: %w", err)
	}
	result.ConfirmPass = confirmRun
	result.Survivors = survivors
	result.Report = GenerateReport(confirmRun)

	// Find best metrics.
	if len(confirmRun.Metrics) > 0 {
		bestIdx := 0
		bestScore := confirmRun.Metrics[0].RobustScore(
			search.RobustScoreParams{})
		for i := 1; i < len(confirmRun.Metrics); i++ {
			score := confirmRun.Metrics[i].RobustScore(
				search.RobustScoreParams{})
			if score > bestScore {
				bestScore = score
				bestIdx = i
			}
		}
		result.BestMetrics = confirmRun.Metrics[bestIdx]
	}

	return result, nil
}

// subsetQueries returns a random subset of queries.
func subsetQueries(
	cases []search.EvalCase,
	fraction float64,
	rng *rand.Rand,
) []search.EvalCase {
	if fraction >= 1.0 {
		return cases
	}
	n := int(float64(len(cases)) * fraction)
	if n < 1 {
		n = 1
	}
	// Shuffle indices and take first n.
	indices := rng.Perm(len(cases))
	subset := make([]search.EvalCase, n)
	for i := 0; i < n; i++ {
		subset[i] = cases[indices[i]]
	}
	return subset
}

// eliminateWorst removes the bottom fraction of strategies based
// on RobustScore, returning the survivors.
func eliminateWorst(
	strategies []search.SearchGenome,
	metrics []search.StrategyMetrics,
	eliminateFraction float64,
) []search.SearchGenome {
	if len(strategies) <= 1 || len(metrics) == 0 {
		return strategies
	}

	// Pair strategies with their scores.
	type scored struct {
		idx   int
		score float64
	}
	scoredList := make([]scored, len(metrics))
	for i, m := range metrics {
		scoredList[i] = scored{
			i, m.RobustScore(search.RobustScoreParams{})}
	}

	// Sort by score descending.
	for i := 0; i < len(scoredList)-1; i++ {
		for j := i + 1; j < len(scoredList); j++ {
			if scoredList[j].score > scoredList[i].score {
				scoredList[i], scoredList[j] =
					scoredList[j], scoredList[i]
			}
		}
	}

	// Keep top (1 - eliminateFraction).
	keepCount := int(float64(len(strategies)) * (1 - eliminateFraction))
	if keepCount < 1 {
		keepCount = 1
	}
	if keepCount > len(strategies) {
		keepCount = len(strategies)
	}

	survivors := make([]search.SearchGenome, 0, keepCount)
	for i := 0; i < keepCount && i < len(scoredList); i++ {
		idx := scoredList[i].idx
		if idx < len(strategies) {
			survivors = append(survivors, strategies[idx])
		}
	}
	return survivors
}
