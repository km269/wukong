package search

import (
	"math"
	"sort"
)

// ============================================================================
// Search Quality Metrics
//
// Inspired by volcengine/SearchCLI's evaluation framework.
// These metrics enable quantitative comparison of search strategies,
// forming the foundation for auto-tuning (SPA).
// ============================================================================

// RankedItem represents a single search result with an identifier
// that can be matched against relevance labels.
type RankedItem struct {
	ID    string  // document/message identifier
	Score float64 // retrieval score (not used for ranking, only for tie-breaking)
}

// RelevanceLabels maps document IDs to graded relevance scores.
// Grade 0 = irrelevant, 1 = marginally relevant, 2 = relevant,
// 3 = highly relevant (following common IR conventions).
type RelevanceLabels map[string]int

// NDCG computes Normalized Discounted Cumulative Gain at position K.
//
// DCG@K = Σ(i=1..K) (2^rel_i - 1) / log2(i + 1)
// NDCG@K = DCG@K / IDCG@K
//
// where rel_i is the relevance grade of the result at position i,
// and IDCG@K is the DCG of the ideal ranking (sorted by relevance
// descending).
//
// An empty result set or all-zero relevance yields NDCG = 0.
func NDCG(items []RankedItem, labels RelevanceLabels, k int) float64 {
	if k <= 0 || len(items) == 0 || len(labels) == 0 {
		return 0.0
	}
	if k > len(items) {
		k = len(items)
	}

	// DCG: actual ranking.
	dcg := 0.0
	for i := 0; i < k; i++ {
		rel := labels[items[i].ID]
		if rel > 0 {
			dcg += (math.Pow(2, float64(rel)) - 1) /
				math.Log2(float64(i+2))
		}
	}

	// IDCG: ideal ranking (sort all labelled docs by grade desc).
	ideal := make([]int, 0, len(labels))
	for _, grade := range labels {
		ideal = append(ideal, grade)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ideal)))
	idcg := 0.0
	for i := 0; i < k && i < len(ideal); i++ {
		if ideal[i] > 0 {
			idcg += (math.Pow(2, float64(ideal[i])) - 1) /
				math.Log2(float64(i+2))
		}
	}

	if idcg == 0 {
		return 0.0
	}
	return dcg / idcg
}

// MRR computes Mean Reciprocal Rank at position K.
//
// RR = 1 / rank_of_first_relevant_result
// MRR = average RR across all queries.
//
// For a single query, MRR == RR. Returns 0 if no relevant result
// in the top K.
func MRR(items []RankedItem, labels RelevanceLabels, k int) float64 {
	if k <= 0 || len(items) == 0 || len(labels) == 0 {
		return 0.0
	}
	if k > len(items) {
		k = len(items)
	}
	for i := 0; i < k; i++ {
		if labels[items[i].ID] > 0 {
			return 1.0 / float64(i+1)
		}
	}
	return 0.0
}

// Precision computes Precision@K: the fraction of results in the
// top K that are relevant (relevance grade > 0).
//
// Precision@K = |{relevant in top K}| / K
func Precision(items []RankedItem, labels RelevanceLabels, k int) float64 {
	if k <= 0 || len(items) == 0 || len(labels) == 0 {
		return 0.0
	}
	if k > len(items) {
		k = len(items)
	}
	relevant := 0
	for i := 0; i < k; i++ {
		if labels[items[i].ID] > 0 {
			relevant++
		}
	}
	return float64(relevant) / float64(k)
}

// Recall computes Recall@K: the fraction of all relevant documents
// that appear in the top K results.
//
// Recall@K = |{relevant in top K}| / |{all relevant}|
func Recall(items []RankedItem, labels RelevanceLabels, k int) float64 {
	if k <= 0 || len(items) == 0 || len(labels) == 0 {
		return 0.0
	}
	totalRelevant := 0
	for _, grade := range labels {
		if grade > 0 {
			totalRelevant++
		}
	}
	if totalRelevant == 0 {
		return 0.0
	}
	if k > len(items) {
		k = len(items)
	}
	relevantInTopK := 0
	for i := 0; i < k; i++ {
		if labels[items[i].ID] > 0 {
			relevantInTopK++
		}
	}
	return float64(relevantInTopK) / float64(totalRelevant)
}

// ZeroResultRate computes the fraction of queries that returned
// zero results. allResults is a slice of result lists, one per query.
func ZeroResultRate(allResults [][]RankedItem) float64 {
	if len(allResults) == 0 {
		return 0.0
	}
	zeroCount := 0
	for _, results := range allResults {
		if len(results) == 0 {
			zeroCount++
		}
	}
	return float64(zeroCount) / float64(len(allResults))
}

// AvgLatencyMs computes the average latency in milliseconds
// from a slice of per-query latency values (in milliseconds).
func AvgLatencyMs(latenciesMs []float64) float64 {
	if len(latenciesMs) == 0 {
		return 0.0
	}
	sum := 0.0
	for _, l := range latenciesMs {
		sum += l
	}
	return sum / float64(len(latenciesMs))
}

// ----------------------------------------------------------------------------
// StrategyMetrics: aggregated metrics for a single candidate strategy
// ----------------------------------------------------------------------------

// StrategyMetrics holds the evaluation results for one SearchGenome
// across a set of evaluation queries.
type StrategyMetrics struct {
	Genome          SearchGenome `json:"genome"`
	NDCG20          float64      `json:"ndcg_20"`
	NDCG10          float64      `json:"ndcg_10"`
	MRR10           float64      `json:"mrr_10"`
	Precision10     float64      `json:"precision_10"`
	Recall10        float64      `json:"recall_10"`
	ZeroResultRate  float64      `json:"zero_result_rate"`
	AvgLatencyMs    float64      `json:"avg_latency_ms"`
	QueryCount      int          `json:"query_count"`
	// Per-query detail for Bad Case analysis.
	PerQuery []QueryMetrics `json:"per_query,omitempty"`
}

// QueryMetrics holds metrics for a single query.
type QueryMetrics struct {
	Query       string  `json:"query"`
	NDCG20      float64 `json:"ndcg_20"`
	NDCG10      float64 `json:"ndcg_10"`
	MRR10       float64 `json:"mrr_10"`
	Precision10 float64 `json:"precision_10"`
	Recall10    float64 `json:"recall_10"`
	ResultCount int     `json:"result_count"`
	LatencyMs   float64 `json:"latency_ms"`
}

// RobustScore computes a composite score combining multiple metrics
// with penalties, inspired by SearchCLI's SPA robust objective:
//
// RobustScore = NDCG@20 + α×MRR@10
//             - β×zero_result_rate
//             - γ×latency_penalty
//             - δ×query_type_variance
//
// The default weights (α=0.1, β=0.5, γ=0.001, δ=0.1) can be
// overridden via RobustScoreParams.
func (m StrategyMetrics) RobustScore(
	params RobustScoreParams,
) float64 {
	p := params.WithDefaults()
	score := m.NDCG20 + p.Alpha*m.MRR10
	score -= p.Beta * m.ZeroResultRate

	// Latency penalty: penalise strategies exceeding latency budget.
	if m.AvgLatencyMs > p.LatencyBudgetMs {
		excess := (m.AvgLatencyMs - p.LatencyBudgetMs) / 1000.0
		score -= p.Gamma * excess
	}

	// Query type variance: penalise inconsistency.
	if len(m.PerQuery) > 1 {
		variance := ndcgVariance(m.PerQuery)
		score -= p.Delta * variance
	}

	return score
}

// RobustScoreParams controls the weights in RobustScore.
type RobustScoreParams struct {
	Alpha            float64 // MRR weight (default 0.1)
	Beta             float64 // zero-result penalty (default 0.5)
	Gamma            float64 // latency penalty (default 0.001)
	Delta            float64 // variance penalty (default 0.1)
	LatencyBudgetMs  float64 // acceptable latency threshold (default 500ms)
}

// WithDefaults fills in default values for zero fields.
func (p RobustScoreParams) WithDefaults() RobustScoreParams {
	out := p
	if out.Alpha == 0 {
		out.Alpha = 0.1
	}
	if out.Beta == 0 {
		out.Beta = 0.5
	}
	if out.Gamma == 0 {
		out.Gamma = 0.001
	}
	if out.Delta == 0 {
		out.Delta = 0.1
	}
	if out.LatencyBudgetMs == 0 {
		out.LatencyBudgetMs = 500
	}
	return out
}

// ndcgVariance computes the variance of per-query NDCG@20 values.
func ndcgVariance(perQuery []QueryMetrics) float64 {
	if len(perQuery) < 2 {
		return 0
	}
	mean := 0.0
	for _, q := range perQuery {
		mean += q.NDCG20
	}
	mean /= float64(len(perQuery))

	sumSqDiff := 0.0
	for _, q := range perQuery {
		diff := q.NDCG20 - mean
		sumSqDiff += diff * diff
	}
	return sumSqDiff / float64(len(perQuery))
}

// ----------------------------------------------------------------------------
// Aggregation helpers
// ----------------------------------------------------------------------------

// AggregateQueryMetrics combines per-query metrics into a
// StrategyMetrics summary by averaging.
func AggregateQueryMetrics(
	genome SearchGenome,
	perQuery []QueryMetrics,
	latenciesMs []float64,
) StrategyMetrics {
	if len(perQuery) == 0 {
		return StrategyMetrics{Genome: genome}
	}

	m := StrategyMetrics{
		Genome:     genome,
		QueryCount: len(perQuery),
		PerQuery:   perQuery,
	}

	// Collect all result lists for zero-result rate.
	allResults := make([][]RankedItem, len(perQuery))

	for i, q := range perQuery {
		m.NDCG20 += q.NDCG20
		m.NDCG10 += q.NDCG10
		m.MRR10 += q.MRR10
		m.Precision10 += q.Precision10
		m.Recall10 += q.Recall10
		if q.ResultCount == 0 {
			allResults[i] = nil
		} else {
			allResults[i] = make([]RankedItem, q.ResultCount)
		}
	}

	n := float64(len(perQuery))
	m.NDCG20 /= n
	m.NDCG10 /= n
	m.MRR10 /= n
	m.Precision10 /= n
	m.Recall10 /= n
	m.ZeroResultRate = ZeroResultRate(allResults)
	m.AvgLatencyMs = AvgLatencyMs(latenciesMs)

	return m
}
