// Code split out of loop.go (P2-8) - same package, zero behavior change.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/km269/wukong/internal/capability"
	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/provider"
	"github.com/km269/wukong/internal/security"
	"github.com/km269/wukong/internal/util"
	"log/slog"
	"strings"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/promptinjection"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/promptinjection/review"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// buildAgentCallbacks creates agent-level callbacks for observability.
// These fire before and after each agent run, providing hooks for
// logging, metrics collection, and security auditing.
func buildAgentCallbacks(cfg *config.WukongConfig) *agent.Callbacks {
	if cfg == nil {
		return nil
	}
	callbacks := agent.NewCallbacks()
	// BeforeAgent: log the invocation start
	callbacks.RegisterBeforeAgent(
		func(ctx context.Context, args *agent.BeforeAgentArgs) (
			*agent.BeforeAgentResult, error,
		) {
			if args != nil && args.Invocation != nil {
				util.Logger.Debug("agent run starting",
					slog.String("invocation_id",
						args.Invocation.InvocationID),
				)
			}
			return nil, nil
		},
	)
	// AfterAgent: log completion and track metrics
	callbacks.RegisterAfterAgent(
		func(ctx context.Context, args *agent.AfterAgentArgs) (
			*agent.AfterAgentResult, error,
		) {
			if args != nil && args.Invocation != nil {
				util.Logger.Debug("agent run completed",
					slog.String("invocation_id",
						args.Invocation.InvocationID),
				)
				if args.Error != nil {
					util.Logger.Warn("agent run error",
						slog.String("invocation_id",
							args.Invocation.InvocationID),
						slog.String("error",
							args.Error.Error()),
					)
				}
			}
			return nil, nil
		},
	)
	return callbacks
}

// effectiveToolSetsTools flattens the tools of the effective
// toolsets — used as the deferred population for the toolsearch
// plugin.
// buildToolCallbacks creates tool-level callbacks for security and
// observability. The security guard checks are performed here
// as a framework-level concern rather than in business logic.
// The hooks registry adds an extensible pre-tool-execute layer
// (observe/reject) on top of the built-in security gate; security
// runs first as a hard gate, then registered hooks run in order and
// the first to reject blocks the call.
func buildToolCallbacks(
	guard *security.Guard, hooks *HookRegistry,
	caps *capability.Registry, validationMode string,
) *tool.Callbacks {
	callbacks := tool.NewCallbacks()

	// BeforeTool: security validation before tool execution
	callbacks.RegisterBeforeTool(
		func(ctx context.Context, args *tool.BeforeToolArgs) (
			*tool.BeforeToolResult, error,
		) {
			if guard != nil {
				// Check tool permission (denylist, allowlist, permission mode)
				if err := guard.CheckToolPermission(
					args.ToolName, nil,
				); err != nil {
					return nil, fmt.Errorf(
						"tool %q blocked by security: %w",
						args.ToolName, err,
					)
				}

				// Check if this operation needs user approval. When an
				// approval broker is wired on the guard, route through
				// the async human-in-the-loop Approval protocol; when no
				// broker is set, RequestApproval returns an immediate
				// denied response (legacy synchronous-deny behavior, so
				// safety never regresses when the feature is off).
				if guard.NeedsApproval(args.ToolName, args.Arguments) {
					sessionID, userID := security.ApprovalContextFrom(ctx)
					resp, aerr := guard.RequestApproval(
						ctx, sessionID, userID,
						args.ToolName, args.Arguments,
					)
					if aerr != nil {
						return nil, fmt.Errorf(
							"approval for %q failed: %w",
							args.ToolName, aerr,
						)
					}
					if resp == nil || !resp.Decision.IsAllowed() {
						decision := security.ApprovalDenied
						reason := "no approval broker configured"
						if resp != nil {
							decision = resp.Decision
							reason = resp.Reason
						}
						return nil, fmt.Errorf(
							"tool %q blocked by approval: %s (%s)",
							args.ToolName, decision, reason,
						)
					}
					// approved → fall through to command/file checks.
				}

				// For command-execution tools, validate the command.
				// Scope declarations are authoritative; undeclared
				// tools follow the configured fallback mode (see
				// commandToolNeedsValidation).
				if commandToolNeedsValidation(
					caps, validationMode, args.ToolName,
				) && len(args.Arguments) > 0 {
					cmd := extractCommandFromArgs(args.Arguments)
					if cmd != "" {
						if err := guard.ValidateCommand(cmd); err != nil {
							return nil, fmt.Errorf(
								"command blocked by security: %w", err,
							)
						}
					}
				}

				// For file-access tools, check against .wukongignore
				if security.IsFileAccessTool(args.ToolName) &&
					len(args.Arguments) > 0 {
					paths := security.ExtractFilePathFromArgs(
						args.Arguments)
					for _, p := range paths {
						if err := guard.CheckFilePath(p); err != nil {
							return nil, fmt.Errorf(
								"file access blocked by "+
									".wukongignore: %w", err,
							)
						}
					}
				}
			}

			// Pre-tool-execute hooks: extensible observe/reject
			// layer. Runs after the security hard gate. The first
			// hook to return reject=true blocks the call with its
			// reason. This mirrors dsh's tools/pre-execute seam.
			if hooks != nil && hooks.HasPreToolHooks() {
				reject, reason, err := hooks.RunPreToolExecute(
					ctx, args.ToolName, args.Arguments,
				)
				if err != nil {
					return nil, fmt.Errorf(
						"pre-tool hook error for %q: %w",
						args.ToolName, err,
					)
				}
				if reject {
					return nil, fmt.Errorf(
						"tool %q blocked by pre-tool hook: %s",
						args.ToolName, reason,
					)
				}
			}

			return nil, nil
		},
	)

	// AfterTool: result size monitoring and truncation
	callbacks.RegisterAfterTool(
		func(ctx context.Context, args *tool.AfterToolArgs) (
			*tool.AfterToolResult, error,
		) {
			if guard == nil {
				return nil, nil
			}

			// Monitor tool execution errors for security events
			if args.Error != nil {
				return nil, nil
			}

			return nil, nil
		},
	)

	return callbacks
}

// isCommandTool checks if a tool name corresponds to a command execution tool.
// isCommandTool checks if a tool name corresponds to a command execution tool.
func isCommandTool(toolName string) bool {
	commandTools := []string{
		"bash", "execute_command", "run_command",
		"shell", "terminal", "command",
		"command_execute",
		"developer_command_execute", // developer extension (ToolSet prefix)
	}
	for _, t := range commandTools {
		if strings.EqualFold(toolName, t) {
			return true
		}
	}
	return false
}

// scopeShell is the permission scope that marks a capability as a
// command-execution surface (declared in builtin/scopes.go).
// scopeShell is the permission scope that marks a capability as a
// command-execution surface (declared in builtin/scopes.go).
const scopeShell = "shell"

// Command validation modes (agent.command_validation_mode, Phase C).
// commandToolNeedsValidation reports whether a tool call's command
// string must pass Guard.ValidateCommand (roadmap P0-1). The mode
// comes from agent.command_validation_mode ("" = hybrid):
//
//   - hybrid: capability scope declarations are authoritative
//     (declared non-shell tools are exempt from the heuristic);
//     undeclared tools fall back to the legacy isCommandTool
//     heuristic so external MCP tools keep coverage.
//   - descriptor: declarations only; undeclared tools never
//     validate. For setups that declare scopes for every
//     command-executing extension (external tools declare via
//     extensions[].tool_scopes).
//   - heuristic: legacy name matching only; declarations ignored.
func commandToolNeedsValidation(
	caps *capability.Registry, mode, toolName string,
) bool {
	switch mode {
	case CommandValidationHeuristic:
		return isCommandTool(toolName)
	case CommandValidationDescriptor:
		if caps != nil {
			if c, ok := caps.ResolveByName(toolName); ok {
				for _, s := range c.Descriptor().Scopes {
					if s == scopeShell {
						return true
					}
				}
			}
		}
		return false
	default: // hybrid
		if caps != nil {
			if c, ok := caps.ResolveByName(toolName); ok {
				scopes := c.Descriptor().Scopes
				if len(scopes) > 0 {
					for _, s := range scopes {
						if s == scopeShell {
							return true
						}
					}
					return false
				}
			}
		}
		return isCommandTool(toolName)
	}
}

// extractCommandFromArgs extracts a command string from tool arguments JSON.
// extractCommandFromArgs extracts a command string from tool arguments JSON.
func extractCommandFromArgs(args []byte) string {
	// Try to extract common command field names from JSON
	var data map[string]any
	if err := json.Unmarshal(args, &data); err != nil {
		return ""
	}
	for _, key := range []string{"command", "cmd", "shell", "script"} {
		if val, ok := data[key]; ok {
			if s, ok := val.(string); ok {
				return s
			}
		}
	}
	return ""
}

// buildModelCallbacks creates model-level callbacks for token usage
// tracking and cost estimation.
// buildModelCallbacks creates model-level callbacks for token usage
// tracking and cost estimation.
func buildModelCallbacks() *model.Callbacks {
	callbacks := model.NewCallbacks()
	// BeforeModel: request-level pre-processing
	callbacks.RegisterBeforeModel(
		func(ctx context.Context, args *model.BeforeModelArgs) (
			*model.BeforeModelResult, error,
		) {
			if args != nil && args.Request != nil {
				util.Logger.Debug("model request",
					slog.Int("message_count",
						len(args.Request.Messages)),
				)
			}
			return nil, nil
		},
	)
	// AfterModel: response-level post-processing and metrics
	callbacks.RegisterAfterModel(
		func(ctx context.Context, args *model.AfterModelArgs) (
			*model.AfterModelResult, error,
		) {
			if args != nil && args.Response != nil {
				util.Logger.Debug("model response",
					slog.String("model",
						args.Response.Model),
				)
				// Track token usage if available
				if args.Response.Usage != nil {
					util.Logger.Debug("token usage",
						slog.Int("prompt_tokens",
							args.Response.Usage.PromptTokens),
						slog.Int("completion_tokens",
							args.Response.Usage.CompletionTokens),
						slog.Int("total_tokens",
							args.Response.Usage.TotalTokens),
					)
				}
			}
			return nil, nil
		},
	)
	return callbacks
}

// buildGuardrailPlugin assembles the prompt-injection guardrail
// runner plugin: a dedicated lightweight model → runner → reviewer →
// prompt-injection detector → guardrail plugin. It returns nil (with a
// warning log) if any step fails, since guardrail is a non-fatal
// enhancement — the agent still runs without it.
//
// This flattens what was previously a 6-level-deep `if err == nil`
// chain in NewCoreLoop into early returns.
// buildGuardrailPlugin assembles the prompt-injection guardrail
// runner plugin: a dedicated lightweight model → runner → reviewer →
// prompt-injection detector → guardrail plugin. It returns nil (with a
// warning log) if any step fails, since guardrail is a non-fatal
// enhancement — the agent still runs without it.
//
// This flattens what was previously a 6-level-deep `if err == nil`
// chain in NewCoreLoop into early returns.
func buildGuardrailPlugin(factory *provider.Factory) plugin.Plugin {
	mdl, err := factory.CreateDefaultModel()
	if err != nil || mdl == nil {
		util.Logger.Warn("guardrail: model unavailable, plugin disabled",
			slog.String("error", errOrEmpty(err)))
		return nil
	}

	reviewAgent := llmagent.New("guardrail-reviewer",
		llmagent.WithModel(mdl),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   util.IntPtr(256),
			Temperature: util.Float64Ptr(0.0),
			Stream:      false,
		}),
		llmagent.WithMaxLLMCalls(1),
	)
	guardRunner := runner.NewRunner("wukong-guardrail", reviewAgent)

	piReviewer, err := review.New(guardRunner)
	if err != nil {
		util.Logger.Warn("guardrail: reviewer init failed, plugin disabled",
			slog.String("error", err.Error()))
		return nil
	}

	piPlugin, err := promptinjection.New(
		promptinjection.WithReviewer(piReviewer),
	)
	if err != nil {
		util.Logger.Warn("guardrail: prompt-injection plugin init failed",
			slog.String("error", err.Error()))
		return nil
	}

	grPlugin, err := guardrail.New(
		guardrail.WithPromptInjection(piPlugin),
	)
	if err != nil {
		util.Logger.Warn("guardrail: guardrail plugin init failed",
			slog.String("error", err.Error()))
		return nil
	}
	return grPlugin
}

// errOrEmpty returns err.Error() or "" for a nil error. Used by
// buildGuardrailPlugin to format a possibly-nil model-creation error.
