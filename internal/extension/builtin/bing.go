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

type bingTool struct {
	client *httpclient.Client
	apiKey string
}

func NewBingTool(apiKey string) tool.Tool {
	bt := &bingTool{
		client: httpclient.New(httpclient.Options{Timeout: 15 * time.Second}),
		apiKey: apiKey,
	}
	return function.NewFunctionTool(
		bt.search,
		function.WithName("bing_search"),
		function.WithDescription(
			"Search the web using Bing Web Search API. "+
				"Returns search results including title, URL, and snippet.",
		),
	)
}

type bingSearchReq struct {
	Query string `json:"query" jsonschema:"description=Search query to find relevant information"`
}

type bingSearchRsp struct {
	Success bool           `json:"success"`
	Results []searchResult `json:"results"`
	Error   string         `json:"error,omitempty"`
}

func (b bingTool) search(ctx context.Context, req bingSearchReq) (bingSearchRsp, error) {
	if req.Query == "" {
		return bingSearchRsp{
			Success: false,
			Error:   "query cannot be empty",
		}, nil
	}

	searchURL := fmt.Sprintf(
		"https://api.bing.microsoft.com/v7.0/search?q=%s&count=10",
		url.QueryEscape(req.Query),
	)

	httpReq, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return bingSearchRsp{
			Success: false,
			Error:   fmt.Sprintf("create request: %v", err),
		}, nil
	}
	httpReq.Header.Set("Ocp-Apim-Subscription-Key", b.apiKey)

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return bingSearchRsp{
			Success: false,
			Error:   fmt.Sprintf("request failed: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return bingSearchRsp{
			Success: false,
			Error:   fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
		}, nil
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return bingSearchRsp{
			Success: false,
			Error:   fmt.Sprintf("parse response: %v", err),
		}, nil
	}

	results := parseBingResults(result)
	if len(results) == 0 {
		return bingSearchRsp{
			Success: false,
			Error:   "no search results found",
		}, nil
	}

	return bingSearchRsp{
		Success: true,
		Results: results,
	}, nil
}
