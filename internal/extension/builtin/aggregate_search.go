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
	"github.com/km269/wukong/pkg/httpclient"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

type aggregateSearchTool struct {
	duckduckgoClient *httpclient.Client
	searxngClient    *httpclient.Client
	tavilyClient     *httpclient.Client
	googleClient     *httpclient.Client
	bingClient       *httpclient.Client
	fetchClient      *httpclient.Client
	searxngURL       string
	searxngAPIKey    string
	tavilyAPIKey     string
	googleAPIKey     string
	googleCSEID      string
	bingAPIKey       string
	enabledBackends  []string
	maxFetchResults  int
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

func NewAggregateSearchTool(enabledBackends []string, searxngURL, searxngAPIKey, tavilyAPIKey, googleAPIKey, googleCSEID, bingAPIKey string) tool.Tool {
	st := &aggregateSearchTool{
		duckduckgoClient: httpclient.New(httpclient.Options{Timeout: 15 * time.Second}),
		searxngClient:    httpclient.New(httpclient.Options{Timeout: 15 * time.Second}),
		tavilyClient:     httpclient.New(httpclient.Options{Timeout: 20 * time.Second}),
		googleClient:     httpclient.New(httpclient.Options{Timeout: 15 * time.Second}),
		bingClient:       httpclient.New(httpclient.Options{Timeout: 15 * time.Second}),
		fetchClient:      httpclient.New(httpclient.Options{Timeout: 30 * time.Second}),
		searxngURL:       searxngURL,
		searxngAPIKey:    searxngAPIKey,
		tavilyAPIKey:     tavilyAPIKey,
		googleAPIKey:     googleAPIKey,
		googleCSEID:      googleCSEID,
		bingAPIKey:       bingAPIKey,
		enabledBackends:  enabledBackends,
		maxFetchResults:  3,
	}
	return function.NewFunctionTool(
		st.search,
		function.WithName("web_search"),
		function.WithDescription(
			"Search the web using multiple search engines and aggregate results. "+
				"Returns combined results from DuckDuckGo, SearXNG, Tavily, Google, and Bing. "+
				"Also fetches and returns full page content (as Markdown) for the top results. "+
				"Use this tool to get comprehensive search coverage across multiple sources.",
		),
	)
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

	var wg sync.WaitGroup
	resultChan := make(chan resultWrapper, len(a.enabledBackends))

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

func (a *aggregateSearchTool) fetchAndConvertPage(ctx context.Context, pageURL string) fetchResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return fetchResult{URL: pageURL, Error: fmt.Sprintf("create request: %v", err)}
	}

	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) "+
			"AppleWebKit/537.36 (KHTML, like Gecko) "+
			"Chrome/120.0.0.0 Safari/537.36 Wukong-Agent/1.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := a.fetchClient.Do(req)
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

func (a *aggregateSearchTool) searchDuckDuckGo(ctx context.Context, query string) ([]searchResult, error) {
	searchURL := "https://api.duckduckgo.com/?q=" + url.QueryEscape(query) + "&format=json&no_redirect=1&no_html=1"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := a.duckduckgoClient.Do(httpReq)
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

	resp, err := a.searxngClient.Do(httpReq)
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

	resp, err := a.tavilyClient.Do(httpReq)
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

	resp, err := a.googleClient.Do(httpReq)
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

	resp, err := a.bingClient.Do(httpReq)
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
