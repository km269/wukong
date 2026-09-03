package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"text/template"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ============================================================================
// P4-A: Instruction templating tests
// ============================================================================

func TestNewRecipeTool_ParsesInstructionTemplate(t *testing.T) {
	recipe := &RecipeConfig{
		Name:        "test",
		Description: "Test",
		Prompt:      "Review {{.lang}} code",
		Instruction: "You are a {{.lang}} expert. Language: {{.lang}}",
		Parameters: []RecipeParameter{
			{Key: "lang", Type: "string", Required: true},
		},
	}
	inner := &stubInner{decl: &tool.Declaration{Name: "recipe-test"}}
	rt, err := newRecipeToolWithInner(inner, recipe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rt.instrTmpl == nil {
		t.Fatal("expected instruction template to be parsed")
	}
}

func TestNewRecipeTool_NoInstructionTemplate(t *testing.T) {
	recipe := &RecipeConfig{
		Name:        "test",
		Description: "Test",
		Prompt:      "Review {{.lang}} code",
		Instruction: "You are a code reviewer.",
		Parameters: []RecipeParameter{
			{Key: "lang", Type: "string", Required: true},
		},
	}
	inner := &stubInner{decl: &tool.Declaration{Name: "recipe-test"}}
	rt, err := newRecipeToolWithInner(inner, recipe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rt.instrTmpl != nil {
		t.Error("expected no instruction template for non-template instruction")
	}
}

func TestRecipeTool_CallRendersInstructionTemplate(t *testing.T) {
	recipe := &RecipeConfig{
		Name:        "test",
		Description: "Test",
		Prompt:      "Review the code.",
		Instruction: "You are a {{.lang}} expert. Focus on {{.focus}}.",
		Parameters: []RecipeParameter{
			{Key: "lang", Type: "string", Required: true},
			{Key: "focus", Type: "string", Required: false,
				Default: "all issues"},
		},
	}
	inner := &stubInner{
		decl:       &tool.Declaration{Name: "recipe-test"},
		callResult: "done",
	}
	rt, err := newRecipeToolWithInner(inner, recipe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	args, _ := json.Marshal(map[string]string{
		"lang": "go",
	})
	result, err := rt.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "done" {
		t.Errorf("expected 'done', got %v", result)
	}
	// Verify the forwarded message contains the rendered instruction.
	rendered := string(inner.lastCallArgs)
	if !strings.Contains(rendered, "[Context]") {
		t.Error("expected [Context] marker in forwarded message")
	}
	if !strings.Contains(rendered, "You are a go expert") {
		t.Error("expected rendered instruction in message")
	}
	if !strings.Contains(rendered, "all issues") {
		t.Error("expected default 'all issues' in rendered instruction")
	}
}

func TestNewRecipeTool_InvalidInstructionTemplate(t *testing.T) {
	recipe := &RecipeConfig{
		Name:        "test",
		Description: "Test",
		Prompt:      "Review {{.lang}} code",
		Instruction: "You are a {{ if .lang }} expert.", // malformed
		Parameters: []RecipeParameter{
			{Key: "lang", Type: "string", Required: true},
		},
	}
	inner := &stubInner{decl: &tool.Declaration{Name: "recipe-test"}}
	_, err := newRecipeToolWithInner(inner, recipe)
	if err == nil {
		t.Fatal("expected error for invalid instruction template")
	}
	if !strings.Contains(err.Error(), "parse instruction template") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ============================================================================
// P4-B: Metrics tests
// ============================================================================

func TestRecipeTool_MetricsTracksCalls(t *testing.T) {
	recipe := &RecipeConfig{
		Name:        "test",
		Description: "Test",
		Prompt:      "Review {{.code}}",
		Instruction: "You are a {{.role}}.",
		Parameters: []RecipeParameter{
			{Key: "code", Type: "string", Required: true},
			{Key: "role", Type: "string", Required: false,
				Default: "reviewer"},
		},
	}
	inner := &stubInner{
		decl:       &tool.Declaration{Name: "recipe-test"},
		callResult: "ok",
	}
	rt, err := newRecipeToolWithInner(inner, recipe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// First call.
	args1, _ := json.Marshal(map[string]string{"code": "x"})
	_, err1 := rt.Call(context.Background(), args1)
	if err1 != nil {
		t.Fatalf("unexpected error: %v", err1)
	}

	// Second call.
	args2, _ := json.Marshal(map[string]string{"code": "y"})
	_, err2 := rt.Call(context.Background(), args2)
	if err2 != nil {
		t.Fatalf("unexpected error: %v", err2)
	}

	m := rt.Metrics()
	if m.CallCount != 2 {
		t.Errorf("expected 2 calls, got %d", m.CallCount)
	}
	if m.SuccessCount != 2 {
		t.Errorf("expected 2 successes, got %d", m.SuccessCount)
	}
	if m.ErrorCount != 0 {
		t.Errorf("expected 0 errors, got %d", m.ErrorCount)
	}
	if m.LastCallAt.IsZero() {
		t.Error("expected non-zero last call time")
	}
	// Duration may be 0 in very fast test runs.
	if m.LastDuration < 0 {
		t.Errorf("expected non-negative last duration, got %v",
			m.LastDuration)
	}
}

func TestRecipeTool_MetricsTracksErrors(t *testing.T) {
	recipe := &RecipeConfig{
		Name:        "test",
		Description: "Test",
		Prompt:      "Review {{.code}}",
		Parameters: []RecipeParameter{
			{Key: "code", Type: "string", Required: true},
		},
	}
	inner := &stubInner{
		decl:    &tool.Declaration{Name: "recipe-test"},
		callErr: errors.New("inner boom"),
	}
	rt, err := newRecipeToolWithInner(inner, recipe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	args, _ := json.Marshal(map[string]string{"code": "x"})
	_, err = rt.Call(context.Background(), args)
	if err == nil {
		t.Fatal("expected error")
	}

	m := rt.Metrics()
	if m.CallCount != 1 {
		t.Errorf("expected 1 call, got %d", m.CallCount)
	}
	if m.ErrorCount != 1 {
		t.Errorf("expected 1 error, got %d", m.ErrorCount)
	}
	if m.SuccessCount != 0 {
		t.Errorf("expected 0 successes, got %d", m.SuccessCount)
	}
	if !strings.Contains(m.LastError, "inner boom") {
		t.Errorf("expected last error, got: %s", m.LastError)
	}
}

func TestRecipeTool_MetricsValidationError(t *testing.T) {
	recipe := &RecipeConfig{
		Name:        "test",
		Description: "Test",
		Prompt:      "Review {{.code}}",
		Parameters: []RecipeParameter{
			{Key: "code", Type: "string", Required: true},
		},
	}
	inner := &stubInner{
		decl: &tool.Declaration{Name: "recipe-test"},
	}
	rt, err := newRecipeToolWithInner(inner, recipe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Missing required parameter.
	_, err = rt.Call(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatal("expected validation error")
	}

	m := rt.Metrics()
	if m.CallCount != 1 {
		t.Errorf("expected 1 call, got %d", m.CallCount)
	}
	if m.ErrorCount != 1 {
		t.Errorf("expected 1 error, got %d", m.ErrorCount)
	}
	if m.SuccessCount != 0 {
		t.Errorf("expected 0 successes, got %d", m.SuccessCount)
	}
}

func TestRecipeTool_MetricsSnapshotIsSafe(t *testing.T) {
	recipe := &RecipeConfig{
		Name:        "test",
		Description: "Test",
		Prompt:      "Review {{.code}}",
		Parameters: []RecipeParameter{
			{Key: "code", Type: "string", Required: true},
		},
	}
	inner := &stubInner{
		decl:       &tool.Declaration{Name: "recipe-test"},
		callResult: "ok",
	}
	rt, err := newRecipeToolWithInner(inner, recipe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	args, _ := json.Marshal(map[string]string{"code": "x"})
	rt.Call(context.Background(), args)

	m1 := rt.Metrics()
	m1.CallCount = 999 // try to mutate

	m2 := rt.Metrics()
	if m2.CallCount != 1 {
		t.Errorf("snapshot should be independent, got %d",
			m2.CallCount)
	}
}

func TestRecipeMetrics_snapshot(t *testing.T) {
	now := time.Now()
	m := RecipeMetrics{
		CallCount:     5,
		SuccessCount:  3,
		ErrorCount:    2,
		RetryCount:    1,
		TotalDuration: 10 * time.Second,
		LastDuration:  2 * time.Second,
		LastError:     "boom",
		LastCallAt:    now,
	}
	s := m.snapshot()
	if s.CallCount != 5 || s.SuccessCount != 3 || s.ErrorCount != 2 {
		t.Error("snapshot should match source")
	}
	// Mutate original, snapshot should be unchanged.
	m.CallCount = 100
	if s.CallCount != 5 {
		t.Error("snapshot should be independent copy")
	}
}

// ============================================================================
// P4-C: Stats tool tests
// ============================================================================

func TestRecipeStatsTool_AllStats(t *testing.T) {
	// Clear registry for isolated test.
	globalMetricsRegistry.mu.Lock()
	globalMetricsRegistry.collectors = make(
		map[string]MetricsCollector)
	globalMetricsRegistry.mu.Unlock()

	// Register a test collector.
	collector := &testMetricsCollector{
		name: "reviewer",
		metrics: RecipeMetrics{
			CallCount:     3,
			SuccessCount:  2,
			ErrorCount:    1,
			LastDuration:  5 * time.Second,
			TotalDuration: 10 * time.Second,
			LastError:     "timeout",
		},
	}
	registerMetricsCollector(collector)

	st := newRecipeStatsTool()
	result, err := st.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var entries []map[string]any
	if err := json.Unmarshal(
		[]byte(result.(string)), &entries); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0]["name"] != "reviewer" {
		t.Errorf("expected 'reviewer', got %v", entries[0]["name"])
	}
	m := entries[0]["metrics"].(map[string]any)
	if m["call_count"] != float64(3) {
		t.Errorf("expected call_count=3, got %v", m["call_count"])
	}
}

func TestRecipeStatsTool_FilterByName(t *testing.T) {
	globalMetricsRegistry.mu.Lock()
	globalMetricsRegistry.collectors = make(
		map[string]MetricsCollector)
	collector := &testMetricsCollector{
		name: "reviewer",
		metrics: RecipeMetrics{
			CallCount:    3,
			SuccessCount: 2,
			ErrorCount:   1,
		},
	}
	globalMetricsRegistry.collectors["reviewer"] = collector
	globalMetricsRegistry.mu.Unlock()

	st := newRecipeStatsTool()
	args, _ := json.Marshal(
		map[string]string{"name": "reviewer"})
	result, err := st.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var entry map[string]any
	if err := json.Unmarshal(
		[]byte(result.(string)), &entry); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}
	if entry["name"] != "reviewer" {
		t.Errorf("expected 'reviewer', got %v", entry["name"])
	}
}

func TestRecipeStatsTool_NotFound(t *testing.T) {
	st := newRecipeStatsTool()
	args, _ := json.Marshal(
		map[string]string{"name": "nonexistent"})
	_, err := st.Call(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for unknown recipe")
	}
}

func TestRecipeStatsTool_Declaration(t *testing.T) {
	st := newRecipeStatsTool()
	d := st.Declaration()
	if d.Name != "recipe_stats" {
		t.Errorf("expected name 'recipe_stats', got %q", d.Name)
	}
}

// testMetricsCollector implements MetricsCollector for testing.
type testMetricsCollector struct {
	name    string
	metrics RecipeMetrics
}

func (c *testMetricsCollector) Name() string { return c.name }
func (c *testMetricsCollector) Metrics() RecipeMetrics {
	return c.metrics.snapshot()
}

// ============================================================================
// Template rendering with instruction
// ============================================================================

func TestRenderInstructionTemplate_Basic(t *testing.T) {
	tmpl := template.Must(
		template.New("instr").Parse(
			"You are a {{.lang}} expert."))
	result, err := renderPrompt(tmpl, map[string]string{
		"lang": "go",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "You are a go expert." {
		t.Errorf("unexpected result: %q", result)
	}
}

// ============================================================================
// Registry tests
// ============================================================================

func TestMetricsRegistryCollectAll(t *testing.T) {
	mr := &metricsRegistry{
		collectors: map[string]MetricsCollector{
			"a": &testMetricsCollector{
				name: "a",
				metrics: RecipeMetrics{
					CallCount: 5,
				},
			},
			"b": &testMetricsCollector{
				name: "b",
				metrics: RecipeMetrics{
					CallCount: 3,
				},
			},
		},
	}
	entries := mr.collectAllMetrics()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// Sorted by call count desc.
	if entries[0].Name != "a" {
		t.Errorf("expected 'a' (higher call count) first, got %q",
			entries[0].Name)
	}
}

func TestCollectAllMetrics_Empty(t *testing.T) {
	mr := &metricsRegistry{
		collectors: make(map[string]MetricsCollector),
	}
	entries := mr.collectAllMetrics()
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}
