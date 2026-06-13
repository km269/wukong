package agent

import (
	"testing"

	"github.com/km269/wukong/internal/config"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestContextRevisionEngine_New(t *testing.T) {
	cfg := &config.WukongConfig{
		Revision: config.RevisionConfig{
			Enabled:          true,
			MaxContextTokens: 64000,
			TrimRatio:        0.3,
			MaxCommandOutput: 8000,
			SearchStrategy:   "include_all",
		},
		Session: config.SessionConfig{
			EnableSummary:  true,
			SummaryTrigger: 50,
		},
	}

	engine := NewContextRevisionEngine(cfg)
	if engine == nil {
		t.Fatal("expected non-nil engine")
	}
	if engine.cfg != cfg {
		t.Error("engine config not set correctly")
	}
	if engine.maxRecent != 100 {
		t.Errorf("expected maxRecent=100, got %d", engine.maxRecent)
	}
}

func TestContextRevisionEngine_TruncateCommandOutput(t *testing.T) {
	cfg := &config.WukongConfig{
		Revision: config.RevisionConfig{
			MaxCommandOutput: 100,
		},
	}
	engine := NewContextRevisionEngine(cfg)

	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "short output unchanged",
			input:  "hello",
			expect: "hello",
		},
		{
			name:   "long output truncated",
			input:  string(make([]byte, 200)),
			expect: "", // Will contain truncation message
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := engine.TruncateCommandOutput(tt.input)
			if tt.name == "short output unchanged" {
				if result != tt.input {
					t.Errorf("expected unchanged, got %d chars", len(result))
				}
			}
			if tt.name == "long output truncated" {
				if len(result) >= len(tt.input) {
					t.Errorf("expected truncated output, got same length")
				}
			}
		})
	}
}

func TestContextRevisionEngine_FilterIrrelevant(t *testing.T) {
	cfg := &config.WukongConfig{}
	engine := NewContextRevisionEngine(cfg)

	// With <= 10 messages, should be unchanged
	short := []string{"a", "b", "c"}
	result := engine.FilterIrrelevant(short)
	if len(result) != len(short) {
		t.Errorf("short messages should be unchanged, got %d", len(result))
	}

	// With > 10 messages, should be summarized
	long := make([]string, 20)
	for i := range long {
		long[i] = "message"
	}
	result = engine.FilterIrrelevant(long)
	if len(result) >= len(long) {
		t.Errorf("long messages should be reduced, got %d", len(result))
	}
}

func TestContextRevisionEngine_Reset(t *testing.T) {
	cfg := &config.WukongConfig{
		Revision: config.RevisionConfig{
			Enabled: true,
		},
	}
	engine := NewContextRevisionEngine(cfg)
	engine.messageCount = 50
	engine.estimatedTokens = 5000

	engine.Reset()

	if engine.messageCount != 0 {
		t.Errorf("expected messageCount=0 after reset, got %d", engine.messageCount)
	}
	if engine.estimatedTokens != 0 {
		t.Errorf("expected estimatedTokens=0 after reset, got %d", engine.estimatedTokens)
	}
}

func TestContextRevisionEngine_ShouldSummarize(t *testing.T) {
	tests := []struct {
		name           string
		enableSummary  bool
		summaryTrigger int
		messageCount   int
		expected       bool
	}{
		{
			name:           "summary disabled",
			enableSummary:  false,
			summaryTrigger: 50,
			messageCount:   100,
			expected:       false,
		},
		{
			name:           "below trigger",
			enableSummary:  true,
			summaryTrigger: 50,
			messageCount:   30,
			expected:       false,
		},
		{
			name:           "at trigger",
			enableSummary:  true,
			summaryTrigger: 50,
			messageCount:   50,
			expected:       true,
		},
		{
			name:           "above trigger",
			enableSummary:  true,
			summaryTrigger: 50,
			messageCount:   100,
			expected:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.WukongConfig{
				Session: config.SessionConfig{
					EnableSummary:  tt.enableSummary,
					SummaryTrigger: tt.summaryTrigger,
				},
			}
			engine := NewContextRevisionEngine(cfg)
			engine.messageCount = tt.messageCount

			if got := engine.ShouldSummarize(); got != tt.expected {
				t.Errorf("ShouldSummarize() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestContextRevisionEngine_GetSearchStrategy(t *testing.T) {
	cfg := &config.WukongConfig{
		Revision: config.RevisionConfig{
			SearchStrategy: "semantic",
		},
	}
	engine := NewContextRevisionEngine(cfg)

	if got := engine.GetSearchStrategy(); got != "semantic" {
		t.Errorf("GetSearchStrategy() = %s, want semantic", got)
	}
}

func TestContextRevisionEngine_IsSemanticSearchEnabled(t *testing.T) {
	cfg := &config.WukongConfig{
		Revision: config.RevisionConfig{
			EnableSemanticSearch: true,
		},
	}
	engine := NewContextRevisionEngine(cfg)

	if !engine.IsSemanticSearchEnabled() {
		t.Error("expected semantic search enabled")
	}
}

func TestContextManager_Delegation(t *testing.T) {
	cfg := &config.WukongConfig{
		Revision: config.RevisionConfig{
			Enabled:          true,
			MaxContextTokens: 64000,
			TrimRatio:        0.3,
		},
	}
	mgr := NewContextManager(cfg)
	if mgr == nil {
		t.Fatal("expected non-nil context manager")
	}
	if mgr.engine == nil {
		t.Fatal("expected non-nil engine")
	}

	// Test delegation
	engine := mgr.GetEngine()
	if engine == nil {
		t.Fatal("expected non-nil engine from GetEngine")
	}

	mgr.Reset()
	if mgr.GetMessageCount() != 0 {
		t.Error("expected 0 messages after reset")
	}
}

func TestBuildSystemInstruction(t *testing.T) {
	cfg := &config.WukongConfig{}
	instruction := buildSystemInstruction(cfg, "", "")
	if instruction == "" {
		t.Error("expected non-empty system instruction")
	}
	// Should contain key phrases
	if !contains(instruction, "Wukong") {
		t.Error("instruction should mention Wukong")
	}
	if !contains(instruction, "tools") {
		t.Error("instruction should mention tools")
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestExtractMessageContent(t *testing.T) {
	// This is a basic test since model.Message is from tRPC
	// Test with empty content
	msg := model.Message{Content: "hello world"}
	result := extractMessageContent(msg)
	if result != "hello world" {
		t.Errorf("expected 'hello world', got '%s'", result)
	}
}
