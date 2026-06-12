package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/km269/wukong/internal/config"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestIsCommandTool(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		expected bool
	}{
		{"bash", "bash", true},
		{"execute_command", "execute_command", true},
		{"run_command", "run_command", true},
		{"shell", "shell", true},
		{"terminal", "terminal", true},
		{"command", "command", true},
		{"command_execute", "command_execute", true},
		{"case insensitive", "BASH", true},
		{"ExEcUtE_CoMmAnD", "ExEcUtE_CoMmAnD", true},
		{"COMMAND_EXECUTE", "COMMAND_EXECUTE", true},
		{"non command tool", "read_file", false},
		{"empty", "", false},
		{"unknown", "unknown_tool", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isCommandTool(tt.toolName)
			if got != tt.expected {
				t.Errorf(
					"isCommandTool(%q) = %v, want %v",
					tt.toolName, got, tt.expected,
				)
			}
		})
	}
}

func TestExtractCommandFromArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]any
		expected string
	}{
		{
			name:     "extract command field",
			args:     map[string]any{"command": "ls -la"},
			expected: "ls -la",
		},
		{
			name:     "extract cmd field",
			args:     map[string]any{"cmd": "pwd"},
			expected: "pwd",
		},
		{
			name:     "extract shell field",
			args:     map[string]any{"shell": "echo hello"},
			expected: "echo hello",
		},
		{
			name:     "extract script field",
			args:     map[string]any{"script": "npm install"},
			expected: "npm install",
		},
		{
			name:     "priority: command over cmd",
			args:     map[string]any{"command": "first", "cmd": "second"},
			expected: "first",
		},
		{
			name:     "non-string value skipped",
			args:     map[string]any{"command": 12345},
			expected: "",
		},
		{
			name:     "no command fields",
			args:     map[string]any{"url": "https://example.com"},
			expected: "",
		},
		{
			name:     "empty args",
			args:     map[string]any{},
			expected: "",
		},
		{
			name:     "nil args",
			args:     nil,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rawArgs []byte
			if tt.args != nil {
				var err error
				rawArgs, err = json.Marshal(tt.args)
				if err != nil {
					t.Fatalf("failed to marshal: %v", err)
				}
			}
			got := extractCommandFromArgs(rawArgs)
			if got != tt.expected {
				t.Errorf(
					"extractCommandFromArgs() = %q, want %q",
					got, tt.expected,
				)
			}
		})
	}
}

func TestBuildSystemInstruction_WithTopOfMind(t *testing.T) {
	cfg := &config.WukongConfig{}
	instruction := buildSystemInstruction(cfg, "Always use Chinese.")
	if instruction == "" {
		t.Error("expected non-empty instruction with top of mind")
	}
	if !contains(instruction, "Always use Chinese.") {
		t.Error("instruction should contain top of mind text")
	}
	if !contains(instruction, "Wukong") {
		t.Error("instruction should contain Wukong")
	}
}

func TestBuildSystemInstruction_WithoutTopOfMind(t *testing.T) {
	cfg := &config.WukongConfig{}
	instruction := buildSystemInstruction(cfg, "")
	if instruction == "" {
		t.Error("expected non-empty instruction")
	}
	if contains(instruction, "Always use Chinese.") {
		t.Error(
			"instruction should not contain unset top of mind text",
		)
	}
}

func TestExtractMessageContent_Multipart(t *testing.T) {
	text := "hello from part"
	msg := model.Message{
		ContentParts: []model.ContentPart{
			{Text: &text},
		},
	}
	result := extractMessageContent(msg)
	if result != "hello from part" {
		t.Errorf(
			"expected 'hello from part', got '%s'", result,
		)
	}
}

func TestExtractMessageContent_Empty(t *testing.T) {
	msg := model.Message{}
	result := extractMessageContent(msg)
	if result != "" {
		t.Errorf("expected empty string, got '%s'", result)
	}
}

func TestExtractMessageContent_PartWithNilText(t *testing.T) {
	msg := model.Message{
		ContentParts: []model.ContentPart{
			{Text: nil},
		},
	}
	result := extractMessageContent(msg)
	if result != "" {
		t.Errorf("expected empty string, got '%s'", result)
	}
}

func TestExtractMessageContent_MultipleParts(t *testing.T) {
	t1 := "hello"
	t2 := "world"
	t3 := ""
	msg := model.Message{
		ContentParts: []model.ContentPart{
			{Text: &t1},
			{Text: &t2},
			{Text: &t3},
		},
	}
	result := extractMessageContent(msg)
	if result != "hello\nworld" {
		t.Errorf(
			"expected 'hello\\nworld', got '%s'", result,
		)
	}
}

func TestExtractMessageContent_MixedParts(t *testing.T) {
	t1 := "first"
	msg := model.Message{
		ContentParts: []model.ContentPart{
			{Text: &t1},
			{Text: nil},
			{Text: nil},
		},
	}
	result := extractMessageContent(msg)
	if result != "first" {
		t.Errorf("expected 'first', got '%s'", result)
	}
}

// TestCoreLoop_RunWhenClosed verifies that Run returns an error
// after the loop is closed.
func TestCoreLoop_RunWhenClosed(t *testing.T) {
	cfg := &config.WukongConfig{
		Agent: config.AgentConfig{
			MaxLLMCalls:       50,
			MaxToolIterations: 30,
		},
	}
	// Create a loop with minimal config. Since we don't have a real
	// model, the loop creation will fail, but we can test the closed
	// state logic by manually setting the closed flag.
	loop := &CoreLoop{
		cfg:    cfg,
		closed: true,
	}

	msg := model.NewUserMessage("test")
	_, err := loop.Run(
		context.Background(), "user1", "session1", msg,
	)
	if err == nil {
		t.Error("expected error when running on closed loop")
	}
}

// TestCoreLoop_CloseIsIdempotent verifies that calling Close multiple
// times does not panic.
func TestCoreLoop_CloseIsIdempotent(t *testing.T) {
	loop := &CoreLoop{
		cfg: &config.WukongConfig{},
	}
	// First close
	err := loop.Close()
	if err != nil {
		t.Errorf("first close should succeed: %v", err)
	}
	// Second close should be a no-op
	err = loop.Close()
	if err != nil {
		t.Errorf("second close should succeed: %v", err)
	}
	// Third close also no-op
	err = loop.Close()
	if err != nil {
		t.Errorf("third close should succeed: %v", err)
	}
}
