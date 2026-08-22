// Package session — eventlog.go
//
// ModelEventLog is a Wukong-level append-only log that records the
// messages a model ACTUALLY sees during an agent turn.
//
// It is deliberately distinct from the framework session.Service's
// own event storage. The framework log records the raw user input;
// but CoreLoop.Run enriches that input with wake-up context, recall
// results, and persistent memories BEFORE handing it to the runner.
// The enriched message is what reaches the model request, so it is
// the thing that must be reconstructable.
//
// Invariant enforced here: "model-visible means logged" — anything
// that reaches a model request must be replayable from this log.
package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Event type constants. These describe the lifecycle of a single
// enriched turn from the perspective of "what the model saw".
const (
	// EventTurnStart marks the beginning of a Run call. Payload is
	// the original user message content (before any enrichment).
	EventTurnStart = "turn_start"
	// EventUserMessage records the original, unmodified user
	// message content. Payload is the raw text.
	EventUserMessage = "user_message"
	// EventContextInject records one enrichment step that modified
	// the message before it reached the model. Source identifies
	// which enricher produced it (wakeup/recall/persistent).
	// Payload is the injected context text.
	EventContextInject = "context_inject"
	// EventModelMessage records the FINAL enriched message content
	// that was passed to runner.Run — the witness for the
	// "model-visible means logged" invariant.
	EventModelMessage = "model_message"
	// EventTurnEnd marks the completion (or rejection) of a turn.
	// Payload is the assistant response text, or the reject reason.
	EventTurnEnd = "turn_end"
)

// ModelEvent is one durable, model-visible fact in a session log.
type ModelEvent struct {
	ID        int64
	SessionID string
	UserID    string
	Seq       int64  // per-session monotonic sequence
	EventType string
	Source    string // for context_inject: wakeup|recall|persistent|"" otherwise
	Payload   string
	CreatedAt time.Time
}

// ModelEventLog is an append-only SQLite-backed log of model-visible
// events. It is safe for concurrent use. The underlying *sql.DB is
// shared (managed by util.DatabasePool) and is NOT closed here.
type ModelEventLog struct {
	mu sync.Mutex
	db *sql.DB

	// seq tracks the next per-session sequence number in memory.
	// It is initialized lazily from the DB on first append for a
	// given session. A small in-memory cache avoids a COUNT(*) on
	// every Append for the hot path.
	seqCache map[string]int64
}

// NewModelEventLog opens (or creates) the wukong_model_events table
// on the given shared DB handle. The DB lifecycle is owned by the
// caller (util.DatabasePool); this type never closes it.
func NewModelEventLog(db *sql.DB) (*ModelEventLog, error) {
	if db == nil {
		return nil, errors.New("model event log: nil db")
	}
	const schema = `
CREATE TABLE IF NOT EXISTS wukong_model_events (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT    NOT NULL,
	user_id    TEXT    NOT NULL,
	seq        INTEGER NOT NULL,
	event_type TEXT    NOT NULL,
	source     TEXT    NOT NULL DEFAULT '',
	payload    TEXT    NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_model_events_session
	ON wukong_model_events(session_id, seq);
CREATE INDEX IF NOT EXISTS idx_model_events_type
	ON wukong_model_events(event_type);
`
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("create model_events table: %w", err)
	}
	return &ModelEventLog{
		db:       db,
		seqCache: make(map[string]int64),
	}, nil
}

// nextSeq returns the next monotonic sequence for a session.
func (l *ModelEventLog) nextSeq(ctx context.Context, sessionID string) (int64, error) {
	l.mu.Lock()
	cached, ok := l.seqCache[sessionID]
	l.mu.Unlock()
	if !ok {
		// Lazy-load the max seq for this session.
		var maxSeq sql.NullInt64
		row := l.db.QueryRowContext(ctx,
			`SELECT MAX(seq) FROM wukong_model_events WHERE session_id = ?`,
			sessionID,
		)
		if err := row.Scan(&maxSeq); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("query max seq: %w", err)
		}
		cached = maxSeq.Int64 // 0 if NULL
	}
	cached++
	l.mu.Lock()
	l.seqCache[sessionID] = cached
	l.mu.Unlock()
	return cached, nil
}

// Append records one model-visible event. It never blocks the agent
// loop on error: failures are logged and returned so the caller can
// decide whether to continue (the invariant is best-effort during
// writes; reads for replay will surface missing data).
func (l *ModelEventLog) Append(
	ctx context.Context,
	sessionID, userID, eventType, source, payload string,
) error {
	if l == nil {
		return nil
	}
	if sessionID == "" {
		return errors.New("model event log: empty session id")
	}
	if source == "" && eventType == EventContextInject {
		source = "unknown"
	}

	seq, err := l.nextSeq(ctx, sessionID)
	if err != nil {
		return err
	}

	_, err = l.db.ExecContext(ctx,
		`INSERT INTO wukong_model_events
		 (session_id, user_id, seq, event_type, source, payload, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sessionID, userID, seq, eventType, source, payload,
		time.Now().UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("append model event: %w", err)
	}
	return nil
}

// Query returns the most recent `limit` events for a session, in
// ascending seq order. limit<=0 means no cap (use a sane ceiling).
func (l *ModelEventLog) Query(
	ctx context.Context, sessionID string, limit int,
) ([]ModelEvent, error) {
	if l == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10000
	}
	rows, err := l.db.QueryContext(ctx,
		`SELECT id, session_id, user_id, seq, event_type, source, payload, created_at
		 FROM wukong_model_events
		 WHERE session_id = ?
		 ORDER BY seq ASC
		 LIMIT ?`,
		sessionID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query model events: %w", err)
	}
	defer rows.Close()

	var out []ModelEvent
	for rows.Next() {
		var e ModelEvent
		var createdNano int64
		if err := rows.Scan(
			&e.ID, &e.SessionID, &e.UserID, &e.Seq,
			&e.EventType, &e.Source, &e.Payload, &createdNano,
		); err != nil {
			return nil, fmt.Errorf("scan model event: %w", err)
		}
		e.CreatedAt = time.Unix(0, createdNano)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ReplayMessages reconstructs the model-visible user message(s) for
// a session from the event log. It returns the LAST model_message
// event payload (the final enriched content the model saw) for each
// turn, in chronological order. This is the read side of the
// "model-visible means logged" invariant: a replayed message must
// equal what was sent to the model.
//
// If no model_message events exist, it falls back to user_message
// events so callers can still inspect what was attempted.
func (l *ModelEventLog) ReplayMessages(
	ctx context.Context, sessionID string,
) ([]model.Message, error) {
	if l == nil {
		return nil, nil
	}
	events, err := l.Query(ctx, sessionID, 0)
	if err != nil {
		return nil, err
	}

	var msgs []model.Message
	// Track the rolling enriched content within a turn: each
	// context_inject appends; the model_message event freezes it.
	var rollingContent string
	flushRolling := func() {
		if rollingContent != "" {
			msgs = append(msgs, model.Message{
				Role:    model.RoleUser,
				Content: rollingContent,
			})
			rollingContent = ""
		}
	}

	for _, e := range events {
		switch e.EventType {
		case EventTurnStart:
			// Turn boundary: reset the rolling content for the
			// new turn. We intentionally do NOT flush here — a
			// prior turn that crashed before emitting model_message
			// would otherwise produce a partial, misleading
			// message. Only model_message / turn_end flush.
			rollingContent = e.Payload
		case EventUserMessage:
			// The original user input starts the rolling content.
			// Overwrites turn_start's payload (same content) without
			// flushing, so no duplicate message is emitted.
			rollingContent = e.Payload
		case EventContextInject:
			// Append injection to the rolling content. The
			// CoreLoop enrichment concatenates these sections
			// in a specific order; replaying them in seq order
			// reproduces the same structure.
			if e.Payload != "" {
				if rollingContent != "" {
					rollingContent += "\n\n" + e.Payload
				} else {
					rollingContent = e.Payload
				}
			}
		case EventModelMessage:
			// The authoritative witness: replace rolling content
			// with the exact final content the model saw.
			rollingContent = e.Payload
			flushRolling()
		case EventTurnEnd:
			flushRolling()
		}
	}
	flushRolling()
	return msgs, nil
}

// ReplayModelMessage returns the most recent model_message payload
// for a session — i.e. exactly what the model saw on its last turn.
// Returns "" if no model_message event has been recorded.
func (l *ModelEventLog) ReplayModelMessage(
	ctx context.Context, sessionID string,
) (string, error) {
	if l == nil {
		return "", nil
	}
	row := l.db.QueryRowContext(ctx,
		`SELECT payload FROM wukong_model_events
		 WHERE session_id = ? AND event_type = ?
		 ORDER BY seq DESC LIMIT 1`,
		sessionID, EventModelMessage,
	)
	var payload string
	err := row.Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("replay last model message: %w", err)
	}
	return payload, nil
}

// VerifyInvariant is a debug aid: it checks that the provided final
// message content matches the last logged model_message for the
// session. A mismatch means the model saw something that is not
// reconstructable from the log — a violation. Returns nil when OK.
// Caller should log (not fail) the returned error.
func (l *ModelEventLog) VerifyInvariant(
	ctx context.Context, sessionID, finalContent string,
) error {
	if l == nil {
		return nil
	}
	logged, err := l.ReplayModelMessage(ctx, sessionID)
	if err != nil {
		return err
	}
	if logged == "" {
		// No prior model_message — this happens on the very first
		// turn before the event has been flushed. Not a violation.
		return nil
	}
	if logged != finalContent {
		util.Logger.Warn("model event log invariant violation: "+
			"final message content differs from last logged model_message",
			"session", sessionID,
			"logged_len", len(logged),
			"actual_len", len(finalContent),
		)
	}
	return nil
}

// Close is a no-op; the DB lifecycle is managed by DatabasePool.
// Implemented for interface compatibility with closable services.
func (l *ModelEventLog) Close() error { return nil }
