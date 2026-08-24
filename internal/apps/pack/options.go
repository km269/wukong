// Package pack provides application packaging functionality.
// It supports multiple output formats: HTML directory, ZIM archive,
// self-contained binary, and desktop application.
package pack

// Format defines the output format for packaging.
type Format string

const (
	// FormatHTML outputs a standard HTML directory structure.
	FormatHTML Format = "html"
	// FormatZIM outputs a ZIM archive file (Kiwix compatible).
	FormatZIM Format = "zim"
	// FormatBinary outputs a self-contained executable with embedded content.
	FormatBinary Format = "binary"
	// FormatApp outputs a desktop application bundle (.app/.exe).
	FormatApp Format = "app"
)

// Options defines configuration for application packaging.
type Options struct {
	Format         Format
	OutputPath     string
	AppName        string
	AppDescription string
	AppVersion     string
	BaseBinary     string
	IconPath       string
	Compress       bool
	IncludeAssets  bool
	EmbedFonts     bool

	// ZIM metadata.
	Language     string // ISO 639-3 language code (default "eng").
	Creator      string // Archive creator (default "Wukong").
	Publisher    string // Archive publisher.
	Date         string // Archive date (YYYY-MM-DD, default today).
	Title        string // Override title (default from main page <title>).
	MainPagePath string // Preferred main page relative path (e.g. "biographies-list.html").
	// If set and the file exists, it takes priority over root index.html
	// and the auto-generated content index.

	// Incremental ZIM packing.
	Incremental bool   // Reuse unchanged clusters from cache.
	CachePath   string // Path to .wukongcache file.
}

// DefaultOptions returns default packaging options.
func DefaultOptions() Options {
	return Options{
		Format:         FormatHTML,
		OutputPath:     "",
		AppName:        "",
		AppDescription: "",
		AppVersion:     "1.0.0",
		BaseBinary:     "",
		IconPath:       "",
		Compress:       false,
		IncludeAssets:  true,
		EmbedFonts:     false,
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

// WithFormat sets the output format.
func (b *OptionsBuilder) WithFormat(format Format) *OptionsBuilder {
	b.opts.Format = format
	return b
}

// WithOutputPath sets the output path.
func (b *OptionsBuilder) WithOutputPath(path string) *OptionsBuilder {
	b.opts.OutputPath = path
	return b
}

// WithAppName sets the application name.
func (b *OptionsBuilder) WithAppName(name string) *OptionsBuilder {
	b.opts.AppName = name
	return b
}

// WithAppDescription sets the application description.
func (b *OptionsBuilder) WithAppDescription(desc string) *OptionsBuilder {
	b.opts.AppDescription = desc
	return b
}

// WithAppVersion sets the application version.
func (b *OptionsBuilder) WithAppVersion(version string) *OptionsBuilder {
	b.opts.AppVersion = version
	return b
}

// WithBaseBinary sets the base binary path.
func (b *OptionsBuilder) WithBaseBinary(path string) *OptionsBuilder {
	b.opts.BaseBinary = path
	return b
}

// WithIconPath sets the icon path.
func (b *OptionsBuilder) WithIconPath(path string) *OptionsBuilder {
	b.opts.IconPath = path
	return b
}

// WithCompress enables compression.
func (b *OptionsBuilder) WithCompress(enable bool) *OptionsBuilder {
	b.opts.Compress = enable
	return b
}

// WithIncludeAssets enables asset inclusion.
func (b *OptionsBuilder) WithIncludeAssets(enable bool) *OptionsBuilder {
	b.opts.IncludeAssets = enable
	return b
}

// Build returns the constructed Options.
func (b *OptionsBuilder) Build() Options {
	return b.opts
}
