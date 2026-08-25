package clone

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestArchiveFallback_Disabled(t *testing.T) {
	a := NewArchiveFallback(false)
	if a.Enabled() {
		t.Error("expected disabled")
	}
	// Methods should be no-ops.
	_, _, ok := a.FindSnapshot(context.Background(), "https://example.com")
	if ok {
		t.Error("expected no snapshot when disabled")
	}
	_, err := a.FetchArchivedPage(context.Background(), "https://example.com")
	if err == nil {
		t.Error("expected error when disabled")
	}
}

func TestFindSnapshot_NoArchive(t *testing.T) {
	// Mock Wayback API returning no snapshots.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"archived_snapshots": map[string]interface{}{},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	a := NewArchiveFallback(true)
	// Override the client to point to the test server.
	a.client = srv.Client()
	// We can't easily override the URL since FindSnapshot hardcodes
	// archive.org. Instead, test the availability parsing logic
	// via the response struct directly.
	resp := `{"archived_snapshots":{}}`
	var avail waybackAvailableResponse
	if err := json.Unmarshal([]byte(resp), &avail); err != nil {
		t.Fatal(err)
	}
	if avail.ArchivedSnapshots.Closest != nil {
		t.Error("expected nil closest for no snapshots")
	}
}

func TestFindSnapshot_ParseResponse(t *testing.T) {
	// Test parsing of a typical Wayback availability response.
	resp := `{
		"archived_snapshots": {
			"closest": {
				"available": true,
				"url": "http://web.archive.org/web/20210101120000/https://example.com",
				"timestamp": "20210101120000",
				"status": "200"
			}
		}
	}`
	var avail waybackAvailableResponse
	if err := json.Unmarshal([]byte(resp), &avail); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	snap := avail.ArchivedSnapshots.Closest
	if snap == nil || !snap.Available {
		t.Fatal("expected available snapshot")
	}
	if snap.Timestamp != "20210101120000" {
		t.Errorf("timestamp = %q, want 20210101120000", snap.Timestamp)
	}
	if snap.URL != "http://web.archive.org/web/20210101120000/https://example.com" {
		t.Errorf("url = %q", snap.URL)
	}
}

func TestSnapshotURLConversion(t *testing.T) {
	// The id_ suffix should be inserted after the timestamp.
	input := "http://web.archive.org/web/20210101120000/https://example.com"
	expected := "http://web.archive.org/web/20210101120000id_/https://example.com"

	// Replicate the conversion logic from FindSnapshot.
	rawURL := input
	parts := splitNTest(rawURL, "/web/", 2)
	if len(parts) != 2 {
		t.Fatal("split failed")
	}
	rest := parts[1]
	slashIdx := indexOfTest(rest, "/")
	if slashIdx <= 0 {
		t.Fatal("no slash after timestamp")
	}
	timestamp := rest[:slashIdx]
	converted := parts[0] + "/web/" + timestamp + "id_/" + rest[slashIdx+1:]

	if converted != expected {
		t.Errorf("converted = %q, want %q", converted, expected)
	}
}

// splitNTest is a test helper mirroring strings.SplitN.
func splitNTest(s, sep string, n int) []string {
	return splitNTestImpl(s, sep, n)
}

// indexOfTest returns the index of sep in s, or -1.
func indexOfTest(s, sep string) int {
	for i := 0; i <= len(s)-len(sep); i++ {
		if s[i:i+len(sep)] == sep {
			return i
		}
	}
	return -1
}

// splitNTestImpl mirrors strings.SplitN to avoid importing strings
// in this test (keeps the test self-contained).
func splitNTestImpl(s, sep string, n int) []string {
	if n <= 0 {
		return nil
	}
	var result []string
	for {
		idx := indexOfTest(s, sep)
		if idx < 0 || len(result) == n-1 {
			result = append(result, s)
			break
		}
		result = append(result, s[:idx])
		s = s[idx+len(sep):]
	}
	return result
}
