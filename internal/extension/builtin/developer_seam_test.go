package builtin

// This file verifies the capability seam: that DeveloperToolSet's
// file/shell tool methods route through the injected FileService /
// ShellService interfaces rather than bypassing them. Mock backends
// record calls and return canned results so we can assert wiring
// without touching the real OS or sandbox.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/km269/wukong/pkg/capability"
)

// --- mocks -------------------------------------------------------------

type mockWrite struct {
	Path    string
	Content []byte
}

// memFileService is an in-memory FileService that records every call
// and stores file contents in a map. It satisfies capability.FileService.
type memFileService struct {
	writes  []mockWrite
	reads   []string
	repl    []mockReplace
	deletes []string

	readErr  error // returned by Read when set
	writeErr error // returned by Write when set
	files    map[string][]byte
}

type mockReplace struct {
	Path string
	Old  string
	New  string
}

func newMemFileService() *memFileService {
	return &memFileService{files: make(map[string][]byte)}
}

func (m *memFileService) Write(
	_ context.Context, path string, content []byte,
) error {
	if m.writeErr != nil {
		return m.writeErr
	}
	m.writes = append(m.writes, mockWrite{Path: path, Content: content})
	m.files[path] = content
	return nil
}

func (m *memFileService) Read(
	_ context.Context, path string,
) ([]byte, error) {
	if m.readErr != nil {
		return nil, m.readErr
	}
	m.reads = append(m.reads, path)
	data, ok := m.files[path]
	if !ok {
		return nil, fmt.Errorf("not found: %s", path)
	}
	return data, nil
}

func (m *memFileService) Replace(
	_ context.Context, path, old, new string,
) (int, error) {
	m.repl = append(m.repl, mockReplace{Path: path, Old: old, New: new})
	data, ok := m.files[path]
	if !ok {
		return 0, fmt.Errorf("not found: %s", path)
	}
	content := string(data)
	count := strings.Count(content, old)
	if count == 0 {
		return 0, nil
	}
	m.files[path] = []byte(strings.ReplaceAll(content, old, new))
	return count, nil
}

func (m *memFileService) Delete(_ context.Context, path string) error {
	m.deletes = append(m.deletes, path)
	delete(m.files, path)
	return nil
}

// stubShellService is a ShellService that records ExecRequests and
// returns a canned ExecResult. To faithfully match the
// SandboxShellService convention, infrastructural failures are
// delivered via ExecResult.Err (the second return value is always
// nil); executeCommand reads res.Err, not the Go-idiomatic error.
type stubShellService struct {
	execs   []capability.ExecRequest
	result  capability.ExecResult
	execErr error
}

func (s *stubShellService) Execute(
	_ context.Context, req capability.ExecRequest,
) (capability.ExecResult, error) {
	s.execs = append(s.execs, req)
	if s.execErr != nil {
		return capability.ExecResult{Err: s.execErr}, nil
	}
	return s.result, nil
}

// --- FileService seam tests -------------------------------------------

func TestDeveloperToolSet_WriteFile_RoutesToFileService(t *testing.T) {
	fs := newMemFileService()
	ts := NewDeveloperToolSet(WithFileService(fs))

	rsp, err := ts.writeFile(context.Background(), FileWriteReq{
		Path:    "/tmp/seam.txt",
		Content: "hello seam",
	})
	if err != nil {
		t.Fatalf("writeFile err: %v", err)
	}
	if !rsp.Success {
		t.Fatalf("expected success, got error: %q", rsp.Error)
	}
	if len(fs.writes) != 1 {
		t.Fatalf("expected 1 Write call, got %d", len(fs.writes))
	}
	if fs.writes[0].Path != "/tmp/seam.txt" {
		t.Errorf("path=%q want /tmp/seam.txt", fs.writes[0].Path)
	}
	if string(fs.writes[0].Content) != "hello seam" {
		t.Errorf("content=%q want hello seam", fs.writes[0].Content)
	}
	if got := fs.files["/tmp/seam.txt"]; string(got) != "hello seam" {
		t.Errorf("stored content=%q want hello seam", got)
	}
}

func TestDeveloperToolSet_WriteFile_PropagatesFileServiceError(t *testing.T) {
	fs := newMemFileService()
	fs.writeErr = errors.New("disk full")
	ts := NewDeveloperToolSet(WithFileService(fs))

	rsp, _ := ts.writeFile(context.Background(), FileWriteReq{
		Path: "/tmp/x", Content: "x",
	})
	if rsp.Success {
		t.Fatal("expected failure when FileService errors")
	}
	if !strings.Contains(rsp.Error, "disk full") {
		t.Errorf("error=%q want to contain 'disk full'", rsp.Error)
	}
}

func TestDeveloperToolSet_ReadFile_RoutesToFileService(t *testing.T) {
	fs := newMemFileService()
	fs.files["/tmp/seam.txt"] = []byte("payload")
	ts := NewDeveloperToolSet(WithFileService(fs))

	rsp, _ := ts.readFile(context.Background(), FileReadReq{
		Path: "/tmp/seam.txt",
	})
	if !rsp.Success {
		t.Fatalf("expected success, got error: %q", rsp.Error)
	}
	if rsp.Content != "payload" {
		t.Errorf("content=%q want payload", rsp.Content)
	}
	if len(fs.reads) != 1 || fs.reads[0] != "/tmp/seam.txt" {
		t.Errorf("reads=%v want [/tmp/seam.txt]", fs.reads)
	}
}

func TestDeveloperToolSet_ReadFile_MissingFileReportsError(t *testing.T) {
	fs := newMemFileService()
	ts := NewDeveloperToolSet(WithFileService(fs))

	rsp, _ := ts.readFile(context.Background(), FileReadReq{
		Path: "/tmp/missing",
	})
	if rsp.Success {
		t.Fatal("expected failure for missing file")
	}
	if !strings.Contains(rsp.Error, "not found") {
		t.Errorf("error=%q want to contain 'not found'", rsp.Error)
	}
}

func TestDeveloperToolSet_ReplaceInFile_RoutesToFileService(t *testing.T) {
	fs := newMemFileService()
	fs.files["/tmp/seam.txt"] = []byte("foo foo foo")
	ts := NewDeveloperToolSet(WithFileService(fs))

	rsp, _ := ts.replaceInFile(context.Background(), FileReplaceReq{
		Path:   "/tmp/seam.txt",
		OldStr: "foo",
		NewStr: "bar",
	})
	if !rsp.Success {
		t.Fatalf("expected success, got error: %q", rsp.Error)
	}
	if rsp.Occurrences != 3 {
		t.Errorf("occurrences=%d want 3", rsp.Occurrences)
	}
	if len(fs.repl) != 1 {
		t.Fatalf("expected 1 Replace call, got %d", len(fs.repl))
	}
	if got := string(fs.files["/tmp/seam.txt"]); got != "bar bar bar" {
		t.Errorf("post-replace content=%q want 'bar bar bar'", got)
	}
}

func TestDeveloperToolSet_ReplaceInFile_NoMatchReportsError(t *testing.T) {
	fs := newMemFileService()
	fs.files["/tmp/seam.txt"] = []byte("nothing here")
	ts := NewDeveloperToolSet(WithFileService(fs))

	rsp, _ := ts.replaceInFile(context.Background(), FileReplaceReq{
		Path:   "/tmp/seam.txt",
		OldStr: "zzz",
		NewStr: "yyy",
	})
	if rsp.Success {
		t.Fatal("expected failure when old_str not found")
	}
	if !strings.Contains(rsp.Error, "old_str not found") {
		t.Errorf("error=%q want to contain 'old_str not found'", rsp.Error)
	}
}

// --- ShellService seam tests ------------------------------------------

func TestDeveloperToolSet_ExecuteCommand_RoutesToShellService(t *testing.T) {
	shell := &stubShellService{
		result: capability.ExecResult{
			Stdout:   "build ok\n",
			ExitCode: 0,
		},
	}
	ts := NewDeveloperToolSet(WithShellService(shell))

	rsp, _ := ts.executeCommand(context.Background(), CommandExecuteReq{
		Command: "make build",
		WorkDir: "/repo",
		Timeout: 10,
	})
	if !rsp.Success {
		t.Fatalf("expected success, got error: %q", rsp.Error)
	}
	if rsp.Stdout != "build ok\n" {
		t.Errorf("stdout=%q want 'build ok\\n'", rsp.Stdout)
	}
	if rsp.ExitCode != 0 {
		t.Errorf("exit=%d want 0", rsp.ExitCode)
	}
	if len(shell.execs) != 1 {
		t.Fatalf("expected 1 Execute call, got %d", len(shell.execs))
	}
	got := shell.execs[0]
	if got.Command != "make build" {
		t.Errorf("command=%q want 'make build'", got.Command)
	}
	if got.WorkDir != "/repo" {
		t.Errorf("workdir=%q want /repo", got.WorkDir)
	}
	// Timeout=10s → 10*time.Second
	if got.Timeout != 10*time.Second {
		t.Errorf("timeout=%v want 10s", got.Timeout)
	}
}

func TestDeveloperToolSet_ExecuteCommand_DefaultTimeoutApplied(t *testing.T) {
	shell := &stubShellService{}
	ts := NewDeveloperToolSet(WithShellService(shell))

	_, _ = ts.executeCommand(context.Background(), CommandExecuteReq{
		Command: "true",
	})
	if len(shell.execs) != 1 {
		t.Fatalf("expected 1 Execute call, got %d", len(shell.execs))
	}
	if shell.execs[0].Timeout != 30*time.Second {
		t.Errorf("default timeout=%v want 30s", shell.execs[0].Timeout)
	}
}

func TestDeveloperToolSet_ExecuteCommand_NonZeroExitIsResultNotInfraErr(
	t *testing.T,
) {
	shell := &stubShellService{
		result: capability.ExecResult{
			Stderr:   "compile failed",
			ExitCode: 2,
		},
	}
	ts := NewDeveloperToolSet(WithShellService(shell))

	rsp, _ := ts.executeCommand(context.Background(), CommandExecuteReq{
		Command: "make",
	})
	// A non-zero exit is a result, not Success; but Error must be
	// empty because infrastructural Err was nil.
	if rsp.Success {
		t.Fatal("expected Success=false for non-zero exit")
	}
	if rsp.ExitCode != 2 {
		t.Errorf("exit=%d want 2", rsp.ExitCode)
	}
	if rsp.Error != "" {
		t.Errorf("error=%q want empty (exit code is a result, not error)",
			rsp.Error)
	}
	if rsp.Stderr != "compile failed" {
		t.Errorf("stderr=%q want 'compile failed'", rsp.Stderr)
	}
}

func TestDeveloperToolSet_ExecuteCommand_PropagatesShellInfraError(
	t *testing.T,
) {
	shell := &stubShellService{
		execErr: errors.New("spawn: not found"),
	}
	ts := NewDeveloperToolSet(WithShellService(shell))

	rsp, _ := ts.executeCommand(context.Background(), CommandExecuteReq{
		Command: "bogus",
	})
	if rsp.Success {
		t.Fatal("expected failure when shell errors")
	}
	if !strings.Contains(rsp.Error, "spawn: not found") {
		t.Errorf("error=%q want to contain 'spawn: not found'", rsp.Error)
	}
}

// --- default wiring test ----------------------------------------------

func TestDeveloperToolSet_DefaultsUseOSAndSandboxBackends(t *testing.T) {
	ts := NewDeveloperToolSet()
	// The zero-option constructor must wire real default backends.
	if ts.fs == nil {
		t.Fatal("default FileService must not be nil")
	}
	if ts.shell == nil {
		t.Fatal("default ShellService must not be nil")
	}
	// Type assertions confirm the exact default implementations.
	if _, ok := ts.fs.(*capability.OSFileService); !ok {
		t.Errorf("default fs type=%T want *capability.OSFileService", ts.fs)
	}
	// SandboxShellService is the documented default (pointer receiver).
	if _, ok := ts.shell.(*capability.SandboxShellService); !ok {
		t.Errorf("default shell type=%T want *capability.SandboxShellService",
			ts.shell)
	}
}
