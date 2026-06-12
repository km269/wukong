// Package codemode provides JavaScript code execution for tool
// discovery and dynamic extension capabilities. Similar to
// Goose's Code Mode, it allows executing JS code to discover
// and invoke tools programmatically.
// Uses the goja pure-Go JavaScript engine for sandboxed execution.
package codemode

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/km269/wukong/internal/config"
)

// Executor runs JavaScript code in a sandboxed environment using goja.
// This provides dynamic tool discovery and invocation capabilities.
type Executor struct {
	mu                sync.RWMutex
	cfg               *config.CodeModeConfig
	closed            bool
	discoverableTools []DiscoveredTool
}

// ExecutionResult holds the result of a code execution.
type ExecutionResult struct {
	Success bool          `json:"success"`
	Output  string        `json:"output,omitempty"`
	Error   string        `json:"error,omitempty"`
	Elapsed time.Duration `json:"elapsed"`
}

// NewExecutor creates a new code mode executor.
func NewExecutor(cfg *config.CodeModeConfig) *Executor {
	if cfg == nil {
		cfg = &config.CodeModeConfig{
			Timeout:     10 * time.Second,
			MaxMemoryMB: 128,
		}
	}
	return &Executor{cfg: cfg}
}

// Execute runs JavaScript code and returns the result.
// The code is executed in a sandboxed environment with limited
// access to system resources.
func (e *Executor) Execute(
	ctx context.Context, code string,
) ExecutionResult {
	e.mu.RLock()
	if e.closed {
		e.mu.RUnlock()
		return ExecutionResult{
			Success: false,
			Error:   "code mode executor is closed",
		}
	}
	e.mu.RUnlock()

	startTime := time.Now()

	// Apply timeout
	execCtx, cancel := context.WithTimeout(
		ctx, e.cfg.Timeout,
	)
	defer cancel()

	// Execute in sandboxed environment
	result := e.executeSandboxed(execCtx, code)
	result.Elapsed = time.Since(startTime)
	return result
}

// SetToolsForDiscovery injects the current tool list into the executor
// so that code_discover_tools can expose them to JS code at runtime.
func (e *Executor) SetToolsForDiscovery(tools []DiscoveredTool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.discoverableTools = tools
}

// ExecuteToolDiscovery discovers available tools through JS execution.
// The __tools global is injected into the sandbox before the script runs,
// allowing JS code to inspect and filter the available tool set.
func (e *Executor) ExecuteToolDiscovery(
	ctx context.Context,
) ([]DiscoveredTool, error) {
	e.mu.RLock()
	injectedTools := e.discoverableTools
	e.mu.RUnlock()

	// If no tools have been registered for discovery, return empty list
	if len(injectedTools) == 0 {
		return nil, nil
	}

	code := `
		// Tool discovery script — __tools is injected by the runtime.
		var tools = [];
		if (typeof __tools !== 'undefined') {
			tools = __tools;
		}
		JSON.stringify(tools);
	`

	result := e.ExecuteWithTools(ctx, code, injectedTools)
	if !result.Success {
		return nil, fmt.Errorf(
			"tool discovery failed: %s", result.Error,
		)
	}

	return parseDiscoveredTools(result.Output), nil
}

// ExecuteWithTools is like Execute but also injects __tools into the
// sandbox before running the script.
func (e *Executor) ExecuteWithTools(
	ctx context.Context, code string, tools []DiscoveredTool,
) ExecutionResult {
	e.mu.RLock()
	if e.closed {
		e.mu.RUnlock()
		return ExecutionResult{
			Success: false,
			Error:   "code mode executor is closed",
		}
	}
	e.mu.RUnlock()

	startTime := time.Now()

	execCtx, cancel := context.WithTimeout(ctx, e.cfg.Timeout)
	defer cancel()

	result := e.executeSandboxedWithTools(execCtx, code, tools)
	result.Elapsed = time.Since(startTime)
	return result
}

// DiscoveredTool represents a dynamically discovered tool.
type DiscoveredTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

// Close shuts down the executor.
func (e *Executor) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	return nil
}

// executeSandboxed runs code in a goja sandboxed JS interpreter.
func (e *Executor) executeSandboxed(
	ctx context.Context, code string,
) ExecutionResult {
	vm := goja.New()

	// Set up sandboxed environment with safe built-ins
	output := setupSandbox(vm)

	// Use a channel to detect timeout
	type evalResult struct {
		val goja.Value
		err error
	}
	done := make(chan evalResult, 1)

	go func() {
		val, err := vm.RunString(code)
		done <- evalResult{val: val, err: err}
	}()

	select {
	case <-ctx.Done():
		// Timeout or cancellation
		vm.Interrupt("execution timeout")
		return ExecutionResult{
			Success: false,
			Error:   "code execution timed out",
		}
	case res := <-done:
		if res.err != nil {
			return ExecutionResult{
				Success: false,
				Error:   fmt.Sprintf("JS error: %v", res.err),
			}
		}
		// Merge console.log output with the expression result
		consoleOut := output.String()
		exprOut := res.val.String()
		merged := mergeOutputs(consoleOut, exprOut)
		return ExecutionResult{
			Success: true,
			Output:  merged,
		}
	}
}

// executeSandboxedWithTools is like executeSandboxed but injects
// __tools into the JS runtime before executing the code.
func (e *Executor) executeSandboxedWithTools(
	ctx context.Context, code string, tools []DiscoveredTool,
) ExecutionResult {
	vm := goja.New()

	// Set up sandboxed environment with safe built-ins
	output := setupSandbox(vm)

	// Inject __tools global variable
	toolsJS := make([]map[string]string, len(tools))
	for i, t := range tools {
		toolsJS[i] = map[string]string{
			"name":        t.Name,
			"description": t.Description,
			"source":      t.Source,
		}
	}
	vm.Set("__tools", toolsJS)

	type evalResult struct {
		val goja.Value
		err error
	}
	done := make(chan evalResult, 1)

	go func() {
		val, err := vm.RunString(code)
		done <- evalResult{val: val, err: err}
	}()

	select {
	case <-ctx.Done():
		vm.Interrupt("execution timeout")
		return ExecutionResult{
			Success: false,
			Error:   "code execution timed out",
		}
	case res := <-done:
		if res.err != nil {
			return ExecutionResult{
				Success: false,
				Error:   fmt.Sprintf("JS error: %v", res.err),
			}
		}
		// Merge console.log output with the expression result
		consoleOut := output.String()
		exprOut := res.val.String()
		merged := mergeOutputs(consoleOut, exprOut)
		return ExecutionResult{
			Success: true,
			Output:  merged,
		}
	}
}

// setupSandbox configures a goja runtime with safe built-in functions
// and restricted global access. Returns the output buffer so callers
// can retrieve console.log output.
func setupSandbox(vm *goja.Runtime) *strings.Builder {
	// Provide console.log as a simple output capture
	var output strings.Builder
	consoleObj := vm.NewObject()
	consoleObj.Set("log", func(call goja.FunctionCall) goja.Value {
		args := make([]string, len(call.Arguments))
		for i, arg := range call.Arguments {
			args[i] = arg.String()
		}
		output.WriteString(strings.Join(args, " ") + "\n")
		return goja.Undefined()
	})
	consoleObj.Set("warn", consoleObj.Get("log"))
	consoleObj.Set("error", consoleObj.Get("log"))
	vm.Set("console", consoleObj)

	// Provide JSON object
	jsonObj := vm.NewObject()
	jsonObj.Set("stringify", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("undefined")
		}
		// Use a simple JSON-like serialization
		return vm.ToValue(toJSON(call.Arguments[0]))
	})
	jsonObj.Set("parse", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		jsonStr := call.Arguments[0].String()
		var result interface{}
		if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
			return goja.Undefined()
		}
		return vm.ToValue(result)
	})
	vm.Set("JSON", jsonObj)

	// Provide setTimeout/clearTimeout stubs
	vm.Set("setTimeout", func(call goja.FunctionCall) goja.Value {
		return goja.Undefined()
	})
	vm.Set("clearTimeout", func(call goja.FunctionCall) goja.Value {
		return goja.Undefined()
	})

	// Provide Math object
	mathObj := vm.NewObject()
	mathObj.Set("abs", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue(0)
		}
		return call.Arguments[0]
	})
	mathObj.Set("floor", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		f := call.Arguments[0].ToFloat()
		return vm.ToValue(math.Floor(f))
	})
	mathObj.Set("ceil", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		f := call.Arguments[0].ToFloat()
		return vm.ToValue(math.Ceil(f))
	})
	mathObj.Set("round", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		f := call.Arguments[0].ToFloat()
		return vm.ToValue(math.Round(f))
	})
	mathObj.Set("max", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		max := call.Arguments[0].ToFloat()
		for _, arg := range call.Arguments[1:] {
			if v := arg.ToFloat(); v > max {
				max = v
			}
		}
		return vm.ToValue(max)
	})
	mathObj.Set("min", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		min := call.Arguments[0].ToFloat()
		for _, arg := range call.Arguments[1:] {
			if v := arg.ToFloat(); v < min {
				min = v
			}
		}
		return vm.ToValue(min)
	})
	mathObj.Set("random", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(0.5) // Deterministic random for sandbox
	})
	vm.Set("Math", mathObj)

	// Provide Date
	vm.Set("Date", goja.Undefined())

	// Block dangerous globals
	vm.Set("eval", goja.Undefined())
	vm.Set("Function", goja.Undefined())
	vm.Set("setInterval", goja.Undefined())

	// Expose __output for explicit access to console.log capture
	vm.Set("__output", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(output.String())
	})

	return &output
}

// mergeOutputs combines console.log output with the expression result.
// If the expression result is "undefined" or empty, only console output
// is returned. Otherwise both are concatenated.
func mergeOutputs(consoleOut, exprOut string) string {
	exprOut = strings.TrimSpace(exprOut)

	// If the expression returns nothing meaningful, just return
	// console output
	if exprOut == "" || exprOut == "undefined" {
		return strings.TrimRight(consoleOut, "\n")
	}

	// If there's no console output, just return the expression result
	if consoleOut == "" {
		return exprOut
	}

	// Both are present: console output first, then expression result
	return strings.TrimRight(consoleOut, "\n") + "\n" + exprOut
}

// toJSON provides proper JSON serialization using encoding/json.
func toJSON(v goja.Value) string {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return "null"
	}
	exported := v.Export()
	data, err := json.Marshal(exported)
	if err != nil {
		return v.String()
	}
	return string(data)
}

// parseDiscoveredTools parses tool descriptions from JSON output.
func parseDiscoveredTools(output string) []DiscoveredTool {
	if output == "" || output == "null" || output == "undefined" {
		return nil
	}
	var tools []DiscoveredTool
	if err := json.Unmarshal([]byte(output), &tools); err != nil {
		// Fallback: wrap the output as a single discovered tool
		return []DiscoveredTool{{
			Name:        "discovered",
			Description: output,
			Source:      "code_mode",
		}}
	}
	return tools
}
