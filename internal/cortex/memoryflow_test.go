// memoryflow_test.go — offline tests for the MemoryFlow and
// GraphFlow services (P1-7 tail). The LLM seam is faked via the
// QueryPlanner / SessionExtractor interfaces; GraphFlow uses the
// heuristic extractor (nil JSONGenerator). Storage is a real
// CortexDB over a temp-directory SQLite file.
package cortex

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/km269/wukong/internal/config"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
	"github.com/liliang-cn/cortexdb/v2/pkg/memoryflow"
)

// fakePlanner records the last planned query.
type fakePlanner struct {
	lastQuery string
}

func (f *fakePlanner) Plan(
	_ context.Context, query string, _ memoryflow.SessionState,
) (*cortexdb.RetrievalPlan, error) {
	f.lastQuery = query
	return &cortexdb.RetrievalPlan{Query: query}, nil
}

// fakeExtractor records the last transcript seen and returns fixed
// promotion candidates.
type fakeExtractor struct {
	lastTranscript memoryflow.Transcript
	candidates     []memoryflow.PromotionCandidate
}

func (f *fakeExtractor) Extract(
	_ context.Context, t memoryflow.Transcript, _ memoryflow.SessionState,
) ([]memoryflow.PromotionCandidate, error) {
	f.lastTranscript = t
	return f.candidates, nil
}

func newTestMemoryFlow(t *testing.T) (*MemoryFlowService, *fakePlanner, *fakeExtractor) {
	t.Helper()
	planner := &fakePlanner{}
	fake := &fakeExtractor{candidates: extractorCandidates()}
	mf, err := NewMemoryFlow(
		&config.MemoryFlowConfig{
			DBPath:    filepath.Join(t.TempDir(), "mf.db"),
			Namespace: "test",
		},
		planner, fake,
	)
	if err != nil {
		t.Fatalf("NewMemoryFlow: %v", err)
	}
	t.Cleanup(func() { _ = mf.Close() })
	return mf, planner, fake
}

func extractorCandidates() []memoryflow.PromotionCandidate {
	return []memoryflow.PromotionCandidate{{
		Kind:    memoryflow.PromotionKindDecision,
		Title:   "Use Go for services",
		Content: "The team decided to use Go for all backend services.",
	}}
}

func TestMemoryFlowIngestAndWakeUp(t *testing.T) {
	mf, planner, _ := newTestMemoryFlow(t)
	ctx := context.Background()

	if err := mf.IngestTurn(ctx, "s1", "u1", "user",
		"We decided to adopt the capability bus"); err != nil {
		t.Fatalf("IngestTurn: %v", err)
	}
	if err := mf.IngestTurn(ctx, "s1", "u1", "assistant",
		"Understood — capability bus it is"); err != nil {
		t.Fatalf("IngestTurn(2): %v", err)
	}

	out, err := mf.WakeUp(ctx, "You are Wukong.", "capability bus", "s1", "u1")
	if err != nil {
		t.Fatalf("WakeUp: %v", err)
	}
	if !strings.Contains(out, "You are Wukong.") {
		t.Errorf("wake-up context missing identity layer:\n%s", out)
	}
	if planner.lastQuery != "capability bus" {
		t.Errorf("planner query = %q, want forwarded query", planner.lastQuery)
	}
}

func TestMemoryFlowPromoteFacts(t *testing.T) {
	mf, _, extractor := newTestMemoryFlow(t)
	ctx := context.Background()

	// PromoteFacts pulls the stored transcript; a zero-turn transcript
	// was a real historical bug (extractor always saw nothing).
	if err := mf.IngestTurn(ctx, "s1", "u1", "user",
		"Important: our on-call rotation starts Monday"); err != nil {
		t.Fatalf("IngestTurn: %v", err)
	}

	candidates, err := mf.PromoteFacts(ctx, "s1", "u1")
	if err != nil {
		t.Fatalf("PromoteFacts: %v", err)
	}
	if len(candidates) != len(extractorCandidates()) {
		t.Fatalf("candidates = %d, want %d",
			len(candidates), len(extractorCandidates()))
	}
	turns := extractor.lastTranscript.Turns
	if len(turns) == 0 {
		t.Fatal("extractor saw a zero-turn transcript " +
			"(regression of the empty-transcript bug)")
	}
	found := false
	for _, turn := range turns {
		if strings.Contains(turn.Content, "on-call rotation") {
			found = true
		}
	}
	if !found {
		t.Errorf("transcript turns missing ingested content: %+v", turns)
	}
}

func TestGraphFlowHeuristicPipeline(t *testing.T) {
	gf, err := NewGraphFlow(
		&config.GraphFlowConfig{
			DBPath:         filepath.Join(t.TempDir(), "gf.db"),
			MaxCharsPerDoc: 4000,
		},
		nil, // heuristic extractor — no LLM needed
	)
	if err != nil {
		t.Fatalf("NewGraphFlow: %v", err)
	}
	defer gf.Close()
	ctx := context.Background()

	result, err := gf.ExtractFromTranscript(ctx, "s1",
		"Alice works at Acme Corp. Bob knows Alice. Acme Corp is in Berlin.")
	if err != nil {
		t.Fatalf("ExtractFromTranscript: %v", err)
	}
	if result == nil {
		t.Fatal("extraction result = nil")
	}

	if err := gf.BuildGraph(ctx, result); err != nil {
		t.Fatalf("BuildGraph: %v", err)
	}
	// Nil result is a documented no-op.
	if err := gf.BuildGraph(ctx, nil); err != nil {
		t.Fatalf("BuildGraph(nil) = %v, want no-op success", err)
	}

	// Context assembly over the built graph must not error even when
	// keywords match nothing.
	if _, err := gf.BuildContext(ctx, []string{"alice"}); err != nil {
		t.Fatalf("BuildContext: %v", err)
	}
	out, err := gf.BuildContext(ctx, nil)
	if err != nil || out != "" {
		t.Fatalf("BuildContext(nil keywords) = %q/%v, want empty no-op", out, err)
	}
}

// compile-time interface checks for the fakes.
var (
	_ memoryflow.QueryPlanner     = (*fakePlanner)(nil)
	_ memoryflow.SessionExtractor = (*fakeExtractor)(nil)
	_ graphflow.JSONGenerator     = (graphflow.JSONGenerator)(nil)
)
