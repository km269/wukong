package metrics

import (
	"testing"
	"time"
)

func TestSearchMetrics_RecordAndSnapshot(t *testing.T) {
	m := New()

	// Record a hybrid search with RRF + reranker.
	m.Record(SearchEvent{
		Mode:          ModeHybrid,
		Duration:      50 * time.Millisecond,
		ResultCount:   10,
		QueryLen:      20,
		UsedRRF:       true,
		UsedReranker:  true,
		PreRerankCount: 30,
	})
	// Record a lexical search.
	m.Record(SearchEvent{
		Mode:         ModeLexical,
		Duration:     5 * time.Millisecond,
		ResultCount:  5,
		QueryLen:     10,
	})
	// Record a vertical search.
	m.Record(SearchEvent{
		Mode:           ModeVertical,
		Duration:       200 * time.Millisecond,
		ResultCount:    3,
		UsedVertical:   true,
		VerticalIntent: "academic",
	})
	// Record an error.
	m.Record(SearchEvent{
		Mode:  ModeVector,
		Error: "embedding failed",
	})

	// Record cache stats.
	m.RecordCacheHit()
	m.RecordCacheHit()
	m.RecordCacheMiss()

	snap := m.Snapshot()

	if snap.TotalSearches != 4 {
		t.Errorf("TotalSearches = %d; want 4", snap.TotalSearches)
	}
	if snap.TotalResults != 18 { // 10 + 5 + 3 + 0
		t.Errorf("TotalResults = %d; want 18", snap.TotalResults)
	}
	if snap.RRFUses != 1 {
		t.Errorf("RRFUses = %d; want 1", snap.RRFUses)
	}
	if snap.RerankerUses != 1 {
		t.Errorf("RerankerUses = %d; want 1", snap.RerankerUses)
	}
	if snap.VerticalUses != 1 {
		t.Errorf("VerticalUses = %d; want 1", snap.VerticalUses)
	}
	if snap.VerticalIntents["academic"] != 1 {
		t.Errorf("VerticalIntents[academic] = %d; want 1", snap.VerticalIntents["academic"])
	}
	if snap.CacheHitRate() != 2.0/3.0 {
		t.Errorf("CacheHitRate = %.2f; want %.2f", snap.CacheHitRate(), 2.0/3.0)
	}
	if snap.ErrorRate() <= 0 {
		t.Error("ErrorRate should be > 0 with one error")
	}

	// Check mode stats.
	hybridStat := snap.ModeStats[ModeHybrid]
	if hybridStat.Count != 1 {
		t.Errorf("hybrid Count = %d; want 1", hybridStat.Count)
	}
	if hybridStat.AvgLatency != 50*time.Millisecond {
		t.Errorf("hybrid AvgLatency = %v; want 50ms", hybridStat.AvgLatency)
	}
	if hybridStat.AvgResults != 10.0 {
		t.Errorf("hybrid AvgResults = %.1f; want 10.0", hybridStat.AvgResults)
	}

	// Check error count.
	vectorStat := snap.ModeStats[ModeVector]
	if vectorStat.Errors != 1 {
		t.Errorf("vector Errors = %d; want 1", vectorStat.Errors)
	}
}

func TestSearchMetrics_LatencyBuckets(t *testing.T) {
	m := New()

	// Record searches at different latency levels.
	m.Record(SearchEvent{Mode: ModeLexical, Duration: 5 * time.Millisecond})   // bucket 0
	m.Record(SearchEvent{Mode: ModeLexical, Duration: 30 * time.Millisecond})  // bucket 1
	m.Record(SearchEvent{Mode: ModeLexical, Duration: 80 * time.Millisecond})  // bucket 2
	m.Record(SearchEvent{Mode: ModeLexical, Duration: 300 * time.Millisecond}) // bucket 3
	m.Record(SearchEvent{Mode: ModeLexical, Duration: 800 * time.Millisecond}) // bucket 4
	m.Record(SearchEvent{Mode: ModeLexical, Duration: 2 * time.Second})        // bucket 5
	m.Record(SearchEvent{Mode: ModeLexical, Duration: 10 * time.Second})       // bucket 6

	snap := m.Snapshot()
	buckets := snap.LatencyBuckets[ModeLexical]
	for i, count := range buckets {
		if count != 1 {
			t.Errorf("bucket[%d] = %d; want 1", i, count)
		}
	}
}

func TestSearchMetrics_Empty(t *testing.T) {
	m := New()
	snap := m.Snapshot()
	if snap.TotalSearches != 0 {
		t.Errorf("TotalSearches = %d; want 0", snap.TotalSearches)
	}
	if snap.CacheHitRate() != 0 {
		t.Error("empty cache hit rate should be 0")
	}
	if snap.ErrorRate() != 0 {
		t.Error("empty error rate should be 0")
	}
}

func TestSnapshot_String(t *testing.T) {
	m := New()
	m.Record(SearchEvent{
		Mode:         ModeHybrid,
		Duration:     50 * time.Millisecond,
		ResultCount:  10,
		UsedRRF:      true,
	})
	snap := m.Snapshot()
	s := snap.String()
	if s == "" {
		t.Error("String() should not be empty")
	}
	// Should contain key labels.
	for _, label := range []string{"Search Metrics", "Total searches", "Pipeline usage"} {
		if !contains(s, label) {
			t.Errorf("String() should contain %q", label)
		}
	}
}

func TestLatencyBucketIndex(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want int
	}{
		{1 * time.Millisecond, 0},
		{10 * time.Millisecond, 1}, // 10ms is NOT < 10ms
		{49 * time.Millisecond, 1},
		{50 * time.Millisecond, 2}, // 50ms is NOT < 50ms
		{99 * time.Millisecond, 2},
		{100 * time.Millisecond, 3}, // 100ms is NOT < 100ms
		{499 * time.Millisecond, 3},
		{500 * time.Millisecond, 4}, // 500ms is NOT < 500ms
		{999 * time.Millisecond, 4},
		{1 * time.Second, 5},        // 1s is NOT < 1s
		{4 * time.Second, 5},
		{5 * time.Second, 6},        // 5s is NOT < 5s
		{10 * time.Second, 6},
	}
	for _, c := range cases {
		got := latencyBucketIndex(c.d)
		if got != c.want {
			t.Errorf("latencyBucketIndex(%v) = %d; want %d", c.d, got, c.want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
