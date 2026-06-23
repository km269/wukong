// Package apps provides creation, management, and launching of
// custom HTML standalone window applications.
// Supports multiple app types: custom (user-created), cloned (website clone),
// imported (external HTML). Provides lifecycle management including
// creation, editing, cloning, packaging, and MCP Apps integration.
package apps

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/km269/wukong/internal/apps/clone"
	"github.com/km269/wukong/internal/apps/pack"
	"github.com/km269/wukong/internal/apps/server"
	"github.com/km269/wukong/internal/config"
)

// AppType defines the type of an HTML application.
type AppType string

const (
	// AppTypeCustom 表示用户手动创建的应用.
	AppTypeCustom AppType = "custom"
	// AppTypeCloned 表示通过网站克隆创建的应用.
	AppTypeCloned AppType = "cloned"
	// AppTypeImported 表示从外部导入的应用.
	AppTypeImported AppType = "imported"
)

// AppStatus defines the lifecycle status of an application.
type AppStatus string

const (
	// AppStatusDraft 表示草稿状态，尚未发布.
	AppStatusDraft AppStatus = "draft"
	// AppStatusActive 表示活跃状态，可正常使用.
	AppStatusActive AppStatus = "active"
	// AppStatusArchived 表示已归档，不再活跃.
	AppStatusArchived AppStatus = "archived"
)

// AppInfo holds metadata about a custom HTML app.
type AppInfo struct {
	// Name 应用名称，用于标识和文件命名.
	Name string `json:"name"`
	// Description 应用描述.
	Description string `json:"description"`
	// FilePath 主 HTML 文件的绝对路径.
	FilePath string `json:"file_path"`
	// CreatedAt 创建时间.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt 最后更新时间.
	UpdatedAt time.Time `json:"updated_at"`
	// Size 应用总大小（字节）.
	Size int64 `json:"size"`

	// Type 应用类型：custom | cloned | imported.
	Type AppType `json:"type"`
	// Status 应用状态：draft | active | archived.
	Status AppStatus `json:"status"`
	// Version 版本号，格式如 "1.0.0".
	Version string `json:"version"`
	// SourceURL 克隆来源 URL（仅 cloned 类型）.
	SourceURL string `json:"source_url,omitempty"`
	// Pages 页面数量（仅 cloned 类型）.
	Pages int `json:"pages,omitempty"`
	// Assets 资源文件数量（CSS/JS/图片等）.
	Assets int `json:"assets,omitempty"`
	// IconPath 应用图标路径.
	IconPath string `json:"icon_path,omitempty"`
	// AppDir 应用目录路径（对于多文件应用）.
	AppDir string `json:"app_dir,omitempty"`
}

// Manager handles custom HTML app lifecycle.
type Manager struct {
	mu        sync.RWMutex
	cfg       *config.AppsConfig
	apps      map[string]AppInfo
	appDir    string // 应用存储目录的绝对路径
}

// NewManager creates a new apps manager.
func NewManager(cfg *config.AppsConfig) (*Manager, error) {
	appDir := cfg.AppDir
	if appDir == "" {
		appDir = ".wukong_apps"
	}

	// 确保目录存在
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return nil, fmt.Errorf("create apps dir: %w", err)
	}

	// 转换为绝对路径
	absAppDir, err := filepath.Abs(appDir)
	if err != nil {
		absAppDir = appDir
	}

	m := &Manager{
		cfg:    cfg,
		apps:   make(map[string]AppInfo),
		appDir: absAppDir,
	}

	// 加载已有应用
	m.loadExisting()
	return m, nil
}

// CreateApp creates a new HTML app with default type (custom) and status (active).
func (m *Manager) CreateApp(
	name, description, htmlContent string,
) (AppInfo, error) {
	return m.CreateAppWithType(name, description, htmlContent, AppTypeCustom, AppStatusActive)
}

// CreateAppWithType creates a new HTML app with specified type and status.
func (m *Manager) CreateAppWithType(
	name, description, htmlContent string,
	appType AppType, status AppStatus,
) (AppInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	filename := sanitizeAppName(name) + ".html"
	filePath := filepath.Join(m.appDir, filename)

	if err := os.WriteFile(filePath, []byte(htmlContent), 0644); err != nil {
		return AppInfo{}, fmt.Errorf("write app file: %w", err)
	}

	info, _ := os.Stat(filePath)
	now := time.Now()
	app := AppInfo{
		Name:        name,
		Description: description,
		FilePath:    filePath,
		CreatedAt:   now,
		UpdatedAt:   now,
		Type:        appType,
		Status:      status,
		Version:     "1.0.0",
		AppDir:      m.appDir,
	}
	if info != nil {
		app.Size = info.Size()
	}

	m.apps[name] = app
	return app, nil
}

// CreateAppWithTemplate creates a new HTML app using a predefined template.
func (m *Manager) CreateAppWithTemplate(
	name, description string, templateType TemplateType,
) (AppInfo, error) {
	htmlContent := GetTemplate(templateType, name)
	return m.CreateApp(name, description, htmlContent)
}

// CreateAppFromImport creates an app from imported HTML content.
func (m *Manager) CreateAppFromImport(
	name, description, htmlContent string,
) (AppInfo, error) {
	return m.CreateAppWithType(name, description, htmlContent, AppTypeImported, AppStatusActive)
}

// CloneApp clones a website and creates a new app from it.
// This is the main entry point for website cloning functionality.
func (m *Manager) CloneApp(ctx context.Context, seedURL string, opts CloneOptions) (AppInfo, *CloneResult, error) {
	// 创建克隆器
	clonerOpts := clone.DefaultOptions()
	if opts.MaxPages > 0 {
		clonerOpts.MaxPages = opts.MaxPages
	}
	if opts.MaxDepth > 0 {
		clonerOpts.MaxDepth = opts.MaxDepth
	}
	if opts.Subdomains {
		clonerOpts.Subdomains = true
	}
	if opts.Scroll {
		clonerOpts.Scroll = true
	}
	if opts.Timeout > 0 {
		clonerOpts.Timeout = opts.Timeout
	}
	if opts.Workers > 0 {
		clonerOpts.Workers = opts.Workers
	}

	// 设置输出目录到 apps 目录下
	host := extractHost(seedURL)
	outputDir := filepath.Join(m.appDir, "cloned", host)
	clonerOpts.OutputDir = outputDir

	c := clone.NewCloner(clonerOpts)

	// 执行克隆
	cloneRes, err := c.Clone(ctx, seedURL)
	if err != nil {
		return AppInfo{}, nil, fmt.Errorf("clone website: %w", err)
	}

	// 转换结果
	result := &CloneResult{
		Success:   cloneRes.Success,
		SeedURL:   cloneRes.SeedURL,
		Host:      cloneRes.Host,
		OutputDir: cloneRes.OutputDir,
		Pages:     cloneRes.Pages,
		Assets:    cloneRes.Assets,
		SizeBytes: cloneRes.SizeBytes,
		Duration:  cloneRes.Duration.String(),
		Errors:    cloneRes.Errors,
	}

	// 创建应用信息
	now := time.Now()
	app := AppInfo{
		Name:        host,
		Description: fmt.Sprintf("克隆于 %s", seedURL),
		FilePath:    filepath.Join(outputDir, "pages", "index.html"),
		CreatedAt:   now,
		UpdatedAt:   now,
		Type:        AppTypeCloned,
		Status:      AppStatusActive,
		Version:     "1.0.0",
		SourceURL:   seedURL,
		Pages:       result.Pages,
		Assets:      result.Assets,
		Size:        result.SizeBytes,
		AppDir:      outputDir,
	}

	m.mu.Lock()
	m.apps[host] = app
	m.mu.Unlock()

	return app, result, nil
}

// CloneOptions defines options for website cloning.
type CloneOptions struct {
	MaxPages    int  // 最大页面数量（0 = 无限制）
	MaxDepth    int  // 最大链接深度（0 = 无限制）
	Subdomains  bool // 是否包含子域名
	Scroll      bool // 是否滚动加载懒加载内容
	Timeout     int  // 页面渲染超时（秒）
	Workers     int  // 并发工作线程数
}

// CloneResult wraps the clone package result for external use.
type CloneResult struct {
	Success    bool
	SeedURL    string
	Host       string
	OutputDir  string
	Pages      int
	Assets     int
	SizeBytes  int64
	Duration   string
	Errors     []string
}

// extractHost extracts the hostname from a URL.
func extractHost(urlStr string) string {
	if !strings.HasPrefix(urlStr, "http://") && !strings.HasPrefix(urlStr, "https://") {
		urlStr = "https://" + urlStr
	}

	parsed, err := url.Parse(urlStr)
	if err != nil {
		// 尝试简单提取
		parts := strings.Split(urlStr, "/")
		if len(parts) > 2 {
			return parts[2]
		}
		return sanitizeAppName(urlStr)
	}

	return parsed.Host
}

// PackApp packages an application into the specified format.
// Supports HTML directory, ZIM archive, self-contained binary, and desktop app.
func (m *Manager) PackApp(ctx context.Context, appName string, opts PackOptions) (*PackResult, error) {
	// 获取应用信息
	m.mu.RLock()
	app, ok := m.apps[appName]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("app %q not found", appName)
	}

	// 确定源目录
	sourceDir := app.AppDir
	if sourceDir == "" {
		// 单文件应用，使用文件所在目录
		sourceDir = filepath.Dir(app.FilePath)
	}

	// 构建打包选项
	packOpts := pack.DefaultOptions()
	packOpts.Format = pack.Format(opts.Format)
	packOpts.OutputPath = opts.OutputPath
	packOpts.AppName = appName
	packOpts.AppDescription = app.Description
	packOpts.AppVersion = app.Version
	packOpts.BaseBinary = opts.BaseBinary
	packOpts.IconPath = opts.IconPath
	packOpts.Compress = opts.Compress

	// 创建打包器
	packer := pack.NewPacker(packOpts)

	// 执行打包
	packRes, err := packer.Pack(ctx, sourceDir)
	if err != nil {
		return nil, fmt.Errorf("pack app: %w", err)
	}

	// 转换结果
	result := &PackResult{
		Success:       packRes.Success,
		Format:        string(packRes.Format),
		OutputPath:    packRes.OutputPath,
		SizeBytes:     packRes.SizeBytes,
		Duration:      packRes.Duration.String(),
		FilesProcessed: packRes.FilesProcessed,
		AssetsIncluded: packRes.AssetsIncluded,
		Errors:        packRes.Errors,
	}

	return result, nil
}

// PackOptions defines options for application packaging.
type PackOptions struct {
	Format        string // html | zim | binary | app
	OutputPath    string // 输出路径
	BaseBinary    string // 基础可执行文件路径（用于 binary/app 格式）
	IconPath      string // 图标路径
	Compress      bool   // 是否压缩
}

// PackResult holds the outcome of a packaging operation.
type PackResult struct {
	Success        bool
	Format         string
	OutputPath     string
	SizeBytes      int64
	Duration       string
	FilesProcessed int
	AssetsIncluded int
	Errors         []string
}

// PreviewServer manages the preview server lifecycle.
type PreviewServer struct {
	srv   *server.Server
	mu    sync.RWMutex
	ctx   context.Context
	cancel context.CancelFunc
}

// PreviewApp starts a local preview server for an app.
func (m *Manager) PreviewApp(ctx context.Context, appName string) (*PreviewResult, error) {
	// 获取应用信息
	m.mu.RLock()
	app, ok := m.apps[appName]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("app %q not found", appName)
	}

	// 确定预览目录
	previewDir := app.AppDir
	if previewDir == "" {
		previewDir = filepath.Dir(app.FilePath)
	}

	// 创建预览服务器
	cfg := server.DefaultConfig()
	cfg.RootDir = previewDir
	cfg.AppName = appName

	srv := server.NewServer(cfg)

	// 创建可取消的上下文
	previewCtx, cancel := context.WithCancel(ctx)

	// 启动服务器
	addr, err := srv.StartAndWait(previewCtx)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start preview server: %w", err)
	}

	return &PreviewResult{
		Success: true,
		URL:     addr,
		AppName: appName,
		Message: fmt.Sprintf("预览服务器已启动，请访问 %s", addr),
		Cancel:  cancel,
	}, nil
}

// PreviewResult holds the result of starting a preview server.
type PreviewResult struct {
	Success bool
	URL     string
	AppName string
	Message string

	// Cancel stops the preview server. Must be called when the
	// preview is no longer needed to avoid resource leaks.
	Cancel context.CancelFunc
}

// Stop gracefully stops the preview server and releases resources.
// It is safe to call multiple times.
func (r *PreviewResult) Stop() {
	if r.Cancel != nil {
		r.Cancel()
	}
}

// ExportApp exports an app to a single HTML file.
func (m *Manager) ExportApp(appName, outputPath string) (*ExportResult, error) {
	// 获取应用信息
	m.mu.RLock()
	app, ok := m.apps[appName]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("app %q not found", appName)
	}

	// 确定源文件
	sourcePath := app.FilePath
	if app.AppDir != "" {
		// 对于多文件应用，尝试找到入口文件
		indexPath := filepath.Join(app.AppDir, "pages", "index.html")
		if exists(indexPath) {
			sourcePath = indexPath
		}
	}

	// 确定输出路径
	if outputPath == "" {
		outputPath = appName + "_export.html"
	}

	// 读取源文件
	content, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("read source file: %w", err)
	}

	// 如果是目录，生成单一 HTML 文件（内联 CSS 和资源）
	if app.AppDir != "" && !isSingleHTMLApp(app) {
		content, err = inlineResources(sourcePath, app.AppDir)
		if err != nil {
			// 如果内联失败，使用原文件
			content, _ = os.ReadFile(sourcePath)
		}
	}

	// 写入输出文件
	if err := os.WriteFile(outputPath, content, 0644); err != nil {
		return nil, fmt.Errorf("write output file: %w", err)
	}

	info, _ := os.Stat(outputPath)

	return &ExportResult{
		Success:   true,
		OutputPath: outputPath,
		Size:      info.Size(),
		Message:   fmt.Sprintf("应用已导出到 %s", outputPath),
	}, nil
}

// ExportResult holds the result of an export operation.
type ExportResult struct {
	Success    bool
	OutputPath string
	Size       int64
	Message    string
}

// isSingleHTMLApp checks if an app is a single HTML file.
func isSingleHTMLApp(app AppInfo) bool {
	if app.AppDir == "" {
		return true
	}
	// 检查 pages 目录
	pagesDir := filepath.Join(app.AppDir, "pages")
	_, err := os.Stat(pagesDir)
	return os.IsNotExist(err)
}

// inlineResources converts a directory-based app to a single self-contained HTML file.
// It inlines CSS styles and converts local image references to base64 data URIs.
func inlineResources(mainFile, appDir string) ([]byte, error) {
	// 读取主文件
	content, err := os.ReadFile(mainFile)
	if err != nil {
		return nil, err
	}

	html := string(content)

	// 1. 内联 <link rel="stylesheet"> 标签
	html = inlineStylesheets(html, appDir)

	// 2. 内联 <img> 标签
	html = inlineImages(html, appDir)

	// 3. 内联 CSS 中的 url() 引用
	html = inlineCSSUrls(html, appDir)

	return []byte(html), nil
}

// inlineStylesheets finds <link rel="stylesheet"> tags and inlines their content.
func inlineStylesheets(html, appDir string) string {
	// 匹配 <link rel="stylesheet" href="...">
	re := regexp.MustCompile(`<link[^>]*rel=["']stylesheet["'][^>]*href=["']([^"']+)["'][^>]*>`)
	return re.ReplaceAllStringFunc(html, func(match string) string {
		submatch := re.FindStringSubmatch(match)
		if len(submatch) < 2 {
			return match
		}

		href := submatch[1]
		cssPath := resolveLocalPath(href, appDir)

		cssContent, err := os.ReadFile(cssPath)
		if err != nil {
			return match // 如果无法读取，保持原样
		}

		// 处理 CSS 中的相对 URL
		cssDir := filepath.Dir(cssPath)
		cssWithUrls := inlineCSSUrls(string(cssContent), cssDir)

		return fmt.Sprintf(`<style>%s</style>`, cssWithUrls)
	})
}

// inlineImages converts <img src="..."> to inline base64 data URIs for local images.
func inlineImages(html, appDir string) string {
	// 匹配 <img ... src="..." ...>
	re := regexp.MustCompile(`<img[^>]+src=["']([^"']+)["'][^>]*>`)
	return re.ReplaceAllStringFunc(html, func(match string) string {
		submatch := re.FindStringSubmatch(match)
		if len(submatch) < 2 {
			return match
		}

		src := submatch[1]

		// 跳过已经是 data URI 或外部 URL
		if strings.HasPrefix(src, "data:") || strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
			return match
		}

		imgPath := resolveLocalPath(src, appDir)
		dataURI, err := imageToDataURI(imgPath)
		if err != nil {
			return match // 如果无法转换，保持原样
		}

		// 替换 src 属性
		reSrc := regexp.MustCompile(`src=["'][^"']+["']`)
		return reSrc.ReplaceAllString(match, fmt.Sprintf(`src="%s"`, dataURI))
	})
}

// inlineCSSUrls handles url() references in inline <style> tags.
func inlineCSSUrls(html, appDir string) string {
	// 匹配 <style>...</style> 标签内容
	re := regexp.MustCompile(`<style[^>]*>(.*?)</style>`)
	return re.ReplaceAllStringFunc(html, func(match string) string {
		submatch := re.FindStringSubmatch(match)
		if len(submatch) < 2 {
			return match
		}

		styleContent := submatch[1]
		// 处理 url() 引用
		styleWithUrls := inlineCSSUrlReferences(styleContent, appDir)

		// 重新组装
		tagStart := strings.Index(match, "<style")
		tagEnd := strings.Index(match, ">")
		if tagEnd == -1 {
			return match
		}
		openTag := match[tagStart:tagEnd+1]

		return fmt.Sprintf(`%s>%s</style>`, openTag, styleWithUrls)
	})
}

// inlineCSSUrlReferences converts url() in CSS to data URIs for local resources.
func inlineCSSUrlReferences(css, appDir string) string {
	// 匹配 url('...') 或 url("...") 或 url(...)
	re := regexp.MustCompile(`url\(['"]?([^'")\s]+)['"]?\)`)
	return re.ReplaceAllStringFunc(css, func(match string) string {
		submatch := re.FindStringSubmatch(match)
		if len(submatch) < 2 {
			return match
		}

		urlValue := submatch[1]

		// 跳过已经是 data URI 或外部 URL
		if strings.HasPrefix(urlValue, "data:") || strings.HasPrefix(urlValue, "http://") || strings.HasPrefix(urlValue, "https://") {
			return match
		}

		resourcePath := resolveLocalPath(urlValue, appDir)
		dataURI, err := imageToDataURI(resourcePath)
		if err != nil {
			return match
		}

		return fmt.Sprintf(`url('%s')`, dataURI)
	})
}

// resolveLocalPath converts a potentially relative path to an absolute path.
func resolveLocalPath(ref, baseDir string) string {
	if filepath.IsAbs(ref) {
		return ref
	}

	// 移除查询参数和锚点
	ref = strings.Split(ref, "?")[0]
	ref = strings.Split(ref, "#")[0]

	return filepath.Join(baseDir, ref)
}

// imageToDataURI converts an image file to a base64 data URI.
func imageToDataURI(imagePath string) (string, error) {
	data, err := os.ReadFile(imagePath)
	if err != nil {
		return "", err
	}

	mimeType := guessImageMimeType(imagePath)
	base64 := base64.StdEncoding.EncodeToString(data)

	return fmt.Sprintf("data:%s;base64,%s", mimeType, base64), nil
}

// guessImageMimeType guesses the MIME type from file extension.
func guessImageMimeType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	default:
		return "application/octet-stream"
	}
}

// exists checks if a file exists.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// GetApp returns an app by name.
func (m *Manager) GetApp(name string) (AppInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	app, ok := m.apps[name]
	return app, ok
}

// ReadAppHTML reads the HTML content of an app.
func (m *Manager) ReadAppHTML(name string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	app, ok := m.apps[name]
	if !ok {
		return "", fmt.Errorf("app %q not found", name)
	}

	content, err := os.ReadFile(app.FilePath)
	if err != nil {
		return "", fmt.Errorf("read app file: %w", err)
	}

	return string(content), nil
}

// ListApps returns all apps.
func (m *Manager) ListApps() []AppInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]AppInfo, 0, len(m.apps))
	for _, app := range m.apps {
		result = append(result, app)
	}
	return result
}

// DeleteApp removes an app.
func (m *Manager) DeleteApp(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	app, ok := m.apps[name]
	if !ok {
		return fmt.Errorf("app %q not found", name)
	}

	if err := os.Remove(app.FilePath); err != nil {
		return fmt.Errorf("delete app file: %w", err)
	}

	delete(m.apps, name)
	return nil
}

// UpdateApp updates an existing app's content.
func (m *Manager) UpdateApp(name, htmlContent string) (AppInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	app, ok := m.apps[name]
	if !ok {
		return AppInfo{}, fmt.Errorf("app %q not found", name)
	}

	if err := os.WriteFile(app.FilePath, []byte(htmlContent), 0644); err != nil {
		return AppInfo{}, fmt.Errorf("write app file: %w", err)
	}

	info, _ := os.Stat(app.FilePath)
	app.UpdatedAt = time.Now()
	if info != nil {
		app.Size = info.Size()
	}
	// 增加版本号
	app.Version = incrementVersion(app.Version)
	m.apps[name] = app
	return app, nil
}

// UpdateAppStatus updates the status of an application.
func (m *Manager) UpdateAppStatus(name string, status AppStatus) (AppInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	app, ok := m.apps[name]
	if !ok {
		return AppInfo{}, fmt.Errorf("app %q not found", name)
	}

	app.Status = status
	app.UpdatedAt = time.Now()
	m.apps[name] = app
	return app, nil
}

// UpdateAppMetadata updates metadata fields of an application.
func (m *Manager) UpdateAppMetadata(name string, updates AppMetadataUpdate) (AppInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	app, ok := m.apps[name]
	if !ok {
		return AppInfo{}, fmt.Errorf("app %q not found", name)
	}

	if updates.Description != "" {
		app.Description = updates.Description
	}
	if updates.IconPath != "" {
		app.IconPath = updates.IconPath
	}
	app.UpdatedAt = time.Now()
	m.apps[name] = app
	return app, nil
}

// AppMetadataUpdate defines fields that can be updated.
type AppMetadataUpdate struct {
	Description string `json:"description,omitempty"`
	IconPath    string `json:"icon_path,omitempty"`
}

// GetAppDir returns the absolute path of the apps directory.
func (m *Manager) GetAppDir() string {
	return m.appDir
}

func (m *Manager) loadExisting() {
	entries, err := os.ReadDir(m.appDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".html" {
			continue
		}

		filePath := filepath.Join(m.appDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}

		name := entry.Name()[:len(entry.Name())-5] // Remove .html
		m.apps[name] = AppInfo{
			Name:      name,
			FilePath:  filePath,
			CreatedAt: info.ModTime(),
			UpdatedAt: info.ModTime(),
			Size:      info.Size(),
			Type:      AppTypeCustom, // 默认为 custom 类型
			Status:    AppStatusActive,
			Version:   "1.0.0",
			AppDir:    m.appDir,
		}
	}
}

// incrementVersion increments a version string (e.g., "1.0.0" -> "1.0.1").
func incrementVersion(version string) string {
	if version == "" {
		// 空版本，返回默认并增加
		return "1.0.1"
	}

	// 尝试解析版本号
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		// 无效格式，返回默认并增加
		return "1.0.1"
	}

	// 验证每个部分都是数字
	for _, p := range parts {
		if !isNumeric(p) {
			return "1.0.1"
		}
	}

	// 增加最后一个数字
	minor := parseVersionPart(parts[2])
	parts[2] = fmt.Sprintf("%d", minor+1)
	return fmt.Sprintf("%s.%s.%s", parts[0], parts[1], parts[2])
}

// isNumeric checks if a string contains only digits.
func isNumeric(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func parseVersionPart(s string) int {
	if len(s) == 0 {
		return 0
	}
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

// TemplateType defines the type of HTML app template.
type TemplateType string

const (
	// TemplateBlank 空白模板，最小化结构.
	TemplateBlank TemplateType = "blank"
	// TemplateCalculator 计算器模板，包含基本计算功能.
	TemplateCalculator TemplateType = "calculator"
	// TemplateDashboard 仪表盘模板，包含图表和统计展示.
	TemplateDashboard TemplateType = "dashboard"
	// TemplateForm 表单模板，包含输入表单.
	TemplateForm TemplateType = "form"
	// TemplateNotes 笔记模板，包含文本编辑功能.
	TemplateNotes TemplateType = "notes"
)

// GetTemplate returns HTML content for a given template type.
func GetTemplate(templateType TemplateType, title string) string {
	switch templateType {
	case TemplateBlank:
		return generateBlankTemplate(title)
	case TemplateCalculator:
		return generateCalculatorTemplate(title)
	case TemplateDashboard:
		return generateDashboardTemplate(title)
	case TemplateForm:
		return generateFormTemplate(title)
	case TemplateNotes:
		return generateNotesTemplate(title)
	default:
		return generateBlankTemplate(title)
	}
}

// ListTemplates returns all available template types with descriptions.
func ListTemplates() []TemplateInfo {
	return []TemplateInfo{
		{Type: TemplateBlank, Name: "空白模板", Description: "最小化 HTML 结构，适合自定义开发"},
		{Type: TemplateCalculator, Name: "计算器模板", Description: "包含基本计算功能的交互式计算器"},
		{Type: TemplateDashboard, Name: "仪表盘模板", Description: "包含图表和统计展示的数据可视化界面"},
		{Type: TemplateForm, Name: "表单模板", Description: "包含输入表单和验证功能"},
		{Type: TemplateNotes, Name: "笔记模板", Description: "包含文本编辑和保存功能的笔记应用"},
	}
}

// TemplateInfo describes a template type.
type TemplateInfo struct {
	Type        TemplateType `json:"type"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
}

// generateBlankTemplate generates a minimal blank HTML template.
func generateBlankTemplate(title string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>%s</title>
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body {
      font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
      background: #f5f5f5;
      color: #333;
      min-height: 100vh;
      padding: 20px;
    }
  </style>
</head>
<body>
  <h1>%s</h1>
  <p>在此添加您的应用内容</p>
</body>
</html>`, title, title)
}

// generateCalculatorTemplate generates an interactive calculator template.
func generateCalculatorTemplate(title string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>%s</title>
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body {
      font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
      background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%);
      min-height: 100vh;
      display: flex;
      justify-content: center;
      align-items: center;
    }
    .calculator {
      background: #fff;
      border-radius: 16px;
      padding: 24px;
      box-shadow: 0 10px 40px rgba(0,0,0,0.2);
      width: 320px;
    }
    .display {
      background: #1a1a2e;
      color: #fff;
      font-size: 32px;
      padding: 16px;
      border-radius: 8px;
      text-align: right;
      margin-bottom: 16px;
      min-height: 60px;
    }
    .buttons {
      display: grid;
      grid-template-columns: repeat(4, 1fr);
      gap: 8px;
    }
    button {
      padding: 16px;
      font-size: 18px;
      border: none;
      border-radius: 8px;
      cursor: pointer;
      transition: transform 0.1s;
    }
    button:hover { transform: scale(1.05); }
    button:active { transform: scale(0.95); }
    .btn-num { background: #e9ecef; color: #333; }
    .btn-op { background: #667eea; color: #fff; }
    .btn-clear { background: #ff6b6b; color: #fff; }
    .btn-equals { background: #51cf66; color: #fff; grid-column: span 2; }
  </style>
</head>
<body>
  <div class="calculator">
    <h1 style="text-align:center;margin-bottom:16px;">%s</h1>
    <div class="display" id="display">0</div>
    <div class="buttons">
      <button class="btn-clear" onclick="clearDisplay()">C</button>
      <button class="btn-op" onclick="appendOp('/')">÷</button>
      <button class="btn-op" onclick="appendOp('*')">×</button>
      <button class="btn-op" onclick="appendOp('-')">−</button>
      <button class="btn-num" onclick="appendNum('7')">7</button>
      <button class="btn-num" onclick="appendNum('8')">8</button>
      <button class="btn-num" onclick="appendNum('9')">9</button>
      <button class="btn-op" onclick="appendOp('+')">+</button>
      <button class="btn-num" onclick="appendNum('4')">4</button>
      <button class="btn-num" onclick="appendNum('5')">5</button>
      <button class="btn-num" onclick="appendNum('6')">6</button>
      <button class="btn-num" onclick="appendNum('1')">1</button>
      <button class="btn-num" onclick="appendNum('2')">2</button>
      <button class="btn-num" onclick="appendNum('3')">3</button>
      <button class="btn-num" onclick="appendNum('0')">0</button>
      <button class="btn-num" onclick="appendNum('.')">.</button>
      <button class="btn-equals" onclick="calculate()">=</button>
    </div>
  </div>
  <script>
    let expression = '';
    function updateDisplay() {
      document.getElementById('display').textContent = expression || '0';
    }
    function appendNum(n) { expression += n; updateDisplay(); }
    function appendOp(op) { expression += op; updateDisplay(); }
    function clearDisplay() { expression = ''; updateDisplay(); }
    function calculate() {
      try {
        expression = eval(expression).toString();
        updateDisplay();
      } catch(e) {
        expression = 'Error'; updateDisplay(); expression = '';
      }
    }
  </script>
</body>
</html>`, title, title)
}

// generateDashboardTemplate generates a data dashboard template.
func generateDashboardTemplate(title string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>%s</title>
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body {
      font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
      background: #1a1a2e;
      color: #fff;
      min-height: 100vh;
      padding: 20px;
    }
    .dashboard { max-width: 1200px; margin: 0 auto; }
    h1 { margin-bottom: 24px; }
    .stats-grid {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
      gap: 16px;
      margin-bottom: 24px;
    }
    .stat-card {
      background: #16213e;
      border-radius: 12px;
      padding: 20px;
      border: 1px solid #0f3460;
    }
    .stat-value { font-size: 32px; font-weight: bold; color: #e94560; }
    .stat-label { font-size: 14px; opacity: 0.7; margin-top: 8px; }
    .chart-area {
      background: #16213e;
      border-radius: 12px;
      padding: 20px;
      border: 1px solid #0f3460;
      min-height: 300px;
      display: flex;
      justify-content: center;
      align-items: center;
    }
    .placeholder { opacity: 0.5; }
  </style>
</head>
<body>
  <div class="dashboard">
    <h1>%s</h1>
    <div class="stats-grid">
      <div class="stat-card">
        <div class="stat-value">1,234</div>
        <div class="stat-label">总访问量</div>
      </div>
      <div class="stat-card">
        <div class="stat-value">89%%</div>
        <div class="stat-label">完成率</div>
      </div>
      <div class="stat-card">
        <div class="stat-value">56</div>
        <div class="stat-label">活跃用户</div>
      </div>
      <div class="stat-card">
        <div class="stat-value">¥12,580</div>
        <div class="stat-label">总收入</div>
      </div>
    </div>
    <div class="chart-area">
      <div class="placeholder">图表区域 - 可添加 Chart.js 或其他图表库</div>
    </div>
  </div>
</body>
</html>`, title, title)
}

// generateFormTemplate generates a form template.
func generateFormTemplate(title string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>%s</title>
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body {
      font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
      background: #f5f5f5;
      min-height: 100vh;
      padding: 40px 20px;
    }
    .form-container {
      max-width: 600px;
      margin: 0 auto;
      background: #fff;
      border-radius: 12px;
      padding: 32px;
      box-shadow: 0 2px 8px rgba(0,0,0,0.1);
    }
    h1 { margin-bottom: 24px; color: #333; }
    .form-group { margin-bottom: 20px; }
    label { display: block; margin-bottom: 8px; font-weight: 500; color: #555; }
    input, textarea, select {
      width: 100%%;
      padding: 12px;
      border: 1px solid #ddd;
      border-radius: 8px;
      font-size: 16px;
      transition: border-color 0.2s;
    }
    input:focus, textarea:focus, select:focus {
      outline: none;
      border-color: #667eea;
    }
    textarea { min-height: 120px; resize: vertical; }
    button {
      background: #667eea;
      color: #fff;
      padding: 12px 24px;
      border: none;
      border-radius: 8px;
      font-size: 16px;
      cursor: pointer;
      width: 100%%;
    }
    button:hover { background: #5a6fd6; }
  </style>
</head>
<body>
  <div class="form-container">
    <h1>%s</h1>
    <form onsubmit="handleSubmit(event)">
      <div class="form-group">
        <label for="name">姓名</label>
        <input type="text" id="name" name="name" required placeholder="请输入姓名">
      </div>
      <div class="form-group">
        <label for="email">邮箱</label>
        <input type="email" id="email" name="email" required placeholder="请输入邮箱">
      </div>
      <div class="form-group">
        <label for="type">类型</label>
        <select id="type" name="type">
          <option value="option1">选项一</option>
          <option value="option2">选项二</option>
          <option value="option3">选项三</option>
        </select>
      </div>
      <div class="form-group">
        <label for="message">消息</label>
        <textarea id="message" name="message" placeholder="请输入详细内容"></textarea>
      </div>
      <button type="submit">提交</button>
    </form>
  </div>
  <script>
    function handleSubmit(e) {
      e.preventDefault();
      const formData = new FormData(e.target);
      const data = Object.fromEntries(formData.entries());
      alert('表单数据: ' + JSON.stringify(data, null, 2));
    }
  </script>
</body>
</html>`, title, title)
}

// generateNotesTemplate generates a notes template.
func generateNotesTemplate(title string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>%s</title>
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body {
      font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
      background: #fff3e0;
      min-height: 100vh;
      padding: 20px;
    }
    .notes-app { max-width: 800px; margin: 0 auto; }
    h1 { color: #e65100; margin-bottom: 20px; }
    .toolbar {
      display: flex;
      gap: 8px;
      margin-bottom: 16px;
      padding: 12px;
      background: #fff;
      border-radius: 8px;
      box-shadow: 0 2px 4px rgba(0,0,0,0.1);
    }
    button {
      padding: 8px 16px;
      border: none;
      border-radius: 4px;
      cursor: pointer;
      font-size: 14px;
    }
    .btn-save { background: #ff9800; color: #fff; }
    .btn-clear { background: #f5f5f5; color: #333; }
    .editor {
      background: #fff;
      border-radius: 8px;
      padding: 20px;
      min-height: 400px;
      box-shadow: 0 2px 4px rgba(0,0,0,0.1);
    }
    textarea {
      width: 100%%;
      height: 100%%;
      min-height: 380px;
      border: none;
      font-size: 16px;
      line-height: 1.6;
      resize: none;
    }
    textarea:focus { outline: none; }
    .status { margin-top: 12px; font-size: 12px; color: #888; }
  </style>
</head>
<body>
  <div class="notes-app">
    <h1>%s</h1>
    <div class="toolbar">
      <button class="btn-save" onclick="saveNote()">保存</button>
      <button class="btn-clear" onclick="clearNote()">清空</button>
    </div>
    <div class="editor">
      <textarea id="noteContent" placeholder="在此输入笔记内容..."></textarea>
    </div>
    <div class="status" id="status">自动保存已启用</div>
  </div>
  <script>
    const noteKey = 'wukong_note_' + '%s';
    // 加载保存的笔记
    document.getElementById('noteContent').value = localStorage.getItem(noteKey) || '';
    function saveNote() {
      const content = document.getElementById('noteContent').value;
      localStorage.setItem(noteKey, content);
      showStatus('已保存');
    }
    function clearNote() {
      document.getElementById('noteContent').value = '';
      localStorage.removeItem(noteKey);
      showStatus('已清空');
    }
    function showStatus(msg) {
      const status = document.getElementById('status');
      status.textContent = msg + ' - ' + new Date().toLocaleTimeString();
    }
    // 自动保存
    setInterval(() => {
      const content = document.getElementById('noteContent').value;
      if (content) localStorage.setItem(noteKey, content);
    }, 5000);
  </script>
</body>
</html>`, title, title, sanitizeAppName(title))
}

// GenerateAppTemplate generates a default HTML app template (deprecated, use GetTemplate).
func GenerateAppTemplate(title string) string {
	return GetTemplate(TemplateBlank, title)
}

func sanitizeAppName(name string) string {
	result := ""
	for _, c := range name {
		if (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '-' || c == '_' {
			result += string(c)
		}
	}
	if result == "" {
		result = "app"
	}
	return result
}
