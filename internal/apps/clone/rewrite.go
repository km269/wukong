// Package clone provides website cloning functionality.
//
// rewrite.go: DOM-based HTML link and asset reference rewriting.
// Walks the parsed HTML tree and replaces all external URL references
// (href, src, srcset, poster, data, etc.) with local paths.
// Detects page links vs asset links automatically and delegates
// path generation via a callback (RewriteSink).
package clone

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/km269/wukong/internal/util"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// RewriteSink receives an absolute URL and its resource kind, and returns
// the local path to use in the rewritten HTML. Return "" to keep the
// original URL unchanged (e.g., for external links).
type RewriteSink func(absURL string, kind URLKind) (localPath string)

// RewriteHTML walks the parsed HTML DOM tree and rewrites all external
// URL references to local paths using the provided sink callback.
//
// Supported elements and attributes:
//
//	<a href>, <area href>          → page/asset (based on LikelyPage)
//	<link href>                    → asset (stylesheet, icon, preload, etc.)
//	<img src/srcset>               → asset
//	<source src/srcset>            → asset
//	<video src/poster>             → asset
//	<audio src>, <track src>       → asset
//	<embed src>, <object data>     → asset
//	<iframe src>, <frame src>      → page/asset
//	<script src>                   → asset (kept if already local)
//	<style> text content           → CSS url() rewriting
//	style="" attribute              → inline CSS url() rewriting
func RewriteHTML(root *html.Node, base *url.URL, sink RewriteSink) {
	walkAndRewrite(root, base, sink)
}

// walkAndRewrite recursively traverses the DOM and rewrites each element.
// Honeypot elements (hidden links/forms designed to trap crawlers) are
// skipped — their URLs are left as-is to avoid triggering anti-bot systems.
func walkAndRewrite(n *html.Node, base *url.URL, sink RewriteSink) {
	if n.Type == html.ElementNode {
		if !isHoneypotNode(n) {
			rewriteElement(n, base, sink)
		}
		// Note: children of honeypot elements are still traversed —
		// only the honeypot element itself (and its links) are skipped.
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkAndRewrite(c, base, sink)
	}
}

// isHoneypotNode detects invisible elements that are likely anti-bot traps.
// Checks the element itself AND its ancestor chain against known honeypot
// patterns: display:none, visibility:hidden, opacity:0, HTML5 hidden attr,
// aria-hidden="true", and common hidden class names.
func isHoneypotNode(n *html.Node) bool {
	// Walk up the ancestor chain — any hidden ancestor taints the child.
	for cur := n; cur != nil; cur = cur.Parent {
		if isHoneypotElement(cur) {
			return true
		}
	}
	return false
}

// isHoneypotElement checks a single element for honeypot indicators.
func isHoneypotElement(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}

	// HTML5 hidden attribute.
	if hasAttr(n, "hidden") {
		return true
	}

	// aria-hidden="true"
	if getAttrLower(n, "aria-hidden") == "true" {
		return true
	}

	// Check inline style for display:none/visibility:hidden/opacity:0.
	styleVal := getAttrLower(n, "style")
	if styleVal != "" {
		if strings.Contains(styleVal, "display:none") ||
			strings.Contains(styleVal, "display: none") ||
			strings.Contains(styleVal, "visibility:hidden") ||
			strings.Contains(styleVal, "visibility: hidden") ||
			strings.Contains(styleVal, "opacity:0") ||
			strings.Contains(styleVal, "opacity: 0") {
			return true
		}
	}

	// Check class attribute for common hidden class names.
	classVal := getAttrLower(n, "class")
	if classVal != "" {
		for _, c := range honeypotClasses {
			if strings.Contains(classVal, c) {
				return true
			}
		}
	}

	return false
}

// honeypotClasses lists CSS class names commonly used to hide elements.
var honeypotClasses = []string{
	"hidden", "hide", "invisible",
	"d-none", "d_none",
	"visually-hidden", "visuallyhidden",
	"sr-only", "sr_only",
	"screen-reader-only",
	"display-none",
	"opacity-0",
	"zero-height",
}

// getAttrLower returns the lowercased value of a named attribute, or "".
func getAttrLower(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return strings.ToLower(a.Val)
		}
	}
	return ""
}

// hasAttr returns true if the element has the named attribute (any value).
func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return true
		}
	}
	return false
}

// resolveLazyLoad converts lazy-loading attributes to standard src.
// Many websites use data-src, data-lazy-src, or data-original for lazy loading.
// After the browser scrolls and loads the image, these attributes contain
// the actual image URL while src may contain a placeholder.
func resolveLazyLoad(n *html.Node) {
	if n.Type != html.ElementNode {
		return
	}

	lazyAttrs := []string{"data-src", "data-lazy-src", "data-original", "data-srcset"}
	foundCount := 0

	for _, attr := range lazyAttrs {
		val := getAttrCI(n, attr)
		if val != "" {
			foundCount++
			var targetAttr string
			var targetVal string

			if attr == "data-src" || attr == "data-lazy-src" || attr == "data-original" {
				targetAttr = "src"
				targetVal = val
				setAttrCI(n, "src", val)
			} else if attr == "data-srcset" {
				targetAttr = "srcset"
				targetVal = val
				setAttrCI(n, "srcset", val)
			}

			existingVal := getAttrCI(n, targetAttr)
			util.Logger.Debug("[wukong/clone/lazy] converted",
				"element", n.Data,
				"from_attr", attr,
				"from_val", truncateForLog(val, 100),
				"to_attr", targetAttr,
				"to_val", truncateForLog(targetVal, 100),
				"existing", existingVal)
		}
	}

	if foundCount > 0 {
		util.Logger.Debug("[wukong/clone/lazy] processed",
			"element", n.Data,
			"count", foundCount)
	}
}

// truncateForLog truncates a string for log output to avoid overly long lines.
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// setAttrCI sets an attribute value, creating it if it doesn't exist.
func setAttrCI(n *html.Node, key, val string) {
	for i, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

// rewriteElement dispatches to the appropriate rewriter based on element tag.
func rewriteElement(n *html.Node, base *url.URL, sink RewriteSink) {
	switch n.DataAtom {
	// Page-or-Asset elements.
	case atom.A, atom.Area:
		rewriteAttr(n, base, "href", sink, pageOrAssetKind)
	case atom.Iframe, atom.Frame:
		rewriteAttr(n, base, "src", sink, pageOrAssetKind)

	// Asset-only elements.
	case atom.Link:
		rewriteLink(n, base, sink)
	case atom.Img:
		resolveLazyLoad(n)
		rewriteAttr(n, base, "src", sink, alwaysAsset)
		rewriteSrcset(n, base, sink)
	case atom.Source:
		resolveLazyLoad(n)
		rewriteAttr(n, base, "src", sink, alwaysAsset)
		rewriteSrcset(n, base, sink)
	case atom.Video:
		rewriteAttr(n, base, "src", sink, alwaysAsset)
		rewriteAttr(n, base, "poster", sink, alwaysAsset)
	case atom.Audio:
		rewriteAttr(n, base, "src", sink, alwaysAsset)
	case atom.Track:
		rewriteAttr(n, base, "src", sink, alwaysAsset)
	case atom.Embed:
		rewriteAttr(n, base, "src", sink, alwaysAsset)
	case atom.Object:
		rewriteAttr(n, base, "data", sink, alwaysAsset)
	case atom.Script:
		rewriteAttr(n, base, "src", sink, alwaysAsset)

	// CSS rewriting.
	case atom.Style:
		rewriteStyleElement(n, base, sink)
	}

	// Inline style attribute on any element.
	rewriteInlineStyle(n, base, sink)
}

// ---------------------------------------------------------------------------
// Attribute rewriting.
// ---------------------------------------------------------------------------

// kindDecider returns the URLKind for a given absolute URL.
type kindDecider func(absURL string) URLKind

func alwaysAsset(absURL string) URLKind { return KindAsset }
func pageOrAssetKind(absURL string) URLKind {
	if LikelyPage(absURL) {
		return KindPage
	}
	return KindAsset
}

// rewriteAttr finds an attribute by name (case-insensitive), normalizes its
// value, and replaces it with the result of the sink callback.
func rewriteAttr(n *html.Node, base *url.URL, attrName string, sink RewriteSink, decide kindDecider) {
	for i, a := range n.Attr {
		if !strings.EqualFold(a.Key, attrName) {
			continue
		}

		val := strings.TrimSpace(a.Val)
		if val == "" {
			return
		}

		// Skip non-fetchable values.
		if shouldSkipRef(val) {
			return
		}

		// Normalize the reference.
		absURL, err := Normalize(base.String(), val)
		if err != nil || absURL == "" {
			return
		}

		kind := decide(absURL)
		localPath := sink(absURL, kind)
		if localPath == "" {
			return // Sink chose to keep original.
		}

		n.Attr[i].Val = localPath
		return
	}
}

// rewriteSrcset handles the srcset attribute which contains comma-separated
// URL-descriptor pairs like "img.jpg 1x, img2x.jpg 2x".
func rewriteSrcset(n *html.Node, base *url.URL, sink RewriteSink) {
	for i, a := range n.Attr {
		if !strings.EqualFold(a.Key, "srcset") {
			continue
		}

		parts := strings.Split(a.Val, ",")
		var rewritten []string

		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}

			fields := strings.Fields(part)
			if len(fields) == 0 {
				continue
			}

			urlStr := fields[0]
			if shouldSkipRef(urlStr) {
				rewritten = append(rewritten, part)
				continue
			}

			absURL, err := Normalize(base.String(), urlStr)
			if err != nil || absURL == "" {
				rewritten = append(rewritten, part)
				continue
			}

			localPath := sink(absURL, KindAsset)
			if localPath == "" {
				rewritten = append(rewritten, part)
				continue
			}

			fields[0] = localPath
			rewritten = append(rewritten, strings.Join(fields, " "))
		}

		n.Attr[i].Val = strings.Join(rewritten, ", ")
		return
	}
}

// ---------------------------------------------------------------------------
// <link> element rewriting.
// ---------------------------------------------------------------------------

// assetRels is the set of <link rel> values that indicate an asset reference.
var assetRels = map[string]bool{
	"stylesheet":                   true,
	"icon":                         true,
	"apple-touch-icon":             true,
	"apple-touch-icon-precomposed": true,
	"mask-icon":                    true,
	"manifest":                     true,
	"preload":                      true,
	"prefetch":                     true,
}

// rewriteLink rewrites the href attribute of a <link> element only if
// its rel attribute indicates an asset (stylesheet, icon, etc.).
func rewriteLink(n *html.Node, base *url.URL, sink RewriteSink) {
	// Check if rel indicates a downloadable resource.
	rel := getAttrCI(n, "rel")
	if rel == "" {
		return
	}

	tokens := strings.Fields(strings.ToLower(rel))
	isAsset := false
	for _, t := range tokens {
		if assetRels[t] {
			isAsset = true
			break
		}
	}
	if !isAsset {
		return
	}

	rewriteAttr(n, base, "href", sink, alwaysAsset)
}

// ---------------------------------------------------------------------------
// CSS rewriting within HTML.
// ---------------------------------------------------------------------------

// rewriteStyleElement rewrites url() references inside a <style> element.
func rewriteStyleElement(n *html.Node, base *url.URL, sink RewriteSink) {
	if n.FirstChild == nil || n.FirstChild.Type != html.TextNode {
		return
	}

	cssText := n.FirstChild.Data
	if cssText == "" {
		return
	}

	rewritten := RewriteCSS([]byte(cssText), base.String(), func(absURL string) string {
		return sink(absURL, KindAsset)
	})

	n.FirstChild.Data = string(rewritten)
}

// rewriteInlineStyle rewrites url() references in an element's style attribute.
func rewriteInlineStyle(n *html.Node, base *url.URL, sink RewriteSink) {
	for i, a := range n.Attr {
		if !strings.EqualFold(a.Key, "style") {
			continue
		}
		if a.Val == "" {
			return
		}

		// Wrap style value as a CSS rule with a dummy selector to use RewriteCSS.
		css := fmt.Sprintf("x{%s}", a.Val)
		rewritten := RewriteCSS([]byte(css), base.String(), func(absURL string) string {
			return sink(absURL, KindAsset)
		})

		// Unwrap: strip the "x{" prefix and "}" suffix.
		result := string(rewritten)
		result = strings.TrimPrefix(result, "x{")
		result = strings.TrimSuffix(result, "}")
		n.Attr[i].Val = strings.TrimSpace(result)
		return
	}
}

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

// shouldSkipRef returns true for URL values that should NOT be rewritten.
func shouldSkipRef(ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.HasPrefix(ref, "#") {
		return true
	}
	lower := strings.ToLower(ref)
	return strings.HasPrefix(lower, "javascript:") ||
		strings.HasPrefix(lower, "mailto:") ||
		strings.HasPrefix(lower, "tel:") ||
		strings.HasPrefix(lower, "data:")
}

// getAttrCI returns an attribute's value with case-insensitive key lookup.
func getAttrCI(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}
