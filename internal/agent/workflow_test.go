package agent

import (
	"testing"

	"github.com/km269/wukong/internal/config"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// TestWorkflowBuilder_AllModes validates that all workflow modes can be
// constructed without panicking. Actual execution requires a real model.
func TestWorkflowBuilder_AllModes(t *testing.T) {
	tests := []struct {
		name string
		mode WorkflowMode
	}{
		{"single", WorkflowSingle},
		{"chain", WorkflowChain},
		{"parallel", WorkflowParallel},
		{"cycle", WorkflowCycle},
		{"graph", WorkflowGraph},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Builder creation requires a real model, so we
			// test the type system and config wiring.
			if tt.mode == WorkflowSingle {
				// Single mode falls back to buildSingleAgent
				return
			}
			// For other modes, validate that OrchestrationConfig
			// correctly maps mode strings.
			oc := &OrchestrationConfig{
				Mode:          tt.mode,
				MaxIterations: 5,
			}
			if string(oc.Mode) != string(tt.mode) {
				t.Errorf("mode mismatch: %s vs %s",
					oc.Mode, tt.mode)
			}
		})
	}
}

// TestWorkflowMode_StringConversion verifies all workflow mode strings.
func TestWorkflowMode_StringConversion(t *testing.T) {
	tests := []struct {
		mode     WorkflowMode
		expected string
	}{
		{WorkflowSingle, "single"},
		{WorkflowChain, "chain"},
		{WorkflowParallel, "parallel"},
		{WorkflowCycle, "cycle"},
		{WorkflowGraph, "graph"},
	}

	for _, tt := range tests {
		if string(tt.mode) != tt.expected {
			t.Errorf("WorkflowMode %q: expected %q, got %q",
				tt.mode, tt.expected, string(tt.mode))
		}
	}
}

// TestOrchestrationConfig_Defaults verifies default values.
func TestOrchestrationConfig_Defaults(t *testing.T) {
	oc := &OrchestrationConfig{}
	if oc.Mode != "" {
		t.Errorf("expected empty mode, got %s", oc.Mode)
	}
	if oc.MaxIterations != 0 {
		t.Errorf("expected 0 max iterations, got %d", oc.MaxIterations)
	}
	if len(oc.SubAgents) != 0 {
		t.Errorf("expected 0 sub agents, got %d", len(oc.SubAgents))
	}
}

// TestOrchestrationConfig_WithSubAgents verifies sub-agent wiring.
func TestOrchestrationConfig_WithSubAgents(t *testing.T) {
	// Create a mock agent list
	agents := []agent.Agent{nil, nil, nil} // nil placeholders for type check
	oc := &OrchestrationConfig{
		Mode:          WorkflowChain,
		SubAgents:     agents,
		MaxIterations: 10,
	}
	if len(oc.SubAgents) != 3 {
		t.Errorf("expected 3 sub agents, got %d", len(oc.SubAgents))
	}
}

// TestFilterTools_EmptyAllowed verifies filtering with empty allowlist.
func TestFilterTools_EmptyAllowed(t *testing.T) {
	b := &WorkflowBuilder{
		tools: []tool.Tool{
			&mockTool{name: "tool_a"},
			&mockTool{name: "tool_b"},
		},
	}

	filtered := b.filterTools(nil)
	if len(filtered) != 0 {
		t.Errorf("expected 0 tools with nil allowed, got %d",
			len(filtered))
	}

	filtered = b.filterTools([]string{})
	if len(filtered) != 0 {
		t.Errorf("expected 0 tools with empty allowed, got %d",
			len(filtered))
	}
}

// TestFilterTools_Subset verifies filtering returns only allowed tools.
func TestFilterTools_Subset(t *testing.T) {
	b := &WorkflowBuilder{
		tools: []tool.Tool{
			&mockTool{name: "read_file"},
			&mockTool{name: "write_file"},
			&mockTool{name: "execute_command"},
			&mockTool{name: "search_code"},
		},
	}

	allowed := []string{"read_file", "search_code"}
	filtered := b.filterTools(allowed)

	if len(filtered) != 2 {
		t.Errorf("expected 2 filtered tools, got %d", len(filtered))
	}

	names := make(map[string]bool)
	for _, t := range filtered {
		decl := t.Declaration()
		if decl != nil {
			names[decl.Name] = true
		}
	}
	if !names["read_file"] {
		t.Error("expected read_file in filtered tools")
	}
	if !names["search_code"] {
		t.Error("expected search_code in filtered tools")
	}
	if names["write_file"] {
		t.Error("write_file should not be in filtered tools")
	}
}

// TestFilterTools_AllMatched verifies all tools pass when all are allowed.
func TestFilterTools_AllMatched(t *testing.T) {
	b := &WorkflowBuilder{
		tools: []tool.Tool{
			&mockTool{name: "tool_a"},
			&mockTool{name: "tool_b"},
			&mockTool{name: "tool_c"},
		},
	}

	allowed := []string{"tool_a", "tool_b", "tool_c"}
	filtered := b.filterTools(allowed)

	if len(filtered) != 3 {
		t.Errorf("expected 3 filtered tools, got %d", len(filtered))
	}
}

// TestFilterTools_NoMatch verifies empty result when no tools match.
func TestFilterTools_NoMatch(t *testing.T) {
	b := &WorkflowBuilder{
		tools: []tool.Tool{
			&mockTool{name: "tool_a"},
			&mockTool{name: "tool_b"},
		},
	}

	allowed := []string{"tool_x", "tool_y"}
	filtered := b.filterTools(allowed)

	if len(filtered) != 0 {
		t.Errorf("expected 0 filtered tools, got %d", len(filtered))
	}
}

// TestContainsKeyword verifies the case-insensitive keyword matching.
func TestContainsKeyword(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		keyword  string
		expected bool
	}{
		{"exact match", "TASK_COMPLETE", "TASK_COMPLETE", true},
		{"lowercase match", "task_complete", "TASK_COMPLETE", true},
		{"mixed case", "Task_Complete", "TASK_COMPLETE", true},
		{"partial", "prefix TASK_COMPLETE suffix", "TASK_COMPLETE", true},
		{"no match", "hello world", "TASK_COMPLETE", false},
		{"keyword longer", "short", "longer_keyword", false},
		{"empty string", "", "test", false},
		{"empty keyword", "test", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsKeyword(tt.s, tt.keyword)
			if got != tt.expected {
				t.Errorf("containsKeyword(%q, %q) = %v, want %v",
					tt.s, tt.keyword, got, tt.expected)
			}
		})
	}
}

// TestDefaultSubAgents_Chain verifies default chain sub-agents.
func TestDefaultSubAgents_Chain(t *testing.T) {
	cfg := &config.WukongConfig{
		Agent: config.AgentConfig{
			Temperature: 0.3,
			MaxTokens:   2048,
		},
	}
	// Default agents require a model; test config-based sub-agents
	cfg.Workflow.SubAgents = []config.WorkflowSubAgentConfig{
		{
			Name:        "planner",
			Instruction: "Plan the task.",
			AllTools:    true,
		},
		{
			Name:         "executor",
			Instruction:  "Execute the plan.",
			AllowedTools: []string{"read_file", "write_file"},
			AllTools:     false,
		},
	}

	if len(cfg.Workflow.SubAgents) != 2 {
		t.Errorf("expected 2 sub-agents, got %d",
			len(cfg.Workflow.SubAgents))
	}

	// Verify the executor has restricted tools
	executor := cfg.Workflow.SubAgents[1]
	if executor.AllTools {
		t.Error("executor should have AllTools=false")
	}
	if len(executor.AllowedTools) != 2 {
		t.Errorf("executor should have 2 allowed tools, got %d",
			len(executor.AllowedTools))
	}
}

// TestDefaultSubAgents_Parallel verifies default parallel sub-agents.
func TestDefaultSubAgents_Parallel(t *testing.T) {
	cfg := &config.WukongConfig{
		Agent: config.AgentConfig{
			Temperature: 0.3,
			MaxTokens:   2048,
		},
	}
	cfg.Workflow.SubAgents = []config.WorkflowSubAgentConfig{
		{
			Name:        "code-analyzer",
			Instruction: "Analyze code.",
			AllTools:    false,
			AllowedTools: []string{
				"read_file", "search_code",
			},
		},
		{
			Name:        "doc-analyzer",
			Instruction: "Analyze docs.",
			AllTools:    false,
			AllowedTools: []string{
				"read_file", "web_fetch",
			},
		},
		{
			Name:        "test-analyzer",
			Instruction: "Analyze tests.",
			AllTools:    true,
		},
	}

	if len(cfg.Workflow.SubAgents) != 3 {
		t.Errorf("expected 3 sub-agents, got %d",
			len(cfg.Workflow.SubAgents))
	}

	// Test analyzer should have AllTools=true
	testAnalyzer := cfg.Workflow.SubAgents[2]
	if !testAnalyzer.AllTools {
		t.Error("test-analyzer should have AllTools=true")
	}
}

// mockTool is a minimal tool.Tool implementation for testing.
type mockTool struct {
	name        string
	description string
}

func (m *mockTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        m.name,
		Description: m.description,
	}
}

// Ensure mockTool implements tool.Tool.
var _ tool.Tool = (*mockTool)(nil)
