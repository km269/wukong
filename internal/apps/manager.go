// Package apps provides creation, management, and launching of
// custom HTML standalone window applications. Similar to Goose's
// Apps feature.
package apps

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/km269/wukong/internal/config"
)

// AppInfo holds metadata about a custom HTML app.
type AppInfo struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	FilePath    string    `json:"file_path"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Size        int64     `json:"size"`
}

// Manager handles custom HTML app lifecycle.
type Manager struct {
	mu   sync.RWMutex
	cfg  *config.AppsConfig
	apps map[string]AppInfo
}

// NewManager creates a new apps manager.
func NewManager(cfg *config.AppsConfig) (*Manager, error) {
	appDir := cfg.AppDir
	if appDir == "" {
		appDir = ".wukong_apps"
	}

	if err := os.MkdirAll(appDir, 0755); err != nil {
		return nil, fmt.Errorf("create apps dir: %w", err)
	}

	m := &Manager{
		cfg:  cfg,
		apps: make(map[string]AppInfo),
	}

	// Load existing apps
	m.loadExisting()
	return m, nil
}

// CreateApp creates a new HTML app.
func (m *Manager) CreateApp(
	name, description, htmlContent string,
) (AppInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	filename := sanitizeAppName(name) + ".html"
	filePath := filepath.Join(m.cfg.AppDir, filename)

	if err := os.WriteFile(
		filePath, []byte(htmlContent), 0644,
	); err != nil {
		return AppInfo{},
			fmt.Errorf("write app file: %w", err)
	}

	info, _ := os.Stat(filePath)
	app := AppInfo{
		Name:        name,
		Description: description,
		FilePath:    filePath,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if info != nil {
		app.Size = info.Size()
	}

	m.apps[name] = app
	return app, nil
}

// GetApp returns an app by name.
func (m *Manager) GetApp(name string) (AppInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	app, ok := m.apps[name]
	return app, ok
}

// ListApps returns all apps.
func (m *Manager) ListApps() []AppInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]AppInfo, 0, len(m.apps))
	for _, app := range m.apps {
		result = append(result, app)
	}
	return result
}

// DeleteApp removes an app.
func (m *Manager) DeleteApp(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	app, ok := m.apps[name]
	if !ok {
		return fmt.Errorf("app %q not found", name)
	}

	if err := os.Remove(app.FilePath); err != nil {
		return fmt.Errorf("delete app file: %w", err)
	}

	delete(m.apps, name)
	return nil
}

// UpdateApp updates an existing app's content.
func (m *Manager) UpdateApp(
	name, htmlContent string,
) (AppInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	app, ok := m.apps[name]
	if !ok {
		return AppInfo{},
			fmt.Errorf("app %q not found", name)
	}

	if err := os.WriteFile(
		app.FilePath, []byte(htmlContent), 0644,
	); err != nil {
		return AppInfo{},
			fmt.Errorf("write app file: %w", err)
	}

	info, _ := os.Stat(app.FilePath)
	app.UpdatedAt = time.Now()
	if info != nil {
		app.Size = info.Size()
	}
	m.apps[name] = app
	return app, nil
}

func (m *Manager) loadExisting() {
	entries, err := os.ReadDir(m.cfg.AppDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".html" {
			continue
		}

		filePath := filepath.Join(m.cfg.AppDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}

		name := entry.Name()[:len(entry.Name())-5] // Remove .html
		m.apps[name] = AppInfo{
			Name:      name,
			FilePath:  filePath,
			CreatedAt: info.ModTime(),
			UpdatedAt: info.ModTime(),
			Size:      info.Size(),
		}
	}
}

// GenerateAppTemplate generates a default HTML app template.
func GenerateAppTemplate(title string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>%s</title>
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body {
      font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
      background: #f5f5f5;
      color: #333;
      display: flex;
      flex-direction: column;
      align-items: center;
      min-height: 100vh;
      padding: 40px 20px;
    }
    .container {
      max-width: 800px;
      width: 100%%;
      background: white;
      border-radius: 12px;
      box-shadow: 0 2px 8px rgba(0,0,0,0.1);
      padding: 40px;
    }
    h1 { font-size: 28px; margin-bottom: 16px; color: #1a1a1a; }
    p { font-size: 16px; line-height: 1.6; color: #666; }
  </style>
</head>
<body>
  <div class="container">
    <h1>%s</h1>
    <p>This is a Wukong custom HTML app. Edit this content to build your application.</p>
  </div>
</body>
</html>`, title, title)
}

func sanitizeAppName(name string) string {
	result := ""
	for _, c := range name {
		if (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '-' || c == '_' {
			result += string(c)
		}
	}
	if result == "" {
		result = "app"
	}
	return result
}
