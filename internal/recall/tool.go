// Package recall provides chat recall tools for the agent.
package recall

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// RecallManager wraps the store and provides tool definitions.
type RecallManager struct {
	store *Store
}

// NewRecallManager creates a new recall manager.
func NewRecallManager(store *Store) *RecallManager {
	return &RecallManager{store: store}
}

// Tools returns all recall-related function tools.
func (m *RecallManager) Tools() []tool.Tool {
	return []tool.Tool{
		function.NewFunctionTool(
			m.searchRecall,
			function.WithName("recall_search"),
			function.WithDescription(
				"Search across all conversation history "+
					"for relevant past discussions. Use this to "+
					"find previously discussed topics, decisions, "+
					"or code changes across sessions.",
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
	Success bool           `json:"success"`
	Results []SearchResult `json:"results,omitempty"`
	Count   int            `json:"count"`
	Error   string         `json:"error,omitempty"`
}

func (m *RecallManager) searchRecall(
	ctx context.Context, req SearchRecallReq,
) (SearchRecallRsp, error) {
	var results []SearchResult
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

// StoreMessage stores a chat message for recall.
func (m *RecallManager) StoreMessage(
	sessionID, userID, role, content string,
) error {
	return m.store.StoreMessage(ChatMessage{
		SessionID: sessionID,
		UserID:    userID,
		Role:      role,
		Content:   content,
	})
}
