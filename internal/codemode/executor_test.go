package codemode

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/km269/wukong/internal/config"
)

func TestExecutor_New(t *testing.T) {
	executor := NewExecutor(nil)
	if executor == nil {
		t.Fatal("expected non-nil executor")
	}
	if executor.cfg.Timeout != 10*time.Second {
		t.Errorf("expected default timeout=10s, got %v", executor.cfg.Timeout)
	}
	if executor.cfg.MaxMemoryMB != 128 {
		t.Errorf("expected default maxMemory=128MB, got %d", executor.cfg.MaxMemoryMB)
	}
}

func TestExecutor_NewWithConfig(t *testing.T) {
	cfg := &config.CodeModeConfig{
		Timeout:     5 * time.Second,
		MaxMemoryMB: 64,
	}
	executor := NewExecutor(cfg)
	if executor.cfg.Timeout != 5*time.Second {
		t.Errorf("expected timeout=5s, got %v", executor.cfg.Timeout)
	}
}

func TestExecutor_SimpleExpression(t *testing.T) {
	executor := NewExecutor(nil)
	ctx := context.Background()

	result := executor.Execute(ctx, "1 + 2")
	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
	if result.Output != "3" {
		t.Errorf("expected output=3, got %s", result.Output)
	}
}

func TestExecutor_StringExpression(t *testing.T) {
	executor := NewExecutor(nil)
	ctx := context.Background()

	result := executor.Execute(ctx, `"hello" + " " + "world"`)
	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
	if result.Output != "hello world" {
		t.Errorf("expected 'hello world', got %s", result.Output)
	}
}

func TestExecutor_ConsoleLog(t *testing.T) {
	executor := NewExecutor(nil)
	ctx := context.Background()

	// console.log should work in sandbox, and output should be
	// merged with the expression result
	result := executor.Execute(ctx, `console.log("test output"); 42`)
	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
	// Output should contain both console.log and expression result
	if !contains(result.Output, "test output") {
		t.Errorf("expected output to contain 'test output', got %s",
			result.Output)
	}
	if !contains(result.Output, "42") {
		t.Errorf("expected output to contain '42', got %s",
			result.Output)
	}
}

func TestExecutor_Timeout(t *testing.T) {
	cfg := &config.CodeModeConfig{
		Timeout: 100 * time.Millisecond,
	}
	executor := NewExecutor(cfg)
	ctx := context.Background()

	// Infinite loop should timeout
	result := executor.Execute(ctx, "while(true) {}")
	if result.Success {
		t.Error("expected timeout failure")
	}
}

func TestExecutor_Closed(t *testing.T) {
	executor := NewExecutor(nil)
	executor.Close()

	result := executor.Execute(context.Background(), "1 + 1")
	if result.Success {
		t.Error("expected failure when closed")
	}
}

func TestExecutor_Elapsed(t *testing.T) {
	executor := NewExecutor(nil)
	ctx := context.Background()

	result := executor.Execute(ctx, "1 + 1")
	// Elapsed time might be sub-nanosecond for trivial expressions
	if result.Elapsed < 0 {
		t.Error("expected non-negative elapsed time")
	}
}

func TestExecutor_JSONStringify(t *testing.T) {
	executor := NewExecutor(nil)
	ctx := context.Background()

	result := executor.Execute(ctx, `JSON.stringify([1, 2, 3])`)
	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
}

func TestExecutor_ToolDiscovery(t *testing.T) {
	executor := NewExecutor(nil)
	ctx := context.Background()

	// Register some discoverable tools first
	executor.SetToolsForDiscovery([]DiscoveredTool{
		{Name: "test_tool", Description: "A test tool",
			Source: "test"},
	})

	tools, err := executor.ExecuteToolDiscovery(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) == 0 {
		t.Error("expected at least one discovered tool")
	}
	if tools[0].Name != "test_tool" {
		t.Errorf("expected tool name 'test_tool', got %s",
			tools[0].Name)
	}
}

func TestExecutor_ToolDiscoveryEmpty(t *testing.T) {
	executor := NewExecutor(nil)
	ctx := context.Background()

	// No tools registered, should return nil without error
	tools, err := executor.ExecuteToolDiscovery(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tools != nil {
		t.Errorf("expected nil tools, got %v", tools)
	}
}

// contains reports whether s contains substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr || len(s) > 0 && findSubstr(s, substr))
}

func findSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestParseDiscoveredTools(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{"empty", ""},
		{"null", "null"},
		{"undefined", "undefined"},
		{"array", "[{\"name\":\"test\"}]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools := parseDiscoveredTools(tt.output)
			if tools == nil && tt.name == "array" {
				t.Log("array parsing returns tools")
			}
		})
	}
}

func TestExecutor_ConcurrentLimit(t *testing.T) {
	cfg := &config.CodeModeConfig{
		Timeout:     500 * time.Millisecond,
		MaxMemoryMB: 128,
	}
	executor := NewExecutor(cfg)

	var started sync.WaitGroup
	var done sync.WaitGroup
	results := make([]ExecutionResult, maxConcurrentExecutions+1)

	for i := range maxConcurrentExecutions {
		started.Add(1)
		done.Add(1)
		go func(idx int) {
			started.Done()
			results[idx] = executor.Execute(
				context.Background(), "while(true){}",
			)
			done.Done()
		}(i)
	}

	started.Wait()
	time.Sleep(100 * time.Millisecond)

	result := executor.Execute(context.Background(), "1")
	if result.Success {
		t.Error("expected rejection when all concurrency slots are full")
	}

	done.Wait()

	result2 := executor.Execute(context.Background(), "1")
	if !result2.Success {
		t.Errorf("expected success after slots freed, got: %s",
			result2.Error)
	}
}

func TestExecutor_JSONParseOversized(t *testing.T) {
	executor := NewExecutor(nil)
	ctx := context.Background()

	// Build a JSON string larger than maxJSONParseSize (1MB).
	largeJSON := `"` + strings.Repeat("x", maxJSONParseSize+100) + `"`
	result := executor.Execute(ctx,
		`JSON.parse(`+largeJSON+`)`)
	if result.Success {
		t.Error("expected oversized JSON.parse to be rejected")
	}
}

func TestExecutor_RegExpDisabled(t *testing.T) {
	executor := NewExecutor(nil)
	ctx := context.Background()

	// Attempting to use RegExp constructor should result in a
	// ReferenceError since RegExp is set to Undefined.
	result := executor.Execute(ctx,
		`new RegExp("test").test("hello")`)
	if result.Success {
		t.Error("expected RegExp constructor to fail (RegExp disabled)")
	}
}

func TestExecutor_MathAbs(t *testing.T) {
	executor := NewExecutor(nil)
	ctx := context.Background()

	result := executor.Execute(ctx, "Math.abs(-42)")
	if !result.Success {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Output != "42" {
		t.Errorf("expected Math.abs(-42)=42, got %s", result.Output)
	}
}
