// Package clone provides website cloning functionality.
//
// enhanced_cloner.go: Improved clone engine integrating browser pool,
// frontier-based resume, robots.txt compliance, sitemap discovery,
// rate limiting, content deduplication, and CSS rewriting.
//
// TODO: API endpoint crawling support
//   - Auto-discover APIs from page JavaScript (fetch/XHR requests)
//   - Support multiple pagination styles: query-param, path-based,
//     header-based (Link/X-Total-Count), body-based (POST JSON),
//     offset/limit, cursor/keyset, seek, token-based
//   - Save raw API responses and optionally render as static HTML
//   - Handle auth tokens and rate limiting for APIs
package clone

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/km269/wukong/internal/apps/sanitize"
	"github.com/km269/wukong/internal/browser"
	"github.com/km269/wukong/internal/browser/antibot"
	"github.com/km269/wukong/internal/browser/antibot/prober"
	"github.com/km269/wukong/internal/browser/types"
	"github.com/km269/wukong/internal/util"
	"github.com/km269/wukong/pkg/httpclient"
	"golang.org/x/net/html"
)

// downloadedAsset records a fetched asset (image, CSS, JS, etc.) cached
// locally during a clone run, keyed by its source URL.
type downloadedAsset struct {
	URL         string
	LocalPath   string
	ContentType string
	Size        int64
	MimeType    string
}

// TraversalMode defines the crawl traversal strategy.
type TraversalMode string

const (
	// TraversalBFS uses breadth-first search: pages are crawled layer by layer.
	TraversalBFS TraversalMode = "bfs"
	// TraversalDFS uses depth-first search: each branch is followed to its
	// maximum depth before backtracking.
	TraversalDFS TraversalMode = "dfs"
)

// EnhancedClonerOptions configures the enhanced cloning engine.
type EnhancedClonerOptions struct {
	// OutputDir is the base directory for cloned output.
	OutputDir string

	// MaxPages limits the total number of pages to clone (0 = unlimited).
	MaxPages int

	// MaxDepth limits the link depth from the seed URL (0 = unlimited).
	MaxDepth int

	// Traversal sets the crawl strategy: "bfs" (breadth-first) or "dfs"
	// (depth-first). Default is "bfs".
	Traversal TraversalMode

	// Subdomains includes subdomains of the seed host in scope.
	Subdomains bool

	// Scroll enables auto-scrolling to trigger lazy loading.
	Scroll bool

	// ScopePrefix restricts crawling to paths starting with this prefix.
	ScopePrefix string
	// ScopeAnchor restricts crawling to pages with a specific URL fragment/anchor.
	ScopeAnchor string

	// Exclude lists path prefixes to skip.
	Exclude []string

	// Refresh re-renders all pages even if they exist locally.
	Refresh bool

	// Force deletes existing clone data and starts fresh.
	Force bool

	// Workers is the number of concurrent page renderers.
	Workers int

	// AssetWorkers is the number of concurrent asset downloaders.
	AssetWorkers int

	// BrowserPages is the Chrome tab pool size (0 = same as Workers).
	// Separate from Workers to allow different rendering concurrency
	// vs browser resource usage.
	BrowserPages int

	// RespectRobots controls whether to obey robots.txt rules.
	RespectRobots bool

	// CrawlDelay overrides the robots.txt crawl-delay.
	CrawlDelay time.Duration

	// NoSitemap disables sitemap-based URL discovery.
	NoSitemap bool

	// EnableResume saves frontier state for resuming interrupted crawls.
	EnableResume bool

	// Persist controls whether frontier state is written to disk on
	// completion (default true). When false, state exists only in memory
	// during the run — useful for privacy-sensitive or one-shot clones.
	Persist bool

	// DedupContent enables SHA-256 content deduplication with hard links.
	DedupContent bool

	// MobileReadable injects responsive CSS for mobile viewing.
	MobileReadable bool

	// Stealth injects anti-detection scripts to hide automation.
	Stealth bool

	// Incremental enables ETag/Last-Modified caching to skip unchanged pages.
	Incremental bool

	// CacheMaxAge is the maximum age of cached content before it's considered
	// stale. Only used when Incremental is true. Default is 24 hours.
	CacheMaxAge time.Duration

	// Timeout is the per-page HTTP request timeout.
	Timeout time.Duration

	// RenderTimeout is the hard timeout for a single page render.
	// Default is 30s. Separate from Timeout to allow generous HTTP
	// timeouts without blocking slow-rendering pages indefinitely.
	RenderTimeout time.Duration

	// Settle is the network-idle quiet period before snapshotting the DOM.
	// Default is 1500ms.
	Settle time.Duration

	// ChromePath is the path to the Chrome/Chromium executable.
	ChromePath string

	// ChromeProfile is the Chrome user-data-dir for persistent browser
	// profile. Cookies, localStorage, and solved Cloudflare challenges
	// are saved between runs. Combine with Headless=false for Turnstile.
	ChromeProfile string

	// BrowserBackend selects the browser backend: "chromedp" or "rod".
	// Default is "rod".
	BrowserBackend browser.BackendType

	// Headless controls headless mode (default true). Set to false to
	// show a visible Chrome window — essential for solving interactive
	// challenges like Cloudflare Turnstile once, then reusing the
	// solved session via ChromeProfile.
	Headless bool

	// UserAgent is the User-Agent header for HTTP requests.
	UserAgent string

	// AssetSameDomain only downloads assets from the same registrable domain
	// as the seed URL. Assets on external domains (CDNs, analytics) are left
	// as live links. Default is true.
	AssetSameDomain bool

	// AssetDomains is a list of additional domain names whose assets should
	// be downloaded even when AssetSameDomain is true. This allows listing
	// known CDNs or content hosts that serve images, fonts, or CSS needed
	// for the page to render correctly. Example: []string{"media.example.com", "cdn.example.com"}
	AssetDomains []string

	// SkipAssetExts is a set of file extensions that should NOT be downloaded.
	// Assets matching these extensions keep their original remote URLs.
	// Typical values: .mp4, .pdf, .zip, .exe, etc.
	SkipAssetExts map[string]bool

	// MaxAssetBytes is the maximum size in bytes for a single asset download.
	// Assets exceeding this limit are left as live links. Default is 50MB.
	MaxAssetBytes int64

	// AntibotEnabled enables automatic anti-bot detection and response.
	// When a page or asset is blocked (403, Cloudflare challenge, CAPTCHA),
	// the engine detects the pattern and auto-escalates stealth measures.
	// Default is true.
	AntibotEnabled bool

	// AntibotAutoEscalate enables automatic escalation on detection.
	// When false, blocks are detected and logged but the engine does NOT
	// change stealth levels automatically. Default is true.
	AntibotAutoEscalate bool

	// CookieFile is the path to a Netscape-format cookie file for
	// authenticated cloning. Cookies are loaded at startup and saved
	// on completion, enabling repeat cloning of login-protected sites.
	// Empty string = no cookie persistence.
	CookieFile string

	// DisableDownloads prevents the browser from auto-downloading files
	// triggered by page navigation or JavaScript. This avoids polluting
	// the working directory with unwanted files (PDFs, installers, etc.)
	// when cloning pages that contain download links or auto-download
	// scripts. The cloner's asset downloader still fetches needed assets
	// via HTTP. Default is true.
	DisableDownloads bool

	// BehaviorSimulation enables human-like behavior simulation (random mouse move, scroll, etc.).
	// This makes automation less detectable by anti-bot systems. Default is false.
	BehaviorSimulation bool

	// ProxyConfig configures proxy settings for browser automation.
	ProxyEnabled     bool
	ProxyPool        []string
	ProxyRotateEvery int
}

// DefaultSkipAssetExts returns the default set of file extensions that should
// not be downloaded (media, archives, documents). These assets remain as live
// links in the cloned output.
func DefaultSkipAssetExts() map[string]bool {
	return map[string]bool{
		// Video
		".mp4": true, ".m4v": true, ".webm": true, ".avi": true,
		".mov": true, ".mkv": true, ".wmv": true, ".flv": true,
		".m3u8": true, ".ts": true,
		// Audio
		".mp3": true, ".wav": true, ".ogg": true, ".flac": true,
		".aac": true, ".m4a": true, ".wma": true, ".oga": true,
		// Documents
		".pdf": true, ".doc": true, ".docx": true, ".xls": true,
		".xlsx": true, ".ppt": true, ".pptx": true,
		// Archives
		".zip": true, ".tar": true, ".gz": true, ".bz2": true,
		".7z": true, ".rar": true, ".tgz": true, ".xz": true,
		".dmg": true, ".iso": true,
		// Installers / packages
		".exe": true, ".msi": true, ".apk": true,
		".pkg": true, ".deb": true, ".rpm": true, ".appimage": true,
	}
}

// DefaultEnhancedOptions returns sensible defaults.
func DefaultEnhancedOptions() EnhancedClonerOptions {
	home, _ := os.UserHomeDir()
	outputDir := filepath.Join(home, ".wukong", "apps", "cloned")

	return EnhancedClonerOptions{
		OutputDir:           outputDir,
		Workers:             4,
		AssetWorkers:        12,
		BrowserPages:        6,
		Timeout:             120 * time.Second,
		RenderTimeout:       120 * time.Second,
		Settle:              5000 * time.Millisecond,
		Traversal:           TraversalBFS,
		RespectRobots:       false,
		EnableResume:        true,
		Persist:             true,
		DedupContent:        true,
		MobileReadable:      true,
		AssetSameDomain:     true,
		SkipAssetExts:       DefaultSkipAssetExts(),
		MaxAssetBytes:       50 * 1024 * 1024, // 50 MB.
		AntibotEnabled:      true,
		AntibotAutoEscalate: true,
		Incremental:         true,
		CacheMaxAge:         24 * time.Hour,
		Headless:            true, // Default: headless Chrome.
		Stealth:             true, // Default: anti-detection active.
		ChromePath:          "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
		ChromeProfile:       "",
		DisableDownloads:    true,  // Default: 禁止浏览器自动下载.
		BehaviorSimulation:  false, // Default: 不启用人类行为模拟.
		ProxyEnabled:        false,
		ProxyRotateEvery:    10,
		UserAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
			"AppleWebKit/537.36 (KHTML, like Gecko) " +
			"Chrome/124.0.0.0 Safari/537.36",
	}
}

// EnhancedCloner is the improved website cloning engine.
type EnhancedCloner struct {
	opts       EnhancedClonerOptions
	seedURL    string
	host       string
	scheme     string
	proxyIndex int
	ctx        context.Context

	// Browser pool for rendering pages.
	browserPool types.BrowserBackend

	// Frontier for URL deduplication and resume.
	front *frontier

	// Asset downloader for static resources.
	assetDownloader *AssetDownloader

	// Content deduper for saving disk space.
	deduper *ContentDeduper

	// Incremental cache for ETag/Last-Modified checks.
	cache *CloneCache

	// HTTP client for non-browser requests.
	httpClient *http.Client

	// robots.txt rules.
	robots *RobotsRule

	// Rate limiter for polite crawling.
	rateLimiter *RateLimiter

	// Anti-bot detection and auto-escalation engine.
	antibot *antibot.Engine

	// preflightTurnstile is set true when the seed URL returns a Cloudflare
	// Turnstile challenge page — a JS-interactive challenge that headless
	// Chrome cannot pass. When true, the retry loop is skipped.
	preflightTurnstile bool

	// cfClearance is the Cloudflare bypass token extracted from Chrome
	// after a successful page render. When present, all subsequent
	// HTTP requests (preflight, assets) include it to skip challenges.
	cfClearance string

	// Page and asset job queues.
	pageJobs  chan pageJob
	assetJobs chan assetJob

	// DFS traversal support: page stack + dispatcher.
	pageStack      []pageJob // DFS LIFO stack.
	pageMu         sync.Mutex
	pageReady      chan struct{} // Signal when new pages are available.
	dispatcherStop chan struct{} // Signal to stop the dispatcher.

	// Downloaded assets registry.
	downloadedAssets map[string]*downloadedAsset
	assetMu          sync.RWMutex

	// Stats and results.
	stats     Stats
	statsMu   sync.RWMutex
	results   []PageResult
	resultsMu sync.Mutex

	// Directories.
	pageDir  string
	assetDir string

	// Wait group for tracking active jobs.
	wg sync.WaitGroup

	// Track enqueued count for MaxPages limit.
	enqueuedPages int
	enqueuedMu    sync.Mutex
}

// pageJob represents a pending page rendering job.
type pageJob struct {
	url     string
	depth   int
	referer string
	inScope bool
}

// assetJob represents a pending asset download job.
type assetJob struct {
	url string
}

// NewEnhancedCloner creates a new enhanced cloning engine.
func NewEnhancedCloner(opts EnhancedClonerOptions) *EnhancedCloner {
	if util.DebugEnabled {
		fmt.Fprintf(os.Stderr, "[DEBUG] NewEnhancedCloner called\n")
	}
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	if opts.AssetWorkers <= 0 {
		opts.AssetWorkers = opts.Workers
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 60 * time.Second
	}
	if opts.UserAgent == "" {
		opts.UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
			"AppleWebKit/537.36 (KHTML, like Gecko) " +
			"Chrome/124.0.0.0 Safari/537.36"
	}

	dl := DefaultAssetDownloader()
	dl.UserAgent = opts.UserAgent
	if opts.MaxAssetBytes > 0 {
		dl.MaxBytes = opts.MaxAssetBytes
	}
	if opts.ProxyEnabled && len(opts.ProxyPool) > 0 {
		dl.ProxyPool = opts.ProxyPool
		if util.DebugEnabled {
			fmt.Fprintf(os.Stderr, "[wukong/clone] asset downloader using proxy pool (%d proxies)\n", len(opts.ProxyPool))
		}
	}

	return &EnhancedCloner{
		opts:             opts,
		front:            newFrontier(),
		assetDownloader:  dl,
		deduper:          NewContentDeduper(!opts.DedupContent),
		downloadedAssets: make(map[string]*downloadedAsset),
		pageReady:        make(chan struct{}, 1),
		dispatcherStop:   make(chan struct{}),
		pageStack:        make([]pageJob, 0),
	}
}

// Clone performs the website cloning operation.
func (ec *EnhancedCloner) Clone(ctx context.Context, seedURL string) (*Result, error) {
	startTime := time.Now()
	ec.ctx = ctx

	// Parse and normalize seed URL.
	parsedURL, err := url.Parse(seedURL)
	if err != nil {
		return nil, fmt.Errorf("parse seed URL: %w", err)
	}

	if parsedURL.Scheme == "" {
		parsedURL.Scheme = "https"
		seedURL = "https://" + seedURL
	}
	ec.scheme = parsedURL.Scheme
	ec.host = parsedURL.Host
	ec.seedURL = seedURL

	// Set the default Referer for asset downloads to the seed URL.
	// This helps bypass anti-hotlink protection on CDNs and media servers
	// that check the Referer header (e.g., media.defense.gov).
	ec.assetDownloader.Referer = seedURL

	// Configure domain-specific Referer overrides for known cross-domain
	// media/CDN servers that validate the Referer header.
	// Using the seed URL as the Referer makes requests appear as if
	// they were triggered by the main site, which many CDNs require.
	ec.assetDownloader.RefererOverrides = map[string]string{
		// U.S. Department of Defense media server — strict referer checking
		"media.defense.gov": seedURL,
		".defense.gov":      seedURL,
		// Common CDNs that sometimes check referer
		".cloudfront.net":  seedURL,
		".akamai.net":      seedURL,
		".akamaized.net":   seedURL,
		".fastly.net":      seedURL,
		".edgecastcdn.net": seedURL,
		".hwcdn.net":       seedURL,
	}

	// Pre-flight Cloudflare detection.
	// Before starting headless Chrome, check if the site uses Cloudflare
	// anti-bot protection. If so, enable Stealth pre-emptively so the
	// first page load is already stealth-protected.
	if util.DebugEnabled {
		fmt.Fprintf(os.Stderr, "[DEBUG] Starting preflight Cloudflare check...\n")
	}
	ec.preflightCloudflareCheck()
	if util.DebugEnabled {
		fmt.Fprintf(os.Stderr, "[DEBUG] Preflight Cloudflare check completed\n")
	}

	// Multi-dimensional anti-bot probing.
	// Probes HTTP headers, robots.txt, WAF fingerprint, JS challenges,
	// and rate limits to build an anti-bot profile and adjust strategy.
	if ec.opts.AntibotEnabled && !ec.opts.Stealth {
		if util.DebugEnabled {
			fmt.Fprintf(os.Stderr, "[DEBUG] Starting antibot probe...\n")
		}
		ec.runAntibotProbe(ctx)
		if util.DebugEnabled {
			fmt.Fprintf(os.Stderr, "[DEBUG] Antibot probe completed\n")
		}
	}

	// Set up output directory.
	outputDir := ec.opts.OutputDir
	if outputDir == "" {
		homeDir, _ := os.UserHomeDir()
		outputDir = filepath.Join(homeDir, ".wukong_apps", "cloned", ec.host)
	}
	ec.opts.OutputDir = outputDir

	ec.pageDir = filepath.Join(outputDir, "pages")
	ec.assetDir = filepath.Join(outputDir, "assets")

	// Force clean if requested.
	if ec.opts.Force {
		os.RemoveAll(outputDir)
	}

	// Initialize incremental cache.
	if ec.opts.Incremental {
		cache, err := NewCloneCache("", ec.host)
		if err != nil {
			// Non-fatal: continue without cache.
			ec.cache = nil
		} else {
			cache.SetSeedURL(seedURL)
			if ec.opts.Force {
				cache.Clear()
			}
			ec.cache = cache
		}
	}

	// Create directories.
	for _, d := range []string{ec.pageDir, ec.assetDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return nil, fmt.Errorf("create directory %s: %w", d, err)
		}
	}

	// Initialize cookie session for authenticated cloning.
	var cloneSess *CloneSession
	if ec.opts.CookieFile != "" {
		sess, err := NewCloneSession(ec.opts.CookieFile)
		if err != nil {
			fmt.Fprintf(os.Stderr,
				"[wukong/session] cookie load failed: %v\n", err)
		} else {
			cloneSess = sess
			fmt.Fprintf(os.Stderr,
				"[wukong/session] cookies loaded from %s\n",
				ec.opts.CookieFile)
		}
	}

	// Initialize HTTP client (with cookie jar if session is active).
	if cloneSess != nil {
		ec.httpClient = cloneSess.HTTPClient()
		// Share the cookie-jar-equipped client with the asset downloader
		// so authenticated assets (behind login walls) are also fetched
		// with the user's session cookies.
		ec.assetDownloader.Client = cloneSess.HTTPClient()
	} else {
		ec.httpClient = &http.Client{
			Timeout: 60 * time.Second,
			CheckRedirect: func(req *http.Request,
				via []*http.Request) error {
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		}
	}

	// Load robots.txt.
	if ec.opts.RespectRobots {
		robots, err := FetchRobots(ctx, ec.httpClient, ec.host, ec.scheme)
		if err != nil {
			// Non-fatal: continue without robots.txt.
		} else {
			ec.robots = robots
			// Set up rate limiting from crawl-delay.
			if ec.opts.CrawlDelay > 0 {
				ec.rateLimiter = NewRateLimiter(ec.opts.CrawlDelay)
			} else {
				ec.rateLimiter = NewRateLimiterFromCrawlDelay(robots.CrawlDelayDuration())
			}
		}
	} else {
		ec.rateLimiter = NewRateLimiter(500 * time.Millisecond)
	}

	// Resume from previous state.
	if ec.opts.EnableResume && !ec.opts.Refresh {
		statePath := frontierStatePath(outputDir)
		if err := ec.front.load(statePath); err == nil {
			seenCount := ec.front.seenCount()
			if seenCount > 0 {
				fmt.Fprintf(os.Stderr, "  Resuming previous crawl (%d pages seen) ...\n", seenCount)
			}
		}
	}

	// Create browser backend with smart proxy pool.
	var proxy string
	if ec.opts.ProxyEnabled && len(ec.opts.ProxyPool) > 0 {
		if browser.GlobalProxyPool() == nil {
			browser.InitGlobalProxyPool(ec.opts.ProxyPool, 30*time.Second)
		}
		proxy = browser.GlobalProxyPool().GetProxy()
	}
	fmt.Fprintf(os.Stderr, "  Launching browser (%s) ...\n", ec.opts.BrowserBackend)
	if util.DebugEnabled {
		fmt.Fprintf(os.Stderr, "[DEBUG] Creating browser backend...\n")
	}
	browserBackend := browser.NewBackend(ec.opts.BrowserBackend, browser.BackendOptions{
		Headless:         ec.opts.Headless,
		Workers:          ec.opts.Workers,
		Settle:           ec.opts.Settle,
		RenderTimeout:    ec.opts.RenderTimeout,
		Scroll:           ec.opts.Scroll,
		ChromeBin:        ec.opts.ChromePath,
		Stealth:          ec.opts.Stealth,
		ProfileDir:       ec.opts.ChromeProfile,
		DisableDownloads: ec.opts.DisableDownloads,
		Proxy:            proxy,
	})
	ec.browserPool = browserBackend
	defer browserBackend.Close()
	fmt.Fprintf(os.Stderr, "  Browser ready. Starting crawl ...\n")

	// Enable human-like behavior simulation if configured
	if ec.opts.BehaviorSimulation {
		browserBackend.SetBehaviorSimulation(true)
	}

	// Initialize anti-bot detection and auto-escalation engine.
	abCfg := antibot.Config{
		Enabled:      ec.opts.AntibotEnabled,
		AutoEscalate: ec.opts.AntibotAutoEscalate,
		InitialLevel: antibot.LevelNone,
		MaxLevel:     antibot.LevelAggressive,
		MaxRetries:   5,
		Cooldown:     45 * time.Second,
	}
	if ec.opts.Stealth {
		abCfg.InitialLevel = antibot.LevelStealth
	} else {
		abCfg.InitialLevel = antibot.LevelFlags
	}
	ec.antibot = antibot.New(abCfg)

	// Create job channels.
	// Buffered channels prevent deadlock when workers discover new pages
	// and try to enqueue them while all workers are busy processing.
	ec.pageJobs = make(chan pageJob, ec.opts.Workers*8)
	ec.assetJobs = make(chan assetJob, ec.opts.AssetWorkers*64)

	// Start traversal dispatcher: feeds pages to workers according to the
	// selected traversal strategy (FIFO for BFS, LIFO for DFS).
	go ec.traversalDispatcher(ctx)

	// Start page workers.
	for i := 0; i < ec.opts.Workers; i++ {
		go ec.pageWorker(ctx, i)
	}

	// Start asset workers.
	for i := 0; i < ec.opts.AssetWorkers; i++ {
		go ec.assetWorker(ctx, i)
	}

	// Enqueue seed URL.
	ec.enqueuePage(seedURL, 0)

	// When scope-prefix is specified but seed URL doesn't match it,
	// automatically add the scope-prefix root as an additional starting point.
	// This ensures the crawl targets the intended scope even if the seed page
	// doesn't contain links to it.
	if ec.opts.ScopePrefix != "" {
		seed, _ := url.Parse(seedURL)
		if seed != nil && !matchesScopePrefix(seed.Path, ec.opts.ScopePrefix) {
			scopeURL := fmt.Sprintf("%s://%s%s", seed.Scheme, seed.Host, ec.opts.ScopePrefix)
			ec.enqueuePage(scopeURL, 0)
		}
	}

	// Discover sitemaps for additional seeds.
	if !ec.opts.NoSitemap && ec.robots != nil && len(ec.robots.Sitemaps) > 0 {
		sitemapURLs, err := FetchSitemaps(ctx, ec.httpClient, ec.robots.Sitemaps)
		if err == nil {
			for _, su := range sitemapURLs {
				// Parse and validate.
				parsed, err := url.Parse(su)
				if err != nil {
					continue
				}
				// Only enqueue same-site URLs.
				if parsed.Host != ec.host && !ec.opts.Subdomains {
					continue
				}
				ec.enqueuePage(su, 1)
			}
		}
	}

	// Wait for all jobs to complete.
	ec.wg.Wait()

	// Signal the DFS dispatcher to stop (avoids goroutine leak).
	close(ec.dispatcherStop)

	// Close channels and wait for workers to drain.
	close(ec.pageJobs)
	close(ec.assetJobs)

	// Save incremental cache.
	if ec.cache != nil {
		ec.cache.UpdateLastSync()
		if err := ec.cache.Save(); err != nil {
			// Non-fatal.
		}
	}

	// Save frontier state (only if both resume AND persist are enabled).
	if ec.opts.EnableResume && ec.opts.Persist {
		statePath := frontierStatePath(outputDir)
		if err := ec.front.save(statePath); err != nil {
			// Non-fatal.
		}
	}

	// Save session cookies for authenticated cloning.
	if cloneSess != nil {
		if err := cloneSess.Save(); err != nil {
			fmt.Fprintf(os.Stderr,
				"[wukong/session] cookie save failed: %v\n", err)
		}
	}

	endTime := time.Now()

	// Build result.
	ec.statsMu.RLock()
	ec.resultsMu.Lock()
	dedupFiles, dedupSaved := ec.deduper.Savings()
	result := &Result{
		Success:           ec.stats.PagesFailed == 0,
		SeedURL:           ec.seedURL,
		Host:              ec.host,
		OutputDir:         outputDir,
		Pages:             ec.stats.PagesCloned,
		Assets:            ec.stats.AssetsDownloaded,
		SizeBytes:         ec.stats.TotalBytes,
		Duration:          endTime.Sub(startTime),
		DedupFiles:        dedupFiles,
		DedupBytesSaved:   dedupSaved,
		AntibotDetections: len(ec.antibot.Escalator.History),
		AntibotStats:      ec.antibot.Stats(),
		StartTime:         startTime,
		EndTime:           endTime,
	}

	for _, pr := range ec.results {
		if pr.Error != "" {
			result.Errors = append(result.Errors,
				fmt.Sprintf("%s: %s", pr.URL, pr.Error))
		}
	}
	ec.resultsMu.Unlock()
	ec.statsMu.RUnlock()

	return result, nil
}

// ---------------------------------------------------------------------------
// Worker goroutines.
// ---------------------------------------------------------------------------

// pageWorker processes pages from the page job channel.
func (ec *EnhancedCloner) pageWorker(ctx context.Context, id int) {
	for job := range ec.pageJobs {
		// Check for cancellation BEFORE processing.
		select {
		case <-ctx.Done():
			ec.wg.Done()
			return
		default:
		}

		result := ec.processPage(ctx, job.url, job.depth, job.referer, job.inScope)

		// Update stats.
		ec.statsMu.Lock()
		if result.Error == "" {
			ec.stats.PagesCloned++
			ec.stats.TotalBytes += result.Size
		} else {
			ec.stats.PagesFailed++
		}
		ec.statsMu.Unlock()

		ec.resultsMu.Lock()
		ec.results = append(ec.results, result)
		ec.resultsMu.Unlock()

		// Done AFTER processing, so wg.Wait() correctly waits for completion.
		ec.wg.Done()
	}
}

// assetWorker processes asset downloads from the asset job channel.
func (ec *EnhancedCloner) assetWorker(ctx context.Context, id int) {
	for job := range ec.assetJobs {
		select {
		case <-ctx.Done():
			ec.wg.Done()
			return
		default:
		}

		if err := ec.processAsset(ctx, job.url); err != nil {
			ec.statsMu.Lock()
			ec.stats.PagesFailed++
			ec.statsMu.Unlock()
		}

		ec.wg.Done()
	}
}

func (ec *EnhancedCloner) enqueueAssetNonBlocking(assetURL string) {
	ec.wg.Add(1)
	select {
	case ec.assetJobs <- assetJob{url: assetURL}:
	default:
		go func() {
			ec.processAsset(ec.ctx, assetURL)
			ec.wg.Done()
		}()
	}
}

// ---------------------------------------------------------------------------
// Page processing.
// ---------------------------------------------------------------------------

// processPage renders and saves a single page.
// Uses a single-pass DOM walk (sink callback) to simultaneously rewrite links
// and discover new pages/assets — eliminating the separate extract+rewrite
// two-pass approach.
func (ec *EnhancedCloner) processPage(ctx context.Context, pageURL string, depth int, referer string, inScope bool) PageResult {
	result := PageResult{
		URL:   pageURL,
		Depth: depth,
	}

	// Incremental cache check: skip rendering if page is unchanged.
	if ec.cache != nil && !ec.opts.Refresh {
		needsUpdate, reason, err := ec.cache.CheckNeedsUpdate(pageURL)
		if err == nil && !needsUpdate {
			// Page unchanged: reuse cached content.
			return ec.useCachedPage(pageURL, reason)
		}
		_ = err // If HEAD request fails, fall through to full render.
	}

	// Robots.txt check.
	if ec.robots != nil {
		parsed, err := url.Parse(pageURL)
		if err == nil {
			if !ec.robots.IsAllowed(parsed.Path) {
				result.Error = "blocked by robots.txt"
				return result
			}
		}
	}

	// Rate limiting.
	if ec.rateLimiter != nil {
		if err := ec.rateLimiter.Wait(ctx); err != nil {
			result.Error = fmt.Sprintf("rate limit: %v", err)
			return result
		}
	}

	// Anti-bot jitter delay (randomised pause for aggressive levels).
	ec.antibot.Wait()

	// Render page in headless Chrome.
	if util.DebugEnabled {
		fmt.Fprintf(os.Stderr, "[wukong/clone] rendering %s ...\n", pageURL)
	}
	renderResult, err := ec.browserPool.RenderWithReferer(ctx, pageURL, referer)
	if err != nil {
		// Non-HTML resource? Route to asset downloader instead of failing.
		if _, ok := errors.AsType[*types.ErrNotHTML](err); ok {
			ec.wg.Add(1)
			ec.assetJobs <- assetJob{url: pageURL}
			ec.front.markVisited(PageKey(ec.host, pageURL))
			return result
		}

		// Check if the error indicates anti-bot blocking.
		if reason, _ := ec.antibot.CheckError(err); reason != antibot.ReasonNone {
			retry, delay, _, msg := ec.antibot.Escalate(pageURL, reason, 0)
			fmt.Fprintf(os.Stderr, "[wukong/antibot] %s\n", msg)
			ec.applyAntiBotLevel()
			if retry {
				select {
				case <-time.After(delay):
					// Re-enqueue for retry with escalated level.
					ec.enqueuePageWithReferer(pageURL, depth, referer, inScope)
					return result
				case <-ctx.Done():
					result.Error = "cancelled during anti-bot backoff"
					return result
				}
			}
			result.Error = fmt.Sprintf("render: %v", err)
			return result
		}
		result.Error = fmt.Sprintf("render: %v", err)
		return result
	}

	// Extract Cloudflare bypass token (cf_clearance cookie) from Chrome.
	// If obtained, all subsequent HTTP requests include it to skip
	// Cloudflare challenges for their TTL (~30 min).
	if renderResult.CloudflareClearance != "" {
		ec.cfClearance = renderResult.CloudflareClearance
		ec.assetDownloader.cfClearance = renderResult.CloudflareClearance
		fmt.Fprintf(os.Stderr,
			"[wukong/antibot] cf_clearance obtained — "+
				"Cloudflare bypass active for subsequent requests\n")
	}

	// Save assets collected from the browser's network stack during rendering.
	// These are already downloaded by the browser, so we save them directly
	// instead of re-downloading via HTTP (which may fail due to DNS issues).
	if renderResult.CollectedAssets != nil && len(renderResult.CollectedAssets) > 0 {
		saved := 0
		for _, asset := range renderResult.CollectedAssets {
			if asset == nil || len(asset.Body) == 0 {
				continue
			}
			if ec.saveCollectedAsset(ctx, asset) {
				saved++
			}
		}
		if util.DebugEnabled && saved > 0 {
			fmt.Fprintf(os.Stderr, "[wukong/clone] saved %d assets from browser cache\n", saved)
		}
	}

	// Detect anti-bot patterns in the rendered page.
	abReason, abDesc := ec.antibot.CheckResponse(
		200, nil, renderResult.HTML)
	if abReason != antibot.ReasonNone {
		fmt.Fprintf(os.Stderr, "[wukong/antibot] %s at %s\n", abDesc, pageURL)

		// Cloudflare Turnstile: in non-headless + stealth mode,
		// Chrome can auto-solve simple Turnstile challenges
		// (checkbox "Verify you are human"). Re-render with
		// extended settle to give the challenge time to complete.
		// (Scrapling's solve_cloudflare=True approach.)
		if abReason == antibot.ReasonCloudflare {
			if !ec.opts.Headless && ec.opts.Stealth {
				fmt.Fprintf(os.Stderr,
					"[wukong/antibot] Turnstile detected — "+
						"attempting auto-solve (non-headless+"+
						"stealth, extended settle)...\n")
				ec.browserPool.SetSettle(10 * time.Second)
				rr2, rErr := ec.browserPool.RenderWithReferer(ctx, pageURL, referer)
				ec.browserPool.SetSettle(ec.opts.Settle)
				if rErr == nil {
					ab2, _ := ec.antibot.CheckResponse(
						200, nil, rr2.HTML)
					if ab2 == antibot.ReasonNone {
						// Challenge passed — use re-rendered page.
						fmt.Fprintf(os.Stderr,
							"[wukong/antibot] Turnstile solved! "+
								"continuing with real page.\n")
						renderResult = rr2
						goto processContent
					}
				}
				fmt.Fprintf(os.Stderr,
					"[wukong/antibot] Turnstile auto-solve failed. "+
						"Try manually in visible Chrome.\n")
			}
			ec.opts.AntibotAutoEscalate = false
			result.Error = "Cloudflare Turnstile blocked — " +
				"headless Chrome cannot pass interactive JS " +
				"challenges. Run: --no-headless " +
				"--chrome-profile ./cf_data"
			return result
		}

		retry, delay, _, msg := ec.antibot.Escalate(
			pageURL, abReason, 200)
		ec.applyAntiBotLevel()

		if retry {
			fmt.Fprintf(os.Stderr, "[wukong/antibot] %s\n", msg)
			select {
			case <-time.After(delay):
				ec.enqueuePageWithReferer(pageURL, depth, referer, inScope)
				return result
			case <-ctx.Done():
				result.Error = "cancelled during anti-bot backoff"
				return result
			}
		}
		// If not retrying, continue but record the detection.
		result.Error = "anti-bot page detected: " + abDesc
		return result
	}

	// Process page content after optional Turnstile auto-solve.
processContent:

	// Clean HTML with enhanced sanitization.
	cleanOpts := sanitize.CleanOptions{
		KeepNoscript:    false,
		KeepMetaRefresh: false,
		MobileReadable:  ec.opts.MobileReadable,
		Banner: fmt.Sprintf("Cloned by Wukong from %s on %s",
			pageURL, time.Now().Format(time.RFC3339)),
	}
	cleanHTML, _ := sanitize.CleanHTMLWithOptions(renderResult.HTML, cleanOpts)

	// SPA salvage: if the rendered DOM is nearly empty AND the above
	// antibot check found no blocking, the site is likely a SPA that
	// needs more settle time for async-loaded content.
	if len(cleanHTML) < 300 && abReason == antibot.ReasonNone {
		fmt.Fprintf(os.Stderr,
			"[wukong/sanitize] %s: %d bytes — SPA? "+
				"re-rendering with extended settle (5s)...\n",
			pageURL, len(cleanHTML))

		// Temporarily increase settle and re-render.
		ec.browserPool.SetSettle(5 * time.Second)
		renderResult2, rErr := ec.browserPool.RenderWithReferer(ctx, pageURL, referer)
		ec.browserPool.SetSettle(ec.opts.Settle) // Restore original.
		if rErr == nil {
			cleanHTML2, _ := sanitize.CleanHTMLWithOptions(
				renderResult2.HTML, cleanOpts)
			if len(cleanHTML2) > len(cleanHTML) {
				fmt.Fprintf(os.Stderr,
					"[wukong/sanitize] SPA re-render: %d → %d bytes\n",
					len(cleanHTML), len(cleanHTML2))
				cleanHTML = cleanHTML2
				renderResult = renderResult2
			}
		}
	}

	// Determine local file path using deterministic mapping.
	localRelPath := PageKey(ec.host, pageURL)
	fullPath := filepath.Join(ec.pageDir, localRelPath)

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		result.Error = fmt.Sprintf("mkdir: %v", err)
		return result
	}

	// Single-pass DOM walk: rewrite links AND discover pages/assets.
	// This merges the former extractLinks + extractAssets + rewritePageLinks
	// into one traversal, using a sink callback.
	pageMirrorPath := filepath.ToSlash(filepath.Join("pages", localRelPath))
	rewrittenHTML := ec.rewriteAndDiscover(cleanHTML, pageURL, pageMirrorPath,
		depth, inScope, &result.LinksFound, &result.AssetsFound)

	// Process links extracted from the browser's rendered DOM.
	// This captures dynamically generated links that static HTML parsing may miss.
	if len(renderResult.ExtractedLinks) > 0 {
		if util.DebugEnabled {
			fmt.Fprintf(os.Stderr, "\n[wukong/clone] browser extracted %d links from %s\n",
				len(renderResult.ExtractedLinks), pageURL)
		}
		browserLinks := ec.processBrowserExtractedLinks(
			renderResult.ExtractedLinks, pageURL, depth, inScope)
		result.LinksFound += browserLinks
		if util.DebugEnabled {
			fmt.Fprintf(os.Stderr, "[wukong/clone] %d new pages enqueued from browser links\n",
				browserLinks)
		}
	} else if util.DebugEnabled {
		fmt.Fprintf(os.Stderr, "\n[wukong/clone] browser extracted 0 links from %s\n", pageURL)
	}

	contentBytes := []byte(rewrittenHTML)

	// Content dedup: if identical content exists, create hard link.
	deduped, err := ec.deduper.TryDedup(contentBytes, fullPath)
	if err != nil {
		// Fallback: write directly on dedup error.
		if writeErr := os.WriteFile(fullPath, contentBytes, 0644); writeErr != nil {
			result.Error = fmt.Sprintf("write: %v", writeErr)
			return result
		}
		ec.deduper.MarkWritten(contentBytes, fullPath)
	} else if deduped {
		// Successfully hard-linked to existing content.
		result.FilePath = fullPath
		result.Title = renderResult.Title
		result.Size = int64(len(contentBytes))
		return result
	} else {
		// New content, write to disk.
		if writeErr := os.WriteFile(fullPath, contentBytes, 0644); writeErr != nil {
			result.Error = fmt.Sprintf("write: %v", writeErr)
			return result
		}
		ec.deduper.MarkWritten(contentBytes, fullPath)
	}

	result.FilePath = fullPath
	result.Title = renderResult.Title
	result.Size = int64(len(rewrittenHTML))

	ec.statsMu.RLock()
	cloned := ec.stats.PagesCloned + 1
	failed := ec.stats.PagesFailed
	ec.statsMu.RUnlock()
	seenCount := ec.front.seenCount()
	pending := seenCount - cloned - failed
	fmt.Fprintf(os.Stderr, "\r  [wukong/clone] %d pages cloned, %d pending, %d failed ...", cloned, pending, failed)
	if util.DebugEnabled {
		fmt.Fprintf(os.Stderr, "\n  saved %s (%d bytes)", pageURL, result.Size)
	}

	// Save to incremental cache for future runs.
	ec.updateCacheEntry(pageURL, fullPath, contentBytes)

	// Mark as visited.
	ec.front.markVisited(localRelPath)

	return result
}

// ---------------------------------------------------------------------------
// Asset processing.
// ---------------------------------------------------------------------------

// saveCollectedAsset saves an asset that was already downloaded by the browser
// during page rendering. Returns true if the asset was newly saved.
func (ec *EnhancedCloner) saveCollectedAsset(ctx context.Context, asset *types.CollectedAsset) bool {
	assetURL := asset.URL

	ec.assetMu.RLock()
	if _, exists := ec.downloadedAssets[assetURL]; exists {
		ec.assetMu.RUnlock()
		return false
	}
	ec.assetMu.RUnlock()

	key := AssetKey(assetURL)

	// Determine local path.
	localPath := LocalPath(ec.host, assetURL, KindAsset)
	fullPath := filepath.Join(ec.assetDir, localPath)

	// Ensure directory.
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		ec.front.markVisited(key)
		return false
	}

	contentType := asset.ContentType
	isCSS := isCSSContentType(contentType) ||
		strings.HasSuffix(strings.ToLower(assetURL), ".css")

	data := asset.Body

	var discoveredAssets []string
	if isCSS {
		cssMirrorPath := filepath.ToSlash(filepath.Join("assets", localPath))
		data = RewriteCSS(data, assetURL, func(absRef string) string {
			// References inside CSS are mostly images (backgrounds, etc.)
			// Font files usually have extensions and are handled correctly.
			refKind := KindImage
			if !ec.wantAsset(absRef, refKind) {
				// Keep original URL for rejected assets.
				return absRef
			}
			refKey := AssetKey(absRef)
			if ec.front.offer(refKey) {
				discoveredAssets = append(discoveredAssets, absRef)
			}
			targetLocalPath := LocalPath(ec.host, absRef, refKind)
			targetMirrorPath := filepath.ToSlash(filepath.Join("assets", targetLocalPath))
			return Rel(cssMirrorPath, targetMirrorPath)
		})
	}

	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		ec.front.markVisited(key)
		return false
	}

	if len(discoveredAssets) > 0 {
		ec.wg.Add(1)
		go func() {
			defer ec.wg.Done()
			for _, discURL := range discoveredAssets {
				select {
				case <-ctx.Done():
					return
				default:
					ec.enqueueAssetNonBlocking(discURL)
				}
			}
		}()
	}

	// Register downloaded asset.
	ec.assetMu.Lock()
	ec.downloadedAssets[assetURL] = &downloadedAsset{
		URL:         assetURL,
		LocalPath:   localPath,
		ContentType: contentType,
		Size:        int64(len(data)),
		MimeType:    contentType,
	}
	ec.assetMu.Unlock()

	ec.statsMu.Lock()
	ec.stats.AssetsDownloaded++
	ec.stats.TotalBytes += int64(len(data))
	ec.statsMu.Unlock()

	ec.front.markVisited(key)
	return true
}

// processAsset downloads and saves a single asset, rewriting CSS references.
// Uses HTTP client first, falls back to browser network stack on network errors.
func (ec *EnhancedCloner) processAsset(ctx context.Context, assetURL string) error {
	key := AssetKey(assetURL)

	ec.assetMu.RLock()
	if _, exists := ec.downloadedAssets[assetURL]; exists {
		ec.assetMu.RUnlock()
		return nil
	}
	ec.assetMu.RUnlock()

	// Add a small random delay before each asset download to reduce
	// the chance of triggering rate limiting. Assume all domains
	// may have anti-bot protection.
	delay := time.Duration(300+rand.Intn(700)) * time.Millisecond
	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return ctx.Err()
	}

	if util.DebugEnabled {
		fmt.Fprintf(os.Stderr, "[wukong/clone] downloading asset: %s\n", assetURL)
	}

	var body []byte
	var contentType string
	var isCSS bool

	// Try HTTP client first.
	assetResult, httpErr := ec.assetDownloader.Download(ctx, assetURL)
	if httpErr == nil {
		body = assetResult.Body
		contentType = assetResult.ContentType
		isCSS = assetResult.IsCSS
	} else {
		if util.DebugEnabled {
			fmt.Fprintf(os.Stderr, "[wukong/clone] HTTP asset download failed: %s - %v\n", assetURL, httpErr)
		}

		// Check if we should try browser fallback.
		// Fallback strategy:
		//   - network errors (DNS, connection, etc.)
		//   - 403 Forbidden: browser has better chance with proper
		//     cookies, referer, and cache from page rendering
		//   - other non-404 HTTP errors
		//   - 404 is skipped because browser would also get 404
		shouldFallback := false
		var de *DownloadError
		if AsDownloadError(httpErr, &de) {
			switch de.Reason {
			case "network":
				shouldFallback = true
			case "http_status":
				if de.StatusCode == 403 {
					shouldFallback = true
				} else if de.StatusCode != 404 {
					shouldFallback = true
				}
			}
		} else {
			shouldFallback = true
		}

		if shouldFallback && ec.browserPool != nil {
			if util.DebugEnabled {
				fmt.Fprintf(os.Stderr, "[wukong/clone] falling back to browser download for: %s\n", assetURL)
			}
			browserResult, browserErr := ec.browserPool.DownloadAsset(ctx, assetURL, ec.seedURL)
			if browserErr == nil && len(browserResult.Body) > 0 {
				body = browserResult.Body
				contentType = browserResult.ContentType
				isCSS = isCSSContentType(contentType) ||
					strings.HasSuffix(strings.ToLower(assetURL), ".css")
				if util.DebugEnabled {
					fmt.Fprintf(os.Stderr, "[wukong/clone] browser download succeeded for: %s (%d bytes)\n", assetURL, len(body))
				}
			} else {
				if util.DebugEnabled {
					fmt.Fprintf(os.Stderr, "[wukong/clone] browser download also failed: %s - %v\n", assetURL, browserErr)
				}
			}
		}

		// If both methods failed, handle anti-bot and return error.
		if len(body) == 0 {
			// Anti-bot check: was this asset blocked by HTTP status?
			if AsDownloadError(httpErr, &de) && de.StatusCode > 0 {
				reason, desc := antibot.DetectHTTP(de.StatusCode, nil)
				if reason != antibot.ReasonNone {
					// Already at max level? Skip useless retry and
					// just give up on this asset instead of wasting
					// minutes on UA rotation that doesn't help.
					atMax := ec.antibot.Level() >= ec.antibot.Escalator.MaxLevel
					isSameLevel := ec.antibot.Escalator.RetryCount(assetURL) > 0

					if atMax && isSameLevel {
						fmt.Fprintf(os.Stderr,
							"[wukong/antibot] asset %s: %s. already at max level (%s), skipping retry\n",
							assetURL, desc, ec.antibot.Level())
						ec.front.markVisited(key)
						return httpErr
					}

					retry, delay, _, msg := ec.antibot.Escalate(
						assetURL, reason, de.StatusCode)
					ec.applyAntiBotLevel()
					fmt.Fprintf(os.Stderr,
						"[wukong/antibot] asset %s: %s. %s\n",
						assetURL, desc, msg)
					if retry {
						time.Sleep(delay)
						ec.wg.Add(1)
						ec.assetJobs <- assetJob{url: assetURL}
						return nil
					}
				}
			}
			ec.front.markVisited(key)
			return httpErr
		}
	}

	// Determine local path.
	localPath := LocalPath(ec.host, assetURL, KindAsset)
	fullPath := filepath.Join(ec.assetDir, localPath)

	// Ensure directory.
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		ec.front.markVisited(key)
		return err
	}

	data := body

	var discoveredAssets []string
	if isCSS {
		cssMirrorPath := filepath.ToSlash(filepath.Join("assets", localPath))
		data = RewriteCSS(data, assetURL, func(absRef string) string {
			// References inside CSS are mostly images (backgrounds, etc.)
			// Font files usually have extensions and are handled correctly.
			refKind := KindImage
			if !ec.wantAsset(absRef, refKind) {
				// Keep original URL for rejected assets.
				return absRef
			}
			refKey := AssetKey(absRef)
			if ec.front.offer(refKey) {
				discoveredAssets = append(discoveredAssets, absRef)
			}
			targetLocalPath := LocalPath(ec.host, absRef, refKind)
			targetMirrorPath := filepath.ToSlash(filepath.Join("assets", targetLocalPath))
			return Rel(cssMirrorPath, targetMirrorPath)
		})
	}

	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		ec.front.markVisited(key)
		if util.DebugEnabled {
			fmt.Fprintf(os.Stderr, "[wukong/clone] asset write failed: %s - %v\n", assetURL, err)
		}
		return err
	}
	if util.DebugEnabled {
		fmt.Fprintf(os.Stderr, "[wukong/clone] asset saved: %s -> %s (%d bytes)\n", assetURL, fullPath, len(data))
	}

	if len(discoveredAssets) > 0 {
		ec.wg.Add(1)
		go func() {
			defer ec.wg.Done()
			for _, discURL := range discoveredAssets {
				select {
				case <-ctx.Done():
					return
				default:
					ec.enqueueAssetNonBlocking(discURL)
				}
			}
			if util.DebugEnabled {
				fmt.Fprintf(os.Stderr, "[wukong/clone] CSS processed: %s, discovered %d assets\n", assetURL, len(discoveredAssets))
			}
		}()
	}

	// Register downloaded asset.
	ec.assetMu.Lock()
	ec.downloadedAssets[assetURL] = &downloadedAsset{
		URL:         assetURL,
		LocalPath:   localPath,
		ContentType: contentType,
		Size:        int64(len(data)),
		MimeType:    contentType,
	}
	ec.assetMu.Unlock()

	ec.statsMu.Lock()
	ec.stats.AssetsDownloaded++
	ec.stats.TotalBytes += int64(len(data))
	ec.statsMu.Unlock()

	ec.front.markVisited(key)
	return nil
}

// ---------------------------------------------------------------------------
// Link rewriting (single-pass DOM walk with discovery).
// ---------------------------------------------------------------------------

// rewriteAndDiscover performs a single-pass DOM walk that simultaneously:
//  1. Rewrites all resource URLs to local relative paths.
//  2. Discovers new pages (enqueues them if in scope and under limits).
//  3. Discovers new assets (enqueues them if the policy allows download).
//
// This merges the former three-pass approach (extractLinks + extractAssets
// + rewritePageLinks) into one efficient traversal.
func (ec *EnhancedCloner) rewriteAndDiscover(htmlStr, pageURL, pageMirrorPath string,
	depth int, inScope bool, linksFound, assetsFound *int) string {

	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return htmlStr
	}

	base, err := url.Parse(pageURL)
	if err != nil {
		return htmlStr
	}

	shouldCrawl := ec.shouldCrawlMore(depth)

	// Build the rewrite-and-discover sink: for each URL encountered,
	// compute its local path, enqueue it for crawling/download, and
	// return the relative path for the rewritten HTML.
	sink := func(absURL string, kind URLKind) string {
		var targetPath string
		switch kind {
		case KindPage:
			*linksFound++
			// Check if this page is in scope and should be crawled.
			// scope-prefix and scope-anchor are OR relationships.
			if shouldCrawl {
				if u, err := url.Parse(absURL); err == nil {
					seed, _ := url.Parse(ec.seedURL)
					if seed != nil && SameSite(seed, u, ec.opts.Subdomains) {
						linkInScope := false

						// Check scope-prefix first.
						if ec.opts.ScopePrefix != "" {
							if matchesScopePrefixWithList(u.Path, ec.opts.ScopePrefix) {
								linkInScope = true
							}
						}

						// Check scope-anchor (OR: if not already matched, check anchor)
						if !linkInScope && ec.opts.ScopeAnchor != "" {
							if u.Fragment != "" && strings.EqualFold(u.Fragment, ec.opts.ScopeAnchor) {
								linkInScope = true
							}
							// Also check last segment of scope-prefix against fragment
							if !linkInScope && ec.opts.ScopePrefix != "" && u.Fragment != "" {
								cleanPrefix := strings.Trim(ec.opts.ScopePrefix, "/")
								parts := strings.Split(cleanPrefix, "/")
								if len(parts) > 0 {
									lastSegment := strings.ToLower(parts[len(parts)-1])
									if strings.ToLower(u.Fragment) == lastSegment {
										linkInScope = true
									}
								}
							}
						}

						// If no scope restrictions at all, allow by default.
						if ec.opts.ScopePrefix == "" && ec.opts.ScopeAnchor == "" {
							linkInScope = true
						}

						if linkInScope {
							ec.enqueuePageWithReferer(absURL, depth+1, pageURL, true)
						}
					}
				}
			}
			targetPath = filepath.ToSlash(filepath.Join("pages",
				LocalPath(ec.host, absURL, KindPage)))

		default:
			*assetsFound++
			if ec.wantAsset(absURL, kind) {
				key := AssetKey(absURL)
				if ec.front.offer(key) {
					ec.enqueueAssetNonBlocking(absURL)
					if util.DebugEnabled {
						fmt.Fprintf(os.Stderr, "[wukong/clone] enqueued asset: %s\n", absURL)
					}
				} else {
					if util.DebugEnabled {
						fmt.Fprintf(os.Stderr, "[wukong/clone] asset already seen: %s\n", absURL)
					}
				}
				targetPath = filepath.ToSlash(filepath.Join("assets",
					LocalPath(ec.host, absURL, kind)))
			} else {
				if util.DebugEnabled {
					fmt.Fprintf(os.Stderr, "[wukong/clone] asset rejected by policy: %s\n", absURL)
				}
				return ""
			}
		}

		return Rel(pageMirrorPath, targetPath)
	}

	RewriteHTML(doc, base, sink)

	var buf bytes.Buffer
	if err := html.Render(&buf, doc); err != nil {
		return htmlStr
	}
	return buf.String()
}

// processBrowserExtractedLinks processes links extracted from the browser's
// rendered DOM. These are links that were dynamically generated by JavaScript
// and may not be present in the static HTML.
func (ec *EnhancedCloner) processBrowserExtractedLinks(
	links []string, pageURL string, depth int, inScope bool) int {

	if !ec.shouldCrawlMore(depth) {
		return 0
	}

	seed, _ := url.Parse(ec.seedURL)
	if seed == nil {
		return 0
	}

	count := 0
	seenInBatch := make(map[string]bool)

	for _, absURL := range links {
		if absURL == "" {
			continue
		}

		// Normalize first to dedup.
		canonURL, err := Normalize(ec.seedURL, absURL)
		if err != nil {
			continue
		}
		if seenInBatch[canonURL] {
			continue
		}
		seenInBatch[canonURL] = true

		u, err := url.Parse(absURL)
		if err != nil {
			continue
		}

		// Only HTTP(S).
		if u.Scheme != "http" && u.Scheme != "https" {
			continue
		}

		// Must be same site.
		if !SameSite(seed, u, ec.opts.Subdomains) {
			continue
		}

		// Must be a page (not a binary asset).
		if !LikelyPage(absURL) {
			continue
		}

		if util.DebugEnabled && count < 20 {
			fmt.Fprintf(os.Stderr, "[wukong/clone] browser link candidate: %s\n", absURL)
		}

		// Use enqueuePageWithReferer which handles all checks (scope, dedup, limits).
		// We check if it was already seen first to count new pages.
		key := PageKey(ec.host, canonURL)
		if ec.front.offer(key) {
			// Not seen before — remove our mark so enqueuePageWithReferer can do it properly.
			ec.front.mu.Lock()
			delete(ec.front.seen, key)
			ec.front.mu.Unlock()
			ec.enqueuePageWithReferer(absURL, depth+1, pageURL, true)
			count++
		}
	}

	if util.DebugEnabled {
		fmt.Fprintf(os.Stderr, "[wukong/clone] browser links: %d total candidates, %d new pages enqueued\n",
			len(seenInBatch), count)
	}

	// Smart pagination detection: find pagination patterns in the links
	// and generate missing pages.
	if len(links) > 0 {
		paginationCount := ec.detectAndGeneratePagination(links, pageURL, depth)
		count += paginationCount
	}

	return count
}

// detectAndGeneratePagination detects pagination patterns from the extracted
// links and generates URLs for missing pages. This handles cases where only
// a subset of pagination links are visible (e.g., "1 2 3 ... 10" where
// pages 4-9 are not shown as links).
func (ec *EnhancedCloner) detectAndGeneratePagination(
	links []string, pageURL string, depth int) int {

	if len(links) == 0 {
		return 0
	}

	seed, _ := url.Parse(ec.seedURL)
	if seed == nil {
		return 0
	}

	// Common pagination parameter names.
	paginationParams := []string{
		"page", "Page", "p", "pg", "pn",
		"page_num", "pageNum", "page_number",
		"start", "offset", "skip",
	}

	// Group links by (host + path) to find pagination patterns.
	type pageInfo struct {
		url     *url.URL
		pageNum int
	}
	pageGroups := make(map[string][]pageInfo)

	baseParsed, _ := url.Parse(pageURL)
	basePath := baseParsed.Path

	if util.DebugEnabled {
		fmt.Fprintf(os.Stderr, "[wukong/clone/pagination] detecting patterns for base path: %s\n", basePath)
	}

	for _, link := range links {
		u, err := url.Parse(link)
		if err != nil {
			continue
		}
		if !SameSite(seed, u, ec.opts.Subdomains) {
			continue
		}
		// Only consider same-path links (same listing page, different page param).
		if u.Path != basePath {
			continue
		}
		if !LikelyPage(link) {
			continue
		}

		// Check for pagination parameters.
		for _, param := range paginationParams {
			val := u.Query().Get(param)
			if val == "" {
				continue
			}
			// Try to parse as integer.
			var pageNum int
			if _, err := fmt.Sscanf(val, "%d", &pageNum); err != nil {
				continue
			}
			key := u.Host + u.Path + "?" + param
			pageGroups[key] = append(pageGroups[key], pageInfo{url: u, pageNum: pageNum})
			if util.DebugEnabled {
				fmt.Fprintf(os.Stderr, "[wukong/clone/pagination] found page link: %s (param=%s, num=%d)\n",
					link, param, pageNum)
			}
			break
		}
	}

	count := 0

	// For each group, find min/max page and generate missing pages.
	for groupKey, pages := range pageGroups {
		if len(pages) < 2 {
			if util.DebugEnabled {
				fmt.Fprintf(os.Stderr, "[wukong/clone/pagination] group %s has only %d page(s), skipping\n",
					groupKey, len(pages))
			}
			continue // Need at least 2 pages to detect a pattern.
		}

		// Find min and max page numbers.
		minPage := pages[0].pageNum
		maxPage := pages[0].pageNum
		for _, p := range pages {
			if p.pageNum < minPage {
				minPage = p.pageNum
			}
			if p.pageNum > maxPage {
				maxPage = p.pageNum
			}
		}

		if util.DebugEnabled {
			fmt.Fprintf(os.Stderr, "[wukong/clone/pagination] group %s: %d pages, range %d-%d\n",
				groupKey, len(pages), minPage, maxPage)
		}

		// Extract the parameter name from the group key.
		// Key format: "host/path?param"
		paramIdx := strings.LastIndex(groupKey, "?")
		if paramIdx < 0 {
			continue
		}
		paramName := groupKey[paramIdx+1:]

		// Use the first page's URL as a template.
		templateURL := pages[0].url

		// Generate all pages from minPage to maxPage.
		for pageNum := minPage; pageNum <= maxPage; pageNum++ {
			// Create a copy of the template URL.
			newURL := *templateURL
			q := newURL.Query()
			q.Set(paramName, fmt.Sprintf("%d", pageNum))
			newURL.RawQuery = q.Encode()

			absURL := newURL.String()

			// Check scope.
			linkInScope := false
			if ec.opts.ScopePrefix != "" {
				if matchesScopePrefix(newURL.Path, ec.opts.ScopePrefix) {
					linkInScope = true
				}
			}
			if ec.opts.ScopePrefix == "" && ec.opts.ScopeAnchor == "" {
				linkInScope = true
			}

			if !linkInScope {
				continue
			}

			// Enqueue the page.
			canonURL, err := Normalize(ec.seedURL, absURL)
			if err != nil {
				continue
			}
			key := PageKey(ec.host, canonURL)
			if ec.front.offer(key) {
				ec.front.mu.Lock()
				delete(ec.front.seen, key)
				ec.front.mu.Unlock()
				ec.enqueuePageWithReferer(absURL, depth+1, pageURL, true)
				count++
			}
		}
	}

	if count > 0 {
		fmt.Fprintf(os.Stderr, "\n[wukong/clone] pagination: generated %d additional pages from pattern detection\n", count)
	} else if util.DebugEnabled {
		fmt.Fprintf(os.Stderr, "[wukong/clone/pagination] no new pages generated\n")
	}

	return count
}

// ---------------------------------------------------------------------------
// Traversal dispatcher.
// ---------------------------------------------------------------------------

// traversalDispatcher feeds page jobs to workers according to the selected
// traversal strategy. For BFS, it reads from the pageJobs channel (FIFO).
// For DFS, it manages a LIFO stack and pushes items to pageJobs as workers
// become available.
func (ec *EnhancedCloner) traversalDispatcher(ctx context.Context) {
	if ec.opts.Traversal != TraversalDFS {
		return // BFS: pages go directly to pageJobs channel.
	}

	for {
		// Wait for pages, stop signal, or context cancellation.
		select {
		case <-ctx.Done():
			return
		case <-ec.dispatcherStop:
			return
		case <-ec.pageReady:
			// Drain the stack into the pageJobs channel (LIFO → reverse).
			ec.drainStack(ctx)
		}
	}
}

// drainStack pops all items from the page stack and sends them to the
// pageJobs channel in LIFO order (deepest pages first for DFS).
func (ec *EnhancedCloner) drainStack(ctx context.Context) {
	for {
		ec.pageMu.Lock()
		if len(ec.pageStack) == 0 {
			ec.pageMu.Unlock()
			return
		}
		// Pop from end (LIFO).
		job := ec.pageStack[len(ec.pageStack)-1]
		ec.pageStack = ec.pageStack[:len(ec.pageStack)-1]
		ec.pageMu.Unlock()

		select {
		case ec.pageJobs <- job:
		case <-ctx.Done():
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

// enqueuePage adds a page URL to the crawl queue.
func (ec *EnhancedCloner) enqueuePage(pageURL string, depth int) {
	inScope := false
	if ec.opts.ScopePrefix != "" {
		parsed, _ := url.Parse(pageURL)
		if parsed != nil && matchesScopePrefix(parsed.Path, ec.opts.ScopePrefix) {
			inScope = true
		}
	}
	ec.enqueuePageWithReferer(pageURL, depth, "", inScope)
}

// enqueuePageWithReferer adds a page URL to the crawl queue with a referer
// URL to simulate user navigation. The seed page should use an empty referer.
// inScope indicates whether this page is within the scope-prefix.
func (ec *EnhancedCloner) enqueuePageWithReferer(pageURL string, depth int, referer string, inScope bool) {
	// Validate URL.
	parsed, err := url.Parse(pageURL)
	if err != nil {
		if util.DebugEnabled {
			fmt.Fprintf(os.Stderr, "[wukong/clone] enqueue rejected (parse error): %s\n", pageURL)
		}
		return
	}

	// Only HTTP(S).
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		if util.DebugEnabled {
			fmt.Fprintf(os.Stderr, "[wukong/clone] enqueue rejected (non-http): %s\n", pageURL)
		}
		return
	}

	seed, _ := url.Parse(ec.seedURL)
	isSeedURL := pageURL == ec.seedURL ||
		(seed != nil && parsed.Path == seed.Path && parsed.Host == seed.Host)

	// Skip non-HTML file extensions to avoid wasting render time on downloads.
	// The seed URL is always allowed.
	if !isSeedURL && !LikelyPage(pageURL) {
		if util.DebugEnabled {
			fmt.Fprintf(os.Stderr, "[wukong/clone] enqueue rejected (not a page): %s\n", pageURL)
		}
		return
	}

	if isSeedURL {
		if ec.opts.ScopePrefix != "" && !matchesScopePrefix(parsed.Path, ec.opts.ScopePrefix) {
			scopeRootURL := fmt.Sprintf("%s://%s%s", parsed.Scheme, parsed.Host, ec.opts.ScopePrefix)
			fmt.Fprintf(os.Stderr,
				"[wukong/clone] warning: seed URL %q does not match scope-prefix %q — "+
					"automatically adding %q as an additional starting point\n",
				pageURL, ec.opts.ScopePrefix, scopeRootURL)
			ec.enqueuePageWithReferer(scopeRootURL, 0, "", true)
		}
	}

	// Always re-check scope here as the final gatekeeper,
	// regardless of what the caller passed in.
	// scope-prefix and scope-anchor are OR relationships:
	// a page is in scope if it matches EITHER the prefix OR the anchor.
	scopeMatched := false

	// Check scope-prefix first.
	if ec.opts.ScopePrefix != "" {
		if matchesScopePrefixWithList(parsed.Path, ec.opts.ScopePrefix) {
			scopeMatched = true
		}
	}

	// Check scope-anchor (OR: if not already matched by prefix, check anchor).
	if !scopeMatched && ec.opts.ScopeAnchor != "" {
		if parsed.Fragment != "" && strings.EqualFold(parsed.Fragment, ec.opts.ScopeAnchor) {
			scopeMatched = true
		}
		// Also check if the last segment of scope-prefix matches the fragment,
		// e.g. scope-prefix="/biographies-list" matches "#biographies"
		if !scopeMatched && ec.opts.ScopePrefix != "" && parsed.Fragment != "" {
			cleanPrefix := strings.Trim(ec.opts.ScopePrefix, "/")
			parts := strings.Split(cleanPrefix, "/")
			if len(parts) > 0 {
				lastSegment := strings.ToLower(parts[len(parts)-1])
				if strings.ToLower(parsed.Fragment) == lastSegment {
					scopeMatched = true
				}
			}
		}
	}

	// Seed URL is always allowed.
	if isSeedURL {
		scopeMatched = true
	}

	// Final scope check: only if at least one scope option is specified.
	if ec.opts.ScopePrefix != "" || ec.opts.ScopeAnchor != "" {
		if !scopeMatched {
			if util.DebugEnabled {
				fmt.Fprintf(os.Stderr, "[wukong/clone] out of scope, skipping: %s (path=%s, fragment=%s)\n",
					pageURL, parsed.Path, parsed.Fragment)
			}
			return
		}
	}

	if !isSeedURL {
		if seed != nil && !SameSite(seed, parsed, ec.opts.Subdomains) {
			if util.DebugEnabled {
				fmt.Fprintf(os.Stderr, "[wukong/clone] enqueue rejected (cross-site): %s\n", pageURL)
			}
			return
		}
	}

	// Normalize and get key.
	canonURL, err := Normalize(ec.seedURL, pageURL)
	if err != nil {
		if util.DebugEnabled {
			fmt.Fprintf(os.Stderr, "[wukong/clone] enqueue rejected (normalize error): %s - %v\n", pageURL, err)
		}
		return
	}
	key := PageKey(ec.host, canonURL)

	// Check MaxDepth.
	if ec.opts.MaxDepth > 0 && depth > ec.opts.MaxDepth {
		if util.DebugEnabled {
			fmt.Fprintf(os.Stderr, "[wukong/clone] enqueue rejected (max depth %d): %s\n", depth, pageURL)
		}
		return
	}

	// Check MaxPages limit.
	if ec.opts.MaxPages > 0 {
		ec.enqueuedMu.Lock()
		if ec.enqueuedPages >= ec.opts.MaxPages {
			ec.enqueuedMu.Unlock()
			if util.DebugEnabled {
				fmt.Fprintf(os.Stderr, "[wukong/clone] enqueue rejected (max pages): %s\n", pageURL)
			}
			return
		}
		ec.enqueuedMu.Unlock()
	}

	// Offer to frontier for dedup.
	if !ec.front.offer(key) {
		if util.DebugEnabled {
			fmt.Fprintf(os.Stderr, "[wukong/clone] enqueue rejected (already seen): %s\n", pageURL)
		}
		return // Already seen.
	}

	ec.enqueuedMu.Lock()
	ec.enqueuedPages++
	ec.enqueuedMu.Unlock()

	ec.wg.Add(1)

	if util.DebugEnabled {
		fmt.Fprintf(os.Stderr, "[wukong/clone] enqueued page (depth=%d): %s\n", depth, canonURL)
	}

	// BFS vs DFS: enqueue to channel (FIFO) or push to stack (LIFO).
	if ec.opts.Traversal == TraversalDFS {
		ec.pageMu.Lock()
		ec.pageStack = append(ec.pageStack, pageJob{url: canonURL, depth: depth, referer: referer, inScope: inScope})
		ec.pageMu.Unlock()
		// Signal dispatcher that new pages are available.
		select {
		case ec.pageReady <- struct{}{}:
		default:
		}
	} else {
		// Non-blocking send: if channel is full, process in a goroutine
		// to avoid deadlock when all workers are busy discovering new pages.
		job := pageJob{url: canonURL, depth: depth, referer: referer, inScope: inScope}
		select {
		case ec.pageJobs <- job:
		default:
			go func(j pageJob) {
				result := ec.processPage(ec.ctx, j.url, j.depth, j.referer, j.inScope)
				ec.statsMu.Lock()
				if result.Error == "" {
					ec.stats.PagesCloned++
					ec.stats.TotalBytes += result.Size
				} else {
					ec.stats.PagesFailed++
				}
				ec.statsMu.Unlock()
				ec.resultsMu.Lock()
				ec.results = append(ec.results, result)
				ec.resultsMu.Unlock()
				ec.wg.Done()
			}(job)
		}
	}
}

// shouldCrawlMore checks whether more links should be followed.
func (ec *EnhancedCloner) shouldCrawlMore(depth int) bool {
	if ec.opts.MaxDepth > 0 && depth >= ec.opts.MaxDepth {
		return false
	}
	if ec.opts.MaxPages > 0 {
		ec.statsMu.RLock()
		pages := ec.stats.PagesCloned
		ec.statsMu.RUnlock()
		if pages >= ec.opts.MaxPages {
			return false
		}
	}
	return true
}

// applyAntiBotLevel applies the current antibot escalation level to the
// browser pool. When the antibot engine escalates to Level 2+ (Stealth),
// this dynamically injects anti-detection scripts into the running browser
// so all subsequent page loads are stealth-enabled without a restart.
// preflightCloudflareCheck performs a fast HEAD request to the seed URL
// before Chrome is started. If Cloudflare headers are detected (cf-ray,
// cf-chl-bypass, etc.), it automatically enables Stealth mode and sets the
// antibot baseline to LevelStealth so the very first page load is already
// stealth-protected — avoiding the "detect → escalate → retry" cycle that
// wastes the first 1-2 page loads.
//
// Uses its own HTTP client because ec.httpClient may not be initialised yet.
func (ec *EnhancedCloner) preflightCloudflareCheck() {
	if !ec.opts.AntibotEnabled || ec.opts.Stealth {
		return // Already enabled or disabled.
	}

	client := httpclient.New(httpclient.Options{Timeout: 10 * time.Second})

	// Use GET — Cloudflare may return 0/-1 ContentLength on HEAD.
	req, err := http.NewRequest("GET", ec.seedURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", ec.opts.UserAgent)
	ec.setBrowserHeaders(req)

	// If we already have a cf_clearance from a previous Chrome render,
	// include it so the preflight bypasses Cloudflare challenges.
	if ec.cfClearance != "" {
		req.AddCookie(&http.Cookie{
			Name:  "cf_clearance",
			Value: ec.cfClearance,
		})
	}

	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if !antibot.HasCloudflareHeaders(resp.Header) {
		return
	}

	// Cloudflare detected BEFORE Chrome starts — enable Stealth.
	ec.opts.Stealth = true

	// Read response body to check for Turnstile markers.
	body, rErr := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if rErr != nil || len(body) == 0 {
		fmt.Fprintf(os.Stderr,
			"[wukong/antibot] Cloudflare detected on %s — "+
				"stealth enabled pre-emptively\n", ec.host)
		return
	}

	if antibot.HasTurnstileMarkers(string(body)) {
		// Turnstile is a JS-interactive challenge. Headless Chrome
		// cannot solve it. Disable auto-escalation NOW so we don't
		// waste ~10s on doomed retries.
		ec.opts.AntibotAutoEscalate = false
		ec.preflightTurnstile = true
		fmt.Fprintf(os.Stderr,
			"[wukong/antibot] Cloudflare Turnstile detected "+
				"on %s — headless Chrome cannot solve "+
				"interactive challenges. Stealth enabled, "+
				"auto-retry disabled (Tip: use a non-"+
				"Cloudflare mirror or real browser profile).\n",
			ec.host)
		return
	}

	fmt.Fprintf(os.Stderr,
		"[wukong/antibot] Cloudflare detected on %s — stealth "+
			"enabled pre-emptively\n", ec.host)
}

// runAntibotProbe performs multi-dimensional anti-bot probing and adjusts
// clone strategy based on the detected threats.
func (ec *EnhancedCloner) runAntibotProbe(ctx context.Context) {
	fmt.Fprintf(os.Stderr, "[wukong/antibot] probing %s for anti-bot measures...\n", ec.seedURL)

	p := prober.NewProber()
	profile := p.Probe(ctx, ec.seedURL)

	switch profile.Level {
	case prober.LevelCritical:
		fmt.Fprintf(os.Stderr, "[wukong/antibot] critical anti-bot protection detected — enabling stealth\n")
		ec.opts.Stealth = true
		ec.opts.AntibotAutoEscalate = false
	case prober.LevelHigh:
		fmt.Fprintf(os.Stderr, "[wukong/antibot] high anti-bot protection (%s) — enabling stealth\n", profile.WAF)
		ec.opts.Stealth = true
	case prober.LevelMedium:
		fmt.Fprintf(os.Stderr, "[wukong/antibot] medium anti-bot protection — enabling stealth\n")
		ec.opts.Stealth = true
	case prober.LevelLow:
		fmt.Fprintf(os.Stderr, "[wukong/antibot] low anti-bot measures detected — monitoring\n")
		if profile.HasRateLimit {
			if ec.opts.CrawlDelay == 0 {
				ec.opts.CrawlDelay = 2000
				fmt.Fprintf(os.Stderr, "[wukong/antibot] rate limiting detected — setting crawl delay to 2s\n")
			}
		}
	default:
		fmt.Fprintf(os.Stderr, "[wukong/antibot] no significant anti-bot measures detected\n")
	}

	if profile.WAF != "" {
		fmt.Fprintf(os.Stderr, "[wukong/antibot] detected WAF: %s\n", profile.WAF)
	}

	if profile.HasJSChallenge {
		fmt.Fprintf(os.Stderr, "[wukong/antibot] JS challenge detected — may require browser rendering\n")
	}
}

// setBrowserHeaders adds realistic Chrome headers (sec-ch-ua, sec-fetch-*)
// to an HTTP request. Cloudflare L3 detection checks these to distinguish
// browsers from simple HTTP clients. Without them, the preflight GET and
// asset requests look artificial.
func (ec *EnhancedCloner) setBrowserHeaders(req *http.Request) {
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8,ja;q=0.7")
	req.Header.Set("sec-ch-ua", `"Chromium";v="130", "Google Chrome";v="130", "Not?A_Brand";v="99"`)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"Windows"`)
	req.Header.Set("sec-fetch-site", "none")
	req.Header.Set("sec-fetch-mode", "navigate")
	req.Header.Set("sec-fetch-dest", "document")
	req.Header.Set("sec-fetch-user", "?1")
	req.Header.Set("upgrade-insecure-requests", "1")
}

// applyAntiBotLevel applies the current antibot escalation level to the
// browser pool. When the antibot engine escalates to Level 2+ (Stealth),
// this dynamically injects anti-detection scripts into the running browser
// so all subsequent page loads are stealth-enabled without a restart.
func (ec *EnhancedCloner) applyAntiBotLevel() {
	if ec.antibot == nil || ec.browserPool == nil {
		return
	}

	level := ec.antibot.Level()

	if ec.antibot.NeedsStealthScript() && !ec.browserPool.StealthEnabled() {
		if err := ec.browserPool.EnableStealth(); err != nil {
			fmt.Fprintf(os.Stderr,
				"[wukong/antibot] failed to enable stealth: %v\n", err)
		}
	}

	if level >= antibot.LevelAggressive {
		rotatedUA := ec.antibot.Escalator.RotateUserAgent()
		ec.assetDownloader.UserAgent = rotatedUA.UserAgent
		if pool, ok := ec.browserPool.(interface{ RotateUA() }); ok {
			pool.RotateUA()
		}
		fmt.Fprintf(os.Stderr,
			"[wukong/antibot] UA rotated for aggressive mode\n")
	}
}

// wantAsset reports whether an asset should be downloaded and localised.
// The kind parameter indicates the asset type (image, CSS, font, JS, etc.)
// as determined by the HTML context (e.g. <img> → image, <link rel=stylesheet> → CSS).
//
// Filtering policies:
//  1. Critical rendering assets (images, CSS, fonts) are ALWAYS downloaded
//     regardless of domain — the page needs them to render correctly.
//  2. Non-critical assets (JS, media, other) are subject to AssetSameDomain.
//  3. SkipAssetExts: skip assets whose file extension is in the skip set
//     (media files, archives, documents). These remain as live links.
func (ec *EnhancedCloner) wantAsset(assetURL string, kind URLKind) bool {
	u, err := url.Parse(assetURL)
	if err != nil {
		return false
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}

	ext := strings.ToLower(PathExt(assetURL))

	// SkipAssetExts: skip bulk media, documents, archives, installers.
	// Check early so skipped assets are never downloaded.
	if ec.opts.SkipAssetExts[ext] {
		return false
	}

	// Critical rendering assets are always downloaded regardless of domain.
	if isCriticalRenderingAsset(ext, kind) {
		return true
	}

	// AssetSameDomain: only download same-registrable-domain assets
	// for non-critical asset types (JS, media, other).
	if ec.opts.AssetSameDomain {
		seed, _ := url.Parse(ec.seedURL)
		if seed != nil && !SameRegistrableDomain(seed, u) {
			// Check if the asset host is in the allowed additional domains.
			if !ec.isAssetDomainAllowed(u.Host) {
				return false
			}
		}
	}

	return true
}

// isCriticalRenderingAsset returns true if the asset is essential for page
// rendering and should be downloaded even when AssetSameDomain is true and
// the host is on a different domain.
func isCriticalRenderingAsset(ext string, kind URLKind) bool {
	// First, check by extension — most reliable.
	switch ext {
	case
		// Images
		".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".ico",
		".bmp", ".tiff", ".tif", ".avif", ".apng", ".jxl", ".heic", ".heif",
		// CSS
		".css",
		// Fonts
		".woff", ".woff2", ".ttf", ".otf", ".eot":
		return true
	}

	// If extension is empty or unknown, use the kind determined from HTML context.
	switch kind {
	case KindImage, KindCSS, KindFont:
		return true
	}

	return false
}

// isAssetDomainAllowed checks whether a host is in the additional allowed
// asset domains list. Matching is case-insensitive and supports subdomain
// matching via leading dot (e.g. ".example.com" matches "cdn.example.com").
func (ec *EnhancedCloner) isAssetDomainAllowed(host string) bool {
	host = strings.ToLower(host)
	for _, domain := range ec.opts.AssetDomains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain == "" {
			continue
		}
		// Exact match.
		if domain == host {
			return true
		}
		// Subdomain match: ".example.com" matches "cdn.example.com".
		if strings.HasPrefix(domain, ".") && strings.HasSuffix(host, domain) {
			return true
		}
		// Registrable domain match: "example.com" matches "cdn.example.com".
		if !strings.HasPrefix(domain, ".") &&
			(strings.HasSuffix(host, "."+domain) || host == domain) {
			return true
		}
	}
	return false
}

// useCachedPage returns a cached page result when the page hasn't changed.
func (ec *EnhancedCloner) useCachedPage(pageURL string, reason string) PageResult {
	result := PageResult{
		URL:       pageURL,
		FromCache: true,
	}

	entry := ec.cache.GetEntry(pageURL)
	if entry == nil || entry.LocalPath == "" {
		result.Error = "cache miss (entry not found)"
		return result
	}

	// Verify the cached file still exists on disk.
	if _, err := os.Stat(entry.LocalPath); err != nil {
		result.Error = fmt.Sprintf("cached file gone: %v", err)
		return result
	}

	result.FilePath = entry.LocalPath
	result.Size = entry.Size
	result.Title = "(cached)"

	// Mark as visited in frontier so resume works correctly.
	key := PageKey(ec.host, pageURL)
	ec.front.markVisited(key)

	return result
}

// updateCacheEntry stores a rendered page in the incremental cache.
func (ec *EnhancedCloner) updateCacheEntry(pageURL, localPath string, content []byte) {
	if ec.cache == nil {
		return
	}
	entry := &CacheEntry{
		URL:         pageURL,
		LocalPath:   localPath,
		ContentHash: sha256Hex(content),
		LastFetched: time.Now(),
		StatusCode:  200,
		Size:        int64(len(content)),
	}
	ec.cache.SetEntry(entry)
}
