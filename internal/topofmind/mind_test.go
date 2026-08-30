package topofmind

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/km269/wukong/internal/config"
)

func TestManager_New(t *testing.T) {
	cfg := &config.TopOfMindConfig{
		Enabled:         true,
		InstructionFile: ".wukong/instructions.md",
		MaxLength:       2000,
	}

	mgr := NewManager(cfg)
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
	if mgr.cfg != cfg {
		t.Error("manager config not set correctly")
	}
}

func TestManager_GetSetAppend(t *testing.T) {
	cfg := &config.TopOfMindConfig{
		Enabled:         true,
		InstructionFile: ".wukong/instructions.md",
		MaxLength:       2000,
	}

	mgr := NewManager(cfg)

	// Initially empty
	if instructions := mgr.GetInstructions(); instructions != "" {
		t.Errorf("expected empty instructions, got %q", instructions)
	}

	// Set instructions
	mgr.SetInstructions("Always use Chinese to respond")
	if got := mgr.GetInstructions(); got != "Always use Chinese to respond" {
		t.Errorf("expected set instruction, got %q", got)
	}

	// Append instructions
	mgr.AppendInstructions("Be polite")
	if got := mgr.GetInstructions(); got != "Always use Chinese to respond\nBe polite" {
		t.Errorf("expected appended instructions, got %q", got)
	}

	// Clear
	mgr.ClearInstructions()
	if got := mgr.GetInstructions(); got != "" {
		t.Errorf("expected cleared instructions, got %q", got)
	}
}

func TestManager_MaxLength(t *testing.T) {
	cfg := &config.TopOfMindConfig{
		Enabled:         true,
		InstructionFile: ".wukong/instructions.md",
		MaxLength:       10,
	}

	mgr := NewManager(cfg)
	mgr.SetInstructions("This is a very long instruction that exceeds the max length")
	if got := mgr.GetInstructions(); len(got) > 10 {
		t.Errorf("expected truncated to 10 chars, got %d: %q", len(got), got)
	}
}

func TestManager_LoadFromFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test_instructions.md")
	content := "Be concise\nUse examples"

	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	cfg := &config.TopOfMindConfig{
		Enabled:         true,
		InstructionFile: filePath,
		MaxLength:       2000,
	}

	mgr := NewManager(cfg)
	if got := mgr.GetInstructions(); got != content {
		t.Errorf("expected file content, got %q", got)
	}
}

func TestManager_FormatForPrompt(t *testing.T) {
	cfg := &config.TopOfMindConfig{
		Enabled:         true,
		InstructionFile: ".wukong/instructions.md",
		MaxLength:       2000,
	}

	mgr := NewManager(cfg)

	// Empty should return empty
	if formatted := mgr.FormatForPrompt(); formatted != "" {
		t.Errorf("expected empty format for empty instructions, got %q", formatted)
	}

	mgr.SetInstructions("Always be concise")
	formatted := mgr.FormatForPrompt()
	if formatted == "" {
		t.Error("expected non-empty format after set")
	}
	// Should contain the instruction
	if !contains(formatted, "Always be concise") {
		t.Errorf("formatted output missing instruction: %q", formatted)
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

// newFileManager creates a manager backed by a real temp instruction file.
func newFileManager(t *testing.T, content string) (*Manager, string) {
	t.Helper()
	filePath := filepath.Join(t.TempDir(), "instructions.md")
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(&config.TopOfMindConfig{
		Enabled:         true,
		InstructionFile: filePath,
		MaxLength:       2000,
	})
	return mgr, filePath
}

// --- 缺口 1: 文件变更触发重载 ---

func TestManager_ReloadOnFileChange(t *testing.T) {
	mgr, filePath := newFileManager(t, "v1")
	if got := mgr.GetInstructions(); got != "v1" {
		t.Fatalf("initial load = %q, want v1", got)
	}

	// 同一秒内重写，mtime 可能不变；等待至少 1 秒确保 mtime 前进。
	time.Sleep(1100 * time.Millisecond)
	if err := os.WriteFile(filePath, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := mgr.GetInstructions(); got != "v2" {
		t.Errorf("after file change = %q, want v2", got)
	}

	// mtime 未变时返回缓存，不重新读取（内容被外部改回也保持 v2）。
	// 这里直接验证缓存行为：SetInstructions 再 Get 应返回手动设置值。
	mgr.SetInstructions("manual")
	if got := mgr.GetInstructions(); got != "manual" {
		t.Errorf("cached = %q, want manual", got)
	}
}

func TestManager_ReloadTruncatesByMaxLength(t *testing.T) {
	mgr, filePath := newFileManager(t, "x")
	mgr.cfg.MaxLength = 4

	time.Sleep(1100 * time.Millisecond)
	if err := os.WriteFile(filePath, []byte("123456789"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := mgr.GetInstructions(); got != "1234" {
		t.Errorf("reload truncate = %q, want 1234", got)
	}
}

// --- 缺口 2: 文件缺失/删除时回退缓存 ---

func TestManager_MissingFileKeepsCache(t *testing.T) {
	mgr, filePath := newFileManager(t, "cached")
	if got := mgr.GetInstructions(); got != "cached" {
		t.Fatalf("initial load = %q", got)
	}

	if err := os.Remove(filePath); err != nil {
		t.Fatal(err)
	}

	if got := mgr.GetInstructions(); got != "cached" {
		t.Errorf("after delete = %q, want cached value", got)
	}
}

func TestManager_NoFileNoPanic(t *testing.T) {
	mgr := NewManager(&config.TopOfMindConfig{
		InstructionFile: filepath.Join(t.TempDir(), "nope.md"),
	})
	if got := mgr.GetInstructions(); got != "" {
		t.Errorf("missing file = %q, want empty", got)
	}
}

func TestManager_EmptyInstructionFile_NoReload(t *testing.T) {
	mgr := NewManager(&config.TopOfMindConfig{})
	if got := mgr.GetInstructions(); got != "" {
		t.Errorf("empty file config = %q, want empty", got)
	}
}

// --- 缺口 3: 并发 Get ---

func TestManager_ConcurrentGetReload(t *testing.T) {
	mgr, filePath := newFileManager(t, "origin")

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = mgr.GetInstructions()
			}
		}()
	}
	wg.Wait()
	// 并发读后内容一致（root 测试测得锁内无竞态）。
	if got := mgr.GetInstructions(); got != "origin" {
		t.Errorf("after concurrent reads = %q, want origin", got)
	}

	// 并发下触发重载也不得产生竞态/错误。
	time.Sleep(1100 * time.Millisecond)
	if err := os.WriteFile(filePath, []byte("updated"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = mgr.GetInstructions()
			}
		}()
	}
	wg.Wait()
	if got := mgr.GetInstructions(); got != "updated" {
		t.Errorf("after concurrent reload = %q, want updated", got)
	}
}
