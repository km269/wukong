//go:build linux

package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// init detects sandbox helper mode at process start.
//
// Linux Landlock restrictions must be applied before exec'ing the target
// command, so we use a self-exec pattern: applySandbox() rewrites the
// exec.Cmd to run this same binary with __SANDBOX_HELPER=1. This init()
// function detects that flag, applies Landlock rules, and syscall.Exec's
// the real command. This function never returns in helper mode.
func init() {
	if os.Getenv("__SANDBOX_HELPER") != "1" {
		return
	}

	// Args: <self> __sandbox__ -- <realCmd> [args...]
	args := os.Args
	sepIdx := -1
	for i, a := range args {
		if a == "--" {
			sepIdx = i
			break
		}
	}
	if sepIdx < 0 || sepIdx+1 >= len(args) {
		fmt.Fprintf(os.Stderr, "sandbox: malformed helper args: %v\n", args)
		os.Exit(1)
	}

	realPath := args[sepIdx+1]
	realArgs := args[sepIdx+1:]

	var cfg helperConfig
	cfgJSON := os.Getenv("__SANDBOX_CONFIG")
	if cfgJSON == "" {
		fmt.Fprintln(os.Stderr, "sandbox: missing __SANDBOX_CONFIG")
		os.Exit(1)
	}
	if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox: invalid config: %v\n", err)
		os.Exit(1)
	}

	if err := setupLandlock(&cfg); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox: landlock setup failed: %v\n", err)
		os.Exit(1)
	}

	// Apply process-level resource limits (RLIMIT_CPU/AS/FSIZE/NPROC)
	// before exec so they bind the real command. setrlimit on self
	// does not require elevated privileges.
	if err := applyResourceLimits(&cfg); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox: resource limits failed: %v\n", err)
		os.Exit(1)
	}

	// Resolve the real command path.
	resolvedPath := realPath
	if !filepath.IsAbs(resolvedPath) {
		found, err := exec.LookPath(resolvedPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sandbox: command %q not found in PATH: %v\n",
				resolvedPath, err)
			os.Exit(1)
		}
		resolvedPath = found
	}

	// Strip sandbox env vars before exec.
	cleanEnv := os.Environ()
	filtered := cleanEnv[:0]
	for _, e := range cleanEnv {
		if strings.HasPrefix(e, "__SANDBOX_") {
			continue
		}
		filtered = append(filtered, e)
	}

	if err := syscall.Exec(resolvedPath, realArgs, filtered); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox: exec %s: %v\n", resolvedPath, err)
		os.Exit(1)
	}
}
