//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/km269/wukong/internal/util"
)

// macOS backend uses the built-in sandbox-exec(1) command.
// Ships with every macOS install — no extra dependencies.

var _sandboxExecPath string

func init() {
	_sandboxExecPath, _ = exec.LookPath("sandbox-exec")
}

const macOSDefaultPolicy = `(version 1)

(allow default)

; Deny all file writes by default.
(deny file-write*)

; Allow writes to explicit directories.
%s

; Allow reading all files.
(allow file-read*)

; Allow process execution.
(allow process*)

; Allow sysctl read (uname, etc.).
(allow sysctl-read)
`

func available() bool {
	return _sandboxExecPath != ""
}

func reasonUnavailable() string {
	if runtime.GOOS != "darwin" {
		return "not macOS"
	}
	if _sandboxExecPath == "" {
		return "sandbox-exec not found (should be at /usr/bin/sandbox-exec)"
	}
	return ""
}

func probeDarwin() ProbeResult {
	if _sandboxExecPath == "" {
		return ProbeResult{
			Platform: "darwin",
			Backend:  "none",
			Warning:  "sandbox-exec not found",
		}
	}
	return ProbeResult{
		Sandboxed: true,
		Platform:  "darwin",
		Backend:   "sandbox-exec",
	}
}

func applySandbox(cmd *exec.Cmd, ctx *sandboxCtx) error {
	allowWrites := new(strings.Builder)
	for _, p := range ctx.writable {
		abs, err := filepath.Abs(p)
		if err != nil {
			return fmt.Errorf("sandbox: resolve writable path %q: %w", p, err)
		}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		} else {
			// Log but continue — the unresolved path may still work.
			util.Logger.Debug("sandbox: symlink resolution failed, using raw path",
				"path", abs, "error", err.Error())
		}
		fmt.Fprintf(allowWrites, "(allow file-write* (subpath %q))\n", abs)
	}

	profile := fmt.Sprintf(macOSDefaultPolicy, allowWrites.String())

	f, err := os.CreateTemp("", "sandbox-*.sb")
	if err != nil {
		return fmt.Errorf("sandbox: create profile: %w", err)
	}
	profilePath := f.Name()
	if _, err := f.WriteString(profile); err != nil {
		f.Close()
		os.Remove(profilePath)
		return fmt.Errorf("sandbox: write profile: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(profilePath)
		return fmt.Errorf("sandbox: close profile: %w", err)
	}

	cleanupOK := false
	defer func() {
		if !cleanupOK {
			os.Remove(profilePath)
		}
	}()

	origPath := cmd.Path
	origArgs := cmd.Args

	cmd.Path = _sandboxExecPath
	cmd.Args = append([]string{
		"sandbox-exec",
		"-f", profilePath,
		"--",
		origPath,
	}, origArgs[1:]...)

	cleanupOK = true
	ctx.addCleanup(func() { os.Remove(profilePath) })
	return nil
}
