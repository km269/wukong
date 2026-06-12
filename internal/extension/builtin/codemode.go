// Package builtin provides the Code Mode tool set for JS execution.
package builtin

import (
	"context"

	"github.com/km269/wukong/internal/codemode"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// CodeModeToolSet provides tools for executing JavaScript code.
type CodeModeToolSet struct {
	tools    []tool.Tool
	executor *codemode.Executor
	inited   bool
	closed   bool
}

// NewCodeModeToolSet creates the code mode tool set.
func NewCodeModeToolSet(executor *codemode.Executor) *CodeModeToolSet {
	ts := &CodeModeToolSet{executor: executor}
	ts.tools = []tool.Tool{
		function.NewFunctionTool(
			ts.executeJS,
			function.WithName("code_execute"),
			function.WithDescription(
				"Execute JavaScript code in a sandboxed "+
					"environment. Use this for dynamic tool "+
					"discovery, data processing, or scripting.",
			),
		),
		function.NewFunctionTool(
			ts.discoverTools,
			function.WithName("code_discover_tools"),
			function.WithDescription(
				"Discover available tools dynamically through "+
					"JavaScript execution.",
			),
		),
	}
	return ts
}

// Tools returns the code mode tools.
func (ts *CodeModeToolSet) Tools(ctx context.Context) []tool.Tool {
	return ts.tools
}

// Name returns the tool set name.
func (ts *CodeModeToolSet) Name() string {
	return "code_mode"
}

// Init initializes the tool set.
func (ts *CodeModeToolSet) Init(ctx context.Context) error {
	ts.inited = true
	return nil
}

// Close releases resources.
func (ts *CodeModeToolSet) Close() error {
	ts.closed = true
	return nil
}

// CodeExecuteReq is the input for executing JS code.
type CodeExecuteReq struct {
	Code string `json:"code" jsonschema:"description=JavaScript code to execute in sandboxed environment"`
}

// CodeExecuteRsp is the output for executing JS code.
type CodeExecuteRsp struct {
	Success bool   `json:"success"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
	Elapsed string `json:"elapsed,omitempty"`
}

func (ts *CodeModeToolSet) executeJS(
	ctx context.Context, req CodeExecuteReq,
) (CodeExecuteRsp, error) {
	result := ts.executor.Execute(ctx, req.Code)
	return CodeExecuteRsp{
		Success: result.Success,
		Output:  result.Output,
		Error:   result.Error,
		Elapsed: result.Elapsed.String(),
	}, nil
}

// CodeDiscoverReq is the input for tool discovery.
type CodeDiscoverReq struct{}

// CodeDiscoverRsp is the output for tool discovery.
type CodeDiscoverRsp struct {
	Success bool                       `json:"success"`
	Tools   []codemode.DiscoveredTool  `json:"tools,omitempty"`
	Count   int                        `json:"count"`
	Error   string                     `json:"error,omitempty"`
}

func (ts *CodeModeToolSet) discoverTools(
	ctx context.Context, req CodeDiscoverReq,
) (CodeDiscoverRsp, error) {
	tools, err := ts.executor.ExecuteToolDiscovery(ctx)
	if err != nil {
		return CodeDiscoverRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}
	return CodeDiscoverRsp{
		Success: true,
		Tools:   tools,
		Count:   len(tools),
	}, nil
}
