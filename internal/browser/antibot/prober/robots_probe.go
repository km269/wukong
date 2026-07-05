package prober

import (
	"context"
	"net/url"
	"strings"
	"time"
)

func RobotsProbe(ctx context.Context, targetURL string) ProbeResult {
	start := time.Now()

	parsed, err := url.Parse(targetURL)
	if err != nil {
		return ProbeResult{
			Dimension: DimensionRobots,
			Error:     err,
			Duration:  time.Since(start),
		}
	}

	robotsURL := parsed.Scheme + "://" + parsed.Host + "/robots.txt"

	headers := map[string]string{
		"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
	}

	client := NewHTTPClient()
	resp, err := client.GetWithHeaders(robotsURL, headers)
	if err != nil {
		return ProbeResult{
			Dimension: DimensionRobots,
			Error:     err,
			Duration:  time.Since(start),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return ProbeResult{
			Dimension:  DimensionRobots,
			Detected:   false,
			Confidence: 0,
			Details:    map[string]interface{}{"status": "not_found"},
			Message:    "robots.txt not found",
			Duration:   time.Since(start),
		}
	}

	if resp.StatusCode >= 400 {
		return ProbeResult{
			Dimension:  DimensionRobots,
			Detected:   true,
			Confidence: 0.5,
			Details:    map[string]interface{}{"status": resp.StatusCode},
			Message:    "robots.txt returned error: " + resp.Status,
			Duration:   time.Since(start),
		}
	}

	body := make([]byte, 10240)
	n, _ := resp.Body.Read(body)
	content := string(body[:n])

	details := make(map[string]interface{})
	detected := false
	confidence := 0.0
	var messages []string

	var disallowCount int
	var crawlDelay string
	var sitemaps []string
	var hasWildcard bool
	var hasUserAgentBlock bool

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasPrefix(strings.ToLower(line), "disallow:") {
			disallowCount++
			path := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(line), "disallow:"))
			if path == "/" || path == "*" || strings.Contains(path, "*") {
				hasWildcard = true
				detected = true
				confidence += 0.3
				messages = append(messages, "Wildcard disallow: "+path)
			}
		}

		if strings.HasPrefix(strings.ToLower(line), "crawl-delay:") {
			crawlDelay = strings.TrimSpace(strings.TrimPrefix(line, "Crawl-Delay:"))
			messages = append(messages, "Crawl-delay: "+crawlDelay)
		}

		if strings.HasPrefix(strings.ToLower(line), "sitemap:") {
			sitemaps = append(sitemaps, strings.TrimSpace(strings.TrimPrefix(line, "Sitemap:")))
		}

		if strings.HasPrefix(strings.ToLower(line), "user-agent:") {
			ua := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(line), "user-agent:"))
			if ua == "*" {
				hasUserAgentBlock = true
			}
		}
	}

	if disallowCount > 10 {
		detected = true
		confidence += 0.1
		messages = append(messages, "High disallow count: "+string(rune(disallowCount+'0')))
	}

	if crawlDelay != "" {
		messages = append(messages, "Crawl-delay specified")
	}

	details["disallow_count"] = disallowCount
	details["crawl_delay"] = crawlDelay
	details["sitemap_count"] = len(sitemaps)
	details["has_wildcard_disallow"] = hasWildcard
	details["has_user_agent_block"] = hasUserAgentBlock

	return ProbeResult{
		Dimension:  DimensionRobots,
		Detected:   detected,
		Confidence: float64(min(1, int(confidence*10))/10.0),
		Details:    details,
		Message:    strings.Join(messages, "; "),
		Duration:   time.Since(start),
	}
}
