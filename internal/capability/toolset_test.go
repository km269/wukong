package capability

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestResolveByName(t *testing.T) {
	r := NewRegistry()
	// Two capabilities sharing the LLM-visible name "fetch".
	add := func(addr, name string) {
		c := NewAdapter(Descriptor{Address: addr, Name: name}, nil)
		if err := r.Register(c); err != nil {
			t.Fatalf("Register(%q): %v", addr, err)
		}
	}
	add("tools.b.fetch", "fetch")
	add("tools.a.fetch", "fetch")
	add("tools.a.search", "search")

	// Deterministic winner: lexicographically smallest address.
	c, ok := r.ResolveByName("fetch")
	if !ok || c.Address() != "tools.a.fetch" {
		t.Fatalf("ResolveByName(fetch) = %v,%v", c, ok)
	}
	c, ok = r.ResolveByName("search")
	if !ok || c.Address() != "tools.a.search" {
		t.Fatalf("ResolveByName(search) = %v,%v", c, ok)
	}
	if _, ok := r.ResolveByName("missing"); ok {
		t.Fatal("ResolveByName(missing) should not resolve")
	}
	if _, ok := r.ResolveByName(""); ok {
		t.Fatal("ResolveByName(empty) should not resolve")
	}
}

func TestFromToolMeta(t *testing.T) {
	ft := &fakeTool{decl: testDecl()}
	c, err := FromToolMeta("tools.developer.developer_command_execute",
		SourceBuiltin, ft, ToolMeta{
			Scopes:   []string{"shell"},
			ReadOnly: false,
		})
	if err != nil {
		t.Fatalf("FromToolMeta: %v", err)
	}
	d := c.Descriptor()
	if len(d.Scopes) != 1 || d.Scopes[0] != "shell" {
		t.Errorf("Scopes = %v, want [shell]", d.Scopes)
	}
	if !d.Mutating {
		t.Error("Mutating = false, want true")
	}

	ro, err := FromToolMeta("tools.web.search", SourceBuiltin, ft,
		ToolMeta{ReadOnly: true})
	if err != nil {
		t.Fatalf("FromToolMeta(ro): %v", err)
	}
	if ro.Descriptor().Mutating {
		t.Error("ReadOnly capability still marked Mutating")
	}

	// Zero meta keeps the conservative defaults.
	def, err := FromToolMeta("tools.x.y", SourceBuiltin, ft, ToolMeta{})
	if err != nil {
		t.Fatalf("FromToolMeta(zero): %v", err)
	}
	d = def.Descriptor()
	if d.Mutating != true || len(d.Scopes) != 0 {
		t.Errorf("zero meta = Mutating %v, Scopes %v",
			d.Mutating, d.Scopes)
	}
}

func TestRegistryToolSet(t *testing.T) {
	r := NewRegistry()
	newCap := func(addr string) {
		ft := &fakeTool{
			decl: &tool.Declaration{Name: addr, Description: "d"},
			fn: func(context.Context, []byte) (any, error) {
				return map[string]any{"addr": addr}, nil
			},
		}
		c, err := FromTool(addr, SourceBuiltin, ft)
		if err != nil {
			t.Fatalf("FromTool(%q): %v", addr, err)
		}
		if err := r.Register(c); err != nil {
			t.Fatalf("Register(%q): %v", addr, err)
		}
	}
	newCap("tools.beta")
	newCap("tools.alpha")

	ts := RegistryToolSet(r)
	if ts.Name() != "capability_registry" {
		t.Errorf("Name = %q", ts.Name())
	}
	if err := ts.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}

	tools := ts.Tools(context.Background())
	if len(tools) != 2 {
		t.Fatalf("Tools len = %d, want 2", len(tools))
	}
	// Sorted manifest: alpha before beta.
	if got := tools[0].Declaration().Name; got != "tools.alpha" {
		t.Errorf("first tool = %q, want tools.alpha", got)
	}

	// Callable end-to-end through the toolset adapter.
	ct, ok := tools[1].(tool.CallableTool)
	if !ok {
		t.Fatal("toolset tool not callable")
	}
	v, err := ct.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	m, ok := v.(map[string]any)
	if !ok || m["addr"] != "tools.beta" {
		t.Errorf("Call = %#v, want addr=tools.beta", v)
	}

	// Empty registry yields an empty manifest, not nil.
	empty := RegistryToolSet(NewRegistry()).Tools(context.Background())
	if len(empty) != 0 {
		t.Errorf("empty registry Tools = %d", len(empty))
	}
}
