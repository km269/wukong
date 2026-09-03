package mcpapps

import (
	"strings"
	"testing"
)

func TestNewUIResource_Defaults(t *testing.T) {
	r := NewUIResource("ui://wukong-apps/demo", "Demo App", "A demo app")

	if r.URI != "ui://wukong-apps/demo" {
		t.Errorf("URI = %q, want %q", r.URI, "ui://wukong-apps/demo")
	}
	if r.Name != "Demo App" {
		t.Errorf("Name = %q, want %q", r.Name, "Demo App")
	}
	if r.Description != "A demo app" {
		t.Errorf("Description = %q, want %q", r.Description, "A demo app")
	}
	if r.MimeType != MimeType {
		t.Errorf("MimeType = %q, want %q", r.MimeType, MimeType)
	}
	if r.Meta == nil {
		t.Fatal("Meta = nil, want non-nil")
	}
}

func TestUIResource_Validate(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		mime    string
		wantErr string
	}{
		{name: "valid ui resource", uri: "ui://wukong-apps/demo", mime: MimeType},
		{name: "empty URI", uri: "", mime: MimeType, wantErr: "URI is required"},
		{name: "unregistered scheme", uri: "http://example.com/app", mime: MimeType, wantErr: "URI must use ui:// scheme"},
		{name: "short scheme prefix", uri: "ui:/x", mime: MimeType, wantErr: "URI must use ui:// scheme"},
		{name: "no scheme at all", uri: "wukong-apps/demo", mime: MimeType, wantErr: "URI must use ui:// scheme"},
		{name: "wrong mime type", uri: "ui://wukong-apps/demo", mime: "text/html", wantErr: `MimeType must be "text/html;profile=mcp-app"`},
		{name: "empty mime type", uri: "ui://wukong-apps/demo", mime: "", wantErr: `MimeType must be "text/html;profile=mcp-app"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &UIResource{URI: tt.uri, MimeType: tt.mime}
			err := r.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestHasUIScheme(t *testing.T) {
	tests := []struct {
		uri  string
		want bool
	}{
		{uri: "ui://wukong-apps/demo", want: true},
		{uri: "ui://", want: true},
		{uri: "u://xxxx", want: false},
		{uri: "ui:/demo", want: false},
		{uri: "abui://demo", want: false},
		{uri: "", want: false},
		{uri: "ui", want: false},
	}
	for _, tt := range tests {
		if got := hasUIScheme(tt.uri); got != tt.want {
			t.Errorf("hasUIScheme(%q) = %v, want %v", tt.uri, got, tt.want)
		}
	}
}

func TestUIResource_ChainableSetters(t *testing.T) {
	r := &UIResource{URI: "ui://demo", MimeType: MimeType}

	csp := CSPFromConfig(nil, nil, nil, nil)
	perms := &Permissions{Camera: &struct{}{}}
	got := r.SetCSP(csp).SetPermissions(perms).SetPrefersBorder(true)

	if got != r {
		t.Fatal("setters must return the receiver for chaining")
	}
	if r.Meta == nil {
		t.Fatal("Meta = nil, want non-nil after setters")
	}
	if r.Meta.CSP != csp {
		t.Error("CSP not set")
	}
	if r.Meta.Permissions != perms {
		t.Error("Permissions not set")
	}
	if r.Meta.PrefersBorder == nil || !*r.Meta.PrefersBorder {
		t.Error("PrefersBorder not set to true")
	}
}

func TestUIResource_SetterInitializesNilMeta(t *testing.T) {
	r := &UIResource{URI: "ui://demo", MimeType: MimeType, Meta: nil}

	r.SetCSP(CSPFromConfig(nil, nil, nil, nil))

	if r.Meta == nil {
		t.Fatal("Meta = nil, want initialized by SetCSP")
	}
	if r.Meta.CSP == nil {
		t.Fatal("CSP = nil, want set")
	}
}

func TestGenerateDefaultCSP(t *testing.T) {
	csp := GenerateDefaultCSP()
	if csp == nil {
		t.Fatal("GenerateDefaultCSP() = nil")
	}
	if len(csp.ConnectDomains) != 0 || len(csp.ResourceDomains) != 0 ||
		len(csp.FrameDomains) != 0 || len(csp.BaseUriDomains) != 0 {
		t.Errorf("default CSP should have empty domain lists, got %+v", csp)
	}
}

func TestCSPFromConfig(t *testing.T) {
	connect := []string{"api.example.com"}
	resource := []string{"static.example.com"}
	frame := []string{"frame.example.com"}
	baseURI := []string{"example.com"}

	csp := CSPFromConfig(connect, resource, frame, baseURI)

	if len(csp.ConnectDomains) != 1 || csp.ConnectDomains[0] != "api.example.com" {
		t.Errorf("ConnectDomains = %v", csp.ConnectDomains)
	}
	if len(csp.ResourceDomains) != 1 || csp.ResourceDomains[0] != "static.example.com" {
		t.Errorf("ResourceDomains = %v", csp.ResourceDomains)
	}
	if len(csp.FrameDomains) != 1 || csp.FrameDomains[0] != "frame.example.com" {
		t.Errorf("FrameDomains = %v", csp.FrameDomains)
	}
	if len(csp.BaseUriDomains) != 1 || csp.BaseUriDomains[0] != "example.com" {
		t.Errorf("BaseUriDomains = %v", csp.BaseUriDomains)
	}
}

func TestUIResource_ToJSON_FromJSON_RoundTrip(t *testing.T) {
	r := NewUIResource("ui://wukong-apps/demo", "Demo", "desc")
	prefersBorder := true
	r.Meta.Permissions = &Permissions{Camera: &struct{}{}}
	r.Meta.PrefersBorder = &prefersBorder
	r.Meta.CSP = CSPFromConfig(
		[]string{"api.example.com"},
		[]string{"static.example.com"},
		[]string{"frame.example.com"},
		[]string{"base.example.com"},
	)

	data, err := r.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON() error = %v", err)
	}

	got, err := FromJSON(data)
	if err != nil {
		t.Fatalf("FromJSON() error = %v", err)
	}

	if got.URI != r.URI || got.Name != r.Name || got.Description != r.Description ||
		got.MimeType != r.MimeType {
		t.Errorf("round-trip mismatch: %+v vs %+v", got, r)
	}
	if got.Meta == nil || got.Meta.CSP == nil {
		t.Fatal("Meta.CSP lost in round-trip")
	}
	if len(got.Meta.CSP.ConnectDomains) != 1 || got.Meta.CSP.ConnectDomains[0] != "api.example.com" {
		t.Errorf("ConnectDomains = %v after round-trip", got.Meta.CSP.ConnectDomains)
	}
	if got.Meta.Permissions == nil || got.Meta.Permissions.Camera == nil {
		t.Error("Permissions.Camera lost in round-trip")
	}
	if got.Meta.PrefersBorder == nil || !*got.Meta.PrefersBorder {
		t.Error("PrefersBorder lost in round-trip")
	}
}

func TestFromJSON_InvalidJSON(t *testing.T) {
	if _, err := FromJSON([]byte("{not valid json")); err == nil {
		t.Fatal("FromJSON() error = nil, want non-nil")
	}
}

func TestNewResourceContent(t *testing.T) {
	c := NewResourceContent("ui://demo", "<html>hi</html>", &UIMeta{Domain: "example.com"})

	if c.URI != "ui://demo" || c.MimeType != MimeType || c.Text != "<html>hi</html>" {
		t.Errorf("NewResourceContent() = %+v", c)
	}
	if c.Meta == nil || c.Meta.UI == nil || c.Meta.UI.Domain != "example.com" {
		t.Error("Meta.UI lost")
	}
	if c.Blob != "" {
		t.Errorf("Blob = %q, want empty", c.Blob)
	}
}

func TestNewResourceContentFromBlob(t *testing.T) {
	c := NewResourceContentFromBlob("ui://demo", "PGh0bWw+", nil)

	if c.URI != "ui://demo" || c.MimeType != MimeType || c.Blob != "PGh0bWw+" {
		t.Errorf("NewResourceContentFromBlob() = %+v", c)
	}
	if c.Text != "" {
		t.Errorf("Text = %q, want empty", c.Text)
	}
	if c.Meta == nil || c.Meta.UI != nil {
		t.Errorf("Meta = %+v, want empty UI meta", c.Meta)
	}
}

func TestResourceContent_Validate(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		mime    string
		text    string
		blob    string
		wantErr string
	}{
		{name: "valid text content", uri: "ui://demo", mime: MimeType, text: "<html>hi</html>"},
		{name: "valid blob content", uri: "ui://demo", mime: MimeType, blob: "PGh0bWw+"},
		{name: "empty URI", uri: "", mime: MimeType, text: "x", wantErr: "URI is required"},
		{name: "wrong mime type", uri: "ui://demo", mime: "text/plain", text: "x", wantErr: `MimeType must be "text/html;profile=mcp-app"`},
		{name: "empty text and blob", uri: "ui://demo", mime: MimeType, wantErr: "either Text or Blob must be provided"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ResourceContent{URI: tt.uri, MimeType: tt.mime, Text: tt.text, Blob: tt.blob}
			err := c.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
