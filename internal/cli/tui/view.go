package tui

import (
	"fmt"
	"strings"

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
	colorAccent    = lipgloss.Color("147") // Teal
	colorBorder    = lipgloss.Color("237") // Dark gray
	colorBanner    = lipgloss.Color("234") // Darker gray for banner

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

	bannerStyle = lipgloss.NewStyle().
			Background(colorBanner).
			Foreground(lipgloss.Color("255")).
			Padding(0, 2)

	bannerAccentStyle = lipgloss.NewStyle().
				Background(colorBanner).
				Foreground(colorAccent).
				Bold(true)

	statusBarStyleBottom = lipgloss.NewStyle().
				Background(lipgloss.Color("237")).
				Foreground(lipgloss.Color("248")).
				Padding(0, 1)

	modalStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("235")).
			Foreground(lipgloss.Color("255")).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(1, 2)

	modalTitleStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true).
			Underline(true)

	modalSelectedStyle = lipgloss.NewStyle().
				Background(colorStatus).
				Foreground(lipgloss.Color("255")).
				Bold(true)

	modalItemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("248"))

	toolCallResultStyle = lipgloss.NewStyle().
				Foreground(colorDim).
				BorderLeft(true).
				BorderLeftForeground(colorAccent).
				Padding(0, 0, 0, 2)
)

// RenderUserMessage formats a user message with styling.
func RenderUserMessage(content string) string {
	return userStyle.Render("You: ") + content
}

// RenderAssistantMessage formats an assistant message with styling.
func RenderAssistantMessage(content string) string {
	return assistantStyle.Render("Wukong: ") + content
}

// RenderSystemMessage formats a system message with styling.
func RenderSystemMessage(content string) string {
	return dimStyle.Render(content)
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

// RenderToolCallResult formats a tool call with its result.
func RenderToolCallResult(entry toolCallEntry) string {
	icon := "○"
	color := colorDim
	switch entry.Status {
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

	var result string
	if entry.Result != "" {
		result = "\n" + toolCallResultStyle.Render(entry.Result)
	}

	return fmt.Sprintf(
		"%s %s %s%s",
		lipgloss.NewStyle().Foreground(color).Render(icon),
		lipgloss.NewStyle().Foreground(color).Bold(true).Render(entry.Name),
		lipgloss.NewStyle().Foreground(colorDim).Render(entry.Args),
		result,
	)
}

// RenderDim renders text in dim style.
func RenderDim(text string) string {
	return dimStyle.Render(text)
}

// RenderBanner renders the top banner bar.
func RenderBanner(version string, profile string, width int) string {
	left := fmt.Sprintf(
		"Wukong Agent %s",
		bannerAccentStyle.Render(version),
	)

	right := fmt.Sprintf(
		"☤ Profile: %s",
		profile,
	)

	spaces := width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if spaces < 1 {
		spaces = 1
	}

	return bannerStyle.
		Width(width).
		Render(left + strings.Repeat(" ", spaces) + right)
}

// RenderStatusBarBottom renders the bottom status bar.
func RenderStatusBarBottom(
	modelName string,
	providerName string,
	skillName string,
	toolCount int,
	status string,
	logs []string,
	width int,
) string {
	left := fmt.Sprintf(
		"Model: %s | Personality: %s",
		bannerAccentStyle.Render(modelName),
		skillName,
	)

	right := fmt.Sprintf(
		"Tools: %d enabled | %s",
		toolCount,
		status,
	)

	spaces := width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if spaces < 1 {
		spaces = 1
	}

	content := left + strings.Repeat(" ", spaces) + right

	if len(logs) > 0 {
		logLine := logs[len(logs)-1]
		if lipgloss.Width(logLine) > width-4 {
			logLine = logLine[:width-8] + "..."
		}
		content += "\n" + statusBarStyleBottom.Width(width).Render(logLine)
	}

	return statusBarStyleBottom.Width(width).Render(content)
}

// RenderHeader renders the top header bar (deprecated, use RenderBanner).
func RenderHeader(
	modelName string,
	providerName string,
	toolCount int,
	skillName string,
	sessionID string,
	status string,
	width int,
) string {
	return RenderBanner("v0.2.4", providerName, width)
}

// RenderModal renders a modal window.
func RenderModal(modal *modalState, width, height int) string {
	if modal == nil {
		return ""
	}

	content := modalTitleStyle.Render(modal.Title) + "\n\n"

	if len(modal.Items) > 0 {
		for i, item := range modal.Items {
			if i == modal.Selected {
				content += modalSelectedStyle.Render("> "+item) + "\n"
			} else {
				content += modalItemStyle.Render("  "+item) + "\n"
			}
		}
	} else {
		content += modal.Content + "\n"
	}

	return modalStyle.
		Width(width).
		Height(height).
		Render(content)
}
