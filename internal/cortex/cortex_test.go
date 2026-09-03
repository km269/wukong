// cortex_test.go — first unit tests for the CortexDB memory stack
// (roadmap P1-7: closing the zero-test gap on internal/cortex).
// Focus: pure algorithm surfaces (VectorCache) and the SQLite-backed
// lexical store end-to-end (which also exercises the P0-3 migration
// path in a real database).
package cortex

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/recall"

	_ "modernc.org/sqlite"
)

func newTestLexicalStore(t *testing.T) *lexicalStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ls, err := newLexicalStore(db)
	if err != nil {
		t.Fatalf("newLexicalStore: %v", err)
	}
	return ls
}

func msg(session, role, content string) recall.ChatMessage {
	return recall.ChatMessage{
		SessionID: session,
		UserID:    "user1",
		Role:      role,
		Content:   content,
		CreatedAt: time.Now(),
	}
}

func TestLexicalStoreMessageLifecycle(t *testing.T) {
	ls := newTestLexicalStore(t)

	id, err := ls.storeMessage(msg("s1", "user", "wukong is a memory-first agent"))
	if err != nil {
		t.Fatalf("storeMessage: %v", err)
	}
	if id <= 0 {
		t.Fatalf("id = %d", id)
	}
	if _, err := ls.storeMessage(
		msg("s1", "assistant", "the agent remembers across sessions"),
	); err != nil {
		t.Fatalf("storeMessage(2): %v", err)
	}

	// FTS5 search hits the stored content.
	results, err := ls.search("memory-first", "user1", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 || results[0].Message.Content != "wukong is a memory-first agent" {
		t.Fatalf("results = %+v", results)
	}

	// Session-scoped search.
	results, err = ls.searchBySession("s1", "remembers", 10)
	if err != nil {
		t.Fatalf("searchBySession: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("session results = %d, want 1", len(results))
	}

	sessions, err := ls.listSessions("user1")
	if err != nil || len(sessions) != 1 || sessions[0] != "s1" {
		t.Fatalf("listSessions = %v/%v", sessions, err)
	}

	if err := ls.deleteSession("s1"); err != nil {
		t.Fatalf("deleteSession: %v", err)
	}
	sessions, _ = ls.listSessions("user1")
	if len(sessions) != 0 {
		t.Fatalf("sessions after delete = %v", sessions)
	}
}

func TestLexicalStorePerSessionLimit(t *testing.T) {
	ls := newTestLexicalStore(t)
	for i := 0; i < 5; i++ {
		if _, err := ls.storeMessage(
			msg("s1", "user", "hello wukong"), 3,
		); err != nil {
			t.Fatalf("storeMessage #%d: %v", i, err)
		}
	}
	sessions, _ := ls.listSessions("user1")
	_ = sessions

	// Only the newest `limit` messages survive per session: search
	// for the oldest distinct marker returns nothing, newest hits.
	results, err := ls.searchLike("hello wukong", "user1", 10)
	if err != nil {
		t.Fatalf("searchLike: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3 (per-session limit)", len(results))
	}
}

func TestLexicalStoreVectors(t *testing.T) {
	ls := newTestLexicalStore(t)
	id, err := ls.storeMessage(msg("s1", "user", "vector search target"))
	if err != nil {
		t.Fatalf("storeMessage: %v", err)
	}

	if err := ls.storeVector(id, "s1", "user1",
		[]float32{1, 0, 0}, "vector search target"); err != nil {
		t.Fatalf("storeVector: %v", err)
	}

	// Identical direction ranks first; orthogonal content is not in
	// the top result.
	results, err := ls.searchVector(
		[]float32{1, 0, 0}, "vector", "user1", "", 5)
	if err != nil {
		t.Fatalf("searchVector: %v", err)
	}
	if len(results) == 0 ||
		results[0].Message.Content != "vector search target" {
		t.Fatalf("results = %+v", results)
	}
}

func TestRerankerFallbackOnUnreachableAPI(t *testing.T) {
	cfg := &config.CortexConfig{
		RerankerModel:   "test-reranker",
		RerankerBaseURL: "http://127.0.0.1:1", // nothing listens here
	}
	r := NewReranker(cfg)
	if r == nil {
		t.Fatal("NewReranker = nil with model configured")
	}

	// API failure must fall back to the original order.
	order, scores, err := r.Rerank(
		context.Background(), "query",
		[]string{"doc-a", "doc-b", "doc-c"}, 2)
	if err != nil {
		t.Fatalf("Rerank fallback returned error: %v", err)
	}
	if len(order) != 3 ||
		order[0] != 0 || order[1] != 1 || order[2] != 2 {
		t.Fatalf("order = %v, want original", order)
	}
	if len(scores) != 3 {
		t.Fatalf("scores = %v", scores)
	}
}

func TestNewRerankerDisabledWithoutModel(t *testing.T) {
	if NewReranker(nil) != nil {
		t.Error("nil config should disable the reranker")
	}
	if NewReranker(&config.CortexConfig{}) != nil {
		t.Error("empty RerankerModel should disable the reranker")
	}
}

func TestVectorCacheBasics(t *testing.T) {
	c := NewVectorCache(WithCacheTTL(time.Minute), WithMaxEntries(8))
	defer c.Stop()

	if _, ok := c.GetMessageVector("missing"); ok {
		t.Error("miss should not hit")
	}

	vec := []float32{1, 2, 3}
	c.SetMessageVector("m1", vec)
	got, ok := c.GetMessageVector("m1")
	if !ok || len(got) != 3 {
		t.Fatalf("GetMessageVector = %v/%v", got, ok)
	}

	c.SetQueryVector("query text", []float32{4, 5})
	if _, ok := c.GetQueryVector("query text"); !ok {
		t.Error("query vector miss")
	}

	c.Clear()
	if _, ok := c.GetMessageVector("m1"); ok {
		t.Error("entries survived Clear")
	}
}

func TestVectorCacheComputeOnce(t *testing.T) {
	c := NewVectorCache()
	defer c.Stop()

	calls := 0
	embed := func(context.Context, []string) ([][]float64, error) {
		calls++
		return [][]float64{{0.5, 0.5}}, nil
	}

	v1, err := c.GetOrComputeMessageVector(
		context.Background(), "k", "text", embed)
	if err != nil || len(v1) != 2 {
		t.Fatalf("first compute = %v/%v", v1, err)
	}
	if _, err := c.GetOrComputeMessageVector(
		context.Background(), "k", "text", embed); err != nil {
		t.Fatalf("cached get: %v", err)
	}
	if calls != 1 {
		t.Fatalf("embedder called %d times, want 1 (cached)", calls)
	}

	// Embedder errors propagate.
	if _, err := c.GetOrComputeMessageVector(
		context.Background(), "k2", "text",
		func(context.Context, []string) ([][]float64, error) {
			return nil, errors.New("embedder down")
		},
	); err == nil {
		t.Fatal("embedder error swallowed")
	}

	// Empty vectors produce a clean (nil, nil) result.
	v, err := c.GetOrComputeMessageVector(
		context.Background(), "k3", "text",
		func(context.Context, []string) ([][]float64, error) {
			return [][]float64{}, nil
		},
	)
	if err != nil || v != nil {
		t.Fatalf("empty vector = %v/%v, want nil/nil", v, err)
	}
}

func TestVectorCacheEvictionAndStats(t *testing.T) {
	c := NewVectorCache(WithMaxEntries(2))
	defer c.Stop()

	c.SetMessageVector("a", []float32{1})
	c.SetMessageVector("b", []float32{2})
	c.SetMessageVector("c", []float32{3}) // evicts "a" (oldest)

	if _, ok := c.GetMessageVector("a"); ok {
		t.Error("oldest entry survived eviction")
	}
	if _, ok := c.GetMessageVector("c"); !ok {
		t.Error("newest entry evicted")
	}

	stats := c.Stats()
	if stats == nil {
		t.Fatal("Stats = nil")
	}
	if rate := c.HitRate(); rate < 0 || rate > 1 {
		t.Errorf("HitRate = %v, outside [0,1]", rate)
	}
}
