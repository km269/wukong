package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// streamingDeltaMsg carries an incremental content delta for streaming.
type streamingDeltaMsg string

// toolCallStartMsg signals that a tool call has started.
type toolCallStartMsg struct {
	Name string
	Args string
}

// toolCallResultMsg signals that a tool call has completed.
type toolCallResultMsg struct {
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
	Delta   string
	Tool    *toolCallStartMsg
	Err     string
	IsEnd   bool
	Content string
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

	// Track first user instruction for project recovery.
	m.trackProjectInstruction(input)

	// Create cancelable context so Ctrl+C can interrupt the request.
	ctx, cancel := context.WithCancel(context.Background())
	m.streamCancel = cancel

	// Channel to pipe events from agent goroutine to TUI update loop
	streamCh := make(chan streamEvent, 64)
	m.streamCh = streamCh

	go func() {
		defer close(streamCh)
		// Use the cancelable context so Ctrl+C interrupts the request.
		// Ensure cancel is called on all exit paths.
		defer cancel()

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
			errMsg := err.Error()
			errMsg = friendlyError(errMsg)
			streamCh <- streamEvent{
				Err: "[Error: " + errMsg + "]\n",
			}
			streamCh <- streamEvent{IsEnd: true}
			return
		}

		var fullContent string
		for evt := range events {
			if evt.Error != nil {
				streamCh <- streamEvent{
					Err: fmt.Sprintf(
						"\n[Error: %s]\n",
						evt.Error.Message,
					),
				}
				continue
			}

			if evt.Response != nil &&
				len(evt.Response.Choices) > 0 {
				choice := evt.Response.Choices[0]

				// Skip tool response events — their raw JSON
				// should not leak into the displayed output.
				if choice.Message.Role == "tool" ||
					evt.Response.Object ==
						"tool.response" {
					continue
				}

				if choice.Delta.Content != "" {
					fullContent += choice.Delta.Content
					streamCh <- streamEvent{
						Delta: choice.Delta.Content,
					}
				}

				// Fallback: if no delta streaming, capture
				// content from Message.Content (non-streaming).
				if fullContent == "" &&
					choice.Message.Content != "" {
					fullContent = choice.Message.Content
					streamCh <- streamEvent{
						Delta: choice.Message.Content,
					}
				}

				// Collect tool calls from both Message (batch)
				// and Delta (streaming) to avoid missing any.
				toolCalls := choice.Message.ToolCalls
				if len(toolCalls) == 0 {
					toolCalls = choice.Delta.ToolCalls
				}
				for _, tc := range toolCalls {
					argsJSON := "{}"
					if tc.Function.Arguments != nil {
						argsJSON = string(tc.Function.Arguments)
					}
					streamCh <- streamEvent{
						Tool: &toolCallStartMsg{
							Name: tc.Function.Name,
							Args: argsJSON,
						},
					}
				}
			}

			if evt.IsRunnerCompletion() {
				streamCh <- streamEvent{
					IsEnd:   true,
					Content: fullContent,
				}
				return
			}
		}

		streamCh <- streamEvent{
			IsEnd:   true,
			Content: fullContent,
		}
	}()

	// Return the first reader command that bridges channel → tea.Msg
	return readStreamEvent(streamCh)
}

// readStreamEvent returns a tea.Cmd that reads the next event from
// the stream channel and returns it as a tea.Msg. It continues to
// self-reschedule until the stream ends.
func readStreamEvent(ch <-chan streamEvent) tea.Cmd {
	return func() tea.Msg {
		evt, ok := <-ch
		if !ok {
			return streamEndMsg{Content: ""}
		}

		switch {
		case evt.IsEnd:
			return streamEndMsg{Content: evt.Content}
		case evt.Err != "":
			return streamingErrorMsg(evt.Err)
		case evt.Tool != nil:
			return *evt.Tool
		case evt.Delta != "":
			return streamingDeltaMsg(evt.Delta)
		default:
			// Empty event, try again
			return readStreamEvent(ch)()
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
	// we use interface{} to avoid an import cycle with the
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
