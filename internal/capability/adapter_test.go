package capability

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// fakeTool implements tool.Tool and tool.CallableTool.
type fakeTool struct {
	decl *tool.Declaration
	fn   func(ctx context.Context, args []byte) (any, error)
}

func (f *fakeTool) Declaration() *tool.Declaration { return f.decl }

func (f *fakeTool) Call(
	ctx context.Context, args []byte,
) (any, error) {
	if f.fn == nil {
		return nil, nil
	}
	return f.fn(ctx, args)
}

// declOnlyTool implements only tool.Tool (no Call).
type declOnlyTool struct {
	decl *tool.Declaration
}

func (d *declOnlyTool) Declaration() *tool.Declaration {
	return d.decl
}

func testDecl() *tool.Declaration {
	return &tool.Declaration{
		Name:        "fetch",
		Description: "Fetch a page",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"url": {Type: "string", Description: "target url"},
			},
			Required: []string{"url"},
		},
	}
}

func TestFromToolDescriptor(t *testing.T) {
	c, err := FromTool(
		"tools.web.fetch", SourceBuiltin, &fakeTool{decl: testDecl()},
	)
	if err != nil {
		t.Fatalf("FromTool: %v", err)
	}
	desc := c.Descriptor()
	if desc.Address != "tools.web.fetch" {
		t.Errorf("Address = %q", desc.Address)
	}
	if desc.Name != "fetch" || desc.Description != "Fetch a page" {
		t.Errorf("Name/Description = %q/%q", desc.Name, desc.Description)
	}
	// Conservative Phase A defaults: ask rather than assume.
	if !desc.Mutating {
		t.Error("Mutating = false, want true (conservative default)")
	}
	if len(desc.Scopes) != 0 {
		t.Errorf("Scopes = %v, want empty", desc.Scopes)
	}
	if desc.Source != SourceBuiltin {
		t.Errorf("Source = %q, want %q", desc.Source, SourceBuiltin)
	}

	var back tool.Schema
	if err := json.Unmarshal(desc.Parameters, &back); err != nil {
		t.Fatalf("unmarshal Parameters: %v", err)
	}
	if !reflect.DeepEqual(&back, testDecl().InputSchema) {
		t.Error("Parameters round-trip differs from InputSchema")
	}
}

func TestFromToolInvokePassthrough(t *testing.T) {
	var gotArgs []byte
	ft := &fakeTool{
		decl: testDecl(),
		fn: func(_ context.Context, args []byte) (any, error) {
			gotArgs = args
			return map[string]any{"status": "ok"}, nil
		},
	}
	c, err := FromTool("tools.web.fetch", SourceBuiltin, ft)
	if err != nil {
		t.Fatalf("FromTool: %v", err)
	}
	raw, err := c.Invoke(
		context.Background(), json.RawMessage(`{"url":"https://x"}`),
	)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if string(gotArgs) != `{"url":"https://x"}` {
		t.Errorf("args = %s, want passthrough", gotArgs)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if out["status"] != "ok" {
		t.Errorf("result = %v, want status=ok", out)
	}
}

func TestFromToolResultNormalization(t *testing.T) {
	cases := []struct {
		name string
		out  any
		want string
	}{
		{"nil", nil, "null"},
		{"bytes", []byte(`{"a":1}`), `{"a":1}`},
		{"raw", json.RawMessage(`[1,2]`), `[1,2]`},
		{"map", map[string]any{"x": "y"}, `{"x":"y"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ft := &fakeTool{
				decl: testDecl(),
				fn: func(context.Context, []byte) (any, error) {
					return tc.out, nil
				},
			}
			c, err := FromTool("tools.web.fetch", SourceBuiltin, ft)
			if err != nil {
				t.Fatalf("FromTool: %v", err)
			}
			raw, err := c.Invoke(context.Background(), nil)
			if err != nil {
				t.Fatalf("Invoke: %v", err)
			}
			if string(raw) != tc.want {
				t.Errorf("result = %s, want %s", raw, tc.want)
			}
		})
	}
}

func TestFromToolUnmarshalableResult(t *testing.T) {
	ft := &fakeTool{
		decl: testDecl(),
		fn: func(context.Context, []byte) (any, error) {
			return make(chan int), nil // not JSON-marshalable
		},
	}
	c, _ := FromTool("tools.web.fetch", SourceBuiltin, ft)
	raw, err := c.Invoke(context.Background(), nil)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("fallback result not JSON string: %v (%s)", err, raw)
	}
}

func TestFromToolInvokeError(t *testing.T) {
	ft := &fakeTool{
		decl: testDecl(),
		fn: func(context.Context, []byte) (any, error) {
			return nil, errors.New("boom")
		},
	}
	c, _ := FromTool("tools.web.fetch", SourceBuiltin, ft)
	if _, err := c.Invoke(context.Background(), nil); err == nil ||
		!strings.Contains(err.Error(), "boom") {
		t.Fatalf("Invoke error = %v, want boom", err)
	}
}

func TestFromToolDeclarationOnly(t *testing.T) {
	c, err := FromTool(
		"tools.x.y", SourceBuiltin, &declOnlyTool{decl: testDecl()},
	)
	if err != nil {
		t.Fatalf("FromTool: %v", err)
	}
	_, err = c.Invoke(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "does not support Call") {
		t.Fatalf("Invoke error = %v, want not-callable", err)
	}
}

func TestFromToolValidation(t *testing.T) {
	if _, err := FromTool("a", SourceBuiltin, nil); err == nil {
		t.Error("nil tool accepted")
	}
	if _, err := FromTool("a", SourceBuiltin, &fakeTool{}); err == nil {
		t.Error("nil declaration accepted")
	}
	if _, err := FromTool(
		"a", SourceBuiltin, &fakeTool{decl: &tool.Declaration{}},
	); err == nil {
		t.Error("nameless declaration accepted")
	}
}

func TestToolFromCapabilityRoundTrip(t *testing.T) {
	src := &fakeTool{
		decl: testDecl(),
		fn: func(context.Context, []byte) (any, error) {
			return map[string]any{"ok": true}, nil
		},
	}
	c, err := FromTool("tools.web.fetch", SourceBuiltin, src)
	if err != nil {
		t.Fatalf("FromTool: %v", err)
	}
	rt := ToolFromCapability(c)

	got := rt.Declaration()
	want := testDecl()
	if got.Name != want.Name || got.Description != want.Description {
		t.Errorf("declaration = %q/%q, want %q/%q",
			got.Name, got.Description, want.Name, want.Description)
	}
	b1, _ := json.Marshal(got.InputSchema)
	b2, _ := json.Marshal(want.InputSchema)
	if string(b1) != string(b2) {
		t.Errorf("InputSchema round-trip:\n got %s\nwant %s", b1, b2)
	}

	v, err := rt.Call(context.Background(), []byte(`{"url":"u"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	m, ok := v.(map[string]any)
	if !ok || m["ok"] != true {
		t.Errorf("Call = %#v, want map ok:true", v)
	}
}

func TestToolFromCapabilityCallError(t *testing.T) {
	c := NewAdapter(
		Descriptor{Address: "tools.x.y", Name: "y"},
		func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return nil, errors.New("kaput")
		},
	)
	if _, err := ToolFromCapability(c).Call(
		context.Background(), nil,
	); err == nil || !strings.Contains(err.Error(), "kaput") {
		t.Fatalf("Call error = %v, want kaput", err)
	}
}

func TestAdapterNilInvoke(t *testing.T) {
	a := NewAdapter(
		Descriptor{Address: "tools.x.y", Name: "y"}, nil,
	)
	if _, err := a.Invoke(context.Background(), nil); err == nil ||
		!strings.Contains(err.Error(), "no invoke function") {
		t.Fatalf("Invoke error = %v, want no-invoke", err)
	}
}

func TestRegistryAsToolsSorted(t *testing.T) {
	r := NewRegistry()
	newTool := func(name string) tool.Tool {
		return &fakeTool{
			decl: &tool.Declaration{Name: name},
		}
	}
	mustRegisterAdapter := func(addr string) {
		c, err := FromTool(addr, SourceBuiltin, newTool(addr))
		if err != nil {
			t.Fatalf("FromTool(%q): %v", addr, err)
		}
		if err := r.Register(c); err != nil {
			t.Fatalf("Register(%q): %v", addr, err)
		}
	}
	mustRegisterAdapter("tools.b")
	mustRegisterAdapter("mcp.a")
	mustRegisterAdapter("tools.a")

	got := r.AsTools()
	if len(got) != 3 {
		t.Fatalf("AsTools len = %d, want 3", len(got))
	}
	for i, wantAddr := range []string{"mcp.a", "tools.a", "tools.b"} {
		ct, ok := got[i].(tool.CallableTool)
		if !ok {
			t.Fatalf("AsTools[%d] not callable", i)
		}
		if ct.Declaration().Name != wantAddr {
			t.Errorf("AsTools[%d] name = %q, want %q",
				i, ct.Declaration().Name, wantAddr)
		}
	}
}
