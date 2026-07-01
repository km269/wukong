// Package session provides a Redis-backed session service implementing
// the tRPC-Agent-Go session.Service interface.
//
// Redis-backed sessions are suitable for production deployments where
// multiple instances of wukong need to share session state.
package session

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// RedisSessionService implements session.Service backed by Redis.
// Session data is partitioned by (appName, userID, sessionID) using
// Redis key prefixes for isolation. Events are stored as JSON blobs
// in Redis lists with configurable size limits.
type RedisSessionService struct {
	client     *redis.Client
	eventLimit int
	ttl        time.Duration
	mu         sync.RWMutex
}

// RedisOption configures a RedisSessionService.
type RedisOption func(*RedisSessionService)

// WithRedisEventLimit sets the maximum events stored per session.
// Default: 1000.
func WithRedisEventLimit(limit int) RedisOption {
	return func(s *RedisSessionService) { s.eventLimit = limit }
}

// WithRedisSessionTTL sets the TTL for session data.
// Default: 0 (no expiration).
func WithRedisSessionTTL(ttl time.Duration) RedisOption {
	return func(s *RedisSessionService) { s.ttl = ttl }
}

// NewRedisSessionService creates a Redis-backed session service.
//
// Example:
//
//	s, err := NewRedisSessionService("redis://localhost:6379",
//	    WithRedisEventLimit(500),
//	    WithRedisSessionTTL(24*time.Hour),
//	)
func NewRedisSessionService(
	redisURL string, opts ...RedisOption,
) (*RedisSessionService, error) {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}

	client := redis.NewClient(opt)
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	s := &RedisSessionService{
		client:     client,
		eventLimit: 1000,
	}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// --- Redis key helpers ---

// sessKey builds the Redis key prefix for a session: "wk:session:{app}:{user}:{id}:"
func sessKey(key session.Key) string {
	return fmt.Sprintf("wk:session:%s:%s:%s", key.AppName, key.UserID, key.SessionID)
}

// eventsKey returns the Redis key for event list storage.
func eventsKey(key session.Key) string {
	return sessKey(key) + "events"
}

// metaKey returns the Redis key for session metadata hash.
func metaKey(key session.Key) string {
	return sessKey(key) + "meta"
}

// userSessionsKey returns the Redis key for user-level session index.
func userSessionsKey(userKey session.UserKey) string {
	return fmt.Sprintf("wk:user_sessions:%s:%s", userKey.AppName, userKey.UserID)
}

// --- session.Service implementation ---

// CreateSession creates a new session in Redis.
func (s *RedisSessionService) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	_ ...session.Option,
) (*session.Session, error) {
	sess := &session.Session{
		ID:        key.SessionID,
		AppName:   key.AppName,
		UserID:    key.UserID,
		State:     state,
		Events:    []event.Event{},
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}

	if err := s.saveSession(ctx, sess); err != nil {
		return nil, fmt.Errorf("redis create session: %w", err)
	}

	// Index this session for the user.
	_ = s.client.SAdd(ctx, userSessionsKey(session.UserKey{
		AppName: key.AppName,
		UserID:  key.UserID,
	}), key.SessionID).Err()

	return sess, nil
}

// GetSession retrieves a session from Redis.
func (s *RedisSessionService) GetSession(
	ctx context.Context,
	key session.Key,
	_ ...session.Option,
) (*session.Session, error) {
	sess, err := s.loadSession(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("redis get session: %w", err)
	}
	if sess == nil {
		return nil, fmt.Errorf("session not found: %s/%s/%s",
			key.AppName, key.UserID, key.SessionID)
	}
	return sess, nil
}

// ListSessions lists all sessions for a user.
func (s *RedisSessionService) ListSessions(
	ctx context.Context,
	userKey session.UserKey,
	_ ...session.Option,
) ([]*session.Session, error) {
	sessionIDs, err := s.client.SMembers(
		ctx, userSessionsKey(userKey),
	).Result()
	if err != nil {
		return nil, fmt.Errorf("redis list sessions: %w", err)
	}

	var sessions []*session.Session
	for _, sid := range sessionIDs {
		key := session.Key{
			AppName:   userKey.AppName,
			UserID:    userKey.UserID,
			SessionID: sid,
		}
		sess, err := s.loadSession(ctx, key)
		if err != nil {
			continue
		}
		if sess != nil {
			sessions = append(sessions, sess)
		}
	}
	return sessions, nil
}

// DeleteSession removes a session and its events from Redis.
func (s *RedisSessionService) DeleteSession(
	ctx context.Context,
	key session.Key,
	_ ...session.Option,
) error {
	pfx := sessKey(key)
	keys, err := s.client.Keys(ctx, pfx+"*").Result()
	if err != nil {
		slog.Warn("redis: list session keys failed",
			"pfx", pfx, "error", err.Error())
	}
	if len(keys) > 0 {
		if err := s.client.Del(ctx, keys...).Err(); err != nil {
			return fmt.Errorf("redis delete session: %w", err)
		}
	}
	if err := s.client.SRem(ctx, userSessionsKey(session.UserKey{
		AppName: key.AppName,
		UserID:  key.UserID,
	}), key.SessionID).Err(); err != nil {
		slog.Warn("redis: remove session from index failed",
			"app", key.AppName,
			"user", key.UserID,
			"session", key.SessionID,
			"error", err.Error())
	}
	return nil
}

// AppendEvent appends an event to the session's event list in Redis.
func (s *RedisSessionService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	_ ...session.Option,
) error {
	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	key := session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}

	p := s.client.Pipeline()
	p.RPush(ctx, eventsKey(key), data)
	if s.eventLimit > 0 {
		// Trim old events beyond the limit.
		p.LTrim(ctx, eventsKey(key), int64(-s.eventLimit), -1)
	}
	// Update timestamp.
	p.HSet(ctx, metaKey(key), "updated_at", time.Now().UnixMilli())
	if s.ttl > 0 {
		p.Expire(ctx, metaKey(key), s.ttl)
		p.Expire(ctx, eventsKey(key), s.ttl)
	}
	if _, err := p.Exec(ctx); err != nil {
		return fmt.Errorf("redis append event: %w", err)
	}
	return nil
}

// State management methods (lightweight implementation).

func (s *RedisSessionService) UpdateAppState(
	ctx context.Context, appName string, state session.StateMap,
) error {
	return s.updateState(ctx, fmt.Sprintf("wk:app_state:%s", appName), state)
}

func (s *RedisSessionService) DeleteAppState(
	ctx context.Context, appName string, key string,
) error {
	return s.client.HDel(ctx,
		fmt.Sprintf("wk:app_state:%s", appName), key,
	).Err()
}

func (s *RedisSessionService) ListAppStates(
	ctx context.Context, appName string,
) (session.StateMap, error) {
	return s.loadState(ctx, fmt.Sprintf("wk:app_state:%s", appName))
}

func (s *RedisSessionService) UpdateUserState(
	ctx context.Context, userKey session.UserKey, state session.StateMap,
) error {
	return s.updateState(ctx,
		fmt.Sprintf("wk:user_state:%s:%s", userKey.AppName, userKey.UserID),
		state,
	)
}

func (s *RedisSessionService) ListUserStates(
	ctx context.Context, userKey session.UserKey,
) (session.StateMap, error) {
	return s.loadState(ctx,
		fmt.Sprintf("wk:user_state:%s:%s", userKey.AppName, userKey.UserID),
	)
}

func (s *RedisSessionService) DeleteUserState(
	ctx context.Context, userKey session.UserKey, key string,
) error {
	return s.client.HDel(ctx,
		fmt.Sprintf("wk:user_state:%s:%s", userKey.AppName, userKey.UserID),
		key,
	).Err()
}

func (s *RedisSessionService) UpdateSessionState(
	ctx context.Context, key session.Key, state session.StateMap,
) error {
	return s.updateState(ctx,
		fmt.Sprintf("wk:session_state:%s:%s:%s",
			key.AppName, key.UserID, key.SessionID),
		state,
	)
}

// Summary methods (no-op for Redis — use the in-memory summarizer
// registered via Runner).

func (s *RedisSessionService) CreateSessionSummary(
	_ context.Context, _ *session.Session, _ string, _ bool,
) error {
	return nil // No-op: summaries are handled by the in-memory summarizer.
}

func (s *RedisSessionService) EnqueueSummaryJob(
	_ context.Context, _ *session.Session, _ string, _ bool,
) error {
	return nil // No-op.
}

func (s *RedisSessionService) GetSessionSummaryText(
	_ context.Context, _ *session.Session, _ ...session.SummaryOption,
) (string, bool) {
	return "", false
}

// Close releases the Redis connection.
func (s *RedisSessionService) Close() error {
	return s.client.Close()
}

// --- internal helpers ---

func (s *RedisSessionService) saveSession(
	ctx context.Context, sess *session.Session,
) error {
	key := session.Key{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}

	meta := map[string]any{
		"app_name":   sess.AppName,
		"user_id":    sess.UserID,
		"session_id": sess.ID,
		"created_at": sess.CreatedAt.UnixMilli(),
		"updated_at": sess.UpdatedAt.UnixMilli(),
	}
	p := s.client.Pipeline()
	p.HSet(ctx, metaKey(key), meta)
	if s.ttl > 0 {
		p.Expire(ctx, metaKey(key), s.ttl)
	}
	_, err := p.Exec(ctx)
	return err
}

func (s *RedisSessionService) loadSession(
	ctx context.Context, key session.Key,
) (*session.Session, error) {
	meta, err := s.client.HGetAll(ctx, metaKey(key)).Result()
	if err != nil {
		return nil, err
	}
	if len(meta) == 0 {
		return nil, nil
	}

	sess := &session.Session{
		ID:      meta["session_id"],
		AppName: meta["app_name"],
		UserID:  meta["user_id"],
		State:   session.StateMap{},
	}

	// Load events.
	rawEvents, err := s.client.LRange(
		ctx, eventsKey(key), 0, -1,
	).Result()
	if err == nil {
		for _, raw := range rawEvents {
			var evt event.Event
			if json.Unmarshal([]byte(raw), &evt) == nil {
				sess.Events = append(sess.Events, evt)
			}
		}
	}

	return sess, nil
}

func (s *RedisSessionService) updateState(
	ctx context.Context, redisKey string, state session.StateMap,
) error {
	if len(state) == 0 {
		return nil
	}
	fields := make([]any, 0, len(state)*2)
	for k, v := range state {
		fields = append(fields, k, string(v))
	}
	if err := s.client.HSet(ctx, redisKey, fields...).Err(); err != nil {
		return fmt.Errorf("redis update state: %w", err)
	}
	if s.ttl > 0 {
		if err := s.client.Expire(ctx, redisKey, s.ttl).Err(); err != nil {
			slog.Warn("redis: set session TTL failed",
				"key", redisKey,
				"ttl", s.ttl.String(),
				"error", err.Error())
		}
	}
	return nil
}

func (s *RedisSessionService) loadState(
	ctx context.Context, redisKey string,
) (session.StateMap, error) {
	result, err := s.client.HGetAll(ctx, redisKey).Result()
	if err != nil {
		return nil, fmt.Errorf("redis load state: %w", err)
	}
	state := make(session.StateMap, len(result))
	for k, v := range result {
		state[k] = []byte(v)
	}
	return state, nil
}
