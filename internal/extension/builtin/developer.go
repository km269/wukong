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

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// DeveloperToolSet provides standard developer tools as function tools.
type DeveloperToolSet struct {
	tools  []tool.Tool
	inited bool
	closed bool
}

// NewDeveloperToolSet creates the built-in developer tool set.
func NewDeveloperToolSet() *DeveloperToolSet {
	ts := &DeveloperToolSet{}
	ts.tools = []tool.Tool{
		function.NewFunctionTool(
			ts.readFile,
			function.WithName("file_read"),
			function.WithDescription(
				"Read contents of a file at the given path. "+
					"Returns file content as text.",
			),
		),
		function.NewFunctionTool(
			ts.writeFile,
			function.WithName("file_write"),
			function.WithDescription(
				"Write content to a file at the given path. "+
					"Creates the file if it doesn't exist, "+
					"overwrites if it does.",
			),
		),
		function.NewFunctionTool(
			ts.executeCommand,
			function.WithName("command_execute"),
			function.WithDescription(
				"Execute a shell command and return its output. "+
					"Use this to run build tools, tests, git commands, etc.",
			),
		),
		function.NewFunctionTool(
			ts.searchCode,
			function.WithName("code_search"),
			function.WithDescription(
				"Search for code patterns in the project "+
					"directory using ripgrep-like pattern matching.",
			),
		),
		function.NewFunctionTool(
			ts.listDirectory,
			function.WithName("directory_list"),
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
	data, err := os.ReadFile(req.Path)
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
	err := os.WriteFile(req.Path, []byte(req.Content), 0644)
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

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "sh", "-c", req.Command)
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return CommandExecuteRsp{
				Success: false,
				Error:   err.Error(),
			}, nil
		}
	}

	return CommandExecuteRsp{
		Success:  exitCode == 0,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
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

	cmd := exec.CommandContext(
		ctx, "rg", "--no-heading", "-n",
		req.Pattern, searchPath,
	)
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
