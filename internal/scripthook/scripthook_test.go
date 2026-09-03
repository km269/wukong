package scripthook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/km269/wukong/internal/capability"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func newTestSet(t *testing.T, files map[string]string) *ScriptHookSet {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		write(t, dir, name, content)
	}
	set, err := Load(dir, 2*time.Second)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return set
}

func userMsg(content string) model.Message {
	return model.Message{Role: model.RoleUser, Content: content}
}

func TestLoadDetectsHooksAndTools(t *testing.T) {
	set := newTestSet(t, map[string]string{
		"guard.js": `
			function beforeStep(ctx) {
				if (ctx.message.content === "bad") {
					return {reject: true, reason: "blocked word"};
				}
				if (ctx.message.content === "shout") {
					return {rewrite: "quiet"};
				}
			}
			function beforeTool(ctx) {
				if (ctx.tool_name === "danger") {
					return {reject: true, reason: "no danger"};
				}
			}
			tool({name: "shouty", description: "uppercases",
			      parameters: {type: "object", properties: {
			        text: {type: "string"}}}},
			     function(args) { return {text: args.text.toUpperCase()}; });
		`,
		"broken.js": "this is not(((( valid js",
		"plain.js":  "var x = 1;",
	})

	if set.Len() != 2 {
		t.Fatalf("scripts = %d, want 2 (broken skipped)", set.Len())
	}

	preSteps := set.PreStepHooks()
	if len(preSteps) != 1 || preSteps[0].Name() != "script:guard" {
		t.Fatalf("preStep hooks = %v", preSteps)
	}
	preTools := set.PreToolHooks()
	if len(preTools) != 1 {
		t.Fatalf("preTool hooks = %d, want 1", len(preTools))
	}
	if got := len(set.ScriptTools()); got != 1 {
		t.Fatalf("script tools = %d, want 1", got)
	}
}

func TestBeforeStepRewriteAndReject(t *testing.T) {
	set := newTestSet(t, map[string]string{
		"guard.js": `
			function beforeStep(ctx) {
				if (ctx.message.content === "bad") {
					return {reject: true, reason: "blocked"};
				}
				if (ctx.message.content === "shout") {
					return {rewrite: "quiet"};
				}
			}
		`,
	})
	hook := set.PreStepHooks()[0]
	ctx := context.Background()

	// Pass-through: undefined return.
	out, reject, err := hook.BeforeStep(ctx, "s", "u", userMsg("hello"))
	if err != nil || reject || out.Content != "hello" {
		t.Fatalf("pass-through = %v/%v/%q", err, reject, out.Content)
	}

	// Rewrite: content replaced, role preserved.
	out, reject, err = hook.BeforeStep(ctx, "s", "u", userMsg("shout"))
	if err != nil || reject || out.Content != "quiet" {
		t.Fatalf("rewrite = %v/%v/%q", err, reject, out.Content)
	}
	if out.Role != model.RoleUser {
		t.Errorf("role = %v, want user", out.Role)
	}

	// Reject.
	_, reject, err = hook.BeforeStep(ctx, "s", "u", userMsg("bad"))
	if err != nil || !reject {
		t.Fatalf("reject = %v/%v", err, reject)
	}
}

func TestBeforeStepFailOpen(t *testing.T) {
	set := newTestSet(t, map[string]string{
		"throw.js": `
			function beforeStep(ctx) { throw new Error("boom"); }
		`,
		"loop.js": `
			function beforeStep(ctx) { while (true) {} }
		`,
	})
	hooks := set.PreStepHooks()
	if len(hooks) != 2 {
		t.Fatalf("hooks = %d, want 2", len(hooks))
	}
	for _, h := range hooks {
		msg, reject, err := h.BeforeStep(
			context.Background(), "s", "u", userMsg("x"))
		// Fail-open: no error, no reject, message unchanged.
		if err != nil || reject || msg.Content != "x" {
			t.Fatalf("%s fail-open broken: %v/%v/%q",
				h.Name(), err, reject, msg.Content)
		}
	}
}

func TestBeforeToolReject(t *testing.T) {
	set := newTestSet(t, map[string]string{
		"guard.js": `
			function beforeTool(ctx) {
				if (ctx.tool_name === "danger" &&
				    ctx.args && ctx.args.path === "/etc/passwd") {
					return {reject: true, reason: "sensitive path"};
				}
			}
		`,
	})
	hook := set.PreToolHooks()[0]
	ctx := context.Background()

	reject, reason, err := hook.BeforeToolExecute(ctx, "danger",
		[]byte(`{"path":"/etc/passwd"}`))
	if err != nil || !reject || reason != "sensitive path" {
		t.Fatalf("reject = %v/%v/%v", err, reject, reason)
	}

	reject, _, err = hook.BeforeToolExecute(ctx, "safe",
		[]byte(`{"path":"docs"}`))
	if err != nil || reject {
		t.Fatalf("allow = %v/%v", err, reject)
	}
}

func TestScriptToolInvoke(t *testing.T) {
	set := newTestSet(t, map[string]string{
		"tools.js": `
			tool({name: "shouty",
			      description: "Uppercase a string",
			      parameters: {type: "object",
			                   properties: {text: {type: "string"}},
			                   required: ["text"]}},
			     function(args) {
			       return {shouted: args.text.toUpperCase()};
			     });
			tool({name: "add",
			      description: "Add two numbers",
			      parameters: {type: "object"}},
			     function(args) { return args.a + args.b; });
		`,
	})
	tools := set.ScriptTools()
	if len(tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(tools))
	}

	decl := tools[0].Declaration()
	if decl.Name != "shouty" || decl.Description != "Uppercase a string" {
		t.Fatalf("declaration = %q/%q", decl.Name, decl.Description)
	}
	if decl.InputSchema == nil ||
		decl.InputSchema.Properties["text"] == nil {
		t.Fatal("parameters schema not carried over")
	}

	out, err := tools[0].(tool.CallableTool).Call(context.Background(),
		[]byte(`{"text":"hello"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	m, ok := out.(map[string]interface{})
	if !ok || m["shouted"] != "HELLO" {
		t.Fatalf("out = %#v, want shouted=HELLO", out)
	}

	// Numeric result (non-string return values are preserved).
	out, err = tools[1].(tool.CallableTool).Call(context.Background(), []byte(`{"a":2,"b":3}`))
	if err != nil {
		t.Fatalf("Call(add): %v", err)
	}
	// Numeric result: goja exports whole JS numbers as int64.
	var num float64
	switch v := out.(type) {
	case int64:
		num = float64(v)
	case float64:
		num = v
	default:
		t.Fatalf("add out type = %T, want number", out)
	}
	if num != 5 {
		t.Fatalf("add out = %v, want 5", num)
	}

	// Capability bus round-trip.
	reg := capability.NewRegistry()
	set.SyncScriptTools(reg)
	cap, ok := reg.Resolve("script.shouty")
	if !ok {
		t.Fatal("script.shouty not registered")
	}
	if got := cap.Descriptor().Source; got != capability.SourceScript {
		t.Errorf("source = %q, want script", got)
	}
	raw, err := cap.Invoke(context.Background(), []byte(`{"text":"go"}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !strings.Contains(string(raw), "GO") {
		t.Errorf("raw = %s, want GO", raw)
	}
}

func TestScriptToolHandlerErrorSurfaces(t *testing.T) {
	set := newTestSet(t, map[string]string{
		"tools.js": `
			tool({name: "fragile", description: "throws"},
			     function(args) { throw new Error("kaboom"); });
		`,
	})
	_, err := set.ScriptTools()[0].(tool.CallableTool).Call(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("err = %v, want kaboom", err)
	}
}

func TestLoadEmptyAndMissingDir(t *testing.T) {
	empty := t.TempDir()
	set, err := Load(empty, time.Second)
	if err != nil || set.Len() != 0 || len(set.ScriptTools()) != 0 {
		t.Fatalf("empty dir: %v/%d", err, set.Len())
	}
	missing := filepath.Join(empty, "nope")
	set, err = Load(missing, time.Second)
	if err != nil || set.Len() != 0 {
		t.Fatalf("missing dir: %v/%d", err, set.Len())
	}
}

func TestResolveDirFallback(t *testing.T) {
	got := ResolveDir("")
	if !strings.HasSuffix(got, filepath.Join(".wukong", "hooks")) {
		t.Errorf("ResolveDir(\"\") = %q, want .wukong/hooks fallback", got)
	}
}
