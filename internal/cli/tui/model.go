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
	"github.com/google/uuid"

	"github.com/km269/wukong/internal/agent"
	"github.com/km269/wukong/internal/config"
)

// maxMessages limits the chat history retained in memory.
// Beyond this limit, the oldest messages are dropped to prevent
// unbounded growth during long sessions.
const maxMessages = 500

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
	streamCancel  func() // NEW: cancel function for interrupting streaming
	streamCh      <-chan streamEvent

	// Exit flag (set by /exit or /quit command)
	quitRequested bool

	// Model info for status bar
	modelName string

	// Project tracking
	workingDir    string
	projectMgr    interface{} // *project.Manager (avoids import cycle)
	instrRecorded bool

	// Layout
	width  int
	height int
	ready  bool
}

// ModelConfig holds dependencies for creating the TUI model.
type ModelConfig struct {
	Config     *config.WukongConfig
	Loop       *agent.CoreLoop
	UserID     string
	SessionID  string
	WorkingDir string
	ProjectMgr interface{} // *project.Manager (avoids import cycle)
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

	// Build startup summary
	startupMsg := buildStartupSummary(cfg.Config)

	// Extract model name for status bar display
	modelDisplay := cfg.Config.DefaultProvider
	if p := cfg.Config.FindProvider(cfg.Config.DefaultProvider); p != nil && p.Model != "" {
		modelDisplay = p.Model
	}

	return &Model{
		viewport:    vp,
		textarea:    ta,
		userID:      cfg.UserID,
		sessionID:   cfg.SessionID,
		loop:        cfg.Loop,
		cfg:         cfg.Config,
		modelName:   modelDisplay,
		workingDir:  cfg.WorkingDir,
		projectMgr:  cfg.ProjectMgr,
		messages: []chatEntry{
			{Role: "system", Content: startupMsg},
		},
		status: "Ready",
	}
}

// buildStartupSummary creates a human-readable config summary
// displayed at the top of the chat on startup.
func buildStartupSummary(cfg *config.WukongConfig) string {
	provider := cfg.DefaultProvider
	modelName := ""
	if p := cfg.FindProvider(provider); p != nil {
		modelName = p.Model
	}

	summary := "🟢 Wukong Ready\n" +
		"  Provider: " + provider
	if modelName != "" {
		summary += "\n  Model:    " + modelName
	}
	summary += "\n  Log:      " + cfg.LogLevel
	summary += "\n  Session:  " + cfg.Session.Backend +
		" | Memory: " + cfg.Memory.Backend +
		" | Recall: " + map[bool]string{true: "on", false: "off"}[cfg.Recall.Enabled]

	if cfg.Agent.Planner != "" {
		summary += "\n  Planner:  " + cfg.Agent.Planner
	}
	summary += "\n  Tools:    parallel=" +
		map[bool]string{true: "on", false: "off"}[cfg.Agent.ParallelTools] +
		" | tool_search=" +
		map[bool]string{true: "on", false: "off"}[cfg.Agent.ToolSearchEnabled] +
		" | guardrail=" +
		map[bool]string{true: "on", false: "off"}[cfg.Security.GuardrailEnabled]

	return summary
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
				// Cancel the in-flight request and exit streaming mode.
				if m.streamCancel != nil {
					m.streamCancel()
				}
				m.streaming = false
				m.status = "Cancelled by user"
				m.messages = append(m.messages,
					chatEntry{Role: "system",
						Content: "[Request cancelled by user]"})
				m.updateViewport()
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
					if m.quitRequested {
						return m, tea.Quit
					}
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

	case streamingDeltaMsg:
		m.currentStream += string(msg)
		m.updateViewport()
		return m, readStreamEvent(m.streamCh)

	case toolCallStartMsg:
		m.toolCalls = append(m.toolCalls, toolCallEntry{
			Name:   msg.Name,
			Args:   msg.Args,
			Status: "running",
		})
		m.updateViewport()
		return m, readStreamEvent(m.streamCh)

	case toolCallResultMsg:
		for i := len(m.toolCalls) - 1; i >= 0; i-- {
			if m.toolCalls[i].Status == "running" {
				m.toolCalls[i].Result = msg.Result
				m.toolCalls[i].Status = "done"
				break
			}
		}
		m.updateViewport()
		return m, nil

	case streamingErrorMsg:
		m.currentStream += string(msg)
		m.updateViewport()
		return m, readStreamEvent(m.streamCh)

	case streamEndMsg:
		m.streaming = false
		m.streamCancel = nil
		// Save the response: prefer Content from streamEndMsg,
		// but fall back to any partially-streamed currentStream
		// so incremental delta output is never lost.
		finalContent := msg.Content
		if finalContent == "" && m.currentStream != "" {
			finalContent = m.currentStream
		}
		if finalContent != "" {
			m.addMessage("assistant", finalContent)
		}
		// Mark all running tool calls as completed.
		for i := range m.toolCalls {
			if m.toolCalls[i].Status == "running" {
				m.toolCalls[i].Status = "done"
			}
		}
		m.currentStream = ""
		m.setStatus("Ready")
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
		m.status = "Goodbye!"
		m.quitRequested = true

	case trimmed == "/exts":
		var extNames []string
		if m.cfg != nil {
			for _, ext := range m.cfg.Extensions {
				if ext.Enabled {
					extNames = append(extNames, ext.Name)
				}
			}
		}
		content := "No extensions loaded."
		if len(extNames) > 0 {
			content = "Loaded Extensions:\n  " +
				strings.Join(extNames, "\n  ")
		}
		m.messages = append(m.messages, chatEntry{
			Role:    "assistant",
			Content: content,
		})

	case trimmed == "/new":
		// Generate a new session ID for a fresh conversation.
		// The backend session service will create a new session
		// on the next message with the new ID.
		m.sessionID = generateSessionID()
		m.messages = nil
		m.toolCalls = nil
		m.currentStream = ""
		m.status = "New session started"

	case trimmed == "/clear":
		m.messages = nil
		m.toolCalls = nil
		m.currentStream = ""
		m.viewport.SetContent("")
		m.status = "Cleared"

	case trimmed == "/model":
		// Show current model/provider info
		p := m.cfg.DefaultProviderConfig()
		modelName := ""
		if p != nil {
			modelName = p.Model
		}
		m.messages = append(m.messages, chatEntry{
			Role: "assistant",
			Content: fmt.Sprintf(
				"Current: %s / %s\n"+
					"Usage: /model <model-name> to switch models",
				m.cfg.DefaultProvider, modelName,
			),
		})

	case strings.HasPrefix(trimmed, "/model "):
		// Switch to a different model
		newModel := strings.TrimSpace(
			strings.TrimPrefix(trimmed, "/model"),
		)
		p := m.cfg.DefaultProviderConfig()
		if p != nil {
			oldModel := p.Model
			p.Model = newModel
			m.status = "Ready"
			m.messages = append(m.messages, chatEntry{
				Role: "assistant",
				Content: fmt.Sprintf(
					"Switched model: %s -> %s",
					oldModel, newModel,
				),
			})
		} else {
			m.messages = append(m.messages, chatEntry{
				Role:    "assistant",
				Content: "No provider configured to switch models.",
			})
		}

	case strings.HasPrefix(trimmed, "/help"):
		m.messages = append(m.messages, chatEntry{
			Role: "assistant",
			Content: `Wukong Commands:
  /new      Start a new session
  /clear    Clear screen
  /help     Show this help
  /exts     List extensions
  /model    Show or switch model (usage: /model [name])
  /exit     Quit wukong
  Ctrl+D    Send message
  Ctrl+C    Quit

Built-in Extensions:
  developer            File ops, commands, code search
  computer_controller  Web fetch, file cache
  memory               Remember preferences & knowledge
  auto_visualiser      Charts, diagrams, tables
  tutorial             Interactive tutorials

Platform Extensions:
  todo_*               Task management & tracking
  recall_*             Cross-session history search
  tom_*                Persistent instruction injection
  code_*               JavaScript code execution
  app_*                Custom HTML app management`,
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
	workingDir string,
	projectMgr interface{},
) error {
	m := NewModel(ModelConfig{
		Config:     cfg,
		Loop:       loop,
		UserID:     userID,
		SessionID:  sessionID,
		WorkingDir: workingDir,
		ProjectMgr: projectMgr,
	})

	p := tea.NewProgram(m, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("tui error: %w", err)
	}

	return nil
}

// generateSessionID creates a new unique session identifier.
// Uses a timestamp-based prefix for sortability followed by random
// bytes for uniqueness.
func generateSessionID() string {
	return uuid.New().String()
}
