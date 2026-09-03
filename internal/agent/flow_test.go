package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/km269/wukong/internal/capability"
	"github.com/km269/wukong/internal/config"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// fakeCap registers a capability that returns a fixed JSON result.
func fakeCap(t *testing.T, reg *capability.Registry, addr, result string) {
	t.Helper()
	c := capability.NewAdapter(
		capability.Descriptor{Address: addr, Name: addr},
		func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(result), nil
		},
	)
	if err := reg.Register(c); err != nil {
		t.Fatalf("register fake capability %q: %v", addr, err)
	}
}

func validLinearFlow() *FlowConfig {
	return &FlowConfig{
		Name:        "f",
		Description: "test flow",
		Nodes: []FlowNode{
			{Name: "first", Type: FlowNodeCapability, Address: "tools.t.first"},
			{Name: "second", Type: FlowNodeCapability, Address: "tools.t.second",
				Args: map[string]string{"in": "{{.first}}"}},
		},
	}
}

func TestValidateFlowConfig(t *testing.T) {
	t.Run("linear ok", func(t *testing.T) {
		order, err := validateFlowConfig(validLinearFlow())
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		if fmt.Sprint(order) != "[first second]" {
			t.Errorf("order = %v", order)
		}
	})

	t.Run("cycle rejected", func(t *testing.T) {
		f := &FlowConfig{
			Name: "cyclic",
			Nodes: []FlowNode{
				{Name: "a", Type: FlowNodeCapability, Address: "tools.t.a"},
				{Name: "b", Type: FlowNodeCapability, Address: "tools.t.b"},
			},
			Edges: []FlowEdge{{From: "a", To: "b"}, {From: "b", To: "a"}},
		}
		if _, err := validateFlowConfig(f); err == nil ||
			!strings.Contains(err.Error(), "cycle") {
			t.Fatalf("err = %v, want cycle", err)
		}
	})

	t.Run("unknown edge node", func(t *testing.T) {
		f := validLinearFlow()
		f.Edges = []FlowEdge{{From: "ghost", To: "second"}}
		if _, err := validateFlowConfig(f); err == nil ||
			!strings.Contains(err.Error(), "ghost") {
			t.Fatalf("err = %v, want unknown source", err)
		}
	})

	t.Run("duplicate node name", func(t *testing.T) {
		f := validLinearFlow()
		f.Nodes[1].Name = "first"
		if _, err := validateFlowConfig(f); err == nil ||
			!strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("err = %v, want duplicate", err)
		}
	})

	t.Run("dot in node name", func(t *testing.T) {
		f := validLinearFlow()
		f.Nodes[0].Name = "a.b"
		if _, err := validateFlowConfig(f); err == nil ||
			!strings.Contains(err.Error(), "dots") {
			t.Fatalf("err = %v, want dots", err)
		}
	})

	t.Run("unknown type", func(t *testing.T) {
		f := validLinearFlow()
		f.Nodes[0].Type = "quantum"
		if _, err := validateFlowConfig(f); err == nil ||
			!strings.Contains(err.Error(), "unknown type") {
			t.Fatalf("err = %v, want unknown type", err)
		}
	})

	t.Run("capability address required", func(t *testing.T) {
		f := validLinearFlow()
		f.Nodes[0].Address = ""
		if _, err := validateFlowConfig(f); err == nil ||
			!strings.Contains(err.Error(), "address is required") {
			t.Fatalf("err = %v, want address required", err)
		}
	})

	t.Run("self loop", func(t *testing.T) {
		f := validLinearFlow()
		f.Edges = []FlowEdge{{From: "first", To: "first"}}
		if _, err := validateFlowConfig(f); err == nil ||
			!strings.Contains(err.Error(), "self-loop") {
			t.Fatalf("err = %v, want self-loop", err)
		}
	})
}

func TestCompileAndRunFlow(t *testing.T) {
	reg := capability.NewRegistry()
	fakeCap(t, reg, "tools.t.first", `{"text":"hello"}`)
	fakeCap(t, reg, "tools.t.second", `{"done":true}`)

	// Node two renders its arg from node one's decoded JSON output.
	f := &FlowConfig{
		Name: "run",
		Nodes: []FlowNode{
			{Name: "first", Type: FlowNodeCapability, Address: "tools.t.first"},
			{Name: "second", Type: FlowNodeCapability, Address: "tools.t.second",
				Args: map[string]string{"in": "{{.first.text}}"}},
		},
	}
	cf, err := compileFlow(f, nil, nil, reg)
	if err != nil {
		t.Fatalf("compileFlow: %v", err)
	}

	out, err := runFlow(context.Background(), cf, "seed")
	if err != nil {
		t.Fatalf("runFlow: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["done"] != true {
		t.Fatalf("out = %#v, want done:true", out)
	}
}

func TestRunFlowConditionalSkip(t *testing.T) {
	reg := capability.NewRegistry()
	fakeCap(t, reg, "tools.t.classify", `"yes"`)
	fakeCap(t, reg, "tools.t.save", `saved`)
	fakeCap(t, reg, "tools.t.tail", `tail`)

	f := &FlowConfig{
		Name: "cond",
		Nodes: []FlowNode{
			{Name: "classify", Type: FlowNodeCapability, Address: "tools.t.classify"},
			{Name: "save", Type: FlowNodeCapability, Address: "tools.t.save"},
			{Name: "tail", Type: FlowNodeCapability, Address: "tools.t.tail"},
		},
		Edges: []FlowEdge{
			{From: "classify", To: "save"},
			{From: "classify", To: "tail",
				When: `{{if eq .classify "yes"}}go{{end}}`},
			{From: "save", To: "tail"},
		},
	}
	cf, err := compileFlow(f, nil, nil, reg)
	if err != nil {
		t.Fatalf("compileFlow: %v", err)
	}

	// classify=yes → tail active; tail also depends on save → runs
	// after save. Result is the last executed node (tail).
	out, err := runFlow(context.Background(), cf, "")
	if err != nil {
		t.Fatalf("runFlow: %v", err)
	}
	if out != "tail" {
		t.Fatalf("out = %#v, want tail", out)
	}

	// Flip the condition: tail's incoming edge from classify is
	// inactive → tail skipped → result is save's output.
	reg2 := capability.NewRegistry()
	fakeCap(t, reg2, "tools.t.classify", `"no"`)
	fakeCap(t, reg2, "tools.t.save", `saved`)
	fakeCap(t, reg2, "tools.t.tail", `tail`)
	cf2, err := compileFlow(f, nil, nil, reg2)
	if err != nil {
		t.Fatalf("compileFlow(2): %v", err)
	}
	out, err = runFlow(context.Background(), cf2, "")
	if err != nil {
		t.Fatalf("runFlow(2): %v", err)
	}
	if out != "saved" {
		t.Fatalf("out = %#v, want saved (tail skipped)", out)
	}
}

func TestRunFlowCapabilityMissing(t *testing.T) {
	reg := capability.NewRegistry() // nothing registered
	f := &FlowConfig{
		Name: "missing",
		Nodes: []FlowNode{
			{Name: "n", Type: FlowNodeCapability, Address: "tools.t.ghost"},
		},
	}
	cf, err := compileFlow(f, nil, nil, reg)
	if err != nil {
		t.Fatalf("compileFlow: %v", err)
	}
	_, err = runFlow(context.Background(), cf, "")
	if err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("err = %v, want not registered", err)
	}
}

func TestFlowToolDeclarationAndCall(t *testing.T) {
	reg := capability.NewRegistry()
	fakeCap(t, reg, "tools.t.echo", `"echo-result"`)

	f := &FlowConfig{
		Name:        "echo",
		Description: "Echo flow",
		Nodes: []FlowNode{
			{Name: "n", Type: FlowNodeCapability, Address: "tools.t.echo",
				Args: map[string]string{"got": "{{.input}}"}},
		},
	}
	cf, err := compileFlow(f, nil, nil, reg)
	if err != nil {
		t.Fatalf("compileFlow: %v", err)
	}
	ft := newFlowTool(cf)

	decl := ft.Declaration()
	if decl.Name != "flow-echo" {
		t.Errorf("name = %q, want flow-echo", decl.Name)
	}
	if !strings.Contains(decl.Description, "Echo flow") ||
		!strings.Contains(decl.Description, "n") {
		t.Errorf("description = %q, want flow steps", decl.Description)
	}

	// {"request": "..."} unwraps to the string input.
	out, err := ft.Call(context.Background(), []byte(`{"request":"hi"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	// Capability results are JSON-decoded for the template context,
	// so the raw JSON string arrives as a Go string.
	if out != "echo-result" {
		t.Errorf("out = %#v, want echo-result", out)
	}
}

func TestSyncFlowCapabilities(t *testing.T) {
	reg := capability.NewRegistry()

	flowToolOf := func(name string) tool.Tool {
		reg2 := capability.NewRegistry()
		fakeCap(t, reg2, "tools.t.x", `{}`)
		cf, err := compileFlow(&FlowConfig{
			Name:        strings.TrimPrefix(name, "flow-"),
			Description: "d",
			Nodes: []FlowNode{
				{Name: "n", Type: FlowNodeCapability, Address: "tools.t.x"},
			},
		}, nil, nil, reg2)
		if err != nil {
			t.Fatalf("compileFlow: %v", err)
		}
		return newFlowTool(cf)
	}

	SyncFlowCapabilities(reg, []tool.Tool{
		flowToolOf("flow-alpha"), flowToolOf("flow-beta"),
	})
	for _, addr := range []string{"flow.alpha", "flow.beta"} {
		if _, ok := reg.Resolve(addr); !ok {
			t.Errorf("capability %q not registered", addr)
		}
	}
	if d, _ := reg.Resolve("flow.alpha"); d != nil {
		if got := d.Descriptor().Source; got != capability.SourceFlow {
			t.Errorf("source = %q, want flow", got)
		}
	}

	// Reload drop: beta removed, gamma added.
	SyncFlowCapabilities(reg, []tool.Tool{flowToolOf("flow-gamma")})
	if _, ok := reg.Resolve("flow.beta"); ok {
		t.Error("stale flow.beta survived sync")
	}
	if _, ok := reg.Resolve("flow.gamma"); !ok {
		t.Error("flow.gamma not registered after sync")
	}
	if got := reg.List("flow"); len(got) != 1 {
		t.Errorf("flow namespace len = %d, want 1", len(got))
	}
}

func TestLoadFlowDirAndToolSet(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(
			filepath.Join(dir, name), []byte(content), 0o644,
		); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("good.yaml", `
name: good
description: A good flow
nodes:
  - name: grab
    type: capability
    address: tools.developer.developer_directory_list
    args:
      path: "{{.input}}"
`)
	write("broken.yaml", `
name: broken
nodes:
  - name: a
    type: capability
    address: tools.t.a
  - name: b
    type: capability
    address: tools.t.b
edges:
  - from: a
    to: b
  - from: b
    to: a
`)
	write("ignored.txt", "not yaml")

	t.Setenv("WUKONG_FLOW_DIR_UNUSED", "1")
	agentCfg := &config.AgentConfig{FlowEnabled: true, FlowDir: dir}
	reg := capability.NewRegistry()

	ts := NewFlowToolSet(nil, agentCfg, reg)
	if ts == nil {
		t.Fatal("NewFlowToolSet = nil, want toolset")
	}
	tools := ts.Tools(context.Background())
	if len(tools) != 1 {
		t.Fatalf("tools = %d, want 1 (broken skipped)", len(tools))
	}
	if got := tools[0].Declaration().Name; got != "flow-good" {
		t.Errorf("tool = %q, want flow-good", got)
	}

	// Disabled → nil.
	if NewFlowToolSet(nil, &config.AgentConfig{FlowDir: dir}, reg) != nil {
		t.Error("disabled flows should yield nil toolset")
	}

	// Sync + resolve + invoke through the capability bus. The real
	// developer tool isn't wired in this offline test, so stand in a
	// fake at the same address.
	SyncFlowCapabilities(reg, tools)
	fakeCap(t, reg, "tools.developer.developer_directory_list", `{"success":true}`)
	cap, ok := reg.Resolve("flow.good")
	if !ok {
		t.Fatal("flow.good not registered")
	}
	raw, err := cap.Invoke(context.Background(), []byte(`{"request":"docs"}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("non-JSON flow result: %v (%s)", err, raw)
	}
	if generic["success"] != true {
		t.Errorf("result = %v, want success:true", generic)
	}
}

func TestFlowToolSetReload(t *testing.T) {
	dir := t.TempDir()
	good := `
name: reloadable
nodes:
  - name: n
    type: capability
    address: tools.t.x
`
	if err := os.WriteFile(
		filepath.Join(dir, "reloadable.yaml"), []byte(good), 0o644,
	); err != nil {
		t.Fatalf("write: %v", err)
	}

	reg := capability.NewRegistry()
	fakeCap(t, reg, "tools.t.x", `{}`)
	agentCfg := &config.AgentConfig{FlowEnabled: true, FlowDir: dir}
	ts := NewFlowToolSet(nil, agentCfg, reg)
	if ts == nil {
		t.Fatal("toolset nil")
	}
	var reloaded int
	ts.SetReloadCallback(func(tools []tool.Tool) { reloaded++ })

	// Rewrite with a different flow name; reload should swap.
	if err := os.WriteFile(
		filepath.Join(dir, "reloadable.yaml"),
		[]byte(strings.Replace(good, "reloadable", "reloaded2", 1)),
		0o644,
	); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if !ts.Reload() {
		t.Fatal("Reload = false")
	}
	if reloaded != 1 {
		t.Fatalf("reload callback count = %d, want 1", reloaded)
	}
	got := ts.Tools(context.Background())[0].Declaration().Name
	if got != "flow-reloaded2" {
		t.Errorf("tool after reload = %q, want flow-reloaded2", got)
	}

	// Emptying the directory aborts the reload (previous set stays).
	if err := os.Remove(filepath.Join(dir, "reloadable.yaml")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if ts.Reload() {
		t.Error("Reload with no flows should fail")
	}
	if got := ts.Tools(context.Background())[0].Declaration().Name; got != "flow-reloaded2" {
		t.Errorf("tool set changed after aborted reload: %q", got)
	}
}
