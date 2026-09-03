package extension

import (
	"context"
	"testing"

	"github.com/km269/wukong/internal/capability"
	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/extension/builtin"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// fakeExtTool implements tool.Tool + tool.CallableTool.
type fakeExtTool struct {
	decl *tool.Declaration
}

func (f *fakeExtTool) Declaration() *tool.Declaration { return f.decl }

func (f *fakeExtTool) Call(context.Context, []byte) (any, error) {
	return "ok", nil
}

func declTool(name string) *fakeExtTool {
	return &fakeExtTool{
		decl: &tool.Declaration{
			Name:        name,
			Description: "test " + name,
		},
	}
}

// fakeToolSet implements tool.ToolSet.
type fakeToolSet struct {
	tools []tool.Tool
}

func (f *fakeToolSet) Tools(context.Context) []tool.Tool {
	return f.tools
}

func (f *fakeToolSet) Close() error { return nil }

func (f *fakeToolSet) Name() string { return "fake" }

func TestExtensionAddressPrefix(t *testing.T) {
	cases := []struct {
		name, extType, want string
	}{
		{"web", "builtin", "tools.web"},
		{"github", "external", "mcp.github"},
		{"mcp_broker", "external", AddrMCPBroker},
		{"orphan", "", "mcp.orphan"},
	}
	for _, tc := range cases {
		if got := extensionAddressPrefix(tc.name, tc.extType); got != tc.want {
			t.Errorf("extensionAddressPrefix(%q,%q) = %q, want %q",
				tc.name, tc.extType, got, tc.want)
		}
	}
}

func TestRegisterCapabilities(t *testing.T) {
	m := NewManager(&config.WukongConfig{})
	m.toolSets["web"] = &fakeToolSet{tools: []tool.Tool{
		declTool("aggregate_search"), declTool("fetch"),
	}}
	m.status["web"] = ExtensionInfo{Name: "web", Type: "builtin"}
	m.toolSets["github"] = &fakeToolSet{tools: []tool.Tool{
		declTool("create_issue"),
	}}
	m.status["github"] = ExtensionInfo{Name: "github", Type: "external"}
	m.toolSets["mcp_broker"] = &fakeToolSet{tools: []tool.Tool{
		declTool("mcp_call"),
	}}
	m.status["mcp_broker"] = ExtensionInfo{
		Name: "mcp_broker", Type: "external",
	}
	// Bootstrap placeholder must be skipped.
	m.toolSets["agent_tools"] = nil
	m.status["agent_tools"] = ExtensionInfo{
		Name: "agent_tools", Type: "builtin",
	}
	// Toolset without a status entry defaults to the MCP namespace.
	m.toolSets["orphan"] = &fakeToolSet{tools: []tool.Tool{
		declTool("t"),
	}}

	reg := capability.NewRegistry()
	n, err := m.RegisterCapabilities(reg, context.Background())
	if err != nil {
		t.Fatalf("RegisterCapabilities: %v", err)
	}
	if n != 5 {
		t.Fatalf("registered = %d, want 5", n)
	}

	for _, addr := range []string{
		"tools.web.aggregate_search",
		"tools.web.fetch",
		"mcp.github.create_issue",
		"mcp.broker.mcp_call",
		"mcp.orphan.t",
	} {
		if _, ok := reg.Resolve(addr); !ok {
			t.Errorf("capability %q not registered", addr)
		}
	}
	if _, ok := reg.Resolve("tools.agent_tools.x"); ok {
		t.Error("nil placeholder toolset should be skipped")
	}

	if d, _ := reg.Resolve("tools.web.aggregate_search"); d != nil {
		if got := d.Descriptor().Source; got != capability.SourceBuiltin {
			t.Errorf("builtin source = %q, want %q",
				got, capability.SourceBuiltin)
		}
	}
	if d, _ := reg.Resolve("mcp.github.create_issue"); d != nil {
		if got := d.Descriptor().Source; got != capability.SourceMCP {
			t.Errorf("mcp source = %q, want %q",
				got, capability.SourceMCP)
		}
	}
}

func TestRegisterCapabilitiesNilRegistry(t *testing.T) {
	m := NewManager(&config.WukongConfig{})
	if _, err := m.RegisterCapabilities(nil, context.Background()); err == nil {
		t.Error("nil registry accepted")
	}
}

func TestRegisterToolSetValidation(t *testing.T) {
	reg := capability.NewRegistry()
	ctx := context.Background()
	if _, err := RegisterToolSet(
		reg, "tools.x", capability.SourceBuiltin, nil, ctx,
	); err == nil {
		t.Error("nil toolset accepted")
	}
	if _, err := RegisterToolSet(
		reg, "bare", capability.SourceBuiltin, &fakeToolSet{}, ctx,
	); err == nil {
		t.Error("namespace-less prefix accepted")
	}
}

func TestRegisterToolsDuplicateSkips(t *testing.T) {
	reg := capability.NewRegistry()
	n, err := RegisterTools(
		reg, "tools.t", capability.SourceBuiltin,
		[]tool.Tool{declTool("dup"), declTool("dup"), declTool("other")},
	)
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	if n != 2 {
		t.Fatalf("registered = %d, want 2 (duplicate skipped)", n)
	}
	if _, ok := reg.Resolve("tools.t.dup"); !ok {
		t.Error("first registration should win")
	}
	if _, ok := reg.Resolve("tools.t.other"); !ok {
		t.Error("tool after duplicate should still register")
	}
}

func TestRegisterToolsUnnamedSkipped(t *testing.T) {
	reg := capability.NewRegistry()
	n, err := RegisterTools(
		reg, "tools.t", capability.SourceBuiltin,
		[]tool.Tool{
			nil,
			&fakeExtTool{decl: nil},
			&fakeExtTool{decl: &tool.Declaration{}},
		},
	)
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	if n != 0 {
		t.Fatalf("registered = %d, want 0", n)
	}
}

func TestRegisterToolsScopeInjection(t *testing.T) {
	reg := capability.NewRegistry()
	scopes := map[string][]string{
		"cmd_tool": {"shell"},
	}
	n, err := RegisterTools(
		reg, "tools.developer", capability.SourceBuiltin,
		[]tool.Tool{declTool("cmd_tool"), declTool("read_tool")},
		WithScopeLookup(func(name string) []string {
			return scopes[name]
		}),
	)
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	if n != 2 {
		t.Fatalf("registered = %d, want 2", n)
	}

	d, ok := reg.Resolve("tools.developer.cmd_tool")
	if !ok {
		t.Fatal("cmd_tool not registered")
	}
	if got := d.Descriptor().Scopes; len(got) != 1 || got[0] != "shell" {
		t.Errorf("cmd_tool scopes = %v, want [shell]", got)
	}

	d, ok = reg.Resolve("tools.developer.read_tool")
	if !ok {
		t.Fatal("read_tool not registered")
	}
	if got := d.Descriptor().Scopes; len(got) != 0 {
		t.Errorf("read_tool scopes = %v, want empty", got)
	}
}

func TestBuiltinConstructors(t *testing.T) {
	// Every name in RegisterBuiltins must resolve to a constructor
	// (placeholder ones included), keeping the two tables in sync.
	cfg := &config.WukongConfig{}
	builtin.RegisterBuiltins(cfg)
	for _, ext := range cfg.Extensions {
		_, err := CreateBuiltinToolSet(ext.Name, cfg)
		if err != nil {
			t.Errorf("builtin %q: %v", ext.Name, err)
		}
	}
	if _, err := CreateBuiltinToolSet("no_such_builtin", cfg); err == nil {
		t.Error("unknown builtin accepted")
	}
}

func TestRegisterCapabilitiesExternalScopes(t *testing.T) {
	cfg := &config.WukongConfig{}
	cfg.Extensions = append(cfg.Extensions, config.ExtensionConfig{
		Name:    "runner",
		Type:    "external",
		Enabled: true,
		ToolScopes: map[string][]string{
			"run_cmd": {"shell"},
		},
	})

	m := NewManager(cfg)
	m.toolSets["runner"] = &fakeToolSet{tools: []tool.Tool{
		declTool("run_cmd"), declTool("read_doc"),
	}}
	m.status["runner"] = ExtensionInfo{
		Name: "runner", Type: "external",
	}

	reg := capability.NewRegistry()
	if _, err := m.RegisterCapabilities(reg, context.Background()); err != nil {
		t.Fatalf("RegisterCapabilities: %v", err)
	}

	if d, ok := reg.Resolve("mcp.runner.run_cmd"); ok {
		if got := d.Descriptor().Scopes; len(got) != 1 || got[0] != "shell" {
			t.Errorf("run_cmd scopes = %v, want [shell]", got)
		}
	} else {
		t.Error("mcp.runner.run_cmd not registered")
	}
	if d, ok := reg.Resolve("mcp.runner.read_doc"); ok {
		if got := d.Descriptor().Scopes; len(got) != 0 {
			t.Errorf("read_doc scopes = %v, want empty", got)
		}
	} else {
		t.Error("mcp.runner.read_doc not registered")
	}
}

func TestToolScopes(t *testing.T) {
	dev := builtin.ToolScopes("developer")
	if dev == nil {
		t.Fatal("developer scopes not declared")
	}
	if got := dev["developer_command_execute"]; len(got) != 1 ||
		got[0] != "shell" {
		t.Errorf("developer_command_execute scopes = %v, want [shell]",
			got)
	}
	if builtin.ToolScopes("web") != nil {
		t.Error("web should have no scope declarations")
	}
}
