package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/km269/wukong/internal/agent"
	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// GatewayServer is the HTTP entry point for all external platform
// callbacks. It manages the ChannelRouter, applies middleware
// (logging, recovery, dedup, rate limiting), and dispatches requests
// to the appropriate Channel handler.
type GatewayServer struct {
	mu        sync.RWMutex
	router    *ChannelRouter
	server    *http.Server
	address   string
	running   bool
	cfg       *config.GatewayConfig
	coreLoop  *agent.CoreLoop
	sessStore *GatewaySessionStore
	dedup     *MessageDeduplicator
	ratelimit *RateLimiter
}

// NewGatewayServer creates a new GatewayServer with the given
// configuration and dependencies.
func NewGatewayServer(
	cfg *config.WukongConfig,
	loop *agent.CoreLoop,
	store *GatewaySessionStore,
) *GatewayServer {
	gc := &cfg.Gateway

	// Message dedup (5 min TTL by default).
	dedup := NewMessageDeduplicator(gc.MessageDedupTTL)

	// Rate limiter: per-user sliding window + concurrency cap.
	rateLimitWindow := gc.RateLimitWindow
	if rateLimitWindow <= 0 {
		rateLimitWindow = 10 * time.Second
	}
	rateLimitPerUser := gc.RateLimitPerUser
	if rateLimitPerUser <= 0 {
		rateLimitPerUser = 10
	}
	ratelimit := NewRateLimiter(
		rateLimitWindow,
		rateLimitPerUser,
		gc.MaxConcurrentSessions,
	)

	return &GatewayServer{
		router:    NewChannelRouter(),
		cfg:       gc,
		coreLoop:  loop,
		sessStore: store,
		dedup:     dedup,
		ratelimit: ratelimit,
	}
}

// Start begins listening on the configured address. It registers all
// channels before starting the HTTP server. This method blocks until
// the server is stopped via Stop().
func (gs *GatewayServer) Start() error {
	gs.mu.Lock()
	if gs.running {
		gs.mu.Unlock()
		return fmt.Errorf("gateway: already running")
	}

	addr := gs.cfg.Address
	if addr == "" {
		addr = ":9093"
	}
	gs.address = addr

	// Build the HTTP handler chain.
	mux := http.NewServeMux()
	mux.Handle("/", gs.router.Handler(gs.handleChannel))
	mux.HandleFunc("/metrics", gs.handleMetrics)

	handler := withRecovery(withRequestLogging(mux))

	gs.server = &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 300 * time.Second, // Long timeout for streaming replies
		IdleTimeout:  120 * time.Second,
	}
	gs.running = true
	gs.mu.Unlock()

	util.Logger.Info("gateway: starting server",
		slog.String("address", addr))

	return gs.server.ListenAndServe()
}

// Stop gracefully shuts down the gateway server with the given
// context timeout. Background workers (dedup, rate limiter) are also
// stopped.
func (gs *GatewayServer) Stop(ctx context.Context) error {
	gs.mu.Lock()
	defer gs.mu.Unlock()

	if !gs.running || gs.server == nil {
		return nil
	}

	gs.running = false
	util.Logger.Info("gateway: shutting down server")

	// Stop background workers.
	gs.dedup.Stop()
	gs.ratelimit.Stop()

	if err := gs.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("gateway: shutdown: %w", err)
	}
	return nil
}

// RegisterChannel registers a platform channel with the router.
// Call this before Start() to add channel support.
func (gs *GatewayServer) RegisterChannel(ch Channel) error {
	return gs.router.Register(ch)
}

// Address returns the listen address of the gateway server.
func (gs *GatewayServer) Address() string {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	return gs.address
}

// IsRunning returns whether the server is currently running.
func (gs *GatewayServer) IsRunning() bool {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	return gs.running
}

// handleChannel is the central request handler that coordinates the
// full message processing pipeline for each incoming platform
// callback:
//
//  1. Verify the request signature
//  2. Parse the platform message
//  3. Check for platform events (URL verification, etc.)
//  4. Deduplicate by MessageID
//  5. Map platform identity to Wukong user/session
//  6. Rate-limit per user + concurrency gate
//  7. Ensure session persistence
//  8. Run the agent loop
//  9. Send the reply back to the platform
func (gs *GatewayServer) handleChannel(
	ch Channel, w http.ResponseWriter, r *http.Request,
) {
	ctx, cancel := context.WithTimeout(
		r.Context(),
		gs.cfg.DefaultTimeout,
	)
	defer cancel()

	// Step 1: Verify request authenticity.
	body, err := ch.VerifyRequest(r)
	if err != nil {
		util.Logger.Warn("gateway: verification failed",
			slog.String("channel", ch.Name()),
			slog.String("error", err.Error()))
		http.Error(w, "Verification failed",
			http.StatusForbidden)
		return
	}

	// Step 2: Check for platform events (URL challenge, etc.).
	// Check both body-based events (Feishu JSON) and URL
	// query-based events (WeCom echostr GET request).
	evt := parsePlatformEvent(body, r.URL.Query())
	if evt != nil && evt.Type != "" {
		evt.Platform = ch.Name()
		resp, err := ch.HandlePlatformEvent(w, evt)
		if err != nil {
			util.Logger.Error("gateway: handle platform event",
				slog.String("channel", ch.Name()),
				slog.String("event_type", evt.Type),
				slog.String("error", err.Error()))
			http.Error(w, err.Error(),
				http.StatusInternalServerError)
			return
		}
		if resp != nil {
			// WeCom URL verification returns plain text;
			// Feishu returns JSON. Detect content type.
			contentType := "application/json"
			if evt.Platform == "wecom" &&
				evt.Type == "url_verify" {
				contentType = "text/plain; charset=utf-8"
			}
			w.Header().Set("Content-Type", contentType)
			w.WriteHeader(http.StatusOK)
			w.Write(resp)
		}
		return
	}

	// Step 3: Parse the message.
	msg, err := ch.ParseMessage(body)
	if err != nil {
		util.Logger.Warn("gateway: message parse failed",
			slog.String("channel", ch.Name()),
			slog.String("error", err.Error()))
		http.Error(w, "Bad request",
			http.StatusBadRequest)
		return
	}
	if msg == nil {
		// Empty message (e.g., unknown event type); ignore
		// silently.
		w.WriteHeader(http.StatusOK)
		return
	}

	msg.Platform = ch.Name()

	// Step 4: Message deduplication.  Platform retries with the
	// same MessageID are silently dropped here to avoid hitting
	// the agent loop multiple times.
	if gs.dedup.IsDuplicate(msg.Platform, msg.MessageID) {
		util.Logger.Debug("gateway: duplicate message dropped",
			slog.String("channel", msg.Platform),
			slog.String("message_id", msg.MessageID),
		)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Step 5: Build Wukong user and session identifiers.
	userID := ch.BuildUserID(msg)
	sessionID := ch.BuildSessionID(msg)

	// Step 6: Rate limiting — per-user rate check + concurrency
	// gate.
	release, allowed := gs.ratelimit.Allow(msg.Platform, userID)
	if !allowed {
		util.Logger.Warn("gateway: rate limit exceeded",
			slog.String("channel", msg.Platform),
			slog.String("user", userID),
		)
		http.Error(w, "Too many requests",
			http.StatusTooManyRequests)
		return
	}
	defer release()

	// Step 7: Ensure session mapping persistence.
	_, err = gs.sessStore.GetOrCreateSession(
		ch.Name(), msg.PlatformUserID, msg.ConversationID,
		userID, sessionID,
	)
	if err != nil {
		util.Logger.Warn("gateway: session mapping failed",
			slog.String("error", err.Error()))
		// Non-fatal; continue with the session IDs we have.
	}

	util.Logger.Info("gateway: processing message",
		slog.String("channel", ch.Name()),
		slog.String("user", userID),
		slog.String("session", sessionID),
		slog.Int("content_len", len(msg.Content)),
	)

	// Step 8: Run the agent loop.
	agentMsg := model.NewUserMessage(msg.Content)
	events, err := gs.coreLoop.Run(
		ctx, userID, sessionID, agentMsg)
	if err != nil {
		util.Logger.Error("gateway: agent run failed",
			slog.String("channel", ch.Name()),
			slog.String("error", err.Error()))
		gs.sendErrorReply(ch, msg, err)
		// Still return 200 to the platform so it doesn't retry.
		w.WriteHeader(http.StatusOK)
		return
	}

	// Step 9: Send the reply (this handles streaming internally).
	replyCtx, replyCancel := context.WithTimeout(
		context.Background(),
		gs.cfg.DefaultTimeout,
	)
	defer replyCancel()

	if err := ch.SendReply(replyCtx, msg, events); err != nil {
		util.Logger.Error("gateway: send reply failed",
			slog.String("channel", ch.Name()),
			slog.String("error", err.Error()))
	}

	// Always acknowledge the platform callback.
	w.WriteHeader(http.StatusOK)
}

// sendErrorReply logs an agent execution error. In the future, this
// could send a user-friendly error message back to the platform.
func (gs *GatewayServer) sendErrorReply(
	_ Channel, _ *GatewayMessage, err error,
) {
	util.Logger.Error("gateway: agent execution error",
		slog.String("error", err.Error()))
}

// handleMetrics exposes a simple JSON endpoint for monitoring
// gateway health: dedup state, rate limiter stats, and running
// channels.
func (gs *GatewayServer) handleMetrics(
	w http.ResponseWriter, r *http.Request,
) {
	type channelInfo struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	type metricsResp struct {
		Status      string             `json:"status"`
		DedupSize   int                `json:"dedup_size"`
		RateLimiter RateLimiterMetrics `json:"rate_limiter"`
		Channels    []channelInfo      `json:"channels"`
	}

	gs.mu.RLock()
	chList := gs.router.ListChannels()
	channels := make([]channelInfo, 0, len(chList))
	for _, ch := range chList {
		channels = append(channels, channelInfo{
			Name: ch.Name(),
			Path: ch.RoutePath(),
		})
	}
	running := gs.running
	gs.mu.RUnlock()

	resp := metricsResp{
		Status:      "ok",
		DedupSize:   gs.dedup.Size(),
		RateLimiter: gs.ratelimit.Metrics(),
		Channels:    channels,
	}
	if !running {
		resp.Status = "stopped"
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// parsePlatformEvent attempts to parse a raw JSON body or URL query
// params into a PlatformEvent. Returns nil if the request doesn't look
// like a platform event.
func parsePlatformEvent(
	body []byte, query url.Values,
) *PlatformEvent {
	// First, check URL query for WeCom URL verification (GET with
	// echostr).
	if echostr := query.Get("echostr"); echostr != "" {
		sig := query.Get("msg_signature")
		ts := query.Get("timestamp")
		nonce := query.Get("nonce")
		return &PlatformEvent{
			Type: "url_verify",
			Data: []byte(echostr),
			Metadata: map[string]string{
				"msg_signature": sig,
				"timestamp":     ts,
				"nonce":         nonce,
				"echostr":       echostr,
			},
		}
	}

	// Then check body for JSON-based events.
	if len(body) == 0 {
		return nil
	}

	// Try to detect common event patterns.
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}

	// Feishu URL challenge: {"type": "url_verification", ...}
	if eventType, ok := raw["type"].(string); ok {
		switch eventType {
		case "url_verification":
			return &PlatformEvent{
				Type: "url_verify",
				Data: body,
			}
		default:
			// Could be an event_callback; let Channel handle it.
			if raw["event"] != nil {
				return &PlatformEvent{
					Type: "event_callback",
					Data: body,
				}
			}
		}
	}

	// Not a recognized platform event; treat as a regular message.
	return nil
}
