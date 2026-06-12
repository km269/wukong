// Package topofmind provides persistent instruction injection
// into every conversation round. Similar to Goose's Top of Mind,
// it reads persistent instructions from a file and injects them
// into the agent's working memory.
package topofmind

import (
	"os"
	"strings"
	"sync"

	"github.com/km269/wukong/internal/config"
)

// Manager handles persistent instruction injection.
type Manager struct {
	mu           sync.RWMutex
	cfg          *config.TopOfMindConfig
	instructions string
	lastModTime  int64
}

// NewManager creates a new Top of Mind manager.
func NewManager(cfg *config.TopOfMindConfig) *Manager {
	return &Manager{
		cfg: cfg,
	}
}

// GetInstructions returns the current persistent instructions.
// Automatically reloads from file if changed.
//
// Uses double-check locking to avoid holding the write lock when
// the file has not changed, while preventing race conditions
// between the modification check and reload.
func (m *Manager) GetInstructions() string {
	m.mu.RLock()
	cfgFile := m.cfg.InstructionFile
	if cfgFile == "" {
		m.mu.RUnlock()
		return ""
	}

	// Fast path: check modification time under read lock.
	info, err := os.Stat(cfgFile)
	if err != nil {
		instructions := m.instructions
		m.mu.RUnlock()
		return instructions // Return cached version
	}

	if info.ModTime().Unix() <= m.lastModTime {
		// File unchanged — return cached instructions.
		instructions := m.instructions
		m.mu.RUnlock()
		return instructions
	}

	// File modified — upgrade to write lock.
	// Use double-check pattern: release read lock, acquire write lock,
	// then re-check modification time in case another goroutine already
	// reloaded while we were waiting for the write lock.
	m.mu.RUnlock()
	m.mu.Lock()

	// Double-check: another goroutine may have already reloaded.
	info2, err2 := os.Stat(cfgFile)
	if err2 == nil && info2.ModTime().Unix() <= m.lastModTime {
		instructions := m.instructions
		m.mu.Unlock()
		return instructions
	}

	m.reloadLocked()
	instructions := m.instructions
	m.mu.Unlock()
	return instructions
}

// SetInstructions manually sets the instructions.
func (m *Manager) SetInstructions(content string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cfg.MaxLength > 0 && len(content) > m.cfg.MaxLength {
		content = content[:m.cfg.MaxLength]
	}
	m.instructions = content
}

// AppendInstructions appends to existing instructions.
func (m *Manager) AppendInstructions(content string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	current := m.instructions
	if current != "" {
		current += "\n"
	}
	current += content

	if m.cfg.MaxLength > 0 && len(current) > m.cfg.MaxLength {
		current = current[:m.cfg.MaxLength]
	}
	m.instructions = current
}

// ClearInstructions removes all persistent instructions.
func (m *Manager) ClearInstructions() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.instructions = ""
}

// FormatForPrompt formats instructions for injection into the system prompt.
func (m *Manager) FormatForPrompt() string {
	instructions := m.GetInstructions()
	if instructions == "" {
		return ""
	}

	return "[Persistent Instructions - Always Follow]\n" +
		strings.TrimSpace(instructions) +
		"\n[/Persistent Instructions]"
}

func (m *Manager) reloadLocked() {
	data, err := os.ReadFile(m.cfg.InstructionFile)
	if err != nil {
		return
	}

	content := string(data)
	if m.cfg.MaxLength > 0 && len(content) > m.cfg.MaxLength {
		content = content[:m.cfg.MaxLength]
	}
	m.instructions = content

	info, _ := os.Stat(m.cfg.InstructionFile)
	if info != nil {
		m.lastModTime = info.ModTime().Unix()
	}
}
