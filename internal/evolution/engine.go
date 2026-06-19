// Package evolution provides the skill self-evolution system.
package evolution

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/provider"
	"github.com/km269/wukong/internal/util"
)

// SkillRefresher is implemented by skill.Manager to allow the
// evolution engine to trigger hot-reload after a patch is applied.
type SkillRefresher interface {
	Refresh() error
	SkillsDir() string
}

// EngineConfig holds configuration for the evolution engine.
type EngineConfig struct {
	Config  *config.WukongConfig
	Factory *provider.Factory
	DBPool  *util.DatabasePool
}

// EvolutionEngine orchestrates the skill self-evolution lifecycle:
// execution trace collection → LLM analysis → patch generation → apply.
//
// The engine is designed to be non-blocking: analysis runs in a
// background goroutine so it doesn't impact the main agent loop.
type EvolutionEngine struct {
	cfg       *EvolutionConfig
	wukongCfg *config.WukongConfig
	analyzer  *EvolutionAnalyzer
	patcher   *EvolutionPatcher
	store     *VersionStore
	refresher SkillRefresher

	mu          sync.Mutex
	analysisCh  chan *ExecutionTrace
	pendingJobs sync.WaitGroup
	closed      bool
}

// NewEngine creates a new evolution engine.
func NewEngine(ec EngineConfig) (*EvolutionEngine, error) {
	evCfg := &ec.Config.Evolution
	if !evCfg.Enabled {
		return nil, nil
	}

	// Create the version store using the shared database pool
	store, err := NewVersionStore(ec.DBPool)
	if err != nil {
		return nil, fmt.Errorf("create version store: %w", err)
	}

	// Create the analyzer
	analyzerCfg := &EvolutionConfig{
		Enabled:         evCfg.Enabled,
		AutoPatch:       evCfg.AutoPatch,
		MinConfidence:   evCfg.MinConfidence,
		MaxPatchSize:    evCfg.MaxPatchSize,
		AnalysisTimeout: evCfg.AnalysisTimeout,
	}
	if analyzerCfg.MinConfidence <= 0 {
		analyzerCfg.MinConfidence = 0.7
	}
	if analyzerCfg.AnalysisTimeout <= 0 {
		analyzerCfg.AnalysisTimeout = 60 * time.Second
	}

	analyzer, err := NewEvolutionAnalyzer(ec.Factory, analyzerCfg)
	if err != nil {
		return nil, fmt.Errorf("create analyzer: %w", err)
	}

	// Create the patcher
	maxVersions := evCfg.MaxVersionsKept
	if maxVersions <= 0 {
		maxVersions = 10
	}
	patcher := NewEvolutionPatcher(store, maxVersions)

	engine := &EvolutionEngine{
		cfg:        analyzerCfg,
		wukongCfg:  ec.Config,
		analyzer:   analyzer,
		patcher:    patcher,
		store:      store,
		analysisCh: make(chan *ExecutionTrace, 64),
	}

	// Start background analysis worker
	engine.pendingJobs.Add(1)
	go engine.analysisWorker()

	util.Logger.Info("evolution: engine started",
		"auto_patch", evCfg.AutoPatch,
		"min_confidence", evCfg.MinConfidence,
		"cooldown_period", evCfg.CooldownPeriod,
	)

	return engine, nil
}

// SetRefresher binds a SkillRefresher (skill.Manager) for hot-reload
// after patches are applied.
func (e *EvolutionEngine) SetRefresher(r SkillRefresher) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.refresher = r
}

// RecordExecution captures a skill execution trace and enqueues it
// for asynchronous analysis. This is called from the AfterAgent
// callback in the agent loop — it must be fast and non-blocking.
//
// Only traces where the skill-name prefix matches ("skill_") are
// accepted; others are silently ignored.
func (e *EvolutionEngine) RecordExecution(
	trace *ExecutionTrace,
) {
	if e == nil || trace == nil {
		return
	}

	e.mu.Lock()
	closed := e.closed
	e.mu.Unlock()

	if closed {
		return
	}

	// Non-blocking send to analysis channel
	select {
	case e.analysisCh <- trace:
	default:
		util.Logger.Warn(
			"evolution: analysis channel full, dropping trace",
			"skill", trace.SkillName,
		)
	}
}

// analysisWorker processes execution traces in the background.
// It performs cooldown checks, runs LLM analysis, and applies
// patches when auto-patch is enabled.
func (e *EvolutionEngine) analysisWorker() {
	defer e.pendingJobs.Done()

	for trace := range e.analysisCh {
		e.processTrace(trace)
	}
}

// processTrace handles a single execution trace through the full
// evolution pipeline.
func (e *EvolutionEngine) processTrace(
	trace *ExecutionTrace,
) {
	ctx := context.Background()

	// Skip if the skill already succeeded perfectly
	if trace.Success && trace.ErrorCount == 0 &&
		trace.QualityScore > 0.8 {
		return
	}

	// Check cooldown period
	if !e.checkCooldown(trace.SkillName) {
		util.Logger.Debug("evolution: cooldown not elapsed",
			"skill", trace.SkillName,
		)
		return
	}

	// Check daily patch limit
	if !e.checkDailyLimit(trace.SkillName) {
		util.Logger.Debug("evolution: daily limit reached",
			"skill", trace.SkillName,
		)
		return
	}

	// Read the current SKILL.md content
	skillContent, skillDir, err := e.readSkillContent(
		trace.SkillName,
	)
	if err != nil {
		util.Logger.Warn("evolution: read skill failed",
			"skill", trace.SkillName,
			"error", err.Error(),
		)
		return
	}

	// Get current version
	currentVersion, err := e.store.GetCurrentVersion(
		trace.SkillName,
	)
	if err != nil {
		currentVersion = 0
	}

	// Record execution trace
	traceJSON, _ := json.Marshal(trace)
	rec := &EvolutionRecord{
		SkillName:     trace.SkillName,
		SessionID:     trace.SessionID,
		TraceJSON:     string(traceJSON),
		HasIssue:      false,
		PatchApplied:  false,
		VersionBefore: currentVersion,
		VersionAfter:  currentVersion,
	}

	// Run LLM analysis
	suggestion, err := e.analyzer.Analyze(
		ctx, trace, skillContent,
	)
	if err != nil {
		util.Logger.Warn("evolution: analysis failed",
			"skill", trace.SkillName,
			"error", err.Error(),
		)
		// Record the attempt
		_ = e.store.RecordEvolution(rec)
		return
	}

	if suggestion == nil {
		// No issue found — record and return
		_ = e.store.RecordEvolution(rec)
		return
	}

	rec.HasIssue = true
	rec.PatchReason = suggestion.Reason
	rec.PatchConfidence = suggestion.Confidence

	util.Logger.Info("evolution: issue detected",
		"skill", suggestion.SkillName,
		"problem_type", suggestion.ProblemType,
		"confidence", suggestion.Confidence,
		"reason", suggestion.Reason,
	)

	// Apply patch if auto-patch is enabled
	if e.cfg.AutoPatch {
		newVersion, patchErr := e.patcher.ApplyPatch(
			suggestion, skillDir,
		)
		if patchErr != nil {
			util.Logger.Warn("evolution: patch failed",
				"skill", suggestion.SkillName,
				"error", patchErr.Error(),
			)
			_ = e.store.RecordEvolution(rec)
			return
		}

		rec.PatchApplied = true
		rec.VersionAfter = newVersion

		// Trigger skill manager refresh for hot-reload
		e.mu.Lock()
		refresher := e.refresher
		e.mu.Unlock()
		if refresher != nil {
			if err := refresher.Refresh(); err != nil {
				util.Logger.Warn(
					"evolution: skill refresh failed",
					"error", err.Error(),
				)
			}
		}

		util.Logger.Info("evolution: skill evolved",
			"skill", suggestion.SkillName,
			"old_version", currentVersion,
			"new_version", newVersion,
			"problem_type", suggestion.ProblemType,
		)
	} else {
		util.Logger.Info("evolution: patch suggestion (not applied)",
			"skill", suggestion.SkillName,
			"reason", suggestion.Reason,
			"confidence", suggestion.Confidence,
		)
	}

	// Record the evolution attempt
	if err := e.store.RecordEvolution(rec); err != nil {
		util.Logger.Warn("evolution: record failed",
			"error", err.Error(),
		)
	}
}

// checkCooldown verifies that enough time has passed since the last
// patch for this skill. Returns false if the cooldown hasn't elapsed.
func (e *EvolutionEngine) checkCooldown(
	skillName string,
) bool {
	if e.wukongCfg.Evolution.CooldownPeriod <= 0 {
		return true
	}

	lastPatch, err := e.store.GetLastPatchTime(skillName)
	if err != nil || lastPatch.IsZero() {
		return true // No previous patches
	}

	elapsed := time.Since(lastPatch)
	return elapsed >= e.wukongCfg.Evolution.CooldownPeriod
}

// checkDailyLimit verifies that the daily patch count hasn't been
// exceeded for this skill.
func (e *EvolutionEngine) checkDailyLimit(
	skillName string,
) bool {
	maxPatches := e.wukongCfg.Evolution.MaxPatchesPerDay
	if maxPatches <= 0 {
		return true
	}

	count, err := e.store.CountPatchesToday(skillName)
	if err != nil {
		util.Logger.Warn("evolution: count patches failed",
			"error", err.Error(),
		)
		return true // Allow on error
	}

	return count < maxPatches
}

// readSkillContent reads the SKILL.md file for a skill by name.
// It searches the configured skills directory.
func (e *EvolutionEngine) readSkillContent(
	skillName string,
) (string, string, error) {
	skillsDir := e.wukongCfg.Skill.SkillsDir
	if skillsDir == "" {
		skillsDir = ".wukong_skills"
	}

	skillDir := filepath.Join(skillsDir, skillName)
	skillPath := filepath.Join(skillDir, "SKILL.md")

	content, err := os.ReadFile(skillPath)
	if err != nil {
		return "", "", fmt.Errorf(
			"read %s: %w", skillPath, err)
	}

	return string(content), skillDir, nil
}

// Close shuts down the evolution engine gracefully.
// It closes the analysis channel, waits for pending jobs to finish,
// and cleans up resources.
func (e *EvolutionEngine) Close() error {
	if e == nil {
		return nil
	}

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	close(e.analysisCh)
	e.mu.Unlock()

	// Wait for pending analysis to complete
	e.pendingJobs.Wait()

	util.Logger.Info("evolution: engine stopped")
	return nil
}

// Store returns the version store for external queries.
func (e *EvolutionEngine) Store() *VersionStore {
	if e == nil {
		return nil
	}
	return e.store
}
