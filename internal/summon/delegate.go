// Package summon provides sub-agent task delegation for wukong.
// It wraps sub-agents as tools that the main agent can call,
// similar to Goose's Summon feature.
package summon

import (
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
)

// Delegate creates a sub-agent tool for task delegation.
// The sub-agent specializes in a particular domain and can be
// called by the parent agent to handle specific tasks.
type Delegate struct {
	name        string
	description string
	agent       agent.Agent
	tool        tool.Tool
}

// DelegateConfig holds configuration for a sub-agent delegate.
type DelegateConfig struct {
	Name        string
	Description string
	Instruction string
	Model       model.Model
	Tools       []tool.Tool
}

// NewDelegate creates a new sub-agent delegate.
func NewDelegate(cfg DelegateConfig) (*Delegate, error) {
	agentOpts := []llmagent.Option{
		llmagent.WithModel(cfg.Model),
		llmagent.WithInstruction(cfg.Instruction),
		llmagent.WithDescription(cfg.Description),
	}
	if len(cfg.Tools) > 0 {
		agentOpts = append(agentOpts,
			llmagent.WithTools(cfg.Tools),
		)
	}

	subAgent := llmagent.New(cfg.Name, agentOpts...)

	agentTool := agenttool.NewTool(
		subAgent,
		agenttool.WithSkipSummarization(false),
		agenttool.WithStreamInner(false),
		agenttool.WithResponseMode(
			agenttool.ResponseModeFinalOnly,
		),
	)

	return &Delegate{
		name:        cfg.Name,
		description: cfg.Description,
		agent:       subAgent,
		tool:        agentTool,
	}, nil
}

// Tool returns the agent tool for use by the parent agent.
func (d *Delegate) Tool() tool.Tool {
	return d.tool
}

// Agent returns the underlying sub-agent.
func (d *Delegate) Agent() agent.Agent {
	return d.agent
}

// Name returns the delegate name.
func (d *Delegate) Name() string {
	return d.name
}
