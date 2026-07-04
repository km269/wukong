// Package pack provides application packaging functionality.
package pack

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/km269/wukong/pkg/zim"
)

// PackError represents a detailed packaging error.
type PackError struct {
	Phase   string
	Path    string
	Message string
	Err     error
}

func (e *PackError) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("[%s] %s: %s", e.Phase, e.Path, e.Message)
	}
	return fmt.Sprintf("[%s] %s", e.Phase, e.Message)
}

func (e *PackError) Unwrap() error {
	return e.Err
}

func newPackError(phase, path string, err error) *PackError {
	return &PackError{
		Phase:   phase,
		Path:    path,
		Message: err.Error(),
		Err:     err,
	}
}

// Packer performs application packaging operations.
type Packer struct {
	opts Options
}

// NewPacker creates a new packer with the given options.
func NewPacker(opts Options) *Packer {
	return &Packer{opts: opts}
}

// Pack performs the packaging operation on the given source directory.
func (p *Packer) Pack(ctx context.Context, sourceDir string) (*Result, error) {
	startTime := time.Now()

	if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
		return nil, newPackError("validation", sourceDir,
			fmt.Errorf("source directory does not exist"))
	}

	outputPath := p.opts.OutputPath
	if outputPath == "" {
		outputPath = p.determineOutputPath(sourceDir)
	}

	if err := p.ensureOutputParentDir(outputPath); err != nil {
		return nil, newPackError("setup", outputPath, err)
	}

	var result *Result
	var err error

	switch p.opts.Format {
	case FormatHTML:
		result, err = p.packHTML(ctx, sourceDir, outputPath)
	case FormatZIM:
		result, err = p.packZIM(ctx, sourceDir, outputPath)
	case FormatBinary:
		result, err = p.packBinary(ctx, sourceDir, outputPath)
	case FormatApp:
		result, err = p.packApp(ctx, sourceDir, outputPath)
	default:
		return nil, newPackError("validation", outputPath,
			fmt.Errorf("unsupported format: %s", p.opts.Format))
	}

	if err != nil {
		return nil, err
	}

	result.StartTime = startTime
	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(startTime)

	return result, nil
}

// ensureOutputParentDir creates the parent directory for the output path.
func (p *Packer) ensureOutputParentDir(outputPath string) error {
	parentDir := filepath.Dir(outputPath)
	if parentDir == "." || parentDir == outputPath {
		return nil
	}
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("create output parent directory: %w", err)
	}
	return nil
}

// determineOutputPath generates a default output path based on source and format.
func (p *Packer) determineOutputPath(sourceDir string) string {
	// 提取应用名称
	appName := p.opts.AppName
	if appName == "" {
		appName = filepath.Base(sourceDir)
	}

	// 根据格式确定输出路径
	switch p.opts.Format {
	case FormatHTML:
		return filepath.Join(filepath.Dir(sourceDir), appName+"_packaged")
	case FormatZIM:
		return filepath.Join(filepath.Dir(sourceDir), appName+".zim")
	case FormatBinary:
		// Windows 需要 .exe 后缀
		if strings.Contains(filepath.Base(sourceDir), "windows") {
			return filepath.Join(filepath.Dir(sourceDir), appName+".exe")
		}
		return filepath.Join(filepath.Dir(sourceDir), appName)
	case FormatApp:
		// macOS 需要 .app 后缀
		return filepath.Join(filepath.Dir(sourceDir), appName+".app")
	default:
		return filepath.Join(filepath.Dir(sourceDir), appName+"_packaged")
	}
}

// packHTML creates a standard HTML directory structure.
func (p *Packer) packHTML(_ context.Context, sourceDir, outputPath string) (*Result, error) {
	result := &Result{
		Format:     FormatHTML,
		OutputPath: outputPath,
	}

	if err := os.MkdirAll(outputPath, 0755); err != nil {
		return nil, newPackError("packHTML", outputPath,
			fmt.Errorf("create output directory: %w", err))
	}

	filesProcessed, assetsIncluded, totalSize, warnings := p.copyDirectory(sourceDir, outputPath)

	result.FilesProcessed = filesProcessed
	result.AssetsIncluded = assetsIncluded
	result.SizeBytes = totalSize
	result.Success = true
	result.Errors = warnings

	return result, nil
}

// copyDirectory recursively copies a directory with graceful error handling.
// Failed file copies are logged but don't stop the entire operation.
func (p *Packer) copyDirectory(src, dst string) (int, int, int64, []string) {
	filesProcessed := 0
	assetsIncluded := 0
	totalSize := int64(0)
	var warnings []string

	err := filepath.Walk(src, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			warnings = append(warnings, fmt.Sprintf("skip file %s: %v", path, walkErr))
			return nil
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skip file %s: %v", path, err))
			return nil
		}

		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			if err := os.MkdirAll(dstPath, info.Mode()); err != nil {
				warnings = append(warnings, fmt.Sprintf("create dir %s: %v", dstPath, err))
				return filepath.SkipDir
			}
			return nil
		}

		if err := copyFile(path, dstPath); err != nil {
			warnings = append(warnings, fmt.Sprintf("copy file %s: %v", path, err))
			return nil
		}

		filesProcessed++
		totalSize += info.Size()

		ext := strings.ToLower(filepath.Ext(path))
		assetExts := []string{".css", ".js", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".woff", ".woff2", ".ttf", ".mp4", ".mp3"}
		for _, assetExt := range assetExts {
			if ext == assetExt {
				assetsIncluded++
				break
			}
		}

		return nil
	})

	if err != nil {
		warnings = append(warnings, fmt.Sprintf("walk directory %s: %v", src, err))
	}

	return filesProcessed, assetsIncluded, totalSize, warnings
}

// copyFile copies a single file with error handling.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("copy content: %w", err)
	}

	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	return os.Chmod(dst, srcInfo.Mode())
}

// packZIM creates a ZIM archive file (Kiwix compatible) with rich metadata.
// Includes title extraction, icon embedding, counter statistics,
// and incremental cluster caching.
func (p *Packer) packZIM(_ context.Context, sourceDir, outputPath string) (*Result, error) {
	result := &Result{
		Format:     FormatZIM,
		OutputPath: outputPath,
	}

	packer := NewZIMPacker()

	var filesProcessed int
	var assetsIncluded int
	var mainPageURL string
	var mainPageTitle string
	var counterStats = make(map[string]int)
	var iconData []byte
	var warnings []string

	err := filepath.Walk(sourceDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			warnings = append(warnings, fmt.Sprintf("skip file %s: %v", path, walkErr))
			return nil
		}
		if info.IsDir() {
			return nil
		}

		relPath, _ := filepath.Rel(sourceDir, path)
		if filepath.Base(relPath) == "state.json" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skip file %s: %v", path, err))
			return nil
		}

		url := filepath.ToSlash(relPath)

		if url == "pages/index.html" && mainPageURL == "" {
			mainPageURL = url
			if t := htmlTitleOfBytes(data); t != "" {
				mainPageTitle = t
			}
		}

		mimeType := getMimeType(url)
		title := url
		if mimeType == "text/html" {
			if t := htmlTitleOfBytes(data); t != "" {
				title = t
			}
		} else {
			assetsIncluded++
		}

		if mimeType == "image/png" && len(data) > 0 {
			if isPNG48x48(data) && iconData == nil {
				iconData = make([]byte, len(data))
				copy(iconData, data)
			}
		}

		counterStats[mimeType]++

		if err := packer.AddArticle(url, title, mimeType, data); err != nil {
			warnings = append(warnings, fmt.Sprintf("skip article %s: %v", url, err))
			return nil
		}
		filesProcessed++
		return nil
	})

	if err != nil {
		return nil, newPackError("packZIM", sourceDir,
			fmt.Errorf("walk directory: %w", err))
	}
	if filesProcessed == 0 {
		return nil, newPackError("packZIM", sourceDir,
			fmt.Errorf("no files found to pack"))
	}

	// Main page with W namespace redirect.
	if mainPageURL != "" {
		packer.SetMainPage('C', mainPageURL)
		packer.AddRedirect('W', "mainPage", "Main Page", 'C', mainPageURL)
	}

	// --- Rich ZIM metadata. ---

	// Title: custom override > extracted from main page > app name.
	zimTitle := p.opts.Title
	if zimTitle == "" {
		zimTitle = mainPageTitle
	}
	if zimTitle == "" {
		zimTitle = p.opts.AppName
	}

	// Language: custom > default "eng".
	lang := p.opts.Language
	if lang == "" {
		lang = "eng"
	}

	// Date: custom > today.
	date := p.opts.Date
	if date == "" {
		date = time.Now().UTC().Format("2006-01-02")
	}

	// Creator and publisher.
	creator := p.opts.Creator
	if creator == "" {
		creator = "Wukong"
	}
	publisher := p.opts.Publisher
	if publisher == "" {
		publisher = creator
	}

	packer.AddMetadata("Title", zimTitle)
	packer.AddMetadata("Name", p.opts.AppName)
	packer.AddMetadata("Language", lang)
	packer.AddMetadata("Description", p.opts.AppDescription)
	packer.AddMetadata("Creator", creator)
	packer.AddMetadata("Publisher", publisher)
	packer.AddMetadata("Date", date)
	packer.AddMetadata("Scraper", "Wukong "+p.opts.AppVersion)

	// Source: the original URL this was cloned from.
	if sourceURL := detectSourceURL(sourceDir); sourceURL != "" {
		packer.AddMetadata("Source", sourceURL)
	}

	// Counter: "mime=count;..." format (Kiwix standard).
	packer.AddMetadata("Counter", buildCounterString(counterStats))

	// Embed 48×48 favicon as Illustrator_48x48 metadata.
	if iconData != nil {
		packer.AddContent('M', "Illustration_48x48@1",
			"Illustration 48x48", "image/png", iconData)
	}

	buildOpts := zim.BuildOptions{
		AppName:        p.opts.AppName,
		AppDescription: p.opts.AppDescription,
		Compress:       p.opts.Compress,
	}
	stats, err := packer.BuildWithStats(outputPath, buildOpts, p.opts.CachePath, p.opts.Incremental)
	if err != nil {
		return nil, newPackError("packZIM", outputPath,
			fmt.Errorf("build ZIM archive: %w", err))
	}

	result.Stats = stats

	info, err := os.Stat(outputPath)
	if err != nil {
		return nil, newPackError("packZIM", outputPath,
			fmt.Errorf("stat output file: %w", err))
	}

	result.FilesProcessed = filesProcessed
	result.AssetsIncluded = assetsIncluded
	result.SizeBytes = info.Size()
	result.Success = true
	result.Errors = warnings

	return result, nil
}

// packBinary creates a self-contained executable with ZIM archive embedded.
// The ZIM content is appended to a copy of the base binary with markers,
// making it a single-file distribution that can be extracted and served.
func (p *Packer) packBinary(ctx context.Context, sourceDir, outputPath string) (*Result, error) {
	result := &Result{
		Format:     FormatBinary,
		OutputPath: outputPath,
	}

	tmpZIM := outputPath + ".tmp.zim"
	zimResult, err := p.packZIM(ctx, sourceDir, tmpZIM)
	if err != nil {
		return nil, newPackError("packBinary", tmpZIM,
			fmt.Errorf("build embedded ZIM: %w", err))
	}
	defer os.Remove(tmpZIM)

	zimData, err := os.ReadFile(tmpZIM)
	if err != nil {
		return nil, newPackError("packBinary", tmpZIM,
			fmt.Errorf("read ZIM data: %w", err))
	}

	baseBinary := p.opts.BaseBinary
	if baseBinary == "" {
		baseBinary, _ = os.Executable()
	}

	if _, err := os.Stat(baseBinary); os.IsNotExist(err) {
		return nil, newPackError("packBinary", baseBinary,
			fmt.Errorf("base binary not found"))
	}

	if err := copyFile(baseBinary, outputPath); err != nil {
		return nil, newPackError("packBinary", outputPath,
			fmt.Errorf("copy base binary: %w", err))
	}

	outFile, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_APPEND, 0755)
	if err != nil {
		return nil, newPackError("packBinary", outputPath,
			fmt.Errorf("open output file: %w", err))
	}
	defer outFile.Close()

	marker := fmt.Sprintf(
		"\n---WUKONG_ZIM_BEGIN:%d:%s:%s:%d---\n",
		len(zimData),
		p.opts.AppName,
		p.opts.AppVersion,
		zimResult.FilesProcessed,
	)
	if _, err := outFile.WriteString(marker); err != nil {
		return nil, newPackError("packBinary", outputPath,
			fmt.Errorf("write ZIM marker: %w", err))
	}

	if _, err := outFile.Write(zimData); err != nil {
		return nil, newPackError("packBinary", outputPath,
			fmt.Errorf("write ZIM data: %w", err))
	}

	endMarker := "\n---WUKONG_ZIM_END---\n"
	if _, err := outFile.WriteString(endMarker); err != nil {
		return nil, newPackError("packBinary", outputPath,
			fmt.Errorf("write end marker: %w", err))
	}

	outInfo, _ := os.Stat(outputPath)
	if outInfo != nil {
		result.SizeBytes = outInfo.Size()
	}

	result.FilesProcessed = zimResult.FilesProcessed
	result.AssetsIncluded = zimResult.AssetsIncluded
	result.Stats = zimResult.Stats
	result.Success = true
	result.Errors = zimResult.Errors

	return result, nil
}

// packApp creates a desktop application bundle.
func (p *Packer) packApp(ctx context.Context, sourceDir, outputPath string) (*Result, error) {
	// 检测操作系统类型
	isWindows := strings.HasSuffix(outputPath, ".exe")
	isMacOS := strings.HasSuffix(outputPath, ".app")

	if isMacOS {
		return p.packMacApp(ctx, sourceDir, outputPath)
	} else if isWindows {
		return p.packWindowsApp(ctx, sourceDir, outputPath)
	} else {
		// Linux AppDir
		return p.packLinuxApp(ctx, sourceDir, outputPath)
	}
}

// packMacApp creates a macOS .app bundle.
func (p *Packer) packMacApp(_ context.Context, sourceDir, outputPath string) (*Result, error) {
	result := &Result{
		Format:     FormatApp,
		OutputPath: outputPath,
	}

	contentsDir := filepath.Join(outputPath, "Contents")
	macosDir := filepath.Join(contentsDir, "MacOS")
	resourcesDir := filepath.Join(contentsDir, "Resources")

	if err := os.MkdirAll(macosDir, 0755); err != nil {
		return nil, newPackError("packMacApp", macosDir,
			fmt.Errorf("create MacOS directory: %w", err))
	}
	if err := os.MkdirAll(resourcesDir, 0755); err != nil {
		return nil, newPackError("packMacApp", resourcesDir,
			fmt.Errorf("create Resources directory: %w", err))
	}

	baseBinary := p.opts.BaseBinary
	if baseBinary == "" {
		baseBinary, _ = os.Executable()
	}

	execPath := filepath.Join(macosDir, "wukong_app")
	if err := copyFile(baseBinary, execPath); err != nil {
		return nil, newPackError("packMacApp", execPath,
			fmt.Errorf("copy executable: %w", err))
	}
	os.Chmod(execPath, 0755)

	appContentDir := filepath.Join(resourcesDir, "app")
	if err := os.MkdirAll(appContentDir, 0755); err != nil {
		return nil, newPackError("packMacApp", appContentDir,
			fmt.Errorf("create app content directory: %w", err))
	}

	var warnings []string
	filesProcessed, assetsIncluded, _, warnings := p.copyDirectory(sourceDir, appContentDir)
	result.Errors = warnings

	// 创建 Info.plist
	plistPath := filepath.Join(contentsDir, "Info.plist")
	plistContent := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleName</key>
    <string>%s</string>
    <key>CFBundleDisplayName</key>
    <string>%s</string>
    <key>CFBundleVersion</key>
    <string>%s</string>
    <key>CFBundleExecutable</key>
    <string>wukong_app</string>
    <key>CFBundleIconFile</key>
    <string>AppIcon</string>
</dict>
</plist>`, p.opts.AppName, p.opts.AppName, p.opts.AppVersion)

	if err := os.WriteFile(plistPath, []byte(plistContent), 0644); err != nil {
		return nil, fmt.Errorf("write Info.plist: %w", err)
	}

	// 复制图标（如果提供）
	if p.opts.IconPath != "" {
		iconDst := filepath.Join(resourcesDir, "AppIcon.icns")
		if err := copyFile(p.opts.IconPath, iconDst); err != nil {
			// 图标复制失败不影响整体打包
			fmt.Printf("Warning: failed to copy icon: %v\n", err)
		}
	}

	// 获取最终大小
	result.SizeBytes = calculateDirSize(outputPath)
	result.FilesProcessed = filesProcessed
	result.AssetsIncluded = assetsIncluded
	result.Success = true

	return result, nil
}

// packWindowsApp creates a Windows application.
func (p *Packer) packWindowsApp(ctx context.Context, sourceDir, outputPath string) (*Result, error) {
	// Windows 应用就是自包含二进制
	return p.packBinary(ctx, sourceDir, outputPath)
}

// packLinuxApp creates a Linux AppDir.
func (p *Packer) packLinuxApp(_ context.Context, sourceDir, outputPath string) (*Result, error) {
	result := &Result{
		Format:     FormatApp,
		OutputPath: outputPath,
	}

	appDir := outputPath
	usrBinDir := filepath.Join(appDir, "usr", "bin")
	usrShareDir := filepath.Join(appDir, "usr", "share", "applications")
	usrIconDir := filepath.Join(appDir, "usr", "share", "icons", "hicolor", "256x256", "apps")

	if err := os.MkdirAll(usrBinDir, 0755); err != nil {
		return nil, newPackError("packLinuxApp", usrBinDir,
			fmt.Errorf("create usr/bin directory: %w", err))
	}
	if err := os.MkdirAll(usrShareDir, 0755); err != nil {
		return nil, newPackError("packLinuxApp", usrShareDir,
			fmt.Errorf("create usr/share/applications directory: %w", err))
	}
	if err := os.MkdirAll(usrIconDir, 0755); err != nil {
		return nil, newPackError("packLinuxApp", usrIconDir,
			fmt.Errorf("create usr/share/icons directory: %w", err))
	}

	baseBinary := p.opts.BaseBinary
	if baseBinary == "" {
		baseBinary, _ = os.Executable()
	}

	execPath := filepath.Join(usrBinDir, p.opts.AppName)
	if err := copyFile(baseBinary, execPath); err != nil {
		return nil, newPackError("packLinuxApp", execPath,
			fmt.Errorf("copy executable: %w", err))
	}
	os.Chmod(execPath, 0755)

	appContentDir := filepath.Join(appDir, "usr", "share", p.opts.AppName)
	if err := os.MkdirAll(appContentDir, 0755); err != nil {
		return nil, newPackError("packLinuxApp", appContentDir,
			fmt.Errorf("create app content directory: %w", err))
	}

	var warnings []string
	filesProcessed, assetsIncluded, _, warnings := p.copyDirectory(sourceDir, appContentDir)

	desktopPath := filepath.Join(usrShareDir, p.opts.AppName+".desktop")
	desktopContent := fmt.Sprintf(`[Desktop Entry]
Name=%s
Exec=%s
Icon=%s
Type=Application
Categories=Utility;
Terminal=false
`, p.opts.AppName, filepath.Join("usr", "bin", p.opts.AppName), p.opts.AppName)

	if err := os.WriteFile(desktopPath, []byte(desktopContent), 0644); err != nil {
		return nil, newPackError("packLinuxApp", desktopPath,
			fmt.Errorf("write desktop file: %w", err))
	}

	if p.opts.IconPath != "" {
		iconDst := filepath.Join(usrIconDir, p.opts.AppName+".png")
		if err := copyFile(p.opts.IconPath, iconDst); err != nil {
			warnings = append(warnings, fmt.Sprintf("warning: failed to copy icon: %v", err))
		}
	}

	result.FilesProcessed = filesProcessed
	result.AssetsIncluded = assetsIncluded
	result.SizeBytes = calculateDirSize(outputPath)
	result.Success = true
	result.Errors = warnings

	return result, nil
}

// calculateDirSize calculates the total size of a directory.
func calculateDirSize(dir string) int64 {
	var size int64
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size
}

// getMimeType determines the MIME type based on file extension.
func getMimeType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".html", ".htm":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js":
		return "text/javascript"
	case ".json":
		return "application/json"
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
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".otf":
		return "font/otf"
	case ".xml":
		return "application/xml"
	case ".pdf":
		return "application/pdf"
	case ".txt":
		return "text/plain"
	default:
		return "application/octet-stream"
	}
}

// htmlTitleOfBytes extracts the <title> text from HTML bytes.
// Returns empty string if no <title> tag is found.
func htmlTitleOfBytes(data []byte) string {
	s := string(data)
	startTag := strings.ToLower(s)
	idx := strings.Index(startTag, "<title>")
	if idx < 0 {
		idx = strings.Index(startTag, "<title ")
	}
	if idx < 0 {
		return ""
	}

	// Find the > of the opening tag.
	gt := strings.Index(s[idx:], ">")
	if gt < 0 {
		return ""
	}
	start := idx + gt + 1

	// Find </title>.
	end := strings.Index(strings.ToLower(s[start:]), "</title>")
	if end < 0 {
		return ""
	}

	title := strings.TrimSpace(s[start : start+end])
	fields := strings.Fields(title)
	return strings.Join(fields, " ")
}

// isPNG48x48 checks if PNG bytes represent a 48×48 image.
func isPNG48x48(data []byte) bool {
	if len(data) < 24 {
		return false
	}
	// PNG signature: 137 80 78 71 13 10 26 10
	if data[0] != 137 || data[1] != 80 || data[2] != 78 || data[3] != 71 {
		return false
	}
	// IHDR starts at offset 16; width at 16, height at 20 (big-endian uint32).
	w := int(data[16])<<24 | int(data[17])<<16 | int(data[18])<<8 | int(data[19])
	h := int(data[20])<<24 | int(data[21])<<16 | int(data[22])<<8 | int(data[23])
	return w == 48 && h == 48
}

// detectSourceURL finds the original source URL by looking for the
// "Cloned by Wukong from ..." banner comment in the main page.
func detectSourceURL(sourceDir string) string {
	indexPath := filepath.Join(sourceDir, "pages", "index.html")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return ""
	}
	s := string(data)
	// Look for "Cloned by Wukong from <URL> on <date>".
	const marker = "Cloned by Wukong from "
	idx := strings.Index(s, marker)
	if idx < 0 {
		return ""
	}
	start := idx + len(marker)
	end := strings.Index(s[start:], " on ")
	if end < 0 {
		end = strings.Index(s[start:], "-->")
	}
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(s[start : start+end])
}

// buildCounterString builds the Kiwix "Counter" metadata value
// in "mime=count;..." format.
func buildCounterString(stats map[string]int) string {
	var parts []string
	for mime, count := range stats {
		parts = append(parts, fmt.Sprintf("%s=%d", mime, count))
	}
	return strings.Join(parts, ";")
}
