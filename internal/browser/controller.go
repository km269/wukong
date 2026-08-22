// Package browser provides web content tools for wukong.
// It supports two backends:
//   - HTTP mode (default): fast, lightweight HTTP fetching
//   - Chromedp mode: full headless browser with JavaScript rendering,
//     real pixel screenshots, and page interaction
//
// The backend is selected based on BrowserConfig.BrowserType:
// "chromium" enables chromedp; anything else uses HTTP mode.
package browser

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/km269/wukong/internal/apps/sanitize"
	"github.com/km269/wukong/internal/browser/settle"
	"github.com/km269/wukong/internal/browser/stealth"
	"github.com/km269/wukong/internal/browser/types"
	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/pkg/httpclient"
	"github.com/km269/wukong/pkg/logutil"
)

// Controller provides web content tools with dual backend support.
// It uses net/http for basic page fetching and a configurable browser backend
// (chromedp or go-rod) for JavaScript rendering, real screenshots, and page interaction.
//
// When cfg.Stealth is true, anti-detection scripts and Chrome flags
// are applied to reduce bot detectability.
type Controller struct {
	cfg            *config.BrowserConfig
	client         *httpclient.Client
	settleTimeout  time.Duration // Network-idle settle duration.
	stealth        bool          // Anti-detection mode enabled.
	chromedpCtx    context.Context
	chromedpCancel context.CancelFunc
	allocCancel    context.CancelFunc   // browser process lifecycle
	backend        types.BrowserBackend // Browser backend (chromedp or go-rod)
}

// NewController creates a new browser automation controller.
func NewController(cfg *config.BrowserConfig) *Controller {
	timeout := 60 * time.Second
	if cfg != nil && cfg.Timeout > 0 {
		timeout = cfg.Timeout
	}

	settle := 1500 * time.Millisecond

	c := &Controller{
		cfg:           cfg,
		settleTimeout: settle,
		stealth:       cfg != nil && cfg.Stealth,
		client:        httpclient.New(httpclient.Options{Timeout: timeout}),
	}

	if cfg != nil && cfg.Enabled &&
		strings.EqualFold(cfg.BrowserType, "chromium") {
		var err error
		c.backend, err = NewBackendFromConfig(cfg)
		if err != nil {
			logutil.Warn("failed to initialize browser backend", slog.String("error", err.Error()))
		}
	}

	return c
}

// initChromedp initializes the headless Chrome browser context.
func (c *Controller) initChromedp() {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("start-maximized", true),
		// Disable IPv6 to avoid connection issues on networks where
		// IPv6 is restricted.
		chromedp.Flag("disable-ipv6", true),
	)

	// Use new headless mode (Chrome 112+) which behaves much closer
	// to a real browser and is less likely to trigger detection.
	if c.cfg.Headless {
		opts = append(opts, chromedp.Flag("headless", "new"))
	}

	// Stealth mode: add anti-detection flags and viewport sizing.
	if c.stealth {
		opts = append(opts,
			chromedp.Flag("disable-blink-features", "AutomationControlled"),
			chromedp.Flag("disable-infobars", true),
			chromedp.Flag("no-default-browser-check", true),
			chromedp.Flag("no-first-run", true),
			chromedp.Flag("disable-component-update", true),
			chromedp.Flag("window-size", "1920,1080"),
			chromedp.Flag("disable-breakpad", true),
			chromedp.Flag("disable-background-timer-throttling", true),
			chromedp.Flag("disable-renderer-backgrounding", true),
			chromedp.Flag("disable-field-trial-config", true),
			chromedp.Flag("force-color-profile", "srgb"),
		)
	}

	// Use custom browser path if configured.
	if c.cfg.BrowserPath != "" {
		opts = append(opts, chromedp.ExecPath(c.cfg.BrowserPath))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(
		context.Background(), opts...,
	)
	c.allocCancel = allocCancel

	ctx, cancel := chromedp.NewContext(allocCtx)
	c.chromedpCtx = ctx
	c.chromedpCancel = cancel

	// Inject stealth script if enabled.
	if c.stealth {
		if err := stealth.Inject(ctx); err != nil {
			logutil.Error("[wukong/browser] stealth injection failed", "error", err)
		} else {
			logutil.Info("[wukong/browser] stealth mode enabled")
		}
	}
}

// StealthEnabled reports whether anti-detection scripts are active.
func (c *Controller) StealthEnabled() bool {
	return c.stealth
}

// EnableStealth dynamically injects anti-detection scripts via CDP.
// Only available with chromedp backend.
func (c *Controller) EnableStealth() error {
	if c.stealth {
		return nil
	}
	if !c.isChromedpBackend() {
		return fmt.Errorf("stealth requires chromedp backend")
	}
	if err := stealth.Inject(c.chromedpCtx); err != nil {
		return fmt.Errorf("enable stealth: %w", err)
	}
	c.stealth = true
	logutil.Info("[wukong/browser] stealth dynamically enabled")
	return nil
}

// waitForSettle delegates to the shared network-idle wait strategy.
func (c *Controller) waitForSettle(tabCtx context.Context) error {
	return settle.Wait(tabCtx, c.settleTimeout)
}

// isBrowserMode returns true if a browser backend (chromedp or go-rod) is available and enabled.
func (c *Controller) isBrowserMode() bool {
	return c.backend != nil
}

// isChromedpBackend returns true if the browser backend is chromedp.
func (c *Controller) isChromedpBackend() bool {
	_, ok := c.backend.(*Pool)
	return ok
}

// NavigateResult contains the result of a page navigation.
type NavigateResult struct {
	Success     bool   `json:"success"`
	URL         string `json:"url"`
	Title       string `json:"title,omitempty"`
	Content     string `json:"content,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	StatusCode  int    `json:"status_code"`
	Error       string `json:"error,omitempty"`
}

// Navigate fetches a URL and returns the page content.
// In chromedp mode, the page is rendered with JavaScript execution.
// In HTTP mode, raw HTML is returned without JS rendering.
func (c *Controller) Navigate(
	ctx context.Context, url string,
) (*NavigateResult, error) {
	if !strings.HasPrefix(url, "http://") &&
		!strings.HasPrefix(url, "https://") {
		url = "https://" + url
	}

	if c.isBrowserMode() {
		return c.navigateWithBrowser(ctx, url)
	}
	return c.navigateWithHTTP(ctx, url)
}

// navigateWithHTTP fetches a URL via HTTP GET without JavaScript.
func (c *Controller) navigateWithHTTP(
	ctx context.Context, url string,
) (*NavigateResult, error) {
	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet, url, nil,
	)
	if err != nil {
		return &NavigateResult{
			Success: false,
			URL:     url,
			Error:   fmt.Sprintf("create request: %v", err),
		}, nil
	}

	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) "+
			"AppleWebKit/537.36 (KHTML, like Gecko) "+
			"Chrome/120.0.0.0 Safari/537.36 Wukong-Agent/1.0")
	req.Header.Set("Accept",
		"text/html,application/xhtml+xml,"+
			"application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,zh-CN;q=0.8")

	resp, err := c.client.Do(req)
	if err != nil {
		return &NavigateResult{
			Success: false,
			URL:     url,
			Error:   fmt.Sprintf("request failed: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	const maxBodySize = 2 * 1024 * 1024 // 2MB
	body, err := io.ReadAll(
		io.LimitReader(resp.Body, maxBodySize),
	)
	if err != nil {
		return &NavigateResult{
			Success:    false,
			URL:        url,
			StatusCode: resp.StatusCode,
			Error:      fmt.Sprintf("read body: %v", err),
		}, nil
	}

	content := string(body)
	title := extractTitle(content)

	const maxDisplaySize = 100000
	if len(content) > maxDisplaySize {
		content = content[:maxDisplaySize] +
			fmt.Sprintf("\n... [truncated, %d bytes total]",
				len(body))
	}

	success := resp.StatusCode >= 200 && resp.StatusCode < 400
	return &NavigateResult{
		Success:     success,
		URL:         url,
		Title:       title,
		Content:     content,
		ContentType: resp.Header.Get("Content-Type"),
		StatusCode:  resp.StatusCode,
	}, nil
}

// navigateWithBrowser fetches a URL using a headless browser,
// executing JavaScript and capturing the fully rendered page.
// Uses the configured browser backend (chromedp or go-rod).
func (c *Controller) navigateWithBrowser(
	ctx context.Context, url string,
) (*NavigateResult, error) {
	result, err := c.backend.Render(ctx, url)
	if err != nil {
		return &NavigateResult{
			Success: false,
			URL:     url,
			Error:   fmt.Sprintf("browser render: %v", err),
		}, nil
	}

	content := result.HTML
	title := result.Title

	const maxDisplaySize = 100000
	if len(content) > maxDisplaySize {
		content = content[:maxDisplaySize] +
			fmt.Sprintf("\n... [truncated, %d bytes total]",
				len(content))
	}

	return &NavigateResult{
		Success:     true,
		URL:         result.URL,
		Title:       title,
		Content:     content,
		ContentType: result.ContentType,
		StatusCode:  200,
	}, nil
}

// ExtractResult contains the result of content extraction.
type ExtractResult struct {
	Success  bool   `json:"success"`
	Text     string `json:"text,omitempty"`
	HTML     string `json:"html,omitempty"`
	Markdown string `json:"markdown,omitempty"`
	Error    string `json:"error,omitempty"`
}

// ExtractText fetches a URL and extracts human-readable text.
func (c *Controller) ExtractText(
	ctx context.Context, url string,
) (*ExtractResult, error) {
	result, err := c.Navigate(ctx, url)
	if err != nil {
		return &ExtractResult{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	if !result.Success {
		return &ExtractResult{
			Success: false,
			Error:   result.Error,
		}, nil
	}

	text := stripHTML(result.Content)
	markdown, _ := sanitize.ExtractMainContentMarkdown(result.Content)

	return &ExtractResult{
		Success:  true,
		Text:     text,
		HTML:     result.Content,
		Markdown: markdown,
	}, nil
}

// ScreenshotResult contains the result of a page screenshot.
type ScreenshotResult struct {
	Success    bool   `json:"success"`
	URL        string `json:"url"`
	ImagePath  string `json:"image_path,omitempty"`
	Title      string `json:"title,omitempty"`
	StatusCode int    `json:"status_code"`
	Error      string `json:"error,omitempty"`
}

// Screenshot captures a page screenshot.
// In browser mode (chromedp/go-rod): captures a real pixel screenshot as PNG.
// In HTTP mode: saves page content as a self-contained HTML file.
func (c *Controller) Screenshot(
	ctx context.Context, url string, outputPath string,
) (*ScreenshotResult, error) {
	if c.isBrowserMode() {
		// Preferred path: capture a real pixel screenshot via CDP.
		// If the capture fails for any reason, fall back to the
		// self-contained HTML snapshot so the user still gets output.
		imagePath, err := c.backend.Screenshot(ctx, url, outputPath)
		if err == nil {
			// Fetch title/status best-effort for the result metadata.
			navResult, _ := c.navigateWithBrowser(ctx, url)
			title, statusCode := "", 200
			if navResult != nil {
				title = navResult.Title
				if navResult.StatusCode != 0 {
					statusCode = navResult.StatusCode
				}
			}
			return &ScreenshotResult{
				Success:    true,
				URL:        url,
				ImagePath:  imagePath,
				Title:      title,
				StatusCode: statusCode,
			}, nil
		}
		logutil.Warn("browser screenshot failed, falling back to HTML snapshot", "error", err.Error())
		return c.screenshotWithHTTP(ctx, url, outputPath)
	}
	return c.screenshotWithHTTP(ctx, url, outputPath)
}

// screenshotWithHTTP saves page content as a self-contained HTML file.
func (c *Controller) screenshotWithHTTP(
	ctx context.Context, url string, outputPath string,
) (*ScreenshotResult, error) {
	navResult, err := c.Navigate(ctx, url)
	if err != nil {
		return &ScreenshotResult{
			Success: false,
			URL:     url,
			Error:   err.Error(),
		}, nil
	}

	if !navResult.Success {
		return &ScreenshotResult{
			Success:    false,
			URL:        url,
			StatusCode: navResult.StatusCode,
			Error:      navResult.Error,
		}, nil
	}

	htmlContent := navResult.Content
	screenshotHTML := buildScreenshotPage(
		navResult.Title, navResult.URL, htmlContent,
	)

	if err := os.WriteFile(
		outputPath, []byte(screenshotHTML), 0644,
	); err != nil {
		return &ScreenshotResult{
			Success: false,
			URL:     url,
			Error:   fmt.Sprintf("write screenshot: %v", err),
		}, nil
	}

	return &ScreenshotResult{
		Success:    true,
		URL:        url,
		ImagePath:  outputPath,
		Title:      navResult.Title,
		StatusCode: navResult.StatusCode,
	}, nil
}

// ClickResult contains the result of a click interaction.
type ClickResult struct {
	Success bool   `json:"success"`
	URL     string `json:"url"`
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}

// ClickElement navigates to a page and clicks an element by CSS selector.
// Only available in browser mode with chromedp backend.
func (c *Controller) ClickElement(
	ctx context.Context, url string, selector string,
) (*ClickResult, error) {
	if !c.isBrowserMode() {
		return &ClickResult{
			Success: false,
			URL:     url,
			Error:   "click interaction requires browser mode",
		}, nil
	}
	if !c.isChromedpBackend() {
		return &ClickResult{
			Success: false,
			URL:     url,
			Error:   "click interaction requires chromedp backend",
		}, nil
	}

	_, timeoutCancel := context.WithTimeout(
		ctx, c.cfg.Timeout,
	)
	defer timeoutCancel()

	cdpCtx, cdpCancel := chromedp.NewContext(c.chromedpCtx)
	defer cdpCancel()

	var htmlContent string
	// Navigate and wait for the page to settle.
	if err := chromedp.Run(cdpCtx, chromedp.Navigate(url)); err != nil {
		return &ClickResult{
			Success: false,
			URL:     url,
			Error:   fmt.Sprintf("navigate: %v", err),
		}, nil
	}
	c.waitForSettle(cdpCtx)

	err := chromedp.Run(cdpCtx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.Click(selector, chromedp.ByQuery),
		chromedp.Sleep(1*time.Second),
		chromedp.OuterHTML("html", &htmlContent),
	)
	if err != nil {
		return &ClickResult{
			Success: false,
			URL:     url,
			Error:   fmt.Sprintf("click element: %v", err),
		}, nil
	}

	const maxContent = 50000
	if len(htmlContent) > maxContent {
		htmlContent = htmlContent[:maxContent] + "..."
	}

	return &ClickResult{
		Success: true,
		URL:     url,
		Content: htmlContent,
	}, nil
}

// FillResult contains the result of a form fill interaction.
type FillResult struct {
	Success bool   `json:"success"`
	URL     string `json:"url"`
	Error   string `json:"error,omitempty"`
}

// FillForm fills an input field by CSS selector with the given value.
// Only available in browser mode with chromedp backend.
func (c *Controller) FillForm(
	ctx context.Context, url string,
	selector string, value string,
) (*FillResult, error) {
	if !c.isBrowserMode() {
		return &FillResult{
			Success: false,
			URL:     url,
			Error:   "form fill requires browser mode",
		}, nil
	}
	if !c.isChromedpBackend() {
		return &FillResult{
			Success: false,
			URL:     url,
			Error:   "form fill requires chromedp backend",
		}, nil
	}

	_, timeoutCancel := context.WithTimeout(
		ctx, c.cfg.Timeout,
	)
	defer timeoutCancel()

	cdpCtx, cdpCancel := chromedp.NewContext(c.chromedpCtx)
	defer cdpCancel()

	// Navigate and wait for page to settle.
	if err := chromedp.Run(cdpCtx, chromedp.Navigate(url)); err != nil {
		return &FillResult{
			Success: false,
			URL:     url,
			Error:   fmt.Sprintf("navigate: %v", err),
		}, nil
	}
	c.waitForSettle(cdpCtx)

	err := chromedp.Run(cdpCtx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.SendKeys(selector, value, chromedp.ByQuery),
	)
	if err != nil {
		return &FillResult{
			Success: false,
			URL:     url,
			Error:   fmt.Sprintf("fill form: %v", err),
		}, nil
	}

	return &FillResult{
		Success: true,
		URL:     url,
	}, nil
}

// Close releases all resources including the browser backend.
func (c *Controller) Close() error {
	c.client.CloseIdleConnections()

	if c.backend != nil {
		c.backend.Close()
	}
	if c.chromedpCancel != nil {
		c.chromedpCancel()
	}
	if c.allocCancel != nil {
		c.allocCancel()
	}

	return nil
}

// extractTitle extracts the <title> from HTML content.
func extractTitle(html string) string {
	lower := strings.ToLower(html)
	startTag := "<title>"
	endTag := "</title>"

	start := strings.Index(lower, startTag)
	if start == -1 {
		return ""
	}
	start += len(startTag)

	end := strings.Index(lower[start:], endTag)
	if end == -1 {
		return ""
	}

	title := strings.TrimSpace(html[start : start+end])
	if len(title) > 500 {
		title = title[:500] + "..."
	}
	return title
}

// buildScreenshotPage wraps page content in a self-contained HTML
// document suitable for offline viewing.
func buildScreenshotPage(
	title, url, content string,
) string {
	headerBar := fmt.Sprintf(
		`<div style="background:#2c3e50;color:#fff;padding:12px 20px;
		font-family:-apple-system,sans-serif;font-size:14px;
		border-radius:6px 6px 0 0;margin:-8px -8px 16px -8px;">
		<strong>%s</strong><br>
		<span style="opacity:0.7;font-size:12px;">%s</span>
		</div>`, title, url)

	wrapper := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1.0">
  <title>%s</title>
  <style>
    body {
      font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI',
                   Roboto, sans-serif;
      max-width: 1200px;
      margin: 0 auto;
      padding: 20px;
      background: #f8f9fa;
      color: #333;
      line-height: 1.6;
    }
    img { max-width: 100%%; height: auto; }
    pre { background: #f1f3f5; padding: 12px; border-radius: 4px;
          overflow-x: auto; }
    a { color: #3498db; }
  </style>
</head>
<body>
  %s
  %s
</body>
</html>`, title, headerBar, content)

	return wrapper
}

// stripHTML removes HTML tags and returns plain text.
func stripHTML(html string) string {
	var result strings.Builder
	inTag := false
	inScript := false
	inStyle := false

	lower := strings.ToLower(html)
	for i := 0; i < len(html); i++ {
		ch := html[i]

		if inTag {
			if ch == '>' {
				inTag = false
				if i >= 7 && lower[i-7:i+1] == "</script>" {
					inScript = false
				}
				if i >= 7 && lower[i-7:i+1] == "</style>" {
					inStyle = false
				}
			}
			continue
		}

		if ch == '<' {
			if i+7 < len(html) && lower[i:i+7] == "<script" {
				inScript = true
			}
			if i+6 < len(html) && lower[i:i+6] == "<style" {
				inStyle = true
			}
			inTag = true
			if !inScript && !inStyle {
				result.WriteByte(' ')
			}
			continue
		}

		if inScript || inStyle {
			continue
		}

		if ch == '&' {
			semi := strings.IndexByte(html[i:], ';')
			if semi > 0 && semi < 10 {
				entity := html[i : i+semi+1]
				switch entity {
				case "&amp;":
					result.WriteByte('&')
				case "&lt;":
					result.WriteByte('<')
				case "&gt;":
					result.WriteByte('>')
				case "&quot;":
					result.WriteByte('"')
				case "&nbsp;":
					result.WriteByte(' ')
				default:
					result.WriteByte(' ')
				}
				i += semi
				continue
			}
		}

		result.WriteByte(ch)
	}

	text := result.String()
	var compact strings.Builder
	prevSpace := false
	for _, r := range text {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !prevSpace {
				compact.WriteByte(' ')
				prevSpace = true
			}
		} else {
			compact.WriteRune(r)
			prevSpace = false
		}
	}

	return strings.TrimSpace(compact.String())
}
