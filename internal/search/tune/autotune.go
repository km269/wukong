// Package tune implements the search strategy tuning engine.
//
// This file implements AutoTuneService — a high-level programmatic
// API that wraps the Tuner with distribution-aware strategy
// proposal, cost prediction, content-aware label caching, and
// checkpoint/resume + apply-with-safety-boundary.
//
// Unlike the CLI (search_tune.go), this service is callable
// directly by the agent runtime with in-memory objects (no file
// paths, no flags). It exists so an Agent can automate the full
// "propose → plan → run → report → apply" loop without spawning
// a subprocess.
package tune

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/km269/wukong/internal/provider"
	"github.com/km269/wukong/internal/search"
)

// ============================================================================
// AutoTuneService
// ============================================================================

// AutoTuneConfig configures an AutoTuneService.
type AutoTuneConfig struct {
	// Searcher executes searches with a genome. Required for Run.
	Searcher Searcher

	// Factory creates LLM models for the Judge. Optional; only
	// needed when LabelSource == "llm".
	Factory *provider.Factory

	// JudgeModel is the model name used for LLM judging. Optional.
	JudgeModel string

	// CheckpointPath is the SQLite path for persisting run state.
	// If empty, an in-memory ephemeral store is used (no resume).
	CheckpointPath string

	// DatasetLabel identifies the dataset for cache key isolation.
	// Labels from different datasets never collide. Default "default".
	DatasetLabel string

	// Logger. Defaults to slog.Default().
	Logger *slog.Logger
}

// AutoTuneService provides a programmatic tuning API for the agent.
//
// It composes the lower-level Tuner, Optimizer, and CheckpointStore
// into a single workflow:
//
//	Plan → ProposeStrategies → RunTuning → Report → ApplyDryRun → Apply
//
// Every method accepts in-memory objects (EvalCase slices, genomes)
// so the agent can call them without file I/O or CLI flags.
type AutoTuneService struct {
	searcher Searcher
	factory  *provider.Factory
	judge    Judge
	cache    LabelCache
	store    CheckpointStore
	dataset  string
	logger   *slog.Logger
}

// NewAutoTuneService creates a service from the config.
// Opens the checkpoint store if CheckpointPath is set; otherwise
// uses an ephemeral in-memory store (no cross-process resume).
func NewAutoTuneService(cfg AutoTuneConfig) (*AutoTuneService, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.DatasetLabel == "" {
		cfg.DatasetLabel = "default"
	}

	svc := &AutoTuneService{
		searcher: cfg.Searcher,
		factory:  cfg.Factory,
		cache:    NewInMemoryLabelCache(),
		dataset:  cfg.DatasetLabel,
		logger:   cfg.Logger,
	}

	if cfg.CheckpointPath != "" {
		store, err := NewSQLiteCheckpointStore(cfg.CheckpointPath)
		if err != nil {
			return nil, fmt.Errorf(
				"autotune: open checkpoint: %w", err)
		}
		svc.store = store
	} else {
		svc.store = newEphemeralCheckpointStore()
	}

	// Build LLM judge eagerly if factory + model are available.
	if cfg.Factory != nil && cfg.JudgeModel != "" {
		svc.judge = NewLLMJudge(cfg.Factory, cfg.JudgeModel)
	}

	return svc, nil
}

// Close releases resources (checkpoint DB).
func (s *AutoTuneService) Close() error {
	if c, ok := s.store.(interface{ Close() error }); ok {
		return c.Close()
	}
	return nil
}

// ============================================================================
// Plan: predict cost without running searches or LLM
// ============================================================================

// PlanOptions controls plan computation.
type PlanOptions struct {
	// TopK results per query. Default 10.
	TopK int
	// PopulationSize caps the number of candidate strategies. Default 8.
	PopulationSize int
	// SilverFilter enables fast-pass elimination (reduces LLM calls).
	SilverFilter bool
	// MultiFidelity enables three-pass evaluation.
	MultiFidelity bool
	// Baseline is the current production strategy. If empty, DefaultGenome.
	Baseline search.SearchGenome
}

// PlanResult is the output of Plan: everything the agent needs to
// decide whether to proceed, narrow scope, or switch label source.
type PlanResult struct {
	// Validation summary of the query set.
	Validation search.ValidationReport `json:"validation"`
	// Proposed candidate strategies.
	Strategies []search.SearchGenome `json:"strategies"`
	// Budget prediction.
	Budget search.TunePlan `json:"budget"`
	// Distribution analysis that informed the strategy proposal.
	Distribution QueryDistribution `json:"distribution"`
	// Recommendation for the agent.
	Recommendation string `json:"recommendation"`
}

// QueryDistribution summarises the query set's shape.
type QueryDistribution struct {
	Total         int            `json:"total"`
	ByType        map[string]int `json:"by_type"`
	AvgQueryLen   float64        `json:"avg_query_len"`
	ShortQueryPct float64        `json:"short_query_pct"` // <= 3 words
	LabelCoverage float64        `json:"label_coverage"`
}

// Plan validates the cases, analyses their distribution, proposes
// candidate strategies, and predicts the evaluation budget — all
// without executing any search or LLM call. This lets the agent
// shrink scope or switch label sources before paying the cost.
func (s *AutoTuneService) Plan(
	ctx context.Context,
	cases []search.EvalCase,
	opts PlanOptions,
) (*PlanResult, error) {
	if len(cases) == 0 {
		return nil, fmt.Errorf("autotune plan: no cases")
	}
	if opts.TopK <= 0 {
		opts.TopK = 10
	}
	if opts.PopulationSize <= 0 {
		opts.PopulationSize = 8
	}
	if opts.Baseline.RecallMode == "" {
		opts.Baseline = search.DefaultGenome()
	}

	validation := search.ValidateCases(cases)
	dist := analyseDistribution(cases)
	strategies := s.ProposeStrategies(opts.Baseline, cases)
	if len(strategies) > opts.PopulationSize {
		strategies = strategies[:opts.PopulationSize]
	}

	budget := search.ComputePlan(
		len(strategies), len(cases), opts.TopK, opts.SilverFilter)

	rec := s.recommend(validation, dist, budget, opts)

	return &PlanResult{
		Validation:     validation,
		Strategies:     strategies,
		Budget:         budget,
		Distribution:   dist,
		Recommendation: rec,
	}, nil
}

// recommend produces a human-readable recommendation for the agent.
func (s *AutoTuneService) recommend(
	v search.ValidationReport,
	d QueryDistribution,
	b search.TunePlan,
	opts PlanOptions,
) string {
	var parts []string
	if len(v.Errors) > 0 {
		parts = append(parts, "fix validation errors before running")
	}
	if d.ShortQueryPct > 0.5 {
		parts = append(parts,
			"query set is keyword-heavy: lexical boundary likely competitive")
	}
	if b.EstimatedLLMCalls > 500 && opts.SilverFilter {
		parts = append(parts,
			"high LLM cost: keep silver-filter on or add pre-labels")
	}
	if b.EstimatedLLMCalls > 500 && !opts.SilverFilter {
		parts = append(parts,
			"high LLM cost: enable silver-filter to cut ~30% of judging")
	}
	if v.LabelCoverage < 0.3 {
		parts = append(parts,
			"low label coverage: use llm or silver label source")
	}
	if opts.MultiFidelity && b.StrategyCount <= 3 {
		parts = append(parts,
			"multi-fidelity not worthwhile with <=3 strategies")
	}
	if len(parts) == 0 {
		return "ready to run"
	}
	return strings.Join(parts, "; ")
}

// ============================================================================
// ProposeStrategies: distribution-aware candidate generation
// ============================================================================

// ProposeStrategies generates candidate strategies informed by the
// query distribution. It starts from GenerateInitialPopulation
// (baseline + boundaries + grid) and adds distribution-specific
// candidates:
//
//   - keyword-heavy sets → more lexical-leaning weights
//   - natural-language sets → more semantic-leaning weights
//   - low label coverage → smaller candidate pools (cheaper judging)
func (s *AutoTuneService) ProposeStrategies(
	baseline search.SearchGenome,
	cases []search.EvalCase,
) []search.SearchGenome {
	cfg := OptimizerConfig{Baseline: baseline}
	pop := GenerateInitialPopulation(cfg)

	if len(cases) == 0 {
		return pop
	}

	dist := analyseDistribution(cases)
	extra := proposeFromDistribution(baseline, dist)
	for _, g := range extra {
		if !containsGenome(pop, g) {
			pop = append(pop, g)
		}
	}
	return pop
}

// proposeFromDistribution adds candidates tailored to the query mix.
func proposeFromDistribution(
	baseline search.SearchGenome,
	dist QueryDistribution,
) []search.SearchGenome {
	var out []search.SearchGenome
	pool := baseline.FTS5PoolSize
	topK := baseline.MaxRetrievedNum

	// Keyword-heavy: try lexical-leaning hybrid weights.
	if dist.ShortQueryPct > 0.4 {
		for _, tw := range []float64{0.6, 0.8} {
			out = append(out, search.SearchGenome{
				RecallMode:      search.RecallModeHybrid,
				DenseWeight:     1 - tw,
				TextWeight:      tw,
				MaxRetrievedNum: topK,
				FTS5PoolSize:    pool,
			}.Normalized())
		}
		// Pure lexical boundary (may already exist, dedup later).
		out = append(out, search.SearchGenome{
			RecallMode:  search.RecallModeLexical,
			DenseWeight: 0, TextWeight: 1,
			MaxRetrievedNum: topK,
			FTS5PoolSize:    pool,
		})
	}

	// Natural-language / long-tail: semantic-leaning weights.
	nlPct := float64(dist.ByType["natural_language"]+dist.ByType["long_tail"]) /
		float64(max(dist.Total, 1))
	if nlPct > 0.4 {
		for _, dw := range []float64{0.85, 0.95} {
			out = append(out, search.SearchGenome{
				RecallMode:      search.RecallModeHybrid,
				DenseWeight:     dw,
				TextWeight:      1 - dw,
				MaxRetrievedNum: topK,
				FTS5PoolSize:    pool,
			}.Normalized())
		}
		out = append(out, search.SearchGenome{
			RecallMode:  search.RecallModeVector,
			DenseWeight: 1, TextWeight: 0,
			MaxRetrievedNum: topK,
			FTS5PoolSize:    pool,
		})
	}

	// Low label coverage: cheaper small-pool candidate.
	if dist.LabelCoverage < 0.3 {
		out = append(out, search.SearchGenome{
			RecallMode:  search.RecallModeHybrid,
			DenseWeight: 0.5, TextWeight: 0.5,
			MaxRetrievedNum: 5,
			FTS5PoolSize:    30,
		})
	}

	return out
}

// ============================================================================
// RunTuning: controlled concurrency + cache + checkpoint
// ============================================================================

// RunOptions controls a tuning run.
type RunOptions struct {
	// RunID. Auto-generated if empty.
	RunID string
	// TopK per query. Default 10.
	TopK int
	// Concurrency bounds parallel search requests. Default 4.
	Concurrency int
	// LabelSource: "prelabelled" | "llm" | "silver".
	LabelSource string
	// MultiFidelity enables three-pass evaluation.
	MultiFidelity bool
	// Resume continues an interrupted run by RunID.
	Resume bool
}

// RunTuning executes the full evaluation with bounded concurrency,
// content-aware label caching, and checkpoint persistence. A single
// (strategy, query) failure does not discard the batch — the Tuner
// records nil results and continues.
func (s *AutoTuneService) RunTuning(
	ctx context.Context,
	cases []search.EvalCase,
	strategies []search.SearchGenome,
	opts RunOptions,
) (*TuneRun, error) {
	if s.searcher == nil {
		return nil, fmt.Errorf("autotune: no searcher configured")
	}
	if len(strategies) == 0 {
		return nil, fmt.Errorf("autotune: no strategies")
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if opts.TopK <= 0 {
		opts.TopK = 10
	}
	if opts.LabelSource == "" {
		opts.LabelSource = "prelabelled"
	}

	// Wrap the cache so keys include dataset + judge config.
	judgeCfg := opts.LabelSource
	if s.judge != nil {
		judgeCfg = judgeCfg + "::" + s.judgeModelFingerprint()
	}
	cache := &datasetAwareLabelCache{
		inner:    s.cache,
		dataset:  s.dataset,
		judgeCfg: judgeCfg,
	}

	tuner := NewTuner(
		s.searcher, s.judge, cache, s.store, s.logger)

	if opts.MultiFidelity {
		result, err := tuner.RunMultiFidelity(
			ctx, strategies, cases,
			MultiFidelityConfig{}, opts.LabelSource,
		)
		if err != nil {
			return nil, fmt.Errorf("autotune multi-fidelity: %w", err)
		}
		return result.ConfirmPass, nil
	}

	run, err := tuner.Run(ctx, TuneRequest{
		RunID:       opts.RunID,
		Strategies:  strategies,
		Cases:       cases,
		TopK:        opts.TopK,
		Concurrency: opts.Concurrency,
		LabelSource: opts.LabelSource,
		Resume:      opts.Resume,
	})
	if err != nil {
		return nil, fmt.Errorf("autotune run: %w", err)
	}
	return run, nil
}

// Resume continues an interrupted run by RunID. The run's strategies
// and cases are loaded from the checkpoint; only incomplete
// (strategy, query) pairs are re-evaluated.
func (s *AutoTuneService) Resume(
	ctx context.Context,
	runID string,
) (*TuneRun, error) {
	if s.store == nil {
		return nil, fmt.Errorf("autotune: no checkpoint store")
	}
	existing, err := s.store.LoadRun(runID)
	if err != nil {
		return nil, fmt.Errorf("autotune resume: load: %w", err)
	}
	if existing == nil {
		return nil, fmt.Errorf("autotune resume: run %q not found", runID)
	}
	return s.RunTuning(ctx, existing.Request.Cases,
		existing.Request.Strategies, RunOptions{
			RunID:       runID,
			TopK:        existing.Request.TopK,
			Concurrency: existing.Request.Concurrency,
			LabelSource: existing.Request.LabelSource,
			Resume:      true,
		})
}

// ============================================================================
// Report & List
// ============================================================================

// Report generates a summary report for a completed or interrupted run.
func (s *AutoTuneService) Report(runID string) (*TuneReport, error) {
	run, err := s.store.LoadRun(runID)
	if err != nil {
		return nil, fmt.Errorf("autotune report: %w", err)
	}
	if run == nil {
		return nil, fmt.Errorf("autotune report: run %q not found", runID)
	}
	r := GenerateReport(run)
	return &r, nil
}

// ListRuns returns all persisted run IDs, most recent first.
func (s *AutoTuneService) ListRuns() ([]string, error) {
	return s.store.ListRuns()
}

// ============================================================================
// Apply: dry-run safety boundary
// ============================================================================

// ApplyPlan describes what would change if the winning strategy
// from a run were promoted to production. It is returned by both
// ApplyDryRun and Apply.
type ApplyPlan struct {
	RunID            string              `json:"run_id"`
	CurrentStrategy  search.SearchGenome `json:"current_strategy"`
	ProposedStrategy search.SearchGenome `json:"proposed_strategy"`
	Diff             []ConfigDiff        `json:"diff"`
	Improvement      float64             `json:"improvement"`
	// Confirmed is true only when Apply was called with confirmed=true.
	Confirmed bool `json:"confirmed"`
	// CandidateConfig is the YAML-ready config snippet for the
	// proposed strategy. The caller writes it to a *candidate*
	// slot, never the active config — promotion is a separate
	// human/agent decision.
	CandidateConfig search.SearchGenome `json:"candidate_config"`
}

// ConfigDiff describes a single field change.
type ConfigDiff struct {
	Field    string `json:"field"`
	OldValue any    `json:"old_value"`
	NewValue any    `json:"new_value"`
}

// ApplyDryRun shows the config that would be applied if the run's
// winning strategy were promoted. Performs NO mutation. The agent
// presents this to the user for confirmation.
func (s *AutoTuneService) ApplyDryRun(
	runID string,
	current search.SearchGenome,
) (*ApplyPlan, error) {
	return s.buildApplyPlan(runID, current, false)
}

// Apply promotes the run's winning strategy to a candidate config.
// If confirmed is false, it behaves identically to ApplyDryRun
// (safety boundary). When confirmed is true, the returned
// CandidateConfig is ready to be written to a candidate slot —
// this method NEVER mutates the live/default search config.
//
// The safety contract:
//  1. dry-run first (confirmed=false) → show diff
//  2. user/agent confirms
//  3. apply (confirmed=true) → return candidate config
//  4. promotion to default entry is a separate explicit step
func (s *AutoTuneService) Apply(
	runID string,
	current search.SearchGenome,
	confirmed bool,
) (*ApplyPlan, error) {
	return s.buildApplyPlan(runID, current, confirmed)
}

func (s *AutoTuneService) buildApplyPlan(
	runID string,
	current search.SearchGenome,
	confirmed bool,
) (*ApplyPlan, error) {
	run, err := s.store.LoadRun(runID)
	if err != nil {
		return nil, fmt.Errorf("autotune apply: %w", err)
	}
	if run == nil {
		return nil, fmt.Errorf("autotune apply: run %q not found", runID)
	}

	report := GenerateReport(run)
	proposed := report.BestStrategy.Normalized()
	current = current.Normalized()

	plan := &ApplyPlan{
		RunID:            runID,
		CurrentStrategy:  current,
		ProposedStrategy: proposed,
		Improvement:      report.Improvement,
		Confirmed:        confirmed,
		CandidateConfig:  proposed,
	}
	plan.Diff = diffGenomes(current, proposed)
	return plan, nil
}

// diffGenomes returns the field-level differences between two genomes.
func diffGenomes(a, b search.SearchGenome) []ConfigDiff {
	var diffs []ConfigDiff
	if a.RecallMode != b.RecallMode {
		diffs = append(diffs, ConfigDiff{
			Field: "recall_mode", OldValue: a.RecallMode, NewValue: b.RecallMode})
	}
	if a.DenseWeight != b.DenseWeight {
		diffs = append(diffs, ConfigDiff{
			Field: "dense_weight", OldValue: a.DenseWeight, NewValue: b.DenseWeight})
	}
	if a.TextWeight != b.TextWeight {
		diffs = append(diffs, ConfigDiff{
			Field: "text_weight", OldValue: a.TextWeight, NewValue: b.TextWeight})
	}
	if a.KeywordMatchPercent != b.KeywordMatchPercent {
		diffs = append(diffs, ConfigDiff{
			Field:    "keyword_match_percent",
			OldValue: a.KeywordMatchPercent, NewValue: b.KeywordMatchPercent})
	}
	if a.MaxRetrievedNum != b.MaxRetrievedNum {
		diffs = append(diffs, ConfigDiff{
			Field:    "max_retrieved_num",
			OldValue: a.MaxRetrievedNum, NewValue: b.MaxRetrievedNum})
	}
	if a.FTS5PoolSize != b.FTS5PoolSize {
		diffs = append(diffs, ConfigDiff{
			Field:    "fts5_pool_size",
			OldValue: a.FTS5PoolSize, NewValue: b.FTS5PoolSize})
	}
	return diffs
}

// ============================================================================
// DatasetAwareLabelCache
// ============================================================================

// datasetAwareLabelCache wraps a LabelCache so that cache keys
// include the dataset label and judge configuration fingerprint.
//
// This implements the SearchCLI invariant: a label is reused only
// when dataset + query + item content + judge config all match.
// When the judge model or prompt changes, judgeCfg changes, the
// prefix changes, and stale labels are naturally ignored — no
// manual cache invalidation needed.
//
// The underlying key from the Tuner is already "query::docContent"
// (the adapter sets item.ID to the content preview), so this wrapper
// only needs to add the dataset + judge prefix.
type datasetAwareLabelCache struct {
	inner    LabelCache
	dataset  string
	judgeCfg string
}

func (c *datasetAwareLabelCache) Get(key string) (int, bool) {
	return c.inner.Get(c.prefix(key))
}

func (c *datasetAwareLabelCache) Set(key string, grade int) {
	c.inner.Set(c.prefix(key), grade)
}

func (c *datasetAwareLabelCache) prefix(key string) string {
	return c.dataset + "\x1f" + c.judgeCfg + "\x1f" + key
}

// judgeModelFingerprint returns a stable fingerprint of the judge
// configuration. When the judge model changes, the fingerprint
// changes, invalidating cached labels.
func (s *AutoTuneService) judgeModelFingerprint() string {
	if s.factory == nil {
		return "no-factory"
	}
	// Hash the model name to keep the key compact and to avoid
	// delimiter collisions.
	h := sha256.Sum256([]byte(s.dataset + "/" + judgeModelName(s.judge)))
	return hex.EncodeToString(h[:8])
}

// judgeModelName extracts the model name from a Judge (if it's an
// LLMJudge), otherwise returns "prelabelled".
func judgeModelName(j Judge) string {
	if lj, ok := j.(*LLMJudge); ok && lj != nil {
		return lj.modelName
	}
	return "prelabelled"
}

// ============================================================================
// EphemeralCheckpointStore (in-memory, no persistence)
// ============================================================================

// ephemeralRun is a single in-memory run entry.
type ephemeralRun struct {
	run *TuneRun
	ts  time.Time
}

// ephemeralCheckpointStore implements CheckpointStore in memory.
// Used when no CheckpointPath is configured (no cross-process resume).
type ephemeralCheckpointStore struct {
	runs map[string]*ephemeralRun
}

func newEphemeralCheckpointStore() *ephemeralCheckpointStore {
	return &ephemeralCheckpointStore{runs: make(map[string]*ephemeralRun)}
}

func (e *ephemeralCheckpointStore) SaveRun(run *TuneRun) error {
	e.runs[run.RunID] = &ephemeralRun{run: run, ts: time.Now()}
	return nil
}

func (e *ephemeralCheckpointStore) LoadRun(runID string) (*TuneRun, error) {
	if entry, ok := e.runs[runID]; ok {
		return entry.run, nil
	}
	return nil, nil
}

func (e *ephemeralCheckpointStore) ListRuns() ([]string, error) {
	type entry struct {
		id string
		ts time.Time
	}
	entries := make([]entry, 0, len(e.runs))
	for id, r := range e.runs {
		entries = append(entries, entry{id, r.ts})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ts.After(entries[j].ts)
	})
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.id
	}
	return ids, nil
}

// ============================================================================
// Helpers
// ============================================================================

// analyseDistribution summarises the query set.
func analyseDistribution(cases []search.EvalCase) QueryDistribution {
	dist := QueryDistribution{
		Total:  len(cases),
		ByType: make(map[string]int),
	}
	totalLen := 0
	shortCount := 0
	labeled := 0
	for _, c := range cases {
		typ := c.QueryType
		if typ == "" {
			typ = "untyped"
		}
		dist.ByType[typ]++

		words := len(strings.Fields(c.Query))
		totalLen += words
		if words <= 3 {
			shortCount++
		}
		if c.HasLabels() {
			labeled++
		}
	}
	if dist.Total > 0 {
		dist.AvgQueryLen = float64(totalLen) / float64(dist.Total)
		dist.ShortQueryPct = float64(shortCount) / float64(dist.Total)
		dist.LabelCoverage = float64(labeled) / float64(dist.Total)
	}
	return dist
}

// containsGenome checks if a genome (by normalised key) is in the slice.
func containsGenome(slice []search.SearchGenome, g search.SearchGenome) bool {
	g = g.Normalized()
	for _, s := range slice {
		s = s.Normalized()
		if s.RecallMode == g.RecallMode &&
			s.DenseWeight == g.DenseWeight &&
			s.TextWeight == g.TextWeight &&
			s.MaxRetrievedNum == g.MaxRetrievedNum &&
			s.FTS5PoolSize == g.FTS5PoolSize {
			return true
		}
	}
	return false
}
