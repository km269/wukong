// Package scripthook implements user JavaScript hooks (roadmap
// P1-4, the Yao "TS hooks" analogue).
//
// Every .js file in the hooks directory (agent.script_hooks_dir,
// default .wukong/hooks) may define:
//
//	function beforeStep(ctx) { ... }   // PreStepHook: runs after
//	    // context enrichment, before the model call. ctx =
//	    // {session_id, user_id, message:{role, content}}. Return
//	    // undefined to pass through, {rewrite: "..."} to replace the
//	    // message content, or {reject: true, reason: "..."} to close
//	    // the turn without spending a step.
//	function beforeTool(ctx) { ... }   // PreToolExecuteHook: runs
//	    // before each tool execution. ctx = {tool_name, args}.
//	    // Return undefined to allow, {reject: true, reason: "..."}
//	    // to block.
//	tool({name, description, parameters}, handler)  // register a
//	    // script tool: handler(args) receives the JSON-decoded
//	    // invocation arguments and returns any JSON-serializable
//	    // value. Tools surface as "script-<name>" LLM tools and
//	    // "script.<name>" capabilities.
//
// Sandbox model (same philosophy as internal/codemode): a fresh
// goja runtime per invocation, ECMAScript built-ins only — no
// filesystem, network, or process access. Each hook/tool call runs
// with a deadline (agent.script_hooks_timeout); a hit deadline
// interrupts the runtime. Hook failures (exception or timeout) fail
// OPEN — logged and skipped — because hooks are user-authored
// extensions in the user's own trust domain and must not brick the
// agent; script tool failures surface to the LLM as call errors.
package scripthook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/km269/wukong/internal/util"
)

const (
	// maxScriptSize caps one hook script file.
	maxScriptSize = 256 << 10 // 256 KB

	// scriptNamespace is the capability-bus namespace for script
	// tools.
	scriptNamespace = "script"
)

// toolNameRe constrains script tool names to manifest-safe runes.
var toolNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ScriptHookSet is the loaded set of hook scripts.
type ScriptHookSet struct {
	scripts []*loadedScript
	timeout time.Duration
}

// loadedScript is one parsed hook file.
type loadedScript struct {
	name       string // file base name (no extension)
	source     string
	hasPreStep bool
	hasPreTool bool
	tools      []scriptToolDef
}

// scriptToolDef is a tool() registration collected at load time.
// The handler function is not retained — invocations re-evaluate
// the script in a fresh runtime and pick the handler by
// registration index (deterministic for straight-line scripts).
type scriptToolDef struct {
	index       int
	name        string
	description string
	parameters  json.RawMessage
}

// Load reads and probes every .js file in dir (sorted). Invalid
// files are warned and skipped. An empty or missing directory
// yields an empty (non-nil) set.
func Load(dir string, timeout time.Duration) (*ScriptHookSet, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	set := &ScriptHookSet{timeout: timeout}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return set, nil
		}
		return nil, fmt.Errorf("read hooks dir: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".js") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, fname := range names {
		path := filepath.Join(dir, fname)
		ls, err := loadScript(path)
		if err != nil {
			util.Logger.Warn("scripthook: skipping script",
				"path", path, "error", err.Error())
			continue
		}
		set.scripts = append(set.scripts, ls)
		util.Logger.Info("scripthook: loaded script",
			"name", ls.name,
			"pre_step", ls.hasPreStep,
			"pre_tool", ls.hasPreTool,
			"tools", len(ls.tools))
	}
	return set, nil
}

// loadScript reads one file and probes it in a throwaway runtime:
// hook function presence and tool() registrations are collected
// here; actual hook/tool invocations re-evaluate the script fresh.
func loadScript(path string) (*loadedScript, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) > maxScriptSize {
		return nil, fmt.Errorf("script exceeds %d bytes", maxScriptSize)
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	ls := &loadedScript{name: base, source: string(data)}

	var tools []scriptToolDef
	var handlers []goja.Value
	_, err = runSandboxed(context.Background(), ls.source, 10*time.Second,
		func(vm *goja.Runtime) (goja.Value, error) {
			installCollector(vm, &tools, &handlers)
			v, err := vm.RunString(ls.source)
			if err != nil {
				return v, err
			}
			ls.hasPreStep = isCallable(vm.Get("beforeStep"))
			ls.hasPreTool = isCallable(vm.Get("beforeTool"))
			return v, nil
		})
	if err != nil {
		return nil, fmt.Errorf("probe: %w", err)
	}
	ls.tools = tools
	return ls, nil
}

// isCallable reports whether v is an invocable function value.
func isCallable(v goja.Value) bool {
	if v == nil || v == goja.Undefined() || v == goja.Null() {
		return false
	}
	_, ok := goja.AssertFunction(v)
	return ok
}

// runSandboxed evaluates source in a fresh goja runtime on a
// dedicated goroutine, enforcing deadline. fn may run additional
// calls against the same runtime after evaluation.
func runSandboxed(
	ctx context.Context, source string, deadline time.Duration,
	fn func(vm *goja.Runtime) (goja.Value, error),
) (goja.Value, error) {
	type outcome struct {
		val goja.Value
		err error
	}
	done := make(chan outcome, 1)

	vm := goja.New()
	setupConsole(vm)
	vm.Set("tool", func(goja.FunctionCall) goja.Value { return goja.Undefined() })

	timer := time.AfterFunc(deadline, func() {
		vm.Interrupt("script deadline exceeded")
	})
	defer timer.Stop()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- outcome{nil, fmt.Errorf("js runtime panic: %v", r)}
			}
		}()
		val, err := vm.RunString(source)
		if err == nil && fn != nil {
			// fn may replace the value (handler invocation).
			val, err = fn(vm)
		}
		done <- outcome{val, err}
	}()

	select {
	case o := <-done:
		return o.val, o.err
	case <-ctx.Done():
		vm.Interrupt("cancelled")
		<-done // drain the interrupted goroutine
		return nil, ctx.Err()
	}
}

// setupConsole wires a minimal console (log/warn/error) to the
// structured logger. This is the whole API whitelist: goja ships
// ECMAScript built-ins only — no fs, net, or process objects exist.
func setupConsole(vm *goja.Runtime) {
	logger := func(level string) func(goja.FunctionCall) goja.Value {
		return func(call goja.FunctionCall) goja.Value {
			parts := make([]string, 0, len(call.Arguments))
			for _, a := range call.Arguments {
				parts = append(parts, a.String())
			}
			util.Logger.Info("scripthook: console."+level,
				"message", strings.Join(parts, " "))
			return goja.Undefined()
		}
	}
	console := vm.NewObject()
	_ = console.Set("log", logger("log"))
	_ = console.Set("warn", logger("warn"))
	_ = console.Set("error", logger("error"))
	_ = vm.Set("console", console)
}

// installCollector replaces the no-op tool() global with one that
// records registrations: metadata into out, handler function values
// into handlers (both valid only within the same runtime
// invocation — the slices belong to the caller's closure).
func installCollector(
	vm *goja.Runtime, out *[]scriptToolDef, handlers *[]goja.Value,
) {
	_ = vm.Set("tool", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			panic(vm.NewGoError(errors.New(
				"tool() requires (options, handler)")))
		}
		opts, ok := call.Arguments[0].Export().(map[string]interface{})
		if !ok {
			panic(vm.NewGoError(errors.New(
				"tool() options must be an object")))
		}
		name, _ := opts["name"].(string)
		if !toolNameRe.MatchString(name) {
			panic(vm.NewGoError(errors.New(
				"tool() name must match [A-Za-z0-9_-]+: " + name)))
		}
		desc, _ := opts["description"].(string)
		var parameters json.RawMessage
		if p, ok := opts["parameters"]; ok && p != nil {
			if b, err := json.Marshal(p); err == nil {
				parameters = b
			}
		}
		*out = append(*out, scriptToolDef{
			index:       len(*out),
			name:        name,
			description: desc,
			parameters:  parameters,
		})
		*handlers = append(*handlers, call.Arguments[1])
		return goja.Undefined()
	})
}

// PreStepScripts reports how many scripts define beforeStep.
func (s *ScriptHookSet) Len() int { return len(s.scripts) }
