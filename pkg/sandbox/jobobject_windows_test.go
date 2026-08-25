//go:build windows

package sandbox

// Windows-specific tests for the Job Object path (process lifecycle
// kill-on-parent-exit + resource limits). Job Object APIs do NOT
// require admin privileges, unlike low-integrity labeling, so these
// tests run on non-admin Windows.

import (
	"context"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// TestApplyJobObject_NoOpWhenNoLimits verifies that with zero limits
// and KillOnParentExit=false, applyJobObject registers no hooks.
func TestApplyJobObject_NoOpWhenNoLimits(t *testing.T) {
	ctx := &sandboxCtx{
		limits:           ResourceLimits{},
		killOnParentExit: false,
	}
	if err := applyJobObject(ctx); err != nil {
		t.Fatalf("applyJobObject zero policy: %v", err)
	}
	if len(ctx.postStart) != 0 {
		t.Errorf("expected 0 postStart hooks, got %d", len(ctx.postStart))
	}
	if len(ctx.cleanup) != 0 {
		t.Errorf("expected 0 cleanup hooks, got %d", len(ctx.cleanup))
	}
}

// TestApplyJobObject_RegistersHooksForKillOnParentExit verifies that
// KillOnParentExit alone triggers job creation with one cleanup and
// one postStart hook, and that the cleanup closes the job handle.
func TestApplyJobObject_RegistersHooksForKillOnParentExit(t *testing.T) {
	ctx := &sandboxCtx{
		limits:           ResourceLimits{},
		killOnParentExit: true,
	}
	if err := applyJobObject(ctx); err != nil {
		t.Fatalf("applyJobObject: %v", err)
	}
	if len(ctx.postStart) != 1 {
		t.Fatalf("expected 1 postStart hook, got %d", len(ctx.postStart))
	}
	if len(ctx.cleanup) != 1 {
		t.Fatalf("expected 1 cleanup hook, got %d", len(ctx.cleanup))
	}
	// Closing the job handle must not error. The cleanup closes the
	// last handle, killing any assigned processes (none here).
	ctx.cleanup[0]()
}

// TestApplyJobObject_RegistersHooksForMemoryLimit verifies that a
// memory limit triggers job creation.
func TestApplyJobObject_RegistersHooksForMemoryLimit(t *testing.T) {
	ctx := &sandboxCtx{
		limits: ResourceLimits{
			MaxMemoryBytes: 1 << 30, // 1 GiB
		},
	}
	if err := applyJobObject(ctx); err != nil {
		t.Fatalf("applyJobObject: %v", err)
	}
	if len(ctx.postStart) != 1 || len(ctx.cleanup) != 1 {
		t.Fatalf("expected 1 postStart + 1 cleanup, got %d + %d",
			len(ctx.postStart), len(ctx.cleanup))
	}
	ctx.cleanup[0]()
}

// TestCmd_RunUnderJobObject verifies that assigning the child process
// to a Job Object does not break command execution. Runs `echo hi`
// under KillOnParentExit + a generous memory limit and asserts the
// output is captured.
func TestCmd_RunUnderJobObject(t *testing.T) {
	cmd := CommandContext(
		context.Background(), "cmd", "/C", "echo hi",
	)
	cmd.Policy = Policy{
		WritableDirs:     nil,
		KillOnParentExit: true,
		Limits: ResourceLimits{
			MaxMemoryBytes: 1 << 30, // 1 GiB — generous, won't trip
		},
	}

	var out strings.Builder
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("Run under job object: %v", err)
	}
	if !strings.Contains(out.String(), "hi") {
		t.Errorf("stdout=%q want to contain 'hi'", out.String())
	}
}

// TestJobObjectConstantsExist is a compile-time guard that the
// Windows constants we depend on are present in x/sys/windows.
func TestJobObjectConstantsExist(t *testing.T) {
	_ = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_ = windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY
	_ = windows.JOB_OBJECT_LIMIT_PROCESS_TIME
	_ = windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS
	_ = windows.JobObjectExtendedLimitInformation
	_ = windows.PROCESS_SET_QUOTA
	_ = windows.PROCESS_TERMINATE
}
