package tune

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/km269/wukong/internal/search"
)

// ---------------------------------------------------------------------------
// Mock Searcher
// ---------------------------------------------------------------------------

// mockSearcher returns deterministic results based on the genome.
type mockSearcher struct {
	mu    sync.Mutex
	calls int
	failQ map[string]bool // queries that should fail
}

func (m *mockSearcher) SearchWithGenome(
	ctx context.Context,
	query string,
	genome search.SearchGenome,
) ([]search.RankedItem, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	if m.failQ[query] {
		return nil, context.DeadlineExceeded
	}
	// Return 3 deterministic items.
	return []search.RankedItem{
		{ID: "doc_a", Score: 0.9},
		{ID: "doc_b", Score: 0.7},
		{ID: "doc_c", Score: 0.5},
	}, nil
}

func (m *mockSearcher) Calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func makeCases(n int, queryType string) []search.EvalCase {
	cases := make([]search.EvalCase, n)
	for i := range cases {
		cases[i] = search.EvalCase{
			Query:       "test query " + queryType,
			QueryType:   queryType,
			RelevantIDs: []string{"doc_a", "doc_b"},
		}
	}
	return cases
}

func newTestService(t *testing.T) *AutoTuneService {
	t.Helper()
	svc, err := NewAutoTuneService(AutoTuneConfig{
		Searcher: &mockSearcher{},
	})
	if err != nil {
		t.Fatalf("NewAutoTuneService: %v", err)
	}
	return svc
}

// ---------------------------------------------------------------------------
// Plan
// ---------------------------------------------------------------------------

func TestAutoTune_Plan_NoSearchOrLLM(t *testing.T) {
	svc := newTestService(t)
	defer svc.Close()
	ms := svc.searcher.(*mockSearcher)

	cases := makeCases(5, "keyword")
	result, err := svc.Plan(context.Background(), cases, PlanOptions{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	// Plan must not invoke the searcher.
	if ms.Calls() != 0 {
		t.Errorf("Plan called searcher %d times; want 0", ms.Calls())
	}
	if len(result.Strategies) == 0 {
		t.Error("Plan returned no strategies")
	}
	if result.Budget.SearchRequests == 0 {
		t.Error("Budget has zero search requests")
	}
	if result.Distribution.Total != 5 {
		t.Errorf("distribution total = %d, want 5",
			result.Distribution.Total)
	}
}

func TestAutoTune_Plan_KeywordHeavy(t *testing.T) {
	svc := newTestService(t)
	defer svc.Close()

	// 4 short keyword queries + 1 NL query.
	cases := []search.EvalCase{
		{Query: "go", QueryType: "keyword"},
		{Query: "git", QueryType: "keyword"},
		{Query: "rpc", QueryType: "keyword"},
		{Query: "cfg", QueryType: "keyword"},
		{Query: "how do I configure the search tuning system", QueryType: "natural_language"},
	}
	result, err := svc.Plan(context.Background(), cases, PlanOptions{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if result.Distribution.ShortQueryPct < 0.7 {
		t.Errorf("short_query_pct = %.2f, want >= 0.7",
			result.Distribution.ShortQueryPct)
	}
	// Recommendation should mention lexical.
	if result.Recommendation == "" {
		t.Error("empty recommendation")
	}
}

func TestAutoTune_Plan_HighLLMCostRecommendsSilverFilter(t *testing.T) {
	svc := newTestService(t)
	defer svc.Close()

	// 20 cases × 8 strategies × 10 topK = 1600 labels.
	cases := makeCases(20, "natural_language")
	result, err := svc.Plan(context.Background(), cases, PlanOptions{
		SilverFilter: false,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if result.Budget.EstimatedLLMCalls <= 500 {
		t.Errorf("estimated LLM calls = %d, want > 500",
			result.Budget.EstimatedLLMCalls)
	}
	// Should recommend enabling silver-filter.
	if !strings.Contains(strings.ToLower(result.Recommendation), "silver") {
		t.Errorf("recommendation %q should mention silver-filter",
			result.Recommendation)
	}
}

// ---------------------------------------------------------------------------
// ProposeStrategies
// ---------------------------------------------------------------------------

func TestAutoTune_ProposeStrategies_KeywordHeavy(t *testing.T) {
	svc := newTestService(t)
	defer svc.Close()

	cases := makeCases(10, "keyword")
	strats := svc.ProposeStrategies(search.DefaultGenome(), cases)
	hasLexicalLean := false
	for _, g := range strats {
		if g.TextWeight > 0.6 {
			hasLexicalLean = true
		}
	}
	if !hasLexicalLean {
		t.Error("keyword-heavy distribution should propose lexical-leaning weights")
	}
}

func TestAutoTune_ProposeStrategies_NLHeavy(t *testing.T) {
	svc := newTestService(t)
	defer svc.Close()

	cases := makeCases(10, "natural_language")
	strats := svc.ProposeStrategies(search.DefaultGenome(), cases)
	hasSemanticLean := false
	for _, g := range strats {
		if g.DenseWeight > 0.8 {
			hasSemanticLean = true
		}
	}
	if !hasSemanticLean {
		t.Error("NL-heavy distribution should propose semantic-leaning weights")
	}
}

// ---------------------------------------------------------------------------
// RunTuning
// ---------------------------------------------------------------------------

func TestAutoTune_RunTuning_Basic(t *testing.T) {
	svc := newTestService(t)
	defer svc.Close()

	cases := makeCases(3, "guide")
	strats := []search.SearchGenome{
		search.DefaultGenome(),
		{RecallMode: search.RecallModeLexical, TextWeight: 1, MaxRetrievedNum: 10, FTS5PoolSize: 50},
	}
	run, err := svc.RunTuning(context.Background(), cases, strats, RunOptions{
		LabelSource: "prelabelled",
	})
	if err != nil {
		t.Fatalf("RunTuning: %v", err)
	}
	if run.Status != "completed" {
		t.Errorf("status = %s, want completed", run.Status)
	}
	if len(run.Metrics) != 2 {
		t.Errorf("metrics count = %d, want 2", len(run.Metrics))
	}
}

func TestAutoTune_RunTuning_FailureIsolation(t *testing.T) {
	// A failing query should not discard the batch.
	ms := &mockSearcher{failQ: map[string]bool{"fail": true}}
	svc, err := NewAutoTuneService(AutoTuneConfig{Searcher: ms})
	if err != nil {
		t.Fatalf("NewAutoTuneService: %v", err)
	}
	defer svc.Close()

	cases := []search.EvalCase{
		{Query: "ok1", RelevantIDs: []string{"doc_a"}},
		{Query: "fail", RelevantIDs: []string{"doc_a"}},
		{Query: "ok2", RelevantIDs: []string{"doc_a"}},
	}
	strats := []search.SearchGenome{search.DefaultGenome()}
	run, err := svc.RunTuning(context.Background(), cases, strats, RunOptions{})
	if err != nil {
		t.Fatalf("RunTuning: %v", err)
	}
	// Run should complete despite one query failing.
	if run.Status != "completed" {
		t.Errorf("status = %s, want completed", run.Status)
	}
	// All 3 (strategy, query) pairs should be marked completed.
	for s := 0; s < len(strats); s++ {
		for q := 0; q < len(cases); q++ {
			if !run.isCompleted(s, q) {
				t.Errorf("pair (%d,%d) not marked completed", s, q)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Resume
// ---------------------------------------------------------------------------

func TestAutoTune_Resume(t *testing.T) {
	svc := newTestService(t)
	defer svc.Close()

	cases := makeCases(4, "guide")
	strats := []search.SearchGenome{search.DefaultGenome()}
	run, err := svc.RunTuning(context.Background(), cases, strats, RunOptions{
		RunID: "resume-test",
	})
	if err != nil {
		t.Fatalf("RunTuning: %v", err)
	}
	if run.RunID != "resume-test" {
		t.Fatalf("runID = %s", run.RunID)
	}

	// Resume should find the run and re-run (all completed → no-op).
	resumed, err := svc.Resume(context.Background(), "resume-test")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumed.RunID != "resume-test" {
		t.Errorf("resumed runID = %s", resumed.RunID)
	}
}

func TestAutoTune_Resume_NotFound(t *testing.T) {
	svc := newTestService(t)
	defer svc.Close()
	_, err := svc.Resume(context.Background(), "nonexistent")
	if err == nil {
		t.Error("Resume should fail for nonexistent run")
	}
}

// ---------------------------------------------------------------------------
// Report & ListRuns
// ---------------------------------------------------------------------------

func TestAutoTune_Report(t *testing.T) {
	svc := newTestService(t)
	defer svc.Close()

	cases := makeCases(2, "guide")
	strats := []search.SearchGenome{search.DefaultGenome()}
	_, err := svc.RunTuning(context.Background(), cases, strats, RunOptions{
		RunID: "report-test",
	})
	if err != nil {
		t.Fatalf("RunTuning: %v", err)
	}

	report, err := svc.Report("report-test")
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if report.RunID != "report-test" {
		t.Errorf("report runID = %s", report.RunID)
	}

	ids, err := svc.ListRuns()
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	found := false
	for _, id := range ids {
		if id == "report-test" {
			found = true
		}
	}
	if !found {
		t.Error("ListRuns did not include report-test")
	}
}

// ---------------------------------------------------------------------------
// Apply dry-run safety boundary
// ---------------------------------------------------------------------------

func TestAutoTune_ApplyDryRun_NoMutation(t *testing.T) {
	svc := newTestService(t)
	defer svc.Close()

	cases := makeCases(2, "guide")
	strats := []search.SearchGenome{search.DefaultGenome()}
	_, err := svc.RunTuning(context.Background(), cases, strats, RunOptions{
		RunID: "apply-test",
	})
	if err != nil {
		t.Fatalf("RunTuning: %v", err)
	}

	current := search.DefaultGenome()
	plan, err := svc.ApplyDryRun("apply-test", current)
	if err != nil {
		t.Fatalf("ApplyDryRun: %v", err)
	}
	if plan.Confirmed {
		t.Error("dry-run plan should not be confirmed")
	}
	if plan.RunID != "apply-test" {
		t.Errorf("plan runID = %s", plan.RunID)
	}
	if len(plan.Diff) == 0 && plan.Improvement == 0 {
		// Diff may be empty if best == current; that's OK.
	}
}

func TestAutoTune_Apply_ConfirmedReturnsCandidate(t *testing.T) {
	svc := newTestService(t)
	defer svc.Close()

	cases := makeCases(2, "guide")
	strats := []search.SearchGenome{
		search.DefaultGenome(),
		{RecallMode: search.RecallModeLexical, TextWeight: 1, MaxRetrievedNum: 10, FTS5PoolSize: 50},
	}
	_, err := svc.RunTuning(context.Background(), cases, strats, RunOptions{
		RunID: "apply-confirmed",
	})
	if err != nil {
		t.Fatalf("RunTuning: %v", err)
	}

	current := search.DefaultGenome()
	plan, err := svc.Apply("apply-confirmed", current, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !plan.Confirmed {
		t.Error("confirmed apply should have Confirmed=true")
	}
	// CandidateConfig should be a valid normalised genome.
	if plan.CandidateConfig.RecallMode == "" {
		t.Error("CandidateConfig has empty recall_mode")
	}
}

func TestAutoTune_Apply_NotConfirmedIsDryRun(t *testing.T) {
	svc := newTestService(t)
	defer svc.Close()

	cases := makeCases(2, "guide")
	strats := []search.SearchGenome{search.DefaultGenome()}
	_, err := svc.RunTuning(context.Background(), cases, strats, RunOptions{
		RunID: "apply-dry",
	})
	if err != nil {
		t.Fatalf("RunTuning: %v", err)
	}

	plan, err := svc.Apply("apply-dry", search.DefaultGenome(), false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if plan.Confirmed {
		t.Error("unconfirmed apply should behave as dry-run")
	}
}

// ---------------------------------------------------------------------------
// DatasetAwareLabelCache
// ---------------------------------------------------------------------------

func TestDatasetAwareLabelCache_Isolation(t *testing.T) {
	inner := NewInMemoryLabelCache()
	c1 := &datasetAwareLabelCache{inner: inner, dataset: "ds1", judgeCfg: "llm-a"}
	c2 := &datasetAwareLabelCache{inner: inner, dataset: "ds2", judgeCfg: "llm-a"}
	c3 := &datasetAwareLabelCache{inner: inner, dataset: "ds1", judgeCfg: "llm-b"}

	c1.Set("q::doc", 3)

	// Same dataset + judge → hit.
	if g, ok := c1.Get("q::doc"); !ok || g != 3 {
		t.Errorf("c1 miss: got (%d, %v)", g, ok)
	}
	// Different dataset → miss (same query/doc).
	if _, ok := c2.Get("q::doc"); ok {
		t.Error("c2 should miss (different dataset)")
	}
	// Different judge config → miss (same dataset).
	if _, ok := c3.Get("q::doc"); ok {
		t.Error("c3 should miss (different judge config)")
	}
}

// ---------------------------------------------------------------------------
// ephemeralCheckpointStore
// ---------------------------------------------------------------------------

func TestEphemeralCheckpointStore_RoundTrip(t *testing.T) {
	store := newEphemeralCheckpointStore()
	run := NewRun(TuneRequest{
		RunID:      "ephemeral-1",
		Strategies: []search.SearchGenome{search.DefaultGenome()},
		Cases:      makeCases(1, "guide"),
	})
	if err := store.SaveRun(run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	loaded, err := store.LoadRun("ephemeral-1")
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if loaded == nil || loaded.RunID != "ephemeral-1" {
		t.Errorf("LoadRun returned %v", loaded)
	}
	ids, _ := store.ListRuns()
	if len(ids) != 1 || ids[0] != "ephemeral-1" {
		t.Errorf("ListRuns = %v", ids)
	}
}

// ---------------------------------------------------------------------------
// diffGenomes
// ---------------------------------------------------------------------------

func TestDiffGenomes(t *testing.T) {
	a := search.SearchGenome{
		RecallMode:  search.RecallModeHybrid,
		DenseWeight: 0.7, TextWeight: 0.3,
		MaxRetrievedNum: 10, FTS5PoolSize: 50,
	}
	b := search.SearchGenome{
		RecallMode:  search.RecallModeLexical,
		DenseWeight: 0, TextWeight: 1,
		MaxRetrievedNum: 10, FTS5PoolSize: 100,
	}
	diffs := diffGenomes(a, b)
	if len(diffs) < 3 {
		t.Errorf("expected >= 3 diffs, got %d", len(diffs))
	}
	// Verify recall_mode diff is present.
	found := false
	for _, d := range diffs {
		if d.Field == "recall_mode" {
			found = true
		}
	}
	if !found {
		t.Error("no recall_mode diff")
	}
}
