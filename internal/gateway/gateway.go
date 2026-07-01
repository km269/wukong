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
	"trpc.group/trpc-go/trpc-agent-go/event"
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
	// Defaults chosen so normal interactive use never trips the
	// limit; tune up if serving power users, down if under abuse.
	rateLimitWindow := gc.RateLimitWindow
	if rateLimitWindow <= 0 {
		rateLimitWindow = 60 * time.Second
	}
	rateLimitPerUser := gc.RateLimitPerUser
	if rateLimitPerUser <= 0 {
		rateLimitPerUser = 20
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
// callback.
//
// Critical timing constraint: platforms (Feishu, WeCom) require the
// callback to return HTTP 200 within ~3 seconds, otherwise they treat
// it as a failure and retry — which the dedup layer then silently
// drops, leaving the user with no visible reply even though the agent
// eventually runs. To honor this, the handler performs only the
// fast synchronous pre-processing (verify → parse → dedup → rate
// limit → session map) and then:
//
//  1. Acknowledges the callback immediately with 200.
//  2. Launches the agent run + reply in a background goroutine whose
//     context is detached from the HTTP request lifecycle, so the
//     agent keeps running even after the connection closes.
//
// Dedup and rate-limit acquisition happen BEFORE the ack so that
// platform retries do not spawn duplicate agent runs.
func (gs *GatewayServer) handleChannel(
	ch Channel, w http.ResponseWriter, r *http.Request,
) {
	// Step 1: Verify request authenticity.
	// Use a short-lived context tied to the request only for the
	// synchronous pre-processing; the agent run gets its own
	// detached context (see processMessageAsync).
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
	// the agent loop multiple times. MUST happen before the ack
	// so retries do not spawn duplicate background runs.
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
	// gate. The release func is handed to the background runner so
	// the concurrency slot is held for the full agent run.
	release, allowed := gs.ratelimit.Allow(msg.Platform, userID)
	if !allowed {
		util.Logger.Warn("gateway: rate limit exceeded",
			slog.String("channel", msg.Platform),
			slog.String("user", userID),
		)
		// Return 200 (not 429) so the platform does NOT retry —
		// a retry would just hit the rate limit again and amplify
		// load. The user may resend manually.
		w.WriteHeader(http.StatusOK)
		return
	}

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

	// Step 8: Acknowledge the callback IMMEDIATELY. The platform
	// (Feishu/WeCom) requires a 200 within ~3s; the agent run takes
	// far longer, so we must not wait for it. The agent + reply run
	// in a background goroutine with a detached context.
	w.WriteHeader(http.StatusOK)

	// Step 9: Run the agent loop + send reply asynchronously.
	go gs.processMessageAsync(ch, msg, userID, sessionID, release)
}

// processMessageAsync runs the agent loop and sends the reply in the
// background, fully detached from the HTTP request that triggered it.
//
// The context is derived from context.Background() so that closing the
// HTTP connection (after the early ack) does NOT cancel the agent.
// The concurrency-slot release func is invoked when the run completes
// (including on error/panic), preventing slot leaks.
func (gs *GatewayServer) processMessageAsync(
	ch Channel,
	msg *GatewayMessage,
	userID, sessionID string,
	release func(),
) {
	// Always release the concurrency slot, even on panic.
	defer func() {
		if r := recover(); r != nil {
			util.Logger.Error("gateway: panic in async processing",
				slog.String("channel", ch.Name()),
				slog.String("user", userID),
				slog.Any("panic", r),
			)
		}
		release()
	}()

	// Detached context: survives HTTP connection close. Bounded by
	// the configured per-run timeout.
	ctx, cancel := context.WithTimeout(
		context.Background(),
		gs.cfg.DefaultTimeout,
	)
	defer cancel()

	// Run the agent loop.
	agentMsg := model.NewUserMessage(msg.Content)
	events, err := gs.coreLoop.Run(ctx, userID, sessionID, agentMsg)
	if err != nil {
		util.Logger.Error("gateway: agent run failed",
			slog.String("channel", ch.Name()),
			slog.String("user", userID),
			slog.String("error", err.Error()))
		gs.sendErrorReply(ch, msg, err)
		return
	}

	// Send the reply (handles streaming internally). Re-derives a
	// fresh context so the reply is not cut short by the run ctx
	// being exhausted mid-stream.
	replyCtx, replyCancel := context.WithTimeout(
		context.Background(),
		gs.cfg.DefaultTimeout,
	)
	defer replyCancel()

	if err := ch.SendReply(replyCtx, msg, events); err != nil {
		util.Logger.Error("gateway: send reply failed",
			slog.String("channel", ch.Name()),
			slog.String("user", userID),
			slog.String("error", err.Error()))
	}
}

// sendErrorReply sends a user-friendly error message back to the
// platform when agent execution fails. This ensures users receive
// feedback instead of seeing no response.
func (gs *GatewayServer) sendErrorReply(
	ch Channel, msg *GatewayMessage, err error,
) {
	util.Logger.Error("gateway: agent execution error",
		slog.String("channel", ch.Name()),
		slog.String("error", err.Error()),
		slog.String("user_id", msg.PlatformUserID),
		slog.String("conversation_id", msg.ConversationID))

	errorMsg := fmt.Sprintf("抱歉，处理您的请求时出现错误：%v", err)
	util.Logger.Info("gateway: preparing error reply",
		slog.String("channel", ch.Name()),
		slog.String("error_message", errorMsg),
		slog.Int("message_length", len(errorMsg)))

	replyCtx, replyCancel := context.WithTimeout(
		context.Background(),
		30*time.Second,
	)
	defer replyCancel()

	util.Logger.Debug("gateway: creating error event channel")
	events := make(chan *event.Event, 1)
	events <- &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Delta: model.Message{
						Content: errorMsg,
					},
				},
			},
		},
	}
	close(events)
	util.Logger.Debug("gateway: error event channel ready, sending reply")

	if sendErr := ch.SendReply(replyCtx, msg, events); sendErr != nil {
		util.Logger.Error("gateway: send error reply failed",
			slog.String("channel", ch.Name()),
			slog.String("user_id", msg.PlatformUserID),
			slog.String("conversation_id", msg.ConversationID),
			slog.String("error", sendErr.Error()))
	} else {
		util.Logger.Info("gateway: error reply sent successfully",
			slog.String("channel", ch.Name()),
			slog.String("user_id", msg.PlatformUserID))
	}
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
