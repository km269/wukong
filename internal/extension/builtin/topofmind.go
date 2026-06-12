// Package builtin provides the Top of Mind tool set.
package builtin

import (
	"context"
	"fmt"

	"github.com/km269/wukong/internal/topofmind"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// TopOfMindToolSet provides tools for managing persistent instructions.
type TopOfMindToolSet struct {
	tools  []tool.Tool
	mgr    *topofmind.Manager
	inited bool
	closed bool
}

// NewTopOfMindToolSet creates the Top of Mind tool set.
func NewTopOfMindToolSet(mgr *topofmind.Manager) *TopOfMindToolSet {
	ts := &TopOfMindToolSet{mgr: mgr}
	ts.tools = []tool.Tool{
		function.NewFunctionTool(
			ts.getInstructions,
			function.WithName("tom_get"),
			function.WithDescription(
				"Get current persistent instructions that are "+
					"injected into every conversation round.",
			),
		),
		function.NewFunctionTool(
			ts.setInstructions,
			function.WithName("tom_set"),
			function.WithDescription(
				"Set persistent instructions that will be "+
					"injected into every future conversation round. "+
					"Use this to remember important context, rules, "+
					"or preferences across turns.",
			),
		),
		function.NewFunctionTool(
			ts.appendInstructions,
			function.WithName("tom_append"),
			function.WithDescription(
				"Append to existing persistent instructions.",
			),
		),
		function.NewFunctionTool(
			ts.clearInstructions,
			function.WithName("tom_clear"),
			function.WithDescription(
				"Clear all persistent instructions.",
			),
		),
	}
	return ts
}

// Tools returns the Top of Mind tools.
func (ts *TopOfMindToolSet) Tools(ctx context.Context) []tool.Tool {
	return ts.tools
}

// Name returns the tool set name.
func (ts *TopOfMindToolSet) Name() string {
	return "top_of_mind"
}

// Init initializes the tool set.
func (ts *TopOfMindToolSet) Init(ctx context.Context) error {
	ts.inited = true
	return nil
}

// Close releases resources.
func (ts *TopOfMindToolSet) Close() error {
	ts.closed = true
	return nil
}

// TOMGetReq is the input for getting instructions.
type TOMGetReq struct{}

// TOMGetRsp is the output for getting instructions.
type TOMGetRsp struct {
	Success      bool   `json:"success"`
	Instructions string `json:"instructions,omitempty"`
}

func (ts *TopOfMindToolSet) getInstructions(
	ctx context.Context, req TOMGetReq,
) (TOMGetRsp, error) {
	instructions := ts.mgr.GetInstructions()
	return TOMGetRsp{
		Success:      true,
		Instructions: instructions,
	}, nil
}

// TOMSetReq is the input for setting instructions.
type TOMSetReq struct {
	Content string `json:"content" jsonschema:"description=Persistent instructions to inject into every turn"`
}

// TOMSetRsp is the output for setting instructions.
type TOMSetRsp struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

func (ts *TopOfMindToolSet) setInstructions(
	ctx context.Context, req TOMSetReq,
) (TOMSetRsp, error) {
	ts.mgr.SetInstructions(req.Content)
	return TOMSetRsp{
		Success: true,
		Message: fmt.Sprintf(
			"Persistent instructions set (%d chars)",
			len(req.Content),
		),
	}, nil
}

// TOMAppendReq is the input for appending instructions.
type TOMAppendReq struct {
	Content string `json:"content" jsonschema:"description=Content to append to existing instructions"`
}

// TOMAppendRsp is the output for appending.
type TOMAppendRsp struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

func (ts *TopOfMindToolSet) appendInstructions(
	ctx context.Context, req TOMAppendReq,
) (TOMAppendRsp, error) {
	ts.mgr.AppendInstructions(req.Content)
	return TOMAppendRsp{
		Success: true,
		Message: "Instructions appended",
	}, nil
}

// TOMClearReq is the input for clearing instructions.
type TOMClearReq struct{}

// TOMClearRsp is the output for clearing.
type TOMClearRsp struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

func (ts *TopOfMindToolSet) clearInstructions(
	ctx context.Context, req TOMClearReq,
) (TOMClearRsp, error) {
	ts.mgr.ClearInstructions()
	return TOMClearRsp{
		Success: true,
		Message: "All persistent instructions cleared",
	}, nil
}
