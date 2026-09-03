// Code split out of model.go (P2-8) - same package, zero behavior change.
package tui

import (
	"context"
	"fmt"
	"github.com/km269/wukong/internal/project"
	"strings"
	"time"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

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
// projectLister is the subset of project.Manager used by the TUI.
// The field holds `any` to keep ModelConfiguration decoupled; the
// runtime value is *project.Manager from the CLI layer.
type projectLister interface {
	ListProjects() []project.ProjectRecord
}

// sessionLister is the subset of session.Service used by the TUI for
// the multi-session tab (C1). The field holds `any` to keep the TUI
// decoupled from the concrete wksession.SessionService.
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
