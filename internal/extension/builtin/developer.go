// Package builtin provides built-in extensions for wukong.
// These are function-based tools that provide common agent capabilities
// like file operations, command execution, and code search.
package builtin

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/km269/wukong/pkg/capability"
	"github.com/km269/wukong/pkg/sandbox"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// DeveloperToolSet provides standard developer tools as function tools.
// fs/shell execution are delegated to capability.FileService /
// ShellService so tests can inject mocks and backends can be
// substituted without touching tool logic.
type DeveloperToolSet struct {
	tools  []tool.Tool
	inited bool
	closed bool
	fs     capability.FileService
	shell  capability.ShellService
}

// Option configures a DeveloperToolSet at construction.
type Option func(*DeveloperToolSet)

// WithFileService overrides the default OS-backed FileService (e.g.
// for mock injection in tests).
func WithFileService(fs capability.FileService) Option {
	return func(ts *DeveloperToolSet) { ts.fs = fs }
}

// WithShellService overrides the default sandbox-backed ShellService.
func WithShellService(ss capability.ShellService) Option {
	return func(ts *DeveloperToolSet) { ts.shell = ss }
}

// NewDeveloperToolSet creates the built-in developer tool set.
// Without options it uses the default OSFileService +
// SandboxShellService; pass WithFileService/WithShellService to
// inject alternatives (mocks, remote backends).
func NewDeveloperToolSet(opts ...Option) *DeveloperToolSet {
	ts := &DeveloperToolSet{
		fs:    capability.NewOSFileService(),
		shell: capability.NewSandboxShellService(),
	}
	for _, o := range opts {
		o(ts)
	}
	ts.tools = []tool.Tool{
		function.NewFunctionTool(
			ts.readFile,
			function.WithName("developer_file_read"),
			function.WithDescription(
				"Read contents of a file at the given path. "+
					"Returns file content as text.",
			),
		),
		function.NewFunctionTool(
			ts.writeFile,
			function.WithName("developer_file_write"),
			function.WithDescription(
				"Write content to a file at the given path. "+
					"Creates the file if it doesn't exist, "+
					"overwrites if it does.",
			),
		),
		function.NewFunctionTool(
			ts.replaceInFile,
			function.WithName("developer_file_replace"),
			function.WithDescription(
				"Find and replace text in a file. Uses exact "+
					"string matching. Prefer this over file_write "+
					"for targeted edits to avoid rewriting entire files.",
			),
		),
		function.NewFunctionTool(
			ts.executeCommand,
			function.WithName("developer_command_execute"),
			function.WithDescription(
				"Execute a shell command and return its output. "+
					"Use this to run build tools, tests, git commands, etc. "+
					"Output is truncated if too long.",
			),
		),
		function.NewFunctionTool(
			ts.searchCode,
			function.WithName("developer_code_search"),
			function.WithDescription(
				"Search for code patterns in the project "+
					"directory using ripgrep. Automatically "+
					"excludes common system directories.",
			),
		),
		function.NewFunctionTool(
			ts.listDirectory,
			function.WithName("developer_directory_list"),
			function.WithDescription(
				"List files and directories at the given path.",
			),
		),
	}
	return ts
}

// Tools returns the developer tools.
func (ts *DeveloperToolSet) Tools(
	ctx context.Context,
) []tool.Tool {
	return ts.tools
}

// Name returns the tool set name.
func (ts *DeveloperToolSet) Name() string {
	return "developer"
}

// Init initializes the tool set.
func (ts *DeveloperToolSet) Init(ctx context.Context) error {
	ts.inited = true
	return nil
}

// Close releases resources.
func (ts *DeveloperToolSet) Close() error {
	ts.closed = true
	return nil
}

// FileReadReq is the input for reading a file.
type FileReadReq struct {
	Path string `json:"path" jsonschema:"description=Path to the file to read"`
}

// FileReadRsp is the output for reading a file.
type FileReadRsp struct {
	Success bool   `json:"success"`
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (ts *DeveloperToolSet) readFile(
	ctx context.Context, req FileReadReq,
) (FileReadRsp, error) {
	data, err := ts.fs.Read(ctx, req.Path)
	if err != nil {
		return FileReadRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}
	return FileReadRsp{
		Success: true,
		Content: string(data),
	}, nil
}

// FileWriteReq is the input for writing a file.
type FileWriteReq struct {
	Path    string `json:"path" jsonschema:"description=Path to the file to write"`
	Content string `json:"content" jsonschema:"description=Content to write"`
}

// FileWriteRsp is the output for writing a file.
type FileWriteRsp struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (ts *DeveloperToolSet) writeFile(
	ctx context.Context, req FileWriteReq,
) (FileWriteRsp, error) {
	err := ts.fs.Write(ctx, req.Path, []byte(req.Content))
	if err != nil {
		return FileWriteRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}
	return FileWriteRsp{
		Success: true,
		Message: fmt.Sprintf(
			"File %q written successfully", req.Path,
		),
	}, nil
}

// FileReplaceReq is the input for find-and-replace in a file.
type FileReplaceReq struct {
	Path    string `json:"path" jsonschema:"description=Path to the file to modify"`
	OldStr  string `json:"old_str" jsonschema:"description=Exact text to find and replace"`
	NewStr  string `json:"new_str" jsonschema:"description=Text to replace with (use empty string to delete)"`
}

// FileReplaceRsp is the output for find-and-replace.
type FileReplaceRsp struct {
	Success     bool   `json:"success"`
	Message     string `json:"message,omitempty"`
	Occurrences int    `json:"occurrences"`
	Error       string `json:"error,omitempty"`
}

func (ts *DeveloperToolSet) replaceInFile(
	ctx context.Context, req FileReplaceReq,
) (FileReplaceRsp, error) {
	count, err := ts.fs.Replace(ctx, req.Path, req.OldStr, req.NewStr)
	if err != nil {
		return FileReplaceRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}
	if count == 0 {
		return FileReplaceRsp{
			Success: false,
			Error: fmt.Sprintf(
				"old_str not found in %q", req.Path,
			),
		}, nil
	}
	return FileReplaceRsp{
		Success:     true,
		Message:     fmt.Sprintf("Replaced %d occurrence(s) in %q", count, req.Path),
		Occurrences: count,
	}, nil
}

// CommandExecuteReq is the input for executing a command.
type CommandExecuteReq struct {
	Command string `json:"command" jsonschema:"description=Shell command to execute"`
	WorkDir string `json:"work_dir,omitempty" jsonschema:"description=Working directory"`
	Timeout int    `json:"timeout,omitempty" jsonschema:"description=Timeout in seconds (default 30)"`
}

// CommandExecuteRsp is the output for executing a command.
type CommandExecuteRsp struct {
	Success  bool   `json:"success"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

func (ts *DeveloperToolSet) executeCommand(
	ctx context.Context, req CommandExecuteReq,
) (CommandExecuteRsp, error) {
	timeout := 30 * time.Second
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}

	// Delegate to the ShellService seam (default: sandboxed exec via
	// pkg/sandbox; mock/remote backends injectable via WithShellService).
	res, _ := ts.shell.Execute(ctx, capability.ExecRequest{
		Command: req.Command,
		WorkDir: req.WorkDir,
		Timeout: timeout,
	})
	if res.Err != nil {
		return CommandExecuteRsp{
			Success: false,
			Error:   res.Err.Error(),
		}, nil
	}

	return formatCommandOutput(res.Stdout, res.Stderr, res.ExitCode)
}

// formatCommandOutput truncates and formats command output for consistent responses.
func formatCommandOutput(stdoutStr, stderrStr string, exitCode int) (CommandExecuteRsp, error) {
	const maxOutput = 8000
	if len(stdoutStr) > maxOutput {
		stdoutStr = stdoutStr[:maxOutput] +
			fmt.Sprintf("\n... (truncated, %d bytes total)", len(stdoutStr))
	}
	if len(stderrStr) > maxOutput {
		stderrStr = stderrStr[:maxOutput] +
			fmt.Sprintf("\n... (truncated, %d bytes total)", len(stderrStr))
	}
	return CommandExecuteRsp{
		Success:  exitCode == 0,
		Stdout:   stdoutStr,
		Stderr:   stderrStr,
		ExitCode: exitCode,
	}, nil
}

// CodeSearchReq is the input for searching code.
type CodeSearchReq struct {
	Pattern string `json:"pattern" jsonschema:"description=Search pattern (regex supported)"`
	Path    string `json:"path,omitempty" jsonschema:"description=Directory to search (default: current)"`
}

// CodeSearchRsp is the output for searching code.
type CodeSearchRsp struct {
	Success bool     `json:"success"`
	Matches []string `json:"matches,omitempty"`
	Count   int      `json:"count"`
	Error   string   `json:"error,omitempty"`
}

func (ts *DeveloperToolSet) searchCode(
	ctx context.Context, req CodeSearchReq,
) (CodeSearchRsp, error) {
	searchPath := req.Path
	if searchPath == "" {
		searchPath = "."
	}

	// Use sandbox-wrapped ripgrep with system file exclusion.
	// The sandbox limits writes to the search path, preventing
	// accidental modification of system files.
	cmd := sandbox.CommandContext(
		ctx, "rg", "--no-heading", "-n",
		"--glob", "!.git",
		"--glob", "!node_modules",
		"--glob", "!.venv",
		"--glob", "!vendor",
		"--glob", "!__pycache__",
		"--glob", "!*.pyc",
		"--glob", "!*.class",
		"--glob", "!*.o",
		"--glob", "!*.so",
		"--glob", "!*.dll",
		"--glob", "!*.exe",
		"--glob", "!*.bin",
		"--glob", "!*.zip",
		"--glob", "!*.tar*",
		"--glob", "!*.gz",
		req.Pattern, searchPath,
	)
	cmd.Dir = searchPath
	cmd.Policy.WritableDirs = []string{searchPath, ".wukong"}
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 {
				return CodeSearchRsp{Success: true, Count: 0}, nil
			}
		}
		return CodeSearchRsp{
			Success: false,
			Error:   fmt.Sprintf("search failed: %v", err),
		}, nil
	}

	lines := strings.Split(
		strings.TrimSpace(string(output)), "\n",
	)
	if len(lines) == 1 && lines[0] == "" {
		return CodeSearchRsp{Success: true, Count: 0}, nil
	}

	return CodeSearchRsp{
		Success: true,
		Matches: lines,
		Count:   len(lines),
	}, nil
}

// ListDirectoryReq is the input for listing a directory.
type ListDirectoryReq struct {
	Path string `json:"path" jsonschema:"description=Directory path to list"`
}

// ListDirectoryRsp is the output for listing a directory.
type ListDirectoryRsp struct {
	Success bool     `json:"success"`
	Entries []string `json:"entries,omitempty"`
	Count   int      `json:"count"`
	Error   string   `json:"error,omitempty"`
}

func (ts *DeveloperToolSet) listDirectory(
	ctx context.Context, req ListDirectoryReq,
) (ListDirectoryRsp, error) {
	entries, err := os.ReadDir(req.Path)
	if err != nil {
		return ListDirectoryRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}

	return ListDirectoryRsp{
		Success: true,
		Entries: names,
		Count:   len(names),
	}, nil
}
