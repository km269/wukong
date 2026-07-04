package config

// ============================================================================
// Apps Configuration (Clone & Pack)
// ============================================================================

// AppsConfig defines custom HTML standalone application settings.
type AppsConfig struct {
	Enabled bool           `mapstructure:"enabled"`
	AppDir  string         `mapstructure:"app_dir"`
	Clone   CloneDefaults  `mapstructure:"clone"`
	Pack    PackDefaults   `mapstructure:"pack"`
}

// CloneDefaults holds default values for website cloning operations.
type CloneDefaults struct {
	MaxPages             int    `mapstructure:"max_pages"`
	MaxDepth             int    `mapstructure:"max_depth"`
	Traversal            string `mapstructure:"traversal"`
	Subdomains           bool   `mapstructure:"subdomains"`
	ScopePrefix          string `mapstructure:"scope_prefix"`
	Workers              int    `mapstructure:"workers"`
	AssetWorkers         int    `mapstructure:"asset_workers"`
	BrowserPages         int    `mapstructure:"browser_pages"`
	Timeout              int    `mapstructure:"timeout"`
	RenderTimeout        int    `mapstructure:"render_timeout"`
	Settle               int    `mapstructure:"settle"`
	Scroll               bool   `mapstructure:"scroll"`
	RespectRobots        bool   `mapstructure:"respect_robots"`
	CrawlDelay           int    `mapstructure:"crawl_delay"`
	NoSitemap            bool   `mapstructure:"no_sitemap"`
	DedupContent         bool   `mapstructure:"dedup_content"`
	MobileReadable       bool   `mapstructure:"mobile_readable"`
	EnableResume         bool   `mapstructure:"enable_resume"`
	Persist              bool   `mapstructure:"persist"`
	Incremental          bool   `mapstructure:"incremental"`
	CacheMaxAge          int    `mapstructure:"cache_max_age"`
	Headless             bool   `mapstructure:"headless"`
	Stealth              bool   `mapstructure:"stealth"`
	ChromeProfile        string `mapstructure:"chrome_profile"`
	ChromePath           string `mapstructure:"chrome_path"`
	AntibotEnabled       bool   `mapstructure:"antibot_enabled"`
	AntibotAutoEscalate  bool   `mapstructure:"antibot_auto_escalate"`
	AssetSameDomain      bool   `mapstructure:"asset_same_domain"`
	MaxAssetBytes        int64  `mapstructure:"max_asset_bytes"`
	CookieFile           string `mapstructure:"cookie_file"`
	UserAgent            string `mapstructure:"user_agent"`
}

// PackDefaults holds default values for app packaging operations.
type PackDefaults struct {
	Compress    bool   `mapstructure:"compress"`
	Incremental bool   `mapstructure:"incremental"`
	Language    string `mapstructure:"language"`
	Creator     string `mapstructure:"creator"`
	Format      string `mapstructure:"format"`
}