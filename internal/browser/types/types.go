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
	Close()
}
