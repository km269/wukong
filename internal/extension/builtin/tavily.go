package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

type tavilyTool struct {
	apiKey     string
	httpClient *http.Client
}

type tavilyResult struct {
	Title      string   `json:"title"`
	URL        string   `json:"url"`
	Content    string   `json:"content"`
	Score      float64  `json:"score"`
	RawContent string   `json:"raw_content"`
	Summary    string   `json:"summary"`
	Answer     string   `json:"answer"`
	Images     []string `json:"images"`
}

type tavilyResponse struct {
	Query    string         `json:"query"`
	Response string         `json:"response"`
	Results  []tavilyResult `json:"results"`
	Answer   string         `json:"answer"`
	FollowUp string         `json:"follow_up"`
}

func NewTavilyTool(apiKey string) tool.Tool {
	tt := &tavilyTool{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
	return function.NewFunctionTool(
		tt.search,
		function.WithName("web_search"),
		function.WithDescription(
			"Search the web using Tavily AI search for relevant information. "+
				"Use this tool when you need comprehensive, AI-enhanced search results. "+
				"Returns both direct answers and detailed source summaries.",
		),
	)
}

type tavilySearchReq struct {
	Query string `json:"query" jsonschema:"description=Search query to find relevant information"`
}

type tavilySearchRsp struct {
	Success bool   `json:"success"`
	Answer  string `json:"answer,omitempty"`
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Snippet string `json:"snippet"`
	} `json:"results"`
	Error string `json:"error,omitempty"`
}

func (t *tavilyTool) search(
	ctx context.Context, req tavilySearchReq,
) (tavilySearchRsp, error) {
	if req.Query == "" {
		return tavilySearchRsp{
			Success: false,
			Error:   "query cannot be empty",
		}, nil
	}

	if t.apiKey == "" {
		return tavilySearchRsp{
			Success: false,
			Error:   "tavily API key not configured",
		}, nil
	}

	searchURL := "https://api.tavily.com/search"

	body, err := json.Marshal(map[string]interface{}{
		"query":               req.Query,
		"api_key":             t.apiKey,
		"search_depth":        "advanced",
		"max_results":         10,
		"include_answer":      true,
		"include_raw_content": false,
	})
	if err != nil {
		return tavilySearchRsp{
			Success: false,
			Error:   fmt.Sprintf("marshal request: %v", err),
		}, nil
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, searchURL, strings.NewReader(string(body)))
	if err != nil {
		return tavilySearchRsp{
			Success: false,
			Error:   fmt.Sprintf("create request: %v", err),
		}, nil
	}
	httpReq.Header.Set("User-Agent", "Wukong/2.0")
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return tavilySearchRsp{
			Success: false,
			Error:   fmt.Sprintf("search request failed: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return tavilySearchRsp{
			Success: false,
			Error:   fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(bodyBytes)),
		}, nil
	}

	var result tavilyResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return tavilySearchRsp{
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
			snippet := r.Content
			if snippet == "" {
				snippet = r.Summary
			}
			if snippet == "" {
				snippet = r.RawContent
			}
			if len(snippet) > 300 {
				snippet = snippet[:300] + "..."
			}
			results = append(results, struct {
				Title   string `json:"title"`
				URL     string `json:"url"`
				Snippet string `json:"snippet"`
			}{
				Title:   r.Title,
				URL:     r.URL,
				Snippet: snippet,
			})
		}
	}

	return tavilySearchRsp{
		Success: true,
		Answer:  result.Answer,
		Results: results,
	}, nil
}
