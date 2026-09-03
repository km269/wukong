// adapters.go — bridges loaded scripts into the agent hook
// interfaces and the capability bus (roadmap P1-4).
package scripthook

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dop251/goja"
	"github.com/km269/wukong/internal/agent"
	"github.com/km269/wukong/internal/capability"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// PreStepHooks returns a PreStepHook adapter per script that
// defines beforeStep, in load (file) order.
func (s *ScriptHookSet) PreStepHooks() []agent.PreStepHook {
	out := make([]agent.PreStepHook, 0, len(s.scripts))
	for _, ls := range s.scripts {
		if ls.hasPreStep {
			out = append(out, &preStepAdapter{set: s, script: ls})
		}
	}
	return out
}

// PreToolHooks returns a PreToolExecuteHook adapter per script
// that defines beforeTool, in load (file) order.
func (s *ScriptHookSet) PreToolHooks() []agent.PreToolExecuteHook {
	out := make([]agent.PreToolExecuteHook, 0, len(s.scripts))
	for _, ls := range s.scripts {
		if ls.hasPreTool {
			out = append(out, &preToolAdapter{set: s, script: ls})
		}
	}
	return out
}

// ScriptTools returns every tool() registration across scripts as
// trpc callable tools (LLM-visible name: the declared tool name).
func (s *ScriptHookSet) ScriptTools() []tool.Tool {
	out := make([]tool.Tool, 0)
	for _, ls := range s.scripts {
		for _, def := range ls.tools {
			out = append(out, &scriptTool{set: s, script: ls, def: def})
		}
	}
	return out
}

// SyncScriptTools registers every script tool in reg under
// "script.<name>" (reload-safe namespace re-sync, mirroring the
// recipe/flow helpers).
func (s *ScriptHookSet) SyncScriptTools(reg *capability.Registry) {
	reg.UnregisterNamespace(scriptNamespace)
	tools := s.ScriptTools()
	for _, t := range tools {
		decl := t.Declaration()
		addr := scriptNamespace + "." + strings.TrimPrefix(decl.Name, "script-")
		c, err := capability.FromToolMeta(
			addr, capability.SourceScript, t, capability.ToolMeta{})
		if err != nil {
			util.Logger.Warn("scripthook: tool adapter failed",
				"address", addr, "error", err.Error())
			continue
		}
		if err := reg.Register(c); err != nil {
			util.Logger.Warn("scripthook: tool registration skipped",
				"address", addr, "error", err.Error())
			continue
		}
	}
	util.Logger.Info("scripthook: script tools synced", "count", len(tools))
}

// callFunction evaluates the script fresh and invokes the named
// global function with a single argument, returning its exported
// value.
func (s *ScriptHookSet) callFunction(
	ctx context.Context, ls *loadedScript, name string, arg interface{},
) (interface{}, error) {
	var exported interface{}
	_, err := runSandboxed(ctx, ls.source, s.timeout,
		func(vm *goja.Runtime) (goja.Value, error) {
			fnVal := vm.Get(name)
			fn, ok := goja.AssertFunction(fnVal)
			if !ok {
				return nil, fmt.Errorf("function %q not defined", name)
			}
			res, err := fn(goja.Undefined(), vm.ToValue(arg))
			if err != nil {
				return nil, err
			}
			if res == nil || res == goja.Undefined() || res == goja.Null() {
				return nil, nil
			}
			exported = res.Export()
			return res, nil
		})
	if err != nil {
		return nil, err
	}
	return exported, nil
}

// ---------------------------------------------------------------------------
// PreStepHook adapter
// ---------------------------------------------------------------------------

type preStepAdapter struct {
	set    *ScriptHookSet
	script *loadedScript
}

func (a *preStepAdapter) Name() string { return "script:" + a.script.name }

// BeforeStep invokes beforeStep({session_id, user_id, message}).
// Fail-open contract: script exceptions, timeouts, and malformed
// results are logged and pass the message through unchanged.
func (a *preStepAdapter) BeforeStep(
	ctx context.Context,
	sessionID, userID string,
	msg model.Message,
) (model.Message, bool, error) {
	result, err := a.set.callFunction(ctx, a.script, "beforeStep", map[string]interface{}{
		"session_id": sessionID,
		"user_id":    userID,
		"message":    map[string]interface{}{"role": string(msg.Role), "content": msg.Content},
	})
	if err != nil {
		util.Logger.Warn("scripthook: beforeStep failed, failing open",
			"script", a.script.name, "error", err.Error())
		return msg, false, nil
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		return msg, false, nil
	}
	if _, rejected := verdict(m); rejected {
		return msg, true, nil
	}
	if rewrite, ok := m["rewrite"].(string); ok && rewrite != msg.Content {
		out := msg
		out.Content = rewrite
		return out, false, nil
	}
	return msg, false, nil
}

// ---------------------------------------------------------------------------
// PreToolExecuteHook adapter
// ---------------------------------------------------------------------------

type preToolAdapter struct {
	set    *ScriptHookSet
	script *loadedScript
}

func (a *preToolAdapter) Name() string { return "script:" + a.script.name }

// BeforeToolExecute invokes beforeTool({tool_name, args}). Same
// fail-open contract as BeforeStep.
func (a *preToolAdapter) BeforeToolExecute(
	ctx context.Context, toolName string, args []byte,
) (bool, string, error) {
	var parsed interface{}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &parsed)
	}
	result, err := a.set.callFunction(ctx, a.script, "beforeTool", map[string]interface{}{
		"tool_name": toolName,
		"args":      parsed,
	})
	if err != nil {
		util.Logger.Warn("scripthook: beforeTool failed, failing open",
			"script", a.script.name, "error", err.Error())
		return false, "", nil
	}
	if m, ok := result.(map[string]interface{}); ok {
		if reason, rejected := verdict(m); rejected {
			util.Logger.Info("scripthook: beforeTool rejected tool call",
				"script", a.script.name,
				"tool", toolName,
				"reason", reason)
			return true, reason, nil
		}
	}
	return false, "", nil
}

// verdict extracts {reject, reason} from a hook result.
func verdict(m map[string]interface{}) (string, bool) {
	if b, ok := m["reject"].(bool); ok && b {
		reason, _ := m["reason"].(string)
		if reason == "" {
			reason = "rejected by script hook"
		}
		return reason, true
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Script tools
// ---------------------------------------------------------------------------

type scriptTool struct {
	set    *ScriptHookSet
	script *loadedScript
	def    scriptToolDef
}

// Declaration rebuilds the tool manifest entry from the options
// captured at load time.
func (t *scriptTool) Declaration() *tool.Declaration {
	decl := &tool.Declaration{
		Name:        t.def.name,
		Description: t.def.description,
		InputSchema: &tool.Schema{Type: "object"},
	}
	if len(t.def.parameters) > 0 {
		var s tool.Schema
		if err := json.Unmarshal(t.def.parameters, &s); err == nil {
			decl.InputSchema = &s
		}
	}
	return decl
}

// Call re-evaluates the script in a fresh runtime, picks the
// handler by registration index (straight-line scripts register
// deterministically), and invokes it with the decoded arguments.
// Unlike hooks, tool failures surface as call errors.
func (t *scriptTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	var args interface{}
	if len(jsonArgs) > 0 {
		if err := json.Unmarshal(jsonArgs, &args); err != nil {
			return nil, fmt.Errorf("decode arguments: %w", err)
		}
	}

	var result interface{}
	var handlers []goja.Value
	_, err := runSandboxed(ctx, t.script.source, t.set.timeout,
		func(vm *goja.Runtime) (goja.Value, error) {
			var throwaway []scriptToolDef
			installCollector(vm, &throwaway, &handlers)
			if _, err := vm.RunString(t.script.source); err != nil {
				return nil, err
			}
			if t.def.index >= len(handlers) {
				return nil, fmt.Errorf(
					"tool handler %q not registered (index %d)",
					t.def.name, t.def.index)
			}
			fn, ok := goja.AssertFunction(handlers[t.def.index])
			if !ok {
				return nil, fmt.Errorf(
					"tool handler %q is not a function", t.def.name)
			}
			res, err := fn(goja.Undefined(), vm.ToValue(args))
			if err != nil {
				return nil, err
			}
			if res == nil || res == goja.Undefined() || res == goja.Null() {
				return goja.Undefined(), nil
			}
			result = res.Export()
			return res, nil
		})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ResolveDir resolves the script hooks directory to an absolute
// path, expanding ~ and falling back to .wukong/hooks relative to
// the working directory.
func ResolveDir(dir string) string {
	if dir == "" {
		dir = filepath.Join(".wukong", "hooks")
	}
	if strings.HasPrefix(dir, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, dir[1:])
		}
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}
