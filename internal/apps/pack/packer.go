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
)

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

	// 验证源目录存在
	if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("source directory does not exist: %s", sourceDir)
	}

	// 确定输出路径
	outputPath := p.opts.OutputPath
	if outputPath == "" {
		outputPath = p.determineOutputPath(sourceDir)
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
		return nil, fmt.Errorf("unsupported format: %s", p.opts.Format)
	}

	if err != nil {
		return nil, err
	}

	result.StartTime = startTime
	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(startTime)

	return result, nil
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
func (p *Packer) packHTML(ctx context.Context, sourceDir, outputPath string) (*Result, error) {
	result := &Result{
		Format:   FormatHTML,
		OutputPath: outputPath,
	}

	// 创建输出目录
	if err := os.MkdirAll(outputPath, 0755); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}

	// 复制所有文件
	filesProcessed, assetsIncluded, totalSize, err := p.copyDirectory(sourceDir, outputPath)
	if err != nil {
		return nil, fmt.Errorf("copy directory: %w", err)
	}

	result.FilesProcessed = filesProcessed
	result.AssetsIncluded = assetsIncluded
	result.SizeBytes = totalSize
	result.Success = true

	return result, nil
}

// copyDirectory recursively copies a directory.
func (p *Packer) copyDirectory(src, dst string) (int, int, int64, error) {
	filesProcessed := 0
	assetsIncluded := 0
	totalSize := int64(0)

	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// 计算相对路径
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			// 创建目录
			return os.MkdirAll(dstPath, info.Mode())
		}

		// 复制文件
		if err := copyFile(path, dstPath); err != nil {
			return err
		}

		filesProcessed++
		totalSize += info.Size()

		// 检查是否是资源文件
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

	return filesProcessed, assetsIncluded, totalSize, err
}

// copyFile copies a single file.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return err
	}

	// 复制权限
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.Chmod(dst, srcInfo.Mode())
}

// packZIM creates a ZIM archive file (Kiwix compatible).
// This uses the native ZIM format implementation following the ZIM specification.
// The ZIM format is the official Wikipedia offline format supported by Kiwix.
func (p *Packer) packZIM(ctx context.Context, sourceDir, outputPath string) (*Result, error) {
	result := &Result{
		Format:     FormatZIM,
		OutputPath: outputPath,
	}

	// Create ZIM packer
	packer := NewZIMPacker()

	// Walk through directory and add all files as articles
	var filesProcessed int
	var assetsIncluded int

	err := filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		// Read file content
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read file %s: %w", path, err)
		}

		// Get relative path as URL
		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}

		// Convert to URL format (use / as separator)
		url := filepath.ToSlash(relPath)
		title := url

		// 处理 index pages specially
		if strings.HasSuffix(url, ".html") {
			baseName := strings.TrimSuffix(url, ".html")
			if baseName == "index" || baseName == "" {
				url = "index"
			}
		}

		// Determine MIME type from extension
		mimeType := getMimeType(url)

		// Count assets (non-HTML files)
		if mimeType != "text/html" {
			assetsIncluded++
		}

		// Add as ZIM article
		if err := packer.AddArticle(url, title, mimeType, data); err != nil {
			return fmt.Errorf("add article %s: %w", url, err)
		}

		filesProcessed++
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("walk directory: %w", err)
	}

	if filesProcessed == 0 {
		return nil, fmt.Errorf("no files found to pack")
	}

	// Build the ZIM archive
	if err := packer.Build(outputPath, p.opts.AppName, p.opts.AppDescription, p.opts.Compress); err != nil {
		return nil, fmt.Errorf("build ZIM archive: %w", err)
	}

	// Get final file size
	info, err := os.Stat(outputPath)
	if err != nil {
		return nil, fmt.Errorf("stat output file: %w", err)
	}

	result.FilesProcessed = filesProcessed
	result.AssetsIncluded = assetsIncluded
	result.SizeBytes = info.Size()
	result.Success = true

	return result, nil
}

// packBinary creates a self-contained executable with embedded content.
func (p *Packer) packBinary(ctx context.Context, sourceDir, outputPath string) (*Result, error) {
	result := &Result{
		Format:     FormatBinary,
		OutputPath: outputPath,
	}

	// 检查基础可执行文件是否存在
	baseBinary := p.opts.BaseBinary
	if baseBinary == "" {
		// 使用当前可执行文件作为基础
		baseBinary, _ = os.Executable()
	}

	if _, err := os.Stat(baseBinary); os.IsNotExist(err) {
		return nil, fmt.Errorf("base binary not found: %s", baseBinary)
	}

	// 复制基础可执行文件到输出路径
	if err := copyFile(baseBinary, outputPath); err != nil {
		return nil, fmt.Errorf("copy base binary: %w", err)
	}

	// 打开输出文件以追加内容
	outFile, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_APPEND, 0755)
	if err != nil {
		return nil, fmt.Errorf("open output file: %w", err)
	}
	defer outFile.Close()

	// 写入内容分隔标记
	marker := "\n---WUKONG_APP_CONTENT_BEGIN---\n"
	if _, err := outFile.WriteString(marker); err != nil {
		return nil, fmt.Errorf("write marker: %w", err)
	}

	// 写入元数据
	meta := fmt.Sprintf("NAME=%s\nVERSION=%s\nDATE=%s\n",
		p.opts.AppName, p.opts.AppVersion, time.Now().Format(time.RFC3339))
	if _, err := outFile.WriteString(meta); err != nil {
		return nil, fmt.Errorf("write meta: %w", err)
	}

	// 写入内容分隔标记结束
	markerEnd := "---WUKONG_APP_CONTENT_FILES---\n"
	if _, err := outFile.WriteString(markerEnd); err != nil {
		return nil, fmt.Errorf("write marker end: %w", err)
	}

	// 追加所有文件内容
	filesProcessed := 0
	assetsIncluded := 0
	totalSize := int64(0)

	err = filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// 写入文件头
		relPath, _ := filepath.Rel(sourceDir, path)
		fileHeader := fmt.Sprintf("FILE:%s:%d\n", relPath, info.Size())
		if _, err := outFile.WriteString(fileHeader); err != nil {
			return err
		}

		// 写入文件内容
		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		if _, err := io.Copy(outFile, srcFile); err != nil {
			return err
		}

		filesProcessed++
		totalSize += info.Size()

		// 检查是否是资源文件
		ext := strings.ToLower(filepath.Ext(path))
		assetExts := []string{".css", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".woff", ".woff2", ".ttf"}
		for _, assetExt := range assetExts {
			if ext == assetExt {
				assetsIncluded++
				break
			}
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("append files: %w", err)
	}

	// 写入结束标记
	endMarker := "\n---WUKONG_APP_CONTENT_END---\n"
	if _, err := outFile.WriteString(endMarker); err != nil {
		return nil, fmt.Errorf("write end marker: %w", err)
	}

	// 获取最终文件大小
	outInfo, _ := os.Stat(outputPath)
	if outInfo != nil {
		result.SizeBytes = outInfo.Size()
	}

	result.FilesProcessed = filesProcessed
	result.AssetsIncluded = assetsIncluded
	result.Success = true

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
func (p *Packer) packMacApp(ctx context.Context, sourceDir, outputPath string) (*Result, error) {
	result := &Result{
		Format:     FormatApp,
		OutputPath: outputPath,
	}

	// 创建 .app 目录结构
	contentsDir := filepath.Join(outputPath, "Contents")
	macosDir := filepath.Join(contentsDir, "MacOS")
	resourcesDir := filepath.Join(contentsDir, "Resources")

	if err := os.MkdirAll(macosDir, 0755); err != nil {
		return nil, fmt.Errorf("create MacOS directory: %w", err)
	}
	if err := os.MkdirAll(resourcesDir, 0755); err != nil {
		return nil, fmt.Errorf("create Resources directory: %w", err)
	}

	// 复制可执行文件
	baseBinary := p.opts.BaseBinary
	if baseBinary == "" {
		baseBinary, _ = os.Executable()
	}

	execPath := filepath.Join(macosDir, "wukong_app")
	if err := copyFile(baseBinary, execPath); err != nil {
		return nil, fmt.Errorf("copy executable: %w", err)
	}
	// 设置可执行权限
	os.Chmod(execPath, 0755)

	// 复制应用内容到 Resources
	appContentDir := filepath.Join(resourcesDir, "app")
	if err := os.MkdirAll(appContentDir, 0755); err != nil {
		return nil, fmt.Errorf("create app content directory: %w", err)
	}

	filesProcessed, assetsIncluded, _, err := p.copyDirectory(sourceDir, appContentDir)
	if err != nil {
		return nil, fmt.Errorf("copy app content: %w", err)
	}

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
func (p *Packer) packLinuxApp(ctx context.Context, sourceDir, outputPath string) (*Result, error) {
	result := &Result{
		Format:     FormatApp,
		OutputPath: outputPath,
	}

	// 创建 AppDir 结构
	appDir := outputPath
	usrBinDir := filepath.Join(appDir, "usr", "bin")
	usrShareDir := filepath.Join(appDir, "usr", "share", "applications")
	usrIconDir := filepath.Join(appDir, "usr", "share", "icons", "hicolor", "256x256", "apps")

	if err := os.MkdirAll(usrBinDir, 0755); err != nil {
		return nil, fmt.Errorf("create usr/bin directory: %w", err)
	}
	if err := os.MkdirAll(usrShareDir, 0755); err != nil {
		return nil, fmt.Errorf("create usr/share/applications directory: %w", err)
	}
	if err := os.MkdirAll(usrIconDir, 0755); err != nil {
		return nil, fmt.Errorf("create usr/share/icons directory: %w", err)
	}

	// 复制可执行文件
	baseBinary := p.opts.BaseBinary
	if baseBinary == "" {
		baseBinary, _ = os.Executable()
	}

	execPath := filepath.Join(usrBinDir, p.opts.AppName)
	if err := copyFile(baseBinary, execPath); err != nil {
		return nil, fmt.Errorf("copy executable: %w", err)
	}
	os.Chmod(execPath, 0755)

	// 复制应用内容
	appContentDir := filepath.Join(appDir, "usr", "share", p.opts.AppName)
	if err := os.MkdirAll(appContentDir, 0755); err != nil {
		return nil, fmt.Errorf("create app content directory: %w", err)
	}

	filesProcessed, assetsIncluded, _, err := p.copyDirectory(sourceDir, appContentDir)
	if err != nil {
		return nil, fmt.Errorf("copy app content: %w", err)
	}

	// 创建 .desktop 文件
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
		return nil, fmt.Errorf("write desktop file: %w", err)
	}

	// 复制图标
	if p.opts.IconPath != "" {
		iconDst := filepath.Join(usrIconDir, p.opts.AppName+".png")
		if err := copyFile(p.opts.IconPath, iconDst); err != nil {
			fmt.Printf("Warning: failed to copy icon: %v\n", err)
		}
	}

	result.FilesProcessed = filesProcessed
	result.AssetsIncluded = assetsIncluded
	result.SizeBytes = calculateDirSize(outputPath)
	result.Success = true

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