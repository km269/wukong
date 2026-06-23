// Package clone provides website cloning functionality.
package clone

import (
	"time"
)

// Result holds the outcome of a website cloning operation.
type Result struct {
	// Success indicates whether the clone completed successfully.
	Success bool

	// SeedURL is the original URL that was cloned.
	SeedURL string

	// Host is the extracted hostname from the seed URL.
	Host string

	// OutputDir is the directory where the clone was saved.
	OutputDir string

	// Pages is the total number of pages cloned.
	Pages int

	// Assets is the total number of assets downloaded.
	Assets int

	// SizeBytes is the total size of the cloned content.
	SizeBytes int64

	// Duration is the time taken for cloning.
	Duration time.Duration

	// Errors lists any errors encountered during cloning.
	Errors []string

	// Skipped lists URLs that were skipped (out of scope, excluded, etc.).
	Skipped []string

	// StartTime is when the cloning operation began.
	StartTime time.Time

	// EndTime is when the cloning operation finished.
	EndTime time.Time
}

// PageResult holds the result of cloning a single page.
type PageResult struct {
	// URL is the page URL that was cloned.
	URL string

	// FilePath is the local path where the page was saved.
	FilePath string

	// Title is the page title extracted from the HTML.
	Title string

	// Size is the size of the saved HTML file.
	Size int64

	// AssetsFound is the number of assets discovered on this page.
	AssetsFound int

	// LinksFound is the number of links discovered on this page.
	LinksFound int

	// Error is any error that occurred while cloning this page.
	Error string

	// Depth is the crawl depth at which this page was found.
	Depth int

	// FromCache indicates whether this page was served from cache.
	FromCache bool
}

// AssetResult holds the result of downloading a single asset.
type AssetResult struct {
	// URL is the original asset URL.
	URL string

	// FilePath is the local path where the asset was saved.
	FilePath string

	// Size is the size of the downloaded asset.
	Size int64

	// ContentType is the MIME type of the asset.
	ContentType string

	// Error is any error that occurred while downloading.
	Error string
}

// Stats tracks cloning progress statistics.
type Stats struct {
	// PagesCloned is the count of successfully cloned pages.
	PagesCloned int

	// PagesPending is the count of pages waiting to be cloned.
	PagesPending int

	// PagesFailed is the count of pages that failed to clone.
	PagesFailed int

	// AssetsDownloaded is the count of successfully downloaded assets.
	AssetsDownloaded int

	// AssetsPending is the count of assets waiting to be downloaded.
	AssetsPending int

	// AssetsFailed is the count of assets that failed to download.
	AssetsFailed int

	// TotalBytes is the cumulative size of cloned content.
	TotalBytes int64
}

// CloneProgress provides real-time progress updates.
type CloneProgress struct {
	// Stats is the current cloning statistics.
	Stats Stats

	// CurrentURL is the URL being processed right now.
	CurrentURL string

	// Message is a human-readable progress message.
	Message string

	// Percentage is the estimated completion percentage (0-100).
	Percentage float64
}