package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ============================================================================
// normalizeRecipeRef tests
// ============================================================================

func TestNormalizeRecipeRef_Prefixed(t *testing.T) {
	names := map[string]bool{"reviewer": true}
	got := normalizeRecipeRef("recipe-reviewer", names)
	if got != "reviewer" {
		t.Errorf("expected 'reviewer', got %q", got)
	}
}

func TestNormalizeRecipeRef_Bare(t *testing.T) {
	names := map[string]bool{"reviewer": true}
	got := normalizeRecipeRef("reviewer", names)
	if got != "reviewer" {
		t.Errorf("expected 'reviewer', got %q", got)
	}
}

func TestNormalizeRecipeRef_NonRecipe(t *testing.T) {
	names := map[string]bool{"reviewer": true}
	got := normalizeRecipeRef("file_read", names)
	if got != "" {
		t.Errorf("expected empty for non-recipe, got %q", got)
	}
}

func TestNormalizeRecipeRef_SelfReference(t *testing.T) {
	names := map[string]bool{"reviewer": true}
	got := normalizeRecipeRef("reviewer", names)
	if got != "reviewer" {
		t.Errorf("expected 'reviewer', got %q", got)
	}
}

// ============================================================================
// recipeDependencies tests
// ============================================================================

func TestRecipeDependencies_DirectRef(t *testing.T) {
	names := map[string]bool{"sub": true, "parent": true}
	recipe := &RecipeConfig{
		Name:  "parent",
		Tools: []string{"file_read", "recipe-sub"},
	}
	deps := recipeDependencies(recipe, names)
	if len(deps) != 1 || deps[0] != "sub" {
		t.Errorf("expected [sub], got %v", deps)
	}
}

func TestRecipeDependencies_BareRef(t *testing.T) {
	names := map[string]bool{"sub": true, "parent": true}
	recipe := &RecipeConfig{
		Name:  "parent",
		Tools: []string{"sub", "file_read"},
	}
	deps := recipeDependencies(recipe, names)
	if len(deps) != 1 || deps[0] != "sub" {
		t.Errorf("expected [sub], got %v", deps)
	}
}

func TestRecipeDependencies_NoDeps(t *testing.T) {
	names := map[string]bool{"solo": true}
	recipe := &RecipeConfig{
		Name:  "solo",
		Tools: []string{"file_read", "code_search"},
	}
	deps := recipeDependencies(recipe, names)
	if len(deps) != 0 {
		t.Errorf("expected no deps, got %v", deps)
	}
}

func TestRecipeDependencies_SelfRefExcluded(t *testing.T) {
	names := map[string]bool{"solo": true}
	recipe := &RecipeConfig{
		Name:  "solo",
		Tools: []string{"solo", "file_read"},
	}
	deps := recipeDependencies(recipe, names)
	if len(deps) != 0 {
		t.Errorf("self-reference should be excluded, got %v", deps)
	}
}

// ============================================================================
// topoSortRecipes tests
// ============================================================================

func TestTopoSort_NoDeps(t *testing.T) {
	recipes := map[string]*RecipeConfig{
		"a": {Name: "a"},
		"b": {Name: "b"},
		"c": {Name: "c"},
	}
	order, err := topoSortRecipes(recipes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 3 {
		t.Fatalf("expected 3 items, got %d", len(order))
	}
}

func TestTopoSort_LinearChain(t *testing.T) {
	recipes := map[string]*RecipeConfig{
		"a": {Name: "a", Tools: []string{"recipe-b"}},
		"b": {Name: "b", Tools: []string{"recipe-c"}},
		"c": {Name: "c"},
	}
	order, err := topoSortRecipes(recipes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// c must come before b, b before a.
	idxC := indexOf(order, "c")
	idxB := indexOf(order, "b")
	idxA := indexOf(order, "a")
	if idxC >= idxB {
		t.Errorf("c should come before b: order=%v", order)
	}
	if idxB >= idxA {
		t.Errorf("b should come before a: order=%v", order)
	}
}

func TestTopoSort_CircularDetected(t *testing.T) {
	recipes := map[string]*RecipeConfig{
		"a": {Name: "a", Tools: []string{"recipe-b"}},
		"b": {Name: "b", Tools: []string{"recipe-a"}},
	}
	_, err := topoSortRecipes(recipes)
	if err == nil {
		t.Fatal("expected circular dependency error")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("error should mention circular, got: %v", err)
	}
}

func TestTopoSort_DiamondDeps(t *testing.T) {
	// a depends on b and c; b and c both depend on d.
	recipes := map[string]*RecipeConfig{
		"a": {Name: "a", Tools: []string{"recipe-b", "recipe-c"}},
		"b": {Name: "b", Tools: []string{"recipe-d"}},
		"c": {Name: "c", Tools: []string{"recipe-d"}},
		"d": {Name: "d"},
	}
	order, err := topoSortRecipes(recipes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	idxD := indexOf(order, "d")
	idxB := indexOf(order, "b")
	idxC := indexOf(order, "c")
	idxA := indexOf(order, "a")
	if idxD >= idxB || idxD >= idxC {
		t.Errorf("d should come before b and c: order=%v", order)
	}
	if idxB >= idxA || idxC >= idxA {
		t.Errorf("b and c should come before a: order=%v", order)
	}
}

func indexOf(slice []string, val string) int {
	for i, v := range slice {
		if v == val {
			return i
		}
	}
	return -1
}

// ============================================================================
// Extends resolution tests
// ============================================================================

func TestResolveExtends_Simple(t *testing.T) {
	recipes := map[string]*RecipeConfig{
		"base": {
			Name:        "base",
			Description: "Base recipe",
			Instruction: "Base instruction",
			Temperature: 0.5,
		},
		"child": {
			Name:        "child",
			Extends:     "base",
			Description: "Child override",
		},
	}
	resolved, err := resolveAllExtends(recipes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	child := resolved["child"]
	if child.Description != "Child override" {
		t.Errorf("expected child description override, got %q",
			child.Description)
	}
	if child.Instruction != "Base instruction" {
		t.Errorf("expected inherited instruction, got %q",
			child.Instruction)
	}
	if child.Temperature != 0.5 {
		t.Errorf("expected inherited temperature 0.5, got %v",
			child.Temperature)
	}
	if child.Extends != "" {
		t.Errorf("extends should be cleared after resolution, got %q",
			child.Extends)
	}
}

func TestResolveExtends_Chain(t *testing.T) {
	recipes := map[string]*RecipeConfig{
		"grandparent": {
			Name:        "grandparent",
			Instruction: "GP instruction",
			MaxTokens:   2048,
		},
		"parent": {
			Name:    "parent",
			Extends: "grandparent",
		},
		"child": {
			Name:      "child",
			Extends:   "parent",
			MaxTokens: 4096, // override
		},
	}
	resolved, err := resolveAllExtends(recipes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	child := resolved["child"]
	if child.Instruction != "GP instruction" {
		t.Errorf("expected inherited from grandparent, got %q",
			child.Instruction)
	}
	if child.MaxTokens != 4096 {
		t.Errorf("expected overridden 4096, got %d",
			child.MaxTokens)
	}
}

func TestResolveExtends_Circular(t *testing.T) {
	recipes := map[string]*RecipeConfig{
		"a": {Name: "a", Extends: "b"},
		"b": {Name: "b", Extends: "a"},
	}
	_, err := resolveAllExtends(recipes)
	if err == nil {
		t.Fatal("expected circular extends error")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("error should mention circular, got: %v", err)
	}
}

func TestResolveExtends_NotFound(t *testing.T) {
	recipes := map[string]*RecipeConfig{
		"child": {Name: "child", Extends: "nonexistent"},
	}
	_, err := resolveAllExtends(recipes)
	if err == nil {
		t.Fatal("expected not found error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention not found, got: %v", err)
	}
}

func TestMergeRecipes_Overrides(t *testing.T) {
	parent := &RecipeConfig{
		Name:        "base",
		Description: "Parent desc",
		Instruction: "Parent instruction",
		Tools:       []string{"file_read"},
		Temperature: 0.3,
	}
	child := &RecipeConfig{
		Name:        "child",
		Description: "Child desc",
		Temperature: 0.7,
	}
	merged := mergeRecipes(parent, child)
	if merged.Name != "child" {
		t.Errorf("expected child name, got %q", merged.Name)
	}
	if merged.Description != "Child desc" {
		t.Errorf("expected child desc, got %q",
			merged.Description)
	}
	if merged.Instruction != "Parent instruction" {
		t.Errorf("expected parent instruction, got %q",
			merged.Instruction)
	}
	if merged.Temperature != 0.7 {
		t.Errorf("expected child temp 0.7, got %v",
			merged.Temperature)
	}
	if len(merged.Tools) != 1 || merged.Tools[0] != "file_read" {
		t.Errorf("expected inherited tools, got %v", merged.Tools)
	}
}

func TestCloneRecipe(t *testing.T) {
	original := &RecipeConfig{
		Name:        "test",
		Description: "desc",
		Tools:       []string{"a", "b"},
		Parameters: []RecipeParameter{
			{Key: "x", Type: "string"},
		},
		Response: &RecipeResponseConfig{
			Strict: true,
			JSONSchema: map[string]any{
				"type": "object",
			},
		},
	}
	clone := cloneRecipe(original)
	if clone.Name != original.Name {
		t.Error("clone should have same name")
	}
	// Modify clone, original should be unaffected.
	clone.Tools[0] = "z"
	if original.Tools[0] == "z" {
		t.Error("original should not be affected by clone modification")
	}
	clone.Response.Strict = false
	if original.Response.Strict == false {
		t.Error("original response should not be affected")
	}
}

// ============================================================================
// Retry tests
// ============================================================================

func TestResolveRetryConfig_Defaults(t *testing.T) {
	cfg := &RecipeRetryConfig{}
	r := resolveRetryConfig(cfg)
	if r.maxAttempts != defaultRetryMaxAttempts {
		t.Errorf("expected default max attempts %d, got %d",
			defaultRetryMaxAttempts, r.maxAttempts)
	}
	if r.initialWait != defaultRetryInitialWait {
		t.Errorf("expected default initial wait %v, got %v",
			defaultRetryInitialWait, r.initialWait)
	}
	if r.backoffFactor != defaultRetryBackoffFactor {
		t.Errorf("expected default backoff %.1f, got %.1f",
			defaultRetryBackoffFactor, r.backoffFactor)
	}
}

func TestResolveRetryConfig_Custom(t *testing.T) {
	cfg := &RecipeRetryConfig{
		MaxAttempts:   5,
		InitialWait:   "500ms",
		BackoffFactor: 3.0,
		MaxWait:       "10s",
	}
	r := resolveRetryConfig(cfg)
	if r.maxAttempts != 5 {
		t.Errorf("expected 5, got %d", r.maxAttempts)
	}
	if r.initialWait != 500*time.Millisecond {
		t.Errorf("expected 500ms, got %v", r.initialWait)
	}
	if r.backoffFactor != 3.0 {
		t.Errorf("expected 3.0, got %v", r.backoffFactor)
	}
	if r.maxWait != 10*time.Second {
		t.Errorf("expected 10s, got %v", r.maxWait)
	}
}

func TestRetryComputeDelay(t *testing.T) {
	r := &resolvedRetry{
		initialWait:   1 * time.Second,
		backoffFactor: 2.0,
		maxWait:       10 * time.Second,
	}
	// attempt 1: no delay.
	if d := r.computeDelay(1); d != 0 {
		t.Errorf("expected 0 delay for attempt 1, got %v", d)
	}
	// attempt 2: initialWait.
	if d := r.computeDelay(2); d != 1*time.Second {
		t.Errorf("expected 1s for attempt 2, got %v", d)
	}
	// attempt 3: initialWait * factor = 2s.
	if d := r.computeDelay(3); d != 2*time.Second {
		t.Errorf("expected 2s for attempt 3, got %v", d)
	}
	// attempt 4: initialWait * factor^2 = 4s.
	if d := r.computeDelay(4); d != 4*time.Second {
		t.Errorf("expected 4s for attempt 4, got %v", d)
	}
}

func TestRetryComputeDelay_MaxCap(t *testing.T) {
	r := &resolvedRetry{
		initialWait:   1 * time.Second,
		backoffFactor: 10.0,
		maxWait:       5 * time.Second,
	}
	// attempt 4: 1 * 10^2 = 100s, capped at 5s.
	if d := r.computeDelay(4); d != 5*time.Second {
		t.Errorf("expected 5s (capped), got %v", d)
	}
}

// callCountingTool is a test double that counts calls and returns
// configurable results per attempt.
type callCountingTool struct {
	decl    *tool.Declaration
	results []callOutcome
	calls   int
}

type callOutcome struct {
	result any
	err    error
}

func (c *callCountingTool) Declaration() *tool.Declaration {
	return c.decl
}

func (c *callCountingTool) Call(
	_ context.Context,
	_ []byte,
) (any, error) {
	idx := c.calls
	c.calls++
	if idx < len(c.results) {
		return c.results[idx].result, c.results[idx].err
	}
	return nil, errors.New("no more results")
}

func TestRetryTool_SuccessOnFirstTry(t *testing.T) {
	inner := &callCountingTool{
		decl: &tool.Declaration{Name: "test"},
		results: []callOutcome{
			{result: "ok", err: nil},
		},
	}
	rt := newRetryTool(inner, &RecipeRetryConfig{
		MaxAttempts: 3,
	}, nil)

	result, err := rt.Call(context.Background(), []byte("{}"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Errorf("expected 'ok', got %v", result)
	}
	if inner.calls != 1 {
		t.Errorf("expected 1 call, got %d", inner.calls)
	}
}

func TestRetryTool_RetriesOnError(t *testing.T) {
	inner := &callCountingTool{
		decl: &tool.Declaration{Name: "test"},
		results: []callOutcome{
			{result: nil, err: errors.New("fail")},
			{result: nil, err: errors.New("fail")},
			{result: "success", err: nil},
		},
	}
	rt := newRetryTool(inner, &RecipeRetryConfig{
		MaxAttempts:   3,
		InitialWait:   "1ms",
		BackoffFactor: 1.0,
	}, nil)

	result, err := rt.Call(context.Background(), []byte("{}"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "success" {
		t.Errorf("expected 'success', got %v", result)
	}
	if inner.calls != 3 {
		t.Errorf("expected 3 calls, got %d", inner.calls)
	}
}

func TestRetryTool_ExhaustsAttempts(t *testing.T) {
	inner := &callCountingTool{
		decl: &tool.Declaration{Name: "test"},
		results: []callOutcome{
			{result: nil, err: errors.New("fail")},
			{result: nil, err: errors.New("fail")},
			{result: nil, err: errors.New("fail")},
		},
	}
	rt := newRetryTool(inner, &RecipeRetryConfig{
		MaxAttempts:   3,
		InitialWait:   "1ms",
		BackoffFactor: 1.0,
	}, nil)

	_, err := rt.Call(context.Background(), []byte("{}"))
	if err == nil {
		t.Fatal("expected error after exhausting attempts")
	}
	if !strings.Contains(err.Error(), "failed after 3") {
		t.Errorf("error should mention 3 attempts, got: %v", err)
	}
	if inner.calls != 3 {
		t.Errorf("expected 3 calls, got %d", inner.calls)
	}
}

func TestRetryTool_ValidationFailureRetries(t *testing.T) {
	inner := &callCountingTool{
		decl: &tool.Declaration{Name: "test"},
		results: []callOutcome{
			{result: "not json", err: nil},
			{result: `{"summary":"ok"}`, err: nil},
		},
	}
	validator := buildOutputValidator(&RecipeResponseConfig{
		JSONSchema: map[string]any{
			"type":     "object",
			"required": []any{"summary"},
		},
	})
	rt := newRetryTool(inner, &RecipeRetryConfig{
		MaxAttempts:   3,
		InitialWait:   "1ms",
		BackoffFactor: 1.0,
	}, validator)

	result, err := rt.Call(context.Background(), []byte("{}"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inner.calls != 2 {
		t.Errorf("expected 2 calls (1st invalid, 2nd valid), got %d",
			inner.calls)
	}
	if result != `{"summary":"ok"}` {
		t.Errorf("expected valid JSON result, got %v", result)
	}
}

func TestRetryTool_ContextCancel(t *testing.T) {
	inner := &callCountingTool{
		decl: &tool.Declaration{Name: "test"},
		results: []callOutcome{
			{result: nil, err: errors.New("fail")},
		},
	}
	rt := newRetryTool(inner, &RecipeRetryConfig{
		MaxAttempts:   5,
		InitialWait:   "10s",
		BackoffFactor: 1.0,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err := rt.Call(ctx, []byte("{}"))
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

// ============================================================================
// Output validator tests
// ============================================================================

func TestBuildOutputValidator_NilResponse(t *testing.T) {
	v := buildOutputValidator(nil)
	if v != nil {
		t.Error("expected nil validator for nil response")
	}
}

func TestBuildOutputValidator_EmptySchema(t *testing.T) {
	v := buildOutputValidator(&RecipeResponseConfig{})
	if v != nil {
		t.Error("expected nil validator for empty schema")
	}
}

func TestOutputValidator_ValidJSON(t *testing.T) {
	resp := &RecipeResponseConfig{
		JSONSchema: map[string]any{
			"type":     "object",
			"required": []any{"summary"},
		},
	}
	v := buildOutputValidator(resp)
	if v == nil {
		t.Fatal("expected non-nil validator")
	}
	err := v(`{"summary":"ok","issues":[]}`)
	if err != nil {
		t.Errorf("expected no error for valid JSON, got: %v", err)
	}
}

func TestOutputValidator_InvalidJSON(t *testing.T) {
	resp := &RecipeResponseConfig{
		JSONSchema: map[string]any{
			"type":     "object",
			"required": []any{"summary"},
		},
	}
	v := buildOutputValidator(resp)
	err := v("not json at all")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("error should mention invalid JSON, got: %v", err)
	}
}

func TestOutputValidator_MissingRequired(t *testing.T) {
	resp := &RecipeResponseConfig{
		JSONSchema: map[string]any{
			"type":     "object",
			"required": []any{"summary", "issues"},
		},
	}
	v := buildOutputValidator(resp)
	err := v(`{"summary":"ok"}`)
	if err == nil {
		t.Fatal("expected error for missing required field")
	}
	if !strings.Contains(err.Error(), "issues") {
		t.Errorf("error should mention missing field, got: %v", err)
	}
}

func TestOutputValidator_AcceptsByteSlice(t *testing.T) {
	resp := &RecipeResponseConfig{
		JSONSchema: map[string]any{
			"type":     "object",
			"required": []any{"x"},
		},
	}
	v := buildOutputValidator(resp)
	err := v([]byte(`{"x":1}`))
	if err != nil {
		t.Errorf("expected no error for []byte input, got: %v", err)
	}
}

func TestOutputValidator_AcceptsStruct(t *testing.T) {
	resp := &RecipeResponseConfig{
		JSONSchema: map[string]any{
			"type":     "object",
			"required": []any{"x"},
		},
	}
	v := buildOutputValidator(resp)
	err := v(map[string]any{"x": 1})
	if err != nil {
		t.Errorf("expected no error for map input, got: %v", err)
	}
}

func TestMarshalResult_String(t *testing.T) {
	b, err := marshalResult("hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(b) != "hello" {
		t.Errorf("expected 'hello', got %q", string(b))
	}
}

func TestMarshalResult_ByteSlice(t *testing.T) {
	b, err := marshalResult([]byte("world"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(b) != "world" {
		t.Errorf("expected 'world', got %q", string(b))
	}
}

func TestMarshalResult_Struct(t *testing.T) {
	b, err := marshalResult(map[string]int{"x": 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]int
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if m["x"] != 1 {
		t.Errorf("expected x=1, got %d", m["x"])
	}
}

// ============================================================================
// Inline recipe loading tests
// ============================================================================

func TestLoadInlineRecipe_Valid(t *testing.T) {
	raw := map[string]any{
		"name":        "inline-helper",
		"description": "Inline helper",
		"instruction": "You are a helper.",
		"tools":       []any{"file_read"},
		"temperature": 0.5,
	}
	recipe, err := loadInlineRecipe(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recipe.Name != "inline-helper" {
		t.Errorf("expected 'inline-helper', got %q", recipe.Name)
	}
	if recipe.Description != "Inline helper" {
		t.Errorf("unexpected description: %q", recipe.Description)
	}
	if recipe.Temperature != 0.5 {
		t.Errorf("expected temp 0.5, got %v", recipe.Temperature)
	}
}

func TestLoadInlineRecipe_WithParameters(t *testing.T) {
	raw := map[string]any{
		"name":   "param-recipe",
		"prompt": "Review {{.lang}}",
		"parameters": []any{
			map[string]any{
				"key":      "lang",
				"type":     "select",
				"required": true,
				"options":  []any{"go", "python"},
			},
		},
	}
	recipe, err := loadInlineRecipe(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recipe.Prompt != "Review {{.lang}}" {
		t.Errorf("unexpected prompt: %q", recipe.Prompt)
	}
	if len(recipe.Parameters) != 1 {
		t.Fatalf("expected 1 parameter, got %d",
			len(recipe.Parameters))
	}
	if recipe.Parameters[0].Key != "lang" {
		t.Errorf("expected key 'lang', got %q",
			recipe.Parameters[0].Key)
	}
}

func TestLoadInlineRecipes_Multiple(t *testing.T) {
	recipes := make(map[string]*RecipeConfig)
	inline := []map[string]any{
		{"name": "a", "description": "A"},
		{"name": "b", "description": "B"},
	}
	loadInlineRecipes(recipes, inline)
	if len(recipes) != 2 {
		t.Fatalf("expected 2 recipes, got %d", len(recipes))
	}
	if recipes["a"] == nil {
		t.Error("expected recipe 'a'")
	}
	if recipes["b"] == nil {
		t.Error("expected recipe 'b'")
	}
}

func TestLoadInlineRecipes_SkipsUnnamed(t *testing.T) {
	recipes := make(map[string]*RecipeConfig)
	inline := []map[string]any{
		{"description": "no name"},
		{"name": "valid", "description": "valid"},
	}
	loadInlineRecipes(recipes, inline)
	if len(recipes) != 1 {
		t.Fatalf("expected 1 recipe (unnamed skipped), got %d",
			len(recipes))
	}
	if recipes["valid"] == nil {
		t.Error("expected 'valid' recipe")
	}
}

// ============================================================================
// mergeToolSets tests
// ============================================================================

func TestMergeToolSets_BaseOnly(t *testing.T) {
	baseTools := map[string]tool.Tool{
		"file_read":  &callCountingTool{decl: &tool.Declaration{Name: "file_read"}},
		"file_write": &callCountingTool{decl: &tool.Declaration{Name: "file_write"}},
	}
	recipe := &RecipeConfig{
		Name:  "test",
		Tools: []string{"file_read"},
	}
	recipeNames := map[string]bool{"test": true}

	result := mergeToolSets(baseTools, nil, recipe, recipeNames)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result))
	}
	if _, ok := result["file_read"]; !ok {
		t.Error("expected file_read in result")
	}
}

func TestMergeToolSets_WithRecipeRef(t *testing.T) {
	baseTools := map[string]tool.Tool{
		"file_read": &callCountingTool{decl: &tool.Declaration{Name: "file_read"}},
	}
	subRecipeTool := &callCountingTool{
		decl: &tool.Declaration{Name: "recipe-sub"},
	}
	recipeRegistry := map[string]tool.Tool{
		"sub": subRecipeTool,
	}
	recipe := &RecipeConfig{
		Name:  "parent",
		Tools: []string{"file_read", "recipe-sub"},
	}
	recipeNames := map[string]bool{"parent": true, "sub": true}

	result := mergeToolSets(baseTools, recipeRegistry, recipe, recipeNames)
	if len(result) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(result))
	}
	if _, ok := result["file_read"]; !ok {
		t.Error("expected file_read in result")
	}
	if _, ok := result["recipe-sub"]; !ok {
		t.Error("expected recipe-sub in result")
	}
}

func TestMergeToolSets_BareRecipeRef(t *testing.T) {
	subRecipeTool := &callCountingTool{
		decl: &tool.Declaration{Name: "recipe-sub"},
	}
	recipeRegistry := map[string]tool.Tool{
		"sub": subRecipeTool,
	}
	recipe := &RecipeConfig{
		Name:  "parent",
		Tools: []string{"sub"},
	}
	recipeNames := map[string]bool{"parent": true, "sub": true}

	result := mergeToolSets(nil, recipeRegistry, recipe, recipeNames)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result))
	}
	if _, ok := result["sub"]; !ok {
		t.Error("expected 'sub' (bare ref) in result")
	}
}

func TestMergeToolSets_EmptyTools(t *testing.T) {
	baseTools := map[string]tool.Tool{
		"file_read": &callCountingTool{decl: &tool.Declaration{Name: "file_read"}},
	}
	recipe := &RecipeConfig{
		Name:  "test",
		Tools: []string{},
	}
	result := mergeToolSets(baseTools, nil, recipe, nil)
	if len(result) != 0 {
		t.Errorf("expected 0 tools for empty Tools, got %d",
			len(result))
	}
}

// ============================================================================
// YAML parsing for new fields
// ============================================================================

func TestRecipeConfig_YAMLRetry(t *testing.T) {
	yamlContent := `
name: test
description: "Test"
instruction: "Test"
retry:
  max_attempts: 5
  initial_wait: "2s"
  backoff_factor: 3.0
  max_wait: "60s"
`
	var recipe RecipeConfig
	if err := yaml.Unmarshal([]byte(yamlContent), &recipe); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recipe.Retry == nil {
		t.Fatal("expected non-nil Retry")
	}
	if recipe.Retry.MaxAttempts != 5 {
		t.Errorf("expected 5, got %d", recipe.Retry.MaxAttempts)
	}
	if recipe.Retry.InitialWait != "2s" {
		t.Errorf("expected '2s', got %q", recipe.Retry.InitialWait)
	}
	if recipe.Retry.BackoffFactor != 3.0 {
		t.Errorf("expected 3.0, got %v", recipe.Retry.BackoffFactor)
	}
	if recipe.Retry.MaxWait != "60s" {
		t.Errorf("expected '60s', got %q", recipe.Retry.MaxWait)
	}
}

func TestRecipeConfig_YAMLExtends(t *testing.T) {
	yamlContent := `
name: child
extends: base
description: "Child override"
`
	var recipe RecipeConfig
	if err := yaml.Unmarshal([]byte(yamlContent), &recipe); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recipe.Extends != "base" {
		t.Errorf("expected 'base', got %q", recipe.Extends)
	}
}

func TestRecipeConfig_YAMLValidateOutput(t *testing.T) {
	yamlContent := `
name: test
response:
  json_schema:
    type: object
    required: [result]
  validate_output: true
`
	var recipe RecipeConfig
	if err := yaml.Unmarshal([]byte(yamlContent), &recipe); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recipe.Response == nil {
		t.Fatal("expected non-nil Response")
	}
	if !recipe.Response.ValidateOutput {
		t.Error("expected ValidateOutput=true")
	}
}

// ============================================================================
// sortStrings test
// ============================================================================

func TestSortStrings(t *testing.T) {
	s := []string{"c", "a", "b"}
	sortStrings(s)
	if s[0] != "a" || s[1] != "b" || s[2] != "c" {
		t.Errorf("expected [a b c], got %v", s)
	}
}
