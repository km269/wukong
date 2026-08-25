package clone

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestBuildRateWhitelist(t *testing.T) {
	if got := buildRateWhitelist(nil); got != nil {
		t.Fatalf("nil input must yield nil set, got %v", got)
	}
	if got := buildRateWhitelist([]string{"", "  "}); got != nil {
		t.Fatalf("all-empty input must yield nil set, got %v", got)
	}

	set := buildRateWhitelist([]string{"CDN.Example.COM", "localhost:3000", " intra.example.org "})
	for _, want := range []string{"cdn.example.com", "localhost:3000", "intra.example.org"} {
		if _, ok := set[want]; !ok {
			t.Errorf("set missing %q", want)
		}
	}
	// Ported entries must NOT leak a portless form (that would exempt
	// every port on the hostname).
	if _, ok := set["localhost"]; ok {
		t.Error(`"localhost:3000" entry must not add portless "localhost"`)
	}

	// A portless entry does match any port — via hostRateExempt.
	ec := &EnhancedCloner{rateWhitelist: buildRateWhitelist([]string{"localhost"})}
	if !ec.hostRateExempt("localhost:8080") {
		t.Error(`portless entry "localhost" must exempt "localhost:8080"`)
	}
}

func TestHostRateExempt(t *testing.T) {
	ec := &EnhancedCloner{
		assetRateLimiters: make(map[string]*RateLimiter),
		rateWhitelist:     buildRateWhitelist([]string{"CDN.Example.COM", "localhost:3000"}),
	}

	cases := []struct {
		host string
		want bool
	}{
		{"cdn.example.com", true},               // exact, lowercase
		{"CDN.Example.COM", true},               // case-insensitive request host
		{"localhost:3000", true},                // exact with port
		{"localhost:8080", false},               // port mismatch, no portless localhost entry
		{"cdn.example.com:443", true},           // portless entry matches any port
		{"other.example.com", false},            // not whitelisted
		{"evilcdn.example.com.attacker", false}, // no suffix matching
	}
	for _, tc := range cases {
		if got := ec.hostRateExempt(tc.host); got != tc.want {
			t.Errorf("hostRateExempt(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}

	// Empty whitelist: nothing exempt.
	ec2 := &EnhancedCloner{assetRateLimiters: make(map[string]*RateLimiter)}
	if ec2.hostRateExempt("cdn.example.com") {
		t.Error("empty whitelist must not exempt anything")
	}
}

func TestWaitAssetRateLimit_WhitelistedSkipsBucket(t *testing.T) {
	ec := &EnhancedCloner{
		assetRateLimiters: make(map[string]*RateLimiter),
		rateWhitelist:     buildRateWhitelist([]string{"localhost:3000"}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// 10 sequential requests to a whitelisted host with a 10s base
	// interval must all pass instantly — no limiter is ever created.
	for i := 0; i < 10; i++ {
		if err := ec.waitAssetRateLimit(ctx, "http://localhost:3000/a.js"); err != nil {
			t.Fatalf("whitelisted request %d blocked: %v", i, err)
		}
	}
	if _, ok := ec.assetRateLimiters["localhost:3000"]; ok {
		t.Error("whitelisted host must not create a limiter")
	}

	// Non-whitelisted host with CrawlDelay 300ms: the second request
	// must be throttled by the bucket.
	ec.opts.CrawlDelay = 300 * time.Millisecond
	rl := ec.hostLimiter("cdn.example.com")
	rl.limiter.Allow() // burn burst
	start := time.Now()
	if err := ec.waitAssetRateLimit(context.Background(), "https://cdn.example.com/a.css"); err != nil {
		t.Fatal(err)
	}
	if waited := time.Since(start); waited < 150*time.Millisecond {
		t.Fatalf("non-whitelisted Wait returned after %v — bucket not enforced", waited)
	}
}

func TestPenalizeHost_WhitelistImmune(t *testing.T) {
	ec := &EnhancedCloner{
		assetRateLimiters: make(map[string]*RateLimiter),
		rateWhitelist:     buildRateWhitelist([]string{"localhost:3000"}),
	}

	// Burn the whitelist guard, then penalize — must be a no-op.
	ec.penalizeHost("http://localhost:3000/rate-limited.js", 429)
	if _, ok := ec.assetRateLimiters["localhost:3000"]; ok {
		t.Fatal("penalizeHost must not create a limiter for whitelisted host")
	}

	// Control: non-whitelisted host is penalized as before.
	ec.penalizeHost("https://cdn.example.com/a.png", 429)
	if got := ec.hostLimiter("cdn.example.com").Interval(); got != 200*time.Millisecond {
		t.Fatalf("non-whitelisted interval = %v, want 200ms", got)
	}
}

func TestRateLimiter_SlowDown(t *testing.T) {
	rl := NewRateLimiter(100 * time.Millisecond)

	if got := rl.Interval(); got != 100*time.Millisecond {
		t.Fatalf("initial interval = %v, want 100ms", got)
	}

	// Each strike doubles the interval.
	if got := rl.SlowDown(2, 30*time.Second); got != 200*time.Millisecond {
		t.Fatalf("after 1st strike = %v, want 200ms", got)
	}
	if got := rl.SlowDown(2, 30*time.Second); got != 400*time.Millisecond {
		t.Fatalf("after 2nd strike = %v, want 400ms", got)
	}

	// Capped at max.
	rl.SlowDown(2, 30*time.Second) // 800ms
	rl.SlowDown(2, 30*time.Second) // 1.6s
	rl.SlowDown(2, 30*time.Second) // 3.2s
	rl.SlowDown(2, 30*time.Second) // 6.4s
	rl.SlowDown(2, 30*time.Second) // 12.8s
	rl.SlowDown(2, 30*time.Second) // 25.6s
	if got := rl.SlowDown(2, 30*time.Second); got != 30*time.Second {
		t.Fatalf("after 8th strike = %v, want cap 30s", got)
	}
	if got := rl.SlowDown(2, 30*time.Second); got != 30*time.Second {
		t.Fatalf("beyond cap = %v, want 30s", got)
	}
}

func TestRateLimiter_SlowDownAppliesToWait(t *testing.T) {
	// After a strike the limiter must actually throttle: waiting for a
	// token right after burning the burst must take ~ the new interval.
	rl := NewRateLimiter(10 * time.Millisecond)
	if !rl.limiter.Allow() {
		t.Fatal("first token must be allowed (burst)")
	}
	rl.SlowDown(2, 30*time.Second) // now 20ms

	start := time.Now()
	if err := rl.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if waited := time.Since(start); waited < 15*time.Millisecond {
		t.Fatalf("Wait returned after %v — slowdown did not apply", waited)
	}
}

func TestHostLimiter_IndependentBuckets(t *testing.T) {
	ec := &EnhancedCloner{
		assetRateLimiters: make(map[string]*RateLimiter),
	}

	a := ec.hostLimiter("cdn-a.example.com")
	b := ec.hostLimiter("cdn-b.example.com")
	if a == b {
		t.Fatal("different hosts must get different limiters")
	}
	if ec.hostLimiter("cdn-a.example.com") != a {
		t.Fatal("same host must reuse its limiter")
	}

	// Penalizing one host must not affect another.
	ec.penalizeHost("https://cdn-a.example.com/img.png", 429)
	if got := a.Interval(); got != 200*time.Millisecond {
		t.Fatalf("penalized host interval = %v, want 200ms", got)
	}
	if got := b.Interval(); got != 100*time.Millisecond {
		t.Fatalf("other host interval = %v, want default 100ms", got)
	}
}

func TestHostLimiter_ConcurrentAccess(t *testing.T) {
	ec := &EnhancedCloner{
		assetRateLimiters: make(map[string]*RateLimiter),
	}

	var wg sync.WaitGroup
	lims := make(chan *RateLimiter, 64)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			host := fmt.Sprintf("h%d.example.com", i)
			lims <- ec.hostLimiter(host)
			ec.penalizeHost("https://"+host+"/x.png", 429)
		}(i)
	}
	wg.Wait()
	close(lims)

	// No race / panic is the primary assertion; sanity-check that each
	// host (penalized exactly once) landed on the doubled interval.
	for lim := range lims {
		if got := lim.Interval(); got != 200*time.Millisecond {
			t.Fatalf("interval %v, want 200ms after one strike", got)
		}
	}
}
