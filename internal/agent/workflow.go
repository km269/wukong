// Package agent provides multi-mode agent orchestration using
// trpc-agent-go's Graph, Chain, Parallel, and Cycle agents.
package agent

import (
	"context"
	"fmt"
	"reflect"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/provider"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/chainagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/cycleagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/parallelagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// WorkflowMode defines the execution mode for the agent.
type WorkflowMode string

const (
	WorkflowSingle          WorkflowMode = "single"
	WorkflowChain           WorkflowMode = "chain"
	WorkflowParallel        WorkflowMode = "parallel"
	WorkflowCycle           WorkflowMode = "cycle"
	WorkflowGraph           WorkflowMode = "graph"
	WorkflowTeamCoordinator WorkflowMode = "team_coordinator"
	WorkflowTeamSwarm       WorkflowMode = "team_swarm"
	WorkflowClaudeCode      WorkflowMode = "claude_code"
	WorkflowCodex           WorkflowMode = "codex"
	WorkflowDify            WorkflowMode = "dify"
)

// OrchestrationConfig holds configuration for multi-agent workflows.
type OrchestrationConfig struct {
	Mode          WorkflowMode
	SubAgents     []agent.Agent
	GraphSchema   *graph.Graph
	MaxIterations int
}

// WorkflowBuilder creates multi-agent workflows using
// trpc-agent-go's built-in agent types.
type WorkflowBuilder struct {
	factory   *provider.Factory
	cfg       *config.WukongConfig
	model     model.Model
	genConfig model.GenerationConfig
	tools     []tool.Tool
	toolSets  []tool.ToolSet
}

// NewWorkflowBuilder creates a new workflow builder.
func NewWorkflowBuilder(
	factory *provider.Factory,
	cfg *config.WukongConfig,
	tools []tool.Tool,
	toolSets []tool.ToolSet,
) (*WorkflowBuilder, error) {
	mdl, err := factory.CreateDefaultModel()
	if err != nil {
		return nil, fmt.Errorf("create model: %w", err)
	}

	return &WorkflowBuilder{
		factory:   factory,
		cfg:       cfg,
		model:     mdl,
		genConfig: provider.GetDefaultGenerationConfig(&cfg.Agent),
		tools:     tools,
		toolSets:  toolSets,
	}, nil
}

// Build creates an agent based on the specified workflow mode.
func (b *WorkflowBuilder) Build(
	ctx context.Context, wfCfg *OrchestrationConfig,
) (agent.Agent, error) {
	if wfCfg == nil {
		wfCfg = &OrchestrationConfig{Mode: WorkflowSingle}
	}

	switch wfCfg.Mode {
	case WorkflowSingle:
		return b.buildSingleAgent()
	case WorkflowChain:
		return b.buildChainAgent(wfCfg)
	case WorkflowParallel:
		return b.buildParallelAgent(wfCfg)
	case WorkflowCycle:
		return b.buildCycleAgent(wfCfg)
	case WorkflowGraph:
		return b.buildGraphAgent(wfCfg)
	case WorkflowTeamCoordinator, WorkflowTeamSwarm, WorkflowClaudeCode, WorkflowCodex, WorkflowDify:
		return b.buildTeamAgent(ctx, wfCfg)
	default:
		return nil, fmt.Errorf(
			"unsupported workflow mode: %s", wfCfg.Mode,
		)
	}
}

// buildSingleAgent creates a standard LLMAgent.
func (b *WorkflowBuilder) buildSingleAgent() (agent.Agent, error) {
	opts := []llmagent.Option{
		llmagent.WithModel(b.model),
		llmagent.WithGenerationConfig(b.genConfig),
		llmagent.WithDescription("Wukong AI Agent - Single mode"),
		llmagent.WithInstruction(buildBaseInstruction("")),
		llmagent.WithAddCurrentTime(true),
	}

	if len(b.tools) > 0 {
		opts = append(opts, llmagent.WithTools(b.tools))
	}
	if len(b.toolSets) > 0 {
		opts = append(opts, llmagent.WithToolSets(b.toolSets))
	}
	if b.cfg.Agent.MaxLLMCalls > 0 {
		opts = append(opts,
			llmagent.WithMaxLLMCalls(b.cfg.Agent.MaxLLMCalls))
	}
	if b.cfg.Agent.ParallelTools {
		opts = append(opts,
			llmagent.WithEnableParallelTools(true))
	}
	if b.cfg.Agent.ContextCompaction {
		opts = append(opts,
			llmagent.WithEnableContextCompaction(true),
		)
	}
	// Preload memory for cross-session awareness
	opts = append(opts, llmagent.WithPreloadMemory(10))

	return llmagent.New("wukong-single", opts...), nil
}

// buildChainAgent creates a ChainAgent that executes sub-agents
// sequentially. If SubAgents are provided via config, they are used;
// otherwise default planner/executor/reviewer agents are created.
func (b *WorkflowBuilder) buildChainAgent(
	wfCfg *OrchestrationConfig,
) (agent.Agent, error) {
	subAgents := wfCfg.SubAgents
	if len(subAgents) == 0 {
		// Use config-defined sub-agents if available, otherwise defaults
		subAgents = b.buildSubAgentsFromConfig("chain")
	}
	if len(subAgents) == 0 {
		return nil, fmt.Errorf(
			"chain workflow requires at least one sub-agent")
	}

	return chainagent.New("wukong-chain",
		chainagent.WithSubAgents(subAgents),
	), nil
}

// buildSubAgentsFromConfig creates sub-agents from the workflow config's
// sub_agents definitions, with tool filtering based on allowed_tools.
func (b *WorkflowBuilder) buildSubAgentsFromConfig(
	mode string,
) []agent.Agent {
	cfgSubAgents := b.cfg.Workflow.SubAgents
	if len(cfgSubAgents) == 0 {
		// Fall back to default agents for this mode
		return b.defaultSubAgents(mode)
	}

	var agents []agent.Agent
	for _, saCfg := range cfgSubAgents {
		ag := b.createSubAgentFromConfig(saCfg)
		if ag != nil {
			agents = append(agents, ag)
		}
	}
	return agents
}

// createSubAgentFromConfig creates a single sub-agent from config
// with optional tool filtering.
func (b *WorkflowBuilder) createSubAgentFromConfig(
	cfg config.WorkflowSubAgentConfig,
) agent.Agent {
	opts := []llmagent.Option{
		llmagent.WithModel(b.model),
		llmagent.WithDescription(
			fmt.Sprintf("Wukong %s sub-agent", cfg.Name)),
		llmagent.WithInstruction(cfg.Instruction),
		llmagent.WithAddCurrentTime(true),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:      false,
			MaxTokens:   intPtr(2048),
			Temperature: float64Ptr(0.3),
		}),
		llmagent.WithMaxLLMCalls(5),
	}

	// Apply tool permissions: if AllTools is false and AllowedTools
	// is specified, filter to only those tools.
	if !cfg.AllTools && len(cfg.AllowedTools) > 0 {
		filtered := b.filterTools(cfg.AllowedTools)
		if len(filtered) > 0 {
			opts = append(opts, llmagent.WithTools(filtered))
		}
	} else if len(b.tools) > 0 {
		opts = append(opts, llmagent.WithTools(b.tools))
	}
	if len(b.toolSets) > 0 {
		opts = append(opts, llmagent.WithToolSets(b.toolSets))
	}

	return llmagent.New(cfg.Name, opts...)
}

// filterTools returns only tools whose names are in the allowed list.
func (b *WorkflowBuilder) filterTools(
	allowed []string,
) []tool.Tool {
	allowedSet := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = true
	}
	var filtered []tool.Tool
	for _, t := range b.tools {
		decl := t.Declaration()
		if decl != nil && allowedSet[decl.Name] {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// defaultSubAgents returns the built-in default agents for each mode.
func (b *WorkflowBuilder) defaultSubAgents(mode string) []agent.Agent {
	switch mode {
	case "chain":
		planner := b.createSpecializedAgent(
			"planner",
			"You are a planning specialist. "+
				"Analyze the user's request and create a "+
				"step-by-step plan to accomplish it. "+
				"Be specific about each step and the tools needed.",
		)
		executor := b.createSpecializedAgent(
			"executor",
			"You are an execution specialist. "+
				"Follow the plan provided and execute each "+
				"step using the available tools. "+
				"Report results for each step.",
		)
		reviewer := b.createSpecializedAgent(
			"reviewer",
			"You are a quality reviewer. "+
				"Review the execution results against the "+
				"original plan. Verify completeness and "+
				"correctness. Provide a final summary.",
		)
		return []agent.Agent{planner, executor, reviewer}
	case "parallel":
		codeAgent := b.createSpecializedAgent(
			"code-analyzer",
			"You are a code analysis specialist. "+
				"Analyze from a technical perspective: "+
				"architecture, patterns, performance, security.",
		)
		docAgent := b.createSpecializedAgent(
			"doc-analyzer",
			"You are a documentation specialist. "+
				"Analyze from a documentation perspective: "+
				"clarity, completeness, examples, API docs.",
		)
		testAgent := b.createSpecializedAgent(
			"test-analyzer",
			"You are a testing specialist. "+
				"Analyze from a testing perspective: "+
				"coverage, edge cases, integration tests, "+
				"test quality.",
		)
		return []agent.Agent{codeAgent, docAgent, testAgent}
	}
	return nil
}

// buildParallelAgent creates a ParallelAgent that executes multiple
// sub-agents concurrently.
func (b *WorkflowBuilder) buildParallelAgent(
	wfCfg *OrchestrationConfig,
) (agent.Agent, error) {
	subAgents := wfCfg.SubAgents
	if len(subAgents) == 0 {
		subAgents = b.buildSubAgentsFromConfig("parallel")
	}
	if len(subAgents) == 0 {
		return nil, fmt.Errorf(
			"parallel workflow requires at least one sub-agent")
	}

	return parallelagent.New("wukong-parallel",
		parallelagent.WithSubAgents(subAgents),
	), nil
}

// buildCycleAgent creates a CycleAgent that runs a loop until
// escalation or max iterations.
//
// Built-in cycle modes:
//   - "default": planner → executor loop
//   - "code_review": code_generator → code_reviewer → modify loop
//
// The code_review cycle is particularly useful for autonomous code
// improvement: the generator writes code, the reviewer finds issues,
// and the generator fixes them until the reviewer approves.
func (b *WorkflowBuilder) buildCycleAgent(
	wfCfg *OrchestrationConfig,
) (agent.Agent, error) {
	// Determine cycle mode from config
	mode := b.cfg.Workflow.CycleMode
	if mode == "" {
		mode = "default"
	}

	var subAgents []agent.Agent
	var maxIter int
	var escalationFn func(evt *event.Event) bool

	switch mode {
	case "code_review":
		generator := b.createSpecializedAgent(
			"code-generator",
			"You are an expert code generator. "+
				"Write clean, correct, well-documented code "+
				"that meets the requirements. "+
				"Use available tools to create and modify files. "+
				"After writing code, respond with a summary "+
				"of what you created.",
		)
		reviewer := b.createSpecializedAgent(
			"code-reviewer",
			"You are a strict code reviewer. "+
				"Examine the generated code for: "+
				"correctness, edge cases, security issues, "+
				"performance problems, style violations, "+
				"and missing error handling. "+
				"Be specific about what needs to change and why. "+
				"If the code is already perfect, respond with "+
				"'CODE_APPROVED'. Otherwise, list specific "+
				"improvements needed.",
		)
		subAgents = []agent.Agent{generator, reviewer}
		maxIter = wfCfg.MaxIterations
		if maxIter <= 0 {
			maxIter = 5
		}
		escalationFn = func(evt *event.Event) bool {
			if evt.Response != nil &&
				len(evt.Response.Choices) > 0 {
				content := evt.Response.Choices[0].
					Message.Content
				return containsKeyword(content, "CODE_APPROVED")
			}
			return false
		}

	default:
		planner := b.createSpecializedAgent(
			"cycle-planner",
			"You are a planning agent in a cycle workflow. "+
				"Each iteration, assess the current state and "+
				"decide the next action. If the task is complete, "+
				"respond with 'TASK_COMPLETE'.",
		)
		executor := b.createSpecializedAgent(
			"cycle-executor",
			"You are an execution agent in a cycle workflow. "+
				"Execute the planned action using available tools. "+
				"Report the result clearly.",
		)
		subAgents = []agent.Agent{planner, executor}
		maxIter = wfCfg.MaxIterations
		if maxIter <= 0 {
			maxIter = 10
		}
		escalationFn = func(evt *event.Event) bool {
			if evt.Response != nil &&
				len(evt.Response.Choices) > 0 {
				content := evt.Response.Choices[0].
					Message.Content
				return containsKeyword(
					content, "TASK_COMPLETE",
				)
			}
			return false
		}
	}

	return cycleagent.New("wukong-cycle",
		cycleagent.WithSubAgents(subAgents),
		cycleagent.WithMaxIterations(maxIter),
		cycleagent.WithEscalationFunc(escalationFn),
	), nil
}

// buildGraphAgent creates a GraphAgent with conditional routing.
func (b *WorkflowBuilder) buildGraphAgent(
	wfCfg *OrchestrationConfig,
) (agent.Agent, error) {
	if wfCfg.GraphSchema != nil {
		return graphagent.New(
			"wukong-graph", wfCfg.GraphSchema,
		)
	}

	// Build a default graph using StateGraph builder
	schema := graph.NewStateSchema().
		AddField("last_response", graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.StateReducer(func(existing, update any) any {
				return update
			}),
		})

	analyzer := b.createSpecializedAgent(
		"graph-analyzer",
		"You are an analysis agent. "+
			"Classify the user's request into one of: "+
			"code_task, search_task, question. "+
			"Respond with only the category name.",
	)
	codeRunner := b.createSpecializedAgent(
		"graph-code",
		"You are a code specialist. "+
			"Write, review, or explain code as requested. "+
			"Use available tools to read/write files.",
	)
	searcher := b.createSpecializedAgent(
		"graph-search",
		"You are a search specialist. "+
			"Search the codebase, web, or knowledge base "+
			"to find relevant information.",
	)
	answerer := b.createSpecializedAgent(
		"graph-answer",
		"You are a general Q&A specialist. "+
			"Answer questions concisely and accurately.",
	)
	reviewer := b.createSpecializedAgent(
		"graph-reviewer",
		"You are a final review agent. "+
			"Synthesize the results and provide a "+
			"comprehensive response to the user.",
	)

	sg := graph.NewStateGraph(schema)

	// Add agent nodes
	sg.AddAgentNode("analyze")
	sg.AddAgentNode("code")
	sg.AddAgentNode("search")
	sg.AddAgentNode("answer")
	sg.AddAgentNode("review")

	// Define conditional routing from analyze
	pathMap := map[string]string{
		"code_task":   "code",
		"search_task": "search",
		"question":    "answer",
	}
	sg.AddConditionalEdges("analyze", func(
		ctx context.Context, state graph.State,
	) (string, error) {
		lastResp, ok := state["last_response"].(string)
		if !ok {
			return "answer", nil
		}
		switch {
		case containsKeyword(lastResp, "code_task"):
			return "code_task", nil
		case containsKeyword(lastResp, "search_task"):
			return "search_task", nil
		default:
			return "question", nil
		}
	}, pathMap)

	// All paths converge to review
	sg.AddEdge("code", "review")
	sg.AddEdge("search", "review")
	sg.AddEdge("answer", "review")

	sg.SetEntryPoint("analyze")
	sg.SetFinishPoint("review")

	compiledGraph, err := sg.Compile()
	if err != nil {
		return nil, fmt.Errorf("compile graph: %w", err)
	}

	return graphagent.New("wukong-graph", compiledGraph,
		graphagent.WithSubAgents([]agent.Agent{
			analyzer, codeRunner, searcher, answerer, reviewer,
		}),
	)
}

// createSpecializedAgent creates a sub-agent with a specific instruction.
func (b *WorkflowBuilder) createSpecializedAgent(
	name, instruction string,
) agent.Agent {
	opts := []llmagent.Option{
		llmagent.WithModel(b.model),
		llmagent.WithDescription(
			fmt.Sprintf("Wukong %s sub-agent", name)),
		llmagent.WithInstruction(instruction),
		llmagent.WithAddCurrentTime(true),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:      false,
			MaxTokens:   intPtr(2048),
			Temperature: float64Ptr(0.3),
		}),
		llmagent.WithMaxLLMCalls(5),
	}

	if len(b.tools) > 0 {
		opts = append(opts, llmagent.WithTools(b.tools))
	}
	if len(b.toolSets) > 0 {
		opts = append(opts, llmagent.WithToolSets(b.toolSets))
	}
	if b.cfg.Agent.ContextCompaction {
		opts = append(opts,
			llmagent.WithEnableContextCompaction(true),
		)
		if b.cfg.Agent.ContextCompactionToolResultMaxTokens > 0 {
			opts = append(opts,
				llmagent.WithContextCompactionToolResultMaxTokens(
					b.cfg.Agent.ContextCompactionToolResultMaxTokens,
				),
			)
		}
		if b.cfg.Agent.ContextCompactionOversizedMaxTokens > 0 {
			opts = append(opts,
				llmagent.WithContextCompactionOversizedToolResultMaxTokens(
					b.cfg.Agent.ContextCompactionOversizedMaxTokens,
				),
			)
		}
	}

	return llmagent.New(name, opts...)
}

// containsKeyword checks if a string contains a keyword
// (case-insensitive).
func containsKeyword(s, keyword string) bool {
	if keyword == "" {
		return false
	}
	if len(keyword) > len(s) {
		return false
	}
	for i := 0; i <= len(s)-len(keyword); i++ {
		match := true
		for j := 0; j < len(keyword); j++ {
			c1 := s[i+j]
			c2 := keyword[j]
			if c1 >= 'A' && c1 <= 'Z' {
				c1 += 32
			}
			if c2 >= 'A' && c2 <= 'Z' {
				c2 += 32
			}
			if c1 != c2 {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func intPtr(i int) *int       { return &i }
func float64Ptr(f float64) *float64 { return &f }

// buildTeamAgent creates a Team-based agent (coordinator, swarm, or
// Claude Code mode). Uses the TeamBuilder for construction.
func (b *WorkflowBuilder) buildTeamAgent(
	ctx context.Context, wfCfg *OrchestrationConfig,
) (agent.Agent, error) {
	tb, err := NewTeamBuilder(b.factory, b.cfg, b.tools, b.toolSets)
	if err != nil {
		return nil, fmt.Errorf("create team builder: %w", err)
	}

	switch wfCfg.Mode {
	case WorkflowTeamCoordinator:
		return tb.BuildCoordinatorTeam(ctx)
	case WorkflowTeamSwarm:
		return tb.BuildSwarm(ctx)
	case WorkflowClaudeCode:
		return tb.BuildClaudeCode()
	case WorkflowCodex:
		return tb.BuildCodex()
	case WorkflowDify:
		return BuildDify(ctx, b.cfg)
	default:
		return nil, fmt.Errorf("unknown team mode: %s", wfCfg.Mode)
	}
}

// buildBaseInstruction returns the base system instruction.
// When templateText is non-empty, it is used as a prefix
// before the hardcoded fallback instruction.
func buildBaseInstruction(templateText string) string {
	if templateText != "" {
		return templateText + "\n\n" +
			"You are Wukong, a helpful and capable AI agent. " +
			"Respect the instructions above when using tools."
	}
	return "You are Wukong, a helpful and capable AI agent. " +
		"You have access to various tools that let you " +
		"interact with the user's system. " +
		"Use tools proactively to complete tasks. " +
		"Break complex tasks into smaller steps. " +
		"Prefer targeted edits over full rewrites."
}
