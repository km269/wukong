package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/km269/wukong/internal/config"
)

func TestNewManager_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	cfg := &config.WukongConfig{
		ProjectDir: dir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	records := mgr.ListProjects()
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}

	// projects.json should not exist yet.
	if _, err := os.Stat(filepath.Join(dir, "projects.json")); !os.IsNotExist(err) {
		t.Errorf("projects.json should not exist until first TrackProject")
	}
}

func TestTrackProject_NewEntry(t *testing.T) {
	dir := t.TempDir()

	cfg := &config.WukongConfig{
		ProjectDir: dir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	workingDir := filepath.Join(t.TempDir(), "my-project")
	os.MkdirAll(workingDir, 0755)

	mgr.TrackProject(workingDir, "session-1", "hello world")

	records := mgr.ListProjects()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	r := records[0]
	if r.Path != workingDir {
		t.Errorf("expected path %q, got %q", workingDir, r.Path)
	}
	if r.SessionID != "session-1" {
		t.Errorf("expected sessionID session-1, got %s", r.SessionID)
	}
	if r.LastInstruction != "hello world" {
		t.Errorf("expected instruction 'hello world', got %q",
			r.LastInstruction)
	}

	// projects.json should exist now.
	if _, err := os.Stat(filepath.Join(dir, "projects.json")); err != nil {
		t.Errorf("projects.json should exist after TrackProject: %v", err)
	}
}

func TestTrackProject_UpdateExisting(t *testing.T) {
	dir := t.TempDir()

	cfg := &config.WukongConfig{
		ProjectDir: dir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	workingDir := filepath.Join(t.TempDir(), "my-project")
	os.MkdirAll(workingDir, 0755)

	mgr.TrackProject(workingDir, "session-1", "first")
	mgr.TrackProject(workingDir, "session-2", "second")

	records := mgr.ListProjects()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	r := records[0]
	if r.SessionID != "session-2" {
		t.Errorf("expected sessionID session-2, got %s", r.SessionID)
	}
	if r.LastInstruction != "second" {
		t.Errorf("expected instruction 'second', got %q",
			r.LastInstruction)
	}
}

func TestTrackProject_MaxLimit(t *testing.T) {
	dir := t.TempDir()

	cfg := &config.WukongConfig{
		ProjectDir: dir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	base := t.TempDir()

	// Insert more than maxProjects entries.
	for i := 0; i < maxProjects+5; i++ {
		wd := filepath.Join(base, "project-"+string(rune('a'+i%26))+string(rune('0'+i/26)))
		os.MkdirAll(wd, 0755)
		mgr.TrackProject(wd, "sess-"+string(rune('0'+i)), "")
	}

	records := mgr.ListProjects()
	if len(records) > maxProjects {
		t.Errorf("expected at most %d records, got %d",
			maxProjects, len(records))
	}
}

func TestUpdateInstruction(t *testing.T) {
	dir := t.TempDir()

	cfg := &config.WukongConfig{
		ProjectDir: dir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	workingDir := filepath.Join(t.TempDir(), "my-project")
	os.MkdirAll(workingDir, 0755)

	mgr.TrackProject(workingDir, "session-1", "")
	mgr.UpdateInstruction(workingDir, "updated instruction")

	records := mgr.ListProjects()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	if records[0].LastInstruction != "updated instruction" {
		t.Errorf("expected 'updated instruction', got %q",
			records[0].LastInstruction)
	}
}

func TestUpdateInstruction_LongTruncation(t *testing.T) {
	dir := t.TempDir()

	cfg := &config.WukongConfig{
		ProjectDir: dir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	workingDir := filepath.Join(t.TempDir(), "my-project")
	os.MkdirAll(workingDir, 0755)

	longInst := ""
	for i := 0; i < 300; i++ {
		longInst += "x"
	}

	mgr.TrackProject(workingDir, "session-1", "")
	mgr.UpdateInstruction(workingDir, longInst)

	records := mgr.ListProjects()
	if len(records[0].LastInstruction) > 200 {
		t.Errorf("instruction should be truncated to 200, got %d",
			len(records[0].LastInstruction))
	}
}

func TestFindByPath(t *testing.T) {
	dir := t.TempDir()

	cfg := &config.WukongConfig{
		ProjectDir: dir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	workingDir := filepath.Join(t.TempDir(), "my-project")
	os.MkdirAll(workingDir, 0755)

	mgr.TrackProject(workingDir, "session-1", "test")

	rec := mgr.FindByPath(workingDir)
	if rec == nil {
		t.Fatal("expected record, got nil")
	}
	if rec.SessionID != "session-1" {
		t.Errorf("expected session-1, got %s", rec.SessionID)
	}

	// Non-existent path.
	rec = mgr.FindByPath("/nonexistent/path")
	if rec != nil {
		t.Errorf("expected nil for non-existent path, got %v", rec)
	}
}

func TestClear(t *testing.T) {
	dir := t.TempDir()

	cfg := &config.WukongConfig{
		ProjectDir: dir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	workingDir := filepath.Join(t.TempDir(), "my-project")
	os.MkdirAll(workingDir, 0755)

	mgr.TrackProject(workingDir, "session-1", "test")
	if len(mgr.ListProjects()) != 1 {
		t.Fatal("expected 1 record before clear")
	}

	if err := mgr.Clear(); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	if len(mgr.ListProjects()) != 0 {
		t.Errorf("expected 0 records after clear, got %d",
			len(mgr.ListProjects()))
	}
}

func TestPersistence_Reload(t *testing.T) {
	dir := t.TempDir()

	cfg := &config.WukongConfig{
		ProjectDir: dir,
	}

	mgr1, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	workingDir := filepath.Join(t.TempDir(), "my-project")
	os.MkdirAll(workingDir, 0755)

	mgr1.TrackProject(workingDir, "session-1", "persist test")

	// Create a new manager pointing at the same directory.
	mgr2, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("second NewManager failed: %v", err)
	}

	records := mgr2.ListProjects()
	if len(records) != 1 {
		t.Fatalf("expected 1 record after reload, got %d",
			len(records))
	}
	if records[0].Path != workingDir {
		t.Errorf("expected path %q, got %q",
			workingDir, records[0].Path)
	}
	if records[0].SessionID != "session-1" {
		t.Errorf("expected session-1, got %s",
			records[0].SessionID)
	}
}
