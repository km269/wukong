package clone

import (
	"net"
	"sync"
	"testing"
	"time"
)

// fakeIPStore builds an ipPenaltyStore whose resolver maps hosts to
// fixed IPs — no real DNS.
func fakeIPStore(v4, v6 int, table map[string][]string) *ipPenaltyStore {
	s := newIPPenaltyStore(v4, v6)
	s.resolve = func(host string) ([]net.IP, error) {
		addrs, ok := table[host]
		if !ok {
			return nil, &net.DNSError{Err: "no such host", Name: host}
		}
		ips := make([]net.IP, 0, len(addrs))
		for _, a := range addrs {
			if ip := net.ParseIP(a); ip != nil {
				ips = append(ips, ip)
			}
		}
		if len(ips) == 0 {
			return nil, &net.DNSError{Err: "no addresses", Name: host}
		}
		return ips, nil
	}
	return s
}

func TestIPSegmentKey(t *testing.T) {
	s := newIPPenaltyStore(24, 64)

	cases := []struct {
		ip   string
		want string
	}{
		{"1.2.3.4", "1.2.3.0/24"},
		{"1.2.3.99", "1.2.3.0/24"},                    // same /24
		{"1.2.4.4", "1.2.4.0/24"},                     // adjacent /24 differs
		{"2001:db8:1:2:3:4:5:6", "2001:db8:1:2::/64"}, // v6 /64
		{"2001:db8:1:2:ffff::1", "2001:db8:1:2::/64"}, // same /64
		{"2001:db8:1:3::1", "2001:db8:1:3::/64"},      // different /64
		{"::ffff:1.2.3.4", "1.2.3.0/24"},              // v4-mapped treated as v4
	}
	for _, tc := range cases {
		if got := s.ipSegmentKey(net.ParseIP(tc.ip)); got != tc.want {
			t.Errorf("ipSegmentKey(%s) = %q, want %q", tc.ip, got, tc.want)
		}
	}

	// Prefix length customization: /32 = exact IP, /48 v6.
	s32 := newIPPenaltyStore(32, 48)
	if got := s32.ipSegmentKey(net.ParseIP("1.2.3.4")); got != "1.2.3.4/32" {
		t.Errorf("/32 key = %q", got)
	}
	if got := s32.ipSegmentKey(net.ParseIP("2001:db8:1:2:3:4:5:6")); got != "2001:db8:1::/48" {
		t.Errorf("v6 /48 key = %q", got)
	}

	// Out-of-range bits fall back to defaults (24/64).
	sBad := newIPPenaltyStore(99, -5)
	if got := sBad.ipSegmentKey(net.ParseIP("1.2.3.4")); got != "1.2.3.0/24" {
		t.Errorf("bad v4 bits fallback = %q", got)
	}
	if got := sBad.ipSegmentKey(net.ParseIP("2001:db8:1:2::1")); got != "2001:db8:1:2::/64" {
		t.Errorf("bad v6 bits fallback = %q", got)
	}

	if got := s.ipSegmentKey(nil); got != "" {
		t.Errorf("nil IP key = %q, want empty", got)
	}
}

func TestRateLimiter_RaiseTo(t *testing.T) {
	rl := NewRateLimiter(100 * time.Millisecond)

	if got := rl.RaiseTo(50 * time.Millisecond); got != 100*time.Millisecond {
		t.Errorf("RaiseTo below current must be a no-op, got %v", got)
	}
	if got := rl.RaiseTo(2 * time.Second); got != 2*time.Second {
		t.Errorf("RaiseTo floor = %v, want 2s", got)
	}
	// Raised interval sticks for subsequent Wait scheduling.
	if got := rl.Interval(); got != 2*time.Second {
		t.Errorf("Interval after RaiseTo = %v, want 2s", got)
	}
	// SlowDown still works from the raised floor (×2 of 2s, capped).
	if got := rl.SlowDown(2, 30*time.Second); got != 4*time.Second {
		t.Errorf("SlowDown after RaiseTo = %v, want 4s", got)
	}
}

func TestIPPenaltyStore_Propagation(t *testing.T) {
	// cdn1 and cdn2 share 203.0.113.0/24; other.example.net is elsewhere.
	s := fakeIPStore(24, 64, map[string][]string{
		"cdn1.example.com":   {"203.0.113.7"},
		"cdn2.example.com":   {"203.0.113.99"},
		"other.example.net":  {"198.51.100.1"},
		"v6alias.example.io": {"2001:db8:9:a::1"},
		"v6same.example.io":  {"2001:db8:9:a::2"},
	})

	// Happy path before any penalty: zero cost, no resolve needed.
	if got := s.segmentFloor("cdn1.example.com"); got != 0 {
		t.Fatalf("floor before any penalty = %v, want 0", got)
	}

	// Penalize cdn1 → its /24 segment records 2s.
	s.penalize("cdn1.example.com", 2*time.Second)

	if got := s.segmentFloor("cdn2.example.com"); got != 2*time.Second {
		t.Errorf("sibling host floor = %v, want 2s (CDN alias propagation)", got)
	}
	if got := s.segmentFloor("other.example.net"); got != 0 {
		t.Errorf("unrelated host floor = %v, want 0", got)
	}

	// Penalties only ever widen: a smaller strike must not lower it.
	s.penalize("cdn2.example.com", 500*time.Millisecond)
	if got := s.segmentFloor("cdn1.example.com"); got != 2*time.Second {
		t.Errorf("floor after weaker strike = %v, want 2s (never lowers)", got)
	}
	// A stronger strike raises it.
	s.penalize("cdn2.example.com", 4*time.Second)
	if got := s.segmentFloor("cdn1.example.com"); got != 4*time.Second {
		t.Errorf("floor after stronger strike = %v, want 4s", got)
	}

	// IPv6 propagation within /64.
	s.penalize("v6alias.example.io", 1*time.Second)
	if got := s.segmentFloor("v6same.example.io"); got != 1*time.Second {
		t.Errorf("v6 sibling floor = %v, want 1s", got)
	}

	// Unresolvable host: no panic, no penalty recorded.
	s.penalize("does-not-resolve.example", 2*time.Second)
	if got := s.segmentFloor("cdn1.example.com"); got != 4*time.Second {
		t.Errorf("floor after unresolvable penalize = %v, want 4s", got)
	}
}

func TestPenalizeHost_IPSegmentWiring(t *testing.T) {
	ec := &EnhancedCloner{
		assetRateLimiters: make(map[string]*RateLimiter),
		ipPenalties: fakeIPStore(24, 64, map[string][]string{
			"cdn1.example.com": {"203.0.113.7"},
			"cdn2.example.com": {"203.0.113.99"},
		}),
	}

	// cdn1 strikes: host bucket 100ms → 200ms, segment records 200ms.
	ec.penalizeHost("https://cdn1.example.com/a.png", 429)
	if got := ec.hostLimiter("cdn1.example.com").Interval(); got != 200*time.Millisecond {
		t.Fatalf("cdn1 interval = %v, want 200ms", got)
	}

	// Sibling cdn2 has its own fresh bucket (100ms) but the segment
	// floor raises it to 200ms at wait time.
	rl2 := ec.hostLimiter("cdn2.example.com")
	if got := rl2.Interval(); got != 100*time.Millisecond {
		t.Fatalf("cdn2 own interval before raise = %v, want 100ms", got)
	}
	floor := ec.ipPenalties.segmentFloor("cdn2.example.com")
	if floor != 200*time.Millisecond {
		t.Fatalf("cdn2 segment floor = %v, want 200ms", floor)
	}
	rl2.RaiseTo(floor)
	if got := rl2.Interval(); got != 200*time.Millisecond {
		t.Fatalf("cdn2 interval after RaiseTo = %v, want 200ms", got)
	}

	// Disabled store (nil): penalizeHost skips propagation entirely.
	ec2 := &EnhancedCloner{
		assetRateLimiters: make(map[string]*RateLimiter),
		ipPenalties:       nil,
	}
	ec2.penalizeHost("https://cdn1.example.com/b.png", 429)
	if got := ec2.hostLimiter("cdn1.example.com").Interval(); got != 200*time.Millisecond {
		t.Fatalf("nil-store interval = %v, want 200ms (host penalty still applies)", got)
	}
}

func TestIPPenaltyStore_Concurrent(t *testing.T) {
	table := map[string][]string{
		"a.example.com": {"203.0.113.1"},
		"b.example.com": {"203.0.113.2"},
		"c.example.com": {"198.51.100.3"},
	}
	ec := &EnhancedCloner{
		assetRateLimiters: make(map[string]*RateLimiter),
		ipPenalties:       fakeIPStore(24, 64, table),
	}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			host := []string{"a.example.com", "b.example.com", "c.example.com"}[i%3]
			ec.penalizeHost("https://"+host+"/x.js", 429)
			_ = ec.ipPenalties.segmentFloor(host)
			_ = ec.hostLimiter(host).Interval()
		}(i)
	}
	wg.Wait()

	// a and b share a segment and each took ~10 strikes in the storm:
	// 100ms ×2^10 is far past the 30s cap, so both segments sit at cap.
	for _, host := range []string{"a.example.com", "b.example.com"} {
		if got := ec.ipPenalties.segmentFloor(host); got != 30*time.Second {
			t.Errorf("%s floor = %v, want 30s cap", host, got)
		}
	}
}
