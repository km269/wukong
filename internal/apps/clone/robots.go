// Package clone provides website cloning functionality.
//
// robots.go: robots.txt parser and sitemap discovery.
// Provides polite crawling compliance and automatic sitemap-based
// URL discovery.
package clone

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/temoto/robotstxt"
	"golang.org/x/time/rate"
)

// RobotsRule stores the parsed robots.txt rules for a host.
type RobotsRule struct {
	Host       string
	Group      *robotstxt.Group
	Sitemaps   []string
	FetchedAt  time.Time
	RawContent string
}

// IsAllowed checks whether a URL path is allowed by robots.txt rules.
func (r *RobotsRule) IsAllowed(path string) bool {
	if r.Group == nil {
		return true // No rules, allow everything.
	}
	return r.Group.Test(path)
}

// CrawlDelayDuration returns the crawl delay as a duration, with a minimum.
// Falls back to 100ms if no crawl-delay is specified.
func (r *RobotsRule) CrawlDelayDuration() time.Duration {
	if r.Group == nil {
		return 100 * time.Millisecond
	}
	delay := r.Group.CrawlDelay
	if delay <= 0 {
		return 100 * time.Millisecond
	}
	return delay
}

// FetchRobots downloads and parses robots.txt for a given host.
// Uses the github.com/temoto/robotstxt library for accurate rule matching.
func FetchRobots(ctx context.Context, client *http.Client, host, scheme string) (*RobotsRule, error) {
	if scheme == "" {
		scheme = "https"
	}

	robotsURL := scheme + "://" + host + "/robots.txt"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Wukong-Cloner/2.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch robots.txt: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &RobotsRule{Host: host, FetchedAt: time.Now()}, nil
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read robots.txt: %w", err)
	}

	robotsData, err := robotstxt.FromBytes(data)
	if err != nil {
		// If parsing fails, treat as no restrictions.
		return &RobotsRule{
			Host:      host,
			FetchedAt: time.Now(),
		}, nil
	}

	// Extract the group for our user agent. Try specific UAs, fall back to *.
	group := robotsData.FindGroup("Wukong-Cloner/2.0")
	if group == nil {
		group = robotsData.FindGroup("Wukong-Cloner")
	}
	if group == nil {
		group = robotsData.FindGroup("*")
	}

	// Extract sitemap URLs (Sitemaps is a field, not a method).
	sitemaps := robotsData.Sitemaps

	return &RobotsRule{
		Host:       host,
		Group:      group,
		Sitemaps:   sitemaps,
		FetchedAt:  time.Now(),
		RawContent: string(data),
	}, nil
}

// ---------------------------------------------------------------------------
// Sitemap discovery.
// ---------------------------------------------------------------------------

// SitemapURL represents a URL entry in a sitemap.
type SitemapURL struct {
	Loc        string
	LastMod    string
	ChangeFreq string
	Priority   float64
}

// sitemapIndex represents a sitemap index XML.
type sitemapIndex struct {
	XMLName  xml.Name       `xml:"sitemapindex"`
	Sitemaps []sitemapEntry `xml:"sitemap"`
}

type sitemapEntry struct {
	Loc string `xml:"loc"`
}

// urlset represents a URL set in a sitemap XML.
type urlset struct {
	XMLName xml.Name     `xml:"urlset"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc      string  `xml:"loc"`
	LastMod  string  `xml:"lastmod"`
	Priority float64 `xml:"priority"`
}

// FetchSitemaps downloads and parses sitemaps to extract URLs.
// Supports both sitemap indexes and regular sitemap URL sets.
func FetchSitemaps(ctx context.Context, client *http.Client, sitemapURLs []string) ([]string, error) {
	var allURLs []string

	for _, su := range sitemapURLs {
		urls, err := fetchSitemap(ctx, client, su)
		if err != nil {
			// Non-fatal: continue with other sitemaps.
			continue
		}

		for _, u := range urls {
			if isIndexURL(u) {
				// Recursively fetch nested sitemaps.
				nested, err := fetchSitemap(ctx, client, u)
				if err != nil {
					continue
				}
				allURLs = append(allURLs, nested...)
			} else {
				allURLs = append(allURLs, u)
			}
		}
	}

	return allURLs, nil
}

// fetchSitemap downloads and parses a single sitemap URL.
func fetchSitemap(ctx context.Context, client *http.Client, url string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Wukong-Cloner/2.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Try parsing as sitemap index first.
	var index sitemapIndex
	if err := xml.Unmarshal(data, &index); err == nil && len(index.Sitemaps) > 0 {
		var urls []string
		for _, se := range index.Sitemaps {
			urls = append(urls, se.Loc)
		}
		return urls, nil
	}

	// Try parsing as URL set.
	var set urlset
	if err := xml.Unmarshal(data, &set); err == nil {
		var urls []string
		for _, su := range set.URLs {
			urls = append(urls, su.Loc)
		}
		return urls, nil
	}

	return nil, fmt.Errorf("unable to parse sitemap")
}

// isIndexURL checks if a URL is likely a sitemap index.
func isIndexURL(u string) bool {
	return strings.HasSuffix(strings.ToLower(u), ".xml") &&
		(strings.Contains(u, "sitemap") || strings.Contains(u, "Sitemap"))
}

// ---------------------------------------------------------------------------
// Rate limiting.
// ---------------------------------------------------------------------------

// RateLimiter provides token-bucket rate limiting for HTTP requests.
type RateLimiter struct {
	mu       sync.Mutex
	limiter  *rate.Limiter
	interval time.Duration // current effective minimum interval
}

// NewRateLimiter creates a rate limiter with the specified interval.
// For example, NewRateLimiter(time.Second) allows 1 request per second.
func NewRateLimiter(interval time.Duration) *RateLimiter {
	return &RateLimiter{
		limiter:  rate.NewLimiter(rate.Every(interval), 1),
		interval: interval,
	}
}

// Wait blocks until a request can be made, or the context is cancelled.
func (rl *RateLimiter) Wait(ctx context.Context) error {
	return rl.limiter.Wait(ctx)
}

// Interval returns the current effective minimum interval between
// requests (affected by SlowDown).
func (rl *RateLimiter) Interval() time.Duration {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.interval
}

// SlowDown penalizes the limiter after the server signaled rate
// limiting (429/503): the current interval is multiplied by factor
// and capped at max. Repeated strikes keep backing off
// multiplicatively. Returns the new interval. The interval never
// drops below the value the limiter was created with — only SlowDown
// ever widens it.
func (rl *RateLimiter) SlowDown(factor float64, max time.Duration) time.Duration {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	next := time.Duration(float64(rl.interval) * factor)
	if next > max {
		next = max
	}
	if next <= rl.interval {
		return rl.interval
	}
	rl.interval = next
	rl.limiter.SetLimit(rate.Every(next))
	return next
}

// RaiseTo lifts the interval to at least floor (no-op when the current
// interval already meets it). Used to propagate IP-segment penalties
// to hosts sharing a penalized server: the host's own bucket inherits
// the segment's minimum interval without an additional multiplication.
func (rl *RateLimiter) RaiseTo(floor time.Duration) time.Duration {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if floor <= rl.interval {
		return rl.interval
	}
	rl.interval = floor
	rl.limiter.SetLimit(rate.Every(floor))
	return floor
}

// NewRateLimiterFromCrawlDelay creates a rate limiter from a crawl-delay value.
func NewRateLimiterFromCrawlDelay(delay time.Duration) *RateLimiter {
	if delay <= 0 {
		delay = 100 * time.Millisecond
	}
	return NewRateLimiter(delay)
}
