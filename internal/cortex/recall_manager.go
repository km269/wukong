// Package cortex provides CortexDB-backed intelligent recall.
//
// This file implements the recall tool manager interface backed
// by CortexDB's vector + FTS5 hybrid search. It exposes the same
// recall_search and recall_sessions tools as recall.RecallManager
// but with semantic vector search capabilities.
package cortex

import (
	"context"

	"github.com/km269/wukong/internal/recall"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// RecallManager wraps CortexStore and provides recall tool
// definitions with vector-enhanced search.
type RecallManager struct {
	store *CortexStore
}

// NewRecallManager creates a cortex-backed recall manager.
func NewRecallManager(store *CortexStore) *RecallManager {
	return &RecallManager{store: store}
}

// Tools returns recall-related function tools.
func (m *RecallManager) Tools() []tool.Tool {
	return []tool.Tool{
		function.NewFunctionTool(
			m.searchRecall,
			function.WithName("recall_search"),
			function.WithDescription(
				"Search across all conversation history "+
					"for relevant past discussions using "+
					"semantic vector search. Use this to "+
					"find previously discussed topics, "+
					"decisions, or code changes across "+
					"sessions.",
			),
		),
		function.NewFunctionTool(
			m.listRecallSessions,
			function.WithName("recall_sessions"),
			function.WithDescription(
				"List recent conversation sessions that "+
					"have stored recall data.",
			),
		),
	}
}

// SearchRecallReq is the input for searching recall.
type SearchRecallReq struct {
	Query   string `json:"query" jsonschema:"description=Search query to find relevant past conversations"`
	Limit   int    `json:"limit,omitempty" jsonschema:"description=Max results (default 10)"`
	Session string `json:"session,omitempty" jsonschema:"description=Optional: limit search to a specific session ID"`
}

// SearchRecallRsp is the output for searching recall.
type SearchRecallRsp struct {
	Success bool               `json:"success"`
	Results []recall.SearchResult `json:"results,omitempty"`
	Count   int                `json:"count"`
	Error   string             `json:"error,omitempty"`
}

func (m *RecallManager) searchRecall(
	ctx context.Context, req SearchRecallReq,
) (SearchRecallRsp, error) {
	var results []recall.SearchResult
	var err error

	if req.Session != "" {
		results, err = m.store.SearchBySession(
			req.Session, req.Query, req.Limit,
		)
	} else {
		results, err = m.store.Search(
			req.Query, "", req.Limit,
		)
	}

	if err != nil {
		return SearchRecallRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return SearchRecallRsp{
		Success: true,
		Results: results,
		Count:   len(results),
	}, nil
}

// ListRecallSessionsReq is the input for listing sessions.
type ListRecallSessionsReq struct{}

// ListRecallSessionsRsp is the output for listing sessions.
type ListRecallSessionsRsp struct {
	Success  bool     `json:"success"`
	Sessions []string `json:"sessions,omitempty"`
	Count    int      `json:"count"`
	Error    string   `json:"error,omitempty"`
}

func (m *RecallManager) listRecallSessions(
	ctx context.Context, req ListRecallSessionsReq,
) (ListRecallSessionsRsp, error) {
	sessions, err := m.store.ListSessions("")
	if err != nil {
		return ListRecallSessionsRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	return ListRecallSessionsRsp{
		Success:  true,
		Sessions: sessions,
		Count:    len(sessions),
	}, nil
}
