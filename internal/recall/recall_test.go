package recall

import (
	"testing"

	"github.com/km269/wukong/internal/config"
)

func TestFtsQuery_Simple(t *testing.T) {
	result := ftsQuery("hello world")
	if result != "hello* world*" {
		t.Errorf("expected 'hello* world*', got %q", result)
	}
}

func TestFtsQuery_Empty(t *testing.T) {
	result := ftsQuery("")
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestFtsQuery_SingleWord(t *testing.T) {
	result := ftsQuery("search")
	if result != "search*" {
		t.Errorf("expected 'search*', got %q", result)
	}
}

func TestFtsQuery_WithSpecialChars(t *testing.T) {
	// Words with special FTS5 chars should not get prefix *
	result := ftsQuery("test*word")
	if result == "test** word*" {
		t.Error("should not double-append * to words with *")
	}
}

func TestFtsQuery_MultipleSpaces(t *testing.T) {
	result := ftsQuery("  hello   world  ")
	// Fields splits on whitespace, trimming handled by TrimSpace
	if result != "hello* world*" {
		t.Errorf("expected 'hello* world*', got %q", result)
	}
}

func TestCalculateScore_ExactMatch(t *testing.T) {
	score := calculateScore("hello", "hello world")
	if score <= 0.0 {
		t.Errorf("expected positive score for exact match, got %f", score)
	}
	if score > 1.0 {
		t.Errorf("score should not exceed 1.0, got %f", score)
	}
}

func TestCalculateScore_NoMatch(t *testing.T) {
	score := calculateScore("xyzabc", "hello world")
	if score != 0.0 {
		t.Errorf("expected 0.0 for no match, got %f", score)
	}
}

func TestCalculateScore_PartialMatch(t *testing.T) {
	// "world" is in the content but "hello world" is the query
	score := calculateScore("hello world", "hello world")
	if score <= 0.0 {
		t.Errorf("expected positive score, got %f", score)
	}
}

func TestCalculateScore_CaseInsensitive(t *testing.T) {
	score1 := calculateScore("HELLO", "Hello World")
	score2 := calculateScore("hello", "Hello World")
	if score1 != score2 {
		t.Errorf("case-insensitive scores should be equal: %f vs %f",
			score1, score2)
	}
}

func TestNewRecallManager(t *testing.T) {
	store := &Store{cfg: &config.RecallConfig{MaxResults: 5}}
	_ = store // suppress unused warning
	mgr := NewRecallManager(nil)
	if mgr == nil {
		t.Fatal("expected non-nil RecallManager")
	}
	if mgr.store != nil {
		t.Error("store should be nil when passed nil")
	}

	mgr2 := NewRecallManager(store)
	if mgr2.store == nil {
		t.Error("store should be set when passed non-nil")
	}
}

func TestRecallManager_Tools(t *testing.T) {
	mgr := NewRecallManager(nil)
	tools := mgr.Tools()
	if len(tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, t := range tools {
		decl := t.Declaration()
		if decl != nil {
			names[decl.Name] = true
		}
	}

	expected := []string{"recall_search", "recall_sessions"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("expected tool %q not found", name)
		}
	}
}

func TestSearchRecallRsp_Struct(t *testing.T) {
	rsp := SearchRecallRsp{
		Success: true,
		Count:   3,
	}
	if !rsp.Success {
		t.Error("expected success")
	}
	if rsp.Count != 3 {
		t.Errorf("expected count 3, got %d", rsp.Count)
	}
}

func TestListRecallSessionsRsp_Struct(t *testing.T) {
	rsp := ListRecallSessionsRsp{
		Success:  true,
		Sessions: []string{"sess-1", "sess-2"},
		Count:    2,
	}
	if !rsp.Success {
		t.Error("expected success")
	}
	if rsp.Count != 2 {
		t.Errorf("expected count 2, got %d", rsp.Count)
	}
}
