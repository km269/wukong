// Package tui provides the terminal user interface for wukong.
// It implements a Bubbletea-based TUI with three-zone layout:
// conversation area, tool call status area, and input area.
package tui

import (
	"fmt"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"
	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"
	"strings"
	"sync"
	"time"
)

// maxMessages limits the chat history retained in memory.
// Beyond this limit, the oldest messages are dropped to prevent
// unbounded growth during long sessions.
const maxMessages = 500

// streamDebounce is how long updateViewport waits before pushing a
// stream-delta-only content change to the viewport. The viewport's
// internal SetContent is comparatively expensive, so batching deltas
// here avoids thrashing the renderer during high-churn streaming.
// streamDebounce is how long updateViewport waits before pushing a
// stream-delta-only content change to the viewport. The viewport's
// internal SetContent is comparatively expensive, so batching deltas
// here avoids thrashing the renderer during high-churn streaming.
const streamDebounce = 50 * time.Millisecond

// maxCommandHistory limits the number of commands saved in history.
// maxCommandHistory limits the number of commands saved in history.
const maxCommandHistory = 100

// chatEntry represents a single message in the conversation.
// chatEntry represents a single message in the conversation.
type chatEntry struct {
	Role    string
	Content string
}

// toolAuditEntry records a tool invocation for the audit panel.
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
// cleanup stops any running streams.
// Safe to call multiple times (protected by sync.Once).
//
// Note: loop.Close() is NOT called here — session.go's
// shutdownBootstrap handles that with an independent context.
func (m *Model) cleanup() {
	m.stopStream()
}

// Update implements tea.Model.
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
// generateSessionID creates a new unique session identifier.
// Uses a timestamp-based prefix for sortability followed by random
// bytes for uniqueness.
func generateSessionID() string {
	return uuid.New().String()
}
