package mcpapps

import (
	"strings"
	"testing"

	"github.com/km269/wukong/internal/apps"
	"github.com/km269/wukong/internal/config"
)

// newTestAppsManager creates an apps.Manager backed by a temp dir.
func newTestAppsManager(t *testing.T) *apps.Manager {
	t.Helper()
	tmpDir := t.TempDir()
	mgr, err := apps.NewManager(&config.AppsConfig{Enabled: true, AppDir: tmpDir})
	if err != nil {
		t.Fatalf("apps.NewManager() error = %v", err)
	}
	return mgr
}

func TestEscapeJS(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "plain text", in: "hello world", want: "hello world"},
		{name: "backslash", in: `a\b`, want: `a\\b`},
		{name: "double quote", in: `say "hi"`, want: `say \"hi\"`},
		{name: "single quote", in: `it's`, want: `it\'s`},
		{name: "newline", in: "a\nb", want: `a\nb`},
		{name: "carriage return", in: "a\rb", want: `a\rb`},
		{name: "tab", in: "a\tb", want: `a\tb`},
		{name: "all special chars", in: `\` + "\n\r\t" + `"'`, want: `\\\n\r\t\"\'`},
		{name: "unicode preserved", in: "你好", want: "你好"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := escapeJS(tt.in); got != tt.want {
				t.Errorf("escapeJS(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestJoinSandboxAttrs(t *testing.T) {
	tests := []struct {
		name  string
		attrs []string
		want  string
	}{
		{name: "empty", attrs: nil, want: ""},
		{name: "single", attrs: []string{"allow-scripts"}, want: "allow-scripts"},
		{
			name:  "multiple",
			attrs: []string{"allow-scripts", "allow-same-origin"},
			want:  "allow-scripts allow-same-origin",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := joinSandboxAttrs(tt.attrs); got != tt.want {
				t.Errorf("joinSandboxAttrs(%v) = %q, want %q", tt.attrs, got, tt.want)
			}
		})
	}
}

func newTestMCPManager(t *testing.T) *Manager {
	t.Helper()
	return NewManager(newTestAppsManager(t))
}

func TestRegisterAppAsMCPResource_AppNotFound(t *testing.T) {
	m := newTestMCPManager(t)

	_, err := m.RegisterAppAsMCPResource("missing-app", "desc")
	if err == nil || !strings.Contains(err.Error(), "app not found: missing-app") {
		t.Fatalf("RegisterAppAsMCPResource() error = %v, want containing %q", err, "app not found: missing-app")
	}
}

func TestRegisterAppAsMCPResource_Success(t *testing.T) {
	mgr := newTestAppsManager(t)
	if _, err := mgr.CreateApp("demo", "Demo description", "<html><body>Hello</body></html>"); err != nil {
		t.Fatalf("CreateApp() error = %v", err)
	}
	m := NewManager(mgr)

	resource, err := m.RegisterAppAsMCPResource("demo", "fallback description")
	if err != nil {
		t.Fatalf("RegisterAppAsMCPResource() error = %v", err)
	}

	if resource.URI != "ui://wukong-apps/demo" {
		t.Errorf("URI = %q, want %q", resource.URI, "ui://wukong-apps/demo")
	}
	// App description must override the passed-in one.
	if resource.Description != "Demo description" {
		t.Errorf("Description = %q, want app description %q", resource.Description, "Demo description")
	}
	if resource.MimeType != MimeType {
		t.Errorf("MimeType = %q, want %q", resource.MimeType, MimeType)
	}

	// Resource and content must be queryable through the manager.
	got, ok := m.GetMCPResource("ui://wukong-apps/demo")
	if !ok || got != resource {
		t.Errorf("GetMCPResource() = %v, %v", got, ok)
	}
	html, ok := m.GetMCPResourceContent("ui://wukong-apps/demo")
	if !ok || html != "<html><body>Hello</body></html>" {
		t.Errorf("GetMCPResourceContent() = %q, %v", html, ok)
	}
}

func TestRegisterAppAsMCPResource_DescriptionFallback(t *testing.T) {
	mgr := newTestAppsManager(t)
	// App created without description.
	if _, err := mgr.CreateApp("plain", "", "<html>hi</html>"); err != nil {
		t.Fatalf("CreateApp() error = %v", err)
	}
	m := NewManager(mgr)

	resource, err := m.RegisterAppAsMCPResource("plain", "passed description")
	if err != nil {
		t.Fatalf("RegisterAppAsMCPResource() error = %v", err)
	}
	if resource.Description != "passed description" {
		t.Errorf("Description = %q, want passed-in %q", resource.Description, "passed description")
	}
}

func TestRegisterToolForApp_Success(t *testing.T) {
	mgr := newTestAppsManager(t)
	if _, err := mgr.CreateApp("demo", "Demo", "<html>hi</html>"); err != nil {
		t.Fatalf("CreateApp() error = %v", err)
	}
	m := NewManager(mgr)

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
	}
	err := m.RegisterToolForApp("demo", "search_ui", "Search the UI", schema)
	if err != nil {
		t.Fatalf("RegisterToolForApp() error = %v", err)
	}

	tools := m.GetMCPTools()
	if len(tools) != 1 {
		t.Fatalf("GetMCPTools() len = %d, want 1", len(tools))
	}
	got := tools[0]
	if got.Name != "search_ui" || got.Description != "Search the UI" {
		t.Errorf("tool = %+v", got)
	}
	if got.Meta == nil || got.Meta.UI == nil {
		t.Fatal("tool Meta.UI = nil")
	}
	if got.Meta.UI.ResourceURI != "ui://wukong-apps/demo" {
		t.Errorf("ResourceURI = %q, want %q", got.Meta.UI.ResourceURI, "ui://wukong-apps/demo")
	}
	if len(got.Meta.UI.Visibility) != 2 ||
		got.Meta.UI.Visibility[0] != VisibilityModel ||
		got.Meta.UI.Visibility[1] != VisibilityApp {
		t.Errorf("Visibility = %v, want [model app]", got.Meta.UI.Visibility)
	}
	schemaJSON, ok := got.InputSchema.(map[string]any)
	if !ok || schemaJSON["type"] != "object" {
		t.Errorf("InputSchema = %v", got.InputSchema)
	}
}

func TestRegisterToolForApp_AppNotFound(t *testing.T) {
	m := newTestMCPManager(t)

	err := m.RegisterToolForApp("missing-app", "tool", "desc", nil)
	if err == nil || !strings.Contains(err.Error(), "app not found: missing-app") {
		t.Fatalf("RegisterToolForApp() error = %v, want containing %q", err, "app not found: missing-app")
	}
	if len(m.GetMCPTools()) != 0 {
		t.Error("tool registered despite app not found")
	}
}

func TestSetCSPForResource(t *testing.T) {
	m := newTestMCPManager(t)
	_ = m.host.RegisterResource(NewUIResource("ui://wukong-apps/demo", "Demo", ""))

	// Unknown resource.
	if err := m.SetCSPForResource("ui://missing", CSPFromConfig(nil, nil, nil, nil)); err == nil {
		t.Fatal("SetCSPForResource(missing) error = nil, want non-nil")
	} else if !strings.Contains(err.Error(), "resource not found: ui://missing") {
		t.Fatalf("SetCSPForResource() error = %v", err)
	}

	// Known resource.
	if err := m.SetCSPForResource("ui://wukong-apps/demo", CSPFromConfig(
		[]string{"api.example.com"}, nil, nil, nil,
	)); err != nil {
		t.Fatalf("SetCSPForResource() error = %v", err)
	}
	resource, _ := m.host.GetResource("ui://wukong-apps/demo")
	if resource.Meta == nil || resource.Meta.CSP == nil {
		t.Fatal("CSP not set on resource")
	}
	if len(resource.Meta.CSP.ConnectDomains) != 1 || resource.Meta.CSP.ConnectDomains[0] != "api.example.com" {
		t.Errorf("ConnectDomains = %v", resource.Meta.CSP.ConnectDomains)
	}
}

func TestSetPermissionsForResource(t *testing.T) {
	m := newTestMCPManager(t)
	_ = m.host.RegisterResource(NewUIResource("ui://wukong-apps/demo", "Demo", ""))

	if err := m.SetPermissionsForResource("ui://missing", &Permissions{}); err == nil {
		t.Fatal("SetPermissionsForResource(missing) error = nil, want non-nil")
	}

	perms := &Permissions{Camera: &struct{}{}, Microphone: &struct{}{}}
	if err := m.SetPermissionsForResource("ui://wukong-apps/demo", perms); err != nil {
		t.Fatalf("SetPermissionsForResource() error = %v", err)
	}
	resource, _ := m.host.GetResource("ui://wukong-apps/demo")
	if resource.Meta == nil || resource.Meta.Permissions != perms {
		t.Error("Permissions not set on resource")
	}
}

func TestGetCSPHeaders(t *testing.T) {
	m := newTestMCPManager(t)

	// Unknown resource falls back to host CSP headers.
	got := m.GetCSPHeaders("ui://missing")
	if !strings.HasPrefix(got, "Content-Security-Policy: ") {
		t.Errorf("GetCSPHeaders(missing) = %q, want CSP prefix", got)
	}
	if !strings.Contains(got, "default-src 'none'") {
		t.Errorf("GetCSPHeaders(missing) missing default-src 'none', got: %s", got)
	}

	// Known resource without per-resource CSP also uses host CSP.
	_ = m.host.RegisterResource(NewUIResource("ui://wukong-apps/demo", "Demo", ""))
	got = m.GetCSPHeaders("ui://wukong-apps/demo")
	if !strings.Contains(got, "default-src 'none'") {
		t.Errorf("GetCSPHeaders() missing default-src 'none', got: %s", got)
	}
}

func TestGenerateSandboxedViewHTML_Success(t *testing.T) {
	mgr := newTestAppsManager(t)
	if _, err := mgr.CreateApp("demo", "Demo", "<html><body>Hello \"world\"</body></html>"); err != nil {
		t.Fatalf("CreateApp() error = %v", err)
	}
	m := NewManager(mgr)
	if _, err := m.RegisterAppAsMCPResource("demo", "desc"); err != nil {
		t.Fatalf("RegisterAppAsMCPResource() error = %v", err)
	}

	html, err := m.GenerateSandboxedViewHTML("ui://wukong-apps/demo")
	if err != nil {
		t.Fatalf("GenerateSandboxedViewHTML() error = %v", err)
	}

	for _, want := range []string{
		"<!DOCTYPE html>",
		`<meta http-equiv="Content-Security-Policy" content="`,
		"default-src 'none'",
		"<title>demo</title>",
		`const appContent = `,
		`<html><body>Hello \"world\"</body></html>`,
		"allow-scripts allow-same-origin",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("sandbox HTML missing %q", want)
		}
	}
}

func TestGenerateSandboxedViewHTML_UnknownResource(t *testing.T) {
	m := newTestMCPManager(t)

	_, err := m.GenerateSandboxedViewHTML("ui://missing")
	if err == nil || !strings.Contains(err.Error(), "resource not found: ui://missing") {
		t.Fatalf("GenerateSandboxedViewHTML() error = %v, want containing %q", err, "resource not found: ui://missing")
	}
}

func TestGenerateSandboxedViewHTML_NoContent(t *testing.T) {
	m := newTestMCPManager(t)
	_ = m.host.RegisterResource(NewUIResource("ui://wukong-apps/demo", "Demo", ""))

	_, err := m.GenerateSandboxedViewHTML("ui://wukong-apps/demo")
	if err == nil || !strings.Contains(err.Error(), "content not found for: ui://wukong-apps/demo") {
		t.Fatalf("GenerateSandboxedViewHTML() error = %v, want containing %q", err, "content not found for: ui://wukong-apps/demo")
	}
}

func TestManager_ListApps(t *testing.T) {
	mgr := newTestAppsManager(t)
	_, _ = mgr.CreateApp("a", "A", "<html>a</html>")
	_, _ = mgr.CreateApp("b", "B", "<html>b</html>")
	m := NewManager(mgr)

	apps := m.ListApps()
	if len(apps) != 2 {
		t.Fatalf("ListApps() len = %d, want 2", len(apps))
	}
}

func TestManager_GetMCPResources(t *testing.T) {
	mgr := newTestAppsManager(t)
	_, _ = mgr.CreateApp("demo", "Demo", "<html>hi</html>")
	m := NewManager(mgr)
	_, _ = m.RegisterAppAsMCPResource("demo", "desc")

	resources := m.GetMCPResources()
	if len(resources) != 1 {
		t.Fatalf("GetMCPResources() len = %d, want 1", len(resources))
	}
	if resources[0].URI != "ui://wukong-apps/demo" {
		t.Errorf("URI = %q", resources[0].URI)
	}
}
