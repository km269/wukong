//go:build windows

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows backend: Low Integrity Level + Restricted Token.
// Low IL processes can read but not write Medium/High IL objects.
// All APIs are built into Windows since Vista — no extra installs.

func available() bool { return true }

func reasonUnavailable() string {
	if runtime.GOOS != "windows" {
		return "not Windows"
	}
	return ""
}

func probeWindows() ProbeResult {
	return ProbeResult{
		Sandboxed: true,
		Platform:  "windows",
		Backend:   "integrity-level",
	}
}

var (
	lowIL    *windows.SID
	lowILErr error
	lowILOnce sync.Once
)

// getLowIL returns the Low Integrity SID, creating it lazily.
// Uses sync.Once to avoid init-time panic and ensure thread safety.
func getLowIL() (*windows.SID, error) {
	lowILOnce.Do(func() {
		lowIL, lowILErr = windows.StringToSid("S-1-16-4096")
	})
	return lowIL, lowILErr
}

func applySandbox(cmd *exec.Cmd, ctx *sandboxCtx) error {
	// 1. Best-effort low-integrity labeling. This calls
	//    SetNamedSecurityInfo which requires admin privileges; on a
	//    non-admin shell it fails. Labeling is decoupled from the
	//    Job Object path (per design): a failure here is skipped
	//    (graceful degradation) so that the restricted Low-IL token
	//    below and the Job Object resource limits / lifecycle still
	//    apply. Both of those do NOT require admin.
	_ = setLowLabelOnDirs(ctx.writable)

	// 2. Restricted token with Low integrity level. Duplicating and
	//    relabeling a token we own does not require admin.
	var token windows.Token
	if err := windows.OpenProcessToken(
		windows.CurrentProcess(),
		windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|
			windows.TOKEN_ADJUST_DEFAULT|windows.TOKEN_ASSIGN_PRIMARY,
		&token,
	); err != nil {
		return fmt.Errorf("sandbox: open token: %w", err)
	}
	defer token.Close()

	var dupToken windows.Token
	if err := windows.DuplicateTokenEx(
		token,
		windows.TOKEN_ALL_ACCESS,
		nil,
		windows.SecurityAnonymous,
		windows.TokenPrimary,
		&dupToken,
	); err != nil {
		return fmt.Errorf("sandbox: duplicate token: %w", err)
	}

	if err := setTokenLowIL(dupToken); err != nil {
		dupToken.Close()
		return err
	}

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Token = syscall.Token(dupToken)

	// 3. Job Object for process lifecycle (kill-on-parent-exit) and
	//    resource limits (CPU / memory / process count). Job Object
	//    APIs do not require admin, so this enforces even when
	//    labeling was skipped above.
	if err := applyJobObject(ctx); err != nil {
		return err
	}
	return nil
}

// applyJobObject creates a Job Object with the requested resource
// limits and/or kill-on-parent-exit, registers a cleanup to close
// the handle (closing the last handle kills all assigned processes
// when JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE is set), and registers a
// post-start hook to assign the child process to the job once it has
// started.
//
// No-op when neither limits nor KillOnParentExit are requested.
func applyJobObject(ctx *sandboxCtx) error {
	limits := ctx.limits
	needJob := ctx.killOnParentExit ||
		limits.MaxCPUSeconds > 0 ||
		limits.MaxMemoryBytes > 0 ||
		limits.MaxProcesses > 0
	if !needJob {
		return nil
	}

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("sandbox: create job object: %w", err)
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	if ctx.killOnParentExit {
		info.BasicLimitInformation.LimitFlags |=
			windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	}
	if limits.MaxMemoryBytes > 0 {
		info.ProcessMemoryLimit = uintptr(limits.MaxMemoryBytes)
		info.BasicLimitInformation.LimitFlags |=
			windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY
	}
	if limits.MaxCPUSeconds > 0 {
		// PerProcessUserTimeLimit is in 100ns ticks.
		info.BasicLimitInformation.PerProcessUserTimeLimit =
			int64(limits.MaxCPUSeconds) * 10_000_000
		info.BasicLimitInformation.LimitFlags |=
			windows.JOB_OBJECT_LIMIT_PROCESS_TIME
	}
	if limits.MaxProcesses > 0 {
		info.BasicLimitInformation.ActiveProcessLimit =
			uint32(limits.MaxProcesses)
		info.BasicLimitInformation.LimitFlags |=
			windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS
	}

	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(job)
		return fmt.Errorf("sandbox: set job limits: %w", err)
	}

	// Closing the last open handle to the job kills all assigned
	// processes when KILL_ON_JOB_CLOSE is set; otherwise it just
	// detaches the job. Registered as cleanup so Wait()/error paths
	// release the handle.
	ctx.addCleanup(func() { windows.CloseHandle(job) })

	// Assign the child to the job after it starts. We need a process
	// handle with PROCESS_SET_QUOTA (and PROCESS_TERMINATE so the job
	// can enforce kill-on-close).
	ctx.addPostStart(func(cmd *exec.Cmd) error {
		if cmd.Process == nil {
			return fmt.Errorf("sandbox: process not started")
		}
		h, err := windows.OpenProcess(
			windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
			false, uint32(cmd.Process.Pid),
		)
		if err != nil {
			return fmt.Errorf("sandbox: open child process %d: %w",
				cmd.Process.Pid, err)
		}
		defer windows.CloseHandle(h)
		if err := windows.AssignProcessToJobObject(job, h); err != nil {
			return fmt.Errorf("sandbox: assign to job: %w", err)
		}
		return nil
	})
	return nil
}

func setTokenLowIL(token windows.Token) error {
	sid, err := getLowIL()
	if err != nil {
		return fmt.Errorf("sandbox: create Low IL SID: %w", err)
	}

	type sidAndAttrs struct {
		Sid        *windows.SID
		Attributes uint32
	}
	type mandatoryLabel struct {
		Label sidAndAttrs
	}
	info := mandatoryLabel{
		Label: sidAndAttrs{
			Sid:        sid,
			Attributes: 0x20, // SE_GROUP_INTEGRITY
		},
	}
	return windows.SetTokenInformation(
		token,
		windows.TokenIntegrityLevel,
		(*byte)(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
}

func setLowLabelOnDirs(dirs []string) error {
	for _, dir := range dirs {
		abs, err := windows.FullPath(dir)
		if err != nil {
			return fmt.Errorf("resolve %q: %w", dir, err)
		}
		if _, err := os.Stat(abs); err != nil {
			return fmt.Errorf("stat %q: %w", abs, err)
		}
		if err := setLowLabel(abs); err != nil {
			return fmt.Errorf("label %q: %w", abs, err)
		}
	}
	return nil
}

func setLowLabel(path string) error {
	sid, err := getLowIL()
	if err != nil {
		return fmt.Errorf("sandbox: get Low IL SID: %w", err)
	}
	ea := windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_READ | windows.GENERIC_WRITE | windows.GENERIC_EXECUTE,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       windows.CONTAINER_INHERIT_ACE | windows.OBJECT_INHERIT_ACE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{ea}, nil)
	if err != nil {
		return fmt.Errorf("build acl: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.LABEL_SECURITY_INFORMATION,
		nil, nil, nil, acl,
	); err != nil {
		return fmt.Errorf("SetNamedSecurityInfo: %w", err)
	}
	return nil
}
