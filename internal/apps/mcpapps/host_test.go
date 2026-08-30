package mcpapps

import (
	"strings"
	"testing"
)

func TestNewSandboxedHost_DefaultCSP(t *testing.T) {
	h := NewSandboxedHost()

	csp := h.GetCSP()
	if csp == nil {
		t.Fatal("GetCSP() = nil, want default CSP")
	}
	if len(csp.ConnectDomains) != 0 {
		t.Errorf("default ConnectDomains = %v, want empty", csp.ConnectDomains)
	}
	if got := h.BuildCSPHeaders(nil); !strings.HasPrefix(got, "Content-Security-Policy: ") {
		t.Errorf("BuildCSPHeaders(nil) = %q, want CSP prefix", got)
	}
}

func TestBuildCSPHeaders_DefaultRestrictive(t *testing.T) {
	h := NewSandboxedHost()
	got := h.BuildCSPHeaders(nil)

	for _, want := range []string{
		"default-src 'none'",
		"script-src 'self' 'unsafe-inline'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data:",
		"media-src 'self' data:",
		"connect-src 'none'",
		"frame-src 'none'",
		"base-uri 'self'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("BuildCSPHeaders(nil) missing %q, got: %s", want, got)
		}
	}
}

func TestBuildCSPHeaders_Domains(t *testing.T) {
	h := NewSandboxedHost()
	csp := CSPFromConfig(
		[]string{"api.example.com", "ws.example.com"},
		[]string{"static.example.com"},
		[]string{"frame.example.com"},
		[]string{"base.example.com"},
	)
	h.SetCSP(csp)

	got := h.BuildCSPHeaders(nil)

	for _, want := range []string{
		"connect-src 'self' api.example.com ws.example.com",
		"script-src 'self' 'unsafe-inline' static.example.com",
		"style-src 'self' 'unsafe-inline' static.example.com",
		"img-src 'self' data: static.example.com",
		"frame-src frame.example.com",
		"base-uri 'self' base.example.com",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("BuildCSPHeaders(nil) missing %q, got: %s", want, got)
		}
	}
}

func TestBuildCSPHeaders_ResourceCSPOverridesHost(t *testing.T) {
	h := NewSandboxedHost()
	// Host allows something restrictive.
	hostCSP := CSPFromConfig(nil, nil, nil, nil)
	h.SetCSP(hostCSP)

	// Resource overrides with its own domains.
	resourceCSP := CSPFromConfig(
		[]string{"resource-api.example.com"},
		nil,
		nil,
		nil,
	)
	resource := &UIResource{
		URI:      "ui://wukong-apps/demo",
		MimeType: MimeType,
		Meta: &UIResourceMeta{
			CSP: resourceCSP,
		},
	}

	got := h.BuildCSPHeaders(resource)

	if !strings.Contains(got, "connect-src 'self' resource-api.example.com") {
		t.Errorf("resource CSP not applied, got: %s", got)
	}
	// frame-src must fall back to 'none' since resource CSP has no frame domains.
	if !strings.Contains(got, "frame-src 'none'") {
		t.Errorf("frame-src 'none' not applied from resource CSP, got: %s", got)
	}
}

func TestBuildCSPHeaders_EmptyResourceMetaUsesHostCSP(t *testing.T) {
	h := NewSandboxedHost()
	h.SetCSP(CSPFromConfig([]string{"api.example.com"}, nil, nil, nil))

	resource := &UIResource{URI: "ui://demo", MimeType: MimeType}

	got := h.BuildCSPHeaders(resource)
	if !strings.Contains(got, "connect-src 'self' api.example.com") {
		t.Errorf("host CSP not used for meta-less resource, got: %s", got)
	}
}

func TestJoinDomains(t *testing.T) {
	tests := []struct {
		name    string
		domains []string
		want    string
	}{
		{name: "empty", domains: nil, want: ""},
		{name: "single", domains: []string{"a.com"}, want: "a.com"},
		{name: "multiple", domains: []string{"a.com", "b.com", "c.com"}, want: "a.com b.com c.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := joinDomains(tt.domains); got != tt.want {
				t.Errorf("joinDomains(%v) = %q, want %q", tt.domains, got, tt.want)
			}
		})
	}
}

func TestJoinDirectives(t *testing.T) {
	tests := []struct {
		name       string
		directives []string
		want       string
	}{
		{name: "empty", directives: nil, want: ""},
		{name: "single", directives: []string{"default-src 'none'"}, want: "default-src 'none'"},
		{
			name:       "multiple",
			directives: []string{"a", "b"},
			want:       "a; b",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := joinDirectives(tt.directives); got != tt.want {
				t.Errorf("joinDirectives(%v) = %q, want %q", tt.directives, got, tt.want)
			}
		})
	}
}

func TestBuildSandboxAttributes(t *testing.T) {
	h := NewSandboxedHost()
	attrs := h.BuildSandboxAttributes()

	if len(attrs) != 2 {
		t.Fatalf("BuildSandboxAttributes() = %v, want 2 attributes", attrs)
	}
	if attrs[0] != "allow-scripts" || attrs[1] != "allow-same-origin" {
		t.Errorf("BuildSandboxAttributes() = %v", attrs)
	}
}

func TestSandboxedHost_RegisterAndGetResource(t *testing.T) {
	h := NewSandboxedHost()
	r := NewUIResource("ui://wukong-apps/demo", "Demo", "")

	if err := h.RegisterResource(r); err != nil {
		t.Fatalf("RegisterResource() error = %v", err)
	}

	got, ok := h.GetResource("ui://wukong-apps/demo")
	if !ok || got != r {
		t.Errorf("GetResource() = %v, %v; want %v, true", got, ok, r)
	}
	if _, ok := h.GetResource("ui://missing"); ok {
		t.Error("GetResource(missing) ok = true, want false")
	}
}

func TestSandboxedHost_RegisterResource_Invalid(t *testing.T) {
	h := NewSandboxedHost()
	r := &UIResource{URI: "http://example.com", MimeType: MimeType}

	err := h.RegisterResource(r)
	if err == nil || !strings.Contains(err.Error(), "validate resource:") {
		t.Fatalf("RegisterResource() error = %v, want containing %q", err, "validate resource:")
	}
	if _, ok := h.GetResource(r.URI); ok {
		t.Error("invalid resource was stored")
	}
}

func TestSandboxedHost_RegisterContent(t *testing.T) {
	h := NewSandboxedHost()
	r := NewUIResource("ui://wukong-apps/demo", "Demo", "")
	_ = h.RegisterResource(r)

	if err := h.RegisterContent(r.URI, "<html>hi</html>"); err != nil {
		t.Fatalf("RegisterContent() error = %v", err)
	}
	content, ok := h.GetContent(r.URI)
	if !ok || content != "<html>hi</html>" {
		t.Errorf("GetContent() = %q, %v", content, ok)
	}
	if _, ok := h.GetContent("ui://missing"); ok {
		t.Error("GetContent(missing) ok = true, want false")
	}
}

func TestSandboxedHost_RegisterContent_UnknownResource(t *testing.T) {
	h := NewSandboxedHost()

	err := h.RegisterContent("ui://missing", "<html>hi</html>")
	if err == nil || !strings.Contains(err.Error(), "resource not found: ui://missing") {
		t.Fatalf("RegisterContent() error = %v, want containing %q", err, "resource not found: ui://missing")
	}
}

func TestSandboxedHost_ListResources(t *testing.T) {
	h := NewSandboxedHost()
	for _, name := range []string{"a", "b", "c"} {
		uri := "ui://wukong-apps/" + name
		_ = h.RegisterResource(NewUIResource(uri, name, ""))
	}

	resources := h.ListResources()
	if len(resources) != 3 {
		t.Fatalf("ListResources() len = %d, want 3", len(resources))
	}
	seen := map[string]bool{}
	for _, r := range resources {
		seen[r.URI] = true
	}
	for _, name := range []string{"a", "b", "c"} {
		if !seen["ui://wukong-apps/"+name] {
			t.Errorf("ListResources() missing %s", name)
		}
	}
}

func TestSandboxedHost_RegisterAndGetTool(t *testing.T) {
	h := NewSandboxedHost()
	tool := &ToolRegistration{
		Name:        "open_ui",
		Description: "Open a UI",
		InputSchema: map[string]any{"type": "object"},
	}
	h.RegisterTool(tool)

	got, ok := h.GetTool("open_ui")
	if !ok || got != tool {
		t.Errorf("GetTool() = %v, %v", got, ok)
	}
	if _, ok := h.GetTool("missing"); ok {
		t.Error("GetTool(missing) ok = true, want false")
	}
}

func TestSandboxedHost_ListTools(t *testing.T) {
	h := NewSandboxedHost()
	h.RegisterTool(&ToolRegistration{Name: "t1"})
	h.RegisterTool(&ToolRegistration{Name: "t2"})

	tools := h.ListTools()
	if len(tools) != 2 {
		t.Fatalf("ListTools() len = %d, want 2", len(tools))
	}
	seen := map[string]bool{}
	for _, t := range tools {
		seen[t.Name] = true
	}
	if !seen["t1"] || !seen["t2"] {
		t.Errorf("ListTools() = %v", tools)
	}
}

func TestSandboxedHost_SetCSP(t *testing.T) {
	h := NewSandboxedHost()
	csp := CSPFromConfig([]string{"api.example.com"}, nil, nil, nil)
	h.SetCSP(csp)

	if h.GetCSP() != csp {
		t.Error("GetCSP() != SetCSP() value")
	}
}
