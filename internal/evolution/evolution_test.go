// Package evolution provides the skill self-evolution system.
package evolution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/km269/wukong/internal/util"
)

// ============================================================================
// Store Tests
// ============================================================================

func TestStore_CreateAndGetVersion(t *testing.T) {
	dbPool, cleanup := setupTestDB(t)
	defer cleanup()

	store, err := NewVersionStore(dbPool)
	if err != nil {
		t.Fatalf("NewVersionStore: %v", err)
	}

	ver := &SkillVersion{
		SkillName:    "test-skill",
		VersionNumber: 1,
		BackupPath:   "/tmp/test/SKILL.v001.md",
		FileHash:     "abc123",
		PatchReason:  "Fixed missing prerequisite",
	}
	if err := store.CreateVersion(ver); err != nil {
		t.Fatalf("CreateVersion: %v", err)
	}
	if ver.ID == 0 {
		t.Error("expected non-zero ID after CreateVersion")
	}

	// Get current version
	current, err := store.GetCurrentVersion("test-skill")
	if err != nil {
		t.Fatalf("GetCurrentVersion: %v", err)
	}
	if current != 1 {
		t.Errorf("expected version 1, got %d", current)
	}

	// Get specific version
	retrieved, err := store.GetVersion("test-skill", 1)
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if retrieved.SkillName != "test-skill" {
		t.Errorf("skill name mismatch: %s", retrieved.SkillName)
	}
	if retrieved.VersionNumber != 1 {
		t.Errorf("version number mismatch: %d", retrieved.VersionNumber)
	}
	if retrieved.BackupPath != "/tmp/test/SKILL.v001.md" {
		t.Errorf("backup path mismatch: %s", retrieved.BackupPath)
	}
}

func TestStore_ListVersions(t *testing.T) {
	dbPool, cleanup := setupTestDB(t)
	defer cleanup()

	store, err := NewVersionStore(dbPool)
	if err != nil {
		t.Fatalf("NewVersionStore: %v", err)
	}

	// Create 3 versions
	for i := 1; i <= 3; i++ {
		ver := &SkillVersion{
			SkillName:    "list-skill",
			VersionNumber: i,
			BackupPath:   "/tmp/list/SKILL.v" +
				formatVersion(i) + ".md",
		}
		if err := store.CreateVersion(ver); err != nil {
			t.Fatalf("CreateVersion %d: %v", i, err)
		}
	}

	versions, err := store.ListVersions("list-skill")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 3 {
		t.Errorf("expected 3 versions, got %d", len(versions))
	}
	// Should be newest first
	if versions[0].VersionNumber != 3 {
		t.Errorf("expected version 3 first, got %d",
			versions[0].VersionNumber)
	}
}

func TestStore_PruneOldVersions(t *testing.T) {
	dbPool, cleanup := setupTestDB(t)
	defer cleanup()

	store, err := NewVersionStore(dbPool)
	if err != nil {
		t.Fatalf("NewVersionStore: %v", err)
	}

	// Create 5 versions
	for i := 1; i <= 5; i++ {
		ver := &SkillVersion{
			SkillName:    "prune-skill",
			VersionNumber: i,
			BackupPath:   "/tmp/prune/SKILL.v" +
				formatVersion(i) + ".md",
		}
		if err := store.CreateVersion(ver); err != nil {
			t.Fatalf("CreateVersion %d: %v", i, err)
		}
	}

	// Prune to keep only 3
	deleted, err := store.PruneOldVersions("prune-skill", 3)
	if err != nil {
		t.Fatalf("PruneOldVersions: %v", err)
	}
	if deleted != 2 {
		t.Errorf("expected 2 deleted, got %d", deleted)
	}

	versions, err := store.ListVersions("prune-skill")
	if err != nil {
		t.Fatalf("ListVersions after prune: %v", err)
	}
	if len(versions) != 3 {
		t.Errorf("expected 3 versions after prune, got %d",
			len(versions))
	}
}

func TestStore_RecordEvolution(t *testing.T) {
	dbPool, cleanup := setupTestDB(t)
	defer cleanup()

	store, err := NewVersionStore(dbPool)
	if err != nil {
		t.Fatalf("NewVersionStore: %v", err)
	}

	rec := &EvolutionRecord{
		SkillName:      "record-skill",
		SessionID:      "session-1",
		TraceJSON:      `{"test":true}`,
		HasIssue:       true,
		PatchApplied:   true,
		PatchReason:    "Test reason",
		PatchConfidence: 0.85,
		VersionBefore:  1,
		VersionAfter:   2,
	}
	if err := store.RecordEvolution(rec); err != nil {
		t.Fatalf("RecordEvolution: %v", err)
	}

	records, err := store.ListRecentRecords("record-skill", 10)
	if err != nil {
		t.Fatalf("ListRecentRecords: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if !records[0].HasIssue {
		t.Error("expected HasIssue to be true")
	}
	if !records[0].PatchApplied {
		t.Error("expected PatchApplied to be true")
	}
}

func TestStore_CountPatchesToday(t *testing.T) {
	dbPool, cleanup := setupTestDB(t)
	defer cleanup()

	store, err := NewVersionStore(dbPool)
	if err != nil {
		t.Fatalf("NewVersionStore: %v", err)
	}

	// Initially zero
	count, err := store.CountPatchesToday("count-skill")
	if err != nil {
		t.Fatalf("CountPatchesToday: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}

	// Add a patch
	rec := &EvolutionRecord{
		SkillName:      "count-skill",
		PatchApplied:   true,
		PatchReason:    "test",
		PatchConfidence: 0.8,
	}
	if err := store.RecordEvolution(rec); err != nil {
		t.Fatalf("RecordEvolution: %v", err)
	}

	count, err = store.CountPatchesToday("count-skill")
	if err != nil {
		t.Fatalf("CountPatchesToday: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1, got %d", count)
	}
}

func TestStore_GetLastPatchTime(t *testing.T) {
	dbPool, cleanup := setupTestDB(t)
	defer cleanup()

	store, err := NewVersionStore(dbPool)
	if err != nil {
		t.Fatalf("NewVersionStore: %v", err)
	}

	// No patches yet
	lastPatch, err := store.GetLastPatchTime("time-skill")
	if err != nil {
		t.Fatalf("GetLastPatchTime: %v", err)
	}
	if !lastPatch.IsZero() {
		t.Error("expected zero time when no patches exist")
	}

	// Add a patch
	rec := &EvolutionRecord{
		SkillName:      "time-skill",
		PatchApplied:   true,
		PatchReason:    "test",
		PatchConfidence: 0.8,
	}
	if err := store.RecordEvolution(rec); err != nil {
		t.Fatalf("RecordEvolution: %v", err)
	}

	lastPatch, err = store.GetLastPatchTime("time-skill")
	if err != nil {
		t.Fatalf("GetLastPatchTime: %v", err)
	}
	if lastPatch.IsZero() {
		t.Error("expected non-zero time after patch")
	}
}

// ============================================================================
// Analyzer Tests
// ============================================================================

func TestStripMarkdownCodeBlock(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "```json\n{\"has_issue\": false}\n```",
			expected: "{\"has_issue\": false}",
		},
		{
			input:    "```\n{\"has_issue\": false}\n```",
			expected: "{\"has_issue\": false}",
		},
		{
			input:    "{\"has_issue\": false}",
			expected: "{\"has_issue\": false}",
		},
		{
			input:    "  {\"has_issue\": false}  ",
			expected: "{\"has_issue\": false}",
		},
	}

	for _, tc := range tests {
		result := stripMarkdownCodeBlock(tc.input)
		if result != tc.expected {
			t.Errorf("stripMarkdownCodeBlock(%q) = %q, want %q",
				tc.input, result, tc.expected)
		}
	}
}

func TestParseAnalysisResponse_NoIssue(t *testing.T) {
	resp := `{"has_issue": false}`
	suggestion, err := parseAnalysisResponse(resp, "test-skill")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if suggestion != nil {
		t.Error("expected nil suggestion for no-issue response")
	}
}

func TestParseAnalysisResponse_WithIssue(t *testing.T) {
	resp := `{
		"has_issue": true,
		"problem_type": "missing_prerequisite",
		"reason": "The skill should first check if the file exists",
		"patch": "## Before reading\nAlways check if the file exists using file_exists tool.",
		"confidence": 0.85
	}`
	suggestion, err := parseAnalysisResponse(resp, "test-skill")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if suggestion == nil {
		t.Fatal("expected non-nil suggestion")
	}
	if suggestion.SkillName != "test-skill" {
		t.Errorf("skill name: want test-skill, got %s",
			suggestion.SkillName)
	}
	if suggestion.ProblemType != "missing_prerequisite" {
		t.Errorf("problem type: want missing_prerequisite, got %s",
			suggestion.ProblemType)
	}
	if suggestion.Confidence != 0.85 {
		t.Errorf("confidence: want 0.85, got %f",
			suggestion.Confidence)
	}
}

func TestParseAnalysisResponse_InvalidJSON(t *testing.T) {
	resp := "not json at all"
	_, err := parseAnalysisResponse(resp, "test-skill")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseAnalysisResponse_IssueButEmptyPatch(t *testing.T) {
	resp := `{"has_issue": true, "patch": ""}`
	_, err := parseAnalysisResponse(resp, "test-skill")
	if err == nil {
		t.Error("expected error when has_issue=true but patch is empty")
	}
}

// ============================================================================
// Patcher Tests
// ============================================================================

func TestFindYAMLEnd(t *testing.T) {
	tests := []struct {
		content  string
		expected int
	}{
		{
			// "---\nname: test\n---\n\n# Body"
			//  0123456789...  closing "---" at 15-17, end at 17
			content:  "---\nname: test\n---\n\n# Body",
			expected: 17,
		},
		{
			content:  "No front matter here",
			expected: -1,
		},
		{
			content:  "---\nname: test\n",
			expected: -1,
		},
	}

	for _, tc := range tests {
		result := findYAMLEnd(tc.content)
		if result != tc.expected {
			t.Errorf("findYAMLEnd(%q) = %d, want %d",
				tc.content, result, tc.expected)
		}
	}
}

func TestAppendPatchToBody_WithYAML(t *testing.T) {
	content := "---\nname: test-skill\ndescription: A test skill\n---\n\n## Steps\n1. Read the file\n2. Process it\n"
	suggestion := &PatchSuggestion{
		SkillName:   "test-skill",
		ProblemType: "missing_error_handling",
		Reason:      "Add error handling for file operations",
		DiffContent: "## Error Handling\nIf file_read fails, check if the path is correct.",
		Confidence:  0.8,
		GeneratedAt: time.Now(),
	}

	result := appendPatchToBody(content, suggestion)

	if !strings.Contains(result, "## Steps") {
		t.Error("original body content missing")
	}
	if !strings.Contains(result, "## Error Handling") {
		t.Error("patch content missing")
	}
	if !strings.Contains(result, "EVOLUTION PATCH") {
		t.Error("evolution patch marker missing")
	}
	if strings.Count(result, "---") < 2 {
		t.Error("YAML front matter may be broken")
	}
}

func TestAppendPatchToBody_NoYAML(t *testing.T) {
	content := "## Steps\n1. Do something\n"
	suggestion := &PatchSuggestion{
		SkillName:   "no-yaml-skill",
		ProblemType: "ambiguous_wording",
		Reason:      "Make wording clearer",
		DiffContent: "## Updated Steps\n1. First check availability\n2. Then proceed",
		Confidence:  0.9,
		GeneratedAt: time.Now(),
	}

	result := appendPatchToBody(content, suggestion)

	if !strings.Contains(result, "---") {
		t.Error("should have auto-generated YAML front matter")
	}
	if !strings.Contains(result, "no-yaml-skill") {
		t.Error("should contain skill name in auto-generated front matter")
	}
}

func TestValidateContent(t *testing.T) {
	if err := validateContent(""); err == nil {
		t.Error("expected error for empty content")
	}
	if err := validateContent("valid content"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	// Very large content (100KB+)
	large := strings.Repeat("x", 100*1024+1)
	if err := validateContent(large); err == nil {
		t.Error("expected error for oversized content")
	}
}

func TestPatcher_ApplyPatch(t *testing.T) {
	// Create a temporary skill directory
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("create skill dir: %v", err)
	}

	// Write a SKILL.md file
	skillContent := "---\nname: test-skill\ndescription: Test\n---\n\n## Steps\n1. Do X\n2. Do Y\n"
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(
		skillPath, []byte(skillContent), 0644,
	); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	// Create patcher with in-memory store
	dbPool, cleanup := setupTestDB(t)
	defer cleanup()

	store, err := NewVersionStore(dbPool)
	if err != nil {
		t.Fatalf("NewVersionStore: %v", err)
	}
	patcher := NewEvolutionPatcher(store, 5)

	suggestion := &PatchSuggestion{
		SkillName:   "test-skill",
		ProblemType: "missing_error_handling",
		Reason:      "Add error handling",
		DiffContent: "## Error Handling\nAlways handle errors gracefully.",
		Confidence:  0.85,
		GeneratedAt: time.Now(),
	}

	newVersion, err := patcher.ApplyPatch(suggestion, skillDir)
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	if newVersion != 1 {
		t.Errorf("expected version 1, got %d", newVersion)
	}

	// Verify SKILL.md was updated
	updated, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read updated SKILL.md: %v", err)
	}
	if !strings.Contains(string(updated), "Error Handling") {
		t.Error("patch content not found in updated SKILL.md")
	}

	// Verify backup was created
	backupPath := filepath.Join(skillDir, "SKILL.v001.md")
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Error("backup file was not created")
	} else {
		backupContent, _ := os.ReadFile(backupPath)
		if string(backupContent) != skillContent {
			t.Error("backup content doesn't match original")
		}
	}

	// Verify version in database
	currentVer, err := store.GetCurrentVersion("test-skill")
	if err != nil {
		t.Fatalf("get current version: %v", err)
	}
	if currentVer != 1 {
		t.Errorf("expected version 1 in db, got %d", currentVer)
	}
}

func TestPatcher_ApplyPatch_NonexistentSkill(t *testing.T) {
	dbPool, cleanup := setupTestDB(t)
	defer cleanup()

	store, _ := NewVersionStore(dbPool)
	patcher := NewEvolutionPatcher(store, 5)

	suggestion := &PatchSuggestion{
		SkillName: "nonexistent",
		Reason:    "test",
		GeneratedAt: time.Now(),
	}
	_, err := patcher.ApplyPatch(
		suggestion, "/nonexistent/path",
	)
	if err == nil {
		t.Error("expected error for nonexistent skill")
	}
}

// ============================================================================
// Helpers
// ============================================================================

// setupTestDB creates a temporary SQLite database for testing.
func setupTestDB(t *testing.T) (*util.DatabasePool, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_evolution.db")

	pool := util.NewDatabasePool(dbPath)

	cleanup := func() {
		pool.Close()
	}
	return pool, cleanup
}

// formatVersion formats a version number for backup filenames.
func formatVersion(n int) string {
	switch {
	case n < 10:
		return "00" + string(byte('0'+n))
	case n < 100:
		return "0" + string(byte('0'+n/10)) +
			string(byte('0'+n%10))
	default:
		return "100"
	}
}
