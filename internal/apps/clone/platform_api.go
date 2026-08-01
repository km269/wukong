// Package clone provides website cloning functionality.
//
// platform_api.go: Platform-specific API shortcut paths.
// Inspired by insane-search's Phase 0, this module intercepts URLs
// from known platforms (Reddit, HN, GitHub, Wikipedia, arXiv) and
// fetches content via their public APIs instead of launching a
// headless browser — 10-50× faster and far cheaper.
package clone

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/km269/wukong/pkg/logutil"

	"golang.org/x/net/html"
)

// platformHTTPClient is the shared HTTP client for platform API calls.
var platformHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
}

// TryPlatformAPI checks if the URL matches a known platform with a
// public API. If so, fetches content via the API and returns HTML.
// Returns "", false when no platform matches or the API call fails
// (caller should fall through to browser rendering).
func TryPlatformAPI(ctx context.Context, pageURL string) (string, bool) {
	parsed, err := url.Parse(pageURL)
	if err != nil {
		return "", false
	}
	host := strings.ToLower(parsed.Host)

	for _, p := range platforms {
		if p.match(host, parsed) {
			htmlStr, ok := p.fetch(ctx, pageURL, parsed)
			if ok {
				logutil.Debug("platform API shortcut succeeded",
					"url", pageURL, "platform", p.name)
				return htmlStr, true
			}
			// No match or failure: fall through to next platform or browser.
			return "", false
		}
	}
	return "", false
}

// platformHandler defines a platform API shortcut.
type platformHandler struct {
	name  string
	match func(host string, u *url.URL) bool
	fetch func(ctx context.Context, rawURL string, u *url.URL) (string, bool)
}

// platforms is the ordered list of registered platform handlers.
var platforms = []platformHandler{
	{name: "reddit", match: matchReddit, fetch: fetchReddit},
	{name: "hackernews", match: matchHackerNews, fetch: fetchHackerNews},
	{name: "github", match: matchGitHub, fetch: fetchGitHub},
	{name: "wikipedia", match: matchWikipedia, fetch: fetchWikipedia},
	{name: "arxiv", match: matchArXiv, fetch: fetchArXiv},
}

// httpGet is a helper that performs a GET request and returns the body.
func httpGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, text/html, */*")
	req.Header.Set("User-Agent", "Wukong-Cloner/1.0 (platform-api)")

	resp, err := platformHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// wrapHTML wraps body content in a minimal HTML document.
func wrapHTML(title, bodyHTML string) string {
	return fmt.Sprintf(
		`<!DOCTYPE html><html><head><meta charset="utf-8"><title>%s</title></head><body>%s</body></html>`,
		html.EscapeString(title), bodyHTML,
	)
}

// --- Reddit ---

func matchReddit(host string, _ *url.URL) bool {
	return strings.Contains(host, "reddit.com") ||
		strings.Contains(host, "redd.it")
}

func fetchReddit(ctx context.Context, rawURL string, u *url.URL) (string, bool) {
	// Append .json to the URL for Reddit's JSON API.
	jsonURL := strings.TrimRight(rawURL, "/") + ".json"
	data, err := httpGet(ctx, jsonURL)
	if err != nil {
		return "", false
	}

	// Reddit returns an array of listing objects.
	var listings []struct {
		Data struct {
			Children []struct {
				Data struct {
					Title     string `json:"title"`
					Author    string `json:"author"`
					Selftext  string `json:"selftext"`
					URL       string `json:"url"`
					Score     int    `json:"score"`
					Subreddit string `json:"subreddit"`
					Permalink string `json:"permalink"`
					NumComments int `json:"num_comments"`
				} `json:"data"`
			} `json:"children"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &listings); err != nil || len(listings) == 0 {
		return "", false
	}

	var sb strings.Builder
	for _, listing := range listings {
		for _, child := range listing.Data.Children {
			d := child.Data
			sb.WriteString("<article>")
			sb.WriteString(fmt.Sprintf("<h1>%s</h1>", html.EscapeString(d.Title)))
			sb.WriteString(fmt.Sprintf("<p>Author: u/%s | Score: %d | r/%s | Comments: %d</p>",
				html.EscapeString(d.Author), d.Score,
				html.EscapeString(d.Subreddit), d.NumComments))
			if d.Selftext != "" {
				sb.WriteString(fmt.Sprintf("<div>%s</div>", html.EscapeString(d.Selftext)))
			}
			if d.URL != "" && !strings.Contains(d.URL, "reddit.com") {
				sb.WriteString(fmt.Sprintf(`<p><a href="%s">Link: %s</a></p>`,
					html.EscapeString(d.URL), html.EscapeString(d.URL)))
			}
			sb.WriteString("</article>")
		}
	}
	if sb.Len() == 0 {
		return "", false
	}
	title := "Reddit"
	if len(listings) > 0 && len(listings[0].Data.Children) > 0 {
		title = listings[0].Data.Children[0].Data.Title
	}
	return wrapHTML(title, sb.String()), true
}

// --- Hacker News ---

var hnItemRe = regexp.MustCompile(`item\?id=(\d+)`)

func matchHackerNews(host string, _ *url.URL) bool {
	return strings.Contains(host, "news.ycombinator.com")
}

func fetchHackerNews(ctx context.Context, rawURL string, u *url.URL) (string, bool) {
	m := hnItemRe.FindStringSubmatch(u.RawQuery)
	if m == nil {
		return "", false
	}
	itemID := m[1]
	apiURL := fmt.Sprintf("https://hacker-news.firebaseio.com/v0/item/%s.json", itemID)
	data, err := httpGet(ctx, apiURL)
	if err != nil {
		return "", false
	}

	var item struct {
		ID        int      `json:"id"`
		Title     string   `json:"title"`
		By        string   `json:"by"`
		Text      string   `json:"text"`
		URL       string   `json:"url"`
		Score     int      `json:"score"`
		Descendants int    `json:"descendants"`
		Kids      []int    `json:"kids"`
		Type      string   `json:"type"`
	}
	if err := json.Unmarshal(data, &item); err != nil || item.Title == "" {
		return "", false
	}

	var sb strings.Builder
	sb.WriteString("<article>")
	sb.WriteString(fmt.Sprintf("<h1>%s</h1>", html.EscapeString(item.Title)))
	sb.WriteString(fmt.Sprintf("<p>By: %s | Score: %d | Comments: %d</p>",
		html.EscapeString(item.By), item.Score, item.Descendants))
	if item.Text != "" {
		sb.WriteString(fmt.Sprintf("<div>%s</div>", item.Text))
	}
	if item.URL != "" {
		sb.WriteString(fmt.Sprintf(`<p><a href="%s">Link: %s</a></p>`,
			html.EscapeString(item.URL), html.EscapeString(item.URL)))
	}
	sb.WriteString("</article>")
	return wrapHTML(item.Title, sb.String()), true
}

// --- GitHub ---

func matchGitHub(host string, _ *url.URL) bool {
	return strings.Contains(host, "github.com")
}

func matchGitHubPath(u *url.URL) (owner, repo, pathType, rest string) {
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return "", "", "", ""
	}
	owner, repo = parts[0], parts[1]
	if len(parts) >= 4 {
		pathType = parts[2] // "blob", "tree", "issues", "pull", etc.
		rest = strings.Join(parts[3:], "/")
	}
	return
}

func fetchGitHub(ctx context.Context, rawURL string, u *url.URL) (string, bool) {
	owner, repo, pathType, rest := matchGitHubPath(u)
	if owner == "" || repo == "" {
		return "", false
	}

	// Fetch repo info.
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo)
	data, err := httpGet(ctx, apiURL)
	if err != nil {
		return "", false
	}

	var repoInfo struct {
		Name        string `json:"name"`
		FullName    string `json:"full_name"`
		Description string `json:"description"`
		HTMLURL     string `json:"html_url"`
		Stars       int    `json:"stargazers_count"`
		Forks       int    `json:"forks_count"`
		Language    string `json:"language"`
		Readme      string `json:"default_branch"`
	}
	if err := json.Unmarshal(data, &repoInfo); err != nil {
		return "", false
	}

	var sb strings.Builder
	sb.WriteString("<article>")
	sb.WriteString(fmt.Sprintf("<h1>%s</h1>", html.EscapeString(repoInfo.FullName)))
	sb.WriteString(fmt.Sprintf("<p>%s</p>", html.EscapeString(repoInfo.Description)))
	sb.WriteString(fmt.Sprintf("<p>⭐ %d | 🍴 %d | Language: %s</p>",
		repoInfo.Stars, repoInfo.Forks, html.EscapeString(repoInfo.Language)))

	// If it's a file path, try to fetch raw content.
	if pathType == "blob" && rest != "" {
		rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s",
			owner, repo, rest)
		if raw, err := httpGet(ctx, rawURL); err == nil {
			sb.WriteString(fmt.Sprintf("<pre><code>%s</code></pre>",
				html.EscapeString(string(raw))))
		}
	}

	sb.WriteString(fmt.Sprintf(`<p><a href="%s">View on GitHub</a></p>`,
		html.EscapeString(repoInfo.HTMLURL)))
	sb.WriteString("</article>")
	return wrapHTML(repoInfo.FullName, sb.String()), true
}

// --- Wikipedia ---

func matchWikipedia(host string, _ *url.URL) bool {
	return strings.HasSuffix(host, ".wikipedia.org")
}

func fetchWikipedia(ctx context.Context, rawURL string, u *url.URL) (string, bool) {
	// Extract article title from URL path.
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "wiki" {
		return "", false
	}
	title := parts[1]
	lang := "en"
	if dotIdx := strings.Index(u.Host, "."); dotIdx > 0 {
		lang = u.Host[:dotIdx]
	}

	// Use the REST API for page HTML.
	apiURL := fmt.Sprintf("https://%s.wikipedia.org/api/rest_v1/page/html/%s",
		lang, url.PathEscape(title))
	data, err := httpGet(ctx, apiURL)
	if err != nil {
		return "", false
	}

	htmlStr := string(data)
	if !strings.Contains(htmlStr, "<body") {
		htmlStr = wrapHTML(title, htmlStr)
	}
	return htmlStr, true
}

// --- arXiv ---

var arxivIDRe = regexp.MustCompile(`/(abs|pdf)/([0-9]{4}\.[0-9]{4,5}|[a-z\-]+/[0-9]{7})`)

func matchArXiv(host string, _ *url.URL) bool {
	return strings.Contains(host, "arxiv.org")
}

func fetchArXiv(ctx context.Context, rawURL string, u *url.URL) (string, bool) {
	m := arxivIDRe.FindStringSubmatch(u.Path)
	if m == nil {
		return "", false
	}
	paperID := m[2]

	// Use the arXiv API to get paper metadata.
	apiURL := fmt.Sprintf("http://export.arxiv.org/api/query?id_list=%s", paperID)
	data, err := httpGet(ctx, apiURL)
	if err != nil {
		return "", false
	}

	// The response is Atom XML. We extract the title and summary.
	// Use a simple regex-free approach: parse as HTML and extract text.
	doc, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return "", false
	}

	title, summary := extractArXivContent(doc)
	if title == "" {
		return "", false
	}

	var sb strings.Builder
	sb.WriteString("<article>")
	sb.WriteString(fmt.Sprintf("<h1>%s</h1>", html.EscapeString(title)))
	sb.WriteString(fmt.Sprintf("<p>arXiv: %s</p>", html.EscapeString(paperID)))
	if summary != "" {
		sb.WriteString(fmt.Sprintf("<h2>Abstract</h2><p>%s</p>",
			html.EscapeString(summary)))
	}
	sb.WriteString(fmt.Sprintf(`<p><a href="https://arxiv.org/abs/%s">View on arXiv</a></p>`,
		paperID))
	sb.WriteString("</article>")
	return wrapHTML(title, sb.String()), true
}

// extractArXivContent walks the parsed XML/HTML tree to find the
// first <title> and <summary> element text content.
func extractArXivContent(n *html.Node) (title, summary string) {
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "title":
				if title == "" {
					title = textContent(node)
				}
			case "summary":
				if summary == "" {
					summary = textContent(node)
				}
			}
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return
}

// textContent extracts all text from an HTML node.
func textContent(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(textContent(c))
	}
	return sb.String()
}
