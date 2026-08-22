package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/km269/wukong/pkg/httpclient"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

type searxngTool struct {
	baseURL    string
	apiKey     string
	httpClient *httpclient.Client
}

type searxngResult struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Content string  `json:"content"`
	Engine  string  `json:"engine"`
	Score   float64 `json:"score"`
}

type searxngResponse struct {
	Query   string          `json:"query"`
	Results []searxngResult `json:"results"`
}

func NewSearXNGTool(baseURL string, apiKey string) tool.Tool {
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}
	st := &searxngTool{
		baseURL:    baseURL,
		apiKey:     apiKey,
		httpClient: newSearchHTTPClient(15 * time.Second),
	}
	return function.NewFunctionTool(
		st.search,
		function.WithName("web_search"),
		function.WithDescription(
			"Search the web using SearXNG for relevant information. "+
				"Use this tool when you need to find up-to-date information, "+
				"news, or answers to questions that may have changed recently.",
		),
	)
}

type searxngSearchReq struct {
	Query string `json:"query" jsonschema:"description=Search query to find relevant information"`
}

type searxngSearchRsp struct {
	Success bool `json:"success"`
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Snippet string `json:"snippet"`
	} `json:"results"`
	Error string `json:"error,omitempty"`
}

func (s *searxngTool) search(
	ctx context.Context, req searxngSearchReq,
) (searxngSearchRsp, error) {
	if req.Query == "" {
		return searxngSearchRsp{
			Success: false,
			Error:   "query cannot be empty",
		}, nil
	}

	searchURL := s.baseURL + "search?q=" + url.QueryEscape(req.Query) + "&format=json&language=zh"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return searxngSearchRsp{
			Success: false,
			Error:   fmt.Sprintf("create request: %v", err),
		}, nil
	}
	httpReq.Header.Set("User-Agent", "Wukong/2.0")
	httpReq.Header.Set("Accept", "application/json")
	if s.apiKey != "" {
		httpReq.Header.Set("X-Searxng-API-Key", s.apiKey)
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return searxngSearchRsp{
			Success: false,
			Error:   fmt.Sprintf("search request failed: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return searxngSearchRsp{
			Success: false,
			Error:   fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
		}, nil
	}

	var result searxngResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return searxngSearchRsp{
			Success: false,
			Error:   fmt.Sprintf("parse response: %v", err),
		}, nil
	}

	var results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Snippet string `json:"snippet"`
	}
	for _, r := range result.Results {
		if r.URL != "" && r.Title != "" {
			results = append(results, struct {
				Title   string `json:"title"`
				URL     string `json:"url"`
				Snippet string `json:"snippet"`
			}{
				Title:   r.Title,
				URL:     r.URL,
				Snippet: r.Content,
			})
		}
	}

	return searxngSearchRsp{
		Success: true,
		Results: results,
	}, nil
}
