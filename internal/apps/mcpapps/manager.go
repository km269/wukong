// Package mcpapps provides MCP Apps specification implementation.
package mcpapps

import (
	"fmt"
	"sync"

	"github.com/km269/wukong/internal/apps"
)

// Manager provides MCP Apps functionality integrated with the apps Manager.
type Manager struct {
	mu              sync.RWMutex
	appsMgr         *apps.Manager
	host            *SandboxedHost
	resourceCounter int
}

// NewManager creates a new MCP Apps Manager.
func NewManager(appsMgr *apps.Manager) *Manager {
	return &Manager{
		appsMgr: appsMgr,
		host:    NewSandboxedHost(),
	}
}

// RegisterAppAsMCPResource registers an app as an MCP UI resource.
func (m *Manager) RegisterAppAsMCPResource(appName string, description string) (*UIResource, error) {
	// 获取应用信息
	app, ok := m.appsMgr.GetApp(appName)
	if !ok {
		return nil, fmt.Errorf("app not found: %s", appName)
	}

	// 生成 URI
	uri := fmt.Sprintf("ui://wukong-apps/%s", appName)

	// 创建 UI 资源
	resource := NewUIResource(uri, appName, description)
	if app.Description != "" {
		resource.Description = app.Description
	}

	// 读取 HTML 内容
	html, err := m.appsMgr.ReadAppHTML(appName)
	if err != nil {
		// 如果无法读取，使用占位符
		html = fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>%s</title>
</head>
<body>
  <h1>%s</h1>
  <p>%s</p>
</body>
</html>`, appName, appName, app.Description)
	}

	// 注册资源
	if err := m.host.RegisterResource(resource); err != nil {
		return nil, fmt.Errorf("register resource: %w", err)
	}

	// 注册内容
	if err := m.host.RegisterContent(uri, html); err != nil {
		return nil, fmt.Errorf("register content: %w", err)
	}

	return resource, nil
}

// GetMCPResources returns all registered MCP resources.
func (m *Manager) GetMCPResources() []*UIResource {
	return m.host.ListResources()
}

// GetMCPResource returns a specific MCP resource.
func (m *Manager) GetMCPResource(uri string) (*UIResource, bool) {
	return m.host.GetResource(uri)
}

// GetMCPResourceContent returns the HTML content for a resource.
func (m *Manager) GetMCPResourceContent(uri string) (string, bool) {
	return m.host.GetContent(uri)
}

// GetMCPTools returns all registered MCP tools.
func (m *Manager) GetMCPTools() []*ToolRegistration {
	return m.host.ListTools()
}

// RegisterToolForApp registers a tool associated with an app's UI.
func (m *Manager) RegisterToolForApp(appName, toolName, description string, inputSchema any) error {
	// 检查应用是否存在
	if _, ok := m.appsMgr.GetApp(appName); !ok {
		return fmt.Errorf("app not found: %s", appName)
	}

	// 生成 URI
	uri := fmt.Sprintf("ui://wukong-apps/%s", appName)

	// 注册工具
	tool := &ToolRegistration{
		Name:        toolName,
		Description: description,
		InputSchema: inputSchema,
		Meta: &ToolMetaHost{
			UI: &ToolUIMetaHost{
				ResourceURI: uri,
				Visibility:  []Visibility{VisibilityModel, VisibilityApp},
			},
		},
	}

	m.host.RegisterTool(tool)
	return nil
}

// SetCSPForResource sets CSP configuration for a resource.
func (m *Manager) SetCSPForResource(uri string, csp *CSPConfig) error {
	resource, ok := m.host.GetResource(uri)
	if !ok {
		return fmt.Errorf("resource not found: %s", uri)
	}

	resource.SetCSP(csp)
	return nil
}

// SetPermissionsForResource sets permission requests for a resource.
func (m *Manager) SetPermissionsForResource(uri string, perms *Permissions) error {
	resource, ok := m.host.GetResource(uri)
	if !ok {
		return fmt.Errorf("resource not found: %s", uri)
	}

	resource.SetPermissions(perms)
	return nil
}

// GetCSPHeaders returns CSP headers for a resource.
func (m *Manager) GetCSPHeaders(uri string) string {
	resource, ok := m.host.GetResource(uri)
	if !ok {
		return m.host.BuildCSPHeaders(nil)
	}
	return m.host.BuildCSPHeaders(resource)
}

// GenerateSandboxedViewHTML generates the HTML for a sandboxed view.
func (m *Manager) GenerateSandboxedViewHTML(uri string) (string, error) {
	resource, ok := m.host.GetResource(uri)
	if !ok {
		return "", fmt.Errorf("resource not found: %s", uri)
	}

	content, ok := m.host.GetContent(uri)
	if !ok {
		return "", fmt.Errorf("content not found for: %s", uri)
	}

	// 生成沙箱 HTML
	csp := m.host.BuildCSPHeaders(resource)
	sandbox := m.host.BuildSandboxAttributes()

	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta http-equiv="Content-Security-Policy" content="%s">
  <title>%s</title>
</head>
<body>
  <div id="app-container">
    <iframe id="app-frame" 
            src="about:blank" 
            sandbox="%s"
            style="border:none;width:100%%;height:100%%;">
    </iframe>
  </div>
  <script>
    // MCP Apps Sandboxed View
    const appContent = %s;
    const iframe = document.getElementById('app-frame');
    
    // Wait for iframe to load, then inject content
    iframe.onload = function() {
      iframe.srcdoc = appContent;
    };
    
    // Trigger load if already complete
    if (iframe.contentDocument.readyState === 'complete') {
      iframe.srcdoc = appContent;
    }
  </script>
</body>
</html>`, csp, resource.Name, joinSandboxAttrs(sandbox), escapeJS(content))

	return html, nil
}

// joinSandboxAttrs joins sandbox attributes.
func joinSandboxAttrs(attrs []string) string {
	result := ""
	for i, a := range attrs {
		if i > 0 {
			result += " "
		}
		result += a
	}
	return result
}

// escapeJS escapes a string for use in JavaScript.
func escapeJS(s string) string {
	result := ""
	for _, c := range s {
		switch c {
		case '\\':
			result += "\\\\"
		case '"':
			result += "\\\""
		case '\'':
			result += "\\'"
		case '\n':
			result += "\\n"
		case '\r':
			result += "\\r"
		case '\t':
			result += "\\t"
		default:
			result += string(c)
		}
	}
	return result
}

// ListApps returns all registered apps from the apps Manager.
func (m *Manager) ListApps() []apps.AppInfo {
	return m.appsMgr.ListApps()
}
