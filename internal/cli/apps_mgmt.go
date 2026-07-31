// Package cli provides the "wukong apps" command for HTML application
// lifecycle management.
package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/km269/wukong/internal/apps"
	"github.com/km269/wukong/internal/apps/server"
	"github.com/km269/wukong/internal/browser/antibot/prober"
	"github.com/km269/wukong/internal/config"
)

// newAppsCmd creates the "wukong apps" command group.
func newAppsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apps",
		Short: "Manage HTML applications",
		Long: `Manage HTML applications created by the agent or user.
Apps can be custom-built, cloned from websites, or imported
from external sources.

Subcommands:
  list      List all apps
  show      Show app details and content preview
  create    Create a new app or use a template
  clone     Clone a website as an offline app
  pack      Package an app into a distributable format
  view      Preview an app in the browser
  delete    Delete an app
  history   View version history
  export    Export an app to a single file`,
	}

	cmd.AddCommand(newAppsListCmd())
	cmd.AddCommand(newAppsShowCmd())
	cmd.AddCommand(newAppsCreateCmd())
	cmd.AddCommand(newAppsCloneCmd())
	cmd.AddCommand(newAppsProbeCmd())
	cmd.AddCommand(newAppsPackCmd())
	cmd.AddCommand(newAppsViewCmd())
	cmd.AddCommand(newAppsDeleteCmd())
	cmd.AddCommand(newAppsHistoryCmd())
	cmd.AddCommand(newAppsExportCmd())
	cmd.AddCommand(newAppsDownloadCmd())

	return cmd
}

// ==========================================================================
// apps list
// ==========================================================================

func newAppsListCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all HTML applications",
		Long: `List all HTML applications managed by wukong,
including their type, status, size, and last modification.

Examples:
  wukong apps list
  wukong apps ls`,
		RunE: runAppsList,
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file")

	return cmd
}

func runAppsList(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")

	mgr, cleanup, err := createAppsManager(configPath)
	if err != nil {
		return err
	}
	defer cleanup()

	appsList := mgr.ListApps()
	if len(appsList) == 0 {
		fmt.Println("No apps found.")
		fmt.Println()
		fmt.Println("Create an app:")
		fmt.Println("  wukong apps create --name my-app ")
		fmt.Println("  --description \"My first app\"")
		fmt.Println()
		fmt.Println("Or let the agent create apps for you during a session.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tTYPE\tSTATUS\tSIZE\tVERSION\tUPDATED")
	fmt.Fprintln(w, "───\t───\t───\t───\t───\t───")

	for _, app := range appsList {
		size := formatSize(app.Size)
		updated := app.UpdatedAt.Format("2006-01-02 15:04")
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			app.Name, app.Type, app.Status,
			size, app.Version, updated)
	}
	w.Flush()

	fmt.Printf("\nTotal: %d app(s)\n", len(appsList))
	fmt.Printf("App directory: %s\n", mgr.GetAppDir())
	return nil
}

// ==========================================================================
// apps show
// ==========================================================================

func newAppsShowCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "show <app-name>",
		Short: "Show app details and content preview",
		Long: `Display detailed information about an app including
metadata, file location, and a content preview.

Examples:
  wukong apps show my-app`,
		RunE: runAppsShow,
		Args: cobra.ExactArgs(1),
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file")

	return cmd
}

func runAppsShow(cmd *cobra.Command, args []string) error {
	name := args[0]
	configPath, _ := cmd.Flags().GetString("config")

	mgr, cleanup, err := createAppsManager(configPath)
	if err != nil {
		return err
	}
	defer cleanup()

	app, ok := mgr.GetApp(name)
	if !ok {
		return fmt.Errorf("app %q not found. Run 'wukong apps list' "+
			"to see available apps.", name)
	}

	fmt.Println(strings.Repeat("─", 60))
	fmt.Printf("  App: %s\n", name)
	fmt.Println(strings.Repeat("─", 60))

	fmt.Printf("\n  Description: %s\n", app.Description)
	fmt.Printf("  Type:        %s\n", app.Type)
	fmt.Printf("  Status:      %s\n", app.Status)
	fmt.Printf("  Version:     %s\n", app.Version)
	fmt.Printf("  Size:        %s\n", formatSize(app.Size))
	fmt.Printf("  Created:     %s\n", app.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("  Updated:     %s\n", app.UpdatedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("  File:        %s\n", app.FilePath)

	if app.AppDir != "" {
		fmt.Printf("  App Dir:     %s\n", app.AppDir)
	}
	if app.SourceURL != "" {
		fmt.Printf("  Source:      %s\n", app.SourceURL)
	}
	if app.Pages > 0 {
		fmt.Printf("  Pages:       %d\n", app.Pages)
	}
	if app.Assets > 0 {
		fmt.Printf("  Assets:      %d\n", app.Assets)
	}

	// Content preview
	html, err := mgr.ReadAppHTML(name)
	if err != nil {
		fmt.Printf("\n  Content: (read error: %v)\n", err)
	} else {
		preview := strings.TrimSpace(html)
		if len(preview) > 500 {
			// Find a good cutoff point
			cutoff := preview[:500]
			if idx := strings.LastIndex(cutoff, "\n"); idx > 400 {
				cutoff = cutoff[:idx]
			}
			preview = cutoff
		}
		fmt.Println("\n  [Content Preview]")
		fmt.Println("  " + strings.Repeat("─", 56))
		for _, line := range strings.Split(preview, "\n") {
			if strings.TrimSpace(line) != "" {
				fmt.Println("  " + line)
			}
		}
		fmt.Println("  ...")
	}

	// Version history count
	versions, _ := mgr.ListVersions(name)
	if len(versions) > 0 {
		fmt.Printf("\n  Versions: %d (latest: %s)\n",
			len(versions), versions[0].Version)
		fmt.Printf("  Run 'wukong apps history %s' for details.\n", name)
	}

	fmt.Println()
	return nil
}

// ==========================================================================
// apps create
// ==========================================================================

func newAppsCreateCmd() *cobra.Command {
	var (
		configPath  string
		appName     string
		description string
		template    string
		htmlFile    string
		force       bool
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new HTML application",
		Long: `Create a new HTML application from a template, an HTML file,
or with blank content.

Templates: blank, calculator, dashboard, form, notes

Examples:
  wukong apps create --name my-app --desc "My app"
  wukong apps create --name calc --template calculator
  wukong apps create --name page --html-file ./index.html
  wukong apps create --name my-app --force  # Overwrite existing app`,
		RunE: runAppsCreate,
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file")
	cmd.Flags().StringVarP(
		&appName, "name", "n", "",
		"App name (required)")
	cmd.Flags().StringVarP(
		&description, "description", "d", "",
		"App description")
	cmd.Flags().StringVarP(
		&template, "template", "t", "",
		"Template to use: blank, calculator, dashboard, form, notes")
	cmd.Flags().StringVarP(
		&htmlFile, "html-file", "f", "",
		"Path to HTML file to import")
	cmd.Flags().BoolVarP(
		&force, "force", "F", false,
		"Overwrite existing app if it exists")

	return cmd
}

func runAppsCreate(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	name, _ := cmd.Flags().GetString("name")
	desc, _ := cmd.Flags().GetString("description")
	tmpl, _ := cmd.Flags().GetString("template")
	htmlFile, _ := cmd.Flags().GetString("html-file")
	force, _ := cmd.Flags().GetBool("force")

	if name == "" {
		return fmt.Errorf("--name is required")
	}

	mgr, cleanup, err := createAppsManager(configPath)
	if err != nil {
		return err
	}
	defer cleanup()

	if _, ok := mgr.GetApp(name); ok {
		if !force {
			return fmt.Errorf("app %q already exists. Use --force to overwrite", name)
		}
		if err := mgr.DeleteApp(name); err != nil {
			return fmt.Errorf("delete existing app: %w", err)
		}
		fmt.Printf("Deleted existing app %q\n", name)
	}

	var app apps.AppInfo

	if htmlFile != "" {
		// Create from file
		content, err := os.ReadFile(htmlFile)
		if err != nil {
			return fmt.Errorf("read html file: %w", err)
		}
		app, err = mgr.CreateAppFromImport(name, desc, string(content))
		if err != nil {
			return fmt.Errorf("import app: %w", err)
		}
		fmt.Printf("App %q imported from %s\n", name, htmlFile)
	} else if tmpl != "" {
		// Create from template
		templateType := apps.TemplateType(tmpl)
		app, err = mgr.CreateAppWithTemplate(name, desc, templateType)
		if err != nil {
			return fmt.Errorf("create from template: %w", err)
		}
		fmt.Printf("App %q created from %s template\n", name, tmpl)
	} else {
		// Create blank app
		app, err = mgr.CreateApp(name, desc, "")
		if err != nil {
			return fmt.Errorf("create app: %w", err)
		}
		fmt.Printf("App %q created\n", name)
	}

	fmt.Printf("  Type:   %s\n", app.Type)
	fmt.Printf("  Status: %s\n", app.Status)
	fmt.Printf("  File:   %s\n", app.FilePath)
	fmt.Println()

	// List available templates
	templates := apps.ListTemplates()
	if len(templates) > 0 {
		fmt.Println("Available templates for future use:")
		for _, t := range templates {
			fmt.Printf("  - %s: %s\n", t.Name, t.Description)
		}
	}

	return nil
}

// ==========================================================================
// apps delete
// ==========================================================================

func newAppsDeleteCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:     "delete <app-name>",
		Aliases: []string{"rm"},
		Short:   "Delete an HTML application",
		Long: `Delete an application and all its files.
This operation cannot be undone.

Examples:
  wukong apps delete my-app
  wukong apps rm my-app`,
		RunE: runAppsDelete,
		Args: cobra.ExactArgs(1),
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file")

	return cmd
}

func runAppsDelete(cmd *cobra.Command, args []string) error {
	name := args[0]
	configPath, _ := cmd.Flags().GetString("config")

	mgr, cleanup, err := createAppsManager(configPath)
	if err != nil {
		return err
	}
	defer cleanup()

	if _, ok := mgr.GetApp(name); !ok {
		return fmt.Errorf("app %q not found", name)
	}

	if err := mgr.DeleteApp(name); err != nil {
		return fmt.Errorf("delete app: %w", err)
	}

	fmt.Printf("App %q deleted.\n", name)
	return nil
}

// ==========================================================================
// apps history
// ==========================================================================

func newAppsHistoryCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "history <app-name>",
		Short: "View version history of an app",
		Long: `Display the version history of an app including
version numbers, timestamps, sizes, and labels.

Examples:
  wukong apps history my-app`,
		RunE: runAppsHistory,
		Args: cobra.ExactArgs(1),
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file")

	return cmd
}

func runAppsHistory(cmd *cobra.Command, args []string) error {
	name := args[0]
	configPath, _ := cmd.Flags().GetString("config")

	mgr, cleanup, err := createAppsManager(configPath)
	if err != nil {
		return err
	}
	defer cleanup()

	if _, ok := mgr.GetApp(name); !ok {
		return fmt.Errorf("app %q not found", name)
	}

	versions, err := mgr.ListVersions(name)
	if err != nil {
		return fmt.Errorf("list versions: %w", err)
	}

	if len(versions) == 0 {
		fmt.Println("No version history for this app.")
		return nil
	}

	fmt.Printf("Version history for %s (%d versions):\n\n", name, len(versions))
	fmt.Printf("  %-10s %-20s %-10s %s\n",
		"VERSION", "TIMESTAMP", "SIZE", "LABEL")
	fmt.Println("  " + strings.Repeat("─", 55))

	for _, v := range versions {
		label := v.Label
		if label == "" {
			label = "-"
		}
		fmt.Printf("  %-10s %-20s %-10s %s\n",
			v.Version,
			v.Timestamp.Format("2006-01-02 15:04:05"),
			formatSize(v.Size),
			label)
	}

	fmt.Printf("\nMax history: %d versions per app\n", 20)
	return nil
}

// ==========================================================================
// apps view — preview an app in the browser
// ==========================================================================

func newAppsViewCmd() *cobra.Command {
	var (
		configPath string
		port       int
	)

	cmd := &cobra.Command{
		Use:   "view <app-name>",
		Short: "Preview a cloned/packaged app in the browser",
		Long: `Start a local HTTP server and open the app in your default browser.

Examples:
  wukong apps view example.com
  wukong apps view example.com --port 8800`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			mgr, cleanup, err := createAppsManager(configPath)
			if err != nil {
				return err
			}
			defer cleanup()

			app, ok := mgr.GetApp(name)
			if !ok {
				return fmt.Errorf("app %q not found", name)
			}

			serveDir := app.AppDir
			if serveDir == "" {
				serveDir = filepath.Dir(app.FilePath)
			}

			return previewApp(cmd.Context(), serveDir, port, name)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to config file")
	cmd.Flags().IntVarP(&port, "port", "p", 0, "Port to listen on (0=auto-select)")

	return cmd
}

// ==========================================================================
// apps export
// ==========================================================================

func newAppsExportCmd() *cobra.Command {
	var (
		configPath string
		outputDir  string
	)

	cmd := &cobra.Command{
		Use:   "export <app-name>",
		Short: "Export an app to a single HTML file",
		Long: `Export an application as a self-contained HTML file
suitable for sharing or deployment.

Examples:
  wukong apps export my-app
  wukong apps export my-app --output ./exports/`,
		RunE: runAppsExport,
		Args: cobra.ExactArgs(1),
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file")
	cmd.Flags().StringVarP(
		&outputDir, "output", "o", "",
		"Output directory (default: current directory)")

	return cmd
}

func runAppsExport(cmd *cobra.Command, args []string) error {
	name := args[0]
	configPath, _ := cmd.Flags().GetString("config")
	outputDir, _ := cmd.Flags().GetString("output")

	if outputDir == "" {
		outputDir = "."
	}

	mgr, cleanup, err := createAppsManager(configPath)
	if err != nil {
		return err
	}
	defer cleanup()

	if _, ok := mgr.GetApp(name); !ok {
		return fmt.Errorf("app %q not found", name)
	}

	outputPath := filepath.Join(outputDir, name+".html")
	result, err := mgr.ExportApp(name, outputPath)
	if err != nil {
		return fmt.Errorf("export app: %w", err)
	}

	fmt.Printf("App %q exported to: %s\n", name, result.OutputPath)
	fmt.Printf("  Size: %s\n", formatSize(result.Size))
	fmt.Println()
	return nil
}

// ==========================================================================
// helpers
// ==========================================================================

// createAppsManager creates an apps manager for CLI use.
func createAppsManager(configPath string) (*apps.Manager, func(), error) {
	loader, err := config.NewLoader(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("parse config: %w", err)
	}

	if !wukongCfg.Apps.Enabled {
		return nil, nil, fmt.Errorf(
			"apps subsystem is disabled. Enable it in config.yaml:\n" +
				"  apps:\n" +
				"    enabled: true\n" +
				"    app_dir: .wukong/apps")
	}

	mgr, err := apps.NewManager(&wukongCfg.Apps)
	if err != nil {
		return nil, nil, fmt.Errorf("create apps manager: %w", err)
	}

	cleanup := func() {
		// Manager has no Close, but we can do any needed cleanup here
	}
	return mgr, cleanup, nil
}

// formatSize formats a byte size for display.
func formatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

// ==========================================================================
// apps pack
// ==========================================================================

func newAppsPackCmd() *cobra.Command {
	var (
		configPath  string
		format      string
		outputPath  string
		baseBinary  string
		iconPath    string
		compress    bool
		incremental bool
		language    string
		title       string
		description string
		date        string
		creator     string
	)

	cmd := &cobra.Command{
		Use:   "pack <app-name>",
		Short: "Package an app into a distributable format",
		Long: `Package an application into a distributable format:
  html   – Self-contained HTML directory
  zim    – ZIM archive (Kiwix compatible, offline reader)
  binary – Standalone executable with embedded content
  app    – Desktop application bundle (.app / .AppDir / .exe)

ZIM archives include rich metadata (title, language, date, source)
and support incremental builds (--incremental) for fast repacks.

Examples:
  wukong apps pack v5.monibuca.com --format zim
  wukong apps pack my-site -f zim --incremental --title "My Site"
  wukong apps pack my-site -f binary -o ./dist/my-site
  wukong apps pack my-site -f zim --language zho --date 2026-06-24`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := args[0]
			cfgPath, _ := cmd.Flags().GetString("config")

			mgr, cleanup, err := createAppsManager(cfgPath)
			if err != nil {
				return err
			}
			defer cleanup()

			fmt.Printf("Packaging %q as %s ...\n", appName, format)

			opts := apps.PackOptions{
				Format:      format,
				OutputPath:  outputPath,
				BaseBinary:  baseBinary,
				IconPath:    iconPath,
				Compress:    compress,
				Incremental: incremental,
				Language:    language,
				Title:       title,
				Description: description,
				Date:        date,
				Creator:     creator,
			}

			result, err := mgr.PackApp(cmd.Context(), appName, opts)
			if err != nil {
				return fmt.Errorf("pack: %w", err)
			}

			fmt.Printf("\nPack complete!\n")
			fmt.Printf("  Format:    %s\n", result.Format)
			fmt.Printf("  Output:    %s\n", result.OutputPath)
			fmt.Printf("  Size:      %s\n", formatSize(result.SizeBytes))
			fmt.Printf("  Duration:  %s\n", result.Duration)
			fmt.Printf("  Files:     %d\n", result.FilesProcessed)
			fmt.Printf("  Assets:    %d\n", result.AssetsIncluded)
			if result.ClustersReused > 0 || result.ClustersCompressed > 0 {
				fmt.Printf("  Cache:     %d clusters reused, %d compressed\n",
					result.ClustersReused, result.ClustersCompressed)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to config file")
	cmd.Flags().StringVarP(&format, "format", "f", "zim",
		"Output format: html | zim | binary | app")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "",
		"Output file path (auto-generated if empty)")
	cmd.Flags().StringVar(&baseBinary, "base-binary", "",
		"Base executable for binary/app format")
	cmd.Flags().StringVar(&iconPath, "icon", "",
		"Icon file path for app format")
	cmd.Flags().BoolVar(&compress, "compress", false,
		"Enable compression (zstd for ZIM)")
	cmd.Flags().BoolVar(&incremental, "incremental", false,
		"Incremental ZIM build (reuse unchanged clusters)")
	cmd.Flags().StringVar(&language, "language", "",
		"ZIM language code (ISO 639-3, default: eng)")
	cmd.Flags().StringVar(&title, "title", "",
		"ZIM title (auto-detected from main page if empty)")
	cmd.Flags().StringVar(&description, "description", "",
		"ZIM description")
	cmd.Flags().StringVar(&date, "date", "",
		"ZIM date YYYY-MM-DD (default: today)")
	cmd.Flags().StringVar(&creator, "creator", "",
		"ZIM creator (default: Wukong)")

	return cmd
}

// ==========================================================================
// apps clone
// ==========================================================================

func newAppsCloneCmd() *cobra.Command {
	var (
		configPath       string
		outputDir        string
		maxPages         int
		maxDepth         int
		traversal        string
		scopePrefix      string
		scopeAnchor      string
		subdomains       bool
		exclude          []string
		scroll           bool
		timeout          int
		renderTimeout    int
		settle           int
		workers          int
		assetWorkers     int
		force            bool
		refresh          bool
		incremental      bool
		chromePath       string
		assetSameDomain  bool
		assetDomains     []string
		noSitemap        bool
		noRobots         bool
		crawlDelay       int
		noAntibot        bool
		noAntibotAutoEsc bool
		cookieFile       string
		chromeProfile    string
		noHeadless       bool
		noChromeProfile  bool
		noStealth        bool
		browserBackend   string
		keepMedia        bool
		skipExt          []string
		allowDownloads   bool
	)

	cmd := &cobra.Command{
		Use:   "clone <url>",
		Short: "Clone a website as an offline app",
		Long: `Clone a website to a local directory with JavaScript stripped out.
Uses headless Chrome to render pages, downloads all assets,
and creates a fully offline-browsable mirror.
Stealth anti-detection and Chrome profile are enabled by default.

Examples:
  wukong apps clone https://example.com
  wukong apps clone example.com --max-pages 50 --max-depth 2
  wukong apps clone example.com --subdomains --scroll
  wukong apps clone example.com --workers 8 --incremental
  wukong apps clone example.com --traversal dfs`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			seedURL := args[0]
			cfgPath, _ := cmd.Flags().GetString("config")

			mgr, cleanup, err := createAppsManager(cfgPath)
			if err != nil {
				return err
			}
			defer cleanup()

			fmt.Printf("Cloning %s ...\n", seedURL)

			opts := apps.CloneOptions{
				OutputDir:       outputDir,
				MaxPages:        maxPages,
				MaxDepth:        maxDepth,
				Traversal:       traversal,
				ScopePrefix:     scopePrefix,
				ScopeAnchor:     scopeAnchor,
				Exclude:         exclude,
				Subdomains:      subdomains,
				Scroll:          scroll,
				Timeout:         timeout,
				RenderTimeout:   renderTimeout,
				Settle:          settle,
				Workers:         workers,
				AssetWorkers:    assetWorkers,
				Force:           force,
				Refresh:         refresh,
				ChromePath:      chromePath,
				CookieFile:      cookieFile,
				ChromeProfile:   chromeProfile,
				NoChromeProfile: noChromeProfile,
				NoStealth:       noStealth,
				BrowserBackend:  browserBackend,
				KeepMedia:       keepMedia,
				SkipExt:         skipExt,
				AllowDownloads:  allowDownloads,
				AssetDomains:    assetDomains,
			}
			if incremental {
				v := true
				opts.Incremental = &v
			}
			if noAntibot {
				v := false
				opts.AntibotEnabled = &v
			}
			if noAntibotAutoEsc {
				v := false
				opts.AntibotAutoEscalate = &v
			}
			if noRobots {
				v := false
				opts.RespectRobots = &v
			}
			if cmd.Flags().Changed("asset-same-domain") {
				v := assetSameDomain
				opts.AssetSameDomain = &v
			}

			// Respect flags default (non-flag bools are false by default, meaning
			// nil pointer will use defaults in EnhancedCloner which are true).
			app, result, err := mgr.CloneApp(cmd.Context(), seedURL, opts)
			if err != nil {
				return fmt.Errorf("clone: %w", err)
			}

			fmt.Printf("\nClone complete!\n")
			fmt.Printf("  App:      %s\n", app.Name)
			fmt.Printf("  Source:   %s\n", app.SourceURL)
			fmt.Printf("  Pages:    %d\n", result.Pages)
			fmt.Printf("  Assets:   %d\n", result.Assets)
			fmt.Printf("  Size:     %s\n", formatSize(result.SizeBytes))
			fmt.Printf("  Duration: %s\n", result.Duration)
			if result.DedupFiles > 0 {
				fmt.Printf("  Dedup:    %d files, %s saved\n",
					result.DedupFiles, formatSize(result.DedupBytesSaved))
			}
			if result.AntibotDetections > 0 {
				fmt.Printf("  Antibot:  %d blocking events detected\n",
					result.AntibotDetections)
			}
			fmt.Printf("  Output:   %s\n", result.OutputDir)

			if len(result.Errors) > 0 {
				fmt.Printf("\nErrors (%d):\n", len(result.Errors))
				hasTimeout := false
				for _, e := range result.Errors {
					fmt.Printf("  - %s\n", e)
					if strings.Contains(e, "deadline exceeded") ||
						strings.Contains(e, "timeout") {
						hasTimeout = true
					}
				}
				if hasTimeout {
					fmt.Printf("\nHint: slow site? Try a longer timeout:\n"+
						"  wukong apps clone %s --timeout 120\n",
						seedURL)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to config file")
	cmd.Flags().StringVarP(&outputDir, "out", "o", "", "Output root; the mirror lands in <out>/<host>/")
	cmd.Flags().IntVarP(&maxPages, "max-pages", "p", 0, "Maximum pages to clone (0 = unlimited)")
	cmd.Flags().IntVarP(&maxDepth, "max-depth", "d", 0, "Maximum link depth (0 = unlimited)")
	cmd.Flags().StringVar(&scopePrefix, "scope-prefix", "", "Only crawl paths starting with this prefix")
	cmd.Flags().StringVar(&scopeAnchor, "scope-anchor", "", "Only crawl pages with a specific URL fragment/anchor (e.g. 'leaders' for #leaders)")
	cmd.Flags().StringArrayVar(&exclude, "exclude", nil, "Path prefixes to skip (repeatable)")
	cmd.Flags().BoolVar(&subdomains, "subdomains", false, "Include subdomains")
	cmd.Flags().BoolVar(&scroll, "scroll", false, "Auto-scroll each page to trigger lazy loading")
	cmd.Flags().IntVarP(&workers, "workers", "w", 0, "Concurrent page renderers (default 4)")
	cmd.Flags().BoolVar(&noRobots, "no-robots", true, "Ignore robots.txt (be nice)")
	cmd.Flags().IntVar(&crawlDelay, "crawl-delay", 0, "Override robots.txt Crawl-delay in milliseconds")
	cmd.Flags().IntVar(&timeout, "timeout", 0, "HTTP request timeout in seconds (default 60)")
	cmd.Flags().IntVar(&renderTimeout, "render-timeout", 0, "Page render hard timeout in seconds (default 30)")
	cmd.Flags().IntVar(&settle, "settle", 0, "Network idle settle time in ms (default 1500)")
	cmd.Flags().IntVar(&assetWorkers, "asset-workers", 0, "Concurrent asset downloaders (default same as workers)")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Delete any existing mirror for the host first")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "Re-render all pages")
	cmd.Flags().BoolVar(&incremental, "incremental", false, "Use ETag/Last-Modified for incremental updates")
	cmd.Flags().BoolVar(&assetSameDomain, "asset-same-domain", false, "Only download assets from same domain")
	cmd.Flags().StringArrayVar(&assetDomains, "asset-domain", nil, "Additional domain to allow assets from (repeatable, e.g. --asset-domain cdn.example.com)")
	cmd.Flags().BoolVar(&noSitemap, "no-sitemap", false, "Disable sitemap URL discovery")
	cmd.Flags().StringVar(&chromePath, "chrome", "", "Path to the Chrome/Chromium executable")
	cmd.Flags().StringVar(&chromePath, "chrome-path", "", "Path to Chrome/Chromium executable (alias for --chrome)")
	cmd.Flags().BoolVar(&noAntibot, "no-antibot", false, "Disable auto anti-bot detection and escalation")
	cmd.Flags().BoolVar(&noAntibotAutoEsc, "no-antibot-auto", false, "Detect blocks but skip auto-escalation")
	cmd.Flags().StringVar(&cookieFile, "cookies", "", "Netscape-format cookie file for authenticated cloning")
	cmd.Flags().StringVar(&chromeProfile, "chrome-profile", "", "Chrome user-data-dir (default ./wukong_chrome_profile)")
	cmd.Flags().BoolVar(&noHeadless, "no-headless", false, "Show visible Chrome window (for manual Turnstile solving)")
	cmd.Flags().BoolVar(&noChromeProfile, "no-chrome-profile", false, "Disable Chrome profile persistence")
	cmd.Flags().BoolVar(&noStealth, "no-stealth", false, "Disable stealth anti-detection (on by default)")
	cmd.Flags().StringVar(&browserBackend, "browser-backend", "", "Browser backend: chromedp or rod (default from config)")
	cmd.Flags().BoolVar(&keepMedia, "keep-media", false, "Download media files (video, audio, PDF, archives) that are normally skipped")
	cmd.Flags().StringArrayVar(&skipExt, "skip-ext", nil, "Additional file extensions to skip (repeatable, e.g. --skip-ext .mp3 --skip-ext .pdf)")
	cmd.Flags().BoolVar(&allowDownloads, "allow-downloads", false, "Allow the browser to auto-download files (default: disabled — cloner manages assets)")

	return cmd
}

// previewApp starts a local HTTP server and opens the app in browser.
func previewApp(ctx context.Context, serveDir string, port int, name string) error {
	srv := server.NewServer(server.Config{
		Port:    port,
		RootDir: serveDir,
		AppName: name,
	})

	addr, err := srv.StartAndWait(ctx)
	if err != nil {
		return fmt.Errorf("start preview server: %w", err)
	}

	fmt.Printf("  Preview: %s\n", addr)

	if err := openBrowser(addr); err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: could not open browser: %v\n", err)
	}

	<-ctx.Done()
	return srv.Stop()
}

// ==========================================================================
// apps probe
// ==========================================================================

func newAppsProbeCmd() *cobra.Command {
	var (
		timeout int
	)

	cmd := &cobra.Command{
		Use:   "probe <url>",
		Short: "Probe a website for anti-bot measures",
		Long: `Probe a website to detect anti-bot measures including WAF,
JavaScript challenges, rate limiting, and security headers.
Returns a threat assessment with recommendations for successful cloning.

Examples:
  wukong apps probe https://example.com
  wukong apps probe https://example.com --timeout 60`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targetURL := args[0]

			ctx, cancel := context.WithTimeout(context.Background(),
				time.Duration(timeout)*time.Second)
			defer cancel()

			fmt.Printf("Probing %s ...\n", targetURL)
			fmt.Println()

			p := prober.NewProber()
			profile := p.Probe(ctx, targetURL)

			fmt.Println("=" + strings.Repeat("-", 60))
			fmt.Printf("ANTIBOT PROFILE for %s\n", targetURL)
			fmt.Println("=" + strings.Repeat("-", 60))

			levelColors := map[prober.AntibotLevel]string{
				prober.LevelNone:     "\033[92m",
				prober.LevelLow:      "\033[94m",
				prober.LevelMedium:   "\033[93m",
				prober.LevelHigh:     "\033[91m",
				prober.LevelCritical: "\033[41m",
			}
			levelNames := map[prober.AntibotLevel]string{
				prober.LevelNone:     "NONE",
				prober.LevelLow:      "LOW",
				prober.LevelMedium:   "MEDIUM",
				prober.LevelHigh:     "HIGH",
				prober.LevelCritical: "CRITICAL",
			}

			color := levelColors[profile.Level]
			name := levelNames[profile.Level]
			fmt.Printf("\nRisk Level: %s%s\033[0m\n", color, name)
			fmt.Printf("Reason: %s\n", profile.LevelReason)

			if profile.WAF != "" {
				fmt.Printf("\nDetected WAF: %s\n", profile.WAF)
			}

			if profile.HasJSChallenge {
				fmt.Println("Has JS Challenge: Yes")
			}

			if profile.HasRateLimit {
				fmt.Println("Has Rate Limit: Yes")
			}

			fmt.Println("\n--- Individual Probe Results ---")
			for _, r := range profile.ProbeResults {
				status := "OK"
				if r.Detected {
					status = fmt.Sprintf("DETECTED (%.1f)", r.Confidence)
				}
				fmt.Printf("\n[%s] %s\n", r.Dimension, status)
				if r.Message != "" {
					fmt.Printf("     Message: %s\n", r.Message)
				}
				if r.Error != nil {
					fmt.Printf("     Error: %v\n", r.Error)
				}
				fmt.Printf("     Duration: %v\n", r.Duration)
			}

			fmt.Println("\n--- Recommendations ---")
			for i, rec := range profile.Recommendations {
				fmt.Printf("%d. %s\n", i+1, rec)
			}

			return nil
		},
	}

	cmd.Flags().IntVar(&timeout, "timeout", 60, "Total timeout in seconds")

	return cmd
}

// ==========================================================================
// apps download
// ==========================================================================

func newAppsDownloadCmd() *cobra.Command {
	var (
		configPath string
		outputDir  string
		maxPages   int
		maxDepth   int
		workers    int
		headless   bool
		stealth    bool
		antibot    bool
		resume     bool
		force      bool
		refresh    bool
		fileExts   []string
	)

	cmd := &cobra.Command{
		Use:   "download <url> [extensions...]",
		Short: "Download files from a website",
		Long: `Download specific file types from a website by crawling its pages.
Supports document formats like PDF, Word, Excel, PowerPoint, and more.

Default file extensions: .txt, .csv, .doc, .docx, .xls, .xlsx, .ppt, .pptx, .pdf

Examples:
  wukong apps download https://example.com
  wukong apps download https://example.com .pdf .docx
  wukong apps download https://example.com --file-extensions .pdf,.docx
  wukong apps download https://example.com --file-extensions .pdf --file-extensions .docx
  wukong apps download https://example.com --max-pages 100 --max-depth 3
  wukong apps download https://example.com --output ./downloads`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			seedURL := strings.ReplaceAll(args[0], "`", "")
			seedURL = strings.TrimSpace(strings.Trim(seedURL, "\"'"))
			if seedURL == "" {
				return fmt.Errorf("URL is required")
			}

			if len(args) > 1 {
				for _, ext := range args[1:] {
					ext = strings.ReplaceAll(ext, "`", "")
					ext = strings.TrimSpace(strings.Trim(ext, "\"'"))
					if ext != "" {
						fileExts = append(fileExts, ext)
					}
				}
			}
			cfgPath, _ := cmd.Flags().GetString("config")

			mgr, cleanup, err := createAppsManager(cfgPath)
			if err != nil {
				return err
			}
			defer cleanup()

			fmt.Printf("Downloading from: %s\n", seedURL)

			opts := apps.DownloadOptions{
				OutputDir: outputDir,
				MaxPages:  maxPages,
				MaxDepth:  maxDepth,
				Workers:   workers,
				Headless:  headless,
				Stealth:   stealth,
				Antibot:   antibot,
				Resume:    resume,
				Force:     force,
				Refresh:   refresh,
			}

			if len(fileExts) > 0 {
				extMap := make(map[string]bool)
				for _, ext := range fileExts {
					ext = strings.TrimSpace(ext)
					ext = strings.Trim(ext, "`\"'")
					if ext == "" {
						continue
					}
					parts := strings.Split(ext, ",")
					for _, part := range parts {
						part = strings.TrimSpace(part)
						if part == "" {
							continue
						}
						if !strings.HasPrefix(part, ".") {
							part = "." + part
						}
						extMap[strings.ToLower(part)] = true
					}
				}
				opts.FileExts = extMap
			}

			result, err := mgr.DownloadFiles(cmd.Context(), seedURL, opts)
			if err != nil {
				return fmt.Errorf("download: %w", err)
			}

			fmt.Printf("\nDownload complete!\n")
			fmt.Printf("  Files downloaded: %d\n", result.FilesDownloaded)
			fmt.Printf("  Files skipped:    %d\n", result.FilesSkipped)
			fmt.Printf("  Files failed:     %d\n", result.FilesFailed)
			fmt.Printf("  Total size:       %s\n", formatSize(result.TotalSize))
			fmt.Printf("  Duration:         %s\n", result.Duration)
			fmt.Printf("  Output directory: %s\n", result.OutputDir)

			if result.AntibotStats != "" {
				fmt.Printf("\n  Anti-bot: %s\n", result.AntibotStats)
			}

			if result.FilesDownloaded > 0 {
				fmt.Printf("\nDownloaded files:\n")
				for _, f := range result.Files {
					fmt.Printf("  - %s (%s)\n", f.FileName, formatSize(f.Size))
				}
			}

			if len(result.Errors) > 0 {
				fmt.Printf("\nErrors (%d):\n", len(result.Errors))
				showCount := 10
				if len(result.Errors) < showCount {
					showCount = len(result.Errors)
				}
				for _, e := range result.Errors[:showCount] {
					fmt.Printf("  - %s\n", e)
				}
				if len(result.Errors) > showCount {
					fmt.Printf("  ... and %d more errors\n", len(result.Errors)-showCount)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to config file")
	cmd.Flags().StringVarP(&outputDir, "output", "o", "", "Output directory for downloaded files")
	cmd.Flags().IntVarP(&maxPages, "max-pages", "p", 50, "Maximum number of pages to crawl")
	cmd.Flags().IntVarP(&maxDepth, "max-depth", "d", 0, "Maximum link depth to follow (0 = unlimited)")
	cmd.Flags().IntVarP(&workers, "workers", "w", 4, "Number of concurrent download workers")
	cmd.Flags().BoolVar(&headless, "headless", true, "Run browser in headless mode")
	cmd.Flags().BoolVar(&stealth, "stealth", true, "Enable stealth anti-detection mode")
	cmd.Flags().BoolVar(&antibot, "antibot", true, "Enable anti-bot detection and bypass")
	cmd.Flags().BoolVar(&resume, "resume", true, "Resume from previous download state")
	cmd.Flags().BoolVar(&force, "force", false, "Force restart (delete existing downloads)")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "Refresh already downloaded files")
	cmd.Flags().StringArrayVar(&fileExts, "file-extensions", nil,
		"File extensions to download (repeatable or comma-separated). Default: .txt .csv .doc .docx .xls .xlsx .ppt .pptx .pdf")

	return cmd
}
