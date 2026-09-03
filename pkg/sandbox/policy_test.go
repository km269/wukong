package sandbox

// Portable tests for the postStart hook mechanism and Policy.Limits
// zero-value contract. These run on every platform; platform-specific
// Job Object / setrlimit enforcement is covered by
// jobobject_windows_test.go and limits_linux_test.go.

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// echoCmd returns a portable sandbox.Cmd that prints a word.
func echoCmd(word string) *Cmd {
	if runtime.GOOS == "windows" {
		return CommandContext(
			context.Background(), "cmd", "/C", "echo "+word,
		)
	}
	return CommandContext(
		context.Background(), "echo", word,
	)
}

// TestCmd_PostStartHook_RunsAfterStart verifies the postStart hook
// fires after a successful Start, receiving the real exec.Cmd with a
// live process handle.
func TestCmd_PostStartHook_RunsAfterStart(t *testing.T) {
	cmd := echoCmd("hi")
	cmd.Policy.WritableDirs = nil

	hooked := false
	cmd.addPostStart(func(c *exec.Cmd) error {
		hooked = true
		if c == nil {
			t.Error("postStart hook received nil exec.Cmd")
			return errors.New("nil cmd")
		}
		if c.Process == nil {
			t.Error("postStart hook received nil process")
			return errors.New("nil process")
		}
		return nil
	})

	var out strings.Builder
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		// A nil hook return means Run should succeed; surface the
		// error if the sandbox path itself failed.
		t.Fatalf("Run: %v", err)
	}
	if !hooked {
		t.Error("postStart hook was not invoked")
	}
	if !strings.Contains(out.String(), "hi") {
		t.Errorf("stdout=%q want to contain 'hi'", out.String())
	}
}

// TestCmd_PostStartHook_ErrorKillsProcess verifies that a hook
// returning an error kills the started process and propagates the
// failure out of Run.
func TestCmd_PostStartHook_ErrorKillsProcess(t *testing.T) {
	cmd := echoCmd("x")
	cmd.Policy.WritableDirs = nil

	sentinel := errors.New("postStart: simulated failure")
	cmd.addPostStart(func(_ *exec.Cmd) error {
		return sentinel
	})

	err := cmd.Run()
	if err == nil {
		t.Fatal("expected Run to fail when postStart hook errors")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("err=%v want to wrap sentinel %v", err, sentinel)
	}
}

// TestPolicy_LimitsZeroValueMeansUnlimited documents the zero-value
// contract: a zero ResourceLimits requests no limits, so applyJobObject
// (Windows) and applyResourceLimits (Linux) are both no-ops.
func TestPolicy_LimitsZeroValueMeansUnlimited(t *testing.T) {
	var p Policy
	if p.Limits.MaxCPUSeconds != 0 || p.Limits.MaxMemoryBytes != 0 ||
		p.Limits.MaxFileBytes != 0 || p.Limits.MaxProcesses != 0 {
		t.Error("zero Policy.Limits should be all-zero (unlimited)")
	}
}
