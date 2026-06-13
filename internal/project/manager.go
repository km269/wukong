// Package project provides automatic working directory tracking and
// session recovery for the wukong AI agent.
//
// Every time wukong session starts, the current working directory is
// recorded alongside the session ID and timestamp. Users can later
// use "wukong project" to list and recover previous sessions.
package project

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"
)

// maxProjects limits the number of tracked projects to prevent
// unbounded growth of projects.json on disk.
const maxProjects = 50

// ProjectRecord represents a single tracked working directory entry.
type ProjectRecord struct {
	Path            string `json:"path"`
	SessionID       string `json:"session_id"`
	LastAccessed    string `json:"last_accessed"`
	LastInstruction string `json:"last_instruction,omitempty"`
}

// Manager handles project tracking persistence and queries.
// It is safe for concurrent access via sync.RWMutex.
type Manager struct {
	mu       sync.RWMutex
	dir      string
	records  []ProjectRecord
}

// NewManager creates a project manager backed by the given directory.
// The directory is created if it does not exist.
// records are loaded from the existing projects.json on disk, if any.
func NewManager(cfg *config.WukongConfig) (*Manager, error) {
	projectDir := cfg.ProjectDir
	if projectDir == "" {
		projectDir = "~/.config/wukong/"
	}

	// Expand ~ to home directory.
	if len(projectDir) >= 2 && projectDir[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home dir: %w", err)
		}
		projectDir = filepath.Join(home, projectDir[2:])
	}

	projectDir = config.ResolvePath(projectDir)
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		return nil, fmt.Errorf("create project dir %s: %w",
			projectDir, err)
	}

	m := &Manager{dir: projectDir}
	if err := m.load(); err != nil {
		// Non-fatal: start with empty records.
		util.Logger.Warn("project: failed to load projects.json, "+
			"starting fresh", slog.String("error", err.Error()))
	}

	return m, nil
}

// filePath returns the full path to projects.json.
func (m *Manager) filePath() string {
	return filepath.Join(m.dir, "projects.json")
}

// load reads the projects.json file from disk.
func (m *Manager) load() error {
	fp := m.filePath()
	data, err := os.ReadFile(fp)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // fresh start, no file yet
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}

	var records []ProjectRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return err
	}

	m.records = records
	return nil
}

// save atomically writes the current records list to projects.json.
// Uses write-temp+rename to avoid corruption on partial writes.
func (m *Manager) save() error {
	fp := m.filePath()

	// Sort by LastAccessed descending so the file is already ordered.
	sort.Slice(m.records, func(i, j int) bool {
		return m.records[i].LastAccessed > m.records[j].LastAccessed
	})

	data, err := json.MarshalIndent(m.records, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal projects: %w", err)
	}

	tmpPath := fp + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}

	if err := os.Rename(tmpPath, fp); err != nil {
		os.Remove(tmpPath) // cleanup
		return fmt.Errorf("rename temp file: %w", err)
	}

	return nil
}

// TrackProject records or updates a working directory entry.
// If the directory is already tracked, the session and access time
// are updated. If dbPath is empty, "default" is recorded.
func (m *Manager) TrackProject(
	workingDir string, sessionID string, instruction string,
) {
	if workingDir == "" {
		return
	}

	workingDir = config.ResolvePath(workingDir)
	timestamp := time.Now().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Find existing entry or create new one.
	found := false
	for i := range m.records {
		if m.records[i].Path == workingDir {
			m.records[i].SessionID = sessionID
			m.records[i].LastAccessed = timestamp
			if instruction != "" {
				m.records[i].LastInstruction = instruction
			}
			// Move to front.
			rec := m.records[i]
			copy(m.records[1:i+1], m.records[0:i])
			m.records[0] = rec
			found = true
			break
		}
	}

	if !found {
		m.records = append(
			[]ProjectRecord{{
				Path:            workingDir,
				SessionID:       sessionID,
				LastAccessed:    timestamp,
				LastInstruction: instruction,
			}},
			m.records...,
		)
	}

	// Enforce maxProjects limit.
	if len(m.records) > maxProjects {
		m.records = m.records[:maxProjects]
	}

	if err := m.save(); err != nil {
		util.Logger.Warn("project: failed to save projects.json",
			slog.String("error", err.Error()))
	}
}

// UpdateInstruction updates the last instruction for a tracked project.
// Called when the user sends their first message in a session.
func (m *Manager) UpdateInstruction(
	workingDir string, instruction string,
) {
	if workingDir == "" || instruction == "" {
		return
	}

	workingDir = config.ResolvePath(workingDir)

	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.records {
		if m.records[i].Path == workingDir {
			// Truncate to 200 chars for display.
			inst := instruction
			if len(inst) > 200 {
				inst = inst[:197] + "..."
			}
			m.records[i].LastInstruction = inst
			break
		}
	}

	if err := m.save(); err != nil {
		util.Logger.Warn("project: failed to save instruction",
			slog.String("error", err.Error()))
	}
}

// ListProjects returns all tracked projects sorted by last accessed
// time (most recent first).
func (m *Manager) ListProjects() []ProjectRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]ProjectRecord, len(m.records))
	copy(result, m.records)
	return result
}

// FindByPath looks up a project record by its working directory path.
// Returns nil if not found.
func (m *Manager) FindByPath(workingDir string) *ProjectRecord {
	workingDir = config.ResolvePath(workingDir)

	m.mu.RLock()
	defer m.mu.RUnlock()

	for i := range m.records {
		if m.records[i].Path == workingDir {
			rec := m.records[i]
			return &rec
		}
	}
	return nil
}

// Clear removes all project records.
func (m *Manager) Clear() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.records = nil
	return m.save()
}
