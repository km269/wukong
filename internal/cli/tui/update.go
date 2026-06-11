package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// sendMessage creates a command to send a user message.
func (m *Model) sendMessage(input string) tea.Cmd {
	// Don't allow sending while streaming
	if m.streaming {
		return nil
	}

	m.addMessage("user", input)
	m.setStatus("Thinking...")
	m.streaming = true
	m.currentStream = ""

	return func() tea.Msg {
		// Create context with timeout
		ctx, cancel := context.WithTimeout(
			context.Background(),
			5*time.Minute,
		)
		defer cancel()

		msg := model.NewUserMessage(input)
		events, err := m.loop.Run(
			ctx, m.userID, m.sessionID, msg,
		)
		if err != nil {
			m.streaming = false
			return refreshMsg{}
		}

		// Read events synchronously
		for evt := range events {
			if evt.Error != nil {
				m.currentStream += fmt.Sprintf(
					"\n[Error: %s]\n", evt.Error.Message,
				)
				continue
			}

			if evt.Response != nil &&
				len(evt.Response.Choices) > 0 {
				choice := evt.Response.Choices[0]

				if choice.Delta.Content != "" {
					m.currentStream += choice.Delta.Content
				}

				// Handle tool calls
				for _, tc := range choice.Message.ToolCalls {
					m.toolCalls = append(m.toolCalls, toolCallEntry{
						Name:   tc.Function.Name,
						Args:   string(tc.Function.Arguments),
						Status: "running",
					})
				}

				// Handle tool results
				if choice.Message.Role == model.RoleTool {
					result := choice.Message.Content
					for i := len(m.toolCalls) - 1; i >= 0; i-- {
						if m.toolCalls[i].Status == "running" {
							m.toolCalls[i].Result = result
							m.toolCalls[i].Status = "done"
							break
						}
					}
				}
			}

			if evt.IsRunnerCompletion() {
				m.streaming = false
				m.addMessage("assistant", m.currentStream)
				m.currentStream = ""
				m.setStatus("Ready")
				return refreshMsg{}
			}
		}

		m.streaming = false
		m.addMessage("assistant", m.currentStream)
		m.currentStream = ""
		m.setStatus("Ready")
		return refreshMsg{}
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
