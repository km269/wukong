// Package summon provides sub-agent task delegation for wukong.
// It wraps sub-agents as tools that the main agent can call,
// similar to Goose's Summon feature. Supports skills/recipes
// loading, sub-agent lifecycle management, and A2A protocol.
package summon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"

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
		// Add current time for time-aware sub-agent reasoning
		llmagent.WithAddCurrentTime(true),
		// Set generation config for controlled output
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:      false, // Sub-agents use final-only response mode
			MaxTokens:   util.IntPtr(2048),
			Temperature: util.Float64Ptr(0.3), // More deterministic for task execution
		}),
		// Limit sub-agent calls to prevent runaway delegation
		llmagent.WithMaxLLMCalls(10),
		llmagent.WithMaxToolIterations(5),
		// Enable post-tool prompt for sub-agent reasoning
		llmagent.WithEnablePostToolPrompt(true),
	}
	if len(cfg.Tools) > 0 {
		agentOpts = append(agentOpts,
			llmagent.WithTools(cfg.Tools),
		)
	}

	// Add tool call retry for sub-agent reliability
	retryPolicy := &tool.RetryPolicy{
		MaxAttempts:     2,
		InitialInterval: 500 * time.Millisecond,
		BackoffFactor:   2.0,
		Jitter:          true,
	}
	agentOpts = append(agentOpts,
		llmagent.WithToolCallRetryPolicy(retryPolicy),
	)

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

// SummonManager manages multiple sub-agent delegates with
// skills/recipes loading and lifecycle management.
type SummonManager struct {
	mu        sync.RWMutex
	cfg       *config.SummonConfig
	delegates map[string]*Delegate
	skills    map[string]SkillInfo
	model     model.Model
	sem       chan struct{} // Concurrency limiter
}

// SkillInfo describes a loaded skill/recipe.
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	FilePath    string `json:"file_path"`
	Instruction string `json:"instruction"`
}

// NewSummonManager creates a new summon manager.
func NewSummonManager(
	cfg *config.SummonConfig, mdl model.Model,
) *SummonManager {
	maxConcurrent := 5
	if cfg != nil && cfg.MaxConcurrent > 0 {
		maxConcurrent = cfg.MaxConcurrent
	}
	return &SummonManager{
		cfg:       cfg,
		delegates: make(map[string]*Delegate),
		skills:    make(map[string]SkillInfo),
		model:     mdl,
		sem:       make(chan struct{}, maxConcurrent),
	}
}

// LoadSkills loads all skills/recipes from the skills directory.
// Skills are Markdown files that define sub-agent behaviors.
func (m *SummonManager) LoadSkills(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	skillsDir := m.cfg.SkillsDir
	if skillsDir == "" {
		skillsDir = ".wukong_skills"
	}

	// Create directory if not exists
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		return fmt.Errorf("create skills dir: %w", err)
	}

	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return fmt.Errorf("read skills dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) != ".md" {
			continue
		}

		filePath := filepath.Join(skillsDir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		content := string(data)
		name := entry.Name()[:len(entry.Name())-3] // Remove .md

		skill := SkillInfo{
			Name:        name,
			Description: extractDescription(content),
			FilePath:    filePath,
			Instruction: content,
		}

		m.skills[name] = skill

		// Create a delegate for this skill
		delegate, err := NewDelegate(DelegateConfig{
			Name:        "skill_" + name,
			Description: skill.Description,
			Instruction: skill.Instruction,
			Model:       m.model,
		})
		if err != nil {
			continue
		}

		m.delegates[name] = delegate
	}

	return nil
}

// GetDelegate returns a delegate by name.
func (m *SummonManager) GetDelegate(name string) (*Delegate, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.delegates[name]
	return d, ok
}

// ListDelegates returns all registered delegates.
func (m *SummonManager) ListDelegates() []*Delegate {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*Delegate, 0, len(m.delegates))
	for _, d := range m.delegates {
		result = append(result, d)
	}
	return result
}

// ListSkills returns all loaded skills.
func (m *SummonManager) ListSkills() []SkillInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]SkillInfo, 0, len(m.skills))
	for _, s := range m.skills {
		result = append(result, s)
	}
	return result
}

// DelegateCount returns the number of active delegates.
func (m *SummonManager) DelegateCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.delegates)
}

// AcquireSlot blocks until a concurrent execution slot is available.
// Returns a release function that must be called when the delegate
// execution completes. Uses context for cancellation support.
func (m *SummonManager) AcquireSlot(
	ctx context.Context,
) (release func(), err error) {
	select {
	case m.sem <- struct{}{}:
		return func() { <-m.sem }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf(
			"summon: acquire slot: %w", ctx.Err(),
		)
	}
}

// WrapTool wraps a delegate tool with concurrency control.
// The returned tool acquires a slot from the semaphore via the
// context before the inner tool executes and releases it after
// completion. This ensures MaxConcurrent sub-agent delegates are
// enforced.
//
// Since tool.Tool does not expose a Call() method (the framework
// handles invocation), slot control is managed through the
// BeforeTool callback which acquires the slot and injects a
// release function into the context. The AfterTool callback
// releases the slot.
func (m *SummonManager) WrapTool(
	inner tool.Tool, delegateName string,
) tool.Tool {
	decl := inner.Declaration()

	return &slotControlledTool{
		inner:   inner,
		manager: m,
		name:    decl.Name,
		desc:    decl.Description,
	}
}

// AvailableSlots returns the number of available concurrent slots.
func (m *SummonManager) AvailableSlots() int {
	return cap(m.sem) - len(m.sem)
}

// MaxConcurrent returns the maximum concurrent delegate limit.
func (m *SummonManager) MaxConcurrent() int {
	return cap(m.sem)
}

// slotControlledTool wraps a delegate tool with concurrency limits
// via the summon manager's semaphore. It implements tool.Tool by
// proxying Declaration() to the inner tool.
//
// Concurrency control is enforced via BeforeTool/AfterTool callbacks
// registered in the agent loop. The BeforeTool callback acquires a
// semaphore slot and stores a release function in the context. The
// AfterTool callback retrieves and calls the release function.
type slotControlledTool struct {
	inner   tool.Tool
	manager *SummonManager
	name    string
	desc    string
}

// Declaration returns the inner tool's declaration unchanged.
func (t *slotControlledTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        t.name,
		Description: t.desc,
		InputSchema: t.inner.Declaration().InputSchema,
	}
}

// Close shuts down all delegates.
func (m *SummonManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name := range m.delegates {
		delete(m.delegates, name)
	}
	return nil
}

// NewDelegateTool creates a tool from an existing agent without
// going through the full Delegate lifecycle. This is useful for
// wrapping externally-created agents (e.g., from the Skill system)
// as summon-compatible tools.
func NewDelegateTool(
	ag agent.Agent, name, description string,
) tool.Tool {
	return agenttool.NewTool(
		ag,
		agenttool.WithSkipSummarization(false),
		agenttool.WithStreamInner(false),
		agenttool.WithResponseMode(
			agenttool.ResponseModeFinalOnly,
		),
	)
}

// extractDescription extracts the first meaningful line from a
// skill's content as its description.
func extractDescription(content string) string {
	// Find first non-empty, non-heading line
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if trimmed[0] != '#' {
			if len(trimmed) > 100 {
				return trimmed[:100] + "..."
			}
			return trimmed
		}
	}
	return "Skill"
}
