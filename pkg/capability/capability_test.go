package capability

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOSFileService_WriteReadRoundTrip(t *testing.T) {
	s := NewOSFileService()
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")

	if err := s.Write(context.Background(), p, []byte("hello world")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := s.Read(context.Background(), p)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("Read got %q want hello world", string(data))
	}
}

func TestOSFileService_Replace(t *testing.T) {
	s := NewOSFileService()
	dir := t.TempDir()
	p := filepath.Join(dir, "r.txt")
	_ = s.Write(context.Background(), p, []byte("foo bar foo baz foo"))

	count, err := s.Replace(context.Background(), p, "foo", "qux")
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if count != 3 {
		t.Errorf("count=%d want 3", count)
	}
	data, _ := s.Read(context.Background(), p)
	if string(data) != "qux bar qux baz qux" {
		t.Errorf("after replace got %q", string(data))
	}
}

func TestOSFileService_Replace_NotFound(t *testing.T) {
	s := NewOSFileService()
	dir := t.TempDir()
	p := filepath.Join(dir, "nf.txt")
	_ = s.Write(context.Background(), p, []byte("nothing here"))

	count, err := s.Replace(context.Background(), p, "absent", "x")
	if err != nil {
		t.Fatalf("Replace err: %v", err)
	}
	if count != 0 {
		t.Errorf("count=%d want 0 when pattern absent", count)
	}
}

func TestOSFileService_Delete(t *testing.T) {
	s := NewOSFileService()
	dir := t.TempDir()
	p := filepath.Join(dir, "d.txt")
	_ = s.Write(context.Background(), p, []byte("x"))

	if err := s.Delete(context.Background(), p); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("file still exists after Delete: %v", err)
	}
}

func TestSandboxShellService_Execute_Success(t *testing.T) {
	s := NewSandboxShellService()
	res, _ := s.Execute(context.Background(), ExecRequest{
		Command: "echo hello",
		Timeout: 5 * time.Second,
	})
	if res.Err != nil {
		t.Fatalf("Err: %v", res.Err)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit=%d want 0", res.ExitCode)
	}
	if !strings.Contains(strings.TrimSpace(res.Stdout), "hello") {
		t.Errorf("stdout=%q want to contain hello", res.Stdout)
	}
}

func TestSandboxShellService_Execute_WorkDir(t *testing.T) {
	s := NewSandboxShellService()
	dir := t.TempDir()
	// Write a marker file, then ask the shell to list it. WorkDir
	// scopes the sandbox write policy so the command can run.
	_ = os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("x"), 0644)
	// Cross-platform "list one file": `ls` on Unix, `dir` on Windows
	// (cmd /C). Both exit 0 when the file exists.
	listCmd := "ls marker.txt"
	if runtime.GOOS == "windows" {
		listCmd = "dir marker.txt"
	}
	res, _ := s.Execute(context.Background(), ExecRequest{
		Command: listCmd,
		WorkDir: dir,
		Timeout: 5 * time.Second,
	})
	if res.Err != nil {
		t.Fatalf("Err: %v", res.Err)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit=%d want 0 (marker should be visible); stderr=%q",
			res.ExitCode, res.Stderr)
	}
}
