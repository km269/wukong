// Package agent provides YAML-based recipe definitions for structured
// sub-agents. Recipes are loaded from .wukong/recipes/*.yaml and
// each defines a named sub-agent with its own instruction, tool list,
// and model configuration.
//
// Recipe YAML schema:
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
	// Model overrides the default model for this recipe (optional).
	Model string `yaml:"model"`
	// Tools lists the tool names to grant to this sub-agent.
	// If empty, no tools are granted (instruction-only).
	// Example: ["file_read", "code_search", "directory_list"]
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
}

// Defaults for recipe configuration.
const (
	defaultRecipeTemperature = 0.3
	defaultRecipeMaxTokens   = 1024
	defaultRecipeMaxIters    = 3
)

// RecipeToolSet loads YAML recipe definitions from a directory and
// registers them as callable agent tools.
type RecipeToolSet struct {
	tools     []tool.Tool
	subAgents []agent.Agent
	inited    bool
	closed    bool
}

// NewRecipeToolSet scans the configured recipe directory for .yaml files,
// creates an LLMAgent per recipe, and wraps them as tools callable by
// the main agent.
//
// Returns nil if recipe_enabled is false or the directory is empty.
func NewRecipeToolSet(
	factory *provider.Factory,
	agentCfg *config.AgentConfig,
	allTools []tool.Tool,
) *RecipeToolSet {
	if !agentCfg.RecipeEnabled {
		return nil
	}

	dir := agentCfg.RecipeDir
	if dir == "" {
		dir = ".wukong/recipes/"
	}

	// Expand ~ and resolve.
	if len(dir) >= 2 && dir[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			util.Logger.Warn("recipe: cannot resolve home dir")
			return nil
		}
		dir = filepath.Join(home, dir[2:])
	}
	dir = config.ResolvePath(dir)

	// Try finding the dir relative to cwd if absolute doesn't exist.
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if wd, err := os.Getwd(); err == nil {
			alt := filepath.Join(wd, ".wukong", "recipes")
			if _, err := os.Stat(alt); err == nil {
				dir = alt
			}
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		util.Logger.Info("recipe: no recipes directory found",
			"dir", dir)
		return nil
	}

	// Collect .yaml / .yml files sorted by name.
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

	if len(yamlFiles) == 0 {
		return nil
	}

	mdl, err := factory.CreateDefaultModel()
	if err != nil {
		util.Logger.Warn("recipe: failed to create model",
			"error", err.Error())
		return nil
	}

	// Build a tool name set for filtering.
	toolNames := make(map[string]tool.Tool)
	for _, t := range allTools {
		if decl := t.Declaration(); decl != nil {
			toolNames[decl.Name] = t
		}
	}

	ts := &RecipeToolSet{}

	for _, fileName := range yamlFiles {
		recipe, err := loadRecipeFile(
			filepath.Join(dir, fileName))
		if err != nil {
			util.Logger.Warn("recipe: failed to load",
				"file", fileName, "error", err.Error())
			continue
		}
		if recipe.Name == "" {
			util.Logger.Warn("recipe: skipping unnamed recipe",
				"file", fileName)
			continue
		}

		subAgent := createRecipeAgent(recipe, mdl, toolNames)
		ts.subAgents = append(ts.subAgents, subAgent)

		t := agenttool.NewTool(subAgent,
			agenttool.WithSkipSummarization(
				recipe.SkipSummarization),
			agenttool.WithStreamInner(false),
			agenttool.WithResponseMode(
				agenttool.ResponseModeFinalOnly,
			),
		)
		ts.tools = append(ts.tools, t)

		util.Logger.Info("recipe: registered",
			slog.String("name", recipe.Name),
			slog.Int("tools", len(recipe.Tools)),
		)
	}

	if len(ts.tools) == 0 {
		return nil
	}

	util.Logger.Info("recipe: loaded recipes",
		slog.Int("count", len(ts.tools)),
		slog.String("dir", dir))

	return ts
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
	ts.inited = true
	return nil
}

// Close releases resources.
func (ts *RecipeToolSet) Close() error {
	ts.closed = true
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
			MaxTokens:   intPtr(maxTok),
			Temperature: float64Ptr(temp),
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

	name := "recipe-" + recipe.Name
	return llmagent.New(name, opts...)
}
