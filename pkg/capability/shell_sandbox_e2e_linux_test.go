//go:build linux

package capability

// Linux-only end-to-end verification that sandbox limits flow
// through the full tool call chain (NewSandboxShellServiceWithLimits
// → Execute → sandbox.Cmd.Policy → setrlimit in the self-exec
// helper) and actually constrain the spawned child process.
//
// Why RLIMIT_FSIZE and not RLIMIT_AS / RLIMIT_NPROC:
//   - RLIMIT_FSIZE is per-process and deterministic — a write
//     beyond the cap raises SIGXFSZ, killing the writer. No
//     cross-process coupling, no per-UID accounting. This is the
//     most stable limit to trip from a shell command.
//   - RLIMIT_AS (memory) requires allocating a predictable amount
//     of memory inside the child, which is fragile across shells
//     and coreutils versions.
//   - RLIMIT_NPROC is per-UID, so the result depends on what else
//     the test user is running — flaky on shared CI.
//
// The test writes via dd(1) which is present on every Linux distro
// that has coreutils. dd's behavior under RLIMIT_FSIZE is
// well-defined: the write() syscall is interrupted by SIGXFSZ
// after the cap is reached, dd reports a short write, and exits
// non-zero. The partial file is left on disk and is observably
// smaller than what was requested.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/km269/wukong/pkg/sandbox"
)

// TestE2E_Limits_Linux_FileSizeCapKillsDd verifies that
// MaxFileBytes flows through the SandboxShellService seam and
// reaches setrlimit(RLIMIT_FSIZE) in the child process. We:
//
//   - set MaxFileBytes = 1024 (1 KiB),
//   - ask dd to write 8 KiB of zeros into a temp file,
//   - assert the run fails (non-zero exit OR Err set),
//   - assert the file was truncated to <= 1 KiB.
//
// The control case (no limit) writes the full 8 KiB and exits 0.
func TestE2E_Limits_Linux_FileSizeCapKillsDd(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "out.bin")

	s := NewSandboxShellServiceWithLimits(
		sandbox.ResourceLimits{MaxFileBytes: 1024}, // 1 KiB cap
		false,
	)
	res, _ := s.Execute(context.Background(), ExecRequest{
		Command: "dd if=/dev/zero of=" + outFile + " bs=4096 count=2 status=none",
		WorkDir: dir,
		Timeout: 5 * time.Second,
	})

	// Expect the command to NOT succeed: either non-zero exit or
	// an infra error (sandbox spawn failure would surface as Err).
	sawFailure := res.ExitCode != 0 || res.Err != nil
	if !sawFailure {
		t.Fatalf("expected dd to fail under RLIMIT_FSIZE=1024, "+
			"but exit=%d err=%v stdout=%q stderr=%q",
			res.ExitCode, res.Err, res.Stdout, res.Stderr)
	}

	// The file may or may not exist depending on whether SIGXFSZ
	// arrived before any write completed. If it exists, it must
	// be at most MaxFileBytes (with a small slack for the kernel's
	// per-write granularity — allow up to one extra block).
	info, err := os.Stat(outFile)
	if err == nil {
		const slack = 4096 // one bs-sized block of overshoot tolerance
		if info.Size() > 1024+slack {
			t.Errorf(
				"file size=%d exceeded RLIMIT_FSIZE=1024 (+slack %d)",
				info.Size(), slack,
			)
		}
	}
	// File missing is also acceptable — kernel may deliver SIGXFSZ
	// before any data hits disk.
}

// TestE2E_Limits_Linux_NoLimitAllowsLargeFile is the control case
// for the FSIZE cap test above. With zero limits, dd writes the
// full 8 KiB and exits 0. This guards against false positives
// where the cap test would pass because dd is broken, not because
// the limit fired.
func TestE2E_Limits_Linux_NoLimitAllowsLargeFile(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "out.bin")

	s := NewSandboxShellServiceWithLimits(
		sandbox.ResourceLimits{}, false,
	)
	res, _ := s.Execute(context.Background(), ExecRequest{
		Command: "dd if=/dev/zero of=" + outFile + " bs=4096 count=2 status=none",
		WorkDir: dir,
		Timeout: 5 * time.Second,
	})
	if res.Err != nil {
		t.Fatalf("Err: %v", res.Err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d want 0; stderr=%q",
			res.ExitCode, res.Stderr)
	}
	info, err := os.Stat(outFile)
	if err != nil {
		t.Fatalf("stat %s: %v", outFile, err)
	}
	if want := int64(8192); info.Size() != want {
		t.Errorf("file size=%d want %d", info.Size(), want)
	}
}
