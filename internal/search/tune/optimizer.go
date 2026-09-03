package tune

import (
	"fmt"
	"math"
	"math/rand"
	"sort"

	"github.com/km269/wukong/internal/search"
)

// ============================================================================
// StrategyOptimizer: lightweight SPA-inspired strategy optimiser
//
// Inspired by volcengine/SearchCLI's SPA (Strategy Population
// Annealing), this optimiser generates candidate strategies from
// meaningful regions, evaluates them, and selects multi-view elites.
//
// Unlike full SPA, this implementation uses a fixed number of
// iterations with decreasing exploration (simulated annealing),
// rather than a full evolutionary loop. This covers ~80% of the
// benefit at ~20% of the complexity.
// ============================================================================

// OptimizerConfig controls the optimiser's behaviour.
type OptimizerConfig struct {
	// Baseline is the current production strategy.
	Baseline search.SearchGenome

	// MaxIterations controls how many rounds of optimisation.
	// Default 3.
	MaxIterations int

	// PopulationSize is the number of candidates per iteration.
	// Default 8.
	PopulationSize int

	// Profile limits which parameters are tuned:
	//   "similarity-only" — only recall mode, weights, threshold,
	//     and candidate size (no rerank/personalisation).
	// Default "similarity-only".
	Profile string

	// TopK for evaluation. Default 10.
	TopK int

	// SilverLabelFiltering enables fast-pass elimination.
	SilverLabelFiltering bool

	// AnnealingStart controls initial exploration amplitude [0,1].
	// Higher = more diverse candidates early. Default 0.8.
	AnnealingStart float64

	// AnnealingEnd controls final exploration amplitude [0,1].
	// Lower = more focused late. Default 0.2.
	AnnealingEnd float64
}

// WithDefaults fills in default values for zero fields.
func (c OptimizerConfig) WithDefaults() OptimizerConfig {
	out := c
	if out.MaxIterations <= 0 {
		out.MaxIterations = 3
	}
	if out.PopulationSize <= 0 {
		out.PopulationSize = 8
	}
	if out.Profile == "" {
		out.Profile = "similarity-only"
	}
	if out.TopK <= 0 {
		out.TopK = 10
	}
	if out.AnnealingStart <= 0 {
		out.AnnealingStart = 0.8
	}
	if out.AnnealingEnd <= 0 {
		out.AnnealingEnd = 0.2
	}
	if out.Baseline.RecallMode == "" {
		out.Baseline = search.DefaultGenome()
	}
	return out
}

// EliteCategory categorises an elite strategy by its strength.
type EliteCategory string

const (
	EliteGlobalBest       EliteCategory = "global_best"
	EliteQueryTypeBest    EliteCategory = "query_type_best"
	EliteStableBest       EliteCategory = "stable_best"
	EliteLowLatencyBest   EliteCategory = "low_latency_best"
	EliteBaselineImprover EliteCategory = "baseline_improver"
)

// Elite represents a selected top-performing strategy.
type Elite struct {
	Genome   search.SearchGenome    `json:"genome"`
	Metrics  search.StrategyMetrics `json:"metrics"`
	Category EliteCategory          `json:"category"`
	Score    float64                `json:"score"`
}

// OptimizerResult holds the output of an optimisation run.
type OptimizerResult struct {
	BestGenome   search.SearchGenome      `json:"best_genome"`
	BestMetrics  search.StrategyMetrics   `json:"best_metrics"`
	Elites       []Elite                  `json:"elites"`
	AllEvaluated []search.StrategyMetrics `json:"all_evaluated"`
	Iterations   int                      `json:"iterations"`
	Improvement  float64                  `json:"improvement_vs_baseline"`
}

// GenerateInitialPopulation creates the first generation of
// candidate strategies from meaningful regions, not random points.
//
// The population includes:
//  1. Current baseline
//  2. Boundary strategies: KeywordOnly, SemanticOnly
//  3. Coarse grid: DenseWeight ∈ {0.25, 0.5, 0.75}
//  4. Domain priors: knowledge-base → more semantic
func GenerateInitialPopulation(
	cfg OptimizerConfig,
) []search.SearchGenome {
	cfg = cfg.WithDefaults()
	baseline := cfg.Baseline.Normalized()
	popSize := cfg.PopulationSize

	population := make([]search.SearchGenome, 0, popSize+4)
	seen := make(map[string]bool)
	addUnique := func(g search.SearchGenome) {
		g = g.Normalized()
		key := fmt.Sprintf("%s_%.2f_%.2f_%.1f_%d_%d",
			g.RecallMode, g.DenseWeight, g.TextWeight,
			g.KeywordMatchPercent, g.MaxRetrievedNum, g.FTS5PoolSize)
		if !seen[key] {
			seen[key] = true
			population = append(population, g)
		}
	}

	// 1. Baseline.
	addUnique(baseline)

	// 2. Boundary: KeywordOnly.
	addUnique(search.SearchGenome{
		RecallMode:  search.RecallModeLexical,
		DenseWeight: 0, TextWeight: 1,
		MaxRetrievedNum: baseline.MaxRetrievedNum,
		FTS5PoolSize:    baseline.FTS5PoolSize,
	})

	// 3. Boundary: SemanticOnly.
	addUnique(search.SearchGenome{
		RecallMode:  search.RecallModeVector,
		DenseWeight: 1, TextWeight: 0,
		MaxRetrievedNum: baseline.MaxRetrievedNum,
		FTS5PoolSize:    baseline.FTS5PoolSize,
	})

	// 4. Coarse grid: DenseWeight ∈ {0.25, 0.5, 0.75}.
	for _, dw := range []float64{0.25, 0.5, 0.75} {
		addUnique(search.SearchGenome{
			RecallMode:      search.RecallModeHybrid,
			DenseWeight:     dw,
			TextWeight:      1 - dw,
			MaxRetrievedNum: baseline.MaxRetrievedNum,
			FTS5PoolSize:    baseline.FTS5PoolSize,
		})
	}

	// 5. Keyword match percent variations.
	addUnique(search.SearchGenome{
		RecallMode:  search.RecallModeHybrid,
		DenseWeight: 0.5, TextWeight: 0.5,
		KeywordMatchPercent: 0.3,
		MaxRetrievedNum:     baseline.MaxRetrievedNum,
		FTS5PoolSize:        baseline.FTS5PoolSize,
	})

	// 6. Candidate size variations.
	addUnique(search.SearchGenome{
		RecallMode:  search.RecallModeHybrid,
		DenseWeight: 0.5, TextWeight: 0.5,
		MaxRetrievedNum: 20,
		FTS5PoolSize:    100,
	})
	addUnique(search.SearchGenome{
		RecallMode:  search.RecallModeHybrid,
		DenseWeight: 0.5, TextWeight: 0.5,
		MaxRetrievedNum: 5,
		FTS5PoolSize:    30,
	})

	// Trim to population size.
	if len(population) > popSize {
		population = population[:popSize]
	}

	return population
}

// GenerateNextPopulation creates the next generation from elites
// using crossover, mutation, and direction move, controlled by
// the annealing temperature.
func GenerateNextPopulation(
	elites []Elite,
	cfg OptimizerConfig,
	temperature float64,
	rng *rand.Rand,
) []search.SearchGenome {
	cfg = cfg.WithDefaults()
	popSize := cfg.PopulationSize
	population := make([]search.SearchGenome, 0, popSize)
	seen := make(map[string]bool)
	addUnique := func(g search.SearchGenome) {
		g = g.Normalized()
		key := fmt.Sprintf("%s_%.2f_%.2f_%.1f_%d_%d",
			g.RecallMode, g.DenseWeight, g.TextWeight,
			g.KeywordMatchPercent, g.MaxRetrievedNum, g.FTS5PoolSize)
		if !seen[key] {
			seen[key] = true
			population = append(population, g)
		}
	}

	// Always keep the global best.
	if len(elites) > 0 {
		addUnique(elites[0].Genome)
	}

	// Crossover: combine weights from two elites.
	for i := 0; i < len(elites) && len(population) < popSize; i++ {
		for j := i + 1; j < len(elites) && len(population) < popSize; j++ {
			child := crossover(elites[i].Genome, elites[j].Genome, rng)
			addUnique(child)
		}
	}

	// Mutation: perturb elites near their values.
	for _, elite := range elites {
		if len(population) >= popSize {
			break
		}
		mutated := mutate(elite.Genome, temperature, rng)
		addUnique(mutated)
	}

	// Direction move: move towards global best.
	if len(elites) > 0 {
		best := elites[0].Genome
		for _, elite := range elites[1:] {
			if len(population) >= popSize {
				break
			}
			moved := moveTowards(elite.Genome, best, temperature, rng)
			addUnique(moved)
		}
	}

	// Fill remaining slots with random neighbourhood candidates.
	for len(population) < popSize && len(elites) > 0 {
		base := elites[rng.Intn(len(elites))].Genome
		random := mutate(base, temperature*1.5, rng)
		addUnique(random)
	}

	return population
}

// crossover combines weights and parameters from two parents.
func crossover(
	a, b search.SearchGenome, rng *rand.Rand,
) search.SearchGenome {
	child := search.SearchGenome{
		RecallMode:      pickMode(a.RecallMode, b.RecallMode, rng),
		DenseWeight:     (a.DenseWeight + b.DenseWeight) / 2,
		TextWeight:      (a.TextWeight + b.TextWeight) / 2,
		MaxRetrievedNum: pickInt(a.MaxRetrievedNum, b.MaxRetrievedNum, rng),
		FTS5PoolSize:    pickInt(a.FTS5PoolSize, b.FTS5PoolSize, rng),
	}
	if rng.Float64() < 0.5 {
		child.KeywordMatchPercent = a.KeywordMatchPercent
	} else {
		child.KeywordMatchPercent = b.KeywordMatchPercent
	}
	return child.Normalized()
}

// mutate perturbs a genome by a temperature-controlled amount.
func mutate(
	g search.SearchGenome,
	temperature float64,
	rng *rand.Rand,
) search.SearchGenome {
	out := g
	// Perturb dense weight.
	delta := (rng.Float64() - 0.5) * temperature * 0.4
	out.DenseWeight = clamp(out.DenseWeight+delta, 0, 1)
	out.TextWeight = 1 - out.DenseWeight

	// Occasionally change recall mode.
	if rng.Float64() < temperature*0.15 {
		modes := []string{
			search.RecallModeHybrid,
			search.RecallModeLexical,
			search.RecallModeVector,
		}
		out.RecallMode = modes[rng.Intn(len(modes))]
	}

	// Perturb keyword match percent.
	if rng.Float64() < temperature*0.3 {
		delta := (rng.Float64() - 0.5) * temperature * 0.3
		out.KeywordMatchPercent = clamp(
			out.KeywordMatchPercent+delta, 0, 0.8)
	}

	// Perturb candidate size.
	if rng.Float64() < temperature*0.3 {
		delta := int((rng.Float64() - 0.5) * temperature * 10)
		out.MaxRetrievedNum = clampInt(out.MaxRetrievedNum+delta, 1, 50)
		out.FTS5PoolSize = clampInt(
			out.FTS5PoolSize+delta*2, 10, 200)
	}

	return out.Normalized()
}

// moveTowards moves a genome towards the target by a
// temperature-controlled step.
func moveTowards(
	g, target search.SearchGenome,
	temperature float64,
	rng *rand.Rand,
) search.SearchGenome {
	out := g
	step := temperature * (0.3 + rng.Float64()*0.4)

	out.DenseWeight = g.DenseWeight +
		(target.DenseWeight-g.DenseWeight)*step
	out.TextWeight = 1 - out.DenseWeight

	if target.MaxRetrievedNum != g.MaxRetrievedNum {
		diff := float64(target.MaxRetrievedNum - g.MaxRetrievedNum)
		out.MaxRetrievedNum = g.MaxRetrievedNum + int(diff*step)
	}
	if target.FTS5PoolSize != g.FTS5PoolSize {
		diff := float64(target.FTS5PoolSize - g.FTS5PoolSize)
		out.FTS5PoolSize = g.FTS5PoolSize + int(diff*step)
	}

	return out.Normalized()
}

// ----------------------------------------------------------------------------
// Multi-view Elite selection
// ----------------------------------------------------------------------------

// SelectElites picks multi-view elites from evaluated strategies.
// Each elite answers: "why is this strategy worth continuing to
// evaluate?"
func SelectElites(
	metrics []search.StrategyMetrics,
	baselineIdx int,
) []Elite {
	if len(metrics) == 0 {
		return nil
	}

	// Compute RobustScore for each strategy.
	scores := make([]struct {
		idx   int
		score float64
		m     search.StrategyMetrics
	}, len(metrics))
	for i, m := range metrics {
		scores[i] = struct {
			idx   int
			score float64
			m     search.StrategyMetrics
		}{i, m.RobustScore(search.RobustScoreParams{}), m}
	}

	// Sort by RobustScore descending.
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	var elites []Elite
	seen := make(map[int]bool)
	addElite := func(idx int, category EliteCategory) {
		if idx < 0 || idx >= len(scores) || seen[idx] {
			return
		}
		seen[idx] = true
		elites = append(elites, Elite{
			Genome:   scores[idx].m.Genome,
			Metrics:  scores[idx].m,
			Category: category,
			Score:    scores[idx].score,
		})
	}

	// 1. Global Best.
	addElite(0, EliteGlobalBest)

	// 2. Stable Best: lowest NDCG variance (most consistent).
	stableIdx := -1
	stableVar := math.MaxFloat64
	for i, s := range scores {
		if seen[i] {
			continue
		}
		v := ndcgVar(s.m)
		if v < stableVar {
			stableVar = v
			stableIdx = i
		}
	}
	addElite(stableIdx, EliteStableBest)

	// 3. Low Latency Best: lowest avg latency among top half.
	half := len(scores) / 2
	if half < 1 {
		half = 1
	}
	lowLatIdx := -1
	lowLat := math.MaxFloat64
	for i := 0; i < half; i++ {
		if seen[i] {
			continue
		}
		if scores[i].m.AvgLatencyMs < lowLat {
			lowLat = scores[i].m.AvgLatencyMs
			lowLatIdx = i
		}
	}
	addElite(lowLatIdx, EliteLowLatencyBest)

	// 4. Baseline Improver: best among strategies that beat baseline
	//    on every metric (no regression).
	if baselineIdx >= 0 && baselineIdx < len(metrics) {
		baseline := metrics[baselineIdx]
		for i, s := range scores {
			if seen[i] {
				continue
			}
			if s.m.NDCG20 >= baseline.NDCG20 &&
				s.m.ZeroResultRate <= baseline.ZeroResultRate &&
				s.m.AvgLatencyMs <= baseline.AvgLatencyMs*1.2 {
				addElite(i, EliteBaselineImprover)
				break
			}
		}
	}

	// 5. Diverse: add a strategy that differs significantly from
	//    existing elites (different recall mode or very different weight).
	for i, s := range scores {
		if seen[i] {
			continue
		}
		isDiverse := true
		for _, e := range elites {
			if s.m.Genome.RecallMode == e.Genome.RecallMode &&
				math.Abs(s.m.Genome.DenseWeight-e.Genome.DenseWeight) < 0.2 {
				isDiverse = false
				break
			}
		}
		if isDiverse {
			addElite(i, EliteQueryTypeBest)
			break
		}
	}

	return elites
}

// ndcgVar computes NDCG@20 variance from per-query metrics.
func ndcgVar(m search.StrategyMetrics) float64 {
	if len(m.PerQuery) < 2 {
		return 0
	}
	mean := 0.0
	for _, q := range m.PerQuery {
		mean += q.NDCG20
	}
	mean /= float64(len(m.PerQuery))
	sumSq := 0.0
	for _, q := range m.PerQuery {
		diff := q.NDCG20 - mean
		sumSq += diff * diff
	}
	return sumSq / float64(len(m.PerQuery))
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func pickMode(a, b string, rng *rand.Rand) string {
	if rng.Float64() < 0.5 {
		return a
	}
	return b
}

func pickInt(a, b int, rng *rand.Rand) int {
	if rng.Float64() < 0.5 {
		return a
	}
	return b
}
