// Package capability defines fs/shell service seams for the agent's
// built-in developer tools. The interfaces decouple tool logic
// (Consumer) from concrete execution backends (Provider), enabling
// mock injection for tests and backend substitution (remote shells,
// audited filesystems, alternative sandboxes) without touching tool
// code.
//
// Security is NOT re-implemented here. Path allow/deny
// (.wukongignore) and approval gates remain in the agent loop's
// BeforeTool layer (security.Guard + PreToolExecuteHook); the
// capability seam only abstracts execution mechanics.
package capability

import (
	"context"
	"os"
	"strings"
	"time"
)

// FileService abstracts filesystem mutations performed by developer
// tools. Methods are mechanic-only; security policy is enforced
// upstream by the BeforeTool gate.
type FileService interface {
	Write(ctx context.Context, path string, content []byte) error
	Read(ctx context.Context, path string) ([]byte, error)
	Replace(ctx context.Context, path, old, new string) (occurrences int, err error)
	Delete(ctx context.Context, path string) error
}

// ShellService abstracts command execution. Implementations may
// apply sandboxing (pkg/sandbox), audit, or remote dispatch.
type ShellService interface {
	Execute(ctx context.Context, req ExecRequest) (ExecResult, error)
}

// ExecRequest carries command execution parameters.
type ExecRequest struct {
	Command      string
	WorkDir      string
	Timeout      time.Duration // zero → implementation default
	WritableDirs []string      // sandbox write policy (pkg/sandbox)
}

// ExecResult is the outcome of a command execution. Err is non-nil
// only for infrastructural failures (spawn, sandbox setup); a
// non-zero ExitCode is a result, not an error.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Err      error
}

// OSFileService is the default FileService backed by the os package.
type OSFileService struct{}

// NewOSFileService returns the default os-backed FileService.
func NewOSFileService() *OSFileService { return &OSFileService{} }

func (s *OSFileService) Write(
	ctx context.Context, path string, content []byte,
) error {
	return os.WriteFile(path, content, 0644)
}

func (s *OSFileService) Read(
	ctx context.Context, path string,
) ([]byte, error) {
	return os.ReadFile(path)
}

func (s *OSFileService) Replace(
	ctx context.Context, path, old, new string,
) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	content := string(data)
	count := strings.Count(content, old)
	if count == 0 {
		return 0, nil
	}
	return count, os.WriteFile(
		path, []byte(strings.ReplaceAll(content, old, new)), 0644,
	)
}

func (s *OSFileService) Delete(ctx context.Context, path string) error {
	return os.Remove(path)
}
