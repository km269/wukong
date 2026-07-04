// Package gateway — GatewayServer
//
// GatewayServer is the hub that drives all registered Channels. It is
// transport-agnostic: there is no HTTP listener. Each Channel owns its
// own inbound transport (e.g. the Feishu WebSocket long-connection) and
// pushes normalized *GatewayMessage values into the shared processing
// pipeline via the MessageHandler that GatewayServer supplies.
//
// The pipeline runs the cross-cutting concerns that are independent of
// how a message was received:
//
//  1. Deduplicate by MessageID (platform SDK retries are dropped)
//  2. Build Wukong user/session identifiers via the Channel
//  3. Rate-limit per user + global concurrency gate
//  4. Ensure session mapping persistence
//  5. Run the agent loop (in a background goroutine)
//  6. Send the reply back to the platform (agent errors become a
//     user-facing error message rather than a silent failure)
//
// Start(ctx) blocks until ctx is cancelled; Stop tears down channels by
// cancelling that context. The Lark SDK (and any well-behaved Channel)
// handles reconnection/heartbeat internally while its Start call runs.
package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/km269/wukong/internal/util"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// tracerName is the OTel tracer name for gateway spans, mirroring the
// convention used by internal/agent ("wukong/agent").
const tracerName = "wukong/gateway"

// AgentRunner is the gateway's view of the agent loop. It is satisfied
// by *agent.CoreLoop, but is declared here (using only the external
// trpc-agent-go model/event types) so the gateway package does not
// import internal/agent — that would create an import cycle
// (config → gateway → agent → config).
type AgentRunner interface {
	Run(ctx context.Context, userID, sessionID string,
		message model.Message) (<-chan *event.Event, error)
}

// GatewayServer is the transport-agnostic hub that drives all
// registered Channels. It owns the shared pipeline (dedup, rate
// limiting, session mapping, agent execution, reply dispatch).
type GatewayServer struct {
	mu        sync.RWMutex
	cfg       *GatewayConfig
	coreLoop  AgentRunner
	sessStore *GatewaySessionStore
	dedup     *MessageDeduplicator
	ratelimit *RateLimiter

	// channels is the set of registered platform adapters, keyed by
	// Channel.Name(). Registration is only permitted before Start.
	channels map[string]Channel

	// running flips to true once Start is called; further channel
	// registrations are rejected while it stays true.
	running bool

	// runCancel, when set, cancels the context passed to every
	// channel's Start, which is how Stop tears them down.
	runCancel context.CancelFunc

	// wg tracks per-channel Start goroutines so Stop can wait for
	// them to return before reporting shutdown complete.
	wg sync.WaitGroup
}

// NewGatewayServer creates a new GatewayServer with the given gateway
// configuration and dependencies.
//
//   - cfg:        the gateway section of the root config (owned by the
//     gateway package; the root config embeds it).
//   - loop:       the agent loop (typically *agent.CoreLoop) used to run
//     each inbound message. May be nil in tests; dispatch then skips
//     the agent run.
//   - store:      the gateway session store (platform<->wukong id
//     mapping). A nil-pool store degrades to in-memory, which is safe.
func NewGatewayServer(
	cfg *GatewayConfig,
	loop AgentRunner,
	store *GatewaySessionStore,
) *GatewayServer {
	if cfg == nil {
		// Defensive: a zero config still works, using field defaults.
		c := GatewayConfig{}
		cfg = &c
	}

	// Message dedup (5 min TTL by default).
	dedup := NewMessageDeduplicator(cfg.MessageDedupTTL)

	// Rate limiter: per-user sliding window + concurrency cap.
	rateLimitWindow := cfg.RateLimitWindow
	if rateLimitWindow <= 0 {
		rateLimitWindow = 10 * time.Second
	}
	rateLimitPerUser := cfg.RateLimitPerUser
	if rateLimitPerUser <= 0 {
		rateLimitPerUser = 10
	}
	ratelimit := NewRateLimiter(
		rateLimitWindow,
		rateLimitPerUser,
		cfg.MaxConcurrentSessions,
	)

	return &GatewayServer{
		cfg:       cfg,
		coreLoop:  loop,
		sessStore: store,
		dedup:     dedup,
		ratelimit: ratelimit,
		channels:  make(map[string]Channel),
	}
}

// Start launches every registered channel's inbound transport and then
// blocks until ctx is cancelled (or a fatal channel error occurs). For
// each channel it spawns a goroutine that calls channel.Start(ctx,
// gs.dispatch); the SDK is expected to handle reconnection/heartbeat
// internally. At least one channel must be registered beforehand.
//
// It is an error to call Start with no channels registered, since a
// gateway with nothing to listen on is almost certainly a
// misconfiguration.
func (gs *GatewayServer) Start(ctx context.Context) error {
	gs.mu.Lock()
	if gs.running {
		gs.mu.Unlock()
		return fmt.Errorf("gateway: already running")
	}
	if len(gs.channels) == 0 {
		gs.mu.Unlock()
		return fmt.Errorf("gateway: no channels registered")
	}

	runCtx, cancel := context.WithCancel(ctx)
	gs.runCancel = cancel
	gs.running = true
	channels := make([]Channel, 0, len(gs.channels))
	for _, ch := range gs.channels {
		channels = append(channels, ch)
	}
	gs.mu.Unlock()

	util.Logger.Info("gateway: starting",
		slog.Int("channels", len(channels)),
		slog.Duration("default_timeout", gs.cfg.DefaultTimeout))

	// Validate all channels before starting. A failed validation
	// prevents startup and returns an error. Only channels that
	// implement the optional Validate interface are checked.
	for _, ch := range channels {
		if validator, ok := ch.(interface{ Validate() error }); ok {
			if err := validator.Validate(); err != nil {
				cancel()
				gs.mu.Lock()
				gs.running = false
				gs.runCancel = nil
				gs.mu.Unlock()
				return fmt.Errorf("gateway: channel validation failed: %w", err)
			}
		}
	}

	// errCh collects the first fatal error from any channel; a nil
	// result just means that channel exited cleanly (rare before ctx
	// cancellation, but tolerated).
	errCh := make(chan error, len(channels))
	for _, ch := range channels {
		gs.wg.Add(1)
		go func(ch Channel) {
			defer gs.wg.Done()
			util.Logger.Info("gateway: channel starting",
				slog.String("channel", ch.Name()))
			err := ch.Start(runCtx, gs.dispatch)
			if err != nil && err != context.Canceled {
				util.Logger.Warn("gateway: channel exited with error",
					slog.String("channel", ch.Name()),
					slog.String("error", err.Error()))
			}
			errCh <- err
		}(ch)
	}

	// Block until the caller cancels (graceful shutdown) or every
	// channel has returned. Returning here lets the caller proceed;
	// Stop is still required to finalize state.
	select {
	case <-runCtx.Done():
		return runCtx.Err()
	case err := <-errCh:
		// One channel failed first; cancel the rest so they tear down.
		cancel()
		return err
	}
}

// Stop gracefully shuts down the gateway. It cancels the run context
// (which causes every channel's Start to return), waits for those
// goroutines, then stops the background workers (dedup, rate limiter)
// and the session store. Safe to call when not running (no-op).
func (gs *GatewayServer) Stop(ctx context.Context) error {
	gs.mu.Lock()
	running := gs.running
	cancel := gs.runCancel
	gs.running = false
	gs.runCancel = nil
	gs.mu.Unlock()

	if !running || cancel == nil {
		return nil
	}

	util.Logger.Info("gateway: shutting down")
	cancel()

	// Wait for channel goroutines with a bounded patience so a wedged
	// SDK can't hang shutdown indefinitely.
	waitDone := make(chan struct{})
	go func() {
		gs.wg.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-ctx.Done():
		util.Logger.Warn("gateway: channel shutdown timed out")
	}

	gs.dedup.Stop()
	gs.ratelimit.Stop()
	if gs.sessStore != nil {
		_ = gs.sessStore.Close()
	}
	return nil
}

// RegisterChannel registers a platform channel. It must be called
// before Start; registering after the gateway is running (or a nil
// channel) is rejected with an error.
func (gs *GatewayServer) RegisterChannel(ch Channel) error {
	if ch == nil {
		return fmt.Errorf("gateway: cannot register nil channel")
	}
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if gs.running {
		return fmt.Errorf("gateway: cannot register channel %q after start",
			ch.Name())
	}
	if _, exists := gs.channels[ch.Name()]; exists {
		return fmt.Errorf("gateway: channel %q already registered",
			ch.Name())
	}
	gs.channels[ch.Name()] = ch
	return nil
}

// channelByName returns the registered channel with the given name, or
// nil if none matches. The caller must not hold gs.mu.
func (gs *GatewayServer) channelByName(name string) Channel {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	return gs.channels[name]
}

// Channels returns a snapshot of the names of all registered channels.
// The slice is a copy and is safe for the caller to retain.
func (gs *GatewayServer) Channels() []string {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	names := make([]string, 0, len(gs.channels))
	for name := range gs.channels {
		names = append(names, name)
	}
	return names
}

// IsRunning returns whether the gateway is currently running.
func (gs *GatewayServer) IsRunning() bool {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	return gs.running
}

// dispatch is the MessageHandler supplied to every channel. It runs the
// shared pipeline for a single inbound message.
//
// The agent run executes in a background goroutine so this function
// returns quickly and does not stall the channel's receive loop. The
// concurrency slot (acquired from the rate limiter) and the session
// timeout are owned by that goroutine and released when it finishes.
func (gs *GatewayServer) dispatch(ctx context.Context, msg *GatewayMessage) {
	if msg == nil {
		return
	}

	// Trace the full pipeline for this inbound message. The span covers
	// dedup → id building → rate limit → session → async agent run, so
	// a gateway message can be followed end-to-end in a trace.
	ctx, span := otel.Tracer(tracerName).Start(ctx, "gateway.dispatch",
		trace.WithAttributes(
			attribute.String("channel", msg.Platform),
			attribute.String("message_id", msg.MessageID),
		),
	)
	defer span.End()

	ch := gs.channelByName(msg.Platform)
	if ch == nil {
		util.Logger.Warn("gateway: no channel for platform",
			slog.String("platform", msg.Platform))
		return
	}

	// Step 1: Deduplicate. Platform retries with the same MessageID
	// are dropped silently to avoid hitting the agent loop twice.
	if gs.dedup.IsDuplicate(msg.Platform, msg.MessageID) {
		util.Logger.Debug("gateway: duplicate message dropped",
			slog.String("channel", msg.Platform),
			slog.String("message_id", msg.MessageID))
		return
	}

	// Step 2: Build Wukong identifiers from the platform message.
	userID := ch.BuildUserID(msg)
	sessionID := ch.BuildSessionID(msg)

	// Step 3: Rate limit (per-user window + global concurrency gate).
	// The release func is held until the agent run finishes.
	// AllowCtx honours ctx so that a saturated gateway no longer parks
	// the platform SDK's receive goroutine: when ctx is cancelled (e.g.
	// gateway shutdown) the waiter returns promptly instead of blocking
	// all subsequent message reception.
	release, allowed := gs.ratelimit.AllowCtx(ctx, msg.Platform, userID)
	if !allowed {
		// Distinguish a per-user rate rejection from a ctx-cancellation
		// (shutdown) so operators can tell overload apart from shutdown.
		if ctx.Err() != nil {
			util.Logger.Debug("gateway: rate-limit wait cancelled by context",
				slog.String("channel", msg.Platform),
				slog.String("user", userID),
				slog.String("ctx_err", ctx.Err().Error()))
		} else {
			util.Logger.Warn("gateway: rate limit exceeded",
				slog.String("channel", msg.Platform),
				slog.String("user", userID))
		}
		return
	}

	// Step 4: Ensure session mapping persistence (non-fatal on error).
	if gs.sessStore != nil {
		if _, err := gs.sessStore.GetOrCreateSession(
			ch.Name(), msg.PlatformUserID, msg.ConversationID,
			userID, sessionID,
		); err != nil {
			util.Logger.Warn("gateway: session mapping failed",
				slog.String("error", err.Error()))
		}
	}

	util.Logger.Info("gateway: processing message",
		slog.String("channel", ch.Name()),
		slog.String("user", userID),
		slog.String("session", sessionID),
		slog.Int("content_len", len(msg.Content)))

	// Step 5 + 6: Run the agent and send the reply in a background
	// goroutine so dispatch returns promptly to the channel's receive
	// loop.
	go gs.runAndReply(ctx, ch, msg, userID, sessionID, release)
}

// runAndReply runs the agent loop for one message and dispatches the
// reply. It owns the concurrency slot (release) and must call it when
// done. Agent failures are turned into a user-facing error reply so the
// user is never left without feedback.
func (gs *GatewayServer) runAndReply(
	ctx context.Context,
	ch Channel,
	msg *GatewayMessage,
	userID, sessionID string,
	release func(),
) {
	defer release()

	// Trace the agent run + reply. This is a child of dispatch's span
	// (ctx is propagated), so a message's full path is one trace.
	runCtx, span := otel.Tracer(tracerName).Start(ctx, "gateway.runAndReply",
		trace.WithAttributes(
			attribute.String("channel", ch.Name()),
			attribute.String("user_id", userID),
			attribute.String("session_id", sessionID),
		),
	)
	defer span.End()

	// No core loop (e.g. in some tests): nothing to run.
	if gs.coreLoop == nil {
		util.Logger.Debug("gateway: no core loop, skipping agent run",
			slog.String("channel", ch.Name()))
		return
	}

	timeout := gs.cfg.DefaultTimeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	timeoutCtx, cancel := context.WithTimeout(runCtx, timeout)
	defer cancel()

	agentMsg := model.NewUserMessage(msg.Content)
	events, err := gs.coreLoop.Run(timeoutCtx, userID, sessionID, agentMsg)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		util.Logger.Error("gateway: agent run failed",
			slog.String("channel", ch.Name()),
			slog.String("error", err.Error()))
		gs.sendErrorReply(ctx, ch, msg, err)
		return
	}

	// Send the reply; this may stream a card depending on channel mode.
	replyCtx, replyCancel := context.WithTimeout(context.Background(), timeout)
	defer replyCancel()
	if err := ch.SendReply(replyCtx, msg, events); err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		util.Logger.Error("gateway: send reply failed",
			slog.String("channel", ch.Name()),
			slog.String("error", err.Error()))
	}
}

// sendErrorReply sends a user-friendly error message back to the
// platform when agent execution fails. This ensures users receive
// feedback instead of seeing no response at all — which is the bug
// this gateway rewrite exists to fix.
func (gs *GatewayServer) sendErrorReply(
	_ context.Context, ch Channel, msg *GatewayMessage, err error,
) {
	errorMsg := fmt.Sprintf("抱歉，处理您的请求时出现错误：%v", err)

	replyCtx, replyCancel := context.WithTimeout(
		context.Background(), 30*time.Second,
	)
	defer replyCancel()

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

	if sendErr := ch.SendReply(replyCtx, msg, events); sendErr != nil {
		util.Logger.Error("gateway: send error reply failed",
			slog.String("channel", ch.Name()),
			slog.String("error", sendErr.Error()))
	}
}
