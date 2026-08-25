package search

import (
	"math"
	"testing"
)

func TestNDCG_PerfectRanking(t *testing.T) {
	items := []RankedItem{
		{ID: "a"}, {ID: "b"}, {ID: "c"},
	}
	labels := RelevanceLabels{"a": 3, "b": 2, "c": 1}
	score := NDCG(items, labels, 3)
	if score < 0.99 {
		t.Errorf("perfect ranking NDCG = %.4f, want ~1.0", score)
	}
}

func TestNDCG_WorstRanking(t *testing.T) {
	items := []RankedItem{
		{ID: "c"}, {ID: "b"}, {ID: "a"},
	}
	labels := RelevanceLabels{"a": 3, "b": 2, "c": 1}
	score := NDCG(items, labels, 3)
	if score > 0.8 {
		t.Errorf("worst ranking NDCG = %.4f, want < 0.8", score)
	}
}

func TestNDCG_EmptyResults(t *testing.T) {
	score := NDCG(nil, RelevanceLabels{"a": 1}, 10)
	if score != 0 {
		t.Errorf("empty results NDCG = %.4f, want 0", score)
	}
}

func TestNDCG_NoLabels(t *testing.T) {
	items := []RankedItem{{ID: "a"}}
	score := NDCG(items, nil, 10)
	if score != 0 {
		t.Errorf("no labels NDCG = %.4f, want 0", score)
	}
}

func TestMRR_FirstRelevant(t *testing.T) {
	items := []RankedItem{
		{ID: "x"}, {ID: "y"}, {ID: "z"},
	}
	labels := RelevanceLabels{"x": 1}
	score := MRR(items, labels, 3)
	if math.Abs(score-1.0) > 0.001 {
		t.Errorf("MRR with first relevant = %.4f, want 1.0", score)
	}
}

func TestMRR_ThirdRelevant(t *testing.T) {
	items := []RankedItem{
		{ID: "x"}, {ID: "y"}, {ID: "z"},
	}
	labels := RelevanceLabels{"z": 1}
	score := MRR(items, labels, 3)
	if math.Abs(score-1.0/3.0) > 0.001 {
		t.Errorf("MRR with third relevant = %.4f, want 0.333", score)
	}
}

func TestMRR_NoRelevant(t *testing.T) {
	items := []RankedItem{{ID: "x"}}
	labels := RelevanceLabels{"w": 1}
	score := MRR(items, labels, 3)
	if score != 0 {
		t.Errorf("MRR with no relevant = %.4f, want 0", score)
	}
}

func TestPrecision(t *testing.T) {
	items := []RankedItem{
		{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"},
	}
	labels := RelevanceLabels{"a": 1, "c": 1}
	score := Precision(items, labels, 5)
	if math.Abs(score-2.0/5.0) > 0.001 {
		t.Errorf("Precision = %.4f, want 0.4", score)
	}
}

func TestPrecision_K(t *testing.T) {
	items := []RankedItem{
		{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"},
	}
	labels := RelevanceLabels{"a": 1, "b": 1}
	score := Precision(items, labels, 2)
	if math.Abs(score-1.0) > 0.001 {
		t.Errorf("Precision@2 = %.4f, want 1.0", score)
	}
}

func TestZeroResultRate(t *testing.T) {
	all := [][]RankedItem{
		{{ID: "a"}}, // 1 result
		{},          // 0 results
		{{ID: "b"}}, // 1 result
	}
	rate := ZeroResultRate(all)
	if math.Abs(rate-1.0/3.0) > 0.001 {
		t.Errorf("ZeroResultRate = %.4f, want 0.333", rate)
	}
}

func TestZeroResultRate_AllZero(t *testing.T) {
	all := [][]RankedItem{{}, {}}
	rate := ZeroResultRate(all)
	if rate != 1.0 {
		t.Errorf("ZeroResultRate = %.4f, want 1.0", rate)
	}
}

func TestRobustScore(t *testing.T) {
	m := StrategyMetrics{
		NDCG20:         0.8,
		MRR10:          0.7,
		ZeroResultRate: 0.1,
		AvgLatencyMs:   200,
	}
	params := RobustScoreParams{}
	score := m.RobustScore(params)
	// Expected: 0.8 + 0.1*0.7 - 0.5*0.1 = 0.8 + 0.07 - 0.05 = 0.82
	if math.Abs(score-0.82) > 0.01 {
		t.Errorf("RobustScore = %.4f, want ~0.82", score)
	}
}

func TestRobustScore_LatencyPenalty(t *testing.T) {
	m := StrategyMetrics{
		NDCG20:         0.8,
		MRR10:          0.7,
		ZeroResultRate: 0.0,
		AvgLatencyMs:   800, // exceeds 500ms budget
	}
	params := RobustScoreParams{}
	score := m.RobustScore(params)
	// Should be less than 0.8 + 0.07 = 0.87 due to latency penalty
	if score >= 0.87 {
		t.Errorf("RobustScore with latency = %.4f, want < 0.87", score)
	}
}

func TestGenome_Normalized(t *testing.T) {
	g := SearchGenome{
		RecallMode:  RecallModeHybrid,
		DenseWeight: 3,
		TextWeight:  1,
	}
	n := g.Normalized()
	if math.Abs(n.DenseWeight-0.75) > 0.001 {
		t.Errorf("normalized dense = %.4f, want 0.75", n.DenseWeight)
	}
	if math.Abs(n.TextWeight-0.25) > 0.001 {
		t.Errorf("normalized text = %.4f, want 0.25", n.TextWeight)
	}
}

func TestGenome_Validate(t *testing.T) {
	valid := SearchGenome{
		RecallMode:      RecallModeHybrid,
		DenseWeight:     0.5,
		TextWeight:      0.5,
		MaxRetrievedNum: 10,
		FTS5PoolSize:    50,
	}
	if err := valid.Validate(); err != nil {
		t.Errorf("valid genome failed: %v", err)
	}

	invalid := SearchGenome{RecallMode: "invalid"}
	if err := invalid.Validate(); err == nil {
		t.Error("invalid recall mode should fail validation")
	}
}

func TestComputePlan(t *testing.T) {
	plan := ComputePlan(5, 20, 10, true)
	if plan.StrategyCount != 5 {
		t.Errorf("strategy count = %d, want 5", plan.StrategyCount)
	}
	if plan.QueryCount != 20 {
		t.Errorf("query count = %d, want 20", plan.QueryCount)
	}
	if plan.SearchRequests != 100 {
		t.Errorf("search requests = %d, want 100", plan.SearchRequests)
	}
	if plan.MaxLabels != 1000 {
		t.Errorf("max labels = %d, want 1000", plan.MaxLabels)
	}
	if plan.EstimatedLLMCalls >= plan.MaxLabels {
		t.Error("silver filtering should reduce LLM calls")
	}
}

func TestValidateCases(t *testing.T) {
	cases := []EvalCase{
		{Query: "hello world", QueryType: "keyword"},
		{Query: "hello world", QueryType: "keyword"}, // duplicate
		{Query: "", QueryType: "keyword"},            // empty
		{Query: "concept of justice", QueryType: "natural_language"},
	}
	report := ValidateCases(cases)
	if report.TotalCases != 4 {
		t.Errorf("total = %d, want 4", report.TotalCases)
	}
	if report.DuplicateQueries != 1 {
		t.Errorf("duplicates = %d, want 1", report.DuplicateQueries)
	}
	if report.EmptyQueries != 1 {
		t.Errorf("empty = %d, want 1", report.EmptyQueries)
	}
}
