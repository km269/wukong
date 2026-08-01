// Package clone provides website cloning functionality.
//
// api_discovery_save.go: Saves discovered hidden API endpoints
// to a JSON file and provides utilities for follow-up pagination
// crawling of discovered endpoints.
package clone

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/km269/wukong/internal/browser/types"
)

// SaveDiscoveredAPIs writes discovered API endpoints to
// api_endpoints.json in the output directory. The file contains
// a JSON array of DiscoveredAPI objects with URL, content type,
// status code, resource type, and detected pagination kind.
func SaveDiscoveredAPIs(
	outputDir string, apis []types.DiscoveredAPI,
) (string, error) {
	if len(apis) == 0 {
		return "", nil
	}

	// Deduplicate by URL.
	seen := make(map[string]bool, len(apis))
	unique := make([]types.DiscoveredAPI, 0, len(apis))
	for _, api := range apis {
		if api.URL == "" || seen[api.URL] {
			continue
		}
		seen[api.URL] = true
		unique = append(unique, api)
	}

	path := filepath.Join(outputDir, "api_endpoints.json")
	data, err := json.MarshalIndent(unique, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal api endpoints: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("write api_endpoints.json: %w", err)
	}
	return path, nil
}
