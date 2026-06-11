// Package tui provides the terminal user interface for wukong.
// It implements a Bubbletea-based TUI with three-zone layout:
// conversation area, tool call status area, and input area.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/km269/wukong/internal/agent"
	"github.com/km269/wukong/internal/config"
)

// chatEntry represents a single message in the conversation.
type chatEntry struct {
	Role    string
	Content string
}

// toolCallEntry tracks a running/completed tool call.
type toolCallEntry struct {
	Name   string
	Args   string
	Result string
	Status string // "running", "done", "error"
}

// Model is the Bubbletea model for the wukong TUI.
type Model struct {
	viewport viewport.Model
	textarea textarea.Model

	// Session state
	userID    string
	sessionID string
	messages  []chatEntry
	status    string

	// Tool call display
	toolCalls []toolCallEntry

	// Agent loop
	loop *agent.CoreLoop
	cfg  *config.WukongConfig

	// Streaming state
	streaming     bool
	currentStream string

	// Layout
	width  int
	height int
	ready  bool
}

// ModelConfig holds dependencies for creating the TUI model.
type ModelConfig struct {
	Config    *config.WukongConfig
	Loop      *agent.CoreLoop
	UserID    string
	SessionID string
}

// NewModel creates a new Bubbletea TUI model.
func NewModel(cfg ModelConfig) *Model {
	ta := textarea.New()
	ta.Placeholder = "Type your message... (Ctrl+D to send, Ctrl+C to quit)"
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.Focus()

	vp := viewport.New(80, 20)

	return &Model{
		viewport:  vp,
		textarea:  ta,
		userID:    cfg.UserID,
		sessionID: cfg.SessionID,
		loop:      cfg.Loop,
		cfg:       cfg.Config,
		messages:  make([]chatEntry, 0),
		status:    "Ready",
	}
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	return textarea.Blink
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			if m.streaming {
				return m, nil
			}
			return m, tea.Quit

		case tea.KeyCtrlD:
			if !m.streaming {
				input := strings.TrimSpace(m.textarea.Value())
				if input == "" {
					return m, nil
				}

				if strings.HasPrefix(input, "/") {
					m.handleCommand(input)
					m.textarea.Reset()
					m.updateViewport()
					return m, nil
				}

				m.textarea.Reset()
				return m, m.sendMessage(input)
			}
		}

	case tea.WindowSizeMsg:
		m.handleResize(msg)
		m.updateViewport()
		return m, nil

	case refreshMsg:
		m.updateViewport()
		return m, nil
	}

	// Update sub-components
	var taCmd tea.Cmd
	m.textarea, taCmd = m.textarea.Update(msg)

	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)

	m.updateViewport()

	return m, tea.Batch(taCmd, vpCmd)
}

// View implements tea.Model.
func (m *Model) View() string {
	if !m.ready {
		return "\n  Initializing Wukong...\n"
	}

	var modelName string
	p := m.cfg.DefaultProviderConfig()
	if p != nil {
		modelName = p.Model
	}

	statusBar := RenderStatusBar(
		m.sessionID, m.status,
		m.cfg.DefaultProvider, modelName,
		m.width,
	)

	conversation := m.viewport.View()

	bottom := m.renderToolCalls()
	bottom += "\n" + m.textarea.View()

	return lipgloss.JoinVertical(
		lipgloss.Top,
		statusBar,
		conversation,
		bottom,
	)
}

func (m *Model) handleResize(msg tea.WindowSizeMsg) {
	m.width = msg.Width
	m.height = msg.Height

	headerHeight := 3
	footerHeight := 6

	if !m.ready {
		m.viewport = viewport.New(
			msg.Width-4, msg.Height-headerHeight-footerHeight,
		)
		m.textarea.SetWidth(msg.Width - 4)
		m.ready = true
	} else {
		m.viewport.Width = msg.Width - 4
		m.viewport.Height = msg.Height - headerHeight - footerHeight
	}
}

func (m *Model) updateViewport() {
	var content string
	for _, msg := range m.messages {
		switch msg.Role {
		case "user":
			content += RenderUserMessage(msg.Content) + "\n\n"
		case "assistant":
			content += RenderAssistantMessage(msg.Content) + "\n\n"
		}
	}

	if m.currentStream != "" {
		content += RenderAssistantMessage(m.currentStream)
	}

	m.viewport.SetContent(content)
	m.viewport.GotoBottom()
}

func (m *Model) renderToolCalls() string {
	if len(m.toolCalls) == 0 {
		return ""
	}

	var parts []string
	for _, tc := range m.toolCalls {
		parts = append(parts, RenderToolCall(tc.Name, tc.Status))
	}

	return lipgloss.NewStyle().
		Padding(0, 2).
		Render(strings.Join(parts, "  "))
}

func (m *Model) handleCommand(input string) {
	trimmed := strings.TrimSpace(input)
	switch {
	case trimmed == "/exit" || trimmed == "/quit":
		// handled by Ctrl+C

	case trimmed == "/new":
		m.sessionID = ""
		m.messages = nil
		m.toolCalls = nil
		m.currentStream = ""
		m.status = "New session"

	case trimmed == "/clear":
		m.messages = nil
		m.toolCalls = nil
		m.currentStream = ""
		m.viewport.SetContent("")
		m.status = "Cleared"

	case strings.HasPrefix(trimmed, "/help"):
		m.messages = append(m.messages, chatEntry{
			Role: "assistant",
			Content: `Wukong Commands:
  /new    Start a new session
  /clear  Clear screen
  /help   Show this help
  /exit   Quit wukong
  Ctrl+D  Send message
  Ctrl+C  Quit

Built-in Tools:
  file_read, file_write  Read/write files
  command_execute        Run shell commands
  code_search            Search code patterns
  directory_list         List files
  todo_*                 Task management`,
		})

	default:
		m.messages = append(m.messages, chatEntry{
			Role: "assistant",
			Content: "Unknown command: " + trimmed +
				". Type /help for available commands.",
		})
	}
}

// StartTUI initializes and runs the Bubbletea TUI.
func StartTUI(
	cfg *config.WukongConfig,
	loop *agent.CoreLoop,
	userID, sessionID string,
) error {
	m := NewModel(ModelConfig{
		Config:    cfg,
		Loop:      loop,
		UserID:    userID,
		SessionID: sessionID,
	})

	p := tea.NewProgram(m, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("tui error: %w", err)
	}

	return nil
}
