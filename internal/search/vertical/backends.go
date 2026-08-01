package vertical

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// sharedHTTPTimeout is the default per-request timeout for
// platform search API calls. The Router's overall deadline
// (cfg.Timeout) wraps this.
const sharedHTTPTimeout = 8 * time.Second

// httpFetch performs a GET request and returns the body bytes.
func httpFetch(ctx context.Context, rawURL string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, text/html, */*")
	req.Header.Set("User-Agent", "Wukong-VerticalSearch/1.0")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: sharedHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4 MB cap
}

// truncate keeps s to at most n runes.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// --- arXiv ---

type arXivBackend struct{}

func (b *arXivBackend) Name() string { return "arxiv" }

func (b *arXivBackend) Search(
	ctx context.Context, query string, limit int,
) ([]Result, error) {
	if limit <= 0 {
		limit = 5
	}
	q := SanitizeQuery(query)
	apiURL := fmt.Sprintf(
		"http://export.arxiv.org/api/query?search_query=all:%s&start=0&max_results=%d",
		url.QueryEscape(q), limit,
	)
	data, err := httpFetch(ctx, apiURL, nil)
	if err != nil {
		return nil, err
	}
	// arXiv returns Atom XML; parse via html.Parse for tag extraction.
	doc, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("arxiv parse: %w", err)
	}
	entries := extractArXivEntries(doc)
	results := make([]Result, 0, len(entries))
	for i, e := range entries {
		score := 1.0 / float64(i+1) // rank-based score
		results = append(results, Result{
			Title:   e.title,
			URL:     e.id,
			Preview: truncate(e.summary, 300),
			Score:   score,
			Source:  "arxiv",
		})
	}
	return results, nil
}

type arxivEntry struct {
	title, id, summary string
}

func extractArXivEntries(doc *html.Node) []arxivEntry {
	var entries []arxivEntry
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "entry" {
			e := arxivEntry{}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type != html.ElementNode {
					continue
				}
				switch c.Data {
				case "title":
					e.title = strings.TrimSpace(textContent(c))
				case "id":
					e.id = strings.TrimSpace(textContent(c))
				case "summary":
					e.summary = strings.TrimSpace(textContent(c))
				}
			}
			if e.title != "" {
				entries = append(entries, e)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return entries
}

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

// --- GitHub ---

type githubBackend struct {
	apiKey string
}

func (b *githubBackend) Name() string { return "github" }

func (b *githubBackend) Search(
	ctx context.Context, query string, limit int,
) ([]Result, error) {
	if limit <= 0 {
		limit = 5
	}
	q := SanitizeQuery(query)
	apiURL := fmt.Sprintf(
		"https://api.github.com/search/repositories?q=%s&sort=stars&order=desc&per_page=%d",
		url.QueryEscape(q), limit,
	)
	headers := map[string]string{"Accept": "application/vnd.github+json"}
	if b.apiKey != "" {
		headers["Authorization"] = "Bearer " + b.apiKey
	}
	data, err := httpFetch(ctx, apiURL, headers)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Items []struct {
			FullName    string `json:"full_name"`
			HTMLURL     string `json:"html_url"`
			Description string `json:"description"`
			Stars       int    `json:"stargazers_count"`
			Language    string `json:"language"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("github parse: %w", err)
	}
	results := make([]Result, 0, len(resp.Items))
	for i, item := range resp.Items {
		preview := item.Description
		if item.Language != "" {
			preview = fmt.Sprintf("[%s] %s", item.Language, preview)
		}
		results = append(results, Result{
			Title:   item.FullName,
			URL:     item.HTMLURL,
			Preview: truncate(preview, 300),
			Score:   1.0 / float64(i+1),
			Source:  "github",
		})
	}
	return results, nil
}

// --- Wikipedia ---

type wikipediaBackend struct{}

func (b *wikipediaBackend) Name() string { return "wikipedia" }

func (b *wikipediaBackend) Search(
	ctx context.Context, query string, limit int,
) ([]Result, error) {
	if limit <= 0 {
		limit = 5
	}
	q := SanitizeQuery(query)
	// Use the OpenSearch API: returns [title, summary, URL].
	apiURL := fmt.Sprintf(
		"https://en.wikipedia.org/w/api.php?action=opensearch&search=%s&limit=%d&namespace=0&format=json&origin=*",
		url.QueryEscape(q), limit,
	)
	data, err := httpFetch(ctx, apiURL, nil)
	if err != nil {
		return nil, err
	}
	// OpenSearch returns an array: [query, [titles], [summaries], [urls]].
	var resp []json.RawMessage
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("wikipedia parse: %w", err)
	}
	if len(resp) < 4 {
		return nil, nil
	}
	var titles, summaries, urls []string
	if err := json.Unmarshal(resp[1], &titles); err != nil {
		return nil, fmt.Errorf("wikipedia titles: %w", err)
	}
	if err := json.Unmarshal(resp[2], &summaries); err != nil {
		return nil, fmt.Errorf("wikipedia summaries: %w", err)
	}
	if err := json.Unmarshal(resp[3], &urls); err != nil {
		return nil, fmt.Errorf("wikipedia urls: %w", err)
	}
	results := make([]Result, 0, len(titles))
	for i := range titles {
		if i >= len(summaries) || i >= len(urls) {
			break
		}
		results = append(results, Result{
			Title:   titles[i],
			URL:     urls[i],
			Preview: truncate(summaries[i], 300),
			Score:   1.0 / float64(i+1),
			Source:  "wikipedia",
		})
	}
	return results, nil
}

// --- Reddit ---

type redditBackend struct{}

func (b *redditBackend) Name() string { return "reddit" }

func (b *redditBackend) Search(
	ctx context.Context, query string, limit int,
) ([]Result, error) {
	if limit <= 0 {
		limit = 5
	}
	q := SanitizeQuery(query)
	// Reddit's search endpoint returns JSON when appended .json.
	apiURL := fmt.Sprintf(
		"https://www.reddit.com/search.json?q=%s&limit=%d&sort=relevance",
		url.QueryEscape(q), limit,
	)
	data, err := httpFetch(ctx, apiURL, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data struct {
			Children []struct {
				Data struct {
					Title     string  `json:"title"`
					Permalink string  `json:"permalink"`
					Selftext  string  `json:"selftext"`
					Score     int     `json:"score"`
					Subreddit string  `json:"subreddit"`
					URL       string  `json:"url"`
					Upvote    float64 `json:"upvote_ratio"`
				} `json:"data"`
			} `json:"children"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("reddit parse: %w", err)
	}
	results := make([]Result, 0, len(resp.Data.Children))
	for i, child := range resp.Data.Children {
		d := child.Data
		preview := d.Selftext
		if preview == "" {
			preview = fmt.Sprintf("r/%s | score: %d", d.Subreddit, d.Score)
		}
		urlStr := "https://www.reddit.com" + d.Permalink
		results = append(results, Result{
			Title:   d.Title,
			URL:     urlStr,
			Preview: truncate(preview, 300),
			Score:   1.0 / float64(i+1),
			Source:  "reddit",
		})
	}
	return results, nil
}
