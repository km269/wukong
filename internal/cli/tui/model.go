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
	"github.com/km269/wukong/internal/util"
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

// ModalType defines the type of modal window.
type ModalType int

const (
	ModalNone ModalType = iota
	ModalCommands
	ModalSkills
	ModalSettings
)

// modalState represents the state of a modal window.
type modalState struct {
	Type     ModalType
	Title    string
	Content  string
	Selected int
	Items    []string
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
	streamCancel  func() // cancel function for interrupting streaming
	streamCh      <-chan streamEvent

	// Exit flag (set by /exit or /quit command)
	quitRequested bool

	// Model info for status bar
	modelName string

	// Project tracking
	workingDir    string
	projectMgr    any // *project.Manager (avoids import cycle)
	instrRecorded bool

	// Layout
	width  int
	height int
	ready  bool

	// Header/Banner info
	providerName string
	toolCount    int
	skillName    string

	// Log buffer for status bar
	logBuffer []string

	// Modal state
	modal       *modalState
	modalHeight int
	modalWidth  int
}

// ModelConfig holds dependencies for creating the TUI model.
type ModelConfig struct {
	Config     *config.WukongConfig
	Loop       *agent.CoreLoop
	UserID     string
	SessionID  string
	WorkingDir string
	ProjectMgr any // *project.Manager (avoids import cycle)
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
	providerDisplay := cfg.Config.DefaultProvider
	if p := cfg.Config.FindProvider(cfg.Config.DefaultProvider); p != nil {
		if p.Model != "" {
			modelDisplay = p.Model
		}
		if p.Name != "" {
			providerDisplay = p.Name
		}
	}

	// Count enabled tools/extensions
	toolCount := 0
	for _, ext := range cfg.Config.Extensions {
		if ext.Enabled {
			toolCount++
		}
	}

	return &Model{
		viewport:     vp,
		textarea:     ta,
		userID:       cfg.UserID,
		sessionID:    cfg.SessionID,
		loop:         cfg.Loop,
		cfg:          cfg.Config,
		modelName:    modelDisplay,
		providerName: providerDisplay,
		toolCount:    toolCount,
		skillName:    "-",
		workingDir:   cfg.WorkingDir,
		projectMgr:   cfg.ProjectMgr,
		messages: []chatEntry{
			{Role: "system", Content: startupMsg},
		},
		status: "Ready",
	}
}

// buildStartupSummary creates a human-readable config summary
// displayed at the top of the chat on startup.
func buildStartupSummary(cfg *config.WukongConfig) string {
	summary := "🟢 Wukong Ready\n" +
		"  Log:      " + cfg.LogLevel +
		" | Session: " + cfg.Session.Backend +
		" | Memory: " + cfg.Memory.Backend +
		" | Recall: " + map[bool]string{true: "on", false: "off"}[cfg.Recall.Enabled] +
		"(" + cfg.Recall.SearchMode + ")"

	if cfg.Agent.Planner != "" {
		summary += "\n  Planner:  " + cfg.Agent.Planner
	}

	agentFeatures := []string{}
	if cfg.Agent.ThinkingEnabled != nil && *cfg.Agent.ThinkingEnabled {
		agentFeatures = append(agentFeatures, "thinking")
	}
	if cfg.Agent.ContextCompaction {
		agentFeatures = append(agentFeatures, "context_compaction")
	}
	if cfg.Agent.Streaming {
		agentFeatures = append(agentFeatures, "streaming")
	}
	if len(agentFeatures) > 0 {
		summary += "\n  Agent:    " + strings.Join(agentFeatures, ", ")
	}

	summary += "\n  Security: permission=" + string(cfg.Security.PermissionMode) +
		" | guardrail=" + map[bool]string{true: "on", false: "off"}[cfg.Security.GuardrailEnabled]

	summary += "\n  Tools:    parallel=" +
		map[bool]string{true: "on", false: "off"}[cfg.Agent.ParallelTools] +
		" | tool_search=" +
		map[bool]string{true: "on", false: "off"}[cfg.Agent.ToolSearchEnabled]

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
		// Handle modal navigation first
		if m.modal != nil {
			switch msg.Type {
			case tea.KeyEscape:
				m.modal = nil
				return m, nil
			case tea.KeyEnter:
				m.handleModalSelection()
				return m, nil
			case tea.KeyUp:
				if m.modal.Selected > 0 {
					m.modal.Selected--
				}
				return m, nil
			case tea.KeyDown:
				if m.modal.Selected < len(m.modal.Items)-1 {
					m.modal.Selected++
				}
				return m, nil
			}
			return m, nil
		}

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
		m.addMessage("system", string(msg))
		m.updateViewport()
		return m, readStreamEvent(m.streamCh)

	case streamEndMsg:
		if !m.streaming {
			return m, nil
		}
		m.streaming = false
		m.streamCancel = nil
		// Use currentStream as the final content since it already
		// contains all accumulated delta content during streaming.
		// This avoids duplication with msg.Content which contains
		// the same full content.
		var finalContent string
		if m.currentStream != "" {
			finalContent = m.currentStream
		} else if msg.Content != "" {
			finalContent = msg.Content
		}
		m.currentStream = ""
		if finalContent != "" {
			m.addMessage("assistant", finalContent)
		}
		// Mark all running tool calls as completed.
		for i := range m.toolCalls {
			if m.toolCalls[i].Status == "running" {
				m.toolCalls[i].Status = "done"
			}
		}
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

	// Render banner (top)
	banner := RenderBanner("v0.2.4", m.providerName, m.width)

	// Render status bar (below banner)
	statusBar := RenderStatusBarBottom(
		m.modelName,
		m.providerName,
		m.skillName,
		m.toolCount,
		m.status,
		m.logBuffer,
		m.width,
	)

	// Render input area (bottom)
	inputArea := m.textarea.View()

	// Calculate heights
	bannerHeight := lipgloss.Height(banner)
	statusBarHeight := lipgloss.Height(statusBar)
	inputHeight := lipgloss.Height(inputArea)

	// Recalculate viewport height to fit remaining space
	availableHeight := m.height - bannerHeight - statusBarHeight - inputHeight
	if availableHeight < 5 {
		availableHeight = 5
	}
	m.viewport.Height = availableHeight

	// Render conversation area
	conversation := m.viewport.View()

	// If modal is open, show it centered
	if m.modal != nil {
		modalContent := RenderModal(m.modal, m.modalWidth, m.modalHeight)
		return lipgloss.Place(
			m.width,
			m.height,
			lipgloss.Center,
			lipgloss.Center,
			modalContent,
		)
	}

	return lipgloss.JoinVertical(
		lipgloss.Top,
		banner,
		statusBar,
		conversation,
		inputArea,
	)
}

func (m *Model) handleResize(msg tea.WindowSizeMsg) {
	m.width = msg.Width
	m.height = msg.Height

	if !m.ready {
		m.viewport = viewport.New(
			msg.Width, msg.Height-8,
		)
		m.textarea.SetWidth(msg.Width)
		m.ready = true
	} else {
		m.viewport.Width = msg.Width
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
		case "system":
			content += RenderSystemMessage(msg.Content) + "\n\n"
		}
	}

	for _, tc := range m.toolCalls {
		content += RenderToolCallResult(tc) + "\n\n"
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

	var content string
	for _, tc := range m.toolCalls {
		content += RenderToolCallResult(tc) + "\n"
	}

	return lipgloss.NewStyle().
		Padding(0, 2).
		Render(content)
}

func (m *Model) renderSystemMessages() string {
	var content string
	for _, msg := range m.messages {
		if msg.Role == "system" {
			content += RenderSystemMessage(msg.Content) + "\n"
		}
	}

	if content == "" {
		return ""
	}

	return lipgloss.NewStyle().
		Padding(0, 2).
		Render(content)
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
			Role:    "system",
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
			Role: "system",
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
			m.modelName = newModel
			m.status = "Ready"
			m.messages = append(m.messages, chatEntry{
				Role: "system",
				Content: fmt.Sprintf(
					"Switched model: %s -> %s",
					oldModel, newModel,
				),
			})
		} else {
			m.messages = append(m.messages, chatEntry{
				Role:    "system",
				Content: "No provider configured to switch models.",
			})
		}

	case trimmed == "/commands":
		m.openCommandsModal()

	case trimmed == "/skills":
		m.openSkillsModal()

	case trimmed == "/settings":
		m.openSettingsModal()

	case strings.HasPrefix(trimmed, "/help"):
		m.messages = append(m.messages, chatEntry{
			Role: "system",
			Content: `Wukong Commands:
  /new        Start a new session
  /clear      Clear screen
  /help       Show this help
  /exts       List extensions
  /model      Show or switch model (usage: /model [name])
  /commands   Open command menu
  /skills     Open skills browser
  /settings   Open settings panel
  /exit       Quit wukong
  Ctrl+D      Send message
  Ctrl+C      Quit

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
			Role: "system",
			Content: "Unknown command: " + trimmed +
				". Type /help for available commands.",
		})
	}
}

func (m *Model) openCommandsModal() {
	commands := []string{
		"/new        - Start a new session",
		"/clear      - Clear screen",
		"/exts       - List extensions",
		"/model      - Show or switch model",
		"/skills     - Browse available skills",
		"/settings   - Open settings",
		"/help       - Show help",
		"/exit       - Quit wukong",
	}
	m.modal = &modalState{
		Type:     ModalCommands,
		Title:    "Available Commands",
		Selected: 0,
		Items:    commands,
	}
	m.modalWidth = 60
	m.modalHeight = 14
}

func (m *Model) openSkillsModal() {
	var skills []string
	if m.cfg != nil {
		for _, ext := range m.cfg.Extensions {
			if ext.Enabled {
				skills = append(skills, ext.Name)
			}
		}
	}
	if len(skills) == 0 {
		skills = []string{"No skills loaded"}
	}
	m.modal = &modalState{
		Type:     ModalSkills,
		Title:    "Loaded Skills",
		Selected: 0,
		Items:    skills,
	}
	m.modalWidth = 60
	m.modalHeight = min(len(skills)+4, 14)
}

func (m *Model) openSettingsModal() {
	content := fmt.Sprintf(`Provider: %s
Model:    %s
Log Level: %s
Memory:   %s
Recall:   %s
Session:  %s

Press ESC to close`,
		m.providerName,
		m.modelName,
		m.cfg.LogLevel,
		m.cfg.Memory.Backend,
		map[bool]string{true: "on", false: "off"}[m.cfg.Recall.Enabled],
		m.cfg.Session.Backend,
	)
	m.modal = &modalState{
		Type:    ModalSettings,
		Title:   "Settings",
		Content: content,
	}
	m.modalWidth = 60
	m.modalHeight = 14
}

func (m *Model) handleModalSelection() {
	if m.modal == nil {
		return
	}

	switch m.modal.Type {
	case ModalCommands:
		selected := m.modal.Items[m.modal.Selected]
		// Extract command from selection
		if strings.HasPrefix(selected, "/") {
			cmd := strings.Split(selected, " ")[0]
			m.modal = nil
			m.handleCommand(cmd)
		}
	case ModalSkills:
		if m.modal.Selected >= 0 && m.modal.Selected < len(m.modal.Items) {
			m.skillName = m.modal.Items[m.modal.Selected]
			m.status = "Skill: " + m.skillName
		}
		m.modal = nil
	case ModalSettings:
		m.modal = nil
	}
}

// StartTUI initializes and runs the Bubbletea TUI.
func StartTUI(
	cfg *config.WukongConfig,
	loop *agent.CoreLoop,
	userID, sessionID string,
	workingDir string,
	projectMgr any,
) error {
	util.SetQuietMode()

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
