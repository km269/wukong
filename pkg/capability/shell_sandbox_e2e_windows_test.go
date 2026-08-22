//go:build windows

package capability

// Windows-only end-to-end sanity for the Job Object wiring path.
//
// We deliberately do NOT try to trip JOB_OBJECT_LIMIT_PROCESS_
// MEMORY / PROCESS_TIME from a shell command — reliably tripping
// them requires the child to allocate a predictable amount of
// memory, and cmd.exe → subshell memory footprints are too
// variable across Windows builds for a stable assertion.
//
// What we CAN verify on Windows is the negative case: when limits
// are generous enough to never trip, the Job Object assignment
// (ApplyProcessToJobObject in postStart) does not break command
// execution. This exercises the full chain
//
//	NewSandboxShellServiceWithLimits → Execute →
//	  sandbox.Cmd.Policy → applyJobObject → AssignProcessToJobObject
//
// end-to-end, complementing the unit-level coverage in
// sandbox/jobobject_windows_test.go which calls applyJobObject
// directly on a synthetic sandboxCtx.
//
// Kernel-level enforcement itself is guaranteed by Windows when
// the limit IS hit; the wiring above is what we need to test, and
// this test does that.

import (
	"context"
	"testing"
	"time"

	"github.com/km269/wukong/pkg/sandbox"
)

// TestE2E_Limits_Windows_JobObjectChainDoesNotBreakCmd verifies
// that with KillOnParentExit + a generous CPU and memory cap, a
// trivial cmd /C echo completes normally. This is the Windows
// equivalent of the cross-platform generous-memory-limit test,
// but combines multiple Job Object limit flags to exercise more
// of the SetInformationJobObject call site end-to-end.
func TestE2E_Limits_Windows_JobObjectChainDoesNotBreakCmd(t *testing.T) {
	s := NewSandboxShellServiceWithLimits(
		sandbox.ResourceLimits{
			MaxCPUSeconds:   30, // 30s — never trips for echo
			MaxMemoryBytes:  1 << 30, // 1 GiB — never trips for cmd
			MaxProcesses:    64, // generous — never trips
		},
		true, // KillOnParentExit — also exercises assign path
	)
	res, _ := s.Execute(context.Background(), ExecRequest{
		Command: "echo win-job-ok",
		Timeout: 10 * time.Second,
	})
	if res.Err != nil {
		t.Fatalf("Err: %v", res.Err)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit=%d want 0; stderr=%q",
			res.ExitCode, res.Stderr)
	}
	// cmd /C echo output includes trailing CRLF; use Contains.
	if !stringContainsFold(res.Stdout, "win-job-ok") {
		t.Errorf("stdout=%q want to contain win-job-ok", res.Stdout)
	}
}

// stringContainsFold is a tiny case-insensitive Contains helper
// kept local to avoid importing strings only for one assertion.
func stringContainsFold(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	if len(s) < len(sub) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a, b := s[i+j], sub[j]
			if a == b {
				continue
			}
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
