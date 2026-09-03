package types

import (
	"context"
	"time"
)

// CollectedAsset holds an asset captured from the browser's network stack during page rendering.
type CollectedAsset struct {
	URL         string
	Body        []byte
	ContentType string
	StatusCode  int
}

// DiscoveredAPI represents an API endpoint discovered by
// intercepting XHR/fetch requests during page rendering.
// Hidden APIs are endpoints that serve structured data (JSON,
// XML, GraphQL) but are not visible as links in the HTML — they
// are called dynamically by the page's JavaScript.
type DiscoveredAPI struct {
	URL         string `json:"url"`
	Method      string `json:"method,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	StatusCode  int    `json:"status_code,omitempty"`
	// ResourceType is the CDP resource type (XHR, Fetch, etc.).
	ResourceType string `json:"resource_type,omitempty"`
	// PaginationKind is the detected pagination style, if any.
	// One of: "query_param", "path_based", "offset_limit",
	// "cursor", "header", "none".
	PaginationKind string `json:"pagination_kind,omitempty"`
}

type RenderResult struct {
	HTML                string
	URL                 string
	Title               string
	ContentType         string
	CloudflareClearance string
	Referer             string
	// Assets collected from the browser network stack during page rendering.
	// Key is the asset URL.
	CollectedAssets map[string]*CollectedAsset
	// Links extracted from the rendered DOM using JavaScript.
	// This includes dynamically generated links that static HTML parsing may miss.
	ExtractedLinks []string
	// DiscoveredAPIs are hidden API endpoints (JSON/XML/GraphQL)
	// intercepted from XHR/fetch requests during page rendering.
	DiscoveredAPIs []DiscoveredAPI
}

type ErrNotHTML struct {
	URL         string
	ContentType string
}

func (e *ErrNotHTML) Error() string {
	return "url " + e.URL + " returned " + e.ContentType + ", not HTML"
}

// AssetDownloadResult holds the result of downloading an asset via the browser network stack.
type AssetDownloadResult struct {
	URL         string
	Body        []byte
	ContentType string
	StatusCode  int
}

type BrowserBackend interface {
	Render(ctx context.Context, url string) (*RenderResult, error)
	RenderWithReferer(ctx context.Context, url, referer string) (*RenderResult, error)
	SetSettle(d time.Duration)
	StealthEnabled() bool
	EnableStealth() error
	// SetBehaviorSimulation enables or disables human-like behavior simulation
	SetBehaviorSimulation(enabled bool)
	// DownloadAsset downloads an asset using the browser's network stack.
	// This is useful when the HTTP client cannot access (e.g., DNS issues) but the browser can.
	DownloadAsset(ctx context.Context, assetURL string, referer string) (*AssetDownloadResult, error)
	// Screenshot navigates to url and captures a real pixel screenshot as PNG
	// written to outputPath. On success it returns the absolute image path;
	// on failure it returns an error (non-nil error means no image was written).
	Screenshot(ctx context.Context, url string, outputPath string) (string, error)
	Close()
}

// UARotator is an optional BrowserBackend capability: the backend
// maintains a pool-wide user-agent profile and can rotate it mid-run.
// The antibot engine asserts this interface when escalating to
// aggressive mode. Both the chromedp and rod backends implement it.
type UARotator interface {
	RotateUA()
}

// AssetCollector is an optional BrowserBackend capability: renders
// capture subresource response bodies from the browser network stack
// into RenderResult.CollectedAssets. The rod backend implements it;
// callers can probe support before relying on that field instead of
// only checking it for nil after the fact.
type AssetCollector interface {
	CollectsAssets() bool
}

// Priority is a render scheduling hint used when browser workers are
// contended: a higher value is dequeued sooner. The zero value is
// PriorityUnset and is treated as PriorityNormal, so existing call
// sites that never mention priority keep today's behaviour.
type Priority int

const (
	PriorityUnset Priority = iota
	PriorityLow
	PriorityNormal
	PriorityHigh
)

// Normalize maps the zero value to PriorityNormal.
func (p Priority) Normalize() Priority {
	if p == PriorityUnset {
		return PriorityNormal
	}
	return p
}

// PriorityRenderer is an optional BrowserBackend capability: renders
// can hint their scheduling priority. When workers are contended,
// higher-priority renders are dequeued first, while long-waiting
// lower-priority renders are gradually promoted (aging) so they
// cannot starve. Both the chromedp and rod backends implement it.
type PriorityRenderer interface {
	RenderWithPriority(ctx context.Context, url, referer string, prio Priority) (*RenderResult, error)
}
