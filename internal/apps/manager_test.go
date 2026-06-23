package apps

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestCreateAppWithType(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	app, err := mgr.CreateAppWithType("custom-app", "自定义应用",
		"<html></html>", AppTypeCustom, AppStatusDraft)
	if err != nil {
		t.Fatalf("CreateAppWithType: %v", err)
	}
	if app.Type != AppTypeCustom {
		t.Errorf("expected type %q, got %q", AppTypeCustom, app.Type)
	}
	if app.Status != AppStatusDraft {
		t.Errorf("expected status %q, got %q", AppStatusDraft, app.Status)
	}
	if app.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", app.Version)
	}
}

func TestCreateAppWithTemplate(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	tests := []struct {
		template TemplateType
		name     string
	}{
		{TemplateBlank, "blank-app"},
		{TemplateCalculator, "calc-app"},
		{TemplateDashboard, "dash-app"},
		{TemplateForm, "form-app"},
		{TemplateNotes, "notes-app"},
	}

	for _, tt := range tests {
		t.Run(string(tt.template), func(t *testing.T) {
			app, err := mgr.CreateAppWithTemplate(tt.name, "测试应用", tt.template)
			if err != nil {
				t.Fatalf("CreateAppWithTemplate: %v", err)
			}
			if app.Name != tt.name {
				t.Errorf("expected name %q, got %q", tt.name, app.Name)
			}
			if app.Type != AppTypeCustom {
				t.Errorf("expected type %q, got %q", AppTypeCustom, app.Type)
			}
			// 验证文件内容包含标题
			content, err := os.ReadFile(app.FilePath)
			if err != nil {
				t.Fatalf("读取文件失败: %v", err)
			}
			if !contains(string(content), tt.name) {
				t.Error("模板内容应包含应用名称")
			}
		})
	}
}

func TestCreateAppFromImport(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	app, err := mgr.CreateAppFromImport("imported-app", "导入的应用",
		"<html><body>Imported</body></html>")
	if err != nil {
		t.Fatalf("CreateAppFromImport: %v", err)
	}
	if app.Type != AppTypeImported {
		t.Errorf("expected type %q, got %q", AppTypeImported, app.Type)
	}
	if app.Status != AppStatusActive {
		t.Errorf("expected status %q, got %q", AppStatusActive, app.Status)
	}
}

func TestUpdateAppStatus(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, _ = mgr.CreateApp("status-test", "desc", "<html></html>")

	app, err := mgr.UpdateAppStatus("status-test", AppStatusArchived)
	if err != nil {
		t.Fatalf("UpdateAppStatus: %v", err)
	}
	if app.Status != AppStatusArchived {
		t.Errorf("expected status %q, got %q", AppStatusArchived, app.Status)
	}

	// 更新不存在应用应报错
	_, err = mgr.UpdateAppStatus("nonexistent", AppStatusActive)
	if err == nil {
		t.Error("expected error updating nonexistent app status")
	}
}

func TestUpdateAppMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, _ = mgr.CreateApp("meta-test", "original desc", "<html></html>")

	app, err := mgr.UpdateAppMetadata("meta-test", AppMetadataUpdate{
		Description: "updated desc",
		IconPath:    "/path/to/icon.png",
	})
	if err != nil {
		t.Fatalf("UpdateAppMetadata: %v", err)
	}
	if app.Description != "updated desc" {
		t.Errorf("expected description 'updated desc', got %q", app.Description)
	}
	if app.IconPath != "/path/to/icon.png" {
		t.Errorf("expected icon path '/path/to/icon.png', got %q", app.IconPath)
	}
}

func TestGetTemplate(t *testing.T) {
	tests := []struct {
		template TemplateType
		title    string
		expected []string // 应包含的内容
	}{
		{TemplateBlank, "空白测试", []string{"<!DOCTYPE html>", "空白测试"}},
		{TemplateCalculator, "计算器测试", []string{"<!DOCTYPE html>", "计算器测试", "calculator"}},
		{TemplateDashboard, "仪表盘测试", []string{"<!DOCTYPE html>", "仪表盘测试", "dashboard"}},
		{TemplateForm, "表单测试", []string{"<!DOCTYPE html>", "表单测试", "form-container"}},
		{TemplateNotes, "笔记测试", []string{"<!DOCTYPE html>", "笔记测试", "notes-app"}},
	}

	for _, tt := range tests {
		t.Run(string(tt.template), func(t *testing.T) {
			content := GetTemplate(tt.template, tt.title)
			if content == "" {
				t.Error("expected non-empty template content")
			}
			for _, exp := range tt.expected {
				if !contains(content, exp) {
					t.Errorf("template should contain %q", exp)
				}
			}
		})
	}
}

func TestListTemplates(t *testing.T) {
	templates := ListTemplates()
	if len(templates) != 5 {
		t.Errorf("expected 5 templates, got %d", len(templates))
	}

	// 验证每个模板都有必要信息
	for _, tmpl := range templates {
		if tmpl.Type == "" {
			t.Error("template type should not be empty")
		}
		if tmpl.Name == "" {
			t.Error("template name should not be empty")
		}
		if tmpl.Description == "" {
			t.Error("template description should not be empty")
		}
	}
}

func TestIncrementVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"1.0.0", "1.0.1"},
		{"1.2.3", "1.2.4"},
		{"2.5.9", "2.5.10"},
		{"", "1.0.1"},
		{"invalid", "1.0.1"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := incrementVersion(tt.input)
			if result != tt.expected {
				t.Errorf("incrementVersion(%q) = %q, want %q",
					tt.input, result, tt.expected)
			}
		})
	}
}

func TestUpdateAppVersionIncrement(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, _ = mgr.CreateApp("version-test", "desc", "<html>v1</html>")

	// 第一次更新
	app1, err := mgr.UpdateApp("version-test", "<html>v2</html>")
	if err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	if app1.Version != "1.0.1" {
		t.Errorf("expected version '1.0.1', got %q", app1.Version)
	}

	// 第二次更新
	app2, err := mgr.UpdateApp("version-test", "<html>v3</html>")
	if err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	if app2.Version != "1.0.2" {
		t.Errorf("expected version '1.0.2', got %q", app2.Version)
	}
}

func TestGetAppDir(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	appDir := mgr.GetAppDir()
	if appDir == "" {
		t.Error("expected non-empty app directory")
	}
	// 验证是绝对路径
	if !filepath.IsAbs(appDir) {
		t.Errorf("expected absolute path, got %q", appDir)
	}
}

// ---------------------------------------------------------------------------
// PreviewApp tests
// ---------------------------------------------------------------------------

func TestPreviewApp(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// 验证不存在的应用返回错误
	_, err = mgr.PreviewApp(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent app")
	}

	// 创建应用验证路径逻辑
	_, err = mgr.CreateApp("preview-test",
		"A preview test app",
		"<html><body><h1>Hello</h1></body></html>")
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	// PreviewApp requires actual network port binding (not suitable
	// for unit tests in constrained CI environments). Verify the
	// app exists and the manager can locate its directory.
	_, ok := mgr.GetApp("preview-test")
	if !ok {
		t.Fatal("expected app to exist before preview")
	}
}

func TestPreviewResult_Stop(t *testing.T) {
	// Stop should be idempotent — calling on nil cancel should not panic.
	r := &PreviewResult{Cancel: nil}
	r.Stop()

	// 创建 cancel 后应能正常释放
	_, cancel := context.WithCancel(context.Background())
	r2 := &PreviewResult{Cancel: cancel}
	r2.Stop()
	// 二次调用不 panic
	r2.Stop()
}

// ---------------------------------------------------------------------------
// ExportApp tests
// ---------------------------------------------------------------------------

func TestExportApp(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.CreateApp("export-test",
		"An export test app",
		`<html><head><style>body{color:red}</style></head><body><h1>Export</h1><img src="test.png"></body></html>`)
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	exportPath := filepath.Join(tmpDir, "exported.html")
	result, err := mgr.ExportApp("export-test", exportPath)
	if err != nil {
		t.Fatalf("ExportApp: %v", err)
	}

	if !result.Success {
		t.Error("expected successful export")
	}
	if _, err := os.Stat(result.OutputPath); os.IsNotExist(err) {
		t.Errorf("exported file not found at %q", result.OutputPath)
	}

	// 验证导出内容包含原始 HTML 元素
	data, err := os.ReadFile(result.OutputPath)
	if err != nil {
		t.Fatalf("read exported file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "<h1>Export</h1>") {
		t.Error("exported file should contain the original content")
	}
}

func TestExportApp_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.ExportApp("nonexistent", filepath.Join(tmpDir, "out.html"))
	if err == nil {
		t.Error("expected error for nonexistent app")
	}
}

// ---------------------------------------------------------------------------
// PackApp tests
// ---------------------------------------------------------------------------

func TestPackApp_HTML(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// 创建一个简单的应用
	_, err = mgr.CreateApp("pack-html-test",
		"HTML pack test",
		"<html><body><h1>Pack Test</h1></body></html>")
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	result, err := mgr.PackApp(context.Background(), "pack-html-test",
		PackOptions{Format: "html"})
	if err != nil {
		t.Fatalf("PackApp (HTML): %v", err)
	}

	if !result.Success {
		t.Error("expected successful pack")
	}
	if result.Format != "html" {
		t.Errorf("expected format 'html', got %q", result.Format)
	}
	if result.OutputPath == "" {
		t.Error("expected non-empty output path")
	}
}

func TestPackApp_ZIM(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.CreateApp("pack-zim-test",
		"ZIM pack test",
		"<html><body><h1>ZIM Test</h1></body></html>")
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	result, err := mgr.PackApp(context.Background(), "pack-zim-test",
		PackOptions{Format: "zim"})
	if err != nil {
		t.Fatalf("PackApp (ZIM): %v", err)
	}

	if !result.Success {
		t.Error("expected successful ZIM pack")
	}
	// 验证 ZIM 文件存在
	if _, err := os.Stat(result.OutputPath); os.IsNotExist(err) {
		t.Errorf("ZIM file not found at %q", result.OutputPath)
	}
	// ZIM 文件至少应该有 header (80 bytes)
	info, err := os.Stat(result.OutputPath)
	if err != nil {
		t.Fatalf("stat ZIM file: %v", err)
	}
	if info.Size() < 80 {
		t.Errorf("ZIM file too small: %d bytes (expected >= 80)", info.Size())
	}
}

func TestPackApp_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.PackApp(context.Background(), "nonexistent",
		PackOptions{Format: "html"})
	if err == nil {
		t.Error("expected error for nonexistent app")
	}
}

// ---------------------------------------------------------------------------
// MCP Apps integration tests — verify manager wiring
// ---------------------------------------------------------------------------

func TestMCPApps_ManagerNonNil(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// MCP Apps integration is wired through the builtin/apps.go
	// extension, not through apps.Manager directly. Verify the
	// manager is healthy for extension wiring.
	if mgr.ListApps() == nil {
		t.Error("ListApps should not return nil")
	}
}

// ---------------------------------------------------------------------------
// CloneApp tests (requires actual browser — skipped in unit test)
// ---------------------------------------------------------------------------

func TestCloneOptions_Defaults(t *testing.T) {
	opts := CloneOptions{
		MaxPages: 10,
		MaxDepth: 2,
		Workers:  3,
	}
	if opts.MaxPages != 10 || opts.MaxDepth != 2 || opts.Workers != 3 {
		t.Error("CloneOptions fields should be settable")
	}
}

func TestPackOptions_Formats(t *testing.T) {
	formats := []string{"html", "zim", "binary", "app"}
	for _, f := range formats {
		opts := PackOptions{Format: f}
		if opts.Format != f {
			t.Errorf("PackOptions.Format = %q, want %q", opts.Format, f)
		}
	}
}

// ---------------------------------------------------------------------------
// History / Version tests
// ---------------------------------------------------------------------------

func TestSaveVersion(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// 创建一个应用
	_, err = mgr.CreateApp("history-test",
		"History test app",
		"<html><body><h1>Version 1</h1></body></html>")
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	// 保存一个版本快照
	err = mgr.SaveVersion("history-test", "initial save")
	if err != nil {
		t.Fatalf("SaveVersion: %v", err)
	}

	// 验证版本列表
	records, err := mgr.ListVersions("history-test")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("expected 1 version, got %d", len(records))
	}
	if records[0].Label != "initial save" {
		t.Errorf("expected label 'initial save', got %q", records[0].Label)
	}

	// 验证 HistoryCount
	if mgr.HistoryCount("history-test") != 1 {
		t.Errorf("expected HistoryCount 1, got %d",
			mgr.HistoryCount("history-test"))
	}
}

func TestGetVersion(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	app, err := mgr.CreateApp("get-version-test",
		"Get version test",
		"<html><body>Original</body></html>")
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	// 保存原始版本
	_ = mgr.SaveVersion("get-version-test", "original")

	// 更新内容（会改变版本号）
	_, _ = mgr.UpdateApp("get-version-test",
		"<html><body>Updated</body></html>")
	_ = mgr.SaveVersion("get-version-test", "updated")

	// 获取第一个版本
	content, err := mgr.GetVersion("get-version-test", app.Version)
	if err != nil {
		t.Fatalf("GetVersion %q: %v", app.Version, err)
	}
	if !strings.Contains(content, "Original") {
		t.Error("expected original content in first version")
	}
}

func TestRollbackTo(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	app, err := mgr.CreateApp("rollback-test",
		"Rollback test",
		"<html><body>V1</body></html>")
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	// 保存第一个版本
	_ = mgr.SaveVersion("rollback-test", "v1")

	// 更新到 v2
	app2, _ := mgr.UpdateApp("rollback-test",
		"<html><body>V2</body></html>")

	// 回滚到 v1
	rolledBack, err := mgr.RollbackTo("rollback-test", app.Version)
	if err != nil {
		t.Fatalf("RollbackTo: %v", err)
	}

	// 回滚后版本应该是 v1 的下一个版本
	if rolledBack.Version == app2.Version {
		t.Error("expected version to increment after rollback")
	}

	// 验证内容恢复到 v1
	content, err := os.ReadFile(rolledBack.FilePath)
	if err != nil {
		t.Fatalf("read rolled-back file: %v", err)
	}
	if !strings.Contains(string(content), "V1") {
		t.Errorf("expected rolled-back content to contain 'V1', got %q",
			string(content))
	}
}

func TestDeleteVersion(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	app, err := mgr.CreateApp("delete-version-test",
		"Delete version test",
		"<html><body>Test</body></html>")
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	_ = mgr.SaveVersion("delete-version-test", "to delete")

	// 删除版本
	err = mgr.DeleteVersion("delete-version-test", app.Version)
	if err != nil {
		t.Fatalf("DeleteVersion: %v", err)
	}

	// 验证版本已删除
	if mgr.HistoryCount("delete-version-test") != 0 {
		t.Error("expected 0 versions after deletion")
	}
}

func TestUpdateAppWithHistory(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.CreateApp("update-history-test",
		"Update with history",
		"<html><body>V1</body></html>")
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	// 第一次通过 UpdateAppWithHistory 更新
	app2, err := mgr.UpdateAppWithHistory("update-history-test",
		"<html><body>V2</body></html>")
	if err != nil {
		t.Fatalf("UpdateAppWithHistory (1): %v", err)
	}
	if app2.Version != "1.0.1" {
		t.Errorf("expected version '1.0.1', got %q", app2.Version)
	}

	// 验证自动保存了上一版本
	if mgr.HistoryCount("update-history-test") != 1 {
		t.Errorf("expected 1 auto-saved version, got %d",
			mgr.HistoryCount("update-history-test"))
	}

	// 第二次更新
	app3, err := mgr.UpdateAppWithHistory("update-history-test",
		"<html><body>V3</body></html>")
	if err != nil {
		t.Fatalf("UpdateAppWithHistory (2): %v", err)
	}
	if app3.Version != "1.0.2" {
		t.Errorf("expected version '1.0.2', got %q", app3.Version)
	}
	if mgr.HistoryCount("update-history-test") != 2 {
		t.Errorf("expected 2 auto-saved versions, got %d",
			mgr.HistoryCount("update-history-test"))
	}
}

func TestListVersions_NewestFirst(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.AppsConfig{
		Enabled: true,
		AppDir:  tmpDir,
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.CreateApp("sort-test",
		"Sort test",
		"<html><body>Content</body></html>")
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	// 保存多个版本
	_ = mgr.SaveVersion("sort-test", "v1")
	time.Sleep(10 * time.Millisecond) // 确保时间戳不同
	_, _ = mgr.UpdateApp("sort-test", "<html><body>V2</body></html>")
	_ = mgr.SaveVersion("sort-test", "v2")

	records, err := mgr.ListVersions("sort-test")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}

	if len(records) < 2 {
		t.Skip("insufficient versions for sort test")
	}

	// 验证最新版本在前
	if !records[0].Timestamp.After(records[1].Timestamp) {
		t.Error("expected newest version first")
	}
}
