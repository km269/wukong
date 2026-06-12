package apps

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/km269/wukong/internal/config"
)

func TestNewManager(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
	if len(mgr.ListApps()) != 0 {
		t.Error("expected empty app list on new manager")
	}
}

func TestCreateApp(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	app, err := mgr.CreateApp("test-app", "A test app",
		"<html><body>Hello</body></html>")
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if app.Name != "test-app" {
		t.Errorf("expected name 'test-app', got %q", app.Name)
	}
	if app.Description != "A test app" {
		t.Errorf("expected description 'A test app', got %q",
			app.Description)
	}

	// Verify file exists
	if _, err := os.Stat(app.FilePath); os.IsNotExist(err) {
		t.Error("app file was not created")
	}
}

func TestGetApp(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.CreateApp("my-app", "desc",
		"<html></html>")
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	app, ok := mgr.GetApp("my-app")
	if !ok {
		t.Fatal("expected to find app 'my-app'")
	}
	if app.Name != "my-app" {
		t.Errorf("expected name 'my-app', got %q", app.Name)
	}

	_, ok = mgr.GetApp("nonexistent")
	if ok {
		t.Error("expected not to find nonexistent app")
	}
}

func TestListApps(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, _ = mgr.CreateApp("app1", "d1", "<html></html>")
	_, _ = mgr.CreateApp("app2", "d2", "<html></html>")

	apps := mgr.ListApps()
	if len(apps) != 2 {
		t.Errorf("expected 2 apps, got %d", len(apps))
	}
}

func TestDeleteApp(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	app, _ := mgr.CreateApp("to-delete", "desc",
		"<html></html>")

	err = mgr.DeleteApp("to-delete")
	if err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}

	// App should be gone
	_, ok := mgr.GetApp("to-delete")
	if ok {
		t.Error("expected app to be deleted")
	}

	// File should be removed
	if _, err := os.Stat(app.FilePath); !os.IsNotExist(err) {
		t.Error("expected app file to be deleted")
	}

	// Delete nonexistent app should error
	err = mgr.DeleteApp("nonexistent")
	if err == nil {
		t.Error("expected error deleting nonexistent app")
	}
}

func TestUpdateApp(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, _ = mgr.CreateApp("update-me", "desc",
		"<html><body>v1</body></html>")

	app, err := mgr.UpdateApp("update-me",
		"<html><body>v2</body></html>")
	if err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	if app.Name != "update-me" {
		t.Errorf("expected name 'update-me', got %q", app.Name)
	}

	// Update nonexistent app should error
	_, err = mgr.UpdateApp("nonexistent", "<html></html>")
	if err == nil {
		t.Error("expected error updating nonexistent app")
	}
}

func TestGenerateAppTemplate(t *testing.T) {
	tmpl := GenerateAppTemplate("My App")
	if tmpl == "" {
		t.Error("expected non-empty template")
	}
	if !contains(tmpl, "My App") {
		t.Error("template should contain the title")
	}
	if !contains(tmpl, "<!DOCTYPE html>") {
		t.Error("template should be valid HTML")
	}
}

func TestSanitizeAppName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"my-app", "my-app"},
		{"My_App", "My_App"},
		{"test123", "test123"},
		{"hello world!", "helloworld"},
		{"spécial", "spcial"},
		{"!!!", "app"},
		{"", "app"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := sanitizeAppName(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeAppName(%q) = %q, want %q",
					tt.input, result, tt.expected)
			}
		})
	}
}

func TestNewManager_DefaultDir(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
}

func TestLoadExistingApps(t *testing.T) {
	tmpDir := t.TempDir()

	// Create some HTML files manually
	os.WriteFile(filepath.Join(tmpDir, "existing.html"),
		[]byte("<html></html>"), 0644)

	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	app, ok := mgr.GetApp("existing")
	if !ok {
		t.Error("expected to load existing app from disk")
	}
	if app.Name != "existing" {
		t.Errorf("expected name 'existing', got %q", app.Name)
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
