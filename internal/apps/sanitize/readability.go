// Package sanitize provides HTML sanitization functionality.
//
// readability.go: Local implementation of a Readability-like algorithm
// for extracting the main content area from a web page. No external
// services or API calls required — all extraction is in-process.
//
// The algorithm is inspired by Mozilla's Readability.js and works by:
//  1. Removing obviously non-content elements (nav, footer, ads, etc.)
//  2. Scoring block elements based on text density and paragraph count
//  3. Selecting the highest-scoring element as the main content
//  4. Returning the extracted HTML for further Markdown conversion
package sanitize

import (
	"strings"

	"golang.org/x/net/html"
)

// nonContentTags are tags that never contain main content.
var nonContentTags = map[string]bool{
	"script":   true,
	"style":    true,
	"noscript": true,
	"iframe":   true,
	"form":     true,
	"input":    true,
	"textarea": true,
	"select":   true,
	"button":   true,
	"svg":      true,
	"canvas":   true,
	"nav":      true,
	"footer":   true,
	"header":   true,
	"aside":    true,
	"menu":     true,
	"menuitem": true,
}

// nonContentClassPatterns are class/id substrings that indicate
// non-content elements (navigation, ads, sidebars, etc.).
var nonContentClassPatterns = []string{
	"nav", "menu", "sidebar", "footer", "header", "banner",
	"advert", "ads", "ad-", "ad_", "adsense", "adbanner",
	"share", "social", "comment", "popup", "modal", "overlay",
	"breadcrumb", "pagination", "cookie", "subscribe",
	"newsletter", "signup", "login", "search-box",
	"related", "recommend", "trending", "popular-posts",
	"widget", "toolbar", "navbar", "topbar", "bottombar",
}

// positiveClassPatterns indicate likely content areas.
var positiveClassPatterns = []string{
	"article", "content", "main", "post", "entry",
	"story", "body", "text", "page-content",
	"post-content", "entry-content", "article-content",
	"markdown", "prose", "rich-text",
}

// positiveTags are tags likely to contain main content.
var positiveTags = map[string]bool{
	"article": true,
	"main":    true,
	"section": true,
}

// candidateTags are block-level tags that may contain main content.
var candidateTags = map[string]bool{
	"div":     true,
	"section": true,
	"article": true,
	"main":    true,
	"td":      true,
	"li":      true,
	"p":       true,
}

// ExtractReadableContent extracts the main content HTML from a web page
// using a Readability-like scoring algorithm. It returns the HTML of
// the identified main content area, with non-content elements removed.
func ExtractReadableContent(htmlContent string) (string, error) {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return htmlContent, err
	}

	// Step 1: Remove non-content elements from the entire tree.
	removeNonContent(doc)

	// Step 2: Find the <body> element.
	body := findBody(doc)
	if body == nil {
		return htmlContent, nil
	}

	// Step 3: Try to find an <article> or <main> tag first.
	if article := findFirstByPredicate(body, func(n *html.Node) bool {
		return n.Type == html.ElementNode && (n.Data == "article" || n.Data == "main")
	}); article != nil {
		return renderNode(article), nil
	}

	// Step 4: Score candidate elements and pick the best one.
	best := scoreCandidates(body)
	if best != nil {
		return renderNode(best), nil
	}

	// Step 5: Fall back to the full body.
	return renderNode(body), nil
}

// ExtractReadableMarkdown combines readability extraction with Markdown
// conversion. This provides clean, reader-friendly content extraction
// entirely in-process without any external API calls.
func ExtractReadableMarkdown(htmlContent string) (string, error) {
	readableHTML, err := ExtractReadableContent(htmlContent)
	if err != nil {
		// Fall back to the existing extraction.
		return ExtractMainContentMarkdown(htmlContent)
	}

	markdown, err := HTMLToMarkdown(readableHTML)
	if err != nil {
		return ExtractMainContentMarkdown(htmlContent)
	}

	markdown = removeEmptyLines(markdown)
	markdown = collapseMultipleBlankLines(markdown)

	// If the readable markdown is too short, it probably failed to
	// identify the main content. Fall back to full extraction.
	if len(markdown) < 200 {
		return ExtractMainContentMarkdown(htmlContent)
	}

	return markdown, nil
}

// removeNonContent recursively removes elements that are obviously
// not main content (scripts, styles, nav, footer, ads, etc.).
func removeNonContent(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling

		if c.Type == html.ElementNode {
			if nonContentTags[c.Data] {
				n.RemoveChild(c)
				c = next
				continue
			}

			// Check class and id for non-content patterns.
			cls := getAttrVal(c, "class")
			id := getAttrVal(c, "id")
			combined := strings.ToLower(cls + " " + id)
			if matchesPattern(combined, nonContentClassPatterns) &&
				!matchesPattern(combined, positiveClassPatterns) {
				n.RemoveChild(c)
				c = next
				continue
			}
		}

		// Recurse into children.
		removeNonContent(c)
		c = next
	}
}

// scoreCandidates walks the DOM tree and scores block-level elements
// based on text density, paragraph count, and class/id signals.
// Returns the node with the highest score, or nil if no candidates.
func scoreCandidates(root *html.Node) *html.Node {
	type candidate struct {
		node  *html.Node
		score float64
	}

	var candidates []candidate
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && candidateTags[n.Data] {
			score := scoreNode(n)
			if score > 0 {
				candidates = append(candidates, candidate{node: n, score: score})
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)

	if len(candidates) == 0 {
		return nil
	}

	// Find the best candidate.
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.score > best.score {
			best = c
		}
	}
	return best.node
}

// scoreNode calculates a content-likelihood score for a DOM node.
// Higher score = more likely to be main content.
func scoreNode(n *html.Node) float64 {
	text := getText(n)
	textLen := len(strings.TrimSpace(text))
	if textLen < 50 {
		return 0
	}

	// Count paragraphs and links.
	var paraCount, linkCount, linkTextLen int
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "p" {
				paraCount++
			}
			if n.Data == "a" {
				linkCount++
				linkTextLen += len(getText(n))
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)

	// Base score: text length.
	score := float64(textLen) / 100.0

	// Bonus for paragraphs.
	score += float64(paraCount) * 3.0

	// Penalty for high link density.
	linkDensity := 0.0
	if textLen > 0 {
		linkDensity = float64(linkTextLen) / float64(textLen)
	}
	if linkDensity > 0.5 {
		score *= 0.3
	}

	// Bonus for positive tags.
	if positiveTags[n.Data] {
		score += 25.0
	}

	// Bonus for positive class/id.
	cls := strings.ToLower(getAttrVal(n, "class") + " " + getAttrVal(n, "id"))
	if matchesPattern(cls, positiveClassPatterns) {
		score += 15.0
	}

	// Penalty for very short content.
	if textLen < 250 {
		score *= 0.5
	}

	return score
}

// findBody returns the first <body> node in the document.
func findBody(doc *html.Node) *html.Node {
	var find func(*html.Node) *html.Node
	find = func(n *html.Node) *html.Node {
		if n.Type == html.ElementNode && n.Data == "body" {
			return n
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if found := find(c); found != nil {
				return found
			}
		}
		return nil
	}
	return find(doc)
}

// findFirstByPredicate returns the first element matching the predicate.
func findFirstByPredicate(root *html.Node, pred func(*html.Node) bool) *html.Node {
	if pred(root) {
		return root
	}
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if found := findFirstByPredicate(c, pred); found != nil {
			return found
		}
	}
	return nil
}

// getText extracts all text content from a node and its descendants.
func getText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(getText(c))
	}
	return sb.String()
}

// getAttrVal returns the value of an attribute on an element node.
func getAttrVal(n *html.Node, attr string) string {
	if n.Type != html.ElementNode {
		return ""
	}
	for _, a := range n.Attr {
		if a.Key == attr {
			return a.Val
		}
	}
	return ""
}

// matchesPattern checks if any pattern is a substring of s.
func matchesPattern(s string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// renderNode renders a node and its descendants to an HTML string.
func renderNode(n *html.Node) string {
	var sb strings.Builder
	if err := html.Render(&sb, n); err != nil {
		return ""
	}
	return sb.String()
}
