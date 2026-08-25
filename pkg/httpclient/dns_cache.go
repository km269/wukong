package httpclient

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/km269/wukong/pkg/logutil"
)

type DNSCache struct {
	mu       sync.RWMutex
	entries  map[string]*dnsEntry
	ttl      time.Duration
	resolver *net.Resolver
}

type dnsEntry struct {
	addrs    []string
	expireAt time.Time
}

func NewDNSCache(ttl time.Duration) *DNSCache {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &DNSCache{
		entries:  make(map[string]*dnsEntry),
		ttl:      ttl,
		resolver: net.DefaultResolver,
	}
}

func (c *DNSCache) Lookup(host string) ([]string, error) {
	c.mu.RLock()
	entry, ok := c.entries[host]
	c.mu.RUnlock()

	if ok && time.Now().Before(entry.expireAt) {
		return entry.addrs, nil
	}

	addrs, err := c.resolver.LookupHost(context.Background(), host)
	if err != nil {
		logutil.Debug("[dns] lookup failed", "host", host, "error", err)
		return nil, err
	}

	c.mu.Lock()
	c.entries[host] = &dnsEntry{
		addrs:    addrs,
		expireAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()

	logutil.Debug("[dns] cached lookup", "host", host, "addrs", len(addrs))
	return addrs, nil
}

func (c *DNSCache) DialContext(dialer *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}

		addrs, err := c.Lookup(host)
		if err != nil {
			return nil, err
		}

		if len(addrs) == 0 {
			return nil, &net.DNSError{Err: "no addresses", Name: host}
		}

		resolvedAddr := net.JoinHostPort(addrs[0], port)
		return dialer.DialContext(ctx, network, resolvedAddr)
	}
}

// WrapDialContext wraps an existing DialContext function with DNS caching.
// It first checks the cache for a resolved IP, then falls back to the
// underlying dial (which may itself do DNS resolution with fallback logic).
func (c *DNSCache) WrapDialContext(underlying func(ctx context.Context, network, addr string) (net.Conn, error)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return underlying(ctx, network, addr)
		}

		// If the host is already an IP, just dial through.
		if net.ParseIP(host) != nil {
			return underlying(ctx, network, addr)
		}

		// Try a cached lookup first — if we have a recent result, use it
		// directly with the underlying dialer via IP.
		if addrs, lookupErr := c.Lookup(host); lookupErr == nil && len(addrs) > 0 {
			resolvedAddr := net.JoinHostPort(addrs[0], port)
			if conn, dialErr := underlying(ctx, network, resolvedAddr); dialErr == nil {
				return conn, nil
			}
			// Cached IP didn't work — invalidate and fall back below.
			c.mu.Lock()
			delete(c.entries, host)
			c.mu.Unlock()
		}

		// Otherwise let the underlying dialer handle the full resolve + dial
		// (which includes public DNS fallback on failure).
		return underlying(ctx, network, addr)
	}
}

func (c *DNSCache) Cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for host, entry := range c.entries {
		if now.After(entry.expireAt) {
			delete(c.entries, host)
		}
	}
}
