// Package agent provides advanced recipe features: model override,
// timeout control, recipe discovery, and hot-reload.
//
// This file implements P3 enhancements:
//
//  1. Model override (P3-A): the `model` field in a recipe is wired
//     to create a per-recipe LLM model via factory.CreateModelWithName.
//  2. Timeout control (P3-B): a `timeout` field wraps recipe
//     execution with context.WithTimeout.
//  3. Recipe discovery (P3-C): a `list_recipes` tool lets the main
//     agent discover available recipes at runtime.
//  4. Hot-reload (P3-D): fsnotify file-system watching detects
//     recipe YAML changes and rebuilds the tool set automatically.
//
// All enhancements are backward compatible.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ===========================================================================
// P3-A: Model override — per-recipe LLM model selection
// ===========================================================================

// createRecipeModel creates a model for a recipe. If recipe.Model is
// set, it uses factory.CreateModelWithName to override the model name
// on the default provider. Falls back to the default model on error.
func createRecipeModel(
	factory providerModelFactory,
	recipe *RecipeConfig,
	defaultModel model.Model,
) model.Model {
	if recipe.Model == "" {
		return defaultModel
	}
	custom, err := factory.CreateModelWithName("", recipe.Model)
	if err != nil {
		slog.Warn("recipe: model override failed, using default",
			slog.String("recipe", recipe.Name),
			slog.String("model", recipe.Model),
			slog.String("error", err.Error()))
		return defaultModel
	}
	slog.Info("recipe: using custom model",
		slog.String("recipe", recipe.Name),
		slog.String("model", recipe.Model))
	return custom
}

// providerModelFactory abstracts the subset of provider.Factory
// methods needed by the recipe system. The concrete *provider.Factory
// satisfies this interface. Using an interface allows stubbing in
// tests.
type providerModelFactory interface {
	CreateModel(name string) (model.Model, error)
	CreateModelWithName(
		providerName string, modelName string,
	) (model.Model, error)
	CreateDefaultModel() (model.Model, error)
}

// ===========================================================================
// P3-B: Timeout control — per-recipe execution timeout
// ===========================================================================

// timeoutTool wraps a CallableTool with a per-call context deadline.
type timeoutTool struct {
	inner   tool.CallableTool
	timeout time.Duration
}

// newTimeoutTool creates a timeout wrapper. Returns the inner tool
// unwrapped if timeout is zero or negative.
func newTimeoutTool(
	inner tool.CallableTool,
	timeout time.Duration,
) tool.CallableTool {
	if timeout <= 0 {
		return inner
	}
	return &timeoutTool{inner: inner, timeout: timeout}
}

// Declaration delegates to the inner tool.
func (tt *timeoutTool) Declaration() *tool.Declaration {
	return tt.inner.Declaration()
}

// Call applies a context deadline before invoking the inner tool.
func (tt *timeoutTool) Call(
	ctx context.Context,
	jsonArgs []byte,
) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, tt.timeout)
	defer cancel()
	return tt.inner.Call(ctx, jsonArgs)
}

// ===========================================================================
// P3-C: Recipe discovery tool — list_recipes
// ===========================================================================

// recipeDiscoveryTool provides the `list_recipes` tool.
type recipeDiscoveryTool struct {
	decl    *tool.Declaration
	configs map[string]*RecipeConfig
}

// newRecipeDiscoveryTool creates the list_recipes discovery tool.
func newRecipeDiscoveryTool(
	recipes map[string]*RecipeConfig,
) *recipeDiscoveryTool {
	return &recipeDiscoveryTool{
		decl: &tool.Declaration{
			Name: "list_recipes",
			Description: "List all available recipe sub-agents " +
				"with their descriptions and parameter " +
				"schemas. Use this to discover which " +
				"specialized agents you can delegate tasks to.",
			InputSchema: &tool.Schema{
				Type:       "object",
				Properties: map[string]*tool.Schema{},
			},
		},
		configs: recipes,
	}
}

// Declaration returns the tool metadata.
func (dt *recipeDiscoveryTool) Declaration() *tool.Declaration {
	return dt.decl
}

// Call returns the list of available recipes as JSON.
func (dt *recipeDiscoveryTool) Call(
	_ context.Context,
	_ []byte,
) (any, error) {
	type paramInfo struct {
		Key         string   `json:"key"`
		Description string   `json:"description"`
		Type        string   `json:"type"`
		Required    bool     `json:"required"`
		Default     string   `json:"default,omitempty"`
		Options     []string `json:"options,omitempty"`
	}
	type recipeInfo struct {
		Name        string      `json:"name"`
		Description string      `json:"description"`
		Parameters  []paramInfo `json:"parameters,omitempty"`
		Tools       []string    `json:"tools,omitempty"`
		Temperature float64     `json:"temperature,omitempty"`
	}

	var result []recipeInfo
	for _, r := range dt.configs {
		ri := recipeInfo{
			Name:        r.Name,
			Description: r.Description,
			Tools:       r.Tools,
			Temperature: r.Temperature,
		}
		for _, p := range r.Parameters {
			ri.Parameters = append(ri.Parameters, paramInfo{
				Key:         p.Key,
				Description: p.Description,
				Type:        p.Type,
				Required:    p.Required,
				Default:     p.Default,
				Options:     p.Options,
			})
		}
		result = append(result, ri)
	}

	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal recipe list: %w", err)
	}
	return string(b), nil
}

// ===========================================================================
// P3-D: Hot-reload — file watching + reload_recipes tool
// ===========================================================================

// hotReloader watches the recipe directory for YAML file changes and
// triggers rebuilds of the tool set.
type hotReloader struct {
	mu      sync.RWMutex
	tools   []tool.Tool
	configs map[string]*RecipeConfig

	factory providerModelFactory
	dir     string

	// allowReloadTool is set by the rebuild callback so the
	// reload_recipes tool can trigger a refresh.
	rebuild func()

	watcher *fsnotify.Watcher
	closeCh chan struct{}
	wg      sync.WaitGroup
}

// newHotReloader creates a file watcher and starts background
// monitoring. Returns nil if file watching can't be initialized
// (non-fatal — recipes still work without hot-reload).
func newHotReloader(
	factory providerModelFactory,
	dir string,
	rebuild func(),
) *hotReloader {
	if dir == "" {
		return nil
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Warn("recipe: hot-reload disabled, fsnotify init failed",
			slog.String("error", err.Error()))
		return nil
	}

	if err := w.Add(dir); err != nil {
		w.Close()
		slog.Warn("recipe: hot-reload disabled, cannot watch dir",
			slog.String("dir", dir),
			slog.String("error", err.Error()))
		return nil
	}

	hr := &hotReloader{
		factory: factory,
		dir:     dir,
		rebuild: rebuild,
		watcher: w,
		closeCh: make(chan struct{}),
	}
	hr.wg.Add(1)
	go hr.watchLoop()

	slog.Info("recipe: hot-reload enabled",
		slog.String("dir", dir))

	return hr
}

// watchLoop processes file-system events in the background.
func (hr *hotReloader) watchLoop() {
	defer hr.wg.Done()

	const debounceInterval = 500 * time.Millisecond
	var timer *time.Timer

	for {
		select {
		case <-hr.closeCh:
			if timer != nil {
				timer.Stop()
			}
			return

		case event, ok := <-hr.watcher.Events:
			if !ok {
				return
			}
			if isYAMLFile(event.Name) {
				if event.Has(fsnotify.Create) ||
					event.Has(fsnotify.Write) ||
					event.Has(fsnotify.Remove) ||
					event.Has(fsnotify.Rename) {
					if timer != nil {
						timer.Stop()
					}
					timer = time.AfterFunc(
						debounceInterval,
						hr.rebuild,
					)
				}
			}

		case err, ok := <-hr.watcher.Errors:
			if !ok {
				return
			}
			slog.Warn("recipe: fsnotify error",
				slog.String("error", err.Error()))
		}
	}
}

// isYAMLFile checks if a path ends with .yaml or .yml.
func isYAMLFile(path string) bool {
	return len(path) > 4 &&
		(path[len(path)-5:] == ".yaml" ||
			path[len(path)-4:] == ".yml")
}

// Close stops the file watcher.
func (hr *hotReloader) Close() error {
	close(hr.closeCh)
	hr.wg.Wait()
	return hr.watcher.Close()
}

// ===========================================================================
// reloadTool — manual reload_recipes tool
// ===========================================================================

// reloadTool implements the `reload_recipes` tool that the main agent
// or user can call to manually trigger a recipe reload from disk.
type reloadTool struct {
	decl   *tool.Declaration
	reload func() string
}

// newReloadTool creates the reload_recipes tool.
func newReloadTool(reloadFn func() string) *reloadTool {
	return &reloadTool{
		decl: &tool.Declaration{
			Name: "reload_recipes",
			Description: "Reload recipe sub-agent definitions " +
				"from disk. Use this after editing recipe YAML " +
				"files to pick up changes without restarting.",
			InputSchema: &tool.Schema{
				Type:       "object",
				Properties: map[string]*tool.Schema{},
			},
		},
		reload: reloadFn,
	}
}

// Declaration returns the tool metadata.
func (rt *reloadTool) Declaration() *tool.Declaration {
	return rt.decl
}

// Call triggers a reload.
func (rt *reloadTool) Call(
	_ context.Context,
	_ []byte,
) (any, error) {
	if rt.reload == nil {
		return nil, fmt.Errorf("reload not available")
	}
	return rt.reload(), nil
}
