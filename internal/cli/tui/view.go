package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

var (
	// Color scheme
	colorUser      = lipgloss.Color("120") // Green
	colorAssistant = lipgloss.Color("213") // Pink
	colorStatus    = lipgloss.Color("63")  // Blue
	colorRunning   = lipgloss.Color("226") // Yellow
	colorDone      = lipgloss.Color("42")  // Green
	colorError     = lipgloss.Color("196") // Red
	colorDim       = lipgloss.Color("240") // Gray

	// Styles
	userStyle = lipgloss.NewStyle().
			Foreground(colorUser).
			Bold(true)

	assistantStyle = lipgloss.NewStyle().
			Foreground(colorAssistant).
			Bold(true)

	statusBarStyle = lipgloss.NewStyle().
			Background(colorStatus).
			Foreground(lipgloss.Color("255")).
			Padding(0, 1)

	dimStyle = lipgloss.NewStyle().
			Foreground(colorDim)
)

// RenderUserMessage formats a user message with styling.
func RenderUserMessage(content string) string {
	return userStyle.Render("You: ") + content
}

// RenderAssistantMessage formats an assistant message with styling.
func RenderAssistantMessage(content string) string {
	return assistantStyle.Render("Wukong: ") + content
}

// RenderStatusBar renders the top status bar.
func RenderStatusBar(
	sessionID string,
	status string,
	provider string,
	model string,
	width int,
) string {
	sid := sessionID
	if len(sid) > 8 {
		sid = sid[:8]
	}
	left := fmt.Sprintf(
		"⚡ Wukong | %s | %s",
		sid,
		provider+"/"+model,
	)

	right := status
	spaces := width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if spaces < 1 {
		spaces = 1
	}
	gap := ""
	for range spaces {
		gap += " "
	}

	return statusBarStyle.
		Width(width - 2).
		Render(left + gap + right)
}

// RenderToolCall formats a tool call status indicator.
func RenderToolCall(name, status string) string {
	icon := "○"
	color := colorDim
	switch status {
	case "running":
		icon = "◉"
		color = colorRunning
	case "done":
		icon = "●"
		color = colorDone
	case "error":
		icon = "●"
		color = colorError
	}

	return lipgloss.NewStyle().
		Foreground(color).
		Render(fmt.Sprintf("%s %s", icon, name))
}

// RenderDim renders text in dim style.
func RenderDim(text string) string {
	return dimStyle.Render(text)
}
