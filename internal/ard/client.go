// Package ard provides Agentic Resource Discovery implementation.
package ard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is an ARD client for querying remote registries.
type Client struct {
	httpClient *http.Client
	timeout    time.Duration
}

// NewClient creates a new ARD client.
func NewClient(timeout time.Duration) *Client {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		httpClient: &http.Client{
			Timeout: timeout,
		},
		timeout: timeout,
	}
}

// FetchCatalog fetches the ai-catalog.json from a registry.
func (c *Client) FetchCatalog(ctx context.Context, baseURL string) (*AICatalog, error) {
	url := fmt.Sprintf("%s/.well-known/ai-catalog.json", strings.TrimSuffix(baseURL, "/"))
	
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	
	req.Header.Set("Accept", MediaTypeAICatalog)
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch catalog: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}
	
	catalog := &AICatalog{}
	if err := json.NewDecoder(resp.Body).Decode(catalog); err != nil {
		return nil, fmt.Errorf("decode catalog: %w", err)
	}
	
	return catalog, nil
}

// Search searches a remote registry.
func (c *Client) Search(ctx context.Context, baseURL string, req *SearchRequest) (*SearchResponse, error) {
	url := fmt.Sprintf("%s/api/v1/search", strings.TrimSuffix(baseURL, "/"))
	
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}
	
	var searchResp SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	
	return &searchResp, nil
}

// Explore explores a remote registry.
func (c *Client) Explore(ctx context.Context, baseURL string, req *ExploreRequest) (*ExploreResponse, error) {
	url := fmt.Sprintf("%s/api/v1/explore", strings.TrimSuffix(baseURL, "/"))
	
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	
	httpReq.Header.Set("Content-Type", "application/json")
	
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("explore: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}
	
	var exploreResp ExploreResponse
	if err := json.NewDecoder(resp.Body).Decode(&exploreResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	
	return &exploreResp, nil
}

// List lists entries from a remote registry.
func (c *Client) List(ctx context.Context, baseURL string, limit, offset int) (*ListResponse, error) {
	url := fmt.Sprintf("%s/api/v1/agents?limit=%d&offset=%d",
		strings.TrimSuffix(baseURL, "/"), limit, offset)
	
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	
	req.Header.Set("Accept", "application/json")
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}
	
	var listResp ListResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	
	return &listResp, nil
}

// Health checks if a registry is healthy.
func (c *Client) Health(ctx context.Context, baseURL string) error {
	url := fmt.Sprintf("%s/health", strings.TrimSuffix(baseURL, "/"))
	
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: status %d", resp.StatusCode)
	}
	
	return nil
}

// FetchEntry fetches a specific entry from a registry.
func (c *Client) FetchEntry(ctx context.Context, url string) (*CatalogEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch entry: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}
	
	var entry CatalogEntry
	if err := json.NewDecoder(resp.Body).Decode(&entry); err != nil {
		return nil, fmt.Errorf("decode entry: %w", err)
	}
	
	return &entry, nil
}

// DiscoverRegistries discovers registries from a referral chain.
func (c *Client) DiscoverRegistries(ctx context.Context, initialURL string) ([]string, error) {
	var registries []string
	seen := make(map[string]bool)
	
	// BFS discovery
	queue := []string{initialURL}
	
	for len(queue) > 0 && len(registries) < 10 {
		url := queue[0]
		queue = queue[1:]
		
		if seen[url] {
			continue
		}
		seen[url] = true
		
		// Check health
		if err := c.Health(ctx, url); err != nil {
			continue
		}
		
		registries = append(registries, url)
		
		// Fetch catalog and look for registry entries
		catalog, err := c.FetchCatalog(ctx, url)
		if err != nil {
			continue
		}
		
		// Look for registry referrals
		for _, entry := range catalog.Entries {
			if entry.Type == MediaTypeAIRegistry && entry.URL != "" {
				queue = append(queue, entry.URL)
			}
		}
	}
	
	return registries, nil
}

// FederatedSearch searches multiple registries.
func (c *Client) FederatedSearch(ctx context.Context, registries []string, req *SearchRequest) ([]SearchResult, error) {
	var allResults []SearchResult
	
	// Search each registry
	for _, baseURL := range registries {
		resp, err := c.Search(ctx, baseURL, req)
		if err != nil {
			continue
		}
		
		for i := range resp.Results {
			allResults = append(allResults, resp.Results[i])
		}
	}
	
	// Deduplicate by identifier
	seen := make(map[string]bool)
	var deduped []SearchResult
	for _, r := range allResults {
		if !seen[r.Identifier] {
			seen[r.Identifier] = true
			deduped = append(deduped, r)
		}
	}
	
	return deduped, nil
}

// TrimURL trims trailing slash from a URL.
func TrimURL(url string) string {
	return strings.TrimSuffix(url, "/")
}
