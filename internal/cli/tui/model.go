// Package tui provides the terminal user interface for wukong.
// It implements a Bubbletea-based TUI with three-zone layout:
// conversation area, tool call status area, and input area.
package tui

import (
	"context"
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
	"trpc.group/trpc-go/trpc-agent-go/session"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/project"
	"github.com/km269/wukong/internal/util"
)

// maxMessages limits the chat history retained in memory.
// Beyond this limit, the oldest messages are dropped to prevent
// unbounded growth during long sessions.
const maxMessages = 500

// streamDebounce is how long updateViewport waits before pushing a
// stream-delta-only content change to the viewport. The viewport's
// internal SetContent is comparatively expensive, so batching deltas
// here avoids thrashing the renderer during high-churn streaming.
const streamDebounce = 50 * time.Millisecond

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
	ModalProjects
	ModalSessions
)

// modalState represents the state of a modal window.
type modalState struct {
	Type     ModalType
	Title    string
	Content  string
	Selected int
	Items    []string
	Scroll   int // viewport offset for content taller than the modal
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
	loop loopRunner
	cfg  *config.WukongConfig

	// Secret values to redact from rendered output (from cfg via
	// SecretValues(); nil-safe, stays nil when cfg is absent).
	secrets []string

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
	projectMgr    any // *project.Manager (set by CLI; asserted via small interfaces)
	instrRecorded bool

	// Session management (multi-session tab, C1)
	sessionMgr        any                 // *wksession.SessionService (set by CLI; asserted via small interfaces)
	sessionModalItems []sessionsModalItem // full IDs for the open sessions modal (reset on open)

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
	toolScrollStart int // first visible tool in the scroll window

	// Viewport scroll control: when false, auto-scroll is disabled
	// (user has manually scrolled up to read history)
	autoScroll bool

	// When true, user has pressed Ctrl+C once to cancel streaming.
	// Second Ctrl+C will trigger exit.
	cancelled bool

	// Debounce timer for SetContent to reduce thrashing during streaming
	lastRenderTime time.Time

	// Incremental streaming-render cache (P3.1). While a response is
	// streaming, streamCachePrefixIdx holds the byte offset of the last
	// provably-safe split point (a blank line after a fully-closed
	// markdown block), streamCacheRendered is the rendered output of
	// content[:streamCachePrefixIdx], and streamCacheValid says whether
	// the cache still matches currentStream. Each frame renders only the
	// growing tail after that prefix instead of the whole document.
	// Invalidated whenever currentStream changes (see
	// invalidateStreamCache/resetStreamCache).
	// streamDeltas counts render frames since the last baseline; a
	// periodic full reconcile (markdownStreamReconcileEvery) bounds any
	// block-boundary approximation.
	streamCachePrefixIdx int
	streamCacheRendered  string
	streamCacheValid     bool
	streamDeltas         int
}

// ModelConfig holds dependencies for creating the TUI model.
type ModelConfig struct {
	Config     *config.WukongConfig
	Loop       loopRunner
	UserID     string
	SessionID  string
	WorkingDir string
	ProjectMgr any // *project.Manager (set by CLI; asserted via small interfaces)
	SessionMgr any // *wksession.SessionService (set by CLI; asserted via small interfaces)
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
		secrets:      cfg.Config.SecretValues(),
		modelName:    modelDisplay,
		providerName: providerDisplay,
		toolCount:    toolCount,
		skillName:    "-",
		version:      cfg.Version,
		workingDir:   cfg.WorkingDir,
		projectMgr:   cfg.ProjectMgr,
		sessionMgr:   cfg.SessionMgr,
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
	m.stopStream()

	m.quitRequested = true
	m.status = "Goodbye!"
	return tea.Quit
}

// stopStream cancels any in-flight streaming request and waits for the
// streaming goroutine to finish. Safe to call multiple times (cancel
// and wait are each guarded by sync.Once). Does NOT set quitRequested.
func (m *Model) stopStream() {
	m.cleanupOnce.Do(func() {
		if m.streaming && m.streamCancel != nil {
			m.streamCancel()
		}
		if m.streamDone != nil {
			select {
			case <-m.streamDone:
				// Stream goroutine finished
			case <-time.After(3 * time.Second):
				// Timeout - proceed with exit anyway
			}
		}
	})
}

// cleanup stops any running streams.
// Safe to call multiple times (protected by sync.Once).
//
// Note: loop.Close() is NOT called here — session.go's
// shutdownBootstrap handles that with an independent context.
func (m *Model) cleanup() {
	m.stopStream()
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
					m.followModalSelection()
				}
				return m, nil
			case tea.KeyDown:
				if m.modal.Selected < len(m.modal.Items)-1 {
					m.modal.Selected++
					m.followModalSelection()
				}
				return m, nil
			case tea.KeyPgUp:
				m.modal.Scroll--
				if m.modal.Scroll < 0 {
					m.modal.Scroll = 0
				}
				return m, nil
			case tea.KeyPgDown:
				m.modal.Scroll++
				return m, nil
			case tea.KeyBackspace:
				// C1: in the sessions tab, Backspace deletes the
				// selected session (server-side) and refreshes.
				if m.modal.Type == ModalSessions {
					m.deleteSelectedSession()
				}
				return m, nil
			}
			return m, nil
		}

		switch msg.Type {
		case tea.KeyCtrlC:
			if m.streaming {
				// Second Ctrl+C while already cancelling force-quits,
				// matching the "Press Ctrl+C again to force-quit" hint.
				if m.cancelled {
					return m, m.requestExit()
				}
				if m.streamCancel != nil {
					m.streamCancel()
				}
				m.cancelled = true
				m.status = "Cancelling..."
				m.messages = append(m.messages,
					chatEntry{Role: "system",
						Content: "[Request cancelled by user]\nWaiting for stream to finish...\nPress Ctrl+C again to force-quit, or type /exit"})
				m.updateViewport()
				// Keep consuming streamCh — the stream goroutine
				// will send streamEndMsg once it observes the
				// cancellation. Without this cmd the channel is
				// never drained and the UI deadlocks on
				// "Cancelling...".
				return m, readStreamEvent(m.streamCh)
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
			// Tab/Shift+Tab navigate the tool list; Enter toggles
			// collapse/expand of the selected tool result (with a
			// result). This matches the footer hint rendered in
			// updateViewport.
			if len(m.toolCalls) > 0 {
				if m.toolSelectedIdx+1 < len(m.toolCalls) {
					m.toolSelectedIdx++
				} else {
					m.toolSelectedIdx = 0
				}
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

		case tea.KeyEnter:
			if m.modal != nil {
				return m, nil
			}
			// Enter toggles collapse/expand of the selected tool.
			// Only tool calls with a finished result are expandable;
			// running/error calls have nothing to reveal.
			if len(m.toolCalls) > 0 &&
				m.toolSelectedIdx >= 0 &&
				m.toolSelectedIdx < len(m.toolCalls) {
				tc := &m.toolCalls[m.toolSelectedIdx]
				if tc.Result != "" {
					tc.Collapsed = !tc.Collapsed
					m.updateViewport()
					return m, nil
				}
			}
			// Not a tool-toggle: fall through to submit the input.

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
			// Default to collapsed: show only the header while the
			// call runs; the result is revealed on Enter when done.
			Collapsed: true,
		})
		m.setStatus("Running: " + msg.Name)
		m.updateViewport()
		return m, readStreamEvent(m.streamCh)

	case toolCallResultMsg:
		// Match the FIRST running entry with this name (oldest
		// call), mirroring the FIFO attribution in update.go's
		// resultQueue: results are attached to tool calls in the
		// order they started.
		for i := 0; i < len(m.toolCalls); i++ {
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
		m.resetStreamCache()
		m.setStatus("Error occurred")
		m.updateViewport()
		return m, readStreamEvent(m.streamCh)

	case streamEndMsg:
		if !m.streaming && !m.cancelled {
			return m, nil
		}
		m.streaming = false
		m.streamCancel = nil
		var finalContent string
		if m.currentStream != "" {
			finalContent = m.currentStream
		} else if msg.Content != "" {
			finalContent = msg.Content
		}
		m.currentStream = ""
		m.resetStreamCache()
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
		m.cancelled = false
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
		// Render only the visible window of tools. The window
		// follows the selected tool and is capped at a quarter of
		// the viewport height so a long tool list never pushes
		// the input area off screen.
		maxVisible := m.viewport.Height / 4
		if maxVisible < 2 {
			maxVisible = 2
		}
		if m.toolSelectedIdx < m.toolScrollStart {
			m.toolScrollStart = m.toolSelectedIdx
		}
		if m.toolSelectedIdx >= m.toolScrollStart+maxVisible {
			m.toolScrollStart = m.toolSelectedIdx - maxVisible + 1
		}
		if m.toolScrollStart < 0 {
			m.toolScrollStart = 0
		}
		start := m.toolScrollStart
		end := start + maxVisible
		if end > len(m.toolCalls) {
			end = len(m.toolCalls)
		}
		for i := start; i < end; i++ {
			tc := m.toolCalls[i]
			selected := i == m.toolSelectedIdx
			buf.WriteString(RenderToolCallResult(tc, selected, m.width) + "\n\n")
		}
		if len(m.toolCalls) > maxVisible {
			hint := "Tab/Shift+Tab to navigate"
			if m.toolSelectedIdx >= 0 &&
				m.toolSelectedIdx < len(m.toolCalls) &&
				m.toolCalls[m.toolSelectedIdx].Result != "" {
				hint = "Tab/Shift+Tab to navigate • Enter to expand/collapse"
			}
			buf.WriteString(dimStyle.Render(fmt.Sprintf(
				"  [%d-%d/%d tools — %s]",
				start+1, end, len(m.toolCalls), hint,
			)) + "\n")
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
		content.WriteString(m.renderStreamedMarkdown(m.currentStream) + "\n")
	}

	// Force render when structure changes (messages/tools changed)
	// Only debounce trivial stream-delta-only updates.
	structuralChange := msgChanged || toolChanged

	now := time.Now()
	if !structuralChange && now.Sub(m.lastRenderTime) < streamDebounce {
		return
	}
	m.lastRenderTime = now

	m.viewport.SetContent(content.String())

	if m.autoScroll {
		m.viewport.GotoBottom()
	}
}

func (m *Model) renderAssistantMessage(content string) string {
	content = util.RedactSecrets(content, m.secrets)
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

// markdownStreamFlushThreshold: content below this size is rendered in
// full every frame — the incremental machinery is not worth the book-
// keeping for small messages (the render itself is fast).
const markdownStreamFlushThreshold = 2 * 1024

// markdownStreamReconcileEvery forces a full re-render every N deltas
// so any subtle block-boundary approximation self-corrects within a
// few frames.
const markdownStreamReconcileEvery = 8

// renderStreamedMarkdown renders assistant content during streaming.
// It caches the rendered output of the stable document prefix (everything
// up to the last blank-line boundary, at which point the markdown blocks
// are independent) and only re-renders the still-growing final block.
//
// Safety: incremental reuse is ONLY attempted when the split point is
// provably independent — an even number of code fences above the split
// AND an even number inside the tail (no open block). Any other case
// falls back to a full render, which is always correct. A periodic full
// reconciliation further bounds any approximation.
func (m *Model) renderStreamedMarkdown(content string) string {
	if m.mdRenderer == nil {
		m.invalidateStreamCache()
		return RenderAssistantMessage(content)
	}

	// Not the active streaming message: full render.
	if !m.streaming || m.currentStream != content {
		m.invalidateStreamCache()
		return m.renderAssistantMessage(content)
	}

	m.streamDeltas++

	// Cache was invalidated (message replaced / /new / /clear / /resume).
	if !m.streamCacheValid {
		m.streamCachePrefixIdx = 0
		m.streamCacheRendered = ""
		m.streamCacheValid = true
		return m.renderStreamedMarkdownFull(content)
	}

	if len(content) < markdownStreamFlushThreshold ||
		m.streamCachePrefixIdx == 0 ||
		m.streamDeltas%markdownStreamReconcileEvery == 0 {
		return m.renderStreamedMarkdownFull(content)
	}

	// Fast path: only the final block changed since last frame. Verify
	// the prefix is still valid, then render just the tail.
	prefixEnd := m.streamCachePrefixIdx
	if prefixEnd > len(content) ||
		!strings.HasPrefix(content, content[:prefixEnd]) {
		// Content was replaced: reset and render in full.
		m.invalidateStreamCache()
		return m.renderAssistantMessage(content)
	}

	tail := content[prefixEnd:]
	if !safeIncrementalTail(tail) {
		// Tail has an open code block or is empty: full render this
		// frame (incremental reuse will resume at the next safe split).
		m.invalidateStreamCache()
		return m.renderAssistantMessage(content)
	}

	rendered := m.streamCacheRendered + "\n\n" + m.renderMarkdownBody(tail)
	if rendered == "" {
		m.invalidateStreamCache()
		return m.renderAssistantMessage(content)
	}
	return assistantStyle.Render("Wukong: ") + rendered
}

// renderMarkdownBody renders a content fragment with the markdown
// renderer and returns the body without the "Wukong: " prefix or
// trailing newlines. Returns "" when the render fails.
func (m *Model) renderMarkdownBody(content string) string {
	if m.mdRenderer == nil {
		return ""
	}
	md, err := m.mdRenderer.Render(content)
	if err != nil || md == "" {
		return ""
	}
	return strings.TrimRight(md, "\n")
}

// safeIncrementalTail reports whether the trailing block can be rendered
// standalone and concatenated with the cached prefix without changing
// markdown semantics. Since the split is at a blank line, blocks are
// independent; the only risk is an odd number of fences (an open code
// block) whose tail would render differently in isolation.
func safeIncrementalTail(tail string) bool {
	if tail == "" {
		return false
	}
	return fenceCount(tail)%2 == 0
}

func fenceCount(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") ||
			strings.HasPrefix(strings.TrimSpace(line), "~~~") {
			n++
		}
	}
	return n
}

// renderStreamedMarkdownFull renders the whole document and re-baselines
// the incremental cache at the last safe split point (if any).
func (m *Model) renderStreamedMarkdownFull(content string) string {
	full := m.renderAssistantMessage(content)

	// Re-baseline: find the last safe blank-line split.
	idx := lastSafeSplit(content)
	if idx > 0 {
		m.streamCachePrefixIdx = idx
		m.streamCacheRendered = strings.TrimRight(
			m.renderMarkdownBody(content[:idx]), "\n")
	} else {
		m.streamCachePrefixIdx = 0
		m.streamCacheRendered = ""
	}
	m.streamCacheValid = true
	return full
}

// lastSafeSplit returns the byte offset just after the last blank line
// whose prefix and suffix both have an even fence count. A split with an
// odd fence count anywhere would break rendering, so only provably safe
// boundaries are returned (0 when none exist).
func lastSafeSplit(content string) int {
	lines := strings.Split(content, "\n")
	// start[i] = byte offset where line i begins.
	start := make([]int, len(lines))
	pos := 0
	for i, ln := range lines {
		start[i] = pos
		pos += len(ln) + 1
	}
	// Walk blank lines from the end; the split point is the byte just
	// after the blank line (the start of the next line). Only accept a
	// split whose prefix and tail both have an even fence count.
	for i := len(lines) - 2; i >= 0; i-- {
		if lines[i] != "" {
			continue
		}
		split := start[i+1]
		tail := content[split:]
		if tail == "" {
			continue
		}
		if fenceCount(tail)%2 != 0 {
			continue
		}
		if fenceCount(content[:split])%2 != 0 {
			continue
		}
		return split
	}
	return 0
}

// invalidateStreamCache forces the next render to re-baseline.
func (m *Model) invalidateStreamCache() {
	m.streamCachePrefixIdx = 0
	m.streamCacheRendered = ""
	m.streamCacheValid = false
}

// resetStreamCache invalidates the incremental streaming-render cache.
// Called whenever currentStream is replaced or cleared.
func (m *Model) resetStreamCache() {
	m.invalidateStreamCache()
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

	case trimmed == "/projects":
		m.showProjects()

	case trimmed == "/sessions":
		m.openSessionsModal()

	case trimmed == "/resume":
		m.messages = append(m.messages, chatEntry{
			Role: "system",
			Content: "Usage: /resume <session-id> to resume a saved " +
				"session. Session context (conversation, memory " +
				"recall) is restored from the server-side store.",
		})

	case strings.HasPrefix(trimmed, "/resume "):
		sid := strings.TrimSpace(strings.TrimPrefix(trimmed, "/resume "))
		if sid == "" {
			break
		}
		m.sessionID = sid
		m.messages = nil
		m.toolCalls = nil
		m.auditLog = nil
		m.currentStream = ""
		m.streaming = false
		m.instrRecorded = false
		m.resetStreamCache()
		m.autoScroll = true
		m.cachedMessages = ""
		m.cachedTools = ""
		m.cachedMsgCount = 0
		m.cachedToolCount = 0
		m.cachedToolStatus = nil
		m.cachedToolCollapsed = nil
		m.cachedToolSelected = -1
		m.viewport.SetContent("")
		m.status = "Resumed session " + sid[:min(len(sid), 12)]
		m.messages = append(m.messages, chatEntry{
			Role: "system",
			Content: "[Session resumed: " + sid + "]\nThe agent's " +
				"server-side session store restores context on the " +
				"next message.",
		})

	case trimmed == "/new":
		m.sessionID = generateSessionID()
		m.messages = nil
		m.toolCalls = nil
		m.auditLog = nil
		m.currentStream = ""
		m.instrRecorded = false
		m.resetStreamCache()
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
		m.resetStreamCache()
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
		m.resetStreamCache()
		m.messages = append(m.messages, chatEntry{
			Role: "system",
			Content: fmt.Sprintf(
				"Theme changed: %s",
				newTheme.String(),
			),
		})

	case strings.HasPrefix(trimmed, "/help"):
		help := `Wukong Commands:
  /new        Start a new session
  /clear      Clear screen
  /help       Show this help
  /exts       List extensions
  /model      Show or switch model (usage: /model [name])
  /theme      Show or switch theme (usage: /theme [dark|light|classic])
  /commands   Open command menu
  /skills     Open skills browser
  /settings   Open settings panel
  /projects   Recover a tracked project session
  /sessions   List and manage sessions
  /exit       Quit wukong
  Ctrl+D      Send message
  Ctrl+C      Quit

Built-in Extensions:
` + m.builtinExtensionHelp() + `
Platform Extensions:
  todo_*               Task management & tracking
  recall_*             Cross-session history search
  tom_*                Persistent instruction injection
  code_*               JavaScript code execution
  app_*                Custom HTML app management`

		m.messages = append(m.messages, chatEntry{
			Role:    "system",
			Content: help,
		})

	default:
		m.messages = append(m.messages, chatEntry{
			Role: "system",
			Content: "Unknown command: " + trimmed +
				". Type /help for available commands.",
		})
	}
}

// builtinExtensionHelp builds the "Built-in Extensions" section of the
// /help output from the ACTUAL enabled extensions in config, so newly
// added extensions appear automatically instead of being hardcoded.
func (m *Model) builtinExtensionHelp() string {
	if m.cfg == nil {
		return "  (no extensions loaded)"
	}
	var lines []string
	for _, ext := range m.cfg.Extensions {
		if !ext.Enabled {
			continue
		}
		desc := extensionDescriptions[ext.Name]
		if desc == "" {
			desc = "type: " + ext.Type
		}
		line := fmt.Sprintf("  %-22s %s", ext.Name, desc)
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "  (no extensions loaded)"
	}
	return strings.Join(lines, "\n")
}

// extensionDescriptions maps known builtin extension names to a short
// description shown in /help. Unknown names fall back to their type.
var extensionDescriptions = map[string]string{
	"developer":           "File ops, commands, code search",
	"computer_controller": "Web fetch, file cache",
	"memory":              "Remember preferences & knowledge",
	"auto_visualiser":     "Charts, diagrams, tables",
	"tutorial":            "Interactive tutorials",
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
		"/projects   - Recover a tracked project session",
		"/sessions   - List and manage sessions",
		"/help       - Show help",
		"/exit       - Quit wukong",
	}
	m.modal = &modalState{
		Type:     ModalCommands,
		Title:    "Available Commands",
		Selected: 0,
		Items:    commands,
	}
	m.layoutModal(len(commands))
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
	m.layoutModal(len(skills))
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
	m.layoutModal(strings.Count(content, "\n") + 1)
}

// layoutModal computes adaptive modal dimensions that fit the screen.
// Height is derived from content but capped so tall content becomes
// scrollable instead of overflowing; width adapts to the terminal.
func (m *Model) layoutModal(contentLines int) {
	// Title line + blank separator + rows + bottom padding.
	needed := contentLines + 3
	maxH := m.height - 8
	if maxH < 6 {
		maxH = 6
	}
	m.modalHeight = needed
	if m.modalHeight > maxH {
		m.modalHeight = maxH
	}

	w := m.width - 8
	if w > 60 {
		w = 60
	}
	if w < 40 {
		w = 40
	}
	m.modalWidth = w
}

// followModalSelection keeps the modal's scroll offset aligned with the
// selected item after an up/down movement.
func (m *Model) followModalSelection() {
	if m.modal == nil {
		return
	}
	maxVisible := m.modalHeight - 3
	if maxVisible < 1 {
		maxVisible = 1
	}
	if m.modal.Selected < m.modal.Scroll {
		m.modal.Scroll = m.modal.Selected
	}
	if m.modal.Selected >= m.modal.Scroll+maxVisible {
		m.modal.Scroll = m.modal.Selected - maxVisible + 1
	}
}

// projectLister is the subset of project.Manager used by the TUI.
// The field holds `any` to keep ModelConfiguration decoupled; the
// runtime value is *project.Manager from the CLI layer.
type projectLister interface {
	ListProjects() []project.ProjectRecord
}

// sessionLister is the subset of session.Service used by the TUI for
// the multi-session tab (C1). The field holds `any` to keep the TUI
// decoupled from the concrete wksession.SessionService.
type sessionLister interface {
	ListSessions(ctx context.Context, userKey session.UserKey) ([]*session.Session, error)
	DeleteSession(ctx context.Context, key session.Key) error
}

// sessionsModalItem describes one entry in the sessions modal. The
// first item is the "new session" action; the last is "back". Session
// rows also carry their full ID so deletion/selection doesn't rely on
// the truncated display string.
type sessionsModalItem struct {
	Label     string
	SessionID string // "" for action rows
}

func (m *Model) sessionsModalItems() ([]sessionsModalItem, string) {
	svc, ok := m.sessionMgr.(sessionLister)
	if !ok || svc == nil {
		return nil, "Session management is not available in this session."
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sessions, err := svc.ListSessions(ctx, session.UserKey{
		AppName: "wukong-app",
		UserID:  m.userID,
	})
	if err != nil {
		return nil, "Failed to list sessions: " + err.Error()
	}

	items := make([]sessionsModalItem, 0, len(sessions)+1)
	items = append(items, sessionsModalItem{Label: "+ New session"})
	for _, s := range sessions {
		items = append(items, sessionsModalItem{
			Label:     fmt.Sprintf("%s  %s", s.ID, formatSessionTime(s.UpdatedAt)),
			SessionID: s.ID,
		})
	}
	items = append(items, sessionsModalItem{Label: "— Back"})
	return items, ""
}

// openSessionsModal opens the multi-session tab (C1): New + existing
// sessions + delete action, so switching is a single Enter.
func (m *Model) openSessionsModal() {
	items, errMsg := m.sessionsModalItems()
	if errMsg != "" {
		m.messages = append(m.messages, chatEntry{
			Role:    "system",
			Content: errMsg,
		})
		return
	}
	labels := make([]string, len(items))
	for i, it := range items {
		labels[i] = it.Label
	}
	m.modal = &modalState{
		Type:     ModalSessions,
		Title:    "Sessions",
		Selected: 0,
		Items:    labels,
	}
	// Stash the full session IDs on the model; the Items slice only
	// holds display text.
	m.sessionModalItems = items
	m.layoutModal(len(labels))
}

// deleteSelectedSession deletes the session under the cursor in the
// sessions tab (C1). The modal is refreshed afterwards; the active
// session is left untouched.
func (m *Model) deleteSelectedSession() {
	if m.modal == nil {
		return
	}
	sel := m.modal.Selected
	if m.modal.Selected < 0 || m.modal.Selected >= len(m.sessionModalItems) {
		return
	}
	item := m.sessionModalItems[sel]
	if item.SessionID == "" {
		return // action row (New / Back)
	}

	svc, ok := m.sessionMgr.(sessionLister)
	if !ok || svc == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := svc.DeleteSession(ctx, session.Key{
		AppName:   "wukong-app",
		UserID:    m.userID,
		SessionID: item.SessionID,
	}); err != nil {
		m.status = "Delete failed: " + err.Error()
		// Refresh anyway (row may already be gone).
	}
	// Refresh the modal list after deletion.
	m.removeSessionFromModal(sel)
}

// removeSessionFromModal drops the deleted row and, if the modal became
// empty (no sessions left), closes it with a notice.
func (m *Model) removeSessionFromModal(deletedSel int) {
	if m.modal == nil || m.modal.Type != ModalSessions {
		return
	}
	if deletedSel >= 0 && deletedSel < len(m.sessionModalItems) {
		m.sessionModalItems = append(
			m.sessionModalItems[:deletedSel],
			m.sessionModalItems[deletedSel+1:]...,
		)
	}
	// After removing a session row the "+ New session" header stays and
	// "— Back" stays; if nothing but the actions remain, close the modal.
	if len(m.sessionModalItems) <= 2 {
		m.modal = nil
		m.sessionModalItems = nil
		m.status = "No stored sessions"
		return
	}
	// Rebuild display items.
	labels := make([]string, len(m.sessionModalItems))
	for i, it := range m.sessionModalItems {
		labels[i] = it.Label
	}
	m.modal.Items = labels
	if m.modal.Selected >= len(labels) {
		m.modal.Selected = len(labels) - 1
	}
	m.followModalSelection()
}

// formatSessionTime renders a session timestamp compactly.
func formatSessionTime(t time.Time) string {
	if t.IsZero() {
		return "unknown time"
	}
	return t.Format("01-02 15:04")
}

// openProjectsModal shows tracked projects as a selectable modal.
// Selecting an entry resumes that project's session (via /resume),
// replacing the old plain-text listing.
func (m *Model) openProjectsModal() {
	mgr, ok := m.projectMgr.(projectLister)
	if !ok || mgr == nil {
		m.messages = append(m.messages, chatEntry{
			Role:    "system",
			Content: "Project tracking is not available in this session.",
		})
		return
	}
	records := mgr.ListProjects()
	items := make([]string, 0, len(records))
	for _, r := range records {
		sID := r.SessionID
		if len(sID) > 8 {
			sID = sID[:8]
		}
		inst := r.LastInstruction
		if len(inst) > 24 {
			inst = inst[:21] + "..."
		}
		items = append(items, fmt.Sprintf("%-28s %s  %s", r.Path, sID, inst))
	}
	if len(items) == 0 {
		m.messages = append(m.messages, chatEntry{
			Role: "system",
			Content: "No tracked projects found. Start a " +
				"'wukong session' in any directory to begin " +
				"tracking.",
		})
		return
	}
	m.modal = &modalState{
		Type:     ModalProjects,
		Title:    "Tracked Projects — Enter to resume",
		Selected: 0,
		Items:    items,
	}
	m.layoutModal(len(items))
}

// showProjects lists tracked projects; C3: now opens the selectable
// ModalProjects rather than appending a plain-text chat reply.
func (m *Model) showProjects() {
	m.openProjectsModal()
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
	case ModalSessions:
		// C1 multi-session tab: New / resume / delete / back.
		sel := m.modal.Selected
		items := m.sessionModalItems
		m.modal = nil
		m.sessionModalItems = nil
		if sel < 0 || sel >= len(items) {
			return
		}
		item := items[sel]
		switch {
		case item.Label == "+ New session":
			m.handleCommand("/new")
		case item.Label == "— Back":
			// just close
		case item.SessionID != "":
			m.handleCommand("/resume " + item.SessionID)
		}
	case ModalProjects:
		// Selecting a project resumes its tracked session. The
		// shortened session id shown in the item is only a display
		// prefix, so re-query the underlying record for the full id.
		mgr, ok := m.projectMgr.(projectLister)
		sel := m.modal.Selected
		if ok && mgr != nil && sel >= 0 && sel < len(m.modal.Items) {
			records := mgr.ListProjects()
			if sel < len(records) {
				m.modal = nil
				m.handleCommand("/resume " + records[sel].SessionID)
			}
		}
	}
}

// StartTUI initializes and runs the Bubbletea TUI.
func StartTUI(
	cfg *config.WukongConfig,
	loop loopRunner,
	userID, sessionID string,
	workingDir string,
	projectMgr any,
	sessionMgr any,
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
		SessionMgr: sessionMgr,
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
