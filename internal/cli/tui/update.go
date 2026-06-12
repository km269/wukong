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
func (m *Model) sendMessage(input string) tea.Cmd {
	if m.streaming {
		return nil
	}

	m.addMessage("user", input)
	m.setStatus("Thinking...")
	m.streaming = true
	m.currentStream = ""

	// Channel to pipe events from agent goroutine to TUI update loop
	streamCh := make(chan streamEvent, 64)
	m.streamCh = streamCh

	go func() {
		defer close(streamCh)

		timeout := time.Duration(defaultTimeoutMinutes) * time.Minute
		if m.cfg != nil && m.cfg.Agent.MaxRunDuration > 0 {
			timeout = m.cfg.Agent.MaxRunDuration
		}
		ctx, cancel := context.WithTimeout(
			context.Background(), timeout,
		)
		defer cancel()

		msg := model.NewUserMessage(input)
		events, err := m.loop.Run(
			ctx, m.userID, m.sessionID, msg,
		)
		if err != nil {
			errMsg := err.Error()
			// Provide user-friendly messages for common errors
			if strings.Contains(errMsg, "context deadline exceeded") {
				errMsg = "Request timed out — the model took too long to respond"
			} else if strings.Contains(errMsg, "connectex") ||
				strings.Contains(errMsg, "connection refused") {
				errMsg = "Cannot connect to model — check network/provider"
			}
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

				if choice.Delta.Content != "" {
					fullContent += choice.Delta.Content
					streamCh <- streamEvent{
						Delta: choice.Delta.Content,
					}
				}

				for _, tc := range choice.Message.ToolCalls {
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
func (m *Model) addMessage(role, content string) {
	m.messages = append(m.messages, chatEntry{
		Role:    role,
		Content: content,
	})
}

// setStatus updates the agent status display.
func (m *Model) setStatus(status string) {
	m.status = status
}

// refreshMsg signals the TUI to refresh the viewport.
type refreshMsg struct{}

// defaultTimeoutMinutes is the default run timeout in minutes.
const defaultTimeoutMinutes = 5
