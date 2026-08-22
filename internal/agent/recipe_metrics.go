// Package agent provides recipe execution metrics and the
// recipe_stats tool (P4-B, P4-C).
//
// This file implements:
//   - MetricsCollector interface for per-tool execution statistics.
//   - recipeStatsTool: query execution metrics for any recipe.
//   - Integration with recipeTool for automatic metric recording.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// MetricsCollector exposes execution statistics for a recipe tool.
// *recipeTool implements this interface.
type MetricsCollector interface {
	// Name returns the recipe name this collector tracks.
	Name() string
	// Metrics returns a snapshot of the current execution metrics.
	Metrics() RecipeMetrics
}

// metricsRegistry stores MetricsCollector instances so the stats
// tool can query them. Thread-safe.
type metricsRegistry struct {
	mu         sync.RWMutex
	collectors map[string]MetricsCollector // keyed by recipe name
}

// globalMetricsRegistry is the singleton used by recipe_stats.
var globalMetricsRegistry = &metricsRegistry{
	collectors: make(map[string]MetricsCollector),
}

// registerMetricsCollector adds a collector to the registry.
func registerMetricsCollector(mc MetricsCollector) {
	globalMetricsRegistry.mu.Lock()
	globalMetricsRegistry.collectors[mc.Name()] = mc
	globalMetricsRegistry.mu.Unlock()
}

// collectAllMetrics returns all metrics sorted by call count desc.
func (mr *metricsRegistry) collectAllMetrics() []recipeMetricsEntry {
	mr.mu.RLock()
	defer mr.mu.RUnlock()

	var entries []recipeMetricsEntry
	for _, c := range mr.collectors {
		m := c.Metrics()
		entries = append(entries, recipeMetricsEntry{
			Name:    c.Name(),
			Metrics: m,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Metrics.CallCount >
			entries[j].Metrics.CallCount
	})
	return entries
}

// recipeMetricsEntry pairs a recipe name with its metrics.
type recipeMetricsEntry struct {
	Name    string        `json:"name"`
	Metrics RecipeMetrics `json:"metrics"`
}

// recipeStatsTool provides the `recipe_stats` tool that the main
// agent or user can call to query recipe execution metrics.
type recipeStatsTool struct {
	decl     *tool.Declaration
	registry *metricsRegistry
}

// newRecipeStatsTool creates the recipe_stats discovery and stats
// query tool.
func newRecipeStatsTool() *recipeStatsTool {
	return &recipeStatsTool{
		decl: &tool.Declaration{
			Name: "recipe_stats",
			Description: "Query execution statistics for " +
				"recipe sub-agents. Returns call count, " +
				"success/error counts, last duration, " +
				"total duration, and last error for each " +
				"recipe. Use this to monitor recipe " +
				"performance and diagnose issues.",
			InputSchema: &tool.Schema{
				Type: "object",
				Properties: map[string]*tool.Schema{
					"name": {
						Type: "string",
						Description: "Recipe name to filter " +
							"stats for. Omit to get all.",
					},
				},
			},
		},
		registry: globalMetricsRegistry,
	}
}

// Declaration returns the tool metadata.
func (st *recipeStatsTool) Declaration() *tool.Declaration {
	return st.decl
}

// Call returns recipe execution statistics as JSON.
func (st *recipeStatsTool) Call(
	_ context.Context,
	jsonArgs []byte,
) (any, error) {
	// Parse optional name filter.
	filterName := ""
	if len(jsonArgs) > 0 {
		var raw map[string]any
		if err := json.Unmarshal(jsonArgs, &raw); err != nil {
			return nil, fmt.Errorf(
				"parse recipe_stats args: %w", err)
		}
		if name, ok := raw["name"].(string); ok {
			filterName = name
		}
	}

	if filterName != "" {
		return st.statsForName(filterName)
	}
	return st.statsForAll()
}

// statsForName returns metrics for a single recipe.
func (st *recipeStatsTool) statsForName(name string) (any, error) {
	st.registry.mu.RLock()
	c, ok := st.registry.collectors[name]
	st.registry.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf(
			"recipe %q not found or has no metrics", name)
	}
	m := c.Metrics()
	entry := recipeMetricsEntry{Name: name, Metrics: m}
	return marshalStatsEntry(entry)
}

// statsForAll returns metrics for all recipes.
func (st *recipeStatsTool) statsForAll() (any, error) {
	entries := st.registry.collectAllMetrics()
	return marshalStatsEntries(entries)
}

// marshalStatsEntry serializes a single metrics entry to JSON string.
func marshalStatsEntry(entry recipeMetricsEntry) (string, error) {
	b, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal stats: %w", err)
	}
	return string(b), nil
}

// marshalStatsEntries serializes multiple metrics entries.
func marshalStatsEntries(
	entries []recipeMetricsEntry,
) (string, error) {
	if entries == nil {
		entries = []recipeMetricsEntry{}
	}
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal stats: %w", err)
	}
	return string(b), nil
}
