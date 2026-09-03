// Package sandbox provides os/exec-compatible command execution with
// filesystem sandboxing — no Docker, no daemon, no extra installation.
//
// Forked and enhanced from github.com/tirdyhouse/sandbox (MIT).
// Added Wukong-specific logging integration, fallback-aware defaults,
// and platform capability detection with graceful degradation.
//
// On supported platforms, sandboxed commands can only write to explicitly
// allowed directories. All other paths are read-only. Unsupported platforms
// run unsandboxed with a logged warning.
//
// Platforms:
//
//	Linux   — Landlock (kernel 5.13+), kernel built-in
//	macOS   — sandbox-exec(1), system built-in
//	Windows — Low Integrity Level + Mandatory Labels
//	Other   — runs unsandboxed with a warning
package sandbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
)

// Cmd represents an external command being prepared or run.
// It mirrors os/exec.Cmd but adds per-command sandbox policies.
type Cmd struct {
	Path string
	Args []string
	Env  []string
	Dir  string

	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	Policy       Policy
	ProcessState *os.ProcessState

	// ctx is propagated to the underlying exec.Cmd via CommandContext.
	// When set, cancellation/timeout of ctx will kill the child process.
	ctx context.Context

	cmd       *exec.Cmd
	cleanup   []func()
	postStart []func(*exec.Cmd) error
}

// Policy defines filesystem and process-level restrictions for a
// sandboxed command.
type Policy struct {
	// WritableDirs lists paths the command is allowed to modify.
	//   nil     → only Dir (or cwd) is writable
	//   empty   → nothing is writable
	//   [paths] → only the listed paths are writable
	WritableDirs []string

	// Limits restricts process-level resource usage (CPU, memory,
	// file size, process count). Zero values mean unlimited. On
	// Windows these are enforced via Job Object; on Linux via
	// setrlimit in the self-exec helper. MaxFileBytes is Linux-only
	// (RLIMIT_FSIZE) — Windows has no equivalent and ignores it.
	Limits ResourceLimits

	// KillOnParentExit binds the command to a Job Object (Windows)
	// or a process group (Linux) so that when the parent (wukong)
	// terminates, the command and its children are killed.
	// Windows enforcement is kernel-level (JOB_OBJECT_LIMIT_KILL_
	// ON_JOB_CLOSE); Linux uses process-group tracking with ctx
	// cancellation as the best-effort lifecycle hook (true kernel-
	// level kill-on-parent-exit needs cgroups, out of scope here).
	KillOnParentExit bool
}

// ResourceLimits are per-process resource caps. Zero = unlimited.
type ResourceLimits struct {
	MaxCPUSeconds  uint64 // Windows: JOB_OBJECT_LIMIT_PROCESS_TIME; Linux: RLIMIT_CPU
	MaxMemoryBytes uint64 // Windows: JOB_OBJECT_LIMIT_PROCESS_MEMORY; Linux: RLIMIT_AS
	MaxFileBytes   uint64 // Linux: RLIMIT_FSIZE (Windows: ignored — no equivalent)
	MaxProcesses   uint64 // Windows: JOB_OBJECT_LIMIT_ACTIVE_PROCESS; Linux: RLIMIT_NPROC
}

// Command returns a Cmd to execute the named program with the given
// arguments. The returned Cmd uses default policies (working dir writable).
// Use CommandContext when the command should be bound to a context
// for cancellation/timeout propagation.
func Command(name string, arg ...string) *Cmd {
	return &Cmd{
		Path: name,
		Args: append([]string{name}, arg...),
	}
}

// CommandContext is like Command but includes a context. If the context
// is cancelled or times out, the underlying process will be killed.
// This mirrors os/exec.CommandContext semantics.
func CommandContext(ctx context.Context, name string, arg ...string) *Cmd {
	return &Cmd{
		Path: name,
		Args: append([]string{name}, arg...),
		ctx:  ctx,
	}
}

// Run starts the command and waits for it to complete.
func (c *Cmd) Run() error {
	if err := c.Start(); err != nil {
		return err
	}
	return c.Wait()
}

// Start starts the command but does not wait for it to complete.
// If build succeeds but cmd.Start fails, registered cleanup
// functions are still executed to release sandbox resources.
// Post-start hooks (e.g., assigning the child process to a Job
// Object) run after a successful Start; a hook failure kills the
// started process, waits for it, and runs cleanup.
func (c *Cmd) Start() error {
	cmd, err := c.build()
	if err != nil {
		c.runCleanup()
		return err
	}
	c.cmd = cmd
	if err := cmd.Start(); err != nil {
		c.runCleanup()
		return err
	}
	for _, fn := range c.postStart {
		if err := fn(cmd); err != nil {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			_ = cmd.Wait()
			c.runCleanup()
			return err
		}
	}
	return nil
}

// Wait waits for the command to exit and releases any sandbox resources.
func (c *Cmd) Wait() error {
	if c.cmd == nil {
		return errors.New("sandbox: command not started")
	}
	err := c.cmd.Wait()
	c.ProcessState = c.cmd.ProcessState
	c.runCleanup()
	return err
}

// Output runs the command and returns its standard output.
func (c *Cmd) Output() ([]byte, error) {
	var buf bytes.Buffer
	if c.Stdout != nil {
		c.Stdout = io.MultiWriter(c.Stdout, &buf)
	} else {
		c.Stdout = &buf
	}
	err := c.Run()
	return buf.Bytes(), err
}

// CombinedOutput runs the command and returns its combined
// standard output and standard error.
func (c *Cmd) CombinedOutput() ([]byte, error) {
	var buf bytes.Buffer
	if c.Stdout != nil {
		c.Stdout = io.MultiWriter(c.Stdout, &buf)
	} else {
		c.Stdout = &buf
	}
	if c.Stderr != nil {
		c.Stderr = io.MultiWriter(c.Stderr, &buf)
	} else {
		c.Stderr = &buf
	}
	err := c.Run()
	return buf.Bytes(), err
}

// build constructs the underlying exec.Cmd, applying sandbox constraints.
// Falls back to unsandboxed execution when the platform is not supported.
func (c *Cmd) build() (*exec.Cmd, error) {
	writable := c.Policy.WritableDirs
	if writable == nil {
		dir := c.Dir
		if dir == "" {
			dir = "."
		}
		writable = []string{dir}
	}

	env := c.Env
	if env == nil {
		env = os.Environ()
	}

	var cmd *exec.Cmd
	if c.ctx != nil {
		cmd = exec.CommandContext(c.ctx, c.Path, c.Args[1:]...)
	} else {
		cmd = exec.Command(c.Path, c.Args[1:]...)
	}
	cmd.Args = c.Args
	cmd.Dir = c.Dir
	cmd.Env = env
	cmd.Stdin = c.Stdin
	cmd.Stdout = c.Stdout
	cmd.Stderr = c.Stderr

	sb := &sandboxCtx{
		writable:         writable,
		limits:           c.Policy.Limits,
		killOnParentExit: c.Policy.KillOnParentExit,
	}
	if err := applySandbox(cmd, sb); err != nil {
		return nil, &exec.Error{Name: c.Path, Err: err}
	}
	for _, fn := range sb.cleanup {
		c.addCleanup(fn)
	}
	for _, fn := range sb.postStart {
		c.addPostStart(fn)
	}

	return cmd, nil
}

type sandboxCtx struct {
	writable         []string
	limits           ResourceLimits
	killOnParentExit bool
	cleanup          []func()
	postStart        []func(*exec.Cmd) error
}

func (s *sandboxCtx) addCleanup(fn func()) {
	s.cleanup = append(s.cleanup, fn)
}

func (s *sandboxCtx) addPostStart(fn func(*exec.Cmd) error) {
	s.postStart = append(s.postStart, fn)
}

func (c *Cmd) addCleanup(fn func()) {
	c.cleanup = append(c.cleanup, fn)
}

func (c *Cmd) addPostStart(fn func(*exec.Cmd) error) {
	c.postStart = append(c.postStart, fn)
}

func (c *Cmd) runCleanup() {
	for i := len(c.cleanup) - 1; i >= 0; i-- {
		c.cleanup[i]()
	}
	c.cleanup = nil
}

// String returns a human-readable description of the command,
// useful for logging and debugging. Mirrors os/exec.Cmd.String().
func (c *Cmd) String() string {
	if c.Path == "" {
		return ""
	}
	s := c.Path
	for _, a := range c.Args[1:] {
		s += " " + a
	}
	return s
}

// Available reports whether sandboxing is supported on this platform.
func Available() bool { return available() }

// ReasonUnavailable returns a human-readable reason why sandboxing
// is not available.
func ReasonUnavailable() string { return reasonUnavailable() }

// ProbeResult describes sandbox capabilities on the current platform.
type ProbeResult struct {
	Sandboxed bool
	Platform  string
	Backend   string
	Warning   string
}

// Probe detects sandbox capabilities on the current platform.
func Probe() ProbeResult {
	switch runtime.GOOS {
	case "linux":
		return probeLinux()
	case "darwin":
		return probeDarwin()
	case "windows":
		return probeWindows()
	default:
		return ProbeResult{
			Platform: runtime.GOOS,
			Backend:  "none",
			Warning:  "sandboxing not supported on " + runtime.GOOS + "; commands run unsandboxed",
		}
	}
}
