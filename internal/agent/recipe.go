// Package agent provides YAML-based recipe definitions for structured
// sub-agents. Recipes are loaded from .wukong/recipes/*.yaml and
// each defines a named sub-agent with its own instruction, tool list,
// and model configuration.
//
// Recipe YAML schema (base fields):
//
//	name: my-reviewer
//	description: "Expert code reviewer for security audits"
//	instruction: "You are an expert security code reviewer..."
//	model: "gpt-4o"         # optional, uses default if omitted
//	tools:                  # tool names to grant
//	  - file_read
//	  - code_search
//	temperature: 0.2         # optional (default: 0.3)
//	max_tokens: 2048         # optional (default: 1024)
//	max_iterations: 5        # optional (default: 3)
//	skip_summarization: false # optional
//
// Parameterized recipes (P0) add prompt and parameters fields:
//
//	name: code-reviewer
//	prompt: |
//	  Review the following {{.language}} code focusing on {{.focus}}:
//	  {{.code}}
//	parameters:
//	  - key: language
//	    type: select
//	    required: true
//	    options: [go, python, rust]
//
// Structured output (P0) constrains the final response:
//
//	response:
//	  json_schema: {type: object, ...}
//	  strict: true
//	  validate_output: true   # P1-B: validate returned JSON
//
// Retry (P1-B) wraps execution with exponential backoff:
//
//	retry:
//	  max_attempts: 3
//	  initial_wait: "1s"
//	  backoff_factor: 2.0
//	  max_wait: "30s"
//
// Sub-recipe composition (P1-A): recipes can reference other recipes
// in their tools list. Circular dependencies are detected at load time:
//
//	tools:
//	  - file_read
//	  - recipe-sub-reviewer   # or just "sub-reviewer"
//
// Recipe extends (P2-B): inherit fields from another recipe:
//
//	name: security-reviewer
//	extends: base-reviewer
//	instruction: "You are a security-focused reviewer."  # overrides
//
// Inline recipes (P2-A): define recipes directly in config.yaml:
//
//	agent:
//	  inline_recipes:
//	    - name: quick-helper
//	      description: "Quick helper"
//	      instruction: "You are a quick helper."
//
// Model override (P3-A): use a specific LLM model per recipe:
//
//	model: "gpt-4o"
//
// Timeout control (P3-B): limit execution time per recipe:
//
//	timeout: "30s"
//
// Recipe discovery (P3-C): list_recipes tool auto-registered.
// Hot-reload (P3-D): fsnotify file watcher auto-detects changes.
//
// Recipes are registered as tools callable by the main agent.
// The tool name is prefixed with "recipe-" (e.g., recipe-my-reviewer).
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/provider"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
)

// RecipeConfig is the YAML schema for a recipe sub-agent definition.
type RecipeConfig struct {
	// Name is the unique identifier for this recipe.
	// Used as the tool name prefix (recipe-<name>).
	Name string `yaml:"name"`
	// Description is shown to the main agent when deciding which
	// tool to call. Should describe when to use this sub-agent.
	Description string `yaml:"description"`
	// Instruction is the system prompt for the sub-agent.
	Instruction string `yaml:"instruction"`
	// Prompt is an optional parameterized task template rendered
	// with Go text/template syntax ({{.ParamKey}}). When set
	// together with Parameters, the rendered text is passed as the
	// sub-agent's user message. Without Parameters, Prompt is
	// ignored.
	Prompt string `yaml:"prompt"`
	// Parameters defines dynamic inputs the main agent supplies.
	// When non-empty, the recipe is wrapped in a recipeTool that
	// validates and renders parameters into Prompt before invoking
	// the sub-agent.
	Parameters []RecipeParameter `yaml:"parameters"`
	// Response constrains the sub-agent's final output to a JSON
	// schema when set. Uses the model-native response_format
	// mechanism.
	Response *RecipeResponseConfig `yaml:"response"`
	// Retry configures retry behavior for recipe execution. When
	// set, the recipe tool is wrapped with exponential backoff
	// retry. See RecipeRetryConfig for details.
	Retry *RecipeRetryConfig `yaml:"retry"`
	// Extends names another recipe to inherit fields from. The
	// child recipe's non-zero fields override the parent's. The
	// extends chain is resolved recursively; circular extends are
	// rejected at load time.
	Extends string `yaml:"extends"`
	// Model overrides the default model for this recipe (optional).
	Model string `yaml:"model"`
	// Tools lists the tool names to grant to this sub-agent.
	// If empty, no tools are granted (instruction-only).
	// May include recipe references: "recipe-<name>" or "<name>"
	// where <name> is a loaded recipe. Circular dependencies are
	// detected at load time.
	// Example: ["file_read", "code_search", "recipe-sub-reviewer"]
	Tools []string `yaml:"tools"`
	// Temperature controls LLM sampling randomness (0.0-2.0).
	// Default: 0.3.
	Temperature float64 `yaml:"temperature"`
	// MaxTokens is the maximum output tokens per LLM call.
	// Default: 1024.
	MaxTokens int `yaml:"max_tokens"`
	// MaxIterations is the maximum tool-calling iterations.
	// Default: 3.
	MaxIterations int `yaml:"max_iterations"`
	// SkipSummarization controls whether the sub-agent's
	// intermediate response is skipped when wrapping as tool.
	SkipSummarization bool `yaml:"skip_summarization"`
	// Timeout controls the maximum execution time for this recipe.
	// Must be a valid Go duration string (e.g. "30s", "5m",
	// "1h"). Zero means no timeout limit. When set, the recipe
	// tool is wrapped with context.WithTimeout.
	Timeout string `yaml:"timeout"`
}

// Defaults for recipe configuration.
const (
	defaultRecipeTemperature = 0.3
	defaultRecipeMaxTokens   = 1024
	defaultRecipeMaxIters    = 3
)

// RecipeToolSet loads YAML recipe definitions from a directory and
// registers them as callable agent tools. Supports hot-reload via
// file-system watching (P3-D).
type RecipeToolSet struct {
	mu        sync.Mutex
	tools     []tool.Tool
	subAgents []agent.Agent

	// P3-D: hot-reloader watches for recipe file changes.
	reloader *hotReloader
	// factory is retained for reload operations.
	factory providerModelFactory
	// agentCfg is retained for reload operations.
	agentCfg *config.AgentConfig
	// allToolsFn provides base tools for reload operations.
	allToolsFn func() []tool.Tool
}

// NewRecipeToolSet scans the configured recipe directory for .yaml
// files and inline recipe definitions from config, creates an
// LLMAgent per recipe, and wraps them as tools callable by the main
// agent.
//
// Loading phases:
//  1. All recipe configs (file-based + inline) loaded into a map.
//  2. Recipe extends chains resolved (P2-B).
//  3. Recipes topologically sorted by sub-recipe deps (P1-A).
//  4. Recipes built in dependency order, granting sub-recipe tools.
//  5. P1-B: Recipes with retry config wrapped with retry logic.
//  6. P3-A: Recipes with model field get per-recipe LLM models.
//  7. P3-B: Recipes with timeout field wrapped with timeout.
//  8. P3-C: list_recipes discovery tool registered.
//  9. P3-D: File watcher started for hot-reload.
//
// Returns nil if recipe_enabled is false or no recipes are found.
func NewRecipeToolSet(
	factory *provider.Factory,
	agentCfg *config.AgentConfig,
	allTools []tool.Tool,
) *RecipeToolSet {
	if !agentCfg.RecipeEnabled {
		return nil
	}

	// Phase 1: Load all recipe configs.
	recipes := make(map[string]*RecipeConfig)
	loadFileRecipes(recipes, agentCfg.RecipeDir)
	loadInlineRecipes(recipes, agentCfg.InlineRecipes)

	if len(recipes) == 0 {
		return nil
	}

	// Phase 2: Resolve extends (P2-B).
	resolved, err := resolveAllExtends(recipes)
	if err != nil {
		util.Logger.Warn("recipe: failed to resolve extends",
			slog.String("error", err.Error()))
		resolved = recipes
	}

	// Phase 3: Topological sort by sub-recipe deps (P1-A).
	recipeNames := make(map[string]bool, len(resolved))
	for name := range resolved {
		recipeNames[name] = true
	}
	order, err := topoSortRecipes(resolved)
	if err != nil {
		util.Logger.Error("recipe: dependency sort failed",
			slog.String("error", err.Error()))
		return nil
	}

	// P3-A: Default model — recipes without model field use this.
	defaultMdl, err := factory.CreateDefaultModel()
	if err != nil {
		util.Logger.Warn("recipe: failed to create model",
			slog.String("error", err.Error()))
		return nil
	}

	baseTools := make(map[string]tool.Tool)
	for _, t := range allTools {
		if decl := t.Declaration(); decl != nil {
			baseTools[decl.Name] = t
		}
	}

	// Phase 4: Build recipe tools in dependency order.
	recipeRegistry := make(map[string]tool.Tool)
	ts := &RecipeToolSet{}

	for _, name := range order {
		recipe, ok := resolved[name]
		if !ok {
			continue
		}

		// P3-A: Per-recipe model override.
		subMdl := createRecipeModel(
			factory, recipe, defaultMdl)

		grantedTools := mergeToolSets(
			baseTools, recipeRegistry, recipe, recipeNames)

		subAgent := createRecipeAgent(recipe, subMdl, grantedTools)
		ts.subAgents = append(ts.subAgents, subAgent)

		t := agenttool.NewTool(subAgent,
			agenttool.WithSkipSummarization(
				recipe.SkipSummarization),
			agenttool.WithStreamInner(false),
			agenttool.WithResponseMode(
				agenttool.ResponseModeFinalOnly,
			),
		)

		var finalTool tool.Tool = t
		if len(recipe.Parameters) > 0 && recipe.Prompt != "" {
			rt, rtErr := newRecipeTool(t, recipe)
			if rtErr != nil {
				util.Logger.Warn("recipe: skip parameterized wrapper",
					slog.String("name", recipe.Name),
					slog.String("error", rtErr.Error()))
			} else {
				finalTool = rt
			}
		}

		// P1-B: Retry wrapper.
		if recipe.Retry != nil {
			callTool, ok := finalTool.(tool.CallableTool)
			if !ok {
				util.Logger.Warn("recipe: retry config ignored",
					slog.String("name", recipe.Name))
			} else {
				validator := buildOutputValidator(
					recipe.Response)
				if recipe.Response == nil ||
					!recipe.Response.ValidateOutput {
					validator = nil
				}
				finalTool = newRetryTool(
					callTool, recipe.Retry, validator)
			}
		}

		// P3-B: Timeout wrapper.
		if recipe.Timeout != "" {
			callTool, ok := finalTool.(tool.CallableTool)
			if !ok {
				util.Logger.Warn("recipe: timeout config ignored",
					slog.String("name", recipe.Name))
			} else {
				td, tdErr := time.ParseDuration(
					recipe.Timeout)
				if tdErr != nil {
					util.Logger.Warn("recipe: invalid timeout",
						slog.String("name", recipe.Name),
						slog.String("timeout", recipe.Timeout),
						slog.String("error", tdErr.Error()))
				} else {
					finalTool = newTimeoutTool(
						callTool, td)
				}
			}
		}

		recipeRegistry[name] = finalTool
		ts.tools = append(ts.tools, finalTool)

		util.Logger.Info("recipe: registered",
			slog.String("name", recipe.Name),
			slog.Int("tools", len(recipe.Tools)),
			slog.Bool("retry", recipe.Retry != nil),
			slog.Bool("parameterized",
				len(recipe.Parameters) > 0 &&
					recipe.Prompt != ""),
			slog.Bool("model_override",
				recipe.Model != ""),
			slog.Bool("timeout",
				recipe.Timeout != ""),
		)
	}

	if len(ts.tools) == 0 {
		return nil
	}

	// P3-C: Recipe discovery tool.
	discoveryTool := newRecipeDiscoveryTool(resolved)
	ts.tools = append(ts.tools, discoveryTool)

	// P3-D: Hot-reload — file watcher + reload_recipes tool.
	reloadDir := resolveRecipeDir(agentCfg.RecipeDir)
	reloadTool := newReloadTool(func() string {
		if ts.Reload() {
			return "Recipes reloaded successfully."
		}
		return "Recipe reload failed — check logs for details."
	})
	ts.tools = append(ts.tools, reloadTool)

	// P4-B/C: Recipe execution stats tool.
	statsTool := newRecipeStatsTool()
	ts.tools = append(ts.tools, statsTool)

	ts.factory = factory
	ts.agentCfg = agentCfg
	ts.allToolsFn = func() []tool.Tool { return allTools }
	ts.reloader = newHotReloader(
		factory, reloadDir, func() { ts.Reload() },
	)

	util.Logger.Info("recipe: loaded recipes",
		slog.Int("count", len(ts.tools)))

	return ts
}

// Reload rebuilds all recipe tools from disk. Returns false if the
// reload fails. Thread-safe.
func (ts *RecipeToolSet) Reload() bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	util.Logger.Info("recipe: manual reload triggered")

	recipes := make(map[string]*RecipeConfig)
	loadFileRecipes(recipes, ts.agentCfg.RecipeDir)
	loadInlineRecipes(recipes, ts.agentCfg.InlineRecipes)
	if len(recipes) == 0 {
		util.Logger.Warn("recipe: reload found no recipes")
		return false
	}

	resolved, err := resolveAllExtends(recipes)
	if err != nil {
		util.Logger.Warn("recipe: reload extends failed",
			slog.String("error", err.Error()))
		resolved = recipes
	}

	recipeNames := make(map[string]bool, len(resolved))
	for name := range resolved {
		recipeNames[name] = true
	}
	order, err := topoSortRecipes(resolved)
	if err != nil {
		util.Logger.Error("recipe: reload sort failed",
			slog.String("error", err.Error()))
		return false
	}

	defaultMdl, err := ts.factory.CreateDefaultModel()
	if err != nil {
		util.Logger.Error("recipe: reload model creation failed",
			slog.String("error", err.Error()))
		return false
	}

	baseTools := make(map[string]tool.Tool)
	for _, t := range ts.allToolsFn() {
		if decl := t.Declaration(); decl != nil {
			baseTools[decl.Name] = t
		}
	}

	recipeRegistry := make(map[string]tool.Tool)
	var newTools []tool.Tool
	var newSubAgents []agent.Agent

	for _, name := range order {
		recipe, ok := resolved[name]
		if !ok {
			continue
		}

		subMdl := createRecipeModel(
			ts.factory, recipe, defaultMdl)

		grantedTools := mergeToolSets(
			baseTools, recipeRegistry, recipe, recipeNames)
		subAgent := createRecipeAgent(
			recipe, subMdl, grantedTools)
		newSubAgents = append(newSubAgents, subAgent)

		t := agenttool.NewTool(subAgent,
			agenttool.WithSkipSummarization(
				recipe.SkipSummarization),
			agenttool.WithStreamInner(false),
			agenttool.WithResponseMode(
				agenttool.ResponseModeFinalOnly,
			),
		)

		var finalTool tool.Tool = t
		if len(recipe.Parameters) > 0 && recipe.Prompt != "" {
			rt, rtErr := newRecipeTool(t, recipe)
			if rtErr != nil {
				util.Logger.Warn("recipe: skip parameterized wrapper",
					slog.String("name", recipe.Name),
					slog.String("error", rtErr.Error()))
			} else {
				finalTool = rt
			}
		}

		if recipe.Retry != nil {
			if callTool, ok := finalTool.(tool.CallableTool); ok {
				validator := buildOutputValidator(
					recipe.Response)
				if recipe.Response == nil ||
					!recipe.Response.ValidateOutput {
					validator = nil
				}
				finalTool = newRetryTool(
					callTool, recipe.Retry, validator)
			}
		}

		if recipe.Timeout != "" {
			if callTool, ok := finalTool.(tool.CallableTool); ok {
				if td, tdErr := time.ParseDuration(
					recipe.Timeout); tdErr == nil {
					finalTool = newTimeoutTool(
						callTool, td)
				}
			}
		}

		recipeRegistry[name] = finalTool
		newTools = append(newTools, finalTool)
	}

	discoveryTool := newRecipeDiscoveryTool(resolved)
	newTools = append(newTools, discoveryTool)
	reloadTool := newReloadTool(func() string {
		if ts.Reload() {
			return "Recipes reloaded successfully."
		}
		return "Recipe reload failed."
	})
	newTools = append(newTools, reloadTool)
	statsTool := newRecipeStatsTool()
	newTools = append(newTools, statsTool)

	ts.tools = newTools
	ts.subAgents = newSubAgents

	util.Logger.Info("recipe: reload completed",
		slog.Int("tools", len(newTools)))
	return true
}

// resolveRecipeDir resolves the recipe directory path to an absolute
// path, expanding ~ and falling back to .wukong/recipes/ relative to
// CWD if the configured path doesn't exist.
func resolveRecipeDir(recipeDir string) string {
	dir := recipeDir
	if dir == "" {
		dir = ".wukong/recipes/"
	}

	if len(dir) >= 2 && dir[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ".wukong/recipes/"
		}
		dir = filepath.Join(home, dir[2:])
	}
	dir = config.ResolvePath(dir)

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if wd, wdErr := os.Getwd(); wdErr == nil {
			alt := filepath.Join(wd, ".wukong", "recipes")
			if _, statErr := os.Stat(alt); statErr == nil {
				dir = alt
			}
		}
	}
	return dir
}

// loadFileRecipes loads recipe YAML files from the configured
// directory into the recipes map.
func loadFileRecipes(
	recipes map[string]*RecipeConfig,
	recipeDir string,
) {
	dir := recipeDir
	if dir == "" {
		dir = ".wukong/recipes/"
	}

	if len(dir) >= 2 && dir[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			util.Logger.Warn("recipe: cannot resolve home dir",
				slog.String("error", err.Error()))
			return
		}
		dir = filepath.Join(home, dir[2:])
	}
	dir = config.ResolvePath(dir)

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if wd, wdErr := os.Getwd(); wdErr == nil {
			alt := filepath.Join(wd, ".wukong", "recipes")
			if _, statErr := os.Stat(alt); statErr == nil {
				dir = alt
			}
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		util.Logger.Info("recipe: no recipes directory found",
			slog.String("dir", dir))
		return
	}

	var yamlFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		if strings.HasSuffix(name, ".yaml") ||
			strings.HasSuffix(name, ".yml") {
			yamlFiles = append(yamlFiles, e.Name())
		}
	}

	for _, fileName := range yamlFiles {
		recipe, err := loadRecipeFile(
			filepath.Join(dir, fileName))
		if err != nil {
			util.Logger.Warn("recipe: failed to load",
				slog.String("file", fileName),
				slog.String("error", err.Error()))
			continue
		}
		if recipe.Name == "" {
			util.Logger.Warn("recipe: skipping unnamed recipe",
				slog.String("file", fileName))
			continue
		}
		recipes[recipe.Name] = recipe
	}
}

// loadInlineRecipes loads recipe definitions from config's
// inline_recipes field (P2-A).
func loadInlineRecipes(
	recipes map[string]*RecipeConfig,
	inline []map[string]any,
) {
	for i, raw := range inline {
		recipe, err := loadInlineRecipe(raw)
		if err != nil {
			util.Logger.Warn("recipe: failed to load inline recipe",
				slog.Int("index", i),
				slog.String("error", err.Error()))
			continue
		}
		if recipe.Name == "" {
			util.Logger.Warn("recipe: skipping unnamed inline recipe",
				slog.Int("index", i))
			continue
		}
		recipes[recipe.Name] = recipe
		util.Logger.Info("recipe: loaded inline recipe",
			slog.String("name", recipe.Name))
	}
}

// loadInlineRecipe converts a config map to a RecipeConfig via
// YAML marshal/unmarshal round-trip.
func loadInlineRecipe(raw map[string]any) (*RecipeConfig, error) {
	data, err := yaml.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal inline recipe: %w", err)
	}
	var recipe RecipeConfig
	if err := yaml.Unmarshal(data, &recipe); err != nil {
		return nil, fmt.Errorf("parse inline recipe: %w", err)
	}
	return &recipe, nil
}

// mergeToolSets builds the tool set for a recipe's sub-agent,
// including base tools and available sub-recipe tools (P1-A).
// Only tools listed in recipe.Tools are included. Recipe tool
// references can be bare ("reviewer") or prefixed
// ("recipe-reviewer").
func mergeToolSets(
	baseTools map[string]tool.Tool,
	recipeRegistry map[string]tool.Tool,
	recipe *RecipeConfig,
	recipeNames map[string]bool,
) map[string]tool.Tool {
	result := make(map[string]tool.Tool)

	for _, name := range recipe.Tools {
		ref := normalizeRecipeRef(name, recipeNames)
		if ref != "" {
			if rt, ok := recipeRegistry[ref]; ok {
				result[name] = rt
			}
			continue
		}
		if t, ok := baseTools[name]; ok {
			result[name] = t
		}
	}

	return result
}

// Tools returns the recipe sub-agent tools.
func (ts *RecipeToolSet) Tools(_ context.Context) []tool.Tool {
	return ts.tools
}

// Name returns the tool set name.
func (ts *RecipeToolSet) Name() string {
	return "recipe_tools"
}

// Init initializes the tool set.
func (ts *RecipeToolSet) Init(_ context.Context) error {
	return nil
}

// Close releases resources including the hot-reload file watcher.
func (ts *RecipeToolSet) Close() error {
	if ts.reloader != nil {
		return ts.reloader.Close()
	}
	return nil
}

// loadRecipeFile reads and parses a single .yaml recipe file.
func loadRecipeFile(path string) (*RecipeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	var recipe RecipeConfig
	if err := yaml.Unmarshal(data, &recipe); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	return &recipe, nil
}

// createRecipeAgent builds an LLMAgent from a recipe configuration.
// Tool names in recipe.Tools are resolved against the provided
// toolNames map; only matching tools are granted.
func createRecipeAgent(
	recipe *RecipeConfig,
	mdl model.Model,
	toolNames map[string]tool.Tool,
) agent.Agent {
	temp := recipe.Temperature
	if temp <= 0 || temp > 2.0 {
		temp = defaultRecipeTemperature
	}

	maxTok := recipe.MaxTokens
	if maxTok <= 0 {
		maxTok = defaultRecipeMaxTokens
	}

	maxIter := recipe.MaxIterations
	if maxIter <= 0 {
		maxIter = defaultRecipeMaxIters
	}

	opts := []llmagent.Option{
		llmagent.WithModel(mdl),
		llmagent.WithDescription(recipe.Description),
		llmagent.WithInstruction(recipe.Instruction),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   util.IntPtr(maxTok),
			Temperature: util.Float64Ptr(temp),
			Stream:      false,
		}),
		llmagent.WithMaxLLMCalls(maxIter),
		llmagent.WithMaxToolIterations(maxIter),
	}

	// Grant only the tools specified in the recipe.
	if len(recipe.Tools) > 0 {
		var granted []tool.Tool
		for _, name := range recipe.Tools {
			if t, ok := toolNames[name]; ok {
				granted = append(granted, t)
			}
		}
		if len(granted) > 0 {
			opts = append(opts, llmagent.WithTools(granted))
		}
	}

	// Constrain final output to a JSON schema when configured.
	if recipe.Response != nil && len(recipe.Response.JSONSchema) > 0 {
		opts = append(opts, llmagent.WithStructuredOutputJSONSchema(
			recipe.Name,
			recipe.Response.JSONSchema,
			recipe.Response.Strict,
			recipe.Response.Description,
		))
	}

	name := "recipe-" + recipe.Name
	return llmagent.New(name, opts...)
}
