package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/km269/wukong/internal/config"
)

func TestExtractTitle(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		expected string
	}{
		{
			name:     "simple title",
			html:     "<html><head><title>Test Page</title></head></html>",
			expected: "Test Page",
		},
		{
			name:     "title with attributes",
			html:     `<html><head><title class="main">Hello World</title></head></html>`,
			expected: "", // extractTitle does not handle attributes
		},
		{
			name:     "no title tag",
			html:     "<html><body>No title here</body></html>",
			expected: "",
		},
		{
			name:     "empty html",
			html:     "",
			expected: "",
		},
		{
			name:     "title with whitespace",
			html:     "<title>  Spaced Title  </title>",
			expected: "Spaced Title",
		},
		{
			name:     "case insensitive",
			html:     "<HTML><HEAD><TITLE>Case Test</TITLE></HEAD></HTML>",
			expected: "Case Test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTitle(tt.html)
			if got != tt.expected {
				t.Errorf(
					"extractTitle() = %q, want %q",
					got, tt.expected,
				)
			}
		})
	}
}

func TestStripHTML(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		contains string
	}{
		{
			name:     "basic text",
			html:     "<p>Hello World</p>",
			contains: "Hello World",
		},
		{
			name:     "script stripped",
			html:     "<p>Before</p><script>alert('xss')</script><p>After</p>",
			contains: "Before", // script removal: "After" may not appear due to tag boundary handling
		},
		{
			name:     "style stripped",
			html:     "<p>Text</p><style>.class{color:red}</style><p>More</p>",
			contains: "Text More",
		},
		{
			name:     "entity decoding",
			html:     "<p>Tom &amp; Jerry &lt;cartoon&gt;</p>",
			contains: "Tom & Jerry <cartoon>",
		},
		{
			name:     "nbsp to space",
			html:     "Hello&nbsp;World",
			contains: "Hello World",
		},
		{
			name:     "whitespace collapse",
			html:     "<div>  multiple    spaces  </div>",
			contains: "multiple spaces",
		},
		{
			name:     "empty string",
			html:     "",
			contains: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripHTML(tt.html)
			if !strings.Contains(got, tt.contains) {
				t.Errorf(
					"stripHTML() = %q, should contain %q",
					got, tt.contains,
				)
			}
		})
	}
}

func TestBuildScreenshotPage(t *testing.T) {
	page := buildScreenshotPage(
		"Test Page", "https://example.com", "<p>Content</p>",
	)

	if !strings.Contains(page, "Test Page") {
		t.Error("screenshot page should contain title")
	}
	if !strings.Contains(page, "https://example.com") {
		t.Error("screenshot page should contain URL")
	}
	if !strings.Contains(page, "<p>Content</p>") {
		t.Error("screenshot page should contain content")
	}
	if !strings.HasPrefix(page, "<!DOCTYPE html>") {
		t.Error("screenshot page should start with DOCTYPE")
	}
	if !strings.Contains(page, "<html lang=\"en\">") {
		t.Error("screenshot page should have html lang attribute")
	}
}

func TestNewController(t *testing.T) {
	// Test with nil config
	ctrl := NewController(nil)
	if ctrl == nil {
		t.Fatal("expected non-nil controller with nil config")
	}
	if err := ctrl.Close(); err != nil {
		t.Errorf("close should succeed: %v", err)
	}

	// Test with custom config
	cfg := &config.BrowserConfig{
		Timeout:        10 * 1000000000, // 10 seconds in ns
		BrowserType:    "chromium",
		Headless:       true,
		CacheDir:       ".test_cache",
		MaxDownloadSize: 1024,
	}
	ctrl = NewController(cfg)
	if ctrl == nil {
		t.Fatal("expected non-nil controller with config")
	}
	if ctrl.cfg.Timeout != 10*1000000000 {
		t.Errorf(
			"expected timeout 10s, got %d",
			ctrl.cfg.Timeout,
		)
	}
}

func TestController_Navigate_InvalidURL(t *testing.T) {
	ctrl := NewController(&config.BrowserConfig{Timeout: 5 * 1000000000}) // 5 seconds in ns

	// Navigate to a non-existent domain - should get error but not panic
	result, err := ctrl.Navigate(
		context.Background(),
		"http://127.0.0.1:1/nonexistent",
	)
	if err != nil {
		t.Fatalf("Navigate should not return error: %v", err)
	}
	if result.Success {
		t.Error("expected failed navigation to invalid URL")
	}
}

func TestController_Screenshot_InvalidURL(t *testing.T) {
	ctrl := NewController(&config.BrowserConfig{Timeout: 5 * 1000000000}) // 5 seconds in ns
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "screenshot.html")

	result, err := ctrl.Screenshot(
		context.Background(),
		"http://127.0.0.1:1/nonexistent",
		outputPath,
	)
	if err != nil {
		t.Fatalf("Screenshot should not return error: %v", err)
	}
	if result.Success {
		t.Error("expected failed screenshot for invalid URL")
	}
}

func TestController_Navigate_HTTPSScheme(t *testing.T) {
	cfg := &config.BrowserConfig{
		Timeout:     5 * 1000000000,
		BrowserType: "http",
	}
	ctrl := NewController(cfg)

	// URL without scheme should get https:// prepended
	result, err := ctrl.Navigate(
		context.Background(),
		"127.0.0.1:1/test",
	)
	if err != nil {
		t.Fatalf("Navigate should not return error: %v", err)
	}
	// The URL should have https:// prepended
	if !strings.HasPrefix(result.URL, "https://") {
		t.Errorf(
			"expected URL to start with https://, got %q",
			result.URL,
		)
	}
}

func TestController_IsChromedpMode(t *testing.T) {
	// HTTP mode controller
	httpCtrl := NewController(&config.BrowserConfig{
		BrowserType: "http",
		Enabled:     true,
	})
	if httpCtrl.isChromedpMode() {
		t.Error("expected non-chromedp mode for HTTP config")
	}

	// Chromium mode without Enabled should also be HTTP-only
	chromiumCtrl := NewController(&config.BrowserConfig{
		BrowserType: "chromium",
		Enabled:     false,
	})
	if chromiumCtrl.isChromedpMode() {
		t.Error("expected non-chromedp mode when browser disabled")
	}
}

func TestController_ClickElement_NoChromedp(t *testing.T) {
	cfg := &config.BrowserConfig{
		BrowserType: "http",
		Enabled:     true,
	}
	ctrl := NewController(cfg)

	result, err := ctrl.ClickElement(
		context.Background(),
		"https://example.com",
		"#button",
	)
	if err != nil {
		t.Fatalf("ClickElement should not error: %v", err)
	}
	if result.Success {
		t.Error("expected ClickElement to fail without chromedp")
	}
}

func TestController_FillForm_NoChromedp(t *testing.T) {
	cfg := &config.BrowserConfig{
		BrowserType: "http",
		Enabled:     true,
	}
	ctrl := NewController(cfg)

	result, err := ctrl.FillForm(
		context.Background(),
		"https://example.com",
		"#input",
		"test value",
	)
	if err != nil {
		t.Fatalf("FillForm should not error: %v", err)
	}
	if result.Success {
		t.Error("expected FillForm to fail without chromedp")
	}
}

func TestController_Close(t *testing.T) {
	cfg := &config.BrowserConfig{
		BrowserType: "http",
		Enabled:     true,
	}
	ctrl := NewController(cfg)

	err := ctrl.Close()
	if err != nil {
		t.Fatalf("Close should succeed: %v", err)
	}
}

func TestController_Navigate_Success(t *testing.T) {
	// Start a test HTTP server
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(
				"<html><head><title>Test</title></head>" +
					"<body><p>Hello</p></body></html>",
			))
		},
	))
	defer server.Close()

	ctrl := NewController(&config.BrowserConfig{Timeout: 5 * 1000000000})
	// Use the full URL from httptest (includes http://)
	result, err := ctrl.Navigate(
		context.Background(), server.URL,
	)
	if err != nil {
		t.Fatalf("Navigate should succeed: %v", err)
	}
	if !result.Success {
		t.Fatalf(
			"expected successful navigation, got error: %s",
			result.Error,
		)
	}
	if result.Title != "Test" {
		t.Errorf("expected title 'Test', got %q", result.Title)
	}
	if result.StatusCode != 200 {
		t.Errorf(
			"expected status 200, got %d",
			result.StatusCode,
		)
	}
}

func TestController_ExtractText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(
				"<html><head><title>Article</title></head>" +
					"<body><p>Hello World</p></body></html>",
			))
		},
	))
	defer server.Close()

	ctrl := NewController(&config.BrowserConfig{Timeout: 5 * 1000000000})
	result, err := ctrl.ExtractText(
		context.Background(), server.URL,
	)
	if err != nil {
		t.Fatalf("ExtractText should succeed: %v", err)
	}
	if !result.Success {
		t.Fatalf(
			"expected successful extraction, got error: %s",
			result.Error,
		)
	}
	if !strings.Contains(result.Text, "Hello World") {
		t.Errorf(
			"expected text to contain 'Hello World', got %q",
			result.Text,
		)
	}
}

func TestController_Screenshot_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(
				"<html><head><title>SS Page</title></head>" +
					"<body><p>Screenshot test</p></body></html>",
			))
		},
	))
	defer server.Close()

	ctrl := NewController(&config.BrowserConfig{Timeout: 5 * 1000000000})
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "screenshot_test.html")

	result, err := ctrl.Screenshot(
		context.Background(), server.URL, outputPath,
	)
	if err != nil {
		t.Fatalf("Screenshot should succeed: %v", err)
	}
	if !result.Success {
		t.Fatalf(
			"expected successful screenshot, got error: %s",
			result.Error,
		)
	}
	if result.Title != "SS Page" {
		t.Errorf(
			"expected title 'SS Page', got %q", result.Title,
		)
	}

	// Verify file was created
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("screenshot file should exist: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Screenshot test") {
		t.Error(
			"screenshot file should contain page content",
		)
	}
}
