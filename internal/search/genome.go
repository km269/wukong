// Package search defines the search strategy parameter space
// (SearchGenome) and evaluation types for search quality tuning.
//
// SearchGenome encodes the tunable parameters of a retrieval
// strategy — recall mode, keyword/semantic weights, match
// threshold, and candidate set size — into a single struct that
// can be configured, evaluated, and auto-tuned.
package search

import (
	"fmt"
	"strings"
)

// Recall mode constants.
const (
	RecallModeLexical = "lexical" // FTS5 / BM25 only
	RecallModeVector  = "vector"  // HNSW / embedding only
	RecallModeHybrid  = "hybrid"  // FTS5 + embedding re-rank
)

// Fusion method constants.
const (
	FusionWeighted = "weighted" // DenseWeight×sim + TextWeight×(1/rank)
	FusionRRF      = "rrf"      // Reciprocal Rank Fusion: Σ 1/(k+rank)
)

// DefaultRRFK is the standard RRF constant. A large k smooths
// rank differences; k=60 is the widely-adopted default (Cormack
// et al., 2009).
const DefaultRRFK = 60.0

// SearchGenome encodes a search strategy as tunable parameters.
// Inspired by volcengine/SearchCLI's Genome concept, this struct
// defines the parameter space for retrieval optimisation.
//
// Fields:
//   - RecallMode: which retrieval channel(s) to use
//   - DenseWeight: semantic (vector) score weight [0,1]
//   - TextWeight: keyword (BM25) score weight [0,1]
//     DenseWeight and TextWeight are normalised so they sum to 1.
//   - KeywordMatchPercent: minimum fraction of query keywords
//     that must match for a document to be retained [0,1].
//     0 means no filtering; 0.3 requires 30% of query keywords.
//   - MaxRetrievedNum: final TopK returned to the caller
//   - FTS5PoolSize: candidate pool size for FTS5 retrieval
//     before re-ranking (only used in hybrid mode)
//   - FusionMethod: how to merge lexical + vector results in hybrid
//     mode. "weighted" (default) uses DenseWeight×sim + TextWeight×rank;
//     "rrf" uses Reciprocal Rank Fusion (rank-based, scale-invariant).
//   - RRFK: the smoothing constant for RRF (default 60). Only used
//     when FusionMethod == "rrf".
type SearchGenome struct {
	RecallMode          string  `json:"recall_mode"`
	DenseWeight         float64 `json:"dense_weight"`
	TextWeight          float64 `json:"text_weight"`
	KeywordMatchPercent float64 `json:"keyword_match_percent"`
	MaxRetrievedNum     int     `json:"max_retrieved_num"`
	FTS5PoolSize        int     `json:"fts5_pool_size"`
	FusionMethod        string  `json:"fusion_method,omitempty"`
	RRFK                float64 `json:"rrf_k,omitempty"`
	RerankerEnabled     bool    `json:"reranker_enabled,omitempty"`
	RerankerTopN        int     `json:"reranker_top_n,omitempty"`
	// MMREnabled promotes diversity in top-K results using
	// Maximal Marginal Relevance, preventing all results from
	// clustering around one document. Inspired by SurfSense's
	// hierarchical indices (document-level diversity).
	MMREnabled bool    `json:"mmr_enabled,omitempty"`
	MMRLambda  float64 `json:"mmr_lambda,omitempty"`
}

// DefaultGenome returns the baseline strategy matching the
// previous hardcoded behaviour (70% semantic + 30% BM25,
// hybrid mode, pool size 50, top-K 10).
func DefaultGenome() SearchGenome {
	return SearchGenome{
		RecallMode:          RecallModeHybrid,
		DenseWeight:         0.7,
		TextWeight:          0.3,
		KeywordMatchPercent: 0.0,
		MaxRetrievedNum:     10,
		FTS5PoolSize:        50,
		FusionMethod:        FusionWeighted,
	}
}

// Normalized returns a copy with DenseWeight and TextWeight
// scaled so they sum to 1.0. If both are zero, defaults to
// 0.5/0.5. Other fields are passed through unchanged.
func (g SearchGenome) Normalized() SearchGenome {
	out := g
	dw, tw := g.DenseWeight, g.TextWeight
	if dw < 0 {
		dw = 0
	}
	if tw < 0 {
		tw = 0
	}
	sum := dw + tw
	switch {
	case sum == 0:
		out.DenseWeight = 0.5
		out.TextWeight = 0.5
	default:
		out.DenseWeight = dw / sum
		out.TextWeight = tw / sum
	}
	return out
}

// Validate checks that the genome fields are within legal ranges.
// Returns an error describing the first violation, or nil if valid.
func (g SearchGenome) Validate() error {
	switch g.RecallMode {
	case RecallModeLexical, RecallModeVector, RecallModeHybrid:
	default:
		return fmt.Errorf(
			"invalid recall_mode %q: want lexical|vector|hybrid",
			g.RecallMode)
	}
	if g.DenseWeight < 0 || g.DenseWeight > 1 {
		return fmt.Errorf(
			"dense_weight %.3f out of range [0,1]",
			g.DenseWeight)
	}
	if g.TextWeight < 0 || g.TextWeight > 1 {
		return fmt.Errorf(
			"text_weight %.3f out of range [0,1]",
			g.TextWeight)
	}
	if g.KeywordMatchPercent < 0 || g.KeywordMatchPercent > 1 {
		return fmt.Errorf(
			"keyword_match_percent %.3f out of range [0,1]",
			g.KeywordMatchPercent)
	}
	if g.MaxRetrievedNum < 1 {
		return fmt.Errorf(
			"max_retrieved_num %d must be >= 1",
			g.MaxRetrievedNum)
	}
	if g.FTS5PoolSize < 1 {
		return fmt.Errorf(
			"fts5_pool_size %d must be >= 1",
			g.FTS5PoolSize)
	}
	switch g.FusionMethod {
	case "", FusionWeighted, FusionRRF:
		// Valid.
	default:
		return fmt.Errorf(
			"invalid fusion_method %q: want weighted|rrf",
			g.FusionMethod)
	}
	if g.RRFK < 0 {
		return fmt.Errorf("rrf_k %.3f must be >= 0", g.RRFK)
	}
	if g.MMRLambda < 0 || g.MMRLambda > 1 {
		return fmt.Errorf("mmr_lambda %.3f out of range [0,1]", g.MMRLambda)
	}
	return nil
}

// EffectiveTopK returns MaxRetrievedNum, defaulting to 10 if <= 0.
func (g SearchGenome) EffectiveTopK() int {
	if g.MaxRetrievedNum <= 0 {
		return 10
	}
	return g.MaxRetrievedNum
}

// EffectivePoolSize returns FTS5PoolSize, defaulting to 50 if <= 0.
// In hybrid mode this is the candidate pool retrieved via FTS5
// before semantic re-ranking.
func (g SearchGenome) EffectivePoolSize() int {
	if g.FTS5PoolSize <= 0 {
		return 50
	}
	return g.FTS5PoolSize
}

// IsHybrid returns true if the recall mode is hybrid.
func (g SearchGenome) IsHybrid() bool {
	return g.RecallMode == RecallModeHybrid
}

// IsVectorOnly returns true if the recall mode is vector only.
func (g SearchGenome) IsVectorOnly() bool {
	return g.RecallMode == RecallModeVector
}

// IsLexicalOnly returns true if the recall mode is lexical only.
func (g SearchGenome) IsLexicalOnly() bool {
	return g.RecallMode == RecallModeLexical
}

// IsRRF returns true if the fusion method is RRF.
func (g SearchGenome) IsRRF() bool {
	return g.FusionMethod == FusionRRF
}

// EffectiveRRFK returns the RRF smoothing constant, defaulting to 60.
func (g SearchGenome) EffectiveRRFK() float64 {
	if g.RRFK <= 0 {
		return DefaultRRFK
	}
	return g.RRFK
}

// EffectiveRerankerTopN returns the number of candidates to pass
// through the reranker, defaulting to MaxRetrievedNum.
func (g SearchGenome) EffectiveRerankerTopN() int {
	if g.RerankerTopN <= 0 {
		return g.EffectiveTopK()
	}
	return g.RerankerTopN
}

// EffectiveMMRLambda returns the MMR trade-off parameter,
// defaulting to 0.7 (relevance-heavy). λ=1 means pure relevance
// (no diversity), λ=0 means pure diversity.
func (g SearchGenome) EffectiveMMRLambda() float64 {
	if g.MMRLambda <= 0 {
		return 0.7
	}
	return g.MMRLambda
}

// MMRSelect applies Maximal Marginal Relevance to select a diverse
// subset of results. candidates must already be sorted by relevance
// (descending). Returns top-K results with diversity enforced.
//
// MMR formula: select d that maximizes
//
//	λ × rel(d) - (1-λ) × max_{d' in S} sim(d, d')
//
// where rel(d) is the relevance score and sim is text overlap.
func MMRSelect(
	candidates []MMRItem,
	k int,
	lambda float64,
	simFunc func(a, b string) float64,
) []MMRItem {
	if len(candidates) <= k {
		return candidates
	}

	selected := make([]MMRItem, 0, k)
	remaining := make([]MMRItem, len(candidates))
	copy(remaining, candidates)

	// First item: highest relevance.
	selected = append(selected, remaining[0])
	remaining = remaining[1:]

	for len(selected) < k && len(remaining) > 0 {
		bestIdx := 0
		bestScore := -1.0
		for i, cand := range remaining {
			// Relevance component.
			rel := cand.Score
			// Max similarity to selected set.
			maxSim := 0.0
			for _, sel := range selected {
				s := simFunc(cand.Text, sel.Text)
				if s > maxSim {
					maxSim = s
				}
			}
			mmr := lambda*rel - (1-lambda)*maxSim
			if mmr > bestScore {
				bestScore = mmr
				bestIdx = i
			}
		}
		selected = append(selected, remaining[bestIdx])
		remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
	}
	return selected
}

// MMRItem is a candidate for MMR selection.
type MMRItem struct {
	Text  string
	Score float64
	Index int // original index in the source list
}

// TextJaccardSimilarity computes Jaccard similarity between two
// texts based on word overlap. Used as a lightweight similarity
// function for MMR when embeddings are not available.
func TextJaccardSimilarity(a, b string) float64 {
	wordsA := strings.Fields(strings.ToLower(a))
	wordsB := strings.Fields(strings.ToLower(b))
	if len(wordsA) == 0 || len(wordsB) == 0 {
		return 0
	}
	setA := make(map[string]bool, len(wordsA))
	for _, w := range wordsA {
		setA[w] = true
	}
	intersection := 0
	for _, w := range wordsB {
		if setA[w] {
			intersection++
		}
	}
	union := len(wordsA) + len(wordsB) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// RRFEntry represents a single result from one retrieval channel
// for RRF fusion. Key is the dedup identifier; Rank is the 0-based
// position in that channel's result list.
type RRFEntry struct {
	Key  string
	Rank int
}

// RRFFuse merges multiple ranked lists using Reciprocal Rank Fusion.
// Each input slice is one channel's ranked results (position 0 = best).
// The RRF score for a document is: Σ 1/(k + rank+1) across channels
// where it appears. Returns a map of key→fused score.
func RRFFuse(channels [][]RRFEntry, k float64) map[string]float64 {
	scores := make(map[string]float64)
	for _, channel := range channels {
		for _, entry := range channel {
			scores[entry.Key] += 1.0 / (k + float64(entry.Rank+1))
		}
	}
	return scores
}
