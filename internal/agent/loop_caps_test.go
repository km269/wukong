package agent

import (
	"context"
	"testing"

	"github.com/km269/wukong/internal/capability"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestEffectiveToolSetsLegacy(t *testing.T) {
	legacy := []tool.ToolSet{&fakeLoopToolSet{name: "a"}}
	cfg := CoreLoopConfig{ToolSets: legacy}
	got := effectiveToolSets(cfg)
	if len(got) != 1 || got[0] != legacy[0] {
		t.Fatalf("legacy path changed: %#v", got)
	}
}

func TestEffectiveToolSetsRegistryReplaces(t *testing.T) {
	reg := capability.NewRegistry()
	legacy := []tool.ToolSet{
		&fakeLoopToolSet{name: "web"},
		&RecipeToolSet{},
	}
	cfg := CoreLoopConfig{ToolSets: legacy, Capabilities: reg}

	// Phase C: recipes are synced into the registry namespace by
	// SyncRecipeCapabilities (invoked from NewCoreLoop), so the
	// registry snapshot alone is the aggregation source.
	got := effectiveToolSets(cfg)
	if len(got) != 1 {
		t.Fatalf("toolset count = %d, want 1 (registry only)", len(got))
	}
	if got[0].Name() != "capability_registry" {
		t.Errorf("toolset = %v, want capability_registry", got[0].Name())
	}
}

func TestEffectiveToolSetsEmptyRegistry(t *testing.T) {
	cfg := CoreLoopConfig{
		ToolSets:     []tool.ToolSet{&fakeLoopToolSet{name: "web"}},
		Capabilities: capability.NewRegistry(),
	}
	got := effectiveToolSets(cfg)
	// Registry present → it is the source of truth even when empty;
	// the legacy toolsets are mirrored into it by bootstrap.
	if len(got) != 1 || got[0].Name() != "capability_registry" {
		t.Fatalf("got = %#v, want [capability_registry]", got)
	}
}

// fakeLoopToolSet is a minimal tool.ToolSet for aggregation tests.
type fakeLoopToolSet struct {
	name  string
	tools []tool.Tool
}

func (f *fakeLoopToolSet) Tools(context.Context) []tool.Tool {
	return f.tools
}

func (f *fakeLoopToolSet) Close() error { return nil }

func (f *fakeLoopToolSet) Name() string { return f.name }

func TestCommandToolNeedsValidation(t *testing.T) {
	// Hybrid mode ("" default): no registry → legacy heuristic.
	if !commandToolNeedsValidation(nil, "", "developer_command_execute") {
		t.Error("hybrid: developer_command_execute should validate")
	}
	if commandToolNeedsValidation(nil, "", "web_search") {
		t.Error("hybrid: web_search should not validate")
	}

	// Registry with declared scopes → declaration is authoritative.
	reg := capability.NewRegistry()
	declare := func(name string, scopes []string) {
		c := capability.NewAdapter(capability.Descriptor{
			Address: "tools.t." + name,
			Name:    name,
			Scopes:  scopes,
		}, nil)
		if err := reg.Register(c); err != nil {
			t.Fatalf("Register(%q): %v", name, err)
		}
	}
	declare("developer_command_execute", []string{"shell"})
	declare("web_search", []string{"network"})

	if !commandToolNeedsValidation(reg, "", "developer_command_execute") {
		t.Error("hybrid descriptor: shell scope should validate")
	}
	if commandToolNeedsValidation(reg, "", "web_search") {
		t.Error("hybrid descriptor: network-only scope must not validate")
	}

	// Declared capability without shell scope whose NAME would match
	// the heuristic must NOT validate (declaration wins).
	declare("shell", []string{"fs.read"})
	if commandToolNeedsValidation(reg, "", "shell") {
		t.Error("hybrid: declared fs.read must win over name match")
	}

	// Undeclared scopes → heuristic fallback: "bash" matches the
	// heuristic name list, so it must still validate.
	declare("bash", nil)
	if !commandToolNeedsValidation(reg, "", "bash") {
		t.Error("hybrid: undeclared scopes must fall back to heuristic")
	}

	// Not registered at all → heuristic applies.
	if !commandToolNeedsValidation(reg, "", "run_command") {
		t.Error("hybrid: unregistered heuristic match must validate")
	}
	if commandToolNeedsValidation(reg, "", "totally_unknown") {
		t.Error("hybrid: unknown tool must not validate")
	}

	// Descriptor mode: declarations only, no heuristic fallback.
	if !commandToolNeedsValidation(reg, "descriptor", "developer_command_execute") {
		t.Error("descriptor: shell scope should validate")
	}
	if commandToolNeedsValidation(reg, "descriptor", "bash") {
		t.Error("descriptor: undeclared tool must NOT validate " +
			"(no heuristic fallback)")
	}
	if commandToolNeedsValidation(nil, "descriptor", "developer_command_execute") {
		t.Error("descriptor: no registry → never validate")
	}

	// Heuristic mode: declarations ignored.
	if !commandToolNeedsValidation(reg, "heuristic", "bash") {
		t.Error("heuristic mode: name match must validate")
	}
	if commandToolNeedsValidation(reg, "heuristic", "web_search") {
		t.Error("heuristic mode: non-command name must not validate")
	}
}

func TestSyncRecipeCapabilities(t *testing.T) {
	reg := capability.NewRegistry()

	recipeTool := func(name string) tool.Tool {
		return &fakeCallableTool{
			decl: &tool.Declaration{Name: name, Description: "r"},
		}
	}

	// Initial sync: "recipe-my-reviewer" → "recipe.my-reviewer";
	// aux tools keep their plain names.
	SyncRecipeCapabilities(reg, []tool.Tool{
		recipeTool("recipe-my-reviewer"),
		recipeTool("recipe-translator"),
		recipeTool("list_recipes"),
	}, 0)

	if got := reg.Len(); got != 3 {
		t.Fatalf("registry len = %d, want 3", got)
	}
	for _, addr := range []string{
		"recipe.my-reviewer", "recipe.translator", "recipe.list_recipes",
	} {
		if _, ok := reg.Resolve(addr); !ok {
			t.Errorf("capability %q not registered", addr)
		}
	}
	if d, _ := reg.Resolve("recipe.my-reviewer"); d != nil {
		if got := d.Descriptor().Source; got != capability.SourceRecipe {
			t.Errorf("source = %q, want recipe", got)
		}
	}

	// Hot reload: translator removed, reviewer renamed, new aux tool.
	// The namespace re-sync must drop stale entries.
	SyncRecipeCapabilities(reg, []tool.Tool{
		recipeTool("recipe-my-reviewer-v2"),
		recipeTool("list_recipes"),
		recipeTool("reload_recipes"),
	}, 0)

	if got := reg.Len(); got != 3 {
		t.Fatalf("after reload len = %d, want 3", got)
	}
	if _, ok := reg.Resolve("recipe.translator"); ok {
		t.Error("stale recipe.translator survived reload sync")
	}
	if _, ok := reg.Resolve("recipe.my-reviewer"); ok {
		t.Error("stale recipe.my-reviewer survived reload sync")
	}
	for _, addr := range []string{
		"recipe.my-reviewer-v2", "recipe.reload_recipes",
	} {
		if _, ok := reg.Resolve(addr); !ok {
			t.Errorf("capability %q not registered after reload", addr)
		}
	}

	// Timeout wrapping: tools stay callable through the wrapper.
	reg2 := capability.NewRegistry()
	SyncRecipeCapabilities(reg2, []tool.Tool{recipeTool("recipe-x")},
		5*60*1000_000_000) // 5m in nanoseconds
	c, ok := reg2.Resolve("recipe.x")
	if !ok {
		t.Fatal("recipe.x not registered with timeout")
	}
	if _, err := c.Invoke(context.Background(), nil); err != nil {
		t.Errorf("timeout-wrapped recipe invoke: %v", err)
	}
}

// fakeCallableTool implements tool.Tool + tool.CallableTool for
// recipe-sync tests.
type fakeCallableTool struct {
	decl *tool.Declaration
}

func (f *fakeCallableTool) Declaration() *tool.Declaration {
	return f.decl
}

func (f *fakeCallableTool) Call(context.Context, []byte) (any, error) {
	return "ok", nil
}

func TestBuildToolsearchCandidates(t *testing.T) {
	declTool := func(name string) tool.Tool {
		return &fakeCallableTool{
			decl: &tool.Declaration{Name: name},
		}
	}
	overlap := &fakeLoopToolSet{name: "overlap", tools: []tool.Tool{
		declTool("todo_create"), // name collision with a preset tool
		declTool("kg_query"),    // toolset-internal duplicate below
		declTool("kg_query"),
	}}
	unique := &fakeLoopToolSet{name: "unique",
		tools: []tool.Tool{declTool("web_search")}}

	cfg := CoreLoopConfig{
		FunctionTools: []tool.Tool{
			declTool("todo_create"),
			declTool("recall_search"),
			declTool("recall_search"), // function-internal duplicate
		},
		// Capabilities nil → legacy toolset path, so the fake
		// toolsets are visible to the flattening.
		ToolSets: []tool.ToolSet{overlap, unique},
	}

	preset, deferred := buildToolsearchCandidates(cfg)

	presetNames := map[string]bool{}
	for _, p := range preset {
		n := p.Declaration().Name
		if presetNames[n] {
			t.Errorf("preset contains duplicate %q", n)
		}
		presetNames[n] = true
	}
	if !presetNames["todo_create"] || !presetNames["recall_search"] {
		t.Errorf("preset = %v, want todo_create + recall_search", presetNames)
	}
	if len(preset) != 2 {
		t.Errorf("preset len = %d, want 2 (deduped)", len(preset))
	}

	deferredNames := map[string]bool{}
	for _, d := range deferred {
		n := d.Declaration().Name
		if presetNames[n] {
			t.Errorf("tool %q appears in both preset and deferred", n)
		}
		if deferredNames[n] {
			t.Errorf("deferred contains duplicate %q", n)
		}
		deferredNames[n] = true
	}
	if !deferredNames["kg_query"] || !deferredNames["web_search"] {
		t.Errorf("deferred = %v, want kg_query + web_search", deferredNames)
	}
	if deferredNames["todo_create"] {
		t.Error("preset tool leaked into deferred population")
	}
	if len(deferred) != 2 {
		t.Errorf("deferred len = %d, want 2", len(deferred))
	}
}
