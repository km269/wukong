// Package builtin provides the Computer Controller extension.
// It enables web scraping, file caching, and browser automation.
package builtin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/km269/wukong/internal/browser"
	"github.com/km269/wukong/internal/config"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// ComputerControllerToolSet provides web scraping, file caching,
// and browser automation tools.
type ComputerControllerToolSet struct {
	tools     []tool.Tool
	cfg       *config.WukongConfig
	browser   *browser.Controller
	inited    bool
	closed    bool
}

// NewComputerControllerToolSet creates the computer controller tool set.
func NewComputerControllerToolSet(
	cfg *config.WukongConfig,
) *ComputerControllerToolSet {
	ts := &ComputerControllerToolSet{
		cfg:     cfg,
		browser: browser.NewController(&cfg.Browser),
	}
	ts.tools = []tool.Tool{
		function.NewFunctionTool(
			ts.webFetch,
			function.WithName("web_fetch"),
			function.WithDescription(
				"Fetch content from a URL and return as text. "+
					"Use this to read web pages, API responses, "+
					"or download text content.",
			),
		),
		function.NewFunctionTool(
			ts.fileCache,
			function.WithName("file_cache"),
			function.WithDescription(
				"Download and cache a file from a URL to local "+
					"storage. Returns the local file path. Use for "+
					"images, PDFs, or other binary resources.",
			),
		),
		function.NewFunctionTool(
			ts.cacheList,
			function.WithName("cache_list"),
			function.WithDescription(
				"List files in the local cache directory.",
			),
		),
		function.NewFunctionTool(
			ts.cacheClear,
			function.WithName("cache_clear"),
			function.WithDescription(
				"Clear the local file cache.",
			),
		),
		function.NewFunctionTool(
			ts.browserNavigate,
			function.WithName("browser_navigate"),
			function.WithDescription(
				"Navigate to a URL and extract page content. "+
					"Returns the page title, text content, and "+
					"status code. Use for reading web pages with "+
					"JavaScript rendering.",
			),
		),
		function.NewFunctionTool(
			ts.browserExtract,
			function.WithName("browser_extract"),
			function.WithDescription(
				"Extract readable text content from a web page. "+
					"Removes HTML tags, scripts, and styles to "+
					"return clean text. Use for parsing article "+
					"content from news sites or documentation.",
			),
		),
		function.NewFunctionTool(
			ts.browserScreenshot,
			function.WithName("browser_screenshot"),
			function.WithDescription(
				"Capture a screenshot of a web page. Saves the "+
					"page content as a self-contained HTML file "+
					"that can be viewed offline. Returns the path "+
					"to the saved file. Use for preserving web "+
					"page snapshots or offline viewing. "+
					"NOTE: This saves the page HTML, not a visual "+
					"image. For visual screenshots, use OS-level "+
					"tools instead.",
			),
		),
	}
	return ts
}

// Tools returns the computer controller tools.
func (ts *ComputerControllerToolSet) Tools(
	ctx context.Context,
) []tool.Tool {
	return ts.tools
}

// Name returns the tool set name.
func (ts *ComputerControllerToolSet) Name() string {
	return "computer_controller"
}

// Init initializes the tool set and creates cache directory.
func (ts *ComputerControllerToolSet) Init(ctx context.Context) error {
	cacheDir := ts.cfg.Browser.CacheDir
	if cacheDir == "" {
		cacheDir = ".wukong_cache"
	}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	ts.inited = true
	return nil
}

// Close releases resources.
func (ts *ComputerControllerToolSet) Close() error {
	ts.closed = true
	return nil
}

// WebFetchReq is the input for fetching web content.
type WebFetchReq struct {
	URL     string `json:"url" jsonschema:"description=URL to fetch content from"`
	Timeout int    `json:"timeout,omitempty" jsonschema:"description=Request timeout in seconds (default 30)"`
}

// WebFetchRsp is the output for fetching web content.
type WebFetchRsp struct {
	Success     bool   `json:"success"`
	Content     string `json:"content,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	StatusCode  int    `json:"status_code"`
	Error       string `json:"error,omitempty"`
}

func (ts *ComputerControllerToolSet) webFetch(
	ctx context.Context, req WebFetchReq,
) (WebFetchRsp, error) {
	timeout := 30 * time.Second
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}

	client := &http.Client{Timeout: timeout}
	httpReq, err := http.NewRequestWithContext(
		ctx, http.MethodGet, req.URL, nil,
	)
	if err != nil {
		return WebFetchRsp{
			Success: false,
			Error:   fmt.Sprintf("create request: %v", err),
		}, nil
	}

	httpReq.Header.Set("User-Agent",
		"Wukong-Agent/1.0 (web-fetch-tool)")

	resp, err := client.Do(httpReq)
	if err != nil {
		return WebFetchRsp{
			Success: false,
			Error:   fmt.Sprintf("fetch failed: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(
		io.LimitReader(resp.Body, 1024*1024),
	) // 1MB limit
	if err != nil {
		return WebFetchRsp{
			Success:    false,
			StatusCode: resp.StatusCode,
			Error:      fmt.Sprintf("read body: %v", err),
		}, nil
	}

	content := string(body)
	// Truncate very long content
	const maxContent = 50000
	if len(content) > maxContent {
		content = content[:maxContent] +
			fmt.Sprintf("\n... (truncated, %d bytes total)", len(content))
	}

	return WebFetchRsp{
		Success:     resp.StatusCode >= 200 && resp.StatusCode < 300,
		Content:     content,
		ContentType: resp.Header.Get("Content-Type"),
		StatusCode:  resp.StatusCode,
	}, nil
}

// FileCacheReq is the input for caching a file.
type FileCacheReq struct {
	URL      string `json:"url" jsonschema:"description=URL of the file to download"`
	Filename string `json:"filename,omitempty" jsonschema:"description=Optional filename for the cached file"`
}

// FileCacheRsp is the output for caching a file.
type FileCacheRsp struct {
	Success  bool   `json:"success"`
	FilePath string `json:"file_path,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (ts *ComputerControllerToolSet) fileCache(
	ctx context.Context, req FileCacheReq,
) (FileCacheRsp, error) {
	cacheDir := ts.cfg.Browser.CacheDir
	if cacheDir == "" {
		cacheDir = ".wukong_cache"
	}

	filename := req.Filename
	if filename == "" {
		// Extract filename from URL
		parts := strings.Split(req.URL, "/")
		filename = parts[len(parts)-1]
		if filename == "" || strings.Contains(filename, "?") {
			filename = fmt.Sprintf("cache_%d", time.Now().UnixNano())
		}
	}

	filePath := filepath.Join(cacheDir, filename)

	client := &http.Client{
		Timeout: time.Duration(ts.cfg.Browser.Timeout),
	}
	httpReq, err := http.NewRequestWithContext(
		ctx, http.MethodGet, req.URL, nil,
	)
	if err != nil {
		return FileCacheRsp{
			Success: false,
			Error:   fmt.Sprintf("create request: %v", err),
		}, nil
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return FileCacheRsp{
			Success: false,
			Error:   fmt.Sprintf("download failed: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return FileCacheRsp{
			Success: false,
			Error: fmt.Sprintf(
				"HTTP %d: %s", resp.StatusCode, resp.Status,
			),
		}, nil
	}

	f, err := os.Create(filePath)
	if err != nil {
		return FileCacheRsp{
			Success: false,
			Error:   fmt.Sprintf("create file: %v", err),
		}, nil
	}
	defer f.Close()

	maxSize := ts.cfg.Browser.MaxDownloadSize
	if maxSize <= 0 {
		maxSize = 100 * 1024 * 1024 // 100MB default
	}

	written, err := io.CopyN(f, resp.Body, maxSize)
	if err != nil && err != io.EOF {
		return FileCacheRsp{
			Success: false,
			Error:   fmt.Sprintf("write file: %v", err),
		}, nil
	}

	return FileCacheRsp{
		Success:  true,
		FilePath: filePath,
		Size:     written,
	}, nil
}

// CacheListReq is the input for listing cache.
type CacheListReq struct{}

// CacheListRsp is the output for listing cache.
type CacheListRsp struct {
	Success bool          `json:"success"`
	Files   []CacheFileInfo `json:"files,omitempty"`
	Count   int           `json:"count"`
	Error   string        `json:"error,omitempty"`
}

// CacheFileInfo describes a cached file.
type CacheFileInfo struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

func (ts *ComputerControllerToolSet) cacheList(
	ctx context.Context, req CacheListReq,
) (CacheListRsp, error) {
	cacheDir := ts.cfg.Browser.CacheDir
	if cacheDir == "" {
		cacheDir = ".wukong_cache"
	}

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return CacheListRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	var files []CacheFileInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, CacheFileInfo{
			Name:    entry.Name(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}

	return CacheListRsp{
		Success: true,
		Files:   files,
		Count:   len(files),
	}, nil
}

// CacheClearReq is the input for clearing cache.
type CacheClearReq struct{}

// CacheClearRsp is the output for clearing cache.
type CacheClearRsp struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (ts *ComputerControllerToolSet) cacheClear(
	ctx context.Context, req CacheClearReq,
) (CacheClearRsp, error) {
	cacheDir := ts.cfg.Browser.CacheDir
	if cacheDir == "" {
		cacheDir = ".wukong_cache"
	}

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return CacheClearRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	cleared := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(cacheDir, entry.Name())
		if err := os.Remove(path); err != nil {
			continue
		}
		cleared++
	}

	return CacheClearRsp{
		Success: true,
		Message: fmt.Sprintf(
			"Cleared %d files from cache", cleared,
		),
	}, nil
}

// BrowserNavigateReq is the input for browser navigation.
type BrowserNavigateReq struct {
	URL string `json:"url" jsonschema:"description=URL to navigate to"`
}

// BrowserNavigateRsp is the output for browser navigation.
type BrowserNavigateRsp struct {
	Success     bool   `json:"success"`
	URL         string `json:"url"`
	Title       string `json:"title,omitempty"`
	Content     string `json:"content,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	StatusCode  int    `json:"status_code"`
	Error       string `json:"error,omitempty"`
}

func (ts *ComputerControllerToolSet) browserNavigate(
	ctx context.Context, req BrowserNavigateReq,
) (BrowserNavigateRsp, error) {
	result, err := ts.browser.Navigate(ctx, req.URL)
	if err != nil {
		return BrowserNavigateRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return BrowserNavigateRsp{
		Success:     result.Success,
		URL:         result.URL,
		Title:       result.Title,
		Content:     result.Content,
		ContentType: result.ContentType,
		StatusCode:  result.StatusCode,
		Error:       result.Error,
	}, nil
}

// BrowserExtractReq is the input for text extraction.
type BrowserExtractReq struct {
	URL string `json:"url" jsonschema:"description=URL to extract text from"`
}

// BrowserExtractRsp is the output for text extraction.
type BrowserExtractRsp struct {
	Success bool   `json:"success"`
	Text    string `json:"text,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (ts *ComputerControllerToolSet) browserExtract(
	ctx context.Context, req BrowserExtractReq,
) (BrowserExtractRsp, error) {
	result, err := ts.browser.ExtractText(ctx, req.URL)
	if err != nil {
		return BrowserExtractRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return BrowserExtractRsp{
		Success: result.Success,
		Text:    result.Text,
		Error:   result.Error,
	}, nil
}

// BrowserScreenshotReq is the input for capturing a screenshot.
type BrowserScreenshotReq struct {
	URL        string `json:"url" jsonschema:"description=URL to capture screenshot of"`
	OutputPath string `json:"output_path,omitempty" jsonschema:"description=Optional output file path for the screenshot HTML"`
}

// BrowserScreenshotRsp is the output for capturing a screenshot.
type BrowserScreenshotRsp struct {
	Success    bool   `json:"success"`
	URL        string `json:"url"`
	ImagePath  string `json:"image_path,omitempty"`
	Title      string `json:"title,omitempty"`
	StatusCode int    `json:"status_code"`
	Error      string `json:"error,omitempty"`
}

func (ts *ComputerControllerToolSet) browserScreenshot(
	ctx context.Context, req BrowserScreenshotReq,
) (BrowserScreenshotRsp, error) {
	outputPath := req.OutputPath
	if outputPath == "" {
		cacheDir := ts.cfg.Browser.CacheDir
		if cacheDir == "" {
			cacheDir = ".wukong_cache"
		}
		// Generate a filename based on URL and timestamp
		safeName := strings.NewReplacer(
			":", "_", "/", "_", "?", "_", "&", "_", "=", "_",
			"#", "_", ".", "_",
		).Replace(req.URL)
		if len(safeName) > 50 {
			safeName = safeName[:50]
		}
		outputPath = filepath.Join(
			cacheDir,
			fmt.Sprintf("screenshot_%s_%d.html",
				safeName, time.Now().Unix()),
		)
	}

	result, err := ts.browser.Screenshot(
		ctx, req.URL, outputPath,
	)
	if err != nil {
		return BrowserScreenshotRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return BrowserScreenshotRsp{
		Success:    result.Success,
		URL:        result.URL,
		ImagePath:  result.ImagePath,
		Title:      result.Title,
		StatusCode: result.StatusCode,
		Error:      result.Error,
	}, nil
}
