// Package sanitize provides HTML sanitization.
package sanitize

import (
	"net/url"
	"strings"
	"testing"
)

func TestCleanHTML_RemovesScriptTags(t *testing.T) {
	input := `<html><head><script>alert('xss')</script></head><body>Safe</body></html>`
	result := CleanHTML(input)
	if strings.Contains(result, "alert") {
		t.Error("script content should be removed")
	}
	if !strings.Contains(result, "Safe") {
		t.Error("safe content should be preserved")
	}
}

func TestCleanHTML_RemovesEventHandlers(t *testing.T) {
	input := `<div onclick="evil()" onload="bad()">Content</div>`
	result := CleanHTML(input)
	if strings.Contains(result, "onclick") {
		t.Error("onclick handler should be removed")
	}
	if !strings.Contains(result, "Content") {
		t.Error("content should be preserved")
	}
}

func TestCleanHTML_RemovesIframe(t *testing.T) {
	input := `<div><iframe src="evil.com"></iframe></div>`
	result := CleanHTML(input)
	if strings.Contains(result, "iframe") {
		t.Error("iframe should be removed")
	}
}

func TestCleanHTML_RemovesNoscript(t *testing.T) {
	input := `<noscript>Fallback</noscript><p>Safe</p>`
	result := CleanHTML(input)
	if strings.Contains(result, "Fallback") {
		t.Error("noscript content should be removed")
	}
	if !strings.Contains(result, "Safe") {
		t.Error("safe content after noscript should be preserved")
	}
}

func TestCleanHTML_RemovesEmbed(t *testing.T) {
	input := `<embed src="bad.swf"><object data="bad"></object><p>Safe</p>`
	result := CleanHTML(input)
	if strings.Contains(result, "embed") || strings.Contains(result, "object") {
		t.Error("embed/object should be removed")
	}
	if !strings.Contains(result, "Safe") {
		t.Error("safe content should be preserved")
	}
}

func TestCleanHTML_RemovesBaseTag(t *testing.T) {
	input := `<head><base href="evil.com/"><title>Test</title></head>`
	result := CleanHTML(input)
	if strings.Contains(result, "<base") {
		t.Error("base tag should be removed")
	}
	if !strings.Contains(result, "Test") {
		t.Error("title should be preserved")
	}
}

func TestCleanHTML_RemovesMetaRefresh(t *testing.T) {
	input := `<meta http-equiv="refresh" content="0;url=evil.com"><p>Safe</p>`
	result := CleanHTML(input)
	if strings.Contains(result, "refresh") {
		t.Error("meta refresh should be removed")
	}
}

func TestCleanHTML_JavascriptURL_Removed(t *testing.T) {
	input := `<a href="javascript:evil()">Click</a>`
	result := CleanHTML(input)
	if strings.Contains(result, "javascript:") {
		t.Error("javascript: URL in href should be removed")
	}
}

func TestCleanHTML_EmptyInput(t *testing.T) {
	result := CleanHTML("")
	if result == "" {
		t.Log("empty input produces output (DOM wrapper)")
	}
}

func TestExtractAssetsDOM_CSS(t *testing.T) {
	input := `<link rel="stylesheet" href="style.css">`
	assets := ExtractAssetsDOM(input, "http://example.com/page.html")
	if len(assets) != 1 {
		t.Fatalf("expected 1 asset, got %d", len(assets))
	}
	if assets[0] != "http://example.com/style.css" {
		t.Errorf("expected style.css URL, got %q", assets[0])
	}
}

func TestExtractAssetsDOM_Img(t *testing.T) {
	input := `<img src="logo.png" alt="Logo">`
	assets := ExtractAssetsDOM(input, "http://example.com/")
	if len(assets) != 1 {
		t.Fatalf("expected 1 asset, got %d", len(assets))
	}
	if assets[0] != "http://example.com/logo.png" {
		t.Errorf("expected logo.png URL, got %q", assets[0])
	}
}

func TestExtractAssetsDOM_Srcset(t *testing.T) {
	input := `<img srcset="img1.jpg 1x, img2.jpg 2x" src="fallback.jpg">`
	assets := ExtractAssetsDOM(input, "http://example.com/")
	if len(assets) < 2 {
		t.Errorf("expected at least 2 srcset assets, got %d", len(assets))
	}
}

func TestExtractAssetsDOM_Favicon(t *testing.T) {
	input := `<link rel="icon" href="favicon.ico">`
	assets := ExtractAssetsDOM(input, "http://example.com/")
	if len(assets) != 1 {
		t.Fatalf("expected 1 asset, got %d", len(assets))
	}
	if assets[0] != "http://example.com/favicon.ico" {
		t.Errorf("expected favicon.ico, got %q", assets[0])
	}
}

func TestExtractAssetsDOM_Deduplicate(t *testing.T) {
	input := `<img src="a.png"><img src="a.png"><img src="b.png">`
	assets := ExtractAssetsDOM(input, "http://example.com/")
	if len(assets) != 2 {
		t.Errorf("expected 2 unique assets, got %d: %v", len(assets), assets)
	}
}

func TestExtractAssetsDOM_Script(t *testing.T) {
	input := `<script src="app.js"></script>`
	assets := ExtractAssetsDOM(input, "http://example.com/")
	if len(assets) != 1 {
		t.Fatalf("expected 1 script asset, got %d", len(assets))
	}
	if assets[0] != "http://example.com/app.js" {
		t.Errorf("expected app.js, got %q", assets[0])
	}
}

func TestExtractLinksDOM_Basic(t *testing.T) {
	input := `<a href="/page1">Page 1</a><a href="/page2">Page 2</a>`
	links := ExtractLinksDOM(input, "http://example.com/", "example.com", false)
	if len(links) != 2 {
		t.Errorf("expected 2 links, got %d", len(links))
	}
}

func TestExtractLinksDOM_SkipSpecial(t *testing.T) {
	input := `<a href="#anchor">A</a><a href="javascript:void(0)">B</a><a href="mailto:a@b.com">C</a>`
	links := ExtractLinksDOM(input, "http://example.com/", "example.com", false)
	if len(links) != 0 {
		t.Errorf("expected 0 links (all special), got %d", len(links))
	}
}

func TestExtractLinksDOM_ScopeFilter(t *testing.T) {
	input := `<a href="http://other.com/page">Other</a>`
	links := ExtractLinksDOM(input, "http://example.com/", "example.com", false)
	if len(links) != 0 {
		t.Errorf("expected 0 links (out of scope), got %d", len(links))
	}
}

func TestExtractLinksDOM_Subdomains(t *testing.T) {
	input := `<a href="http://sub.example.com/page">Sub</a>`
	links := ExtractLinksDOM(input, "http://example.com/", "example.com", true)
	if len(links) != 1 {
		t.Errorf("expected 1 link (subdomain), got %d", len(links))
	}
}

func TestExtractLinksDOM_Deduplicate(t *testing.T) {
	input := `<a href="/page">P1</a><a href="/page">P2</a>`
	links := ExtractLinksDOM(input, "http://example.com/", "example.com", false)
	if len(links) != 1 {
		t.Errorf("expected 1 unique link, got %d", len(links))
	}
}

func TestResolveURL_Absolute(t *testing.T) {
	base, _ := url.Parse("http://example.com/dir/page.html")
	result := resolveURL("http://other.com/", base)
	if result != "http://other.com/" {
		t.Errorf("expected absolute URL, got %q", result)
	}
}

func TestResolveURL_Relative(t *testing.T) {
	base, _ := url.Parse("http://example.com/dir/page.html")
	result := resolveURL("../style.css", base)
	if result != "http://example.com/style.css" {
		t.Errorf("expected resolved relative URL, got %q", result)
	}
}

func TestResolveURL_Data(t *testing.T) {
	base, _ := url.Parse("http://example.com/")
	result := resolveURL("data:image/png;base64,xxx", base)
	if result != "data:image/png;base64,xxx" {
		t.Error("data: URLs should pass through unchanged")
	}
}

func TestResolveURL_ProtocolRelative(t *testing.T) {
	base, _ := url.Parse("https://example.com/")
	result := resolveURL("//cdn.example.com/lib.js", base)
	expected := "https://cdn.example.com/lib.js"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestIsInScope_SameHost(t *testing.T) {
	parsed, _ := url.Parse("http://example.com/page")
	if !isInScope(parsed, "example.com", false) {
		t.Error("same host should be in scope")
	}
}

func TestIsInScope_DifferentHost(t *testing.T) {
	parsed, _ := url.Parse("http://other.com/page")
	if isInScope(parsed, "example.com", false) {
		t.Error("different host should be out of scope")
	}
}

func TestIsInScope_Subdomain(t *testing.T) {
	parsed, _ := url.Parse("http://sub.example.com/page")
	if !isInScope(parsed, "example.com", true) {
		t.Error("subdomain should be in scope when enabled")
	}
	if isInScope(parsed, "example.com", false) {
		t.Error("subdomain should be out of scope when disabled")
	}
}

func TestDeduplicate(t *testing.T) {
	items := []string{"a", "b", "a", "c", "b"}
	result := deduplicate(items)
	if len(result) != 3 {
		t.Errorf("expected 3 unique items, got %d", len(result))
	}
}

func TestRemoveScriptTags_RegexFallback(t *testing.T) {
	input := `<html><script>var x=1;</script><p>Hi</p></html>`
	result := removeScriptTags(input)
	if strings.Contains(result, "var x=1") {
		t.Error("script content should be removed")
	}
	if !strings.Contains(result, "Hi") {
		t.Error("non-script content should be preserved")
	}
}
