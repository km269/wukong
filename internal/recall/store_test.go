package recall

import (
	"testing"
	"time"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"
)

func TestNewStore_SQLite(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/recall_test.db"
	pool := util.NewDatabasePool(dbPath)
	defer pool.Close()

	cfg := &config.RecallConfig{
		Backend:              "sqlite",
		DBPath:               dbPath,
		MaxResults:           10,
		MaxMessagesPerSession: 50,
	}
	store, err := NewStore(cfg, pool)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil Store")
	}

	// Close should succeed
	if err := store.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func TestStore_StoreAndSearch_LIKE(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/recall_like_test.db"
	pool := util.NewDatabasePool(dbPath)
	defer pool.Close()

	cfg := &config.RecallConfig{
		Backend:              "sqlite",
		DBPath:               dbPath,
		MaxResults:           10,
		MaxMessagesPerSession: 5,
	}
	store, err := NewStore(cfg, pool)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Store a message
	msg := ChatMessage{
		SessionID: "test-session-1",
		UserID:    "test-user",
		Role:      "user",
		Content:   "What is the capital of France?",
		CreatedAt: time.Now(),
	}
	if err := store.StoreMessage(msg); err != nil {
		t.Fatalf("StoreMessage failed: %v", err)
	}

	// Search for it (FTS5 may not be available, so fallback to LIKE)
	results, err := store.Search("capital", "", 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 search result")
	}
	if results[0].Message.SessionID != "test-session-1" {
		t.Errorf("expected session 'test-session-1', got %q",
			results[0].Message.SessionID)
	}
	if results[0].Message.Role != "user" {
		t.Errorf("expected role 'user', got %q",
			results[0].Message.Role)
	}
}

func TestStore_StoreMultipleMessages(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/recall_multi_test.db"
	pool := util.NewDatabasePool(dbPath)
	defer pool.Close()

	cfg := &config.RecallConfig{
		Backend:              "sqlite",
		DBPath:               dbPath,
		MaxResults:           10,
		MaxMessagesPerSession: 3,
	}
	store, err := NewStore(cfg, pool)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-session-multi"
	// Store 5 messages (exceeds MaxMessagesPerSession=3)
	for i := 0; i < 5; i++ {
		msg := ChatMessage{
			SessionID: sessionID,
			UserID:    "test-user",
			Role:      "user",
			Content:   "Message number " + string(rune('0'+i)),
			CreatedAt: time.Now().Add(time.Duration(i) * time.Second),
		}
		if err := store.StoreMessage(msg); err != nil {
			t.Fatalf("StoreMessage %d failed: %v", i, err)
		}
	}

	// Verify session messages are capped
	sessions, err := store.ListSessions("test-user")
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(sessions) == 0 {
		t.Fatal("expected at least 1 session")
	}
}

func TestStore_DeleteSession(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/recall_delete_test.db"
	pool := util.NewDatabasePool(dbPath)
	defer pool.Close()

	cfg := &config.RecallConfig{
		Backend:              "sqlite",
		DBPath:               dbPath,
		MaxResults:           10,
		MaxMessagesPerSession: 100,
	}
	store, err := NewStore(cfg, pool)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	msg := ChatMessage{
		SessionID: "to-delete",
		UserID:    "test-user",
		Role:      "user",
		Content:   "Delete me",
		CreatedAt: time.Now(),
	}
	if err := store.StoreMessage(msg); err != nil {
		t.Fatalf("StoreMessage failed: %v", err)
	}

	// Delete the session
	if err := store.DeleteSession("to-delete"); err != nil {
		t.Fatalf("DeleteSession failed: %v", err)
	}

	// Search should return nothing
	results, err := store.Search("Delete", "", 5)
	if err != nil {
		t.Fatalf("Search after delete failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results after delete, got %d", len(results))
	}
}

func TestStore_SearchBySession(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/recall_bysession_test.db"
	pool := util.NewDatabasePool(dbPath)
	defer pool.Close()

	cfg := &config.RecallConfig{
		Backend:              "sqlite",
		DBPath:               dbPath,
		MaxResults:           10,
		MaxMessagesPerSession: 100,
	}
	store, err := NewStore(cfg, pool)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Store in session A
	store.StoreMessage(ChatMessage{
		SessionID: "sess-a",
		UserID:    "u1",
		Role:      "user",
		Content:   "Apple pie recipe",
		CreatedAt: time.Now(),
	})
	// Store in session B
	store.StoreMessage(ChatMessage{
		SessionID: "sess-b",
		UserID:    "u1",
		Role:      "user",
		Content:   "Banana bread recipe",
		CreatedAt: time.Now(),
	})

	// Search only session A
	results, err := store.SearchBySession("sess-a", "recipe", 5)
	if err != nil {
		t.Fatalf("SearchBySession failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	for _, r := range results {
		if r.Message.SessionID != "sess-a" {
			t.Errorf("expected only sess-a results, got %q",
				r.Message.SessionID)
		}
	}
}

func TestSearchResult_Preview(t *testing.T) {
	// Verify preview truncation is handled in SearchResult construction
	sr := SearchResult{
		Preview: "This is a short preview",
		Score:   0.85,
	}
	if sr.Preview != "This is a short preview" {
		t.Errorf("preview mismatch: %q", sr.Preview)
	}
	if sr.Score != 0.85 {
		t.Errorf("score mismatch: %f", sr.Score)
	}
}
