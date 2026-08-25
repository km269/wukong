package clone

import (
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/km269/wukong/pkg/httpclient"
	"github.com/km269/wukong/pkg/logutil"
)

// ipPenaltyStore implements IP-segment penalty propagation for asset
// rate limiting (P2-4). Per-host token buckets stay authoritative on
// the happy path — DNS is only consulted after a 429/503 penalty (or
// when a penalized segment exists), so the normal download path pays
// nothing.
//
// Flow: when a host is penalized, its IPs are resolved (cached) and
// the segment key (CIDR prefix) of each IP records the new minimum
// interval. Any other host resolving into a penalized segment gets its
// own bucket raised to at least that interval on its next wait
// (RateLimiter.RaiseTo) — catching CDN aliases (cdn1/cdn2 → same /24)
// that per-host buckets would otherwise throttle independently.
type ipPenaltyStore struct {
	mu       sync.Mutex
	segments map[string]time.Duration // segment key -> minimum interval
	v4Bits   int                      // IPv4 prefix length (e.g. 24)
	v6Bits   int                      // IPv6 prefix length (e.g. 64)

	// resolve maps a hostname to its IPs. Backed by httpclient.DNSCache
	// in production; injectable for tests.
	resolve func(host string) ([]net.IP, error)
}

// newIPPenaltyStore creates a store with the given prefix lengths.
// Zero/negative values fall back to the politeness defaults (/24, /64).
func newIPPenaltyStore(v4Bits, v6Bits int) *ipPenaltyStore {
	if v4Bits <= 0 || v4Bits > 32 {
		v4Bits = 24
	}
	if v6Bits <= 0 || v6Bits > 128 {
		v6Bits = 64
	}
	cache := httpclient.NewDNSCache(5 * time.Minute)
	return &ipPenaltyStore{
		segments: make(map[string]time.Duration),
		v4Bits:   v4Bits,
		v6Bits:   v6Bits,
		resolve: func(host string) ([]net.IP, error) {
			addrs, err := cache.Lookup(host)
			if err != nil {
				return nil, err
			}
			ips := make([]net.IP, 0, len(addrs))
			for _, a := range addrs {
				if ip := net.ParseIP(a); ip != nil {
					ips = append(ips, ip)
				}
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("no IP addresses for %s", host)
			}
			return ips, nil
		},
	}
}

// ipSegmentKey returns the CIDR segment key for an IP at the store's
// prefix lengths, e.g. "1.2.3.0/24". IPv4-mapped IPv6 addresses are
// treated as IPv4. Returns "" for the nil IP.
func (s *ipPenaltyStore) ipSegmentKey(ip net.IP) string {
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		seg := v4.Mask(net.CIDRMask(s.v4Bits, 32))
		return seg.String() + "/" + strconv.Itoa(s.v4Bits)
	}
	if v6 := ip.To16(); v6 != nil {
		seg := v6.Mask(net.CIDRMask(s.v6Bits, 128))
		return seg.String() + "/" + strconv.Itoa(s.v6Bits)
	}
	return ""
}

// penalize records a segment penalty for every IP segment a host
// resolves to, raising existing segment intervals to at least
// interval. Resolution failures are logged and skipped — segment
// propagation is best-effort on top of the host-level penalty.
func (s *ipPenaltyStore) penalize(host string, interval time.Duration) {
	ips, err := s.resolve(host)
	if err != nil {
		logutil.Debug("[ip-penalty] resolve failed", "host", host, "error", err)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ip := range ips {
		key := s.ipSegmentKey(ip)
		if key == "" {
			continue
		}
		if cur, ok := s.segments[key]; !ok || interval > cur {
			s.segments[key] = interval
			logutil.Debug("[ip-penalty] segment penalty recorded",
				"segment", key, "min_interval", interval)
		}
	}
}

// segmentFloor returns the strictest (largest) penalized interval
// among the IP segments a host resolves to, or 0 when none of its
// segments are penalized. DNS is only consulted when at least one
// segment penalty exists.
func (s *ipPenaltyStore) segmentFloor(host string) time.Duration {
	s.mu.Lock()
	empty := len(s.segments) == 0
	s.mu.Unlock()
	if empty {
		return 0 // happy path: no penalties recorded anywhere
	}

	ips, err := s.resolve(host)
	if err != nil {
		return 0
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	var floor time.Duration
	for _, ip := range ips {
		key := s.ipSegmentKey(ip)
		if cur, ok := s.segments[key]; ok && cur > floor {
			floor = cur
		}
	}
	return floor
}
