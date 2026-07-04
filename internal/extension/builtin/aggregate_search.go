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

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

type aggregateSearchTool struct {
	duckduckgoClient *http.Client
	searxngClient    *http.Client
	tavilyClient     *http.Client
	searxngURL       string
	searxngAPIKey    string
	tavilyAPIKey     string
	enabledBackends  []string
}

type searchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	Source  string `json:"source"`
}

func NewAggregateSearchTool(enabledBackends []string, searxngURL, searxngAPIKey, tavilyAPIKey string) tool.Tool {
	st := &aggregateSearchTool{
		duckduckgoClient: &http.Client{Timeout: 15 * time.Second},
		searxngClient:    &http.Client{Timeout: 15 * time.Second},
		tavilyClient:     &http.Client{Timeout: 20 * time.Second},
		searxngURL:       searxngURL,
		searxngAPIKey:    searxngAPIKey,
		tavilyAPIKey:     tavilyAPIKey,
		enabledBackends:  enabledBackends,
	}
	return function.NewFunctionTool(
		st.search,
		function.WithName("web_search"),
		function.WithDescription(
			"Search the web using multiple search engines and aggregate results. "+
				"Returns combined results from DuckDuckGo, SearXNG, and Tavily. "+
				"Use this tool to get comprehensive search coverage across multiple sources.",
		),
	)
}

type aggregateSearchReq struct {
	Query string `json:"query" jsonschema:"description=Search query to find relevant information"`
}

type aggregateSearchRsp struct {
	Success bool           `json:"success"`
	Results []searchResult `json:"results"`
	Error   string         `json:"error,omitempty"`
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

	return aggregateSearchRsp{
		Success: true,
		Results: allResults,
	}, nil
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

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
