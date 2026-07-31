// Package tune implements the search strategy tuning engine.
//
// It provides batch evaluation of multiple SearchGenome strategies
// against a set of evaluation queries, computing quality metrics
// (NDCG, MRR, Precision, etc.) for each strategy. This is the
// wukong-native equivalent of volcengine/SearchCLI's
// `vs search tune run` command.
//
// The engine is backend-agnostic: it accepts a Searcher interface
// that wraps any search backend (CortexStore, RecallStore, etc.)
// and a Judge interface for relevance labeling (LLM Judge or
// pre-labelled data).
package tune

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/km269/wukong/internal/search"
)

// ----------------------------------------------------------------------------
// Interfaces
// ----------------------------------------------------------------------------

// Searcher abstracts a search backend for evaluation.
// Implementations wrap CortexStore, RecallStore, or any other
// retrieval system to execute searches with a specific genome.
type Searcher interface {
	// SearchWithGenome executes a search with the given genome
	// and query, returning ranked results.
	SearchWithGenome(
		ctx context.Context,
		query string,
		genome search.SearchGenome,
	) ([]search.RankedItem, error)
}

// Judge evaluates the relevance of a document to a query.
// Returns a graded relevance score (0 = irrelevant, 3 = highly
// relevant). Implementations include LLMJudge and PreLabelledJudge.
type Judge interface {
	// Judge evaluates the relevance of docContent to query.
	Judge(
		ctx context.Context,
		query, docContent string,
	) (int, error)
}

// LabelCache caches relevance judgments to avoid redundant
// LLM calls across strategies that return the same documents.
// The cache key must include query + docContent + judge config
// so that stale labels are not reused when the judge changes.
type LabelCache interface {
	// Get returns the cached grade for the key, or false if absent.
	Get(key string) (int, bool)
	// Set stores a grade for the key.
	Set(key string, grade int)
}

// CheckpointStore persists run state for resume after interruption.
type CheckpointStore interface {
	// SaveRun persists the current run state.
	SaveRun(run *TuneRun) error
	// LoadRun retrieves a run by ID.
	LoadRun(runID string) (*TuneRun, error)
	// ListRuns returns all run IDs.
	ListRuns() ([]string, error)
}

// ----------------------------------------------------------------------------
// Request / Response types
// ----------------------------------------------------------------------------

// TuneRequest specifies the parameters for a tuning run.
type TuneRequest struct {
	// RunID is a unique identifier for this run. If empty, one
	// is generated automatically.
	RunID string

	// Strategies is the list of candidate genomes to evaluate.
	Strategies []search.SearchGenome

	// Cases is the evaluation query set.
	Cases []search.EvalCase

	// TopK controls how many results are evaluated per query.
	// Defaults to the genome's MaxRetrievedNum.
	TopK int

	// Concurrency controls parallel search requests. Default 4.
	Concurrency int

	// LabelSource specifies how relevance is determined:
	//   "prelabelled" - use RelevanceGrades from EvalCase
	//   "llm"         - use LLM Judge
	//   "silver"      - silver-label fast pass (source item recall)
	LabelSource string

	// Resume indicates whether to resume an interrupted run.
	Resume bool
}

// TuneRun holds the complete state of a tuning run.
type TuneRun struct {
	RunID     string      `json:"run_id"`
	Request   TuneRequest `json:"request"`
	StartedAt time.Time   `json:"started_at"`
	UpdatedAt time.Time   `json:"updated_at"`
	Status    string      `json:"status"` // running | completed | interrupted
	// Results maps strategy index → per-query results.
	Results [][]search.RankedItem `json:"results"`
	// Metrics maps strategy index → aggregated metrics.
	Metrics []search.StrategyMetrics `json:"metrics"`
	// LatenciesMs maps strategy index → per-query latencies.
	LatenciesMs [][]float64 `json:"latencies_ms"`
	// Completed tracks which (strategy, query) pairs are done.
	Completed [][]bool `json:"completed"`
	// LabelsCache stores LLM judge labels for reuse.
	Labels map[string]int `json:"labels,omitempty"`
}

// NewRun creates a new TuneRun with initialised state.
func NewRun(req TuneRequest) *TuneRun {
	nStrat := len(req.Strategies)
	nQuery := len(req.Cases)
	return &TuneRun{
		RunID:       req.RunID,
		Request:     req,
		StartedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Status:      "running",
		Results:     make([][]search.RankedItem, nStrat*nQuery),
		Metrics:     make([]search.StrategyMetrics, nStrat),
		LatenciesMs: make([][]float64, nStrat),
		Completed:   make([][]bool, nStrat),
		Labels:      make(map[string]int),
	}
}

// idx returns the flat index for (strategyIdx, queryIdx).
func (r *TuneRun) idx(s, q int) int {
	return s*len(r.Request.Cases) + q
}

// isCompleted checks if a (strategy, query) pair is done.
func (r *TuneRun) isCompleted(s, q int) bool {
	if s >= len(r.Completed) {
		return false
	}
	if q >= len(r.Completed[s]) {
		return false
	}
	return r.Completed[s][q]
}

// ----------------------------------------------------------------------------
// Tuner
// ----------------------------------------------------------------------------

// Tuner runs batch evaluation of search strategies.
type Tuner struct {
	searcher   Searcher
	judge      Judge
	cache      LabelCache
	checkpoint CheckpointStore
	logger     *slog.Logger
}

// NewTuner creates a new tuner with the given components.
// cache and checkpoint may be nil (no caching / no resume).
func NewTuner(
	searcher Searcher,
	judge Judge,
	cache LabelCache,
	checkpoint CheckpointStore,
	logger *slog.Logger,
) *Tuner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Tuner{
		searcher:   searcher,
		judge:      judge,
		cache:      cache,
		checkpoint: checkpoint,
		logger:     logger,
	}
}

// Run executes the tuning run. If req.Resume is true and a
// checkpoint exists, the run continues from where it left off.
func (t *Tuner) Run(
	ctx context.Context, req TuneRequest,
) (*TuneRun, error) {
	if len(req.Strategies) == 0 {
		return nil, fmt.Errorf("no strategies to evaluate")
	}
	if len(req.Cases) == 0 {
		return nil, fmt.Errorf("no evaluation cases")
	}
	if req.Concurrency <= 0 {
		req.Concurrency = 4
	}
	if req.TopK <= 0 {
		// Use the first strategy's TopK as default.
		req.TopK = req.Strategies[0].EffectiveTopK()
	}
	if req.RunID == "" {
		req.RunID = fmt.Sprintf("run_%d", time.Now().Unix())
	}

	// Resume or create new run.
	var run *TuneRun
	if req.Resume && t.checkpoint != nil {
		existing, err := t.checkpoint.LoadRun(req.RunID)
		if err == nil && existing != nil {
			t.logger.Info("resuming run",
				slog.String("run_id", req.RunID),
				slog.Int("strategies", len(existing.Request.Strategies)))
			run = existing
			run.Status = "running"
		}
	}
	if run == nil {
		run = NewRun(req)
	}

	// Initialise completed tracking.
	for i := range run.Completed {
		if len(run.Completed[i]) < len(req.Cases) {
			run.Completed[i] = make([]bool, len(req.Cases))
		}
	}

	// Run evaluation.
	t.runEvaluation(ctx, run)

	// Compute metrics.
	t.computeMetrics(run)

	// Finalise.
	run.Status = "completed"
	run.UpdatedAt = time.Now()

	// Save final checkpoint.
	if t.checkpoint != nil {
		if err := t.checkpoint.SaveRun(run); err != nil {
			t.logger.Warn("checkpoint save failed",
				slog.Any("error", err))
		}
	}

	return run, nil
}

// runEvaluation executes all (strategy, query) pairs with
// bounded concurrency.
func (t *Tuner) runEvaluation(ctx context.Context, run *TuneRun) {
	type evalTask struct {
		stratIdx int
		queryIdx int
		genome   search.SearchGenome
		query    string
	}

	var tasks []evalTask
	for si, genome := range run.Request.Strategies {
		for qi, ec := range run.Request.Cases {
			if run.isCompleted(si, qi) {
				continue
			}
			tasks = append(tasks, evalTask{
				stratIdx: si,
				queryIdx: qi,
				genome:   genome,
				query:    ec.Query,
			})
		}
	}

	t.logger.Info("starting evaluation",
		slog.Int("tasks", len(tasks)),
		slog.Int("strategies", len(run.Request.Strategies)),
		slog.Int("queries", len(run.Request.Cases)),
		slog.Int("concurrency", run.Request.Concurrency))

	// Bounded concurrency via semaphore.
	sem := make(chan struct{}, run.Request.Concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex

	saveCheckpoint := func() {
		if t.checkpoint != nil {
			mu.Lock()
			run.UpdatedAt = time.Now()
			_ = t.checkpoint.SaveRun(run)
			mu.Unlock()
		}
	}

	checkpointTicker := time.NewTicker(5 * time.Second)
	defer checkpointTicker.Stop()
	go func() {
		for range checkpointTicker.C {
			saveCheckpoint()
		}
	}()

	for _, task := range tasks {
		select {
		case <-ctx.Done():
			run.Status = "interrupted"
			saveCheckpoint()
			return
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(t2 evalTask) {
			defer wg.Done()
			defer func() { <-sem }()

			start := time.Now()
			results, err := t.searcher.SearchWithGenome(
				ctx, t2.query, t2.genome)
			latency := float64(time.Since(start).Microseconds()) / 1000.0

			mu.Lock()
			defer mu.Unlock()

			flatIdx := run.idx(t2.stratIdx, t2.queryIdx)
			if err != nil {
				t.logger.Warn("search failed",
					slog.Int("strategy", t2.stratIdx),
					slog.Int("query", t2.queryIdx),
					slog.Any("error", err))
				run.Results[flatIdx] = nil
			} else {
				run.Results[flatIdx] = results
			}

			// Track latency.
			if len(run.LatenciesMs[t2.stratIdx]) <= t2.queryIdx {
				// Extend slice.
				needed := t2.queryIdx + 1
				newSlice := make([]float64, needed)
				copy(newSlice, run.LatenciesMs[t2.stratIdx])
				run.LatenciesMs[t2.stratIdx] = newSlice
			}
			run.LatenciesMs[t2.stratIdx][t2.queryIdx] = latency

			// Mark completed.
			if len(run.Completed[t2.stratIdx]) <= t2.queryIdx {
				run.Completed[t2.stratIdx] = make([]bool, len(run.Request.Cases))
			}
			run.Completed[t2.stratIdx][t2.queryIdx] = true
		}(task)
	}

	wg.Wait()
	saveCheckpoint()
}

// computeMetrics calculates per-strategy metrics from collected results.
func (t *Tuner) computeMetrics(run *TuneRun) {
	topK := run.Request.TopK

	for si, genome := range run.Request.Strategies {
		var perQuery []search.QueryMetrics
		var latencies []float64

		for qi, ec := range run.Request.Cases {
			flatIdx := run.idx(si, qi)
			results := run.Results[flatIdx]

			latency := 0.0
			if qi < len(run.LatenciesMs[si]) {
				latency = run.LatenciesMs[si][qi]
			}
			latencies = append(latencies, latency)

			// Determine labels.
			var labels search.RelevanceLabels
			if ec.HasLabels() {
				// Use pre-labelled data.
				labels = ec.Labels()
			} else if t.judge != nil {
				// Use LLM Judge to label results.
				labels = t.judgeResults(
					context.Background(),
					ec.Query, results, topK,
				)
			}

			qm := search.QueryMetrics{
				Query:       ec.Query,
				ResultCount: len(results),
				LatencyMs:   latency,
			}
			if len(labels) > 0 {
				qm.NDCG20 = search.NDCG(results, labels, min(topK, 20))
				qm.NDCG10 = search.NDCG(results, labels, min(topK, 10))
				qm.MRR10 = search.MRR(results, labels, min(topK, 10))
				qm.Precision10 = search.Precision(results, labels, min(topK, 10))
				qm.Recall10 = search.Recall(results, labels, min(topK, 10))
			}
			perQuery = append(perQuery, qm)
		}

		run.Metrics[si] = search.AggregateQueryMetrics(
			genome, perQuery, latencies)
	}
}

// judgeResults uses the LLM Judge to label search results.
// Results without a cached label are judged individually.
func (t *Tuner) judgeResults(
	ctx context.Context,
	query string,
	results []search.RankedItem,
	topK int,
) search.RelevanceLabels {
	labels := make(search.RelevanceLabels)
	if t.judge == nil {
		return labels
	}

	k := topK
	if k > len(results) {
		k = len(results)
	}

	for i := 0; i < k; i++ {
		item := results[i]
		if item.ID == "" {
			continue
		}

		// Check cache first.
		cacheKey := labelCacheKey(query, item.ID)
		if t.cache != nil {
			if grade, ok := t.cache.Get(cacheKey); ok {
				labels[item.ID] = grade
				continue
			}
		}

		// Judge with LLM.
		grade, err := t.judge.Judge(ctx, query, item.ID)
		if err != nil {
			t.logger.Warn("judge failed",
				slog.String("query", query),
				slog.String("doc", item.ID),
				slog.Any("error", err))
			continue
		}

		labels[item.ID] = grade

		// Cache the label.
		if t.cache != nil {
			t.cache.Set(cacheKey, grade)
		}
	}

	return labels
}

// labelCacheKey generates a cache key for a (query, doc) pair.
func labelCacheKey(query, docID string) string {
	return fmt.Sprintf("%s::%s", query, docID)
}

// ----------------------------------------------------------------------------
// Report
// ----------------------------------------------------------------------------

// TuneReport summarises a tuning run for human review.
type TuneReport struct {
	RunID         string                   `json:"run_id"`
	BestStrategy  search.SearchGenome      `json:"best_strategy"`
	BestScore     float64                  `json:"best_score"`
	AllMetrics    []search.StrategyMetrics `json:"all_metrics"`
	Improvement   float64                  `json:"improvement_vs_baseline"` // best NDCG@20 vs first strategy
	TotalQueries  int                      `json:"total_queries"`
	TotalSearches int                      `json:"total_searches"`
	Duration      string                   `json:"duration"`
}

// GenerateReport creates a human-readable report from a TuneRun.
func GenerateReport(run *TuneRun) TuneReport {
	report := TuneReport{
		RunID:         run.RunID,
		AllMetrics:    run.Metrics,
		TotalQueries:  len(run.Request.Cases),
		TotalSearches: len(run.Request.Strategies) * len(run.Request.Cases),
		Duration:      time.Since(run.StartedAt).Round(time.Second).String(),
	}

	if len(run.Metrics) == 0 {
		return report
	}

	// Find best strategy by RobustScore.
	bestIdx := 0
	bestScore := run.Metrics[0].RobustScore(search.RobustScoreParams{})
	report.BestScore = bestScore
	report.BestStrategy = run.Metrics[0].Genome

	for i := 1; i < len(run.Metrics); i++ {
		score := run.Metrics[i].RobustScore(search.RobustScoreParams{})
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	report.BestStrategy = run.Metrics[bestIdx].Genome
	report.BestScore = bestScore

	// Improvement vs baseline (first strategy).
	if len(run.Metrics) > 0 {
		baselineNDCG := run.Metrics[0].NDCG20
		bestNDCG := run.Metrics[bestIdx].NDCG20
		if baselineNDCG > 0 {
			report.Improvement = (bestNDCG - baselineNDCG) / baselineNDCG
		}
	}

	return report
}
