package agent

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
)

type evolutionTracker struct{}

func newEvolutionTracker() plugin.Plugin {
	return &evolutionTracker{}
}

func (t *evolutionTracker) Name() string {
	return "evolution_tracker"
}

func (t *evolutionTracker) Register(reg *plugin.Registry) {
	reg.BeforeAgent(t.beforeAgent)
	reg.OnEvent(func(ctx context.Context, inv *agent.Invocation, e *event.Event) (*event.Event, error) {
		return t.onEvent(ctx, inv, e)
	})
}

func (t *evolutionTracker) beforeAgent(
	ctx context.Context,
	args *agent.BeforeAgentArgs,
) (*agent.BeforeAgentResult, error) {
	if args.Invocation != nil {
		args.Invocation.SetState("evo_start_at", timeNow())
		args.Invocation.SetState("evo_llm_calls", 0)
		args.Invocation.SetState("evo_tool_call_count", 0)
		args.Invocation.SetState("evo_tool_calls", []map[string]string{})
	}
	return nil, nil
}

func (t *evolutionTracker) onEvent(
	ctx context.Context,
	inv *agent.Invocation,
	e *event.Event,
) (*event.Event, error) {
	if inv == nil {
		return e, nil
	}

	if e.Response != nil && len(e.Response.Choices) > 0 {
		choice := e.Response.Choices[0]

		if len(choice.Message.ToolCalls) > 0 {
			if val, ok := inv.GetState("evo_tool_call_count"); ok {
				if count, ok := val.(int); ok {
					inv.SetState("evo_tool_call_count", count+len(choice.Message.ToolCalls))
				}
			}

			for _, tc := range choice.Message.ToolCalls {
				t.addToolCall(inv, tc.Function.Name, string(tc.Function.Arguments))
			}
		}

		if choice.Message.Role == "assistant" {
			if val, ok := inv.GetState("evo_llm_calls"); ok {
				if count, ok := val.(int); ok {
					inv.SetState("evo_llm_calls", count+1)
				}
			}
		}
	}

	return e, nil
}

func (t *evolutionTracker) addToolCall(inv *agent.Invocation, name, args string) {
	if val, ok := inv.GetState("evo_tool_calls"); ok {
		if calls, ok := val.([]map[string]string); ok {
			calls = appendToolCall(calls, name, args)
			inv.SetState("evo_tool_calls", calls)
			return
		}
	}
	calls := []map[string]string{
		{"name": name, "args": args},
	}
	inv.SetState("evo_tool_calls", calls)
}

func appendToolCall(calls []map[string]string, name, args string) []map[string]string {
	call := map[string]string{
		"name": name,
		"args": args,
	}
	return append(calls, call)
}

func timeNow() int64 {
	return time.Now().UnixNano()
}
