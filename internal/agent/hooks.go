// Package agent — hooks.go
//
// HookRegistry implements a lightweight waterfall extension model
// inspired by deepseek-harness's agent/pre-step and tools/pre-execute
// events. The framework's tool.Callbacks only allows before/after
// observation plus reject-by-error; it cannot rewrite the message
// the model is about to see, nor can it intercept at the step
// boundary (one model request + its tool calls).
//
// PreStepHook fills that gap: it runs AFTER context enrichment and
// BEFORE runner.Run, can rewrite the claimed message, or reject the
// turn entirely (which still records a turn_end, matching dsh's
// "a rejected first claim closes a durable turn that spent no
// step, so the log records the attempt").
//
// PreToolExecuteHook wraps the framework's BeforeTool callback to
// provide a registry-based observe/reject layer. Arg-rewriting is
// not exposed here because the framework's BeforeToolResult type
// does not support it; future versions may add a tool-wrapper path
// similar to newTimeoutTool when arg rewriting becomes necessary.
package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// PreStepHook intercepts a turn at the step boundary — after the
// claimed user message has been enriched but before it is handed to
// the runner. Hooks are chained as a waterfall: each receives the
// output (possibly rewritten) of the previous hook.
//
// Return values:
//   - rewritten: the (possibly modified) message to forward to the
//     next hook and ultimately to the model.
//   - reject: when true, the turn is closed with no step. The
//     reason is logged and recorded as a turn_end event. Subsequent
//     hooks are NOT called.
//   - err: a hard error aborts the chain and propagates to Run.
type PreStepHook interface {
	Name() string
	BeforeStep(
		ctx context.Context,
		sessionID, userID string,
		msg model.Message,
	) (rewritten model.Message, reject bool, err error)
}

// PreToolExecuteHook intercepts a tool call before execution. It is
// a registry-based observe/reject layer wired through the
// framework's tool.Callbacks.BeforeTool. Multiple hooks are called
// in registration order; the first to return reject=true blocks the
// call with its reason.
//
// Note: this interface cannot rewrite tool arguments because the
// underlying framework callback type does not expose arg mutation.
// Use a tool wrapper (see newTimeoutTool) for that level of control.
type PreToolExecuteHook interface {
	Name() string
	BeforeToolExecute(
		ctx context.Context,
		toolName string,
		args []byte,
	) (reject bool, reason string, err error)
}

// PreStepFunc is a convenience adapter allowing a plain function to
// satisfy PreStepHook.
type PreStepFunc struct {
	N string
	F func(
		ctx context.Context,
		sessionID, userID string,
		msg model.Message,
	) (model.Message, bool, error)
}

func (h PreStepFunc) Name() string { return h.N }
func (h PreStepFunc) BeforeStep(
	ctx context.Context,
	sessionID, userID string,
	msg model.Message,
) (model.Message, bool, error) {
	return h.F(ctx, sessionID, userID, msg)
}

// HookRegistry holds registered PreStep and PreToolExecute hooks.
// Dispatch is waterfall for pre-step (each hook receives the
// previous's output) and first-reject for pre-tool-execute.
type HookRegistry struct {
	mu      sync.RWMutex
	preStep []PreStepHook
	preTool []PreToolExecuteHook
}

// NewHookRegistry creates an empty registry.
func NewHookRegistry() *HookRegistry {
	return &HookRegistry{}
}

// RegisterPreStep appends a pre-step hook to the waterfall chain.
// Hooks run in registration order. Returns the registry for chaining.
func (r *HookRegistry) RegisterPreStep(h PreStepHook) *HookRegistry {
	if h == nil {
		return r
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.preStep = append(r.preStep, h)
	return r
}

// RegisterPreToolExecute appends a pre-tool-execute hook.
// Returns the registry for chaining.
func (r *HookRegistry) RegisterPreToolExecute(h PreToolExecuteHook) *HookRegistry {
	if h == nil {
		return r
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.preTool = append(r.preTool, h)
	return r
}

// HasPreStepHooks reports whether any pre-step hooks are registered.
func (r *HookRegistry) HasPreStepHooks() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.preStep) > 0
}

// HasPreToolHooks reports whether any pre-tool-execute hooks are
// registered. Used to skip the dispatch hot path when empty.
func (r *HookRegistry) HasPreToolHooks() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.preTool) > 0
}

// RunPreStep executes the pre-step waterfall over the claimed
// message. Each hook receives the output of the previous one. The
// first hook to return reject=true stops the chain and returns
// (msg, true, reason, nil). A hard error from any hook aborts the
// chain and propagates as the returned error.
//
// When the registry is empty, returns the message unchanged.
func (r *HookRegistry) RunPreStep(
	ctx context.Context,
	sessionID, userID string,
	msg model.Message,
) (model.Message, bool, string, error) {
	if r == nil {
		return msg, false, "", nil
	}
	r.mu.RLock()
	hooks := make([]PreStepHook, len(r.preStep))
	copy(hooks, r.preStep)
	r.mu.RUnlock()

	cur := msg
	for _, h := range hooks {
		rewritten, reject, err := h.BeforeStep(ctx, sessionID, userID, cur)
		if err != nil {
			return cur, false, "", fmt.Errorf(
				"pre-step hook %q: %w", h.Name(), err,
			)
		}
		if reject {
			util.Logger.Info("pre-step hook rejected turn",
				"hook", h.Name(),
				"session", sessionID,
			)
			return rewritten, true, h.Name(), nil
		}
		cur = rewritten
	}
	return cur, false, "", nil
}

// RunPreToolExecute executes the pre-tool-execute chain. Returns
// (reject, reason, err). The first hook to return reject=true wins;
// subsequent hooks are not called. A hard error from any hook
// propagates as the returned error.
//
// When the registry is empty, returns (false, "", nil).
func (r *HookRegistry) RunPreToolExecute(
	ctx context.Context,
	toolName string,
	args []byte,
) (bool, string, error) {
	if r == nil {
		return false, "", nil
	}
	r.mu.RLock()
	hooks := make([]PreToolExecuteHook, len(r.preTool))
	copy(hooks, r.preTool)
	r.mu.RUnlock()

	for _, h := range hooks {
		reject, reason, err := h.BeforeToolExecute(ctx, toolName, args)
		if err != nil {
			return false, "", fmt.Errorf(
				"pre-tool hook %q: %w", h.Name(), err,
			)
		}
		if reject {
			return true, reason, nil
		}
	}
	return false, "", nil
}
