package gateway

import (
	"context"
	"sync"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
)

// fakeChannel is a minimal Channel implementation for testing the
// gateway's dispatch pipeline without a real platform transport or
// CoreLoop.
type fakeChannel struct {
	name     string
	replies  []*GatewayMessage
	mu       sync.Mutex
	stopOnce sync.Once
	stopped  bool
}

func (f *fakeChannel) Name() string { return f.name }

func (f *fakeChannel) Start(ctx context.Context, _ MessageHandler) error {
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeChannel) Stop(_ context.Context) error {
	f.stopOnce.Do(func() { f.stopped = true })
	return nil
}

func (f *fakeChannel) BuildUserID(msg *GatewayMessage) string {
	return f.name + ":" + msg.PlatformUserID
}

func (f *fakeChannel) BuildSessionID(msg *GatewayMessage) string {
	return f.name + "-" + msg.ConversationID
}

func (f *fakeChannel) SendReply(
	_ context.Context, msg *GatewayMessage, _ <-chan *event.Event,
) error {
	f.mu.Lock()
	f.replies = append(f.replies, msg)
	f.mu.Unlock()
	return nil
}

// newTestGateway builds a GatewayServer with a fake channel and
// nil-safe shared infra (no DB, no CoreLoop). dispatch exercises only
// the dedup / id-building / rate-limit / session-map steps; the agent
// run path is avoided by making dispatch find the channel and stop at
// the goroutine boundary (which needs a CoreLoop, so tests below only
// cover the synchronous pre-agent steps).
func newTestGateway(t *testing.T) (*GatewayServer, *fakeChannel) {
	t.Helper()
	cfg := &GatewayConfig{
		Enabled:               true,
		RateLimitPerUser:      5,
		RateLimitWindow:       time.Minute,
		MaxConcurrentSessions: 10,
		MessageDedupTTL:       5 * time.Minute,
	}
	// sessStore with nil pool degrades to in-memory (no persistence),
	// which is fine for these tests.
	gs := NewGatewayServer(cfg, nil, NewGatewaySessionStore(nil))
	fc := &fakeChannel{name: "fake"}
	if err := gs.RegisterChannel(fc); err != nil {
		t.Fatalf("register: %v", err)
	}
	return gs, fc
}

// TestRegisterChannelRejectsAfterStart ensures channels cannot be
// registered once the gateway is running.
func TestRegisterChannelRejectsAfterStart(t *testing.T) {
	gs, _ := newTestGateway(t)
	// Simulate running state without actually starting (Start blocks).
	gs.mu.Lock()
	gs.running = true
	gs.mu.Unlock()
	defer func() { gs.running = false }()

	err := gs.RegisterChannel(&fakeChannel{name: "second"})
	if err == nil {
		t.Fatal("expected error registering after start")
	}
}

// TestRegisterChannelRejectsNil guards against nil registration.
func TestRegisterChannelRejectsNil(t *testing.T) {
	gs, _ := newTestGateway(t)
	if err := gs.RegisterChannel(nil); err == nil {
		t.Fatal("expected error registering nil channel")
	}
}

// TestDedupDropsDuplicate exercises the dedup step directly: a second
// dispatch with the same MessageID is dropped before reaching the
// agent. We call dispatch and rely on the agent goroutine to no-op
// (coreLoop is nil; the goroutine logs the error and returns without
// blocking the test because the slot is released via defer).
func TestDedupDropsDuplicate(t *testing.T) {
	gs, _ := newTestGateway(t)
	msg := &GatewayMessage{
		Platform:       "fake",
		PlatformUserID: "u1",
		ConversationID: "c1",
		MessageID:      "m1",
		Content:        "hi",
	}

	// First dispatch: not a duplicate.
	if gs.dedup.IsDuplicate(msg.Platform, msg.MessageID) {
		t.Fatal("first IsDuplicate should be false")
	}
	// Second check with same ID must be flagged as duplicate.
	if !gs.dedup.IsDuplicate(msg.Platform, msg.MessageID) {
		t.Fatal("second IsDuplicate should be true")
	}
}

// TestChannelByName verifies channel lookup used by dispatch.
func TestChannelByName(t *testing.T) {
	gs, fc := newTestGateway(t)
	if got := gs.channelByName("fake"); got != fc {
		t.Error("expected to find the fake channel")
	}
	if got := gs.channelByName("missing"); got != nil {
		t.Errorf("expected nil for missing channel, got %v", got)
	}
}

// TestChannelsListsNames verifies the Channels() snapshot.
func TestChannelsListsNames(t *testing.T) {
	gs, _ := newTestGateway(t)
	names := gs.Channels()
	if len(names) != 1 || names[0] != "fake" {
		t.Errorf("expected [fake], got %v", names)
	}
}

// TestStartRejectsEmpty ensures Start fails fast with no channels.
func TestStartRejectsEmpty(t *testing.T) {
	cfg := &GatewayConfig{}
	gs := NewGatewayServer(cfg, nil, NewGatewaySessionStore(nil))
	err := gs.Start(context.Background())
	if err == nil {
		t.Fatal("expected error starting with no channels")
	}
}

// TestStopIdempotent ensures Stop is safe to call when not running.
func TestStopIdempotent(t *testing.T) {
	gs, _ := newTestGateway(t)
	// Not running yet; Stop should be a no-op.
	if err := gs.Stop(context.Background()); err != nil {
		t.Fatalf("stop when not running: %v", err)
	}
}
