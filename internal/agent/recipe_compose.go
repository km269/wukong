// Package agent provides recipe composition, retry, and inheritance
// support that extends the base recipe system.
//
// This file implements four P1/P2 enhancements:
//
//  1. Sub-recipe composition (P1-A): recipes can reference other
//     recipes in their `tools` list. The referenced recipe's tool is
//     granted to the referencing recipe's sub-agent, enabling
//     hierarchical recipe orchestration.
//  2. Retry & validation (P1-B): a `retry` config field wraps recipe
//     execution with exponential backoff. When `response.validate_output`
//     is true, the returned JSON is validated before acceptance.
//  3. Recipe extends (P2-B): a recipe can `extends` another recipe,
//     inheriting all fields. Child fields override the parent. This
//     enables sharing common recipe configurations.
//
// All enhancements are backward compatible: recipes without the new
// fields behave exactly as before.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// RecipeRetryConfig configures retry behavior for recipe execution.
//
// When set on a recipe, the recipe tool is wrapped with retry logic.
// On execution failure (error or output validation failure), the
// recipe is retried up to MaxAttempts times with exponential backoff.
type RecipeRetryConfig struct {
	// MaxAttempts is the total number of attempts including the
	// first try. Must be >= 1. Default: 3.
	MaxAttempts int `yaml:"max_attempts"`
	// InitialWait is the delay before the second attempt.
	// Default: 1s.
	InitialWait string `yaml:"initial_wait"`
	// BackoffFactor controls how the delay grows after each failed
	// attempt: delay = InitialWait * BackoffFactor^(attempt-1).
	// Default: 2.0.
	BackoffFactor float64 `yaml:"backoff_factor"`
	// MaxWait caps the computed delay. Zero means no cap.
	// Default: 30s.
	MaxWait string `yaml:"max_wait"`
}

// retryDefaults are applied when fields are zero or invalid.
const (
	defaultRetryMaxAttempts   = 3
	defaultRetryInitialWait   = 1 * time.Second
	defaultRetryBackoffFactor = 2.0
	defaultRetryMaxWait       = 30 * time.Second
)

// resolveRetryConfig fills in defaults for missing fields.
func resolveRetryConfig(cfg *RecipeRetryConfig) *resolvedRetry {
	r := &resolvedRetry{
		maxAttempts:   cfg.MaxAttempts,
		backoffFactor: cfg.BackoffFactor,
	}
	if r.maxAttempts < 1 {
		r.maxAttempts = defaultRetryMaxAttempts
	}
	r.initialWait = parseDurationOrDefault(
		cfg.InitialWait, defaultRetryInitialWait)
	r.maxWait = parseDurationOrDefault(
		cfg.MaxWait, defaultRetryMaxWait)
	if r.backoffFactor <= 0 {
		r.backoffFactor = defaultRetryBackoffFactor
	}
	return r
}

// resolvedRetry is the validated, ready-to-use retry configuration.
type resolvedRetry struct {
	maxAttempts   int
	initialWait   time.Duration
	backoffFactor float64
	maxWait       time.Duration
}

// computeDelay returns the delay before the given attempt (1-based).
func (r *resolvedRetry) computeDelay(attempt int) time.Duration {
	if attempt <= 1 {
		return 0
	}
	// attempt=2 -> initialWait * factor^0 = initialWait
	// attempt=3 -> initialWait * factor^1
	// attempt=N -> initialWait * factor^(N-2)
	power := float64(attempt - 2)
	delay := float64(r.initialWait)
	for i := 0; i < int(power); i++ {
		delay *= r.backoffFactor
	}
	d := time.Duration(delay)
	if r.maxWait > 0 && d > r.maxWait {
		d = r.maxWait
	}
	return d
}

// parseDurationOrDefault parses a duration string, returning the
// default on error or empty input.
func parseDurationOrDefault(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return def
	}
	return d
}

// retryTool wraps a CallableTool with retry logic.
//
// It implements tool.CallableTool. On Call failure, it retries up
// to the configured max attempts with exponential backoff. If a
// JSON schema validator is set, the output is validated before
// acceptance; validation failure triggers a retry.
type retryTool struct {
	inner    tool.CallableTool
	retry    *resolvedRetry
	validate func(any) error // nil = no validation
}

// newRetryTool wraps a CallableTool with retry and optional output
// validation.
func newRetryTool(
	inner tool.CallableTool,
	cfg *RecipeRetryConfig,
	validator func(any) error,
) *retryTool {
	return &retryTool{
		inner:    inner,
		retry:    resolveRetryConfig(cfg),
		validate: validator,
	}
}

// Declaration delegates to the inner tool.
func (rt *retryTool) Declaration() *tool.Declaration {
	return rt.inner.Declaration()
}

// Call executes the inner tool with retry logic.
func (rt *retryTool) Call(
	ctx context.Context,
	jsonArgs []byte,
) (any, error) {
	var lastErr error
	var lastResult any

	for attempt := 1; attempt <= rt.retry.maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		result, err := rt.inner.Call(ctx, jsonArgs)
		if err == nil {
			if rt.validate != nil {
				if vErr := rt.validate(result); vErr != nil {
					lastErr = fmt.Errorf(
						"output validation (attempt %d/%d): %w",
						attempt, rt.retry.maxAttempts, vErr)
					lastResult = result
					slog.Debug("recipe retry: validation failed",
						slog.Int("attempt", attempt),
						slog.Int("max", rt.retry.maxAttempts),
						slog.String("error", vErr.Error()))
				} else {
					return result, nil
				}
			} else {
				return result, nil
			}
		} else {
			lastErr = err
			slog.Debug("recipe retry: call failed",
				slog.Int("attempt", attempt),
				slog.Int("max", rt.retry.maxAttempts),
				slog.String("error", err.Error()))
		}

		if attempt < rt.retry.maxAttempts {
			delay := rt.retry.computeDelay(attempt + 1)
			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
		}
	}

	if lastResult != nil && rt.validate != nil {
		// Return last result even if validation failed on final
		// attempt — the caller may still find it useful.
		slog.Warn("recipe retry: returning result that failed validation",
			slog.String("error", lastErr.Error()))
		return lastResult, nil
	}
	return nil, fmt.Errorf("recipe failed after %d attempts: %w",
		rt.retry.maxAttempts, lastErr)
}

// buildOutputValidator creates a validator function from a
// RecipeResponseConfig. The validator checks that the output is
// valid JSON and that all top-level required fields exist.
//
// This is a lightweight safety net — the model-native
// WithStructuredOutputJSONSchema handles strict schema enforcement
// at the provider level. This validator catches cases where the
// provider doesn't enforce the schema or returns malformed JSON.
func buildOutputValidator(
	resp *RecipeResponseConfig,
) func(any) error {
	if resp == nil || len(resp.JSONSchema) == 0 {
		return nil
	}

	requiredFields := extractRequiredFields(resp.JSONSchema)

	return func(result any) error {
		jsonBytes, err := marshalResult(result)
		if err != nil {
			return fmt.Errorf("marshal result: %w", err)
		}

		var parsed map[string]any
		if err := json.Unmarshal(jsonBytes, &parsed); err != nil {
			return fmt.Errorf("output is not valid JSON: %w", err)
		}

		for _, field := range requiredFields {
			if _, ok := parsed[field]; !ok {
				return fmt.Errorf(
					"output missing required field: %s", field)
			}
		}

		return nil
	}
}

// extractRequiredFields pulls the top-level "required" array from
// a JSON Schema map.
func extractRequiredFields(schema map[string]any) []string {
	raw, ok := schema["required"]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	var fields []string
	for _, v := range arr {
		if s, ok := v.(string); ok {
			fields = append(fields, s)
		}
	}
	return fields
}

// marshalResult converts a tool Call result to JSON bytes.
// The result may be a string, []byte, or any JSON-serializable type.
func marshalResult(result any) ([]byte, error) {
	switch v := result.(type) {
	case string:
		return []byte(v), nil
	case []byte:
		return v, nil
	default:
		return json.Marshal(result)
	}
}

// ---------------------------------------------------------------------------
// Sub-recipe composition (P1-A)
// ---------------------------------------------------------------------------

// recipeToolNamePrefix is the prefix for recipe tool names.
const recipeToolNamePrefix = "recipe-"

// normalizeRecipeRef normalizes a tool name that may reference a
// recipe. If the name is a bare recipe name (e.g. "reviewer") or
// a prefixed name (e.g. "recipe-reviewer"), it returns the recipe
// name without prefix ("reviewer"). For non-recipe tool names, it
// returns "".
//
// A name is considered a recipe reference if:
//   - It starts with "recipe-" (prefixed form)
//   - It matches a known recipe name exactly (bare form)
func normalizeRecipeRef(name string, recipeNames map[string]bool) string {
	if strings.HasPrefix(name, recipeToolNamePrefix) {
		return strings.TrimPrefix(name, recipeToolNamePrefix)
	}
	if recipeNames[name] {
		return name
	}
	return ""
}

// recipeDependencies returns the list of recipe names that the given
// recipe depends on (references in its tools list).
func recipeDependencies(
	recipe *RecipeConfig,
	recipeNames map[string]bool,
) []string {
	var deps []string
	for _, toolName := range recipe.Tools {
		ref := normalizeRecipeRef(toolName, recipeNames)
		if ref != "" && ref != recipe.Name {
			deps = append(deps, ref)
		}
	}
	return deps
}

// topoSortRecipes performs a topological sort of recipes by their
// sub-recipe dependencies. Returns the sorted order or an error if
// a circular dependency is detected.
//
// Recipes with no dependencies come first. Each recipe appears after
// all recipes it depends on.
func topoSortRecipes(
	recipes map[string]*RecipeConfig,
) ([]string, error) {
	// Build recipe name set for reference normalization.
	recipeNames := make(map[string]bool, len(recipes))
	for name := range recipes {
		recipeNames[name] = true
	}

	// Build dependency graph.
	deps := make(map[string][]string, len(recipes))
	for name, recipe := range recipes {
		deps[name] = recipeDependencies(recipe, recipeNames)
	}

	// Kahn's algorithm.
	inDegree := make(map[string]int, len(recipes))
	for name := range recipes {
		inDegree[name] = 0
	}
	for _, dlist := range deps {
		for _, d := range dlist {
			inDegree[d]++ // Wait, this is backwards.
		}
	}

	// Actually, let me redo this properly.
	// inDegree[name] = number of recipes that name depends on.
	// We want to process recipes with 0 dependencies first.
	inDegree = make(map[string]int, len(recipes))
	dependents := make(map[string][]string, len(recipes))
	for name := range recipes {
		inDegree[name] = len(deps[name])
	}
	for name, dlist := range deps {
		for _, d := range dlist {
			dependents[d] = append(dependents[d], name)
		}
	}

	// Start with recipes that have no dependencies.
	var queue []string
	for name, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}

	// Sort queue for deterministic ordering.
	sortStrings(queue)

	var sorted []string
	for len(queue) > 0 {
		// Pop front.
		current := queue[0]
		queue = queue[1:]
		sorted = append(sorted, current)

		for _, dependent := range dependents[current] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				queue = append(queue, dependent)
				sortStrings(queue)
			}
		}
	}

	if len(sorted) != len(recipes) {
		// Circular dependency detected.
		var cycleNodes []string
		for name, deg := range inDegree {
			if deg > 0 {
				cycleNodes = append(cycleNodes, name)
			}
		}
		sortStrings(cycleNodes)
		return nil, fmt.Errorf(
			"circular recipe dependency detected among: %s",
			strings.Join(cycleNodes, ", "))
	}

	return sorted, nil
}

// sortStrings sorts a string slice in place (insertion sort for
// small slices).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// ---------------------------------------------------------------------------
// Recipe extends / inheritance (P2-B)
// ---------------------------------------------------------------------------

// resolveExtends resolves the `extends` chain for a recipe, merging
// fields from parent recipes. Child fields take precedence over
// parent fields.
//
// The extends chain is resolved recursively. A circular extends
// (A extends B extends A) returns an error.
func resolveExtends(
	name string,
	recipes map[string]*RecipeConfig,
	visiting map[string]bool,
) (*RecipeConfig, error) {
	recipe, ok := recipes[name]
	if !ok {
		return nil, fmt.Errorf("recipe not found: %s", name)
	}

	if recipe.Extends == "" {
		// No parent — return as-is (clone to be safe).
		return cloneRecipe(recipe), nil
	}

	// Detect circular extends.
	if visiting[name] {
		return nil, fmt.Errorf(
			"circular extends detected: %s", name)
	}
	visiting[name] = true
	defer delete(visiting, name)

	parent, err := resolveExtends(recipe.Extends, recipes, visiting)
	if err != nil {
		return nil, fmt.Errorf("resolve extends for %s: %w",
			name, err)
	}

	return mergeRecipes(parent, recipe), nil
}

// resolveAllExtends resolves extends for all recipes in the map.
// Returns a new map with fully resolved recipes.
func resolveAllExtends(
	recipes map[string]*RecipeConfig,
) (map[string]*RecipeConfig, error) {
	result := make(map[string]*RecipeConfig, len(recipes))
	for name := range recipes {
		resolved, err := resolveExtends(
			name, recipes, make(map[string]bool))
		if err != nil {
			return nil, fmt.Errorf("recipe %s: %w", name, err)
		}
		result[name] = resolved
	}
	return result, nil
}

// mergeRecipes merges a child recipe onto a parent. Non-zero child
// fields override parent fields. Slice fields are replaced (not
// appended) when the child's slice is non-empty.
func mergeRecipes(parent, child *RecipeConfig) *RecipeConfig {
	merged := cloneRecipe(parent)

	if child.Name != "" {
		merged.Name = child.Name
	}
	if child.Description != "" {
		merged.Description = child.Description
	}
	if child.Instruction != "" {
		merged.Instruction = child.Instruction
	}
	if child.Prompt != "" {
		merged.Prompt = child.Prompt
	}
	if len(child.Parameters) > 0 {
		merged.Parameters = child.Parameters
	}
	if child.Response != nil {
		merged.Response = child.Response
	}
	if child.Model != "" {
		merged.Model = child.Model
	}
	if len(child.Tools) > 0 {
		merged.Tools = child.Tools
	}
	if child.Temperature != 0 {
		merged.Temperature = child.Temperature
	}
	if child.MaxTokens != 0 {
		merged.MaxTokens = child.MaxTokens
	}
	if child.MaxIterations != 0 {
		merged.MaxIterations = child.MaxIterations
	}
	if child.SkipSummarization {
		merged.SkipSummarization = child.SkipSummarization
	}
	if child.Retry != nil {
		merged.Retry = child.Retry
	}
	// Extends is not inherited — only the child's own extends is
	// resolved, and the merged result has no further extends.
	merged.Extends = ""

	return merged
}

// cloneRecipe creates a shallow copy of a RecipeConfig.
func cloneRecipe(r *RecipeConfig) *RecipeConfig {
	if r == nil {
		return nil
	}
	clone := *r
	if r.Parameters != nil {
		clone.Parameters = make(
			[]RecipeParameter, len(r.Parameters))
		copy(clone.Parameters, r.Parameters)
	}
	if r.Tools != nil {
		clone.Tools = make([]string, len(r.Tools))
		copy(clone.Tools, r.Tools)
	}
	if r.Response != nil {
		respCopy := *r.Response
		if r.Response.JSONSchema != nil {
			respCopy.JSONSchema = make(
				map[string]any, len(r.Response.JSONSchema))
			for k, v := range r.Response.JSONSchema {
				respCopy.JSONSchema[k] = v
			}
		}
		clone.Response = &respCopy
	}
	if r.Retry != nil {
		retryCopy := *r.Retry
		clone.Retry = &retryCopy
	}
	return &clone
}
