// Package skill provides Agent Skill system integration using
// trpc-agent-go's skill package. Skills are reusable, composable
// agent workflows defined in SKILL.md files within a skill directory.
//
// A SKILL.md file follows the trpc-agent-go skill format:
// - Optional YAML front matter with name/description metadata
// - Markdown body with workflow instructions
// - Optional doc files for supplementary documentation
//
// Skills are stored as directories under the skills root, with each
// directory containing a SKILL.md file.
package skill

import (
	"context"
	"fmt"
	"os"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	agentskill "trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Manager manages the lifecycle of agent skills, including
// discovery, loading, and agent creation from skills.
type Manager struct {
	cfg        config.SkillConfig
	repository *agentskill.FSRepository
	summaries  []agentskill.Summary
}

// NewManager creates a new skill manager.
func NewManager(cfg config.SkillConfig) *Manager {
	return &Manager{cfg: cfg}
}

// Initialize discovers and indexes all SKILL.md files from the
// configured skills directory using trpc-agent-go's FSRepository.
func (m *Manager) Initialize(ctx context.Context) error {
	if !m.cfg.Enabled {
		util.Logger.Debug("skill system disabled")
		return nil
	}

	skillsDir := m.cfg.SkillsDir
	if skillsDir == "" {
		skillsDir = ".wukong_skills"
	}

	// Ensure skills directory exists
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		return fmt.Errorf("create skills dir: %w", err)
	}

	// Create FS repository backed by the skills directory
	repo, err := agentskill.NewFSRepository(skillsDir)
	if err != nil {
		return fmt.Errorf("create skill repository: %w", err)
	}

	m.repository = repo
	m.summaries = repo.Summaries()

	util.Logger.Info("skill system initialized",
		"skills_dir", skillsDir,
		"skills_found", len(m.summaries),
	)

	return nil
}

// GetSkill retrieves a full skill by name from the repository.
func (m *Manager) GetSkill(
	ctx context.Context, name string,
) (*agentskill.Skill, error) {
	if m.repository == nil {
		return nil, fmt.Errorf("skill repository not initialized")
	}

	sk, err := agentskill.GetForContext(ctx, m.repository, name)
	if err != nil {
		return nil, fmt.Errorf("get skill %q: %w", name, err)
	}
	return sk, nil
}

// ListSummaries returns summaries of all available skills.
func (m *Manager) ListSummaries() []agentskill.Summary {
	return m.summaries
}

// SkillCount returns the number of available skills.
func (m *Manager) SkillCount() int {
	return len(m.summaries)
}

// HasSkill checks if a skill with the given name exists.
func (m *Manager) HasSkill(name string) bool {
	for _, s := range m.summaries {
		if s.Name == name {
			return true
		}
	}
	return false
}

// CreateSkillAgent creates an LLMAgent from a loaded skill.
// The agent is configured with the skill's instruction and
// the provided model and tools.
func (m *Manager) CreateSkillAgent(
	ctx context.Context,
	name string,
	mdl model.Model,
	tools []tool.Tool,
) (agent.Agent, error) {
	sk, err := m.GetSkill(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("load skill %q: %w", name, err)
	}

	opts := []llmagent.Option{
		llmagent.WithModel(mdl),
		llmagent.WithDescription(
			fmt.Sprintf("Skill: %s - %s",
				sk.Summary.Name, sk.Summary.Description),
		),
		llmagent.WithInstruction(sk.Body),
		llmagent.WithAddCurrentTime(true),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:      false,
			MaxTokens:   intPtr(2048),
			Temperature: float64Ptr(0.3),
		}),
		llmagent.WithMaxLLMCalls(10),
	}

	if len(tools) > 0 {
		opts = append(opts, llmagent.WithTools(tools))
	}

	return llmagent.New("skill-"+name, opts...), nil
}

// Refresh reloads the skill repository to pick up new or updated
// skills at runtime.
func (m *Manager) Refresh() error {
	if m.repository == nil {
		return fmt.Errorf("skill repository not initialized")
	}

	if refreshable, ok := any(m.repository).(agentskill.RefreshableRepository); ok {
		if err := refreshable.Refresh(); err != nil {
			return fmt.Errorf("refresh skills: %w", err)
		}
	}

	m.summaries = m.repository.Summaries()
	return nil
}

func intPtr(i int) *int          { return &i }
func float64Ptr(f float64) *float64 { return &f }
