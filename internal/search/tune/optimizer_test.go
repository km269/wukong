package tune

import (
	"math/rand"
	"testing"

	"github.com/km269/wukong/internal/search"
)

// newRand creates a deterministic random number generator for tests.
func newRand(seed int64) *rand.Rand {
	return rand.New(rand.NewSource(seed))
}

func TestGenerateInitialPopulation(t *testing.T) {
	cfg := OptimizerConfig{
		Baseline:       search.DefaultGenome(),
		PopulationSize: 10,
	}
	pop := GenerateInitialPopulation(cfg)
	if len(pop) == 0 {
		t.Error("population is empty")
	}
	// Should include baseline.
	found := false
	for _, g := range pop {
		if g.RecallMode == search.DefaultGenome().RecallMode {
			found = true
			break
		}
	}
	if !found {
		t.Error("population should include baseline")
	}
	// Should include boundary strategies.
	hasLexical := false
	hasVector := false
	for _, g := range pop {
		if g.RecallMode == search.RecallModeLexical {
			hasLexical = true
		}
		if g.RecallMode == search.RecallModeVector {
			hasVector = true
		}
	}
	if !hasLexical {
		t.Error("population should include lexical boundary")
	}
	if !hasVector {
		t.Error("population should include vector boundary")
	}
}

func TestGenerateNextPopulation(t *testing.T) {
	cfg := OptimizerConfig{
		Baseline:       search.DefaultGenome(),
		PopulationSize: 6,
	}
	elites := []Elite{
		{
			Genome: search.SearchGenome{
				RecallMode:      search.RecallModeHybrid,
				DenseWeight:     0.7,
				TextWeight:      0.3,
				MaxRetrievedNum: 10,
				FTS5PoolSize:    50,
			},
			Metrics: search.StrategyMetrics{},
		},
		{
			Genome: search.SearchGenome{
				RecallMode:      search.RecallModeLexical,
				DenseWeight:     0,
				TextWeight:      1,
				MaxRetrievedNum: 10,
				FTS5PoolSize:    50,
			},
			Metrics: search.StrategyMetrics{},
		},
	}

	rng := newRand(42)
	pop := GenerateNextPopulation(elites, cfg, 0.5, rng)
	if len(pop) == 0 {
		t.Error("next population is empty")
	}
	// Should include the global best (first elite).
	foundBest := false
	for _, g := range pop {
		if g.RecallMode == search.RecallModeHybrid &&
			g.DenseWeight > 0.6 {
			foundBest = true
		}
	}
	if !foundBest {
		t.Error("next population should include global best")
	}
}

func TestSelectElites(t *testing.T) {
	metrics := []search.StrategyMetrics{
		{Genome: search.SearchGenome{RecallMode: search.RecallModeHybrid, DenseWeight: 0.7},
			NDCG20: 0.8, MRR10: 0.7, AvgLatencyMs: 100},
		{Genome: search.SearchGenome{RecallMode: search.RecallModeLexical, DenseWeight: 0},
			NDCG20: 0.6, MRR10: 0.5, AvgLatencyMs: 50},
		{Genome: search.SearchGenome{RecallMode: search.RecallModeVector, DenseWeight: 1},
			NDCG20: 0.7, MRR10: 0.6, AvgLatencyMs: 200},
	}

	elites := SelectElites(metrics, 0)
	if len(elites) == 0 {
		t.Error("no elites selected")
	}
	// First elite should be global best (highest RobustScore).
	if elites[0].Category != EliteGlobalBest {
		t.Errorf("first elite = %s, want global_best",
			elites[0].Category)
	}
	if elites[0].Metrics.NDCG20 != 0.8 {
		t.Errorf("global best NDCG = %.2f, want 0.8",
			elites[0].Metrics.NDCG20)
	}
}

func TestEliminateWorst(t *testing.T) {
	strategies := []search.SearchGenome{
		{RecallMode: search.RecallModeHybrid, DenseWeight: 0.7, MaxRetrievedNum: 10, FTS5PoolSize: 50},
		{RecallMode: search.RecallModeLexical, DenseWeight: 0, MaxRetrievedNum: 10, FTS5PoolSize: 50},
		{RecallMode: search.RecallModeVector, DenseWeight: 1, MaxRetrievedNum: 10, FTS5PoolSize: 50},
		{RecallMode: search.RecallModeHybrid, DenseWeight: 0.3, MaxRetrievedNum: 10, FTS5PoolSize: 50},
	}
	metrics := []search.StrategyMetrics{
		{NDCG20: 0.9}, // best
		{NDCG20: 0.3}, // worst
		{NDCG20: 0.7},
		{NDCG20: 0.5},
	}
	// Eliminate 25% (1 out of 4).
	survivors := eliminateWorst(strategies, metrics, 0.25)
	if len(survivors) != 3 {
		t.Errorf("survivors = %d, want 3", len(survivors))
	}
	// The worst (NDCG=0.3) should be eliminated.
	for _, s := range survivors {
		if s.RecallMode == search.RecallModeLexical {
			t.Error("worst strategy should be eliminated")
		}
	}
}
