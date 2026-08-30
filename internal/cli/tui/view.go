package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/km269/wukong/internal/util"
)

// ThemeType defines available color themes.
type ThemeType int

const (
	ThemeDark ThemeType = iota
	ThemeLight
	ThemeClassic
)

// String returns the theme name.
func (t ThemeType) String() string {
	switch t {
	case ThemeLight:
		return "light"
	case ThemeClassic:
		return "classic"
	default:
		return "dark"
	}
}

// ParseTheme converts a string to ThemeType.
func ParseTheme(name string) ThemeType {
	switch strings.ToLower(name) {
	case "light", "l":
		return ThemeLight
	case "classic", "c":
		return ThemeClassic
	default:
		return ThemeDark
	}
}

// ColorPalette holds the color scheme for a theme.
type ColorPalette struct {
	User      lipgloss.Color
	Assistant lipgloss.Color
	Status    lipgloss.Color
	Running   lipgloss.Color
	Done      lipgloss.Color
	Error     lipgloss.Color
	Dim       lipgloss.Color
	Accent    lipgloss.Color
	Border    lipgloss.Color
	Banner    lipgloss.Color
	BannerFg  lipgloss.Color
	StatusBg  lipgloss.Color
	StatusFg  lipgloss.Color
}

// Theme palettes
var (
	darkPalette = ColorPalette{
		User:      lipgloss.Color("120"),
		Assistant: lipgloss.Color("213"),
		Status:    lipgloss.Color("63"),
		Running:   lipgloss.Color("226"),
		Done:      lipgloss.Color("42"),
		Error:     lipgloss.Color("196"),
		Dim:       lipgloss.Color("240"),
		Accent:    lipgloss.Color("147"),
		Border:    lipgloss.Color("237"),
		Banner:    lipgloss.Color("234"),
		BannerFg:  lipgloss.Color("255"),
		StatusBg:  lipgloss.Color("237"),
		StatusFg:  lipgloss.Color("248"),
	}

	lightPalette = ColorPalette{
		User:      lipgloss.Color("22"),
		Assistant: lipgloss.Color("54"),
		Status:    lipgloss.Color("25"),
		Running:   lipgloss.Color("178"),
		Done:      lipgloss.Color("28"),
		Error:     lipgloss.Color("160"),
		Dim:       lipgloss.Color("245"),
		Accent:    lipgloss.Color("29"),
		Border:    lipgloss.Color("240"),
		Banner:    lipgloss.Color("252"),
		BannerFg:  lipgloss.Color("0"),
		StatusBg:  lipgloss.Color("252"),
		StatusFg:  lipgloss.Color("235"),
	}

	classicPalette = ColorPalette{
		User:      lipgloss.Color("34"),
		Assistant: lipgloss.Color("35"),
		Status:    lipgloss.Color("36"),
		Running:   lipgloss.Color("33"),
		Done:      lipgloss.Color("32"),
		Error:     lipgloss.Color("31"),
		Dim:       lipgloss.Color("90"),
		Accent:    lipgloss.Color("37"),
		Border:    lipgloss.Color("245"),
		Banner:    lipgloss.Color("240"),
		BannerFg:  lipgloss.Color("235"),
		StatusBg:  lipgloss.Color("240"),
		StatusFg:  lipgloss.Color("232"),
	}
)

// Current theme and styles
var (
	currentTheme   = ThemeDark
	colorUser      = darkPalette.User
	colorAssistant = darkPalette.Assistant
	colorStatus    = darkPalette.Status
	colorRunning   = darkPalette.Running
	colorDone      = darkPalette.Done
	colorError     = darkPalette.Error
	colorDim       = darkPalette.Dim
	colorAccent    = darkPalette.Accent
	colorBorder    = darkPalette.Border
	colorBanner    = darkPalette.Banner
	colorBannerFg  = darkPalette.BannerFg
	colorStatusBg  = darkPalette.StatusBg
	colorStatusFg  = darkPalette.StatusFg

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
			Foreground(colorBannerFg).
			Padding(0, 2)

	bannerAccentStyle = lipgloss.NewStyle().
				Background(colorBanner).
				Foreground(colorAccent).
				Bold(true)

	statusBarStyleBottom = lipgloss.NewStyle().
				Background(colorStatusBg).
				Foreground(colorStatusFg).
				Padding(0, 1)

	modalStyle = lipgloss.NewStyle().
			Background(colorStatusBg).
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

// SetTheme changes the active color theme and reinitializes all styles.
func SetTheme(theme ThemeType) {
	currentTheme = theme

	var palette ColorPalette
	switch theme {
	case ThemeLight:
		palette = lightPalette
	case ThemeClassic:
		palette = classicPalette
	default:
		palette = darkPalette
	}

	colorUser = palette.User
	colorAssistant = palette.Assistant
	colorStatus = palette.Status
	colorRunning = palette.Running
	colorDone = palette.Done
	colorError = palette.Error
	colorDim = palette.Dim
	colorAccent = palette.Accent
	colorBorder = palette.Border
	colorBanner = palette.Banner
	colorBannerFg = palette.BannerFg
	colorStatusBg = palette.StatusBg
	colorStatusFg = palette.StatusFg

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
		Foreground(colorBannerFg).
		Padding(0, 2)

	bannerAccentStyle = lipgloss.NewStyle().
		Background(colorBanner).
		Foreground(colorAccent).
		Bold(true)

	statusBarStyleBottom = lipgloss.NewStyle().
		Background(colorStatusBg).
		Foreground(colorStatusFg).
		Padding(0, 1)

	modalStyle = lipgloss.NewStyle().
		Background(colorStatusBg).
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
}

// GetTheme returns the current theme type.
func GetTheme() ThemeType {
	return currentTheme
}

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
// Supports collapse/expand state, width-limited wrapping for long
// results (no hard truncation in expanded state), and a
// selected-highlight indicator for keyboard navigation.
func RenderToolCallResult(entry toolCallEntry, selected bool, maxWidth int) string {
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

	collapseIcon := "▼"
	if entry.Collapsed {
		collapseIcon = "▶"
	}

	header := fmt.Sprintf(
		"%s %s %s",
		lipgloss.NewStyle().Foreground(color).Render(collapseIcon),
		lipgloss.NewStyle().Foreground(color).Render(icon),
		lipgloss.NewStyle().Foreground(color).Bold(true).Render(entry.Name),
	)

	if entry.Status == "running" && !entry.StartTime.IsZero() {
		elapsed := time.Since(entry.StartTime)
		elapsedStr := formatDuration(elapsed)
		header += " " + lipgloss.NewStyle().Foreground(color).Render(elapsedStr)
	}

	if selected {
		header = "▌ " + header
	}

	if entry.Args != "" && !entry.Collapsed {
		argsStr := entry.Args
		if len(argsStr) > 80 {
			argsStr = argsStr[:77] + "..."
		}
		header += " " + lipgloss.NewStyle().Foreground(colorDim).Render(argsStr)
	}

	var result string
	if entry.Result != "" && !entry.Collapsed {
		// Expanded: wrap the full result at maxWidth instead of
		// truncating, so nothing is silently lost. The visible
		// window + viewport scrolling handle long content.
		w := maxWidth
		if w < 10 {
			w = 10
		}
		result = "\n" + toolCallResultStyle.Width(w).Render(entry.Result)
	} else if entry.Result != "" && entry.Collapsed {
		result = "\n" + lipgloss.NewStyle().Foreground(colorDim).Render(
			fmt.Sprintf("  [%d chars hidden — press Enter to expand]", len(entry.Result)),
		)
	}

	return header + result
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("(%dms)", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("(%.1fs)", d.Seconds())
	}
	mins := int(d.Minutes())
	secs := int(d.Seconds()) % 60
	return fmt.Sprintf("(%dm%ds)", mins, secs)
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
	return RenderBanner(util.Version, providerName, width)
}

// RenderModal renders a modal window.
// Content taller than the modal is clipped to a scrollable window
// using modal.Scroll (kept aligned with the selected item by
// followModalSelection in the model).
func RenderModal(modal *modalState, width, height int) string {
	if modal == nil {
		return ""
	}

	content := modalTitleStyle.Render(modal.Title) + "\n\n"

	// Maximum visible body rows (height minus title, padding, border).
	maxVisible := height - 3
	if maxVisible < 1 {
		maxVisible = 1
	}

	if len(modal.Items) > 0 {
		// Clamp scroll to a valid range so a shrunken modal never
		// shows blank space.
		if modal.Scroll > len(modal.Items)-maxVisible {
			modal.Scroll = len(modal.Items) - maxVisible
		}
		if modal.Scroll < 0 {
			modal.Scroll = 0
		}
		end := modal.Scroll + maxVisible
		if end > len(modal.Items) {
			end = len(modal.Items)
		}
		for i := modal.Scroll; i < end; i++ {
			item := modal.Items[i]
			if i == modal.Selected {
				content += modalSelectedStyle.Render("> "+item) + "\n"
			} else {
				content += modalItemStyle.Render("  "+item) + "\n"
			}
		}
		if len(modal.Items) > maxVisible {
			content += dimStyle.Render(fmt.Sprintf(
				"  [%d/%d — ↑↓ scroll, PgUp/PgDn jump]",
				modal.Selected+1, len(modal.Items),
			)) + "\n"
		}
	} else {
		content += modal.Content + "\n"
	}

	return modalStyle.
		Width(width).
		Height(height).
		Render(content)
}
