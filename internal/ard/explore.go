// Package ard provides Agentic Resource Discovery implementation.
package ard

import (
	"sort"
	"strings"
	"time"
)

// ExploreOptions defines advanced options for explore API.
type ExploreOptions struct {
	// Basic filters
	Type         string   // Media type filter
	Tags         []string // Tag filter (any match)
	TagsAll      []string // Tag filter (all must match)
	Capabilities []string // Capability filter (any match)
	Publisher    string   // Publisher domain filter

	// Sorting options
	SortBy    string // "name", "type", "updated", "score"
	SortDesc  bool   // Sort descending

	// Pagination
	Limit  int
	Offset int

	// Search query (optional)
	Query string

	// Facets (return counts for each facet)
	IncludeFacets bool
}

// ExploreResult is the result of explore API.
type ExploreResult struct {
	Entries  []CatalogEntry `json:"entries"`
	Total    int           `json:"total"`
	Facets   *ExploreFacets `json:"facets,omitempty"`
}

// ExploreFacets contains facet counts for filtering.
type ExploreFacets struct {
	Types        map[string]int `json:"types,omitempty"`
	Tags         map[string]int `json:"tags,omitempty"`
	Publishers   map[string]int `json:"publishers,omitempty"`
	Capabilities map[string]int `json:"capabilities,omitempty"`
}

// Explore performs advanced exploration with filtering and sorting.
func (r *Registry) ExploreWithOptions(opts *ExploreOptions) (*ExploreResult, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if opts == nil {
		opts = &ExploreOptions{}
	}

	// Filter entries
	entries := r.filterEntriesAdvanced(opts)

	// Calculate total before pagination
	total := len(entries)

	// Build facets if requested
	var facets *ExploreFacets
	if opts.IncludeFacets {
		facets = r.buildFacets(entries)
	}

	// Sort entries
	r.sortEntries(entries, opts.SortBy, opts.SortDesc)

	// Apply pagination
	if opts.Offset >= total {
		entries = []CatalogEntry{}
	} else {
		end := opts.Offset + opts.Limit
		if opts.Limit <= 0 {
			end = total
		}
		if end > total {
			end = total
		}
		entries = entries[opts.Offset:end]
	}

	return &ExploreResult{
		Entries: entries,
		Total:   total,
		Facets:  facets,
	}, nil
}

// filterEntriesAdvanced filters entries with advanced options.
func (r *Registry) filterEntriesAdvanced(opts *ExploreOptions) []CatalogEntry {
	var entries []CatalogEntry

	for i := range r.catalog.Entries {
		entry := &r.catalog.Entries[i]

		// Type filter
		if opts.Type != "" && entry.Type != opts.Type {
			continue
		}

		// Publisher filter
		if opts.Publisher != "" {
			urn, err := ParseURN(entry.Identifier)
			if err != nil || urn.Publisher != opts.Publisher {
				continue
			}
		}

		// Tags filter (any match)
		if len(opts.Tags) > 0 {
			if !hasAnyTag(entry.Tags, opts.Tags) {
				continue
			}
		}

		// Tags filter (all must match)
		if len(opts.TagsAll) > 0 {
			if !hasAllTags(entry.Tags, opts.TagsAll) {
				continue
			}
		}

		// Capabilities filter
		if len(opts.Capabilities) > 0 {
			if !hasAnyCapability(entry.Capabilities, opts.Capabilities) {
				continue
			}
		}

		// Query filter
		if opts.Query != "" {
			queryLower := lowercase(opts.Query)
			matches := contains(lowercase(entry.DisplayName), queryLower) ||
				contains(lowercase(entry.Description), queryLower) ||
				hasAnyTagLower(entry.Tags, queryLower) ||
				hasAnyCapabilityLower(entry.Capabilities, queryLower)
			if !matches {
				continue
			}
		}

		entries = append(entries, *entry)
	}

	return entries
}

// sortEntries sorts entries by the specified field.
func (r *Registry) sortEntries(entries []CatalogEntry, sortBy string, desc bool) {
	switch sortBy {
	case "name":
		sort.Slice(entries, func(i, j int) bool {
			cmp := strings.Compare(entries[i].DisplayName, entries[j].DisplayName)
			if desc {
				return cmp > 0
			}
			return cmp < 0
		})
	case "type":
		sort.Slice(entries, func(i, j int) bool {
			cmp := strings.Compare(entries[i].Type, entries[j].Type)
			if desc {
				return cmp > 0
			}
			return cmp < 0
		})
	case "updated":
		sort.Slice(entries, func(i, j int) bool {
			// Compare timestamps
			ti := parseTime(entries[i].UpdatedAt)
			tj := parseTime(entries[j].UpdatedAt)
			if desc {
				return ti.After(tj)
			}
			return ti.Before(tj)
		})
	default:
		// Default: sort by identifier
		sort.Slice(entries, func(i, j int) bool {
			cmp := strings.Compare(entries[i].Identifier, entries[j].Identifier)
			if desc {
				return cmp > 0
			}
			return cmp < 0
		})
	}
}

// buildFacets builds facet counts from entries.
func (r *Registry) buildFacets(entries []CatalogEntry) *ExploreFacets {
	facets := &ExploreFacets{
		Types:        make(map[string]int),
		Tags:         make(map[string]int),
		Publishers:   make(map[string]int),
		Capabilities: make(map[string]int),
	}

	for _, entry := range entries {
		// Count types
		facets.Types[entry.Type]++

		// Count tags
		for _, tag := range entry.Tags {
			facets.Tags[tag]++
		}

		// Count capabilities
		for _, cap := range entry.Capabilities {
			facets.Capabilities[cap]++
		}

		// Count publishers
		urn, err := ParseURN(entry.Identifier)
		if err == nil {
			facets.Publishers[urn.Publisher]++
		}
	}

	return facets
}

// Helper functions

func hasAnyTag(entryTags, filterTags []string) bool {
	for _, ft := range filterTags {
		for _, et := range entryTags {
			if stringsEqual(et, ft) {
				return true
			}
		}
	}
	return false
}

func hasAllTags(entryTags, filterTags []string) bool {
	for _, ft := range filterTags {
		found := false
		for _, et := range entryTags {
			if stringsEqual(et, ft) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func hasAnyCapability(entryCaps, filterCaps []string) bool {
	for _, fc := range filterCaps {
		for _, ec := range entryCaps {
			if stringsEqual(ec, fc) {
				return true
			}
		}
	}
	return false
}

func hasAnyTagLower(tags []string, queryLower string) bool {
	for _, tag := range tags {
		if contains(lowercase(tag), queryLower) {
			return true
		}
	}
	return false
}

func hasAnyCapabilityLower(caps []string, queryLower string) bool {
	for _, cap := range caps {
		if contains(lowercase(cap), queryLower) {
			return true
		}
	}
	return false
}

func stringsEqual(a, b string) bool {
	return lowercase(a) == lowercase(b)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// BrowseByType returns entries grouped by type.
func (r *Registry) BrowseByType() (map[string][]CatalogEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string][]CatalogEntry)

	for _, entry := range r.catalog.Entries {
		result[entry.Type] = append(result[entry.Type], entry)
	}

	// Sort each group by name
	for typ := range result {
		sort.Slice(result[typ], func(i, j int) bool {
			return strings.Compare(result[typ][i].DisplayName, result[typ][j].DisplayName) < 0
		})
	}

	return result, nil
}

// BrowseByPublisher returns entries grouped by publisher.
func (r *Registry) BrowseByPublisher() (map[string][]CatalogEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string][]CatalogEntry)

	for _, entry := range r.catalog.Entries {
		urn, err := ParseURN(entry.Identifier)
		if err != nil {
			continue
		}
		result[urn.Publisher] = append(result[urn.Publisher], entry)
	}

	// Sort each group by name
	for pub := range result {
		sort.Slice(result[pub], func(i, j int) bool {
			return strings.Compare(result[pub][i].DisplayName, result[pub][j].DisplayName) < 0
		})
	}

	return result, nil
}

// BrowseByTag returns entries grouped by tag.
func (r *Registry) BrowseByTag() (map[string][]CatalogEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string][]CatalogEntry)

	for _, entry := range r.catalog.Entries {
		for _, tag := range entry.Tags {
			result[tag] = append(result[tag], entry)
		}
	}

	// Sort each group by name
	for tag := range result {
		sort.Slice(result[tag], func(i, j int) bool {
			return strings.Compare(result[tag][i].DisplayName, result[tag][j].DisplayName) < 0
		})
	}

	return result, nil
}

// GetTypes returns all unique types in the catalog.
func (r *Registry) GetTypes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	types := make(map[string]bool)
	for _, entry := range r.catalog.Entries {
		types[entry.Type] = true
	}

	result := make([]string, 0, len(types))
	for typ := range types {
		result = append(result, typ)
	}

	sort.Strings(result)
	return result
}

// GetPublishers returns all unique publishers in the catalog.
func (r *Registry) GetPublishers() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	publishers := make(map[string]bool)
	for _, entry := range r.catalog.Entries {
		urn, err := ParseURN(entry.Identifier)
		if err != nil {
			continue
		}
		publishers[urn.Publisher] = true
	}

	result := make([]string, 0, len(publishers))
	for pub := range publishers {
		result = append(result, pub)
	}

	sort.Strings(result)
	return result
}

// GetTags returns all unique tags in the catalog.
func (r *Registry) GetTags() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tags := make(map[string]bool)
	for _, entry := range r.catalog.Entries {
		for _, tag := range entry.Tags {
			tags[tag] = true
		}
	}

	result := make([]string, 0, len(tags))
	for tag := range tags {
		result = append(result, tag)
	}

	sort.Strings(result)
	return result
}

// GetCapabilities returns all unique capabilities in the catalog.
func (r *Registry) GetCapabilities() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	caps := make(map[string]bool)
	for _, entry := range r.catalog.Entries {
		for _, cap := range entry.Capabilities {
			caps[cap] = true
		}
	}

	result := make([]string, 0, len(caps))
	for cap := range caps {
		result = append(result, cap)
	}

	sort.Strings(result)
	return result
}