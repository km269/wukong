// Package ard OKF Bundle discovery support.
//
// This file extends the ARD (Agentic Resource Discovery) system
// with the ability to discover and register OKF (Open Knowledge
// Format) Bundles as catalog entries. This enables federated
// knowledge discovery: an agent can find knowledge bundles
// across remote registries, just as it currently finds agents
// and MCP servers.
//
// New media type:
//   application/okf-bundle+json — identifies an OKF Bundle entry
//
// URN format for OKF entries:
//   urn:air:<publisher>:knowledge:<bundle-name>
package ard

import (
	"fmt"
	"strings"

	"github.com/km269/wukong/internal/okf"
)

// MediaTypeOKFBundle is the ARD media type for OKF Bundle entries.
const MediaTypeOKFBundle = "application/okf-bundle+json"

// OKFBundleMetadata holds OKF-specific metadata embedded in a
// CatalogEntry's Metadata field.
type OKFBundleMetadata struct {
	OKFVersion   string   `json:"okf_version,omitempty"`
	ConceptCount int      `json:"concept_count,omitempty"`
	ConceptTypes []string `json:"concept_types,omitempty"`
	GitURL       string   `json:"git_url,omitempty"`
	BundlePath   string   `json:"bundle_path,omitempty"`
}

// NewOKFBundleEntry creates a CatalogEntry for an OKF Bundle,
// making it discoverable via ARD federation.
func NewOKFBundleEntry(
	name, displayName, description, gitURL string,
	tags []string,
	okfMeta OKFBundleMetadata,
) CatalogEntry {
	if okfMeta.OKFVersion == "" {
		okfMeta.OKFVersion = okf.OKFVersion
	}

	meta := make(map[string]any)
	if okfMeta.OKFVersion != "" {
		meta["okf_version"] = okfMeta.OKFVersion
	}
	if okfMeta.ConceptCount > 0 {
		meta["concept_count"] = okfMeta.ConceptCount
	}
	if len(okfMeta.ConceptTypes) > 0 {
		meta["concept_types"] = okfMeta.ConceptTypes
	}
	if okfMeta.BundlePath != "" {
		meta["bundle_path"] = okfMeta.BundlePath
	}
	meta["git_url"] = gitURL

	return CatalogEntry{
		Identifier:   fmt.Sprintf("urn:air:wukong.ai:knowledge:%s", name),
		DisplayName:  displayName,
		Type:         MediaTypeOKFBundle,
		URL:          gitURL,
		Description:  description,
		Tags:         tags,
		Capabilities: okfMeta.ConceptTypes,
		Version:      okfMeta.OKFVersion,
		UpdatedAt:    Now(),
		Metadata:     meta,
	}
}

// IsOKFBundleEntry returns true if the catalog entry represents
// an OKF Bundle.
func IsOKFBundleEntry(entry *CatalogEntry) bool {
	return entry != nil &&
		entry.Type == MediaTypeOKFBundle
}

// ExtractOKFMetadata extracts OKF-specific metadata from a
// CatalogEntry's Metadata field.
func ExtractOKFMetadata(entry *CatalogEntry) OKFBundleMetadata {
	var meta OKFBundleMetadata
	if entry == nil || entry.Metadata == nil {
		return meta
	}

	if v, ok := entry.Metadata["okf_version"]; ok {
		if s, ok := v.(string); ok {
			meta.OKFVersion = s
		}
	}
	if v, ok := entry.Metadata["concept_count"]; ok {
		switch val := v.(type) {
		case int:
			meta.ConceptCount = val
		case float64:
			meta.ConceptCount = int(val)
		}
	}
	if v, ok := entry.Metadata["concept_types"]; ok {
		if slice, ok := v.([]any); ok {
			for _, item := range slice {
				if s, ok := item.(string); ok {
					meta.ConceptTypes = append(
						meta.ConceptTypes, s)
				}
			}
		}
	}
	if v, ok := entry.Metadata["git_url"]; ok {
		if s, ok := v.(string); ok {
			meta.GitURL = s
		}
	}
	if v, ok := entry.Metadata["bundle_path"]; ok {
		if s, ok := v.(string); ok {
			meta.BundlePath = s
		}
	}
	return meta
}

// RegisterOKFBundle registers an OKF Bundle in the ARD catalog
// for federated discovery by other agents.
func RegisterOKFBundle(
	ts *ToolSet,
	name, displayName, description, gitURL string,
	tags []string,
	okfMeta OKFBundleMetadata,
) error {
	entry := NewOKFBundleEntry(
		name, displayName, description, gitURL, tags, okfMeta)
	return ts.Register(entry)
}

// SearchOKFBundles searches the catalog for OKF Bundle entries
// matching the query. Returns all OKF entries when query is empty.
func SearchOKFBundles(ts *ToolSet, query string) []CatalogEntry {
	all := ts.List()
	var results []CatalogEntry

	query = strings.ToLower(query)
	for i := range all {
		if !IsOKFBundleEntry(&all[i]) {
			continue
		}
		if query == "" {
			results = append(results, all[i])
			continue
		}
		entry := all[i]
		if strings.Contains(strings.ToLower(entry.DisplayName), query) ||
			strings.Contains(strings.ToLower(entry.Description), query) {
			results = append(results, entry)
			continue
		}
		for _, tag := range entry.Tags {
			if strings.Contains(strings.ToLower(tag), query) {
				results = append(results, entry)
				break
			}
		}
	}
	return results
}
