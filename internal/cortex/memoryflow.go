// Package cortex provides CortexDB-backed intelligent recall.
//
// This file implements MemoryFlowService — a wrapper around
// CortexDB's MemoryFlow Service that provides conversation transcript
// recording, wake-up context generation, and fact promotion
// from conversations to long-term knowledge.
package cortex

import (
	"context"
	"fmt"

	"github.com/km269/wukong/internal/config"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/memoryflow"
)

// MemoryFlowService wraps CortexDB's MemoryFlow Service for use in
// the Wukong agent pipeline. It provides:
//
//   - IngestTurn: record a single conversation turn.
//   - WakeUp: build context layers for the next agent run.
//   - PromoteFacts: extract promotion candidates and elevate them
//     to the knowledge base.
//
// The service is designed to work alongside the existing
// memory.Service (tRPC) — it enriches the agent with conversational
// context rather than replacing the key-value memory store.
type MemoryFlowService struct {
	cfg       *config.MemoryFlowConfig
	db        *cortexdb.DB
	flow      *memoryflow.Service
	planner   memoryflow.QueryPlanner
	extractor memoryflow.SessionExtractor
}

// NewMemoryFlow creates a new MemoryFlow service with LLM-driven
// query planning and session extraction.
func NewMemoryFlow(
	cfg *config.MemoryFlowConfig,
	planner memoryflow.QueryPlanner,
	extractor memoryflow.SessionExtractor,
) (*MemoryFlowService, error) {
	dbPath := config.ResolvePath(cfg.DBPath)

	dbCfg := cortexdb.DefaultConfig(dbPath)
	if cfg.EmbeddingDimensions > 0 {
		dbCfg.Dimensions = cfg.EmbeddingDimensions
	}

	db, err := cortexdb.Open(dbCfg)
	if err != nil {
		return nil, fmt.Errorf(
			"memoryflow: open cortexdb: %w", err)
	}

	flow, err := memoryflow.New(db, planner, extractor)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf(
			"memoryflow: create flow: %w", err)
	}

	return &MemoryFlowService{
		cfg:       cfg,
		db:        db,
		flow:      flow,
		planner:   planner,
		extractor: extractor,
	}, nil
}

// IngestTurn records a single conversation turn into the MemoryFlow
// transcript store. Call this after each user/assistant message.
//
// SessionID is used to group turns into a single transcript.
// UserID identifies the user for scoped recall.
func (m *MemoryFlowService) IngestTurn(
	ctx context.Context,
	sessionID string,
	userID string,
	role string,
	content string,
) error {
	_, err := m.flow.IngestTranscript(ctx,
		memoryflow.IngestTranscriptRequest{
			Transcript: memoryflow.Transcript{
				SessionID: sessionID,
				UserID:    userID,
				Source:    "chat",
				Turns: []memoryflow.TranscriptTurn{
					{
						Role:    role,
						Content: content,
					},
				},
			},
			Scope:     cortexdb.MemoryScopeSession,
			Namespace: m.cfg.Namespace,
		},
	)
	if err != nil {
		return fmt.Errorf(
			"memoryflow: ingest turn: %w", err)
	}
	return nil
}

// WakeUp builds a layered context for the next agent run.
// It assembles:
//   Layer 1: Identity (agent persona)
//   Layer 2: Recalled memories from past conversations
//   Layer 3: Session-level context
//
// The returned string can be injected into the system prompt.
func (m *MemoryFlowService) WakeUp(
	ctx context.Context,
	identity string,
	query string,
	sessionID string,
) (string, error) {
	resp, err := m.flow.WakeUpLayers(ctx,
		memoryflow.WakeUpLayersRequest{
			Identity: identity,
			Recall: memoryflow.RecallRequest{
				Query:     query,
				SessionID: sessionID,
				Scope:     cortexdb.MemoryScopeSession,
				Namespace: m.cfg.Namespace,
			},
		},
	)
	if err != nil {
		return "", fmt.Errorf(
			"memoryflow: wake up: %w", err)
	}

	// Format layers into a context string.
	if resp == nil || len(resp.Layers) == 0 {
		return "", nil
	}

	var builder string
	for _, layer := range resp.Layers {
		if layer.Text != "" {
			if builder != "" {
				builder += "\n\n"
			}
			if layer.Title != "" {
				builder += fmt.Sprintf(
					"## %s\n%s", layer.Title, layer.Text)
			} else {
				builder += layer.Text
			}
		}
	}
	return builder, nil
}

// PromoteFacts extracts promotion candidates from the current
// session transcript and promotes them to the knowledge base.
// Call this periodically (e.g., at session end) to elevate
// important facts from ephemeral conversation to long-term memory.
func (m *MemoryFlowService) PromoteFacts(
	ctx context.Context,
	sessionID string,
	userID string,
) ([]memoryflow.PromotionCandidate, error) {
	// Build a transcript from the session.
	transcript := memoryflow.Transcript{
		SessionID: sessionID,
		UserID:    userID,
		Source:    "chat",
	}

	// Construct session state for extraction.
	state := memoryflow.SessionState{
		SessionID: sessionID,
		UserID:    userID,
	}

	candidates, err := m.extractor.Extract(
		ctx, transcript, state)
	if err != nil {
		return nil, fmt.Errorf(
			"memoryflow: extract candidates: %w", err)
	}

	return candidates, nil
}

// Close releases resources held by the MemoryFlow service.
func (m *MemoryFlowService) Close() error {
	if m.db != nil {
		return m.db.Close()
	}
	return nil
}

// Planner returns the query planner for external use.
func (m *MemoryFlowService) Planner() memoryflow.QueryPlanner {
	return m.planner
}

// Extractor returns the session extractor for external use.
func (m *MemoryFlowService) Extractor() memoryflow.SessionExtractor {
	return m.extractor
}
