package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/km269/wukong/pkg/httpclient"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

type googleTool struct {
	client *httpclient.Client
	apiKey string
	cseID  string
}

func NewGoogleTool(apiKey, cseID string) tool.Tool {
	gt := &googleTool{
		client: httpclient.New(httpclient.Options{Timeout: 15 * time.Second}),
		apiKey: apiKey,
		cseID:  cseID,
	}
	return function.NewFunctionTool(
		gt.search,
		function.WithName("google_search"),
		function.WithDescription(
			"Search the web using Google Custom Search API. "+
				"Returns search results including title, URL, and snippet.",
		),
	)
}

type googleSearchReq struct {
	Query string `json:"query" jsonschema:"description=Search query to find relevant information"`
}

type googleSearchRsp struct {
	Success bool           `json:"success"`
	Results []searchResult `json:"results"`
	Error   string         `json:"error,omitempty"`
}

func (g googleTool) search(ctx context.Context, req googleSearchReq) (googleSearchRsp, error) {
	if req.Query == "" {
		return googleSearchRsp{
			Success: false,
			Error:   "query cannot be empty",
		}, nil
	}

	searchURL := fmt.Sprintf(
		"https://www.googleapis.com/customsearch/v1?key=%s&cx=%s&q=%s&num=10",
		url.QueryEscape(g.apiKey),
		url.QueryEscape(g.cseID),
		url.QueryEscape(req.Query),
	)

	httpReq, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return googleSearchRsp{
			Success: false,
			Error:   fmt.Sprintf("create request: %v", err),
		}, nil
	}

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return googleSearchRsp{
			Success: false,
			Error:   fmt.Sprintf("request failed: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return googleSearchRsp{
			Success: false,
			Error:   fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
		}, nil
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return googleSearchRsp{
			Success: false,
			Error:   fmt.Sprintf("parse response: %v", err),
		}, nil
	}

	results := parseGoogleResults(result)
	if len(results) == 0 {
		return googleSearchRsp{
			Success: false,
			Error:   "no search results found",
		}, nil
	}

	return googleSearchRsp{
		Success: true,
		Results: results,
	}, nil
}
