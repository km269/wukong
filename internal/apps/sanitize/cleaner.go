// Package sanitize provides HTML sanitization functionality.
// It removes JavaScript, insecure elements, and dynamic content
// from HTML for safe offline viewing.
//
// The sanitizer uses golang.org/x/net/html for DOM-level parsing,
// avoiding the edge cases and bypass risks of regex-based cleaning.
package sanitize

import (
	"bytes"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// ---------------------------------------------------------------------------
// DOM-level sanitization
// ---------------------------------------------------------------------------

// CleanHTML removes JavaScript and other dynamic/insecure content from
// HTML using DOM tree traversal. This provides more reliable cleaning
// than regex-based approaches, correctly handling:
//   - Nested tags and malformed markup
//   - Script content that contains "</" patterns
//   - Event handlers on any element
//   - Inline style expressions (expression(), javascript: URLs)
//
// The returned HTML is a well-formed representation of the cleaned DOM.
func CleanHTML(htmlStr string) string {
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		// Fallback to regex cleaning on parse failure.
		htmlStr = removeScriptTags(htmlStr)
		htmlStr = removeInlineEvents(htmlStr)
		htmlStr = removeNoScriptTags(htmlStr)
		htmlStr = removeIframeTags(htmlStr)
		htmlStr = removeEmbedTags(htmlStr)
		htmlStr = removeBaseTag(htmlStr)
		htmlStr = removeMetaRefresh(htmlStr)
		return htmlStr
	}

	cleanDOM(doc)

	var buf bytes.Buffer
	if err := html.Render(&buf, doc); err != nil {
		return htmlStr
	}
	return buf.String()
}

// cleanDOM recursively traverses the DOM tree and removes unsafe content.
func cleanDOM(n *html.Node) {
	// Walk children first (may remove nodes during traversal).
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		cleanDOM(c)

		switch c.Type {
		case html.ElementNode:
			if isUnsafeElement(c) {
				n.RemoveChild(c)
			} else {
				cleanElementAttributes(c)
			}
		}
		c = next
	}
}

// isUnsafeElement checks if an element should be removed entirely.
func isUnsafeElement(n *html.Node) bool {
	switch n.Data {
	case "script", "noscript", "iframe", "embed",
		"object", "applet", "base":
		return true
	}
	return false
}

// cleanElementAttributes removes unsafe attributes from an element.
func cleanElementAttributes(n *html.Node) {
	var toRemove []string
	for _, attr := range n.Attr {
		if isUnsafeAttribute(n.Data, attr.Key, attr.Val) {
			toRemove = append(toRemove, attr.Key)
		}
	}
	for _, key := range toRemove {
		removeAttr(n, key)
	}
}

// isUnsafeAttribute checks if an attribute is unsafe and should be removed.
func isUnsafeAttribute(element, key, val string) bool {
	// Remove all inline event handlers (on* attributes).
	if strings.HasPrefix(key, "on") {
		return true
	}

	// Remove attributes with javascript: URLs.
	switch key {
	case "href", "src", "action", "formaction", "xlink:href":
		if strings.HasPrefix(strings.TrimSpace(strings.ToLower(val)),
			"javascript:") {
			return true
		}
	}

	// Remove meta refresh/redirect.
	if element == "meta" && key == "http-equiv" &&
		strings.EqualFold(val, "refresh") {
		return true
	}

	return false
}

// removeAttr removes an attribute by name from a node.
func removeAttr(n *html.Node, name string) {
	for i, attr := range n.Attr {
		if attr.Key == name {
			n.Attr = append(n.Attr[:i], n.Attr[i+1:]...)
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Regex fallback helpers (used when HTML parsing fails)
// ---------------------------------------------------------------------------

// removeScriptTags removes all <script> tags and their content.
func removeScriptTags(html string) string {
	re := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	html = re.ReplaceAllString(html, "")
	re = regexp.MustCompile(`(?is)<script[^>]*/>`)
	return re.ReplaceAllString(html, "")
}

// removeInlineEvents removes inline event handlers like onclick, onload.
func removeInlineEvents(html string) string {
	events := []string{
		"onclick", "ondblclick", "onmousedown", "onmouseup",
		"onmouseover", "onmouseout", "onmousemove",
		"onkeydown", "onkeyup", "onkeypress",
		"onfocus", "onblur", "onchange", "onsubmit",
		"onload", "onunload", "onerror", "onabort",
		"onresize", "onscroll", "onselect",
	}
	for _, event := range events {
		re := regexp.MustCompile(event + `="[^"]*"`)
		html = re.ReplaceAllString(html, "")
		re = regexp.MustCompile(event + `='[^']*'`)
		html = re.ReplaceAllString(html, "")
		re = regexp.MustCompile(event + `=[^\s>]*`)
		html = re.ReplaceAllString(html, "")
	}
	return html
}

// removeNoScriptTags removes <noscript> tags.
func removeNoScriptTags(html string) string {
	re := regexp.MustCompile(`(?is)<noscript[^>]*>.*?</noscript>`)
	return re.ReplaceAllString(html, "")
}

// removeIframeTags removes <iframe> tags.
func removeIframeTags(html string) string {
	re := regexp.MustCompile(`(?is)<iframe[^>]*>.*?</iframe>`)
	html = re.ReplaceAllString(html, "")
	re = regexp.MustCompile(`(?is)<iframe[^>]*/>`)
	return re.ReplaceAllString(html, "")
}

// removeEmbedTags removes <embed> and <object> tags.
func removeEmbedTags(html string) string {
	re := regexp.MustCompile(`(?is)<embed[^>]*>`)
	html = re.ReplaceAllString(html, "")
	re = regexp.MustCompile(`(?is)<object[^>]*>.*?</object>`)
	return re.ReplaceAllString(html, "")
}

// removeBaseTag removes <base> tags.
func removeBaseTag(html string) string {
	re := regexp.MustCompile(`(?is)<base[^>]*>`)
	return re.ReplaceAllString(html, "")
}

// removeMetaRefresh removes meta refresh tags.
func removeMetaRefresh(html string) string {
	re := regexp.MustCompile(`(?is)<meta[^>]*http-equiv="refresh"[^>]*>`)
	return re.ReplaceAllString(html, "")
}

// ---------------------------------------------------------------------------
// Asset and link extraction (regex-based, stable for URL patterns)
// ---------------------------------------------------------------------------

// Asset represents a discovered asset (CSS, JS, image, etc.).
type Asset struct {
	URL         string
	Type        string // css, js, img, font, media
	OriginalTag string
}

// ExtractAssets extracts asset URLs from HTML content.
func ExtractAssets(html, baseURL string) []string {
	assets := []string{}
	parsedBase, err := url.Parse(baseURL)
	if err != nil {
		return assets
	}

	// CSS links (4 common patterns)
	cssPattern := regexp.MustCompile(`<link[^>]*href="([^"]+)"[^>]*type="text/css"[^>]*>`)
	cssPattern2 := regexp.MustCompile(`<link[^>]*type="text/css"[^>]*href="([^"]+)"[^>]*>`)
	cssPattern3 := regexp.MustCompile(`<link[^>]*rel="stylesheet"[^>]*href="([^"]+)"[^>]*>`)
	cssPattern4 := regexp.MustCompile(`<link[^>]*href="([^"]+)"[^>]*rel="stylesheet"[^>]*>`)

	assets = appendAssets(assets, html, cssPattern, parsedBase)
	assets = appendAssets(assets, html, cssPattern2, parsedBase)
	assets = appendAssets(assets, html, cssPattern3, parsedBase)
	assets = appendAssets(assets, html, cssPattern4, parsedBase)

	// Images
	imgPattern := regexp.MustCompile(`<img[^>]*src="([^"]+)"[^>]*>`)
	assets = appendAssets(assets, html, imgPattern, parsedBase)

	// Image srcset
	srcsetPattern := regexp.MustCompile(`srcset="([^"]+)"`)
	srcsetMatches := srcsetPattern.FindAllStringSubmatch(html, -1)
	for _, match := range srcsetMatches {
		if len(match) > 1 {
			urls := strings.Split(match[1], ",")
			for _, u := range urls {
				u = strings.TrimSpace(u)
				parts := strings.Fields(u)
				if len(parts) > 0 {
					absURL := resolveURL(parts[0], parsedBase)
					assets = append(assets, absURL)
				}
			}
		}
	}

	// Video/audio source
	sourcePattern := regexp.MustCompile(`<source[^>]*src="([^"]+)"[^>]*>`)
	assets = appendAssets(assets, html, sourcePattern, parsedBase)

	// Background images
	bgPattern := regexp.MustCompile(`background(?:-image)?\s*:\s*url\(['"]?([^'")\s]+)['"]?\)`)
	assets = appendAssets(assets, html, bgPattern, parsedBase)

	// Favicon
	faviconPattern := regexp.MustCompile(`<link[^>]*rel="[^"]*icon[^"]*"[^>]*href="([^"]+)"[^>]*>`)
	assets = appendAssets(assets, html, faviconPattern, parsedBase)

	return deduplicate(assets)
}

// appendAssets extracts URLs using a pattern and resolves them.
func appendAssets(assets []string, html string, pattern *regexp.Regexp, baseURL *url.URL) []string {
	matches := pattern.FindAllStringSubmatch(html, -1)
	for _, match := range matches {
		if len(match) > 1 {
			absURL := resolveURL(match[1], baseURL)
			assets = append(assets, absURL)
		}
	}
	return assets
}

// ---------------------------------------------------------------------------
// Link extraction
// ---------------------------------------------------------------------------

// Link represents a discovered page link.
type Link struct {
	URL  string
	Text string
}

// ExtractLinks extracts page links from HTML content.
func ExtractLinks(html, baseURL, host string, includeSubdomains bool) []string {
	links := []string{}
	parsedBase, err := url.Parse(baseURL)
	if err != nil {
		return links
	}

	hrefPattern := regexp.MustCompile(`<a[^>]*href="([^"]+)"[^>]*>`)
	matches := hrefPattern.FindAllStringSubmatch(html, -1)

	for _, match := range matches {
		if len(match) > 1 {
			href := match[1]

			if strings.HasPrefix(href, "#") ||
				strings.HasPrefix(href, "javascript:") ||
				strings.HasPrefix(href, "mailto:") ||
				strings.HasPrefix(href, "tel:") {
				continue
			}

			absURL := resolveURL(href, parsedBase)
			parsedAbs, err := url.Parse(absURL)
			if err != nil {
				continue
			}

			if !isInScope(parsedAbs, host, includeSubdomains) {
				continue
			}

			if parsedAbs.Scheme != "http" && parsedAbs.Scheme != "https" {
				continue
			}

			links = append(links, absURL)
		}
	}

	return deduplicate(links)
}

// ---------------------------------------------------------------------------
// URL utilities
// ---------------------------------------------------------------------------

// resolveURL resolves a potentially relative URL to an absolute URL.
func resolveURL(href string, baseURL *url.URL) string {
	if strings.HasPrefix(href, "data:") {
		return href
	}
	if strings.HasPrefix(href, "//") {
		return baseURL.Scheme + ":" + href
	}
	parsedHref, err := url.Parse(href)
	if err != nil {
		return href
	}
	return baseURL.ResolveReference(parsedHref).String()
}

// isInScope checks if a URL is within the cloning scope.
func isInScope(parsedURL *url.URL, host string, includeSubdomains bool) bool {
	urlHost := parsedURL.Host
	if urlHost == host {
		return true
	}
	if includeSubdomains && strings.HasSuffix(urlHost, "."+host) {
		return true
	}
	return false
}

// deduplicate removes duplicate strings from a slice.
func deduplicate(items []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// DOM-based asset extraction (more reliable than regex)
// ---------------------------------------------------------------------------

// ExtractAssetsDOM extracts asset URLs from HTML using DOM traversal.
// This is more reliable than regex-based extraction, correctly handling:
//   - Self-closing tags and attribute order variations
//   - Nested elements within tags
//   - srcset/src attributes with complex URL patterns
func ExtractAssetsDOM(htmlStr, baseURL string) []string {
	parsedBase, err := url.Parse(baseURL)
	if err != nil {
		return nil
	}

	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		// Fall back to regex extraction on parse failure.
		return ExtractAssets(htmlStr, baseURL)
	}

	var assets []string
	seen := make(map[string]bool)
	extractAssetsFromNode(doc, parsedBase, &assets, seen)
	return assets
}

// extractAssetsFromNode recursively traverses the DOM and collects asset URLs.
func extractAssetsFromNode(n *html.Node, base *url.URL,
	assets *[]string, seen map[string]bool) {
	if n.Type == html.ElementNode {
		collectAssetURLs(n, base, assets, seen)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		extractAssetsFromNode(c, base, assets, seen)
	}
}

// collectAssetURLs extracts asset URLs from a single element's attributes.
func collectAssetURLs(n *html.Node, base *url.URL,
	assets *[]string, seen map[string]bool) {
	switch n.Data {
	case "link":
		// CSS stylesheets and favicons.
		rel := getAttr(n, "rel")
		href := getAttr(n, "href")
		if href != "" {
			if strings.Contains(strings.ToLower(rel), "stylesheet") ||
				strings.Contains(strings.ToLower(rel), "icon") {
				addResolvedURL(href, base, assets, seen)
			}
		}

	case "img":
		// src and srcset.
		if src := getAttr(n, "src"); src != "" {
			addResolvedURL(src, base, assets, seen)
		}
		if srcset := getAttr(n, "srcset"); srcset != "" {
			for _, part := range strings.Split(srcset, ",") {
				u := strings.TrimSpace(part)
				if idx := strings.LastIndex(u, " "); idx >= 0 {
					u = u[:idx] // Remove size descriptor.
				}
				addResolvedURL(strings.TrimSpace(u), base, assets, seen)
			}
		}

	case "source":
		if src := getAttr(n, "src"); src != "" {
			addResolvedURL(src, base, assets, seen)
		}

	case "script":
		if src := getAttr(n, "src"); src != "" {
			addResolvedURL(src, base, assets, seen)
		}

	case "style":
		// Inline CSS background-image URLs.
		extractCSSBackgroundURLs(n.FirstChild, base, assets, seen)
	}
}

// extractCSSBackgroundURLs extracts url() references from inline CSS.
func extractCSSBackgroundURLs(n *html.Node, base *url.URL,
	assets *[]string, seen map[string]bool) {
	if n == nil || n.Type != html.TextNode {
		return
	}
	cssPattern := regexp.MustCompile(
		`url\(['"]?([^'")\s]+)['"]?\)`)
	matches := cssPattern.FindAllStringSubmatch(n.Data, -1)
	for _, m := range matches {
		if len(m) > 1 {
			addResolvedURL(m[1], base, assets, seen)
		}
	}
}

// addResolvedURL resolves a URL and adds it to the list if not seen.
func addResolvedURL(rawURL string, base *url.URL,
	assets *[]string, seen map[string]bool) {
	absURL := resolveURL(rawURL, base)
	if !seen[absURL] {
		seen[absURL] = true
		*assets = append(*assets, absURL)
	}
}

// getAttr returns the value of the named attribute, or "" if not found.
func getAttr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// DOM-based link extraction
// ---------------------------------------------------------------------------

// ExtractLinksDOM extracts page links from HTML using DOM traversal.
// This handles attribute ordering, self-closing tags, and nested elements
// more reliably than regex.
func ExtractLinksDOM(htmlStr, baseURL, host string,
	includeSubdomains bool) []string {
	parsedBase, err := url.Parse(baseURL)
	if err != nil {
		return nil
	}

	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return ExtractLinks(htmlStr, baseURL, host, includeSubdomains)
	}

	var links []string
	seen := make(map[string]bool)
	extractLinksFromNode(doc, parsedBase, host,
		includeSubdomains, &links, seen)
	return links
}

// extractLinksFromNode recursively traverses the DOM for <a href>.
func extractLinksFromNode(n *html.Node, base *url.URL, host string,
	includeSubdomains bool, links *[]string, seen map[string]bool) {
	if n.Type == html.ElementNode && n.Data == "a" {
		href := getAttr(n, "href")
		if href != "" && !isSpecialLink(href) {
			absURL := resolveURL(href, base)
			parsed, err := url.Parse(absURL)
			if err == nil &&
				(parsed.Scheme == "http" || parsed.Scheme == "https") &&
				isInScope(parsed, host, includeSubdomains) &&
				!seen[absURL] {
				seen[absURL] = true
				*links = append(*links, absURL)
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		extractLinksFromNode(c, base, host,
			includeSubdomains, links, seen)
	}
}

// isSpecialLink returns true for non-navigational href values.
func isSpecialLink(href string) bool {
	return strings.HasPrefix(href, "#") ||
		strings.HasPrefix(href, "javascript:") ||
		strings.HasPrefix(href, "mailto:") ||
		strings.HasPrefix(href, "tel:")
}

// ---------------------------------------------------------------------------
// Link rewriting
// ---------------------------------------------------------------------------

// RewriteLinks rewrites absolute URLs to relative local paths.
func RewriteLinks(html string, baseURL string) string {
	parsedBase, err := url.Parse(baseURL)
	if err != nil {
		return html
	}

	hrefPattern := regexp.MustCompile(`href="([^"]+)"`)
	html = hrefPattern.ReplaceAllStringFunc(html, func(match string) string {
		submatch := hrefPattern.FindStringSubmatch(match)
		if len(submatch) < 2 {
			return match
		}
		originalURL := submatch[1]
		if strings.HasPrefix(originalURL, "#") ||
			strings.HasPrefix(originalURL, "javascript:") ||
			strings.HasPrefix(originalURL, "mailto:") ||
			strings.HasPrefix(originalURL, "tel:") ||
			strings.HasPrefix(originalURL, "data:") {
			return match
		}
		parsedURL, err := url.Parse(originalURL)
		if err != nil {
			return match
		}
		if parsedURL.Host != parsedBase.Host {
			return match
		}
		localPath := urlToLocalPath(parsedURL, parsedBase)
		return `href="pages/` + localPath + `"`
	})

	srcPattern := regexp.MustCompile(`src="([^"]+)"`)
	html = srcPattern.ReplaceAllStringFunc(html, func(match string) string {
		submatch := srcPattern.FindStringSubmatch(match)
		if len(submatch) < 2 {
			return match
		}
		originalURL := submatch[1]
		if strings.HasPrefix(originalURL, "data:") {
			return match
		}
		parsedURL, err := url.Parse(originalURL)
		if err != nil {
			return match
		}
		if parsedURL.Host != parsedBase.Host {
			return match
		}
		localPath := urlToLocalPath(parsedURL, parsedBase)
		return `src="assets/` + localPath + `"`
	})

	return html
}

// urlToLocalPath converts a URL to a local file path.
func urlToLocalPath(parsedURL *url.URL, baseURL *url.URL) string {
	path := parsedURL.Path
	path = strings.TrimPrefix(path, baseURL.Path)
	if path == "" || path == "/" {
		return "index.html"
	}
	path = strings.ReplaceAll(path, "/", "_")
	path = strings.ReplaceAll(path, "?", "_")
	path = strings.ReplaceAll(path, "&", "_")
	return path
}
