package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// loopRunner is the minimal agent-loop surface the TUI depends on.
// It is a small interface (rather than *agent.CoreLoop) so tests can
// inject a fake loop to exercise the streaming/resultQueue paths.
type loopRunner interface {
	Run(ctx context.Context, userID, sessionID string, message model.Message) (<-chan *event.Event, error)
}

// streamingDeltaMsg carries an incremental content delta for streaming.
type streamingDeltaMsg string

// toolCallStartMsg signals that a tool call has started.
type toolCallStartMsg struct {
	Name string
	Args string
}

// toolCallResultMsg signals that a tool call has completed.
// Name matches the toolCallEntry.Name so results are assigned
// to the correct tool call even when multiple tools run concurrently.
type toolCallResultMsg struct {
	Name   string
	Result string
}

// streamingErrorMsg carries an error during streaming.
type streamingErrorMsg string

// streamEndMsg signals the end of the streaming response.
type streamEndMsg struct {
	Content string
}

// streamEvent carries a streaming event from the agent goroutine.
type streamEvent struct {
	Delta        string
	Tool         *toolCallStartMsg
	ToolResult   *toolCallResultMsg
	Err          string
	IsEnd        bool
	Content      string
	NoToolResult bool // tool.response arrived with no content to attribute
}

// sendMessage creates a command to send a user message.
// Uses an intermediate channel to deliver streaming deltas and tool calls
// to the TUI update loop for real-time display.
// Stores a cancel function so Ctrl+C can interrupt in-flight requests.
func (m *Model) sendMessage(input string) tea.Cmd {
	if m.streaming {
		return nil
	}

	m.addMessage("user", input)
	m.setStatus("Thinking...")
	m.streaming = true
	m.currentStream = ""
	m.resetStreamCache()
	m.autoScroll = true
	m.cancelled = false
	m.lastRenderTime = time.Time{}

	// Track first user instruction for project recovery.
	m.trackProjectInstruction(input)

	// Create cancelable context so Ctrl+C can interrupt the request.
	ctx, cancel := context.WithCancel(context.Background())
	m.streamCancel = cancel

	// Channel to pipe events from agent goroutine to TUI update loop
	streamCh := make(chan streamEvent, 64)
	m.streamCh = streamCh

	// Channel to signal when the stream goroutine finishes
	streamDone := make(chan struct{})
	m.streamDone = streamDone

	go func() {
		defer close(streamCh)
		defer close(streamDone)
		defer cancel()

		defer func() {
			if r := recover(); r != nil {
				select {
				case streamCh <- streamEvent{
					Err: fmt.Sprintf("\n[Internal error: %v]\n", r),
				}:
				case <-ctx.Done():
				}
				select {
				case streamCh <- streamEvent{IsEnd: true}:
				case <-ctx.Done():
				}
			}
		}()

		timeout := time.Duration(defaultTimeoutMinutes) * time.Minute
		if m.cfg != nil && m.cfg.Agent.MaxRunDuration > 0 {
			timeout = m.cfg.Agent.MaxRunDuration
		}
		ctx, timeoutCancel := context.WithTimeout(ctx, timeout)
		defer timeoutCancel()

		msg := model.NewUserMessage(input)
		events, err := m.loop.Run(
			ctx, m.userID, m.sessionID, msg,
		)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				// Context was cancelled — send a clean end signal
				// but do NOT send an error message.
				if ctx.Err() == context.Canceled {
					select {
					case streamCh <- streamEvent{IsEnd: true}:
					default:
						// Channel already has pending events; try once more
						// after a brief yield.
						time.Sleep(10 * time.Millisecond)
						select {
						case streamCh <- streamEvent{IsEnd: true}:
						default:
						}
					}
				}
				return
			}
			errMsg := err.Error()
			errMsg = friendlyError(errMsg)
			select {
			case streamCh <- streamEvent{
				Err: "[Error: " + errMsg + "]\n",
			}:
			case <-ctx.Done():
				return
			}
			select {
			case streamCh <- streamEvent{IsEnd: true}:
			case <-ctx.Done():
			}
			return
		}

		// resultQueue tracks tool names in the order their calls STARTED.
		// tool.response events carry no tool-call ID (the framework's
		// runner distinguishes them internally), so attaching results in
		// start order is the only correct generic attribution. TUI-side
		// matching takes the FIRST running entry with this name.
		var resultQueue []string
		var fullContent string
		for evt := range events {
			// Check if context is cancelled to avoid wasted work
			select {
			case <-ctx.Done():
				return
			default:
			}

			if evt.Error != nil {
				select {
				case streamCh <- streamEvent{
					Err: fmt.Sprintf(
						"\n[Error: %s]\n",
						evt.Error.Message,
					),
				}:
				case <-ctx.Done():
					return
				}
				continue
			}

			if evt.Response != nil &&
				len(evt.Response.Choices) > 0 {
				choice := evt.Response.Choices[0]

				if choice.Message.Role == "tool" ||
					evt.Response.Object ==
						"tool.response" {
					// A tool.response event corresponds to the
					// OLDEST tool call that is still awaiting a
					// result, in start order (see resultQueue).
					if len(choice.Message.ToolCalls) == 0 {
						if len(resultQueue) > 0 {
							name := resultQueue[0]
							resultQueue = resultQueue[1:]
							res := choice.Message.Content
							if res == "" {
								res = "(no content)"
							}
							// Redact secrets before the result
							// reaches the message channel (output
							// side AND stored message/audit).
							res = util.RedactSecrets(
								res, m.secrets)
							select {
							case streamCh <- streamEvent{
								ToolResult: &toolCallResultMsg{
									Name:   name,
									Result: res,
								},
							}:
							case <-ctx.Done():
								return
							}
						} else {
							// No outstanding tool call to attach
							// to; surface as a system note so the
							// response is not silently dropped.
							select {
							case streamCh <- streamEvent{
								NoToolResult: true,
							}:
							case <-ctx.Done():
								return
							}
						}
					}
					continue
				}

				if choice.Delta.Content != "" {
					fullContent += choice.Delta.Content
					select {
					case streamCh <- streamEvent{
						Delta: choice.Delta.Content,
					}:
					case <-ctx.Done():
						return
					}
				}

				if fullContent == "" &&
					choice.Message.Content != "" {
					fullContent = choice.Message.Content
					select {
					case streamCh <- streamEvent{
						Delta: choice.Message.Content,
					}:
					case <-ctx.Done():
						return
					}
				}

				toolCalls := choice.Message.ToolCalls
				if len(toolCalls) == 0 {
					toolCalls = choice.Delta.ToolCalls
				}
				for _, tc := range toolCalls {
					argsJSON := "{}"
					if tc.Function.Arguments != nil {
						argsJSON = string(tc.Function.Arguments)
					}
					// Tool call args are shown in the TUI header;
					// strip secret values before they are rendered.
					argsJSON = util.RedactSecrets(argsJSON, m.secrets)
					resultQueue = append(resultQueue, tc.Function.Name)
					select {
					case streamCh <- streamEvent{
						Tool: &toolCallStartMsg{
							Name: tc.Function.Name,
							Args: argsJSON,
						},
					}:
					case <-ctx.Done():
						return
					}
				}
			}

			if evt.IsRunnerCompletion() {
				select {
				case streamCh <- streamEvent{
					IsEnd:   true,
					Content: fullContent,
				}:
				case <-ctx.Done():
				}
				return
			}
		}

		select {
		case streamCh <- streamEvent{
			IsEnd:   true,
			Content: fullContent,
		}:
		case <-ctx.Done():
		}
	}()

	// Return the first reader command that bridges channel → tea.Msg
	return readStreamEvent(streamCh)
}

// readStreamEvent returns a tea.Cmd that reads the next event from
// the stream channel and returns it as a tea.Msg. It skips empty
// events via an internal loop to avoid recursion and stack growth.
func readStreamEvent(ch <-chan streamEvent) tea.Cmd {
	return func() tea.Msg {
		for {
			evt, ok := <-ch
			if !ok {
				return streamEndMsg{Content: ""}
			}

			switch {
			case evt.IsEnd:
				return streamEndMsg{Content: evt.Content}
			case evt.Err != "":
				return streamingErrorMsg(evt.Err)
			case evt.ToolResult != nil:
				return *evt.ToolResult
			case evt.NoToolResult:
				return streamingDeltaMsg("\n[Tool result empty — no content returned]\n")
			case evt.Tool != nil:
				return *evt.Tool
			case evt.Delta != "":
				return streamingDeltaMsg(evt.Delta)
			}
		}
	}
}

// addMessage appends a message to the conversation.
// Excess oldest messages are trimmed to prevent unbounded growth.
func (m *Model) addMessage(role, content string) {
	m.messages = append(m.messages, chatEntry{
		Role:    role,
		Content: content,
	})
	// Trim oldest messages when exceeding the limit.
	if len(m.messages) > maxMessages {
		trimCount := len(m.messages) - maxMessages
		m.messages = m.messages[trimCount:]
	}
}

// setStatus updates the agent status display.
func (m *Model) setStatus(status string) {
	m.status = status
}

// trackProjectInstruction records the first user message as the
// project's "last instruction" for session recovery. Only the
// first user message per session is recorded to avoid writing
// on every single input.
func (m *Model) trackProjectInstruction(input string) {
	if m.instrRecorded || m.projectMgr == nil ||
		m.workingDir == "" || input == "" {
		return
	}
	m.instrRecorded = true

	// The project package defines Manager.UpdateInstruction;
	// we use any to avoid an import cycle with the
	// tui package.
	type instructionUpdater interface {
		UpdateInstruction(workingDir string, instruction string)
	}
	if updater, ok := m.projectMgr.(instructionUpdater); ok {
		updater.UpdateInstruction(m.workingDir, input)
	}
}

// refreshMsg signals the TUI to refresh the viewport.
type refreshMsg struct{}

// defaultTimeoutMinutes is the default run timeout in minutes.
// Increased for large local models (26B+) that may take 10+ min.
const defaultTimeoutMinutes = 30

// friendlyError maps common error strings to user-friendly messages.
func friendlyError(errMsg string) string {
	lower := strings.ToLower(errMsg)

	switch {
	case strings.Contains(lower, "context deadline exceeded"):
		return "Request timed out — the model took too long to respond"
	case strings.Contains(lower, "connectex"),
		strings.Contains(lower, "connection refused"),
		strings.Contains(lower, "no such host"):
		return "Cannot connect to model — check network/provider"
	case strings.Contains(lower, "401") ||
		strings.Contains(lower, "unauthorized"):
		return "Authentication failed — check API key or credentials"
	case strings.Contains(lower, "429") ||
		strings.Contains(lower, "rate limit"):
		return "Rate limited by provider — wait and retry"
	case strings.Contains(lower, "500") ||
		strings.Contains(lower, "502") ||
		strings.Contains(lower, "503"):
		return "Model service unavailable — provider may be experiencing issues"
	case strings.Contains(lower, "canceled") ||
		strings.Contains(lower, "cancelled"):
		return "Request cancelled by user"
	default:
		return errMsg
	}
}
