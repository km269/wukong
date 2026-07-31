// Package search defines the search strategy parameter space
// (SearchGenome) and evaluation types for search quality tuning.
//
// SearchGenome encodes the tunable parameters of a retrieval
// strategy — recall mode, keyword/semantic weights, match
// threshold, and candidate set size — into a single struct that
// can be configured, evaluated, and auto-tuned.
package search

import "fmt"

// Recall mode constants.
const (
	RecallModeLexical = "lexical" // FTS5 / BM25 only
	RecallModeVector  = "vector"  // HNSW / embedding only
	RecallModeHybrid  = "hybrid"  // FTS5 + embedding re-rank
)

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
type SearchGenome struct {
	RecallMode          string  `json:"recall_mode"`
	DenseWeight         float64 `json:"dense_weight"`
	TextWeight          float64 `json:"text_weight"`
	KeywordMatchPercent float64 `json:"keyword_match_percent"`
	MaxRetrievedNum     int     `json:"max_retrieved_num"`
	FTS5PoolSize        int     `json:"fts5_pool_size"`
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
