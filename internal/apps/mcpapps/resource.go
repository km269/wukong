// Package mcpapps provides MCP Apps specification implementation.
// Following the official MCP Apps specification (io.modelcontextprotocol/ui).
// Supports interactive UI rendering via ui:// resource scheme.
package mcpapps

import (
	"encoding/json"
	"fmt"
)

// Protocol version following MCP Apps specification.
const ProtocolVersion = "2026-01-26"

// MIME type for MCP Apps HTML content.
const MimeType = "text/html;profile=mcp-app"

// URI scheme for UI resources.
const URIScheme = "ui://"

// UIResource represents a UI resource as defined in MCP Apps spec.
type UIResource struct {
	// URI uniquely identifies the UI resource using ui:// scheme.
	URI string `json:"uri"`
	// Name is the human-readable display name.
	Name string `json:"name"`
	// Description describes the UI's purpose.
	Description string `json:"description,omitempty"`
	// MimeType should be "text/html;profile=mcp-app".
	MimeType string `json:"mimeType"`
	// Meta contains security and rendering configuration.
	Meta *UIResourceMeta `json:"_meta,omitempty"`
}

// UIResourceMeta contains metadata for security and rendering.
type UIResourceMeta struct {
	// CSP configures Content Security Policy.
	CSP *CSPConfig `json:"csp,omitempty"`
	// Permissions requests browser capabilities.
	Permissions *Permissions `json:"permissions,omitempty"`
	// Domain specifies a dedicated origin for the view.
	Domain string `json:"domain,omitempty"`
	// PrefersBorder indicates visual boundary preference.
	PrefersBorder *bool `json:"prefersBorder,omitempty"`
}

// CSPConfig defines Content Security Policy settings.
type CSPConfig struct {
	// ConnectDomains are origins for network requests (fetch/XHR/WebSocket).
	ConnectDomains []string `json:"connectDomains,omitempty"`
	// ResourceDomains are origins for static resources (scripts, images, styles, fonts).
	ResourceDomains []string `json:"resourceDomains,omitempty"`
	// FrameDomains are origins allowed for nested iframes.
	FrameDomains []string `json:"frameDomains,omitempty"`
	// BaseUriDomains are additional allowed base URIs.
	BaseUriDomains []string `json:"baseUriDomains,omitempty"`
}

// Permissions requests browser capabilities.
type Permissions struct {
	Camera       *struct{} `json:"camera,omitempty"`
	Microphone   *struct{} `json:"microphone,omitempty"`
	Geolocation  *struct{} `json:"geolocation,omitempty"`
	ClipboardWrite *struct{} `json:"clipboardWrite,omitempty"`
}

// ToolMeta defines tool metadata for UI association.
type ToolMeta struct {
	// ResourceURI is the URI of the UI resource for rendering tool results.
	ResourceURI string `json:"resourceUri,omitempty"`
	// Visibility controls who can access this tool.
	Visibility []Visibility `json:"visibility,omitempty"`
}

// Visibility defines tool visibility options.
type Visibility string

const (
	// VisibilityModel indicates tool is visible to and callable by the agent.
	VisibilityModel Visibility = "model"
	// VisibilityApp indicates tool is callable by the app from this server only.
	VisibilityApp Visibility = "app"
)

// NewUIResource creates a new UI resource with default values.
func NewUIResource(uri, name, description string) *UIResource {
	return &UIResource{
		URI:         uri,
		Name:        name,
		Description: description,
		MimeType:    MimeType,
		Meta:        &UIResourceMeta{},
	}
}

// SetCSP configures CSP settings.
func (r *UIResource) SetCSP(csp *CSPConfig) *UIResource {
	if r.Meta == nil {
		r.Meta = &UIResourceMeta{}
	}
	r.Meta.CSP = csp
	return r
}

// SetPermissions configures permission requests.
func (r *UIResource) SetPermissions(p *Permissions) *UIResource {
	if r.Meta == nil {
		r.Meta = &UIResourceMeta{}
	}
	r.Meta.Permissions = p
	return r
}

// SetPrefersBorder sets the border preference.
func (r *UIResource) SetPrefersBorder(prefers bool) *UIResource {
	if r.Meta == nil {
		r.Meta = &UIResourceMeta{}
	}
	r.Meta.PrefersBorder = &prefers
	return r
}

// Validate checks if the resource is valid.
func (r *UIResource) Validate() error {
	if r.URI == "" {
		return fmt.Errorf("URI is required")
	}
	if !hasUIScheme(r.URI) {
		return fmt.Errorf("URI must use ui:// scheme")
	}
	if r.MimeType != MimeType {
		return fmt.Errorf("MimeType must be %q", MimeType)
	}
	return nil
}

// hasUIScheme checks if URI uses the ui:// scheme.
func hasUIScheme(uri string) bool {
	return len(uri) >= 5 && uri[:5] == URIScheme
}

// GenerateDefaultCSP returns a restrictive CSP by default.
func GenerateDefaultCSP() *CSPConfig {
	return &CSPConfig{
		ConnectDomains:  []string{},
		ResourceDomains: []string{},
		FrameDomains:   []string{},
		BaseUriDomains: []string{},
	}
}

// CSPFromConfig creates a CSP config from allowed domains.
func CSPFromConfig(connect, resource, frame, baseURI []string) *CSPConfig {
	return &CSPConfig{
		ConnectDomains:   connect,
		ResourceDomains: resource,
		FrameDomains:    frame,
		BaseUriDomains:  baseURI,
	}
}

// ToJSON serializes the resource to JSON.
func (r *UIResource) ToJSON() ([]byte, error) {
	return json.Marshal(r)
}

// FromJSON deserializes a resource from JSON.
func FromJSON(data []byte) (*UIResource, error) {
	var r UIResource
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ResourceContent represents the content returned by resources/read.
type ResourceContent struct {
	URI      string      `json:"uri"`
	MimeType string      `json:"mimeType"`
	Text     string      `json:"text,omitempty"`
	Blob     string      `json:"blob,omitempty"` // base64 encoded
	Meta     *ContentMeta `json:"_meta,omitempty"`
}

// ContentMeta contains UI-specific metadata for resource content.
type ContentMeta struct {
	UI *UIMeta `json:"ui,omitempty"`
}

// UIMeta contains UI rendering metadata.
type UIMeta struct {
	CSP            *CSPConfig    `json:"csp,omitempty"`
	Permissions    *Permissions  `json:"permissions,omitempty"`
	Domain         string       `json:"domain,omitempty"`
	PrefersBorder  *bool        `json:"prefersBorder,omitempty"`
}

// NewResourceContent creates content from HTML text.
func NewResourceContent(uri, html string, meta *UIMeta) *ResourceContent {
	return &ResourceContent{
		URI:      uri,
		MimeType: MimeType,
		Text:     html,
		Meta: &ContentMeta{
			UI: meta,
		},
	}
}

// NewResourceContentFromBlob creates content from base64 encoded HTML.
func NewResourceContentFromBlob(uri string, blob string, meta *UIMeta) *ResourceContent {
	return &ResourceContent{
		URI:      uri,
		MimeType: MimeType,
		Blob:     blob,
		Meta: &ContentMeta{
			UI: meta,
		},
	}
}

// Validate checks if the content is valid.
func (c *ResourceContent) Validate() error {
	if c.URI == "" {
		return fmt.Errorf("URI is required")
	}
	if c.MimeType != MimeType {
		return fmt.Errorf("MimeType must be %q", MimeType)
	}
	if c.Text == "" && c.Blob == "" {
		return fmt.Errorf("either Text or Blob must be provided")
	}
	return nil
}
