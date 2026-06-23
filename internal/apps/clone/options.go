// Package clone provides website cloning functionality.
// It renders pages in headless Chrome, extracts static content,
// downloads assets, and creates offline-ready HTML directories.
package clone

import (
	"time"
)

// Options defines configuration for website cloning.
type Options struct {
	// MaxPages is the maximum number of pages to clone (0 = unlimited).
	MaxPages int

	// MaxDepth is the maximum link depth to follow (0 = unlimited).
	MaxDepth int

	// Subdomains indicates whether to include subdomains of the seed host.
	Subdomains bool

	// Scroll enables auto-scrolling to trigger lazy-loaded content.
	Scroll bool

	// ScopePrefix limits crawling to paths starting with this prefix.
	ScopePrefix string

	// Exclude lists path prefixes to skip during crawling.
	Exclude []string

	// Refresh indicates whether to re-render existing pages.
	Refresh bool

	// Force indicates whether to delete existing clone before starting.
	Force bool

	// Workers is the number of concurrent page renderers.
	Workers int

	// NoRobots indicates whether to ignore robots.txt rules.
	NoRobots bool

	// Timeout is the maximum time to wait for page rendering.
	Timeout int // seconds

	// OutputDir is the base directory for cloned output.
	OutputDir string

	// ChromePath is the path to Chrome/Chromium executable.
	ChromePath string

	// Incremental enables incremental cloning with ETag/Last-Modified caching.
	Incremental bool

	// CacheMaxAge is the maximum age for cached content before refresh.
	// Only used when Incremental is true. Default is 24 hours.
	CacheMaxAge time.Duration

	// UseCache indicates whether to use existing cache for this clone.
	UseCache bool
}

// DefaultOptions returns default cloning options.
func DefaultOptions() Options {
	return Options{
		MaxPages:    0,    // unlimited
		MaxDepth:    0,    // unlimited
		Subdomains:  false,
		Scroll:      false,
		ScopePrefix: "",
		Exclude:     nil,
		Refresh:     false,
		Force:       false,
		Workers:     4,
		NoRobots:    false,
		Timeout:     30,
		OutputDir:   "",
		ChromePath:  "",
		Incremental: false,
		CacheMaxAge: 24 * time.Hour,
		UseCache:    true,
	}
}

// OptionsBuilder helps construct Options with validation.
type OptionsBuilder struct {
	opts Options
}

// NewOptionsBuilder creates a new OptionsBuilder with defaults.
func NewOptionsBuilder() *OptionsBuilder {
	return &OptionsBuilder{opts: DefaultOptions()}
}

// WithMaxPages sets the maximum pages to clone.
func (b *OptionsBuilder) WithMaxPages(n int) *OptionsBuilder {
	b.opts.MaxPages = n
	return b
}

// WithMaxDepth sets the maximum link depth.
func (b *OptionsBuilder) WithMaxDepth(d int) *OptionsBuilder {
	b.opts.MaxDepth = d
	return b
}

// WithSubdomains enables subdomain inclusion.
func (b *OptionsBuilder) WithSubdomains(enable bool) *OptionsBuilder {
	b.opts.Subdomains = enable
	return b
}

// WithScroll enables auto-scrolling.
func (b *OptionsBuilder) WithScroll(enable bool) *OptionsBuilder {
	b.opts.Scroll = enable
	return b
}

// WithScopePrefix sets the scope prefix for crawling.
func (b *OptionsBuilder) WithScopePrefix(prefix string) *OptionsBuilder {
	b.opts.ScopePrefix = prefix
	return b
}

// WithExclude adds excluded path prefixes.
func (b *OptionsBuilder) WithExclude(paths []string) *OptionsBuilder {
	b.opts.Exclude = paths
	return b
}

// WithRefresh enables re-rendering existing pages.
func (b *OptionsBuilder) WithRefresh(enable bool) *OptionsBuilder {
	b.opts.Refresh = enable
	return b
}

// WithForce enables force deletion of existing clone.
func (b *OptionsBuilder) WithForce(enable bool) *OptionsBuilder {
	b.opts.Force = enable
	return b
}

// WithWorkers sets the number of concurrent workers.
func (b *OptionsBuilder) WithWorkers(n int) *OptionsBuilder {
	b.opts.Workers = n
	return b
}

// WithTimeout sets the rendering timeout in seconds.
func (b *OptionsBuilder) WithTimeout(seconds int) *OptionsBuilder {
	b.opts.Timeout = seconds
	return b
}

// WithOutputDir sets the output directory.
func (b *OptionsBuilder) WithOutputDir(dir string) *OptionsBuilder {
	b.opts.OutputDir = dir
	return b
}

// WithChromePath sets the Chrome executable path.
func (b *OptionsBuilder) WithChromePath(path string) *OptionsBuilder {
	b.opts.ChromePath = path
	return b
}

// WithIncremental enables incremental cloning.
func (b *OptionsBuilder) WithIncremental(enable bool) *OptionsBuilder {
	b.opts.Incremental = enable
	return b
}

// WithCacheMaxAge sets the maximum age for cached content.
func (b *OptionsBuilder) WithCacheMaxAge(age time.Duration) *OptionsBuilder {
	b.opts.CacheMaxAge = age
	return b
}

// WithUseCache enables using existing cache.
func (b *OptionsBuilder) WithUseCache(enable bool) *OptionsBuilder {
	b.opts.UseCache = enable
	return b
}

// Build returns the constructed Options.
func (b *OptionsBuilder) Build() Options {
	return b.opts
}