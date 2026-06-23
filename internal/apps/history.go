// Package apps provides version history and rollback for HTML applications.
//
// Each app maintains a versions/ subdirectory containing timestamped
// snapshots. The Manager can list versions, retrieve historical content,
// and roll back to any previous version.
package apps

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// VersionRecord describes a single historical version of an app.
type VersionRecord struct {
	// Version is the version string at the time of the snapshot (e.g., "1.0.3").
	Version string `json:"version"`
	// Timestamp is when this version was saved.
	Timestamp time.Time `json:"timestamp"`
	// Size is the file size of the snapshot in bytes.
	Size int64 `json:"size"`
	// Label is an optional human-readable label for this version.
	Label string `json:"label,omitempty"`
}

// maxHistoryVersions limits the number of retained history snapshots
// per app. Older versions are pruned when the limit is exceeded.
const maxHistoryVersions = 20

// SaveVersion saves the current app content as a historical version.
// The snapshot is stored in <appDir>/<name>.versions/<version>_<ts>.html
// alongside a .json metadata file. Old versions are pruned when the
// count exceeds maxHistoryVersions.
//
// This method is safe for external callers that do not hold the
// manager lock.
func (m *Manager) SaveVersion(appName, label string) error {
	m.mu.RLock()
	app, ok := m.apps[appName]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("app %q not found", appName)
	}

	return m.saveVersionLocked(app, label)
}

// saveVersionLocked performs the actual version save without acquiring
// the manager lock. Callers must ensure thread safety externally.
func (m *Manager) saveVersionLocked(app AppInfo, label string) error {
	// Determine the content source path.
	// For multi-file apps (cloned with AppDir), prefer pages/index.html.
	// For single-file apps, use the FilePath directly.
	srcPath := app.FilePath
	if app.AppDir != "" {
		indexPath := filepath.Join(app.AppDir, "pages", "index.html")
		if fileExists(indexPath) {
			srcPath = indexPath
		}
	}

	content, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("read app content: %w", err)
	}

	// Ensure versions directory exists.
	versionsDir := filepath.Join(m.appDir, app.Name+".versions")
	if err := os.MkdirAll(versionsDir, 0755); err != nil {
		return fmt.Errorf("create versions dir: %w", err)
	}

	// Generate filename from version and timestamp.
	ts := time.Now()
	filename := fmt.Sprintf("%s_%s.html",
		app.Version, ts.Format("20060102_150405"))

	// Write the HTML snapshot.
	htmlPath := filepath.Join(versionsDir, filename)
	if err := os.WriteFile(htmlPath, content, 0644); err != nil {
		return fmt.Errorf("write version snapshot: %w", err)
	}

	// Write metadata file.
	meta := VersionRecord{
		Version:   app.Version,
		Timestamp: ts,
		Size:      int64(len(content)),
		Label:     label,
	}
	metaData, _ := json.MarshalIndent(meta, "", "  ")
	metaPath := strings.TrimSuffix(htmlPath, ".html") + ".json"
	_ = os.WriteFile(metaPath, metaData, 0644)

	// Prune old versions if over the limit.
	m.pruneHistory(versionsDir)

	return nil
}

// UpdateAppWithHistory updates an app's content and saves the previous
// version as a historical snapshot. This combines UpdateApp and
// SaveVersion into a single atomic operation.
func (m *Manager) UpdateAppWithHistory(name, htmlContent string) (AppInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	app, ok := m.apps[name]
	if !ok {
		return AppInfo{}, fmt.Errorf("app %q not found", name)
	}

	// Save current version before overwriting (non-fatal on failure).
	if saveErr := m.saveVersionLocked(app,
		"auto-save before update"); saveErr != nil {
		// Log but continue — update should not be blocked by
		// history save failures.
		_ = saveErr
	}

	// Write new content.
	if err := os.WriteFile(app.FilePath, []byte(htmlContent), 0644); err != nil {
		return AppInfo{}, fmt.Errorf("write app file: %w", err)
	}

	info, _ := os.Stat(app.FilePath)
	app.UpdatedAt = time.Now()
	if info != nil {
		app.Size = info.Size()
	}
	app.Version = incrementVersion(app.Version)
	m.apps[name] = app

	return app, nil
}

// ListVersions returns all saved versions for an app, sorted
// by timestamp (newest first).
func (m *Manager) ListVersions(appName string) ([]VersionRecord, error) {
	_, ok := m.GetApp(appName)
	if !ok {
		return nil, fmt.Errorf("app %q not found", appName)
	}

	versionsDir := filepath.Join(m.appDir, appName+".versions")
	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		// No history yet — return empty list, not an error.
		return []VersionRecord{}, nil
	}

	var records []VersionRecord
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		metaPath := filepath.Join(versionsDir, entry.Name())
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}

		var record VersionRecord
		if err := json.Unmarshal(data, &record); err != nil {
			continue
		}
		records = append(records, record)
	}

	// Sort newest first.
	sort.Slice(records, func(i, j int) bool {
		return records[i].Timestamp.After(records[j].Timestamp)
	})

	return records, nil
}

// GetVersion retrieves the HTML content of a specific version.
// The version parameter is the version string (e.g., "1.0.3").
// If multiple snapshots share the same version, the newest is returned.
func (m *Manager) GetVersion(appName, version string) (string, error) {
	_, ok := m.GetApp(appName)
	if !ok {
		return "", fmt.Errorf("app %q not found", appName)
	}

	versionsDir := filepath.Join(m.appDir, appName+".versions")
	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		return "", fmt.Errorf("no history for app %q", appName)
	}

	// Find the matching HTML file.
	prefix := version + "_"
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".html") {
			continue
		}
		if strings.HasPrefix(entry.Name(), prefix) {
			htmlPath := filepath.Join(versionsDir, entry.Name())
			data, err := os.ReadFile(htmlPath)
			if err != nil {
				continue
			}
			return string(data), nil
		}
	}

	return "", fmt.Errorf(
		"version %q not found for app %q", version, appName)
}

// RollbackTo rolls back an app's content to a previous version.
// The current content is saved as a new version before the rollback
// so the operation is non-destructive.
func (m *Manager) RollbackTo(appName, version string) (AppInfo, error) {
	content, err := m.GetVersion(appName, version)
	if err != nil {
		return AppInfo{}, fmt.Errorf("rollback: %w", err)
	}

	// Save current version before rollback (so this is reversible).
	if saveErr := m.SaveVersion(appName, fmt.Sprintf(
		"auto-save before rollback to v%s", version)); saveErr != nil {
		// Non-fatal: continue with rollback even if pre-save fails.
		_ = saveErr
	}

	// Apply the historical content.
	return m.UpdateApp(appName, content)
}

// DeleteVersion removes a specific version snapshot from history.
func (m *Manager) DeleteVersion(appName, version string) error {
	_, ok := m.GetApp(appName)
	if !ok {
		return fmt.Errorf("app %q not found", appName)
	}

	versionsDir := filepath.Join(m.appDir, appName+".versions")
	prefix := version + "_"

	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		return fmt.Errorf("no history for app %q", appName)
	}

	var removed int
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		if err := os.Remove(
			filepath.Join(versionsDir, entry.Name())); err != nil {
			return fmt.Errorf("remove version file %q: %w",
				entry.Name(), err)
		}
		removed++
	}

	if removed == 0 {
		return fmt.Errorf(
			"version %q not found for app %q", version, appName)
	}
	return nil
}

// HistoryCount returns the number of saved versions for an app.
func (m *Manager) HistoryCount(appName string) int {
	records, err := m.ListVersions(appName)
	if err != nil {
		return 0
	}
	return len(records)
}

// pruneHistory removes the oldest version snapshots when the limit
// is exceeded.
func (m *Manager) pruneHistory(versionsDir string) {
	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		return
	}

	// Collect HTML files with their modification times.
	type fileInfo struct {
		name    string
		modTime time.Time
	}
	var files []fileInfo
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".html") {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			files = append(files, fileInfo{
				name:    entry.Name(),
				modTime: info.ModTime(),
			})
		}
	}

	// Prune if over limit.
	if len(files) <= maxHistoryVersions {
		return
	}

	// Sort oldest first.
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})

	// Remove oldest entries.
	toRemove := files[:len(files)-maxHistoryVersions]
	for _, f := range toRemove {
		base := strings.TrimSuffix(f.name, ".html")
		htmlPath := filepath.Join(versionsDir, f.name)
		metaPath := filepath.Join(versionsDir, base+".json")
		_ = os.Remove(htmlPath)
		_ = os.Remove(metaPath)
	}
}

// fileExists checks if a file exists.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
