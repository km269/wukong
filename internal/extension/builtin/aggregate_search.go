package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/km269/wukong/internal/apps/sanitize"
	"github.com/km269/wukong/internal/browser"
	"github.com/km269/wukong/internal/cortex"
	"github.com/km269/wukong/internal/util"
	"github.com/km269/wukong/pkg/httpclient"
	htmlpkg "golang.org/x/net/html"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

type aggregateSearchTool struct {
	// searchClient is shared by all search backends and the page
	// fetcher — one connection pool and one rate limiter instead of
	// six. Per-backend timeouts are applied per-request via
	// DoWithTimeout.
	searchClient    *httpclient.Client
	searxngURL      string
	searxngAPIKey   string
	tavilyAPIKey    string
	googleAPIKey    string
	googleCSEID     string
	bingAPIKey      string
	enabledBackends []string
	maxFetchResults int
	browser         *browser.Controller // optional: browser automation for page fetching
	cortexStore     *cortex.CortexStore // optional: internal index search
	userID          string              // user ID for cortex store queries
}

type searchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	Source  string `json:"source"`
}

type fetchResult struct {
	URL      string `json:"url"`
	Title    string `json:"title,omitempty"`
	Markdown string `json:"markdown,omitempty"`
	Error    string `json:"error,omitempty"`
}

func NewAggregateSearchTool(
	enabledBackends []string,
	searxngURL, searxngAPIKey, tavilyAPIKey, googleAPIKey, googleCSEID, bingAPIKey string,
	browserCtrl *browser.Controller,
	cortexStore *cortex.CortexStore,
	userID string,
) (tool.Tool, *aggregateSearchTool) {
	st := &aggregateSearchTool{
		searchClient:    searchHTTPClient(),
		searxngURL:      searxngURL,
		searxngAPIKey:   searxngAPIKey,
		tavilyAPIKey:    tavilyAPIKey,
		googleAPIKey:    googleAPIKey,
		googleCSEID:     googleCSEID,
		bingAPIKey:      bingAPIKey,
		enabledBackends: enabledBackends,
		maxFetchResults: 3,
		browser:         browserCtrl,
		cortexStore:     cortexStore,
		userID:          userID,
	}
	return function.NewFunctionTool(
		st.search,
		function.WithName("web_search"),
		function.WithDescription(
			"Search the web using multiple search engines and aggregate results. "+
				"Returns combined results from DuckDuckGo, SearXNG, Tavily, Google, and Bing. "+
				"Also searches the local knowledge index (CortexStore) for relevant context. "+
				"Fetches and returns full page content (as Markdown) for the top results "+
				"using browser automation with anti-detection when available. "+
				"Use this tool to get comprehensive search coverage across multiple sources.",
		),
	), st
}

// Per-backend request timeouts, applied per-request via DoWithTimeout
// on the shared search client.
const (
	searchTimeoutAPI    = 15 * time.Second // duckduckgo / searxng / google / bing
	searchTimeoutTavily = 20 * time.Second
	searchTimeoutFetch  = 30 * time.Second
)

var (
	searchClientOnce sync.Once
	searchClientVal  *httpclient.Client
)

// searchHTTPClient returns the process-wide shared httpclient for all
// web search backends and the page fetcher: one Transport (one
// connection pool) and one rate limiter (10/s global) instead of one
// per backend. Transient failures and 5xx/429 are retried up to
// MaxRetries times with jittered backoff honoring Retry-After, and the
// utls Chrome fingerprint avoids being blocked by TLS-fingerprinting
// WAFs. The client Timeout is the ceiling (30s, the fetch timeout);
// shorter per-backend budgets are set per-request.
func searchHTTPClient() *httpclient.Client {
	searchClientOnce.Do(func() {
		searchClientVal = httpclient.New(httpclient.Options{
			Timeout:            searchTimeoutFetch,
			MaxRetries:         3,
			RetryDelay:         500 * time.Millisecond,
			EnableRateLimit:    true,
			RateLimitPerSecond: 10,
			RateLimitBurst:     20,
			TLSFingerprint:     true,
		})
	})
	return searchClientVal
}

// SetCortexStore allows late injection of CortexStore after the tool
// has been created. This is needed because CortexStore is initialised
// in bootstrapSession after toolsets are created.
func (a *aggregateSearchTool) SetCortexStore(cs *cortex.CortexStore, userID string) {
	a.cortexStore = cs
	a.userID = userID
}

type aggregateSearchReq struct {
	Query      string `json:"query" jsonschema:"description=Search query to find relevant information"`
	FetchCount int    `json:"fetch_count,omitempty" jsonschema:"description=Number of top results to fetch full content from (0=skip fetch, default=3)"`
}

type aggregateSearchRsp struct {
	Success      bool           `json:"success"`
	Results      []searchResult `json:"results"`
	FetchResults []fetchResult  `json:"fetch_results,omitempty"`
	Error        string         `json:"error,omitempty"`
}

func (a *aggregateSearchTool) search(
	ctx context.Context, req aggregateSearchReq,
) (aggregateSearchRsp, error) {
	if req.Query == "" {
		return aggregateSearchRsp{
			Success: false,
			Error:   "query cannot be empty",
		}, nil
	}

	type resultWrapper struct {
		results []searchResult
		err     error
	}

	// Channel capacity: external backends + 1 for cortex index.
	chanCap := len(a.enabledBackends)
	if a.cortexStore != nil {
		chanCap++
	}

	var wg sync.WaitGroup
	resultChan := make(chan resultWrapper, chanCap)

	for _, backend := range a.enabledBackends {
		wg.Add(1)
		go func(be string) {
			defer wg.Done()
			switch be {
			case "duckduckgo":
				results, err := a.searchDuckDuckGo(ctx, req.Query)
				resultChan <- resultWrapper{results: results, err: err}
			case "searxng":
				results, err := a.searchSearXNG(ctx, req.Query)
				resultChan <- resultWrapper{results: results, err: err}
			case "tavily":
				results, err := a.searchTavily(ctx, req.Query)
				resultChan <- resultWrapper{results: results, err: err}
			case "google":
				results, err := a.searchGoogle(ctx, req.Query)
				resultChan <- resultWrapper{results: results, err: err}
			case "bing":
				results, err := a.searchBing(ctx, req.Query)
				resultChan <- resultWrapper{results: results, err: err}
			}
		}(backend)
	}

	// Search internal CortexStore index concurrently.
	if a.cortexStore != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results, err := a.searchCortexIndex(ctx, req.Query)
			resultChan <- resultWrapper{results: results, err: err}
		}()
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	var allResults []searchResult
	urlSet := make(map[string]bool)

	for r := range resultChan {
		if r.err != nil {
			continue
		}
		for _, res := range r.results {
			if !urlSet[res.URL] && res.URL != "" {
				urlSet[res.URL] = true
				allResults = append(allResults, res)
			}
		}
	}

	if len(allResults) == 0 {
		// Fallback: use browser automation to search directly when all
		// API-based search backends fail or return no results.
		if a.browser != nil {
			if util.DebugEnabled {
				fmt.Println("[wukong/web] all backends returned no results, falling back to browser search")
			}
			browserResults, err := a.searchViaBrowser(ctx, req.Query)
			if err == nil && len(browserResults) > 0 {
				if util.DebugEnabled {
					fmt.Printf("[wukong/web] browser search returned %d results\n", len(browserResults))
				}
				allResults = browserResults
			}
		}
	}

	if len(allResults) == 0 {
		return aggregateSearchRsp{
			Success: false,
			Error:   "no search results found from any engine",
		}, nil
	}

	if len(allResults) > 20 {
		allResults = allResults[:20]
	}

	maxFetch := a.maxFetchResults
	if req.FetchCount > 0 {
		maxFetch = req.FetchCount
	}

	var fetchResults []fetchResult
	if maxFetch > 0 && len(allResults) > 0 {
		fetchCount := maxFetch
		if fetchCount > len(allResults) {
			fetchCount = len(allResults)
		}

		var fetchWg sync.WaitGroup
		fetchResultChan := make(chan fetchResult, fetchCount)

		for i := 0; i < fetchCount; i++ {
			result := allResults[i]
			if result.URL == "" {
				continue
			}
			fetchWg.Add(1)
			go func(url, title string) {
				defer fetchWg.Done()
				fr := a.fetchAndConvertPage(ctx, url)
				if title != "" {
					fr.Title = title
				}
				fetchResultChan <- fr
			}(result.URL, result.Title)
		}

		fetchWg.Wait()
		close(fetchResultChan)

		for fr := range fetchResultChan {
			fetchResults = append(fetchResults, fr)
		}
	}

	return aggregateSearchRsp{
		Success:      true,
		Results:      allResults,
		FetchResults: fetchResults,
	}, nil
}

// searchCortexIndex searches the local CortexStore knowledge index and
// converts results to the unified searchResult format.
func (a *aggregateSearchTool) searchCortexIndex(
	ctx context.Context, query string,
) ([]searchResult, error) {
	results, err := a.cortexStore.Search(ctx, query, a.userID, 5)
	if err != nil {
		return nil, err
	}

	var sr []searchResult
	for _, r := range results {
		preview := r.Preview
		if preview == "" {
			preview = r.Message.Content
		}
		if len(preview) > 300 {
			preview = preview[:300] + "..."
		}
		sr = append(sr, searchResult{
			Title:   fmt.Sprintf("Local: %s", r.Message.Role),
			URL:     fmt.Sprintf("cortex://session/%s/msg/%d", r.Message.SessionID, r.Message.ID),
			Snippet: preview,
			Source:  "cortex",
		})
	}
	return sr, nil
}

func (a *aggregateSearchTool) fetchAndConvertPage(ctx context.Context, pageURL string) fetchResult {
	// Fetch chain: browser → local readability → HTTP
	// 1. Browser automation (JS rendering, anti-detection, stealth).
	if a.browser != nil {
		fr := a.fetchWithBrowser(ctx, pageURL)
		if fr.Markdown != "" || fr.Error == "" {
			return fr
		}
	}
	// 2. Local Reader (HTTP fetch + Readability extraction + Markdown).
	fr := a.fetchWithLocalReader(ctx, pageURL)
	if fr.Markdown != "" {
		return fr
	}
	// 3. Simple HTTP GET fallback.
	return a.fetchWithHTTP(ctx, pageURL)
}

// fetchWithBrowser uses the browser Controller to fetch a page with
// full JavaScript rendering and anti-detection support.
func (a *aggregateSearchTool) fetchWithBrowser(ctx context.Context, pageURL string) fetchResult {
	result, err := a.browser.ExtractText(ctx, pageURL)
	if err != nil {
		return fetchResult{URL: pageURL, Error: fmt.Sprintf("browser fetch: %v", err)}
	}
	if !result.Success {
		return fetchResult{URL: pageURL, Error: result.Error}
	}

	markdown := result.Markdown
	if markdown == "" {
		markdown = result.Text
	}

	const maxMarkdownLen = 50000
	if len(markdown) > maxMarkdownLen {
		markdown = markdown[:maxMarkdownLen] +
			fmt.Sprintf("\n\n... (truncated, %d chars total)", len(markdown))
	}

	title := ""
	if extracted := extractTitleFromHTML(result.HTML); extracted != "" {
		title = extracted
	}

	return fetchResult{
		URL:      pageURL,
		Title:    title,
		Markdown: markdown,
	}
}

// fetchWithLocalReader fetches a page via HTTP and extracts the main
// content using a local Readability algorithm, then converts to Markdown.
// No external API calls — all extraction happens in-process.
func (a *aggregateSearchTool) fetchWithLocalReader(ctx context.Context, pageURL string) fetchResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return fetchResult{URL: pageURL, Error: fmt.Sprintf("local reader request: %v", err)}
	}

	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) "+
			"AppleWebKit/537.36 (KHTML, like Gecko) "+
			"Chrome/120.0.0.0 Safari/537.36 Wukong-Agent/1.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,zh-CN;q=0.8")

	resp, err := a.searchClient.DoWithTimeout(req, searchTimeoutFetch)
	if err != nil {
		return fetchResult{URL: pageURL, Error: fmt.Sprintf("local reader fetch: %v", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fetchResult{URL: pageURL, Error: fmt.Sprintf("local reader HTTP %d", resp.StatusCode)}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return fetchResult{URL: pageURL, Error: fmt.Sprintf("local reader read: %v", err)}
	}

	htmlContent := string(body)
	title := extractTitleFromHTML(htmlContent)

	// Use local Readability algorithm to extract main content + Markdown.
	markdown, err := sanitize.ExtractReadableMarkdown(htmlContent)
	if err != nil || len(markdown) < 50 {
		return fetchResult{URL: pageURL, Error: "local reader: extraction failed"}
	}

	const maxMarkdownLen = 50000
	if len(markdown) > maxMarkdownLen {
		markdown = markdown[:maxMarkdownLen] +
			fmt.Sprintf("\n\n... (truncated, %d chars total)", len(markdown))
	}

	return fetchResult{
		URL:      pageURL,
		Title:    title,
		Markdown: markdown,
	}
}

// fetchWithHTTP fetches a page via simple HTTP GET and converts to Markdown.
func (a *aggregateSearchTool) fetchWithHTTP(ctx context.Context, pageURL string) fetchResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return fetchResult{URL: pageURL, Error: fmt.Sprintf("create request: %v", err)}
	}

	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) "+
			"AppleWebKit/537.36 (KHTML, like Gecko) "+
			"Chrome/120.0.0.0 Safari/537.36 Wukong-Agent/1.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := a.searchClient.DoWithTimeout(req, searchTimeoutFetch)
	if err != nil {
		return fetchResult{URL: pageURL, Error: fmt.Sprintf("fetch failed: %v", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fetchResult{
			URL:   pageURL,
			Error: fmt.Sprintf("HTTP %d", resp.StatusCode),
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return fetchResult{URL: pageURL, Error: fmt.Sprintf("read body: %v", err)}
	}

	htmlContent := string(body)
	title := extractTitleFromHTML(htmlContent)

	markdown, err := sanitize.ExtractMainContentMarkdown(htmlContent)
	if err != nil {
		return fetchResult{
			URL:   pageURL,
			Title: title,
			Error: fmt.Sprintf("convert to markdown: %v", err),
		}
	}

	const maxMarkdownLen = 50000
	if len(markdown) > maxMarkdownLen {
		markdown = markdown[:maxMarkdownLen] +
			fmt.Sprintf("\n\n... (truncated, %d chars total)", len(markdown))
	}

	return fetchResult{
		URL:      pageURL,
		Title:    title,
		Markdown: markdown,
	}
}

func extractTitleFromHTML(html string) string {
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

// searchViaBrowser uses browser automation to navigate to search engine
// pages and scrape results. Used as a fallback when all API-based search
// backends fail or return no results.
func (a *aggregateSearchTool) searchViaBrowser(
	ctx context.Context, query string,
) ([]searchResult, error) {
	// Define search engines to try in order.
	// Bing and Baidu are accessible in China; Google and DuckDuckGo may need proxy.
	type engineDef struct {
		name     string
		url      string
		parseFn  func(string) []searchResult
		priority int
	}

	engines := []engineDef{
		{
			name:     "Bing",
			url:      fmt.Sprintf("https://www.bing.com/search?q=%s&count=10", url.QueryEscape(query)),
			parseFn:  parseBingSearchResults,
			priority: 1,
		},
		{
			name:     "Baidu",
			url:      fmt.Sprintf("https://www.baidu.com/s?wd=%s&rn=10", url.QueryEscape(query)),
			parseFn:  parseBaiduSearchResults,
			priority: 2,
		},
		{
			name:     "WeChat",
			url:      fmt.Sprintf("https://weixin.sogou.com/weixin?type=2&query=%s", url.QueryEscape(query)),
			parseFn:  parseSogouWeChatResults,
			priority: 3,
		},
		{
			name:     "Zhihu",
			url:      fmt.Sprintf("https://www.zhihu.com/search?type=content&q=%s", url.QueryEscape(query)),
			parseFn:  parseZhihuSearchResults,
			priority: 4,
		},
		{
			name:     "DuckDuckGo",
			url:      fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query)),
			parseFn:  parseDuckDuckGoHTMLResults,
			priority: 5,
		},
		{
			name:     "Google",
			url:      fmt.Sprintf("https://www.google.com/search?q=%s&num=10", url.QueryEscape(query)),
			parseFn:  parseGoogleSearchResults,
			priority: 6,
		},
	}

	var allResults []searchResult
	seen := make(map[string]bool)

	for _, eng := range engines {
		result, err := a.browser.Navigate(ctx, eng.url)
		if err != nil || !result.Success || result.Content == "" {
			if util.DebugEnabled {
				fmt.Printf("[wukong/web] browser search (%s): failed\n", eng.name)
			}
			continue
		}

		results := eng.parseFn(result.Content)
		if util.DebugEnabled {
			fmt.Printf("[wukong/web] browser search (%s): %d results\n", eng.name, len(results))
		}

		for _, r := range results {
			if !seen[r.URL] && r.URL != "" {
				seen[r.URL] = true
				allResults = append(allResults, r)
			}
		}

		// If we have enough results from the first engine, return early.
		if len(allResults) >= 5 {
			break
		}
	}

	if len(allResults) == 0 {
		return nil, fmt.Errorf("browser search returned no results from any engine")
	}
	return allResults, nil
}

// parseSogouWeChatResults extracts WeChat article results from Sogou WeChat search.
// Sogou WeChat search URL: https://weixin.sogou.com/weixin?type=2&query=...
// Results are in <div class="txt-box"> with <h3><a href="...">title</a></h3>
// and <p class="txt-info">snippet</p>.
func parseSogouWeChatResults(htmlContent string) []searchResult {
	doc, err := htmlpkg.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return nil
	}

	var results []searchResult
	var walk func(*htmlpkg.Node)
	walk = func(n *htmlpkg.Node) {
		if n.Type == htmlpkg.ElementNode && n.Data == "div" {
			cls := getAttr(n, "class")
			if strings.Contains(cls, "txt-box") {
				result := searchResult{Source: "wechat-browser"}

				// Find title link.
				var findTitle func(*htmlpkg.Node)
				findTitle = func(n *htmlpkg.Node) {
					if result.URL != "" {
						return
					}
					if n.Type == htmlpkg.ElementNode && n.Data == "a" {
						href := getAttr(n, "href")
						if href != "" {
							if strings.HasPrefix(href, "http") {
								result.URL = href
							} else if strings.HasPrefix(href, "/") {
								result.URL = "https://weixin.sogou.com" + href
							}
							result.Title = strings.TrimSpace(getTextContent(n))
						}
					}
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						findTitle(c)
						if result.URL != "" {
							return
						}
					}
				}

				// Find snippet.
				var findSnippet func(*htmlpkg.Node)
				findSnippet = func(n *htmlpkg.Node) {
					if result.Snippet != "" {
						return
					}
					if n.Type == htmlpkg.ElementNode &&
						(n.Data == "p" || n.Data == "div") {
						cls := getAttr(n, "class")
						if strings.Contains(cls, "txt-info") {
							text := strings.TrimSpace(getTextContent(n))
							if len(text) > 20 {
								result.Snippet = text
								return
							}
						}
					}
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						findSnippet(c)
						if result.Snippet != "" {
							return
						}
					}
				}

				findTitle(n)
				findSnippet(n)
				if result.URL != "" {
					if len(result.Snippet) > 300 {
						result.Snippet = result.Snippet[:300] + "..."
					}
					results = append(results, result)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return results
}

// parseZhihuSearchResults extracts results from Zhihu search.
// Zhihu search results are in <div class="SearchResult-Card"> with
// <a class="ContentLink"> containing the title and URL.
func parseZhihuSearchResults(htmlContent string) []searchResult {
	doc, err := htmlpkg.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return nil
	}

	var results []searchResult
	seen := make(map[string]bool)
	var walk func(*htmlpkg.Node)
	walk = func(n *htmlpkg.Node) {
		if n.Type == htmlpkg.ElementNode && n.Data == "a" {
			href := getAttr(n, "href")
			// Zhihu internal links start with /question/, /p/, /pin/
			if strings.HasPrefix(href, "/question/") ||
				strings.HasPrefix(href, "/p/") ||
				strings.HasPrefix(href, "https://www.zhihu.com/question/") {
				if strings.HasPrefix(href, "/") {
					href = "https://www.zhihu.com" + href
				}
				// Extract question ID for clean URL.
				if strings.Contains(href, "/answer/") {
					parts := strings.Split(href, "/answer/")
					href = parts[0]
				}
				if !seen[href] {
					title := strings.TrimSpace(getTextContent(n))
					if title != "" && len(title) > 5 {
						seen[href] = true
						results = append(results, searchResult{
							Title:  title,
							URL:    href,
							Source: "zhihu-browser",
						})
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return results
}

// parseBingSearchResults extracts search results from a Bing search results page.
func parseBingSearchResults(htmlContent string) []searchResult {
	doc, err := htmlpkg.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return nil
	}

	var results []searchResult
	var walk func(*htmlpkg.Node)
	walk = func(n *htmlpkg.Node) {
		if n.Type == htmlpkg.ElementNode && n.Data == "li" {
			if hasAttr(n, "class", "b_algo") {
				if r := extractBingResult(n); r.URL != "" {
					results = append(results, r)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return results
}

// extractBingResult parses a single Bing search result <li class="b_algo">.
func extractBingResult(li *htmlpkg.Node) searchResult {
	var result searchResult
	result.Source = "bing-browser"

	// Find <h2><a href="URL">Title</a></h2>
	var findLink func(*htmlpkg.Node)
	findLink = func(n *htmlpkg.Node) {
		if n.Type == htmlpkg.ElementNode && n.Data == "a" {
			if href := getAttr(n, "href"); href != "" && strings.HasPrefix(href, "http") {
				if result.URL == "" {
					result.URL = href
					result.Title = strings.TrimSpace(getTextContent(n))
				}
			}
		}
		if result.URL == "" {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				findLink(c)
			}
		}
	}

	// Find snippet text in <p> or <div class="b_caption">
	var findSnippet func(*htmlpkg.Node)
	findSnippet = func(n *htmlpkg.Node) {
		if result.Snippet != "" {
			return
		}
		if n.Type == htmlpkg.ElementNode && (n.Data == "p" || n.Data == "div") {
			cls := getAttr(n, "class")
			if strings.Contains(cls, "b_caption") ||
				strings.Contains(cls, "b_lineclamp") ||
				strings.Contains(cls, "b_algoSlug") {
				text := strings.TrimSpace(getTextContent(n))
				if len(text) > 20 && !strings.Contains(text, "·") {
					result.Snippet = text
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findSnippet(c)
			if result.Snippet != "" {
				return
			}
		}
	}

	findLink(li)
	findSnippet(li)

	if len(result.Snippet) > 300 {
		result.Snippet = result.Snippet[:300] + "..."
	}
	return result
}

// parseGoogleSearchResults extracts search results from a Google search results page.
func parseGoogleSearchResults(htmlContent string) []searchResult {
	doc, err := htmlpkg.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return nil
	}

	var results []searchResult
	seen := make(map[string]bool)
	var walk func(*htmlpkg.Node)
	walk = func(n *htmlpkg.Node) {
		if n.Type == htmlpkg.ElementNode && n.Data == "a" {
			href := getAttr(n, "href")
			if strings.HasPrefix(href, "/url?q=") {
				href = extractGoogleRedirectURL(href)
			}
			if strings.HasPrefix(href, "http") && !seen[href] {
				// Check if this link contains an <h3> (Google result title).
				if h3 := findFirstByTag(n, "h3"); h3 != nil {
					title := strings.TrimSpace(getTextContent(h3))
					if title != "" && !isGoogleNavLink(href) {
						seen[href] = true
						results = append(results, searchResult{
							Title:   title,
							URL:     href,
							Snippet: "", // Google snippets are hard to extract reliably
							Source:  "google-browser",
						})
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return results
}

// extractGoogleRedirectURL extracts the actual URL from a Google redirect
// URL like /url?q=https://example.com&sa=U&ved=...
func extractGoogleRedirectURL(redirectURL string) string {
	u, err := url.Parse(redirectURL)
	if err != nil {
		return ""
	}
	q := u.Query()
	if q.Get("q") != "" {
		return q.Get("q")
	}
	return ""
}

// isGoogleNavLink returns true for Google navigation links that aren't
// actual search results (e.g., Google account, settings links).
func isGoogleNavLink(href string) bool {
	for _, prefix := range []string{
		"https://www.google.com/preferences",
		"https://accounts.google.com",
		"https://support.google.com",
		"https://policies.google.com",
		"https://www.google.com/intl",
	} {
		if strings.HasPrefix(href, prefix) {
			return true
		}
	}
	return false
}

// parseBaiduSearchResults extracts search results from a Baidu search page.
// Baidu result structure: <div class="result"> containing <h3><a href="...">title</a></h3>
// and <span class="content-right_8Zs40">snippet</span>.
func parseBaiduSearchResults(htmlContent string) []searchResult {
	doc, err := htmlpkg.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return nil
	}

	var results []searchResult
	var walk func(*htmlpkg.Node)
	walk = func(n *htmlpkg.Node) {
		if n.Type == htmlpkg.ElementNode && n.Data == "div" {
			cls := getAttr(n, "class")
			// Baidu result containers: "result", "result-op", "c-container"
			if strings.Contains(cls, "result") || strings.Contains(cls, "c-container") {
				if r := extractBaiduResult(n); r.URL != "" {
					results = append(results, r)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return results
}

// extractBaiduResult parses a single Baidu search result container.
func extractBaiduResult(container *htmlpkg.Node) searchResult {
	var result searchResult
	result.Source = "baidu-browser"

	// Find the first <a> with an href that looks like a real URL.
	// Baidu wraps result URLs in its own redirect (e.g., http://www.baidu.com/link?url=...).
	var findLink func(*htmlpkg.Node)
	findLink = func(n *htmlpkg.Node) {
		if result.URL != "" {
			return
		}
		if n.Type == htmlpkg.ElementNode && n.Data == "a" {
			href := getAttr(n, "href")
			if href != "" && strings.HasPrefix(href, "http") {
				result.URL = href
				result.Title = strings.TrimSpace(getTextContent(n))
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findLink(c)
			if result.URL != "" {
				return
			}
		}
	}

	// Find snippet in various Baidu class patterns.
	var findSnippet func(*htmlpkg.Node)
	findSnippet = func(n *htmlpkg.Node) {
		if result.Snippet != "" {
			return
		}
		if n.Type == htmlpkg.ElementNode && (n.Data == "span" || n.Data == "div") {
			cls := getAttr(n, "class")
			if strings.Contains(cls, "content-right") ||
				strings.Contains(cls, "c-abstract") ||
				strings.Contains(cls, "c-span-last") {
				text := strings.TrimSpace(getTextContent(n))
				if len(text) > 20 {
					result.Snippet = text
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findSnippet(c)
			if result.Snippet != "" {
				return
			}
		}
	}

	findLink(container)
	findSnippet(container)

	if len(result.Snippet) > 300 {
		result.Snippet = result.Snippet[:300] + "..."
	}
	return result
}

// parseDuckDuckGoHTMLResults extracts results from DuckDuckGo HTML search page.
// DuckDuckGo HTML version uses: <div class="result"> with <a class="result__a" href="...">
// and <a class="result__snippet" href="...">.
func parseDuckDuckGoHTMLResults(htmlContent string) []searchResult {
	doc, err := htmlpkg.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return nil
	}

	var results []searchResult
	var walk func(*htmlpkg.Node)
	walk = func(n *htmlpkg.Node) {
		if n.Type == htmlpkg.ElementNode && n.Data == "a" {
			cls := getAttr(n, "class")
			if strings.Contains(cls, "result__a") {
				href := getAttr(n, "href")
				// DuckDuckGo wraps URLs: //duckduckgo.com/l/?uddg=ENCODED_URL
				if strings.Contains(href, "uddg=") {
					if u, err := url.Parse(href); err == nil {
						if q := u.Query().Get("uddg"); q != "" {
							href = q
						}
					}
				}
				if strings.HasPrefix(href, "http") {
					results = append(results, searchResult{
						Title:  strings.TrimSpace(getTextContent(n)),
						URL:    href,
						Source: "duckduckgo-browser",
					})
				}
			}
			if strings.Contains(cls, "result__snippet") {
				if len(results) > 0 && results[len(results)-1].Snippet == "" {
					results[len(results)-1].Snippet =
						strings.TrimSpace(getTextContent(n))
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	for i := range results {
		if len(results[i].Snippet) > 300 {
			results[i].Snippet = results[i].Snippet[:300] + "..."
		}
	}
	return results
}

// hasAttr checks if an element node has a specific attribute value
// containing the given substring (space-separated class matching).
func hasAttr(n *htmlpkg.Node, attr, contains string) bool {
	if n.Type != htmlpkg.ElementNode {
		return false
	}
	for _, a := range n.Attr {
		if a.Key == attr {
			for _, c := range strings.Fields(a.Val) {
				if c == contains {
					return true
				}
			}
		}
	}
	return false
}

// getAttr returns the value of an attribute on an element node.
func getAttr(n *htmlpkg.Node, attr string) string {
	if n.Type != htmlpkg.ElementNode {
		return ""
	}
	for _, a := range n.Attr {
		if a.Key == attr {
			return a.Val
		}
	}
	return ""
}

// getTextContent extracts all text content from a node and its descendants.
func getTextContent(n *htmlpkg.Node) string {
	if n.Type == htmlpkg.TextNode {
		return n.Data
	}
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(getTextContent(c))
	}
	return sb.String()
}

// findFirstByTag finds the first descendant element with the given tag name.
func findFirstByTag(n *htmlpkg.Node, tag string) *htmlpkg.Node {
	if n.Type == htmlpkg.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findFirstByTag(c, tag); found != nil {
			return found
		}
	}
	return nil
}

func (a *aggregateSearchTool) searchDuckDuckGo(ctx context.Context, query string) ([]searchResult, error) {
	searchURL := "https://api.duckduckgo.com/?q=" + url.QueryEscape(query) + "&format=json&no_redirect=1&no_html=1"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := a.searchClient.DoWithTimeout(httpReq, searchTimeoutAPI)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return parseDDGResults(result), nil
}

func (a *aggregateSearchTool) searchSearXNG(ctx context.Context, query string) ([]searchResult, error) {
	urlStr := a.searxngURL
	if !strings.HasSuffix(urlStr, "/") {
		urlStr += "/"
	}
	searchURL := urlStr + "search?q=" + url.QueryEscape(query) + "&format=json&language=zh"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("User-Agent", "Wukong/2.0")
	if a.searxngAPIKey != "" {
		httpReq.Header.Set("X-Searxng-API-Key", a.searxngAPIKey)
	}

	resp, err := a.searchClient.DoWithTimeout(httpReq, searchTimeoutAPI)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return parseSearXNGResults(result), nil
}

func (a *aggregateSearchTool) searchTavily(ctx context.Context, query string) ([]searchResult, error) {
	if a.tavilyAPIKey == "" {
		return nil, fmt.Errorf("tavily API key not configured")
	}

	searchURL := "https://api.tavily.com/search"

	body, err := json.Marshal(map[string]interface{}{
		"query":               query,
		"api_key":             a.tavilyAPIKey,
		"search_depth":        "advanced",
		"max_results":         10,
		"include_answer":      true,
		"include_raw_content": false,
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, searchURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.searchClient.DoWithTimeout(httpReq, searchTimeoutTavily)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return parseTavilyResults(result), nil
}

func parseDDGResults(data map[string]interface{}) []searchResult {
	var results []searchResult

	if answer, ok := data["AbstractText"]; ok {
		if answerStr, ok := answer.(string); ok && answerStr != "" {
			results = append(results, searchResult{
				Title:   "AI Answer",
				URL:     "",
				Snippet: answerStr,
				Source:  "duckduckgo",
			})
		}
	}

	if related, ok := data["RelatedTopics"]; ok {
		if rs, ok := related.([]interface{}); ok {
			for _, r := range rs {
				if item, ok := r.(map[string]interface{}); ok {
					text := getString(item, "Text")
					url := getString(item, "FirstURL")
					if url != "" && text != "" {
						results = append(results, searchResult{
							Title:   text,
							URL:     url,
							Snippet: text,
							Source:  "duckduckgo",
						})
					}
				}
			}
		}
	}

	return results
}

func parseSearXNGResults(data map[string]interface{}) []searchResult {
	var results []searchResult

	if resultsList, ok := data["results"]; ok {
		if rs, ok := resultsList.([]interface{}); ok {
			for _, r := range rs {
				if item, ok := r.(map[string]interface{}); ok {
					title := getString(item, "title")
					url := getString(item, "url")
					content := getString(item, "content")
					if url != "" && title != "" {
						results = append(results, searchResult{
							Title:   title,
							URL:     url,
							Snippet: content,
							Source:  "searxng",
						})
					}
				}
			}
		}
	}

	return results
}

func parseTavilyResults(data map[string]interface{}) []searchResult {
	var results []searchResult

	if answer, ok := data["answer"]; ok {
		if answerStr, ok := answer.(string); ok && answerStr != "" {
			results = append(results, searchResult{
				Title:   "AI Answer",
				URL:     "",
				Snippet: answerStr,
				Source:  "tavily",
			})
		}
	}

	if resultsList, ok := data["results"]; ok {
		if rs, ok := resultsList.([]interface{}); ok {
			for _, r := range rs {
				if item, ok := r.(map[string]interface{}); ok {
					title := getString(item, "title")
					url := getString(item, "url")
					content := getString(item, "content")
					if content == "" {
						content = getString(item, "summary")
					}
					if len(content) > 300 {
						content = content[:300] + "..."
					}
					if url != "" && title != "" {
						results = append(results, searchResult{
							Title:   title,
							URL:     url,
							Snippet: content,
							Source:  "tavily",
						})
					}
				}
			}
		}
	}

	return results
}

func (a *aggregateSearchTool) searchGoogle(ctx context.Context, query string) ([]searchResult, error) {
	if a.googleAPIKey == "" || a.googleCSEID == "" {
		return nil, fmt.Errorf("google API key or CSE ID not configured")
	}

	searchURL := fmt.Sprintf(
		"https://www.googleapis.com/customsearch/v1?key=%s&cx=%s&q=%s&num=10",
		url.QueryEscape(a.googleAPIKey),
		url.QueryEscape(a.googleCSEID),
		url.QueryEscape(query),
	)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := a.searchClient.DoWithTimeout(httpReq, searchTimeoutAPI)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return parseGoogleResults(result), nil
}

func (a *aggregateSearchTool) searchBing(ctx context.Context, query string) ([]searchResult, error) {
	if a.bingAPIKey == "" {
		return nil, fmt.Errorf("bing API key not configured")
	}

	searchURL := fmt.Sprintf(
		"https://api.bing.microsoft.com/v7.0/search?q=%s&count=10",
		url.QueryEscape(query),
	)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Ocp-Apim-Subscription-Key", a.bingAPIKey)

	resp, err := a.searchClient.DoWithTimeout(httpReq, searchTimeoutAPI)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return parseBingResults(result), nil
}

func parseGoogleResults(data map[string]interface{}) []searchResult {
	var results []searchResult

	if items, ok := data["items"]; ok {
		if rs, ok := items.([]interface{}); ok {
			for _, r := range rs {
				if item, ok := r.(map[string]interface{}); ok {
					title := getString(item, "title")
					url := getString(item, "link")
					snippet := getString(item, "snippet")
					if url != "" && title != "" {
						results = append(results, searchResult{
							Title:   title,
							URL:     url,
							Snippet: snippet,
							Source:  "google",
						})
					}
				}
			}
		}
	}

	return results
}

func parseBingResults(data map[string]interface{}) []searchResult {
	var results []searchResult

	if webPages, ok := data["webPages"]; ok {
		if wp, ok := webPages.(map[string]interface{}); ok {
			if items, ok := wp["value"]; ok {
				if rs, ok := items.([]interface{}); ok {
					for _, r := range rs {
						if item, ok := r.(map[string]interface{}); ok {
							title := getString(item, "name")
							url := getString(item, "url")
							snippet := getString(item, "snippet")
							if url != "" && title != "" {
								results = append(results, searchResult{
									Title:   title,
									URL:     url,
									Snippet: snippet,
									Source:  "bing",
								})
							}
						}
					}
				}
			}
		}
	}

	return results
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
