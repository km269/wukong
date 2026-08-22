// Package capability — shell_sandbox.go
//
// SandboxShellService is the default ShellService, backed by
// pkg/sandbox (Linux Landlock / macOS sandbox-exec / Windows Low
// Integrity Level). Falls back to unsandboxed exec when the platform
// is unsupported, mirroring the previous direct sandbox.CommandContext
// usage in builtin.DeveloperToolSet.
package capability

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/km269/wukong/pkg/sandbox"
)

// SandboxShellService executes shell commands under pkg/sandbox
// filesystem write protection. Optional ResourceLimits and
// KillOnParentExit are applied to every executed command's Policy.
type SandboxShellService struct {
	limits           sandbox.ResourceLimits
	killOnParentExit bool
}

// NewSandboxShellService returns the default sandbox-backed
// ShellService with no resource limits and no lifecycle binding.
func NewSandboxShellService() *SandboxShellService {
	return &SandboxShellService{}
}

// NewSandboxShellServiceWithLimits returns a SandboxShellService
// that applies per-process resource caps and optional parent-exit
// binding to every executed command. The limits map onto Windows
// Job Object constraints and Linux setrlimit constraints (see
// sandbox.ResourceLimits for platform coverage). Zero values mean
// "unlimited", so passing a zero-value limits and false kill flag
// is equivalent to NewSandboxShellService.
func NewSandboxShellServiceWithLimits(
	limits sandbox.ResourceLimits, killOnParentExit bool,
) *SandboxShellService {
	return &SandboxShellService{
		limits:           limits,
		killOnParentExit: killOnParentExit,
	}
}

// Execute runs the command in a sandboxed shell. A non-zero exit
// code is returned as a normal ExecResult (Err nil); only spawn /
// sandbox-setup failures set Err.
func (s *SandboxShellService) Execute(
	ctx context.Context, req ExecRequest,
) (ExecResult, error) {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var shell, shellFlag string
	if runtime.GOOS == "windows" {
		shell, shellFlag = "cmd", "/C"
	} else {
		shell, shellFlag = "sh", "-c"
	}

	sc := sandbox.CommandContext(execCtx, shell, shellFlag, req.Command)
	if req.WorkDir != "" {
		sc.Dir = req.WorkDir
		sc.Policy.WritableDirs = []string{req.WorkDir, ".wukong"}
	}
	// Apply process-level resource caps and lifecycle binding.
	// These are no-ops when zero/false (default constructor path).
	sc.Policy.Limits = s.limits
	sc.Policy.KillOnParentExit = s.killOnParentExit

	var stdout, stderr strings.Builder
	sc.Stdout = &stdout
	sc.Stderr = &stderr

	err := sc.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			// Non-zero exit is a command result, not infra failure.
			err = nil
		} else {
			return ExecResult{Err: err}, nil
		}
	}
	return ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, nil
}
