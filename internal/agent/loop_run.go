// Code split out of loop.go (P2-8) - same package, zero behavior change.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/km269/wukong/internal/recall"
	"github.com/km269/wukong/internal/security"
	wksession "github.com/km269/wukong/internal/session"
	"github.com/km269/wukong/internal/util"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"log/slog"
	"strings"
	"time"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// preStepRejectTag is the event.Tag value that marks a pre-step
// rejection in the event stream. Consumers (TUI/gateway) inspect
// this to distinguish a policy rejection from a normal (possibly
// empty) completion, and read the rejecting hook name from
// Extensions["reject"].
const preStepRejectTag = "pre_step_reject"

// newPreStepRejectStream returns an event stream carrying a single
// rejection marker and then closes. The marker event has
// Tag=preStepRejectTag and the rejecting hook name in Extensions,
// with no Response content and no Error — so it flows through
// RunStream without triggering the error path or emitting model
// content. This implements dsh's "a rejected claim closes a durable
// turn that spent no step" semantic: the turn is normally closed
// (nil error) but carries a visible rejection marker.
// newPreStepRejectStream returns an event stream carrying a single
// rejection marker and then closes. The marker event has
// Tag=preStepRejectTag and the rejecting hook name in Extensions,
// with no Response content and no Error — so it flows through
// RunStream without triggering the error path or emitting model
// content. This implements dsh's "a rejected claim closes a durable
// turn that spent no step" semantic: the turn is normally closed
// (nil error) but carries a visible rejection marker.
func newPreStepRejectStream(reason string) <-chan *event.Event {
	ch := make(chan *event.Event, 1)
	reasonJSON, _ := json.Marshal(
		map[string]string{"reason": reason},
	)
	ch <- &event.Event{
		Author:    "system",
		Tag:       preStepRejectTag,
		Timestamp: time.Now(),
		Extensions: map[string]json.RawMessage{
			"reject": reasonJSON,
		},
	}
	close(ch)
	return ch
}

// IsPreStepReject reports whether an event is a pre-step rejection
// marker emitted by newPreStepRejectStream. Consumers use this to
// surface "blocked by policy" to the user instead of treating the
// empty stream as a silent no-op.
// IsPreStepReject reports whether an event is a pre-step rejection
// marker emitted by newPreStepRejectStream. Consumers use this to
// surface "blocked by policy" to the user instead of treating the
// empty stream as a silent no-op.
func IsPreStepReject(evt *event.Event) bool {
	return evt != nil && evt.Tag == preStepRejectTag
}

// PreStepRejectReason extracts the rejecting hook name from a
// pre-step rejection marker event. Returns "" if the event is not a
// rejection marker or carries no reason.
// PreStepRejectReason extracts the rejecting hook name from a
// pre-step rejection marker event. Returns "" if the event is not a
// rejection marker or carries no reason.
func PreStepRejectReason(evt *event.Event) string {
	if evt == nil || evt.Tag != preStepRejectTag {
		return ""
	}
	if evt.Extensions == nil {
		return ""
	}
	raw, ok := evt.Extensions["reject"]
	if !ok {
		return ""
	}
	var m struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	return m.Reason
}

// Run executes a single user message and returns the event stream.
// The returned channel emits events including tool calls, streaming
// content, and final completion.
// Run executes a single user message and returns the event stream.
// The returned channel emits events including tool calls, streaming
// content, and final completion.
func (l *CoreLoop) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
) (<-chan *event.Event, error) {
	l.mu.RLock()
	if l.closed {
		l.mu.RUnlock()
		return nil, fmt.Errorf("core loop is closed")
	}
	l.mu.RUnlock()

	// Create a trace span for this agent run
	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "agent.Run",
		trace.WithAttributes(
			attribute.String("user_id", userID),
			attribute.String("session_id", sessionID),
		),
	)
	defer span.End()

	// Apply context optimization before running
	ctx = l.contextMgr.PrepareContext(ctx, session.Key{
		AppName:   "wukong-app",
		UserID:    userID,
		SessionID: sessionID,
	})

	// Record the turn boundary and the ORIGINAL user message before
	// any enrichment. This enforces the "model-visible means logged"
	// invariant at the turn start: the raw input is durable. Logging
	// is best-effort — a log failure must never block the model.
	if l.modelEventLog != nil {
		origContent := extractMessageContent(message)
		if err := l.modelEventLog.Append(ctx, sessionID, userID,
			wksession.EventTurnStart, "", origContent); err != nil {
			util.Logger.Warn("model event log: turn_start failed",
				slog.String("error", err.Error()))
		}
		if err := l.modelEventLog.Append(ctx, sessionID, userID,
			wksession.EventUserMessage, "", origContent); err != nil {
			util.Logger.Warn("model event log: user_message failed",
				slog.String("error", err.Error()))
		}
	}

	// Store user message for recall.
	if l.recallStore != nil {
		content := extractMessageContent(message)
		msg := recall.ChatMessage{
			SessionID: sessionID,
			UserID:    userID,
			Role:      "user",
			Content:   content,
		}
		if l.cortexStore != nil {
			if err := l.cortexStore.StoreMessage(ctx, msg); err != nil {
				util.Logger.Warn("cortex: store user message failed",
					slog.String("error", err.Error()))
			}
		} else {
			if err := l.recallStore.StoreMessage(msg); err != nil {
				util.Logger.Warn("recall: store user message failed",
					slog.String("error", err.Error()))
			}
		}
	}

	// Record transcript in MemoryFlow for context enrichment.
	var wakeCtx string
	if l.memoryFlow != nil {
		content := extractMessageContent(message)
		util.Logger.Info("memoryflow: starting ingest and wakeup",
			"sess", sessionID[:min(8, len(sessionID))],
			"user", userID,
			"input_chars", len(content))

		if err := l.memoryFlow.IngestTurn(
			ctx, sessionID, userID, "user", content,
		); err != nil {
			util.Logger.Warn("memoryflow: ingest user turn failed",
				slog.String("error", err.Error()))
		} else {
			util.Logger.Debug("memoryflow: ingest user turn succeeded",
				"sess", sessionID[:min(8, len(sessionID))],
				"user", userID)
		}

		// [Fix 1] Build wake-up context from past conversations
		// and inject it into the message as additional system context.
		identity := "You are Wukong, an AI coding assistant."
		wc, wErr := l.memoryFlow.WakeUp(
			ctx, identity, content, sessionID, userID,
		)
		if wErr != nil {
			util.Logger.Warn("memoryflow: wakeup failed",
				"sess", sessionID[:min(8, len(sessionID))],
				slog.String("error", wErr.Error()))
		} else if wc == "" {
			util.Logger.Info("memoryflow: wakeup returned empty",
				"sess", sessionID[:min(8, len(sessionID))],
				"reason", "no prior conversation history yet")
		} else {
			wakeCtx = wc
			preview := wc
			if len(wakeCtx) > 200 {
				preview = wakeCtx[:200] + "..."
			}
			util.Logger.Info("memoryflow: wakeup injected successfully",
				"sess", sessionID[:min(8, len(sessionID))],
				"chars", len(wakeCtx),
				"preview", preview)
			// Record the wake-up context that will be injected into
			// the model-visible message (context_inject event).
			if l.modelEventLog != nil {
				if err := l.modelEventLog.Append(ctx, sessionID, userID,
					wksession.EventContextInject, "wakeup", wakeCtx); err != nil {
					util.Logger.Warn("model event log: context_inject wakeup failed",
						slog.String("error", err.Error()))
				}
			}
			if message.Role == model.RoleUser {
				message = model.Message{
					Role: "user",
					Content: fmt.Sprintf(
						"[Context from past conversations]\n%s\n\n"+
							"[Current user message]\n%s",
						wakeCtx, extractMessageContent(message),
					),
				}
				util.Logger.Debug("memoryflow: message updated with wakeup context",
					"total_chars", len(message.Content))
			}
		}
	}

	// [Fix 2] Actively search recall history and inject into context.
	// This ensures cross-session chat history is always available,
	// complementing the LLM-driven recall_search tool usage.
	if l.recallStore != nil {
		content := extractMessageContent(message)
		var searchResults []recall.SearchResult
		var searchErr error

		if l.cortexStore != nil {
			searchResults, searchErr = l.cortexStore.Search(ctx, content, userID, 5)
		} else {
			searchResults, searchErr = l.recallStore.Search(content, userID, 5)
		}

		if searchErr != nil {
			util.Logger.Warn("recall: search failed",
				"sess", sessionID[:min(8, len(sessionID))],
				slog.String("error", searchErr.Error()))
		} else if len(searchResults) > 0 {
			util.Logger.Info("recall: found relevant history",
				"sess", sessionID[:min(8, len(sessionID))],
				"count", len(searchResults))

			var recallCtx strings.Builder
			recallCtx.WriteString("[Relevant conversation history]\n")
			for i, result := range searchResults {
				if result.Preview != "" {
					fmt.Fprintf(&recallCtx, "%d. %s\n", i+1, result.Preview)
				}
			}

			// Record the recall context that will be injected
			// (context_inject event).
			if l.modelEventLog != nil {
				if err := l.modelEventLog.Append(ctx, sessionID, userID,
					wksession.EventContextInject, "recall",
					recallCtx.String()); err != nil {
					util.Logger.Warn("model event log: context_inject recall failed",
						slog.String("error", err.Error()))
				}
			}

			if message.Role == model.RoleUser {
				origContent := extractMessageContent(message)
				message = model.Message{
					Role: "user",
					Content: fmt.Sprintf("%s\n\n%s",
						recallCtx.String(), origContent,
					),
				}
				util.Logger.Debug("recall: message updated with search results",
					"total_chars", len(message.Content))
			}
		} else {
			util.Logger.Debug("recall: no relevant history found",
				"sess", sessionID[:min(8, len(sessionID))])
		}
	}

	// [Fix 4] Inject persistent memories from tRPC Memory store
	// into the conversation context before each agent run.
	// Deduplicates against MemoryFlow wake-up context to avoid
	// redundant information from being injected twice.
	if l.memoryService != nil {
		userKey := memory.UserKey{
			AppName: "wukong-app",
			UserID:  userID,
		}
		util.Logger.Info("memory: reading persistent memories",
			"sess", sessionID[:min(8, len(sessionID))],
			"user", userID,
			"limit", 5)

		memories, mErr := l.memoryService.ReadMemories(
			ctx, userKey, 5,
		)
		if mErr != nil {
			util.Logger.Warn("memory: read failed",
				"sess", sessionID[:min(8, len(sessionID))],
				slog.String("error", mErr.Error()))
		} else if len(memories) == 0 {
			util.Logger.Info("memory: no persistent memories found",
				"sess", sessionID[:min(8, len(sessionID))],
				"reason", "cold start — auto_extract not yet triggered")
		} else {
			util.Logger.Debug("memory: found persistent memories",
				"sess", sessionID[:min(8, len(sessionID))],
				"count", len(memories))
			for i, m := range memories {
				if m.Memory != nil && m.Memory.Memory != "" {
					memPreview := m.Memory.Memory
					if len(memPreview) > 100 {
						memPreview = memPreview[:100] + "..."
					}
					util.Logger.Debug("memory: memory entry",
						"index", i+1,
						"content", memPreview,
						"id", m.ID)
				}
			}

			var memCtx strings.Builder
			memCtx.WriteString(
				"[Remembered facts from previous conversations]\n")
			var deduped int
			var dedupedMemories []string
			idx := 1
			for _, m := range memories {
				if m.Memory == nil || m.Memory.Memory == "" {
					continue
				}
				// Skip memories that are already substantially
				// present in the MemoryFlow wake-up context.
				if wakeCtx != "" &&
					isMemoryDuplicated(m.Memory.Memory, wakeCtx) {
					deduped++
					dedupedMemories = append(dedupedMemories, m.Memory.Memory)
					continue
				}
				fmt.Fprintf(&memCtx, "%d. %s\n",
					idx, m.Memory.Memory)
				idx++
			}

			if deduped > 0 {
				for i, dm := range dedupedMemories {
					dmPreview := dm
					if len(dmPreview) > 100 {
						dmPreview = dmPreview[:100] + "..."
					}
					util.Logger.Info("memory: deduplicated (already in wakeup)",
						"sess", sessionID[:min(8, len(sessionID))],
						"index", i+1,
						"content", dmPreview)
				}
			}

			injected := idx - 1
			if injected == 0 {
				util.Logger.Info("memory: all memories already "+
					"covered by wake-up context",
					"sess", sessionID[:min(8, len(sessionID))],
					"total", len(memories),
					"deduped", deduped)
			} else {
				util.Logger.Info("memory: persistent memories injected",
					"sess", sessionID[:min(8, len(sessionID))],
					"injected", injected,
					"total", len(memories),
					"deduped", deduped,
					"memctx_chars", memCtx.Len())
				// Record the persistent-memory context that will be
				// injected (context_inject event).
				if l.modelEventLog != nil {
					if err := l.modelEventLog.Append(ctx, sessionID, userID,
						wksession.EventContextInject, "persistent",
						memCtx.String()); err != nil {
						util.Logger.Warn("model event log: context_inject persistent failed",
							slog.String("error", err.Error()))
					}
				}
				// Prepend persistent memories to the user message.
				if message.Role == model.RoleUser {
					origContent := extractMessageContent(message)
					message = model.Message{
						Role: "user",
						Content: fmt.Sprintf("%s\n\n[User message]\n%s",
							memCtx.String(), origContent,
						),
					}
					util.Logger.Debug("memory: message updated with persistent memories",
						"total_chars", len(message.Content))
				}
			}

			// [Optimization] Record memory references after injection to track usage frequency.
			// This helps the SmartCleanup algorithm prioritize frequently-used memories.
			if len(memories) > 0 {
				var injectedIDs []string
				for _, m := range memories {
					if m.Memory != nil && m.Memory.Memory != "" {
						if wakeCtx == "" || !isMemoryDuplicated(m.Memory.Memory, wakeCtx) {
							injectedIDs = append(injectedIDs, m.ID)
						}
					}
				}
				if len(injectedIDs) > 0 {
					go func(uk memory.UserKey, ids []string) {
						bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						if mm, ok := l.memoryService.(interface {
							BatchRecordMemoryReferences(context.Context, memory.UserKey, []string) error
						}); ok {
							if err := mm.BatchRecordMemoryReferences(bgCtx, uk, ids); err != nil {
								util.Logger.Debug("memory: record references failed",
									slog.String("error", err.Error()))
							}
						}
					}(userKey, injectedIDs)
				}
			}
		}
	}

	// Log final message structure for debugging
	finalContent := extractMessageContent(message)
	var contextTags []string
	if strings.Contains(finalContent, "[Context from past conversations]") {
		contextTags = append(contextTags, "wakeup")
	}
	if strings.Contains(finalContent, "[Remembered facts from previous conversations]") {
		contextTags = append(contextTags, "persistent")
	}
	if strings.Contains(finalContent, "[User message]") {
		contextTags = append(contextTags, "user_input")
	}
	util.Logger.Info("memory: final message context structure",
		"sess", sessionID[:min(8, len(sessionID))],
		"context_types", contextTags,
		"total_chars", len(finalContent))

	// Pre-step hooks: extensible waterfall over the enriched message.
	// Runs AFTER built-in enrichment (wakeup/recall/persistent) and
	// BEFORE runner.Run. A hook may rewrite the message the model is
	// about to see, or reject the turn. A rejected turn closes with
	// no step (no model request), matching dsh's "a rejected claim
	// closes a durable turn that spent no step" — we still record a
	// turn_end so the attempt is durable.
	if l.hooks != nil && l.hooks.HasPreStepHooks() {
		rewritten, reject, reason, herr := l.hooks.RunPreStep(
			ctx, sessionID, userID, message,
		)
		if herr != nil {
			span.SetStatus(codes.Error, herr.Error())
			span.RecordError(herr)
			return nil, fmt.Errorf("pre-step hooks: %w", herr)
		}
		if reject {
			if l.modelEventLog != nil {
				_ = l.modelEventLog.Append(ctx, sessionID, userID,
					wksession.EventTurnEnd,
					"pre_step_reject:"+reason, "")
			}
			// dsh semantics: a rejected claim closes a durable
			// turn that spent no step. Return a reject-marked
			// EMPTY event stream (one synthetic marker event,
			// then closed) with nil error — so callers can
			// distinguish "blocked by policy" from "runner
			// failed". The marker carries Tag=pre_step_reject
			// and the rejecting hook name in Extensions; consumers
			// (TUI/gateway) inspect evt.Tag to surface it. Run
			// already recorded turn_end above, so RunStream skips
			// its own turn_end when it sees the marker.
			span.SetAttributes(attribute.String(
				"pre_step_reject", reason))
			return newPreStepRejectStream(reason), nil
		}
		message = rewritten
	}

	// Record the FINAL model-visible message — the authoritative
	// witness for "model-visible means logged". This is exactly
	// what the model is about to see (post-enrichment, post-hooks).
	if l.modelEventLog != nil {
		finalVisible := extractMessageContent(message)
		if err := l.modelEventLog.Append(ctx, sessionID, userID,
			wksession.EventModelMessage, "", finalVisible); err != nil {
			util.Logger.Warn("model event log: model_message failed",
				slog.String("error", err.Error()))
		}
	}

	runOpts := []agent.RunOption{}
	if l.cfg.Agent.JSONRepairEnabled {
		runOpts = append(runOpts,
			agent.WithToolCallArgumentsJSONRepairEnabled(true),
		)
	}

	// Inject session/user identity into the ctx so the framework's
	// BeforeTool callback can attribute approval requests to this
	// turn. The async Approval gate reads this via
	// security.ApprovalContextFrom.
	ctx = security.WithApprovalContext(ctx, sessionID, userID)

	events, err := l.runner.Run(ctx, userID, sessionID, message, runOpts...)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return nil, fmt.Errorf("runner run: %w", err)
	}

	return events, nil
}

// RunStream processes streaming events and extracts the final response.
// Returns the complete assistant response text and calls onEvent
// for each event emitted.
// RunStream processes streaming events and extracts the final response.
// Returns the complete assistant response text and calls onEvent
// for each event emitted.
func (l *CoreLoop) RunStream(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	onEvent func(evt *event.Event) error,
) (string, error) {
	// Create a span that wraps the full stream processing lifecycle
	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "agent.RunStream",
		trace.WithAttributes(
			attribute.String("user_id", userID),
			attribute.String("session_id", sessionID),
		),
	)
	defer span.End()

	events, err := l.Run(ctx, userID, sessionID, message)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return "", err
	}

	// Track this in-flight RunStream so Close() waits for the
	// synchronous post-run side effects (recall/cortex/MemoryFlow
	// writes, contextMgr.AfterRun) to finish before closing the DB
	// pool. Add only after Run succeeded: a closed/errored Run never
	// reaches the post-run write path, so it must not be tracked.
	l.runWg.Add(1)
	defer l.runWg.Done()

	var responseText string
	var textBuilder strings.Builder
	// rejected tracks whether this turn was blocked by a pre-step
	// hook before any model step ran. Run already recorded turn_end
	// for such turns (source=pre_step_reject), so RunStream must
	// skip its own turn_end recording to avoid a duplicate.
	rejected := false
	var allEvents []event.Event
	toolCallCount := 0
	var eventCount int

	for evt := range events {
		eventCount++
		allEvents = append(allEvents, *evt)

		// Detect a pre-step rejection marker early and short-circuit:
		// the marker is a synthetic control event with no embedded
		// *model.Response, so the normal evt.Error / evt.Response
		// processing below would dereference a nil pointer. We still
		// deliver it to onEvent (so TUI/gateway can surface the
		// rejection) and flag the turn so the normal turn_end
		// recording is skipped (Run already recorded it).
		if IsPreStepReject(evt) {
			rejected = true
			if onEvent != nil {
				if err := onEvent(evt); err != nil {
					span.SetStatus(codes.Error, err.Error())
					span.RecordError(err)
					return textBuilder.String(), err
				}
			}
			continue
		}

		// Notify callback
		if onEvent != nil {
			if err := onEvent(evt); err != nil {
				span.SetStatus(codes.Error, err.Error())
				span.RecordError(err)
				return textBuilder.String(), err
			}
		}

		// Check for errors
		if evt.Error != nil {
			span.SetStatus(codes.Error, evt.Error.Message)
			return textBuilder.String(),
				fmt.Errorf("agent error: %s", evt.Error.Message)
		}

		// Collect streaming content (skip tool response events).
		if evt.Response != nil && len(evt.Response.Choices) > 0 {
			choice := evt.Response.Choices[0]
			// Skip tool response JSON from leaking into output.
			if choice.Message.Role != "tool" {
				if choice.Delta.Content != "" {
					textBuilder.WriteString(choice.Delta.Content)
				}
			}
			// Count tool calls in this response
			toolCallCount += len(choice.Message.ToolCalls)
		}

		// Check for runner completion
		if evt.IsRunnerCompletion() {
			// Extract final result from state delta if available
			// but only override if it's non-empty to avoid
			// losing incrementally collected delta content.
			if evt.StateDelta != nil {
				if lastResp, ok := evt.StateDelta["last_response"]; ok {
					lastRespStr := string(lastResp)
					if lastRespStr != "" {
						textBuilder.Reset()
						textBuilder.WriteString(lastRespStr)
					}
				}
			}
		}
	}

	// Fallback: if no delta content was collected, try reading
	// the final response from the last completion event's Message.Content.
	responseText = textBuilder.String()
	if responseText == "" && len(allEvents) > 0 {
		lastEvt := allEvents[len(allEvents)-1]
		if lastEvt.Response != nil &&
			len(lastEvt.Response.Choices) > 0 {
			responseText = lastEvt.Response.Choices[0].Message.Content
		}
	}

	// Add metrics attributes to span
	span.SetAttributes(
		attribute.Int("event_count", eventCount),
		attribute.Int("tool_call_count", toolCallCount),
		attribute.Int("response_length", len(responseText)),
	)

	// Store assistant response for recall.
	if l.recallStore != nil && responseText != "" {
		msg := recall.ChatMessage{
			SessionID: sessionID,
			UserID:    userID,
			Role:      "assistant",
			Content:   responseText,
		}
		if l.cortexStore != nil {
			if err := l.cortexStore.StoreMessage(ctx, msg); err != nil {
				util.Logger.Warn("cortex: store assistant message failed",
					slog.String("error", err.Error()))
			}
		} else {
			if err := l.recallStore.StoreMessage(msg); err != nil {
				util.Logger.Warn("recall: store assistant message failed",
					slog.String("error", err.Error()))
			}
		}
	}

	// Store tool call and tool response messages for recall.
	// Tool interactions contain valuable context (web search results,
	// command outputs, file contents) that enrich future searches.
	if l.recallStore != nil {
		for _, evt := range allEvents {
			if evt.Response == nil ||
				len(evt.Response.Choices) == 0 {
				continue
			}
			choice := evt.Response.Choices[0]
			// Store tool call requests.
			for _, tc := range choice.Message.ToolCalls {
				toolContent := fmt.Sprintf(
					"Tool: %s\nArgs: %s",
					tc.Function.Name,
					string(tc.Function.Arguments),
				)
				toolMsg := recall.ChatMessage{
					SessionID: sessionID,
					UserID:    userID,
					Role:      "tool_call",
					Content:   toolContent,
				}
				if l.cortexStore != nil {
					if err := l.cortexStore.StoreMessage(ctx, toolMsg); err != nil {
						util.Logger.Debug(
							"cortex: store tool call failed",
							slog.String("error", err.Error()))
					}
				} else {
					if err := l.recallStore.StoreMessage(toolMsg); err != nil {
						util.Logger.Debug(
							"recall: store tool call failed",
							slog.String("error", err.Error()))
					}
				}
			}
			// Store tool response content.
			if choice.Message.Role == "tool" &&
				choice.Message.Content != "" {
				toolResp := recall.ChatMessage{
					SessionID: sessionID,
					UserID:    userID,
					Role:      "tool_response",
					Content:   choice.Message.Content,
				}
				if l.cortexStore != nil {
					if err := l.cortexStore.StoreMessage(ctx, toolResp); err != nil {
						util.Logger.Debug(
							"cortex: store tool response failed",
							slog.String("error", err.Error()))
					}
				} else {
					if err := l.recallStore.StoreMessage(toolResp); err != nil {
						util.Logger.Debug(
							"recall: store tool response failed",
							slog.String("error", err.Error()))
					}
				}
			}
		}
	}

	// [Fix 2] Record assistant response in MemoryFlow transcript.
	// Use a bounded timeout so a slow embedder/indexer (e.g. first-time
	// gse dictionary load) doesn't block RunStream's return for too long.
	if l.memoryFlow != nil && responseText != "" {
		ingestCtx, ingestCancel := context.WithTimeout(ctx, 15*time.Second)
		if err := l.memoryFlow.IngestTurn(
			ingestCtx, sessionID, userID, "assistant", responseText,
		); err != nil {
			util.Logger.Warn("memoryflow: ingest assistant turn failed",
				slog.String("error", err.Error()))
		}
		ingestCancel()
	}

	// [Fix 3] Bridge MemoryFlow → tRPC Memory: promote extracted
	// facts from conversation to the persistent memory store.
	if l.memoryFlow != nil && responseText != "" {
		l.bgWg.Add(1)
		go func() {
			defer l.bgWg.Done()
			defer func() {
				if r := recover(); r != nil {
					util.Logger.Warn("memoryflow: promote panic",
						"error", fmt.Sprint(r))
				}
			}()
			bgCtx, cancel := context.WithTimeout(
				context.Background(), 60*time.Second,
			)
			defer cancel()
			candidates, err := l.memoryFlow.PromoteFacts(
				bgCtx, sessionID, userID,
			)
			if err != nil {
				util.Logger.Debug("memoryflow: promote skipped",
					"reason", err.Error())
				return
			}
			// Write promoted facts to the tRPC persistent memory
			// store so they survive across sessions.
			userKey := memory.UserKey{
				AppName: "wukong-app",
				UserID:  userID,
			}
			var stored int
			for _, c := range candidates {
				if c.Content == "" {
					continue
				}
				if l.memoryService == nil {
					util.Logger.Debug("memoryflow: promoted fact (not stored)",
						"kind", c.Kind,
						"content", c.Content[:min(80, len(c.Content))],
					)
					continue
				}
				// Determine topics from the candidate's kind
				// and collection.
				topics := []string{
					string(c.Kind),
					c.Collection,
				}
				if err := l.memoryService.AddMemory(
					bgCtx, userKey, c.Content, topics,
				); err != nil {
					util.Logger.Warn(
						"memoryflow: store fact failed",
						"kind", c.Kind,
						"error", err.Error(),
					)
					continue
				}
				stored++
				util.Logger.Debug("memoryflow: promoted fact stored",
					"kind", c.Kind,
					"content", c.Content[:min(80, len(c.Content))],
				)
			}
			if stored > 0 {
				util.Logger.Info("memoryflow: facts stored",
					"stored", stored,
					"total_candidates", len(candidates),
				)
			}
		}()
	}

	// GraphFlow auto-extract: when enabled, extract entities and
	// relationships from the conversation transcript and persist
	// them into the knowledge graph after each turn.
	if l.graphFlow != nil &&
		l.cfg.GraphFlow.Enabled &&
		l.cfg.GraphFlow.AutoExtract &&
		responseText != "" {
		l.bgWg.Add(1)
		go func() {
			defer l.bgWg.Done()
			defer func() {
				if r := recover(); r != nil {
					util.Logger.Warn("graphflow: auto-extract panic",
						"error", fmt.Sprint(r))
				}
			}()
			bgCtx, cancel := context.WithTimeout(
				context.Background(), 60*time.Second,
			)
			defer cancel()

			// Build transcript from collected events.
			var transcriptText strings.Builder
			transcriptText.WriteString(
				fmt.Sprintf("User: %s\n",
					extractMessageContent(message)))
			transcriptText.WriteString(
				fmt.Sprintf("Assistant: %s\n", responseText))

			result, err := l.graphFlow.ExtractFromTranscript(
				bgCtx, sessionID, transcriptText.String())
			if err != nil {
				util.Logger.Debug("graphflow: extract failed",
					slog.String("error", err.Error()))
				return
			}
			if result == nil || len(result.Nodes) == 0 {
				return
			}
			if err := l.graphFlow.BuildGraph(bgCtx, result); err != nil {
				util.Logger.Warn("graphflow: build graph failed",
					slog.String("error", err.Error()))
				return
			}
			util.Logger.Info("graphflow: knowledge graph updated",
				"nodes", len(result.Nodes),
				"edges", len(result.Edges),
				"session", sessionID[:min(8, len(sessionID))],
			)
		}()
	}

	// Trigger context optimization after run with real events
	l.contextMgr.AfterRun(ctx, responseText, allEvents)

	// Record the turn_end event — the assistant response the model
	// produced. This closes the durable turn in the model-visible
	// event log. Best-effort: a logging failure is not fatal.
	// Skipped for pre-step rejections: Run already recorded turn_end
	// (source=pre_step_reject) when it emitted the marker stream.
	if l.modelEventLog != nil && !rejected {
		if err := l.modelEventLog.Append(ctx, sessionID, userID,
			wksession.EventTurnEnd, "assistant", responseText); err != nil {
			util.Logger.Warn("model event log: turn_end failed",
				slog.String("error", err.Error()))
		}
	}

	return responseText, nil
}

// RunUserMessage is a convenience method that handles the complete
// lifecycle of a user message: prepare context, run agent, and
// return the final response text.
// errOrEmpty returns err.Error() or "" for a nil error. Used by
// buildGuardrailPlugin to format a possibly-nil model-creation error.
func errOrEmpty(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// isMemoryDuplicated checks whether a memory content string is
// already substantially present in the MemoryFlow wake-up context.
// Uses a sliding window of kMinDupeLen characters to detect
// substring matches of at least 60% overlap, which catches both
// exact duplicates and near-duplicates (e.g., slightly reworded).
// isMemoryDuplicated checks whether a memory content string is
// already substantially present in the MemoryFlow wake-up context.
// Uses a sliding window of kMinDupeLen characters to detect
// substring matches of at least 60% overlap, which catches both
// exact duplicates and near-duplicates (e.g., slightly reworded).
const kMinDupeLen = 30

func isMemoryDuplicated(memory, wakeCtx string) bool {
	if len(memory) < kMinDupeLen || len(wakeCtx) < kMinDupeLen {
		return false
	}

	// Build a set of kMinDupeLen-char windows from the memory.
	windows := make(map[string]bool, len(memory)-kMinDupeLen+1)
	for i := 0; i <= len(memory)-kMinDupeLen; i++ {
		windows[memory[i:i+kMinDupeLen]] = true
	}

	// Count matching windows in the wake-up context.
	var matches int
	for i := 0; i <= len(wakeCtx)-kMinDupeLen; i++ {
		if windows[wakeCtx[i:i+kMinDupeLen]] {
			matches++
		}
	}

	// If >= 60% of the memory's windows appear in the wake-up
	// context, consider it a duplicate.
	totalWindows := len(windows)
	if totalWindows == 0 {
		return false
	}
	return float64(matches)/float64(totalWindows) >= 0.6
}
