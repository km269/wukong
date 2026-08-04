package config

// ============================================================================
// Apps Configuration (Clone & Pack)
// ============================================================================

// AppsConfig defines custom HTML standalone application settings.
type AppsConfig struct {
	Enabled bool          `mapstructure:"enabled"`
	AppDir  string        `mapstructure:"app_dir"`
	Clone   CloneDefaults `mapstructure:"clone"`
	Pack    PackDefaults  `mapstructure:"pack"`
}

// CloneDefaults holds default values for website cloning operations.
//
// Time-valued fields use a bare int and are converted to time.Duration
// at the call site. The unit is documented per field to avoid ambiguity
// (seconds vs milliseconds). See internal/apps/manager.go:applyConfigDefaults
// for the canonical conversion factors.
//
// Several fields overlap with BrowserConfig (Headless, Stealth,
// ChromeProfile, ChromePath, BrowserBackend, ProxyEnabled, ProxyPool,
// ProxyRotateEvery, UserAgent, Scroll). Per the project hard constraint
// "apps download and apps clone must use identical anti-crawling
// measures", these should be kept consistent with browser.* values.
// Validate() warns when stealth/headless/backend diverge.
type CloneDefaults struct {
	MaxPages            int                `mapstructure:"max_pages"`
	MaxDepth            int                `mapstructure:"max_depth"`
	Traversal           string             `mapstructure:"traversal"`
	Subdomains          bool               `mapstructure:"subdomains"`
	ScopePrefix         string             `mapstructure:"scope_prefix"`
	Workers             int                `mapstructure:"workers"`
	AssetWorkers        int                `mapstructure:"asset_workers"`
	BrowserPages        int                `mapstructure:"browser_pages"`
	Timeout             int                `mapstructure:"timeout"`        // seconds
	RenderTimeout       int                `mapstructure:"render_timeout"` // seconds
	Settle              int                `mapstructure:"settle"`         // milliseconds (network idle wait)
	Scroll              bool               `mapstructure:"scroll"`
	RespectRobots       bool               `mapstructure:"respect_robots"`
	CrawlDelay          int                `mapstructure:"crawl_delay"` // milliseconds
	NoSitemap           bool               `mapstructure:"no_sitemap"`
	DedupContent        bool               `mapstructure:"dedup_content"`
	MobileReadable      bool               `mapstructure:"mobile_readable"`
	EnableResume        bool               `mapstructure:"enable_resume"`
	Persist             bool               `mapstructure:"persist"`
	Incremental         bool               `mapstructure:"incremental"`
	CacheMaxAge         int                `mapstructure:"cache_max_age"` // seconds
	Headless            bool               `mapstructure:"headless"`
	Stealth             bool               `mapstructure:"stealth"`
	ChromeProfile       string             `mapstructure:"chrome_profile"`
	ChromePath          string             `mapstructure:"chrome_path"`
	AntibotEnabled      bool               `mapstructure:"antibot_enabled"`
	AntibotAutoEscalate bool               `mapstructure:"antibot_auto_escalate"`
	AssetSameDomain     bool               `mapstructure:"asset_same_domain"`
	MaxAssetBytes       int64              `mapstructure:"max_asset_bytes"`
	CookieFile          string             `mapstructure:"cookie_file"`
	UserAgent           string             `mapstructure:"user_agent"`
	BrowserBackend      BrowserBackendType `mapstructure:"browser_backend"`
	ProxyEnabled        bool               `mapstructure:"proxy_enabled"`
	ProxyPool           []string           `mapstructure:"proxy_pool"`
	ProxyRotateEvery    int                `mapstructure:"proxy_rotate_every"`
	ArchiveFallback     bool               `mapstructure:"archive_fallback"`
}

// PackDefaults holds default values for app packaging operations.
type PackDefaults struct {
	Compress    bool   `mapstructure:"compress"`
	Incremental bool   `mapstructure:"incremental"`
	Language    string `mapstructure:"language"`
	Creator     string `mapstructure:"creator"`
	Format      string `mapstructure:"format"`
}
