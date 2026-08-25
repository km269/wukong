//go:build linux

package sandbox

// Linux-specific tests for applyResourceLimits (setrlimit). These do
// not require root — setrlimit for self-limits is unprivileged. We
// only assert rlimits we can set safely without risking the test
// process itself (RLIMIT_CPU with a large budget; RLIMIT_FSIZE /
// RLIMIT_NPROC are left to integration coverage on Linux CI to
// avoid destabilizing the test binary).

import (
	"syscall"
	"testing"
)

// TestApplyResourceLimits_CPU_SetsRlimit verifies that MaxCPUSeconds
// flows through to RLIMIT_CPU and is observable via Getrlimit.
func TestApplyResourceLimits_CPU_SetsRlimit(t *testing.T) {
	var orig syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_CPU, &orig); err != nil {
		t.Fatalf("getrlimit CPU (before): %v", err)
	}
	defer syscall.Setrlimit(syscall.RLIMIT_CPU, &orig)

	cfg := &helperConfig{Limits: ResourceLimits{MaxCPUSeconds: 3600}}
	if err := applyResourceLimits(cfg); err != nil {
		t.Fatalf("applyResourceLimits: %v", err)
	}

	var cur syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_CPU, &cur); err != nil {
		t.Fatalf("getrlimit CPU (after): %v", err)
	}
	if cur.Cur != 3600 || cur.Max != 3600 {
		t.Errorf("CPU rlimit cur=%d max=%d want 3600/3600", cur.Cur, cur.Max)
	}
}

// TestApplyResourceLimits_ZeroValuesIsNoOp verifies that a zero
// ResourceLimits does not error and leaves RLIMIT_CPU untouched.
func TestApplyResourceLimits_ZeroValuesIsNoOp(t *testing.T) {
	var before syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_CPU, &before); err != nil {
		t.Fatalf("getrlimit CPU (before): %v", err)
	}

	cfg := &helperConfig{Limits: ResourceLimits{}}
	if err := applyResourceLimits(cfg); err != nil {
		t.Fatalf("applyResourceLimits zero: %v", err)
	}

	var after syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_CPU, &after); err != nil {
		t.Fatalf("getrlimit CPU (after): %v", err)
	}
	if after.Cur != before.Cur || after.Max != before.Max {
		t.Errorf("CPU rlimit changed: before=%v after=%v", before, after)
	}
}

// TestRlimitNPROCValue documents the locally-defined RLIMIT_NPROC
// constant (Go's syscall package omits it).
func TestRlimitNPROCValue(t *testing.T) {
	if rlimitNPROC != 6 {
		t.Errorf("rlimitNPROC=%d want 6 (asm-generic RLIMIT_NPROC)", rlimitNPROC)
	}
}

// TestApplyResourceLimits_FSIZE_SetsRlimit verifies MaxFileBytes
// flows to RLIMIT_FSIZE. Uses a generous 1 MiB budget and restores
// the original limit immediately to avoid destabilizing the test
// binary's own writes.
func TestApplyResourceLimits_FSIZE_SetsRlimit(t *testing.T) {
	var orig syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &orig); err != nil {
		t.Fatalf("getrlimit FSIZE (before): %v", err)
	}
	defer syscall.Setrlimit(syscall.RLIMIT_FSIZE, &orig)

	const budget = 1 << 20 // 1 MiB — large enough to not trip test writes
	cfg := &helperConfig{Limits: ResourceLimits{MaxFileBytes: budget}}
	if err := applyResourceLimits(cfg); err != nil {
		t.Fatalf("applyResourceLimits: %v", err)
	}

	var cur syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &cur); err != nil {
		t.Fatalf("getrlimit FSIZE (after): %v", err)
	}
	if cur.Cur != budget || cur.Max != budget {
		t.Errorf("FSIZE rlimit cur=%d max=%d want %d/%d", cur.Cur, cur.Max, budget, budget)
	}
}
