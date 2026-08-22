// Package tui provides the terminal user interface for wukong.
// It implements a Bubbletea-based TUI with three-zone layout:
// conversation area, tool call status area, and input area.
package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
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

// maxCommandHistory limits the number of commands saved in history.
const maxCommandHistory = 100

// chatEntry represents a single message in the conversation.
type chatEntry struct {
	Role    string
	Content string
}

// toolAuditEntry records a tool invocation for the audit panel.
type toolAuditEntry struct {
	Name       string
	ArgsSize   int
	ResultSize int
	DurationMs int64
	IsError    bool
	Timestamp  string
}

const maxAuditEntries = 50

// toolCallEntry tracks a running/completed tool call.
type toolCallEntry struct {
	Name      string
	Args      string
	Result    string
	Status    string // "running", "done", "error"
	Collapsed bool   // UI state: collapsed or expanded
	StartTime time.Time
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

	// Tool audit log for /audit command
	auditLog []toolAuditEntry

	// Agent loop
	loop *agent.CoreLoop
	cfg  *config.WukongConfig

	// Streaming state
	streaming     bool
	currentStream string
	streamCancel  func() // cancel function for interrupting streaming
	streamCh      <-chan streamEvent
	streamDone    chan struct{} // signaled when stream goroutine finishes

	// Exit flag (set by /exit or /quit command)
	quitRequested bool
	cleanupOnce   sync.Once // ensures cleanup runs only once

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
	version      string

	// Log buffer for status bar
	logBuffer []string

	// Modal state
	modal       *modalState
	modalHeight int
	modalWidth  int

	// Markdown rendering (cached for performance)
	mdRenderer *glamour.TermRenderer

	// Command history for Up/Down arrow navigation
	cmdHistory    []string
	cmdHistoryIdx int

	// Incremental rendering cache — split into two independent layers so that
	// tool status changes during streaming do NOT trigger a full message rebuild.
	cachedMessages      string
	cachedMsgCount      int
	cachedTools         string
	cachedToolCount     int
	cachedToolStatus    []string
	cachedToolCollapsed []bool
	cachedToolSelected  int

	// Tool-panel navigation state
	toolSelectedIdx int

	// Viewport scroll control: when false, auto-scroll is disabled
	// (user has manually scrolled up to read history)
	autoScroll bool

	// When true, user has pressed Ctrl+C once to cancel streaming.
	// Second Ctrl+C will trigger exit.
	cancelled bool

	// Debounce timer for SetContent to reduce thrashing during streaming
	lastRenderTime time.Time
}

// ModelConfig holds dependencies for creating the TUI model.
type ModelConfig struct {
	Config     *config.WukongConfig
	Loop       *agent.CoreLoop
	UserID     string
	SessionID  string
	WorkingDir string
	ProjectMgr any // *project.Manager (avoids import cycle)
	Version    string
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

	// Initialize markdown renderer with auto theme detection
	mdRenderer, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(80),
	)
	if err != nil {
		mdRenderer = nil
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
		version:      cfg.Version,
		workingDir:   cfg.WorkingDir,
		projectMgr:   cfg.ProjectMgr,
		mdRenderer:   mdRenderer,
		autoScroll:   true,
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

// requestExit initiates a clean exit sequence:
// 1. Cancel any in-flight streaming request
// 2. Wait for the streaming goroutine to finish
// 3. Return tea.Quit to stop the Bubbletea program
//
// Note: loop.Close() is intentionally NOT called here — it is handled
// by session.go's shutdownBootstrap which uses an independent context,
// avoiding "context deadline exceeded" errors from in-flight events.
func (m *Model) requestExit() tea.Cmd {
	m.cleanupOnce.Do(func() {
		// Cancel streaming if active
		if m.streaming && m.streamCancel != nil {
			m.streamCancel()
		}
		// Wait for stream goroutine to finish (with timeout)
		if m.streamDone != nil {
			select {
			case <-m.streamDone:
				// Stream goroutine finished
			case <-time.After(3 * time.Second):
				// Timeout - proceed with exit anyway
			}
		}
	})

	m.quitRequested = true
	m.status = "Goodbye!"
	return tea.Quit
}

// cleanup stops any running streams.
// Safe to call multiple times (protected by sync.Once).
//
// Note: loop.Close() is NOT called here — session.go's
// shutdownBootstrap handles that with an independent context.
func (m *Model) cleanup() {
	m.cleanupOnce.Do(func() {
		if m.streaming && m.streamCancel != nil {
			m.streamCancel()
		}
		if m.streamDone != nil {
			select {
			case <-m.streamDone:
			case <-time.After(3 * time.Second):
			}
		}
	})
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
				if m.streamCancel != nil {
					m.streamCancel()
				}
				m.cancelled = true
				m.status = "Cancelling..."
				m.messages = append(m.messages,
					chatEntry{Role: "system",
						Content: "[Request cancelled by user]\nWaiting for stream to finish...\nPress Ctrl+C again to force-quit, or type /exit"})
				m.updateViewport()
				// Return nil — still need to keep consuming streamCh
				// until the goroutine sends streamEndMsg or closes channel.
				return m, nil
			}
			if m.cancelled || m.quitRequested {
				return m, m.requestExit()
			}
			return m, m.requestExit()

		case tea.KeyPgUp:
			m.viewport.PageUp()
			m.autoScroll = false
			return m, nil

		case tea.KeyPgDown:
			m.viewport.PageDown()
			m.autoScroll = false
			return m, nil

		case tea.KeyTab:
			if m.modal != nil {
				return m, nil
			}
			if len(m.toolCalls) > 0 {
				idx := m.toolSelectedIdx
				if idx >= len(m.toolCalls) {
					idx = len(m.toolCalls) - 1
				}
				if m.toolCalls[idx].Result != "" {
					m.toolCalls[idx].Collapsed = !m.toolCalls[idx].Collapsed
					m.updateViewport()
					return m, nil
				}
				// Tool has no result yet — still select it for when it arrives
				m.toolSelectedIdx = idx
				m.updateViewport()
				return m, nil
			}
			// No tools: re-enable auto-scroll and let key pass through
			m.autoScroll = true
			return m, nil

		case tea.KeyShiftTab:
			if len(m.toolCalls) > 0 {
				if m.toolSelectedIdx > 0 {
					m.toolSelectedIdx--
				} else {
					m.toolSelectedIdx = len(m.toolCalls) - 1
				}
				m.updateViewport()
				return m, nil
			}

		case tea.KeyUp:
			if msg.Alt {
				m.viewport.LineUp(1)
				m.autoScroll = false
				return m, nil
			}
			if !m.streaming {
				input := m.textarea.Value()
				if input == "" || !strings.ContainsRune(input, '\n') {
					if m.cmdHistoryIdx < len(m.cmdHistory) {
						m.cmdHistoryIdx++
						histIdx := len(m.cmdHistory) - m.cmdHistoryIdx
						if histIdx >= 0 && histIdx < len(m.cmdHistory) {
							m.textarea.SetValue(m.cmdHistory[histIdx])
							return m, nil
						}
					}
				}
			}

		case tea.KeyDown:
			if msg.Alt {
				m.viewport.LineDown(1)
				m.autoScroll = false
				return m, nil
			}
			if !m.streaming {
				input := m.textarea.Value()
				if input == "" || !strings.ContainsRune(input, '\n') {
					if m.cmdHistoryIdx > 0 {
						m.cmdHistoryIdx--
						histIdx := len(m.cmdHistory) - m.cmdHistoryIdx
						if histIdx >= 0 && histIdx < len(m.cmdHistory) {
							m.textarea.SetValue(m.cmdHistory[histIdx])
							return m, nil
						}
					} else if m.cmdHistoryIdx == 0 {
						m.textarea.SetValue("")
						return m, nil
					}
				}
			}

		case tea.KeyHome:
			if msg.Alt {
				m.viewport.GotoTop()
				m.autoScroll = false
				return m, nil
			}

		case tea.KeyEnd:
			if msg.Alt {
				m.viewport.GotoBottom()
				m.autoScroll = true
				return m, nil
			}

		case tea.KeyCtrlD:
			if !m.streaming {
				input := strings.TrimSpace(m.textarea.Value())
				if input == "" {
					return m, nil
				}

				m.addToHistory(input)

				if strings.HasPrefix(input, "/") {
					m.handleCommand(input)
					m.textarea.Reset()
					m.cmdHistoryIdx = 0
					m.updateViewport()
					if m.quitRequested {
						m.cleanup()
						return m, tea.Quit
					}
					return m, nil
				}

				m.textarea.Reset()
				m.cmdHistoryIdx = 0
				return m, m.sendMessage(input)
			}
			// Streaming in progress: Ctrl+D is ignored.
			// User must wait or press Ctrl+C to cancel.
			m.setStatus("Streaming — press Ctrl+C to cancel")
			return m, nil
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
		m.setStatus("Streaming...")
		m.updateViewport()
		return m, readStreamEvent(m.streamCh)

	case toolCallStartMsg:
		m.toolCalls = append(m.toolCalls, toolCallEntry{
			Name:      msg.Name,
			Args:      msg.Args,
			Status:    "running",
			StartTime: time.Now(),
		})
		m.setStatus("Running: " + msg.Name)
		m.updateViewport()
		return m, readStreamEvent(m.streamCh)

	case toolCallResultMsg:
		for i := len(m.toolCalls) - 1; i >= 0; i-- {
			if m.toolCalls[i].Status == "running" &&
				m.toolCalls[i].Name == msg.Name {
				m.toolCalls[i].Result = msg.Result
				m.toolCalls[i].Status = "done"
				m.recordAuditEntry(m.toolCalls[i])
				break
			}
		}
		pendingTools := 0
		for _, tc := range m.toolCalls {
			if tc.Status == "running" {
				pendingTools++
			}
		}
		if pendingTools > 0 {
			m.setStatus(fmt.Sprintf("%d tool(s) running...", pendingTools))
		} else {
			m.setStatus("Thinking...")
		}
		m.updateViewport()
		return m, readStreamEvent(m.streamCh)

	case streamingErrorMsg:
		m.addMessage("system", string(msg))
		m.currentStream = ""
		m.streaming = false
		m.setStatus("Error occurred")
		m.updateViewport()
		return m, readStreamEvent(m.streamCh)

	case streamEndMsg:
		if !m.streaming && !m.cancelled {
			return m, nil
		}
		m.streaming = false
		m.cancelled = false
		m.streamCancel = nil
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
		toolCount := 0
		errorCount := 0
		for i := range m.toolCalls {
			if m.toolCalls[i].Status == "running" {
				m.toolCalls[i].Status = "done"
			}
			toolCount++
			if m.toolCalls[i].Status == "error" {
				errorCount++
			}
		}
		m.autoScroll = true
		if m.cancelled {
			m.setStatus("Cancelled")
		} else if toolCount > 0 {
			if errorCount > 0 {
				m.setStatus(fmt.Sprintf(
					"Done — %d tool(s) used, %d error(s)",
					toolCount, errorCount,
				))
			} else {
				m.setStatus(fmt.Sprintf(
					"Done — %d tool(s) used", toolCount,
				))
			}
		} else {
			m.setStatus("Ready")
		}
		m.updateViewport()
		return m, nil
	}

	// Update sub-components.
	// IMPORTANT: Only pass NON-control keys to the textarea.
	// Control keys (Ctrl+D, Ctrl+C, etc.) are intercepted above for
	// custom handling. Passing them through would cause the textarea
	// to append control runes (e.g. EOT=4 for Ctrl+D) to its internal
	// buffer AFTER we've already reset the value, leaving the textarea
	// in a corrupted state that makes subsequent Ctrl+D presses
	// unresponsive because the buffer is never truly empty.
	var taCmd tea.Cmd
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.Type != tea.KeyCtrlD &&
			keyMsg.Type != tea.KeyCtrlC &&
			keyMsg.Type != tea.KeyEsc &&
			keyMsg.Type != tea.KeyEnter &&
			!keyMsg.Alt {
			m.textarea, taCmd = m.textarea.Update(msg)
		}
	} else {
		m.textarea, taCmd = m.textarea.Update(msg)
	}

	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)

	return m, tea.Batch(taCmd, vpCmd)
}

// View implements tea.Model.
func (m *Model) View() string {
	if !m.ready {
		return "\n  Initializing Wukong...\n"
	}

	// Render banner (top)
	banner := RenderBanner(m.version, m.providerName, m.width)

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

	m.recalculateLayout()
}

// recalculateLayout computes and caches the viewport height
// based on the current layout elements. Must be called after any
// change to the layout that affects heights.
func (m *Model) recalculateLayout() {
	banner := RenderBanner(m.version, m.providerName, m.width)
	statusBar := RenderStatusBarBottom(
		m.modelName, m.providerName, m.skillName,
		m.toolCount, m.status, m.logBuffer, m.width,
	)
	inputArea := m.textarea.View()

	bannerHeight := lipgloss.Height(banner)
	statusBarHeight := lipgloss.Height(statusBar)
	inputHeight := lipgloss.Height(inputArea)

	availableHeight := m.height - bannerHeight - statusBarHeight - inputHeight
	if availableHeight < 5 {
		availableHeight = 5
	}
	m.viewport.Height = availableHeight
}

func (m *Model) updateViewport() {
	msgCount := len(m.messages)
	toolCount := len(m.toolCalls)

	if m.toolSelectedIdx >= toolCount && toolCount > 0 {
		m.toolSelectedIdx = toolCount - 1
	}
	if toolCount == 0 {
		m.toolSelectedIdx = 0
	}

	if msgCount < m.cachedMsgCount {
		m.cachedMessages = ""
		m.cachedMsgCount = 0
	}
	if toolCount < m.cachedToolCount {
		m.cachedTools = ""
		m.cachedToolCount = 0
	}

	msgChanged := msgCount != m.cachedMsgCount

	if msgChanged {
		var buf strings.Builder
		start := m.cachedMsgCount
		if msgCount < m.cachedMsgCount {
			start = 0
			m.cachedMessages = ""
		}
		for i := start; i < msgCount; i++ {
			msg := m.messages[i]
			switch msg.Role {
			case "user":
				buf.WriteString(RenderUserMessage(msg.Content) + "\n\n")
			case "assistant":
				buf.WriteString(m.renderAssistantMessage(msg.Content) + "\n\n")
			case "system":
				buf.WriteString(RenderSystemMessage(msg.Content) + "\n\n")
			}
		}
		if start == 0 || m.cachedMessages == "" {
			m.cachedMessages = buf.String()
		} else {
			m.cachedMessages += buf.String()
		}
		m.cachedMsgCount = msgCount
	}

	toolChanged := false
	if toolCount != m.cachedToolCount {
		toolChanged = true
	}
	if m.toolSelectedIdx != m.cachedToolSelected {
		toolChanged = true
	}
	if !toolChanged {
		for i := range m.toolCalls {
			if i < len(m.cachedToolStatus) &&
				m.toolCalls[i].Status != m.cachedToolStatus[i] {
				toolChanged = true
				break
			}
			if i < len(m.cachedToolCollapsed) &&
				m.toolCalls[i].Collapsed != m.cachedToolCollapsed[i] {
				toolChanged = true
				break
			}
		}
	}

	if toolChanged {
		var buf strings.Builder
		for i, tc := range m.toolCalls {
			selected := i == m.toolSelectedIdx
			buf.WriteString(RenderToolCallResult(tc, selected) + "\n\n")
		}
		m.cachedTools = buf.String()
		m.cachedToolCount = toolCount
		m.cachedToolSelected = m.toolSelectedIdx
		m.cachedToolStatus = make([]string, toolCount)
		m.cachedToolCollapsed = make([]bool, toolCount)
		for i := range m.toolCalls {
			m.cachedToolStatus[i] = m.toolCalls[i].Status
			m.cachedToolCollapsed[i] = m.toolCalls[i].Collapsed
		}
	}

	var content strings.Builder
	content.WriteString(m.cachedMessages)
	content.WriteString(m.cachedTools)

	if m.currentStream != "" {
		content.WriteString(m.renderAssistantMessage(m.currentStream) + "\n")
	}

	// Force render when structure changes (messages/tools changed)
	// Only debounce trivial stream-delta-only updates.
	structuralChange := msgChanged || toolChanged

	now := time.Now()
	if !structuralChange && now.Sub(m.lastRenderTime) < 16*time.Millisecond {
		return
	}
	m.lastRenderTime = now

	m.viewport.SetContent(content.String())

	if m.autoScroll {
		m.viewport.GotoBottom()
	}
}

func (m *Model) renderAssistantMessage(content string) string {
	rendered := RenderAssistantMessage(content)
	if m.mdRenderer == nil {
		return rendered
	}

	md, err := m.mdRenderer.Render(content)
	if err != nil {
		return rendered
	}
	if md == "" {
		return rendered
	}
	return assistantStyle.Render("Wukong: ") + md
}

func (m *Model) addToHistory(input string) {
	if len(m.cmdHistory) == 0 || m.cmdHistory[len(m.cmdHistory)-1] != input {
		m.cmdHistory = append(m.cmdHistory, input)
		if len(m.cmdHistory) > maxCommandHistory {
			m.cmdHistory = m.cmdHistory[1:]
		}
	}
}

func (m *Model) setLog(msg string) {
	if msg == "" {
		return
	}
	m.logBuffer = append(m.logBuffer, msg)
	if len(m.logBuffer) > 5 {
		m.logBuffer = m.logBuffer[1:]
	}
}

// recordAuditEntry logs a completed tool call into the bounded
// audit ring buffer used by the /audit command.
func (m *Model) recordAuditEntry(tc toolCallEntry) {
	var durationMs int64
	if !tc.StartTime.IsZero() {
		durationMs = time.Since(tc.StartTime).Milliseconds()
	}
	entry := toolAuditEntry{
		Name:       tc.Name,
		ArgsSize:   len(tc.Args),
		ResultSize: len(tc.Result),
		DurationMs: durationMs,
		IsError:    tc.Status == "error",
		Timestamp:  time.Now().Format("15:04:05"),
	}
	if len(m.auditLog) >= maxAuditEntries {
		m.auditLog = m.auditLog[1:]
	}
	m.auditLog = append(m.auditLog, entry)
}

// renderAuditPanel builds a string representation of the recent
// audit entries for display in the chat area.
func (m *Model) renderAuditPanel(limit int) string {
	if len(m.auditLog) == 0 {
		return "No tool audit entries yet."
	}
	if limit <= 0 || limit > len(m.auditLog) {
		limit = len(m.auditLog)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Tool Audit (last %d of %d):\n", limit, len(m.auditLog)))
	sb.WriteString(strings.Repeat("─", 50) + "\n")

	// Show most recent first
	start := len(m.auditLog) - limit
	for i := len(m.auditLog) - 1; i >= start; i-- {
		e := m.auditLog[i]
		statusIcon := "✓"
		if e.IsError {
			statusIcon = "✗"
		}
		sb.WriteString(fmt.Sprintf(
			" %s %-20s | args: %5dB | result: %5dB | %6dms | %s\n",
			statusIcon,
			e.Name,
			e.ArgsSize,
			e.ResultSize,
			e.DurationMs,
			e.Timestamp,
		))
	}
	return sb.String()
}

func (m *Model) handleCommand(input string) {
	trimmed := strings.TrimSpace(input)
	switch {
	case trimmed == "/exit" || trimmed == "/quit":
		m.cleanup()
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
		m.sessionID = generateSessionID()
		m.messages = nil
		m.toolCalls = nil
		m.auditLog = nil
		m.currentStream = ""
		m.instrRecorded = false
		m.autoScroll = true
		m.status = "New session started"
		m.cachedMessages = ""
		m.cachedTools = ""
		m.cachedMsgCount = 0
		m.cachedToolCount = 0
		m.cachedToolStatus = nil
		m.cachedToolCollapsed = nil
		m.cachedToolSelected = -1

	case trimmed == "/clear":
		m.messages = nil
		m.toolCalls = nil
		m.currentStream = ""
		m.autoScroll = true
		m.cachedMessages = ""
		m.cachedTools = ""
		m.cachedMsgCount = 0
		m.cachedToolCount = 0
		m.cachedToolStatus = nil
		m.cachedToolCollapsed = nil
		m.cachedToolSelected = -1
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

	case trimmed == "/audit":
		m.messages = append(m.messages, chatEntry{
			Role:    "system",
			Content: m.renderAuditPanel(10),
		})

	case strings.HasPrefix(trimmed, "/audit "):
		limitStr := strings.TrimSpace(
			strings.TrimPrefix(trimmed, "/audit"),
		)
		limit := 10
		if n, err := fmt.Sscanf(limitStr, "%d", &limit); err != nil || n != 1 {
			limit = 10
		}
		m.messages = append(m.messages, chatEntry{
			Role:    "system",
			Content: m.renderAuditPanel(limit),
		})

	case trimmed == "/theme":
		m.messages = append(m.messages, chatEntry{
			Role: "system",
			Content: fmt.Sprintf(
				"Theme: %s\nAvailable: dark (default), light, classic\nUsage: /theme [name]",
				GetTheme().String(),
			),
		})

	case strings.HasPrefix(trimmed, "/theme "):
		themeName := strings.TrimSpace(
			strings.TrimPrefix(trimmed, "/theme"),
		)
		newTheme := ParseTheme(themeName)
		SetTheme(newTheme)
		m.cachedMessages = ""
		m.cachedTools = ""
		m.cachedMsgCount = 0
		m.cachedToolCount = 0
		m.cachedToolStatus = nil
		m.cachedToolCollapsed = nil
		m.cachedToolSelected = -1
		m.lastRenderTime = time.Time{}
		m.messages = append(m.messages, chatEntry{
			Role: "system",
			Content: fmt.Sprintf(
				"Theme changed: %s",
				newTheme.String(),
			),
		})

	case strings.HasPrefix(trimmed, "/help"):
		m.messages = append(m.messages, chatEntry{
			Role: "system",
			Content: `Wukong Commands:
  /new        Start a new session
  /clear      Clear screen
  /help       Show this help
  /exts       List extensions
  /model      Show or switch model (usage: /model [name])
  /theme      Show or switch theme (usage: /theme [dark|light|classic])
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
		"/theme      - Show or switch theme",
		"/audit      - Show tool audit log",
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
	version string,
) error {
	util.SetQuietMode()

	if version == "" {
		version = util.Version
	}

	m := NewModel(ModelConfig{
		Config:     cfg,
		Loop:       loop,
		UserID:     userID,
		SessionID:  sessionID,
		WorkingDir: workingDir,
		ProjectMgr: projectMgr,
		Version:    version,
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
