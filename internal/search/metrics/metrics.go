// Package metrics provides search pipeline observability: latency
// tracking, result quality metrics, fusion/reranker usage stats,
// and vertical routing hit rates. Metrics are collected in-process
// and exposed via Snapshot() for monitoring and tuning.
//
// Inspired by insane-search's search observability dashboard, this
// package enables data-driven optimisation of retrieval parameters
// (genome tuning, reranker thresholds, MMR lambda) by surfacing
// the impact of each pipeline stage on result quality and latency.
package metrics

import (
	"fmt"
	"sync"
	"time"
)

// SearchMode identifies the retrieval mode used for a search.
type SearchMode string

const (
	ModeLexical  SearchMode = "lexical"
	ModeVector   SearchMode = "vector"
	ModeHybrid   SearchMode = "hybrid"
	ModeVertical SearchMode = "vertical"
	ModeFallback SearchMode = "fallback"
)

// SearchEvent records a single search operation for metrics.
type SearchEvent struct {
	Mode        SearchMode
	Duration    time.Duration
	ResultCount int
	QueryLen    int
	// Pipeline stage flags.
	UsedRRF      bool
	UsedReranker bool
	UsedMMR      bool
	UsedVertical bool
	// Vertical routing intent (when UsedVertical is true).
	VerticalIntent string
	// Error is non-empty when the search failed.
	Error string
	// PreRerankCount is the candidate count before reranking.
	PreRerankCount int
	// PreMMRCount is the candidate count before MMR.
	PreMMRCount int
}

// SearchMetrics is a thread-safe collector for search pipeline metrics.
type SearchMetrics struct {
	mu sync.Mutex

	// Counters by mode.
	modeCounts map[SearchMode]int64
	modeErrors map[SearchMode]int64

	// Latency tracking by mode (sum and count for average, plus min/max).
	latencySum   map[SearchMode]time.Duration
	latencyCount map[SearchMode]int64
	latencyMin   map[SearchMode]time.Duration
	latencyMax   map[SearchMode]time.Duration

	// Result count tracking.
	totalResults  int64
	totalSearches int64
	resultCounts  map[SearchMode]int64 // sum of result counts per mode

	// Pipeline stage usage.
	rrfUses      int64
	rerankerUses int64
	mmrUses      int64
	verticalUses int64

	// Vertical routing by intent.
	verticalIntentCounts map[string]int64

	// Candidate set sizes (for tuning pool size parameters).
	totalPreRerank int64
	totalPreMMR    int64

	// Vector cache stats.
	cacheHits   int64
	cacheMisses int64

	// Latency histogram buckets (simple bucketing for percentiles).
	// Buckets: <10ms, <50ms, <100ms, <500ms, <1s, <5s, >=5s.
	latencyBuckets map[SearchMode][7]int64
}

// New creates a new SearchMetrics collector.
func New() *SearchMetrics {
	return &SearchMetrics{
		modeCounts:           make(map[SearchMode]int64),
		modeErrors:           make(map[SearchMode]int64),
		latencySum:           make(map[SearchMode]time.Duration),
		latencyCount:         make(map[SearchMode]int64),
		latencyMin:           make(map[SearchMode]time.Duration),
		latencyMax:           make(map[SearchMode]time.Duration),
		resultCounts:         make(map[SearchMode]int64),
		verticalIntentCounts: make(map[string]int64),
		latencyBuckets:       make(map[SearchMode][7]int64),
	}
}

// Record records a search event.
func (m *SearchMetrics) Record(e SearchEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.modeCounts[e.Mode]++
	m.totalSearches++

	if e.Error != "" {
		m.modeErrors[e.Mode]++
		return
	}

	// Latency tracking.
	m.latencySum[e.Mode] += e.Duration
	m.latencyCount[e.Mode]++
	if min, ok := m.latencyMin[e.Mode]; !ok || e.Duration < min {
		m.latencyMin[e.Mode] = e.Duration
	}
	if e.Duration > m.latencyMax[e.Mode] {
		m.latencyMax[e.Mode] = e.Duration
	}

	// Result count tracking.
	m.totalResults += int64(e.ResultCount)
	m.resultCounts[e.Mode] += int64(e.ResultCount)

	// Pipeline stage usage.
	if e.UsedRRF {
		m.rrfUses++
	}
	if e.UsedReranker {
		m.rerankerUses++
	}
	if e.UsedMMR {
		m.mmrUses++
	}
	if e.UsedVertical {
		m.verticalUses++
		if e.VerticalIntent != "" {
			m.verticalIntentCounts[e.VerticalIntent]++
		}
	}

	// Candidate set sizes.
	m.totalPreRerank += int64(e.PreRerankCount)
	m.totalPreMMR += int64(e.PreMMRCount)

	// Latency bucket.
	bucket := latencyBucketIndex(e.Duration)
	buckets := m.latencyBuckets[e.Mode]
	buckets[bucket]++
	m.latencyBuckets[e.Mode] = buckets
}

// RecordCacheHit records a vector cache hit.
func (m *SearchMetrics) RecordCacheHit() {
	m.mu.Lock()
	m.cacheHits++
	m.mu.Unlock()
}

// RecordCacheMiss records a vector cache miss.
func (m *SearchMetrics) RecordCacheMiss() {
	m.mu.Lock()
	m.cacheMisses++
	m.mu.Unlock()
}

// Snapshot returns a point-in-time copy of all metrics for reporting.
func (m *SearchMetrics) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	snap := Snapshot{
		TotalSearches:   m.totalSearches,
		TotalResults:    m.totalResults,
		RRFUses:         m.rrfUses,
		RerankerUses:    m.rerankerUses,
		MMRUses:         m.mmrUses,
		VerticalUses:    m.verticalUses,
		CacheHits:       m.cacheHits,
		CacheMisses:     m.cacheMisses,
		TotalPreRerank:  m.totalPreRerank,
		TotalPreMMR:     m.totalPreMMR,
		ModeStats:       make(map[SearchMode]ModeStat),
		VerticalIntents: copyMap(m.verticalIntentCounts),
		LatencyBuckets:  make(map[SearchMode][7]int64),
	}

	for mode, count := range m.modeCounts {
		ms := ModeStat{
			Count:  count,
			Errors: m.modeErrors[mode],
		}
		if c := m.latencyCount[mode]; c > 0 {
			ms.AvgLatency = m.latencySum[mode] / time.Duration(c)
			ms.MinLatency = m.latencyMin[mode]
			ms.MaxLatency = m.latencyMax[mode]
		}
		if count > 0 {
			ms.AvgResults = float64(m.resultCounts[mode]) / float64(count)
		}
		snap.ModeStats[mode] = ms
		snap.LatencyBuckets[mode] = m.latencyBuckets[mode]
	}

	return snap
}

// Snapshot is a point-in-time view of search metrics.
type Snapshot struct {
	TotalSearches   int64
	TotalResults    int64
	RRFUses         int64
	RerankerUses    int64
	MMRUses         int64
	VerticalUses    int64
	CacheHits       int64
	CacheMisses     int64
	TotalPreRerank  int64
	TotalPreMMR     int64
	ModeStats       map[SearchMode]ModeStat
	VerticalIntents map[string]int64
	LatencyBuckets  map[SearchMode][7]int64
}

// ModeStat holds per-mode statistics.
type ModeStat struct {
	Count      int64
	Errors     int64
	AvgLatency time.Duration
	MinLatency time.Duration
	MaxLatency time.Duration
	AvgResults float64
}

// CacheHitRate returns the vector cache hit rate (0-1).
func (s Snapshot) CacheHitRate() float64 {
	total := s.CacheHits + s.CacheMisses
	if total == 0 {
		return 0
	}
	return float64(s.CacheHits) / float64(total)
}

// ErrorRate returns the overall error rate (0-1).
func (s Snapshot) ErrorRate() float64 {
	if s.TotalSearches == 0 {
		return 0
	}
	var totalErrors int64
	for _, ms := range s.ModeStats {
		totalErrors += ms.Errors
	}
	return float64(totalErrors) / float64(s.TotalSearches)
}

// String returns a human-readable summary of the metrics.
func (s Snapshot) String() string {
	var sb stringBuilder
	sb.Printf("=== Search Metrics ===\n")
	sb.Printf("Total searches: %d\n", s.TotalSearches)
	sb.Printf("Total results:  %d\n", s.TotalResults)
	sb.Printf("Error rate:     %.2f%%\n", s.ErrorRate()*100)
	sb.Printf("Cache hit rate: %.2f%%\n", s.CacheHitRate()*100)
	sb.Printf("Pipeline usage: RRF=%d Reranker=%d MMR=%d Vertical=%d\n",
		s.RRFUses, s.RerankerUses, s.MMRUses, s.VerticalUses)
	sb.Printf("\n--- By Mode ---\n")
	for mode, ms := range s.ModeStats {
		sb.Printf("  %s: searches=%d errors=%d avg_latency=%s avg_results=%.1f\n",
			mode, ms.Count, ms.Errors, ms.AvgLatency, ms.AvgResults)
	}
	if len(s.VerticalIntents) > 0 {
		sb.Printf("\n--- Vertical Intents ---\n")
		for intent, count := range s.VerticalIntents {
			sb.Printf("  %s: %d\n", intent, count)
		}
	}
	return sb.String()
}

// --- Helpers ---

// latencyBucketIndex returns the bucket index for a latency value.
// Buckets: 0=<10ms, 1=<50ms, 2=<100ms, 3=<500ms, 4=<1s, 5=<5s, 6=>=5s.
func latencyBucketIndex(d time.Duration) int {
	switch {
	case d < 10*time.Millisecond:
		return 0
	case d < 50*time.Millisecond:
		return 1
	case d < 100*time.Millisecond:
		return 2
	case d < 500*time.Millisecond:
		return 3
	case d < 1*time.Second:
		return 4
	case d < 5*time.Second:
		return 5
	default:
		return 6
	}
}

// copyMap returns a shallow copy of the input map.
func copyMap(m map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// stringBuilder is a minimal string builder to avoid importing fmt.
type stringBuilder struct {
	buf []byte
}

func (sb *stringBuilder) Printf(format string, args ...interface{}) {
	sb.buf = append(sb.buf, fmt.Sprintf(format, args...)...)
}

func (sb *stringBuilder) String() string {
	return string(sb.buf)
}
