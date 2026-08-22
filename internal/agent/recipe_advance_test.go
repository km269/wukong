package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ============================================================================
// P3-A: Model override tests
// ============================================================================

// stubModelFactory implements providerModelFactory for testing.
type stubModelFactory struct {
	defaultModel model.Model
	customModel  model.Model
	customErr    error
	createCalled string // last providerName passed to CreateModel
}

func (f *stubModelFactory) CreateModel(
	name string,
) (model.Model, error) {
	f.createCalled = name
	return nil, nil // not used in recipe tests
}

func (f *stubModelFactory) CreateModelWithName(
	providerName string,
	modelName string,
) (model.Model, error) {
	f.createCalled = providerName
	if f.customErr != nil {
		return nil, f.customErr
	}
	return f.customModel, nil
}

func (f *stubModelFactory) CreateDefaultModel() (model.Model, error) {
	return f.defaultModel, nil
}

// stubModel is a minimal model.Model implementation.
type stubModel struct{ name string }

func (m *stubModel) Info() model.Info { return model.Info{Name: m.name} }

func (m *stubModel) GenerateContent(
	_ context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	return nil, errors.New("not implemented")
}

func TestCreateRecipeModel_UsesDefault(t *testing.T) {
	factory := &stubModelFactory{
		defaultModel: &stubModel{name: "default"},
	}
	recipe := &RecipeConfig{Name: "test"}
	result := createRecipeModel(factory, recipe,
		factory.defaultModel)
	if result != factory.defaultModel {
		t.Error("expected default model for recipe without model field")
	}
}

func TestCreateRecipeModel_UsesOverride(t *testing.T) {
	factory := &stubModelFactory{
		defaultModel: &stubModel{name: "default"},
		customModel:  &stubModel{name: "gpt-4o"},
	}
	recipe := &RecipeConfig{Name: "test", Model: "gpt-4o"}
	result := createRecipeModel(factory, recipe,
		factory.defaultModel)
	if result != factory.customModel {
		t.Error("expected custom model for recipe with model field")
	}
}

func TestCreateRecipeModel_FallbackOnError(t *testing.T) {
	factory := &stubModelFactory{
		defaultModel: &stubModel{name: "default"},
		customErr:    errors.New("provider not found"),
	}
	recipe := &RecipeConfig{Name: "test", Model: "unknown-model"}
	result := createRecipeModel(factory, recipe,
		factory.defaultModel)
	if result != factory.defaultModel {
		t.Error("expected fallback to default on error")
	}
}

// ============================================================================
// P3-B: Timeout tool tests
// ============================================================================

func TestTimeoutTool_NoTimeoutReturnsInner(t *testing.T) {
	inner := &callCountingTool{
		decl:    &tool.Declaration{Name: "test"},
		results: []callOutcome{{result: "ok"}},
	}
	result := newTimeoutTool(inner, 0)
	if result != inner {
		t.Error("expected inner tool returned for zero timeout")
	}
}

func TestTimeoutTool_NegativeTimeoutReturnsInner(t *testing.T) {
	inner := &callCountingTool{
		decl:    &tool.Declaration{Name: "test"},
		results: []callOutcome{{result: "ok"}},
	}
	result := newTimeoutTool(inner, -1*time.Second)
	if result != inner {
		t.Error("expected inner tool returned for negative timeout")
	}
}

func TestTimeoutTool_SuccessfulCall(t *testing.T) {
	inner := &callCountingTool{
		decl:    &tool.Declaration{Name: "test"},
		results: []callOutcome{{result: "done", err: nil}},
	}
	tt := newTimeoutTool(inner, 5*time.Second)
	result, err := tt.Call(context.Background(), []byte("{}"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "done" {
		t.Errorf("expected 'done', got %v", result)
	}
	if inner.calls != 1 {
		t.Errorf("expected 1 call, got %d", inner.calls)
	}
}

func TestTimeoutTool_DeadlineExceeded(t *testing.T) {
	inner := &callCountingTool{
		decl: &tool.Declaration{Name: "test"},
		results: []callOutcome{{
			result: nil,
			err:    context.DeadlineExceeded,
		}},
	}
	// Use a real deadline that exceeds.
	tt := newTimeoutTool(inner, 1*time.Millisecond)
	ctx, cancel := context.WithTimeout(
		context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond)

	_, err := tt.Call(ctx, []byte("{}"))
	if err == nil {
		t.Fatal("expected deadline exceeded error")
	}
}

func TestTimeoutTool_PropagatesError(t *testing.T) {
	inner := &callCountingTool{
		decl:    &tool.Declaration{Name: "test"},
		results: []callOutcome{{result: nil, err: errors.New("boom")}},
	}
	tt := newTimeoutTool(inner, 5*time.Second)
	_, err := tt.Call(context.Background(), []byte("{}"))
	if err == nil || err.Error() != "boom" {
		t.Errorf("expected 'boom' error, got: %v", err)
	}
}

func TestTimeoutTool_DeclarationDelegates(t *testing.T) {
	inner := &callCountingTool{
		decl: &tool.Declaration{
			Name:        "my-tool",
			Description: "test tool",
		},
	}
	tt := newTimeoutTool(inner, 5*time.Second)
	if d := tt.Declaration(); d.Name != "my-tool" {
		t.Errorf("expected name 'my-tool', got %q", d.Name)
	}
}

// ============================================================================
// P3-C: Recipe discovery tool tests
// ============================================================================

func TestDiscoveryTool_ReturnsRecipes(t *testing.T) {
	recipes := map[string]*RecipeConfig{
		"reviewer": {
			Name:        "reviewer",
			Description: "Code reviewer",
			Tools:       []string{"file_read"},
			Temperature: 0.3,
			Parameters: []RecipeParameter{
				{
					Key:         "language",
					Description: "Language",
					Type:        "select",
					Required:    true,
					Default:     "",
					Options:     []string{"go", "python"},
				},
			},
		},
		"summarizer": {
			Name:        "summarizer",
			Description: "Summarizer",
		},
	}
	dt := newRecipeDiscoveryTool(recipes)
	result, err := dt.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var list []map[string]any
	if err := json.Unmarshal(
		[]byte(result.(string)), &list); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 recipes, got %d", len(list))
	}

	// Verify first recipe has parameters.
	var r1 map[string]any
	if list[0]["name"] == "reviewer" {
		r1 = list[0]
	} else {
		r1 = list[1]
	}
	params, ok := r1["parameters"].([]any)
	if !ok {
		t.Fatalf("expected parameters array, got %T",
			r1["parameters"])
	}
	if len(params) != 1 {
		t.Errorf("expected 1 parameter, got %d", len(params))
	}
}

func TestDiscoveryTool_EmptyRecipes(t *testing.T) {
	dt := newRecipeDiscoveryTool(map[string]*RecipeConfig{})
	result, err := dt.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var list []any
	if err := json.Unmarshal(
		[]byte(result.(string)), &list); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d", len(list))
	}
}

func TestDiscoveryTool_Declaration(t *testing.T) {
	dt := newRecipeDiscoveryTool(nil)
	d := dt.Declaration()
	if d.Name != "list_recipes" {
		t.Errorf("expected name 'list_recipes', got %q", d.Name)
	}
	if d.Description == "" {
		t.Error("expected non-empty description")
	}
}

// ============================================================================
// Reload tool tests
// ============================================================================

func TestReloadTool_CallsReloadFn(t *testing.T) {
	called := false
	rt := newReloadTool(func() string {
		called = true
		return "reloaded"
	})
	result, err := rt.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("expected reload function to be called")
	}
	if result != "reloaded" {
		t.Errorf("expected 'reloaded', got %v", result)
	}
}

func TestReloadTool_NilFnReturnsError(t *testing.T) {
	rt := newReloadTool(nil)
	_, err := rt.Call(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil reload function")
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReloadTool_Declaration(t *testing.T) {
	rt := newReloadTool(func() string { return "" })
	d := rt.Declaration()
	if d.Name != "reload_recipes" {
		t.Errorf("expected name 'reload_recipes', got %q", d.Name)
	}
}

// ============================================================================
// YAML parsing for P3 fields
// ============================================================================

func TestRecipeConfig_YAMLTimeout(t *testing.T) {
	yamlContent := `
name: test
description: "Test"
instruction: "Test"
timeout: "30s"
`
	var recipe RecipeConfig
	if err := yaml.Unmarshal([]byte(yamlContent), &recipe); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recipe.Timeout != "30s" {
		t.Errorf("expected '30s', got %q", recipe.Timeout)
	}
}

func TestRecipeConfig_YAMLModelField(t *testing.T) {
	yamlContent := `
name: test
description: "Test"
instruction: "Test"
model: "gpt-4o"
`
	var recipe RecipeConfig
	if err := yaml.Unmarshal([]byte(yamlContent), &recipe); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recipe.Model != "gpt-4o" {
		t.Errorf("expected 'gpt-4o', got %q", recipe.Model)
	}
}

// ============================================================================
// isYAMLFile test
// ============================================================================

func TestIsYAMLFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/path/to/recipe.yaml", true},
		{"/path/to/recipe.yml", true},
		{"/path/to/recipe.json", false},
		{"/path/to/file", false},
		{"a.yaml", true},
		{"a.yml", true},
	}
	for _, tc := range tests {
		got := isYAMLFile(tc.path)
		if got != tc.want {
			t.Errorf("isYAMLFile(%q) = %v, want %v",
				tc.path, got, tc.want)
		}
	}
}

// ============================================================================
// resolveRecipeDir test
// ============================================================================

func TestResolveRecipeDir_Default(t *testing.T) {
	dir := resolveRecipeDir("")
	if dir == "" {
		t.Error("expected non-empty default dir")
	}
	// Should end with recipes or recipes/
	if !strings.Contains(dir, "recipes") {
		t.Errorf("expected dir to contain 'recipes', got %q", dir)
	}
}

func TestResolveRecipeDir_Absolute(t *testing.T) {
	dir := resolveRecipeDir(".wukong/recipes/")
	if dir == "" {
		t.Error("expected non-empty dir")
	}
	if !strings.Contains(dir, "recipes") {
		t.Errorf("expected dir to contain 'recipes', got %q", dir)
	}
}
