package prober

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func RateLimitProbe(ctx context.Context, targetURL string) ProbeResult {
	start := time.Now()

	headers := map[string]string{
		"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
		"Accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		"Accept-Language": "en-US,en;q=0.9",
	}

	client := NewHTTPClient()
	var statusCodes []int
	var delays []time.Duration
	var got429 bool

	for i := 0; i < 5; i++ {
		reqStart := time.Now()
		resp, err := client.GetWithHeaders(targetURL, headers)
		reqEnd := time.Now()

		if err != nil {
			continue
		}

		statusCodes = append(statusCodes, resp.StatusCode)
		delays = append(delays, reqEnd.Sub(reqStart))
		resp.Body.Close()

		if resp.StatusCode == 429 {
			got429 = true
			break
		}

		time.Sleep(100 * time.Millisecond)
	}

	details := make(map[string]interface{})
	detected := false
	confidence := 0.0
	var messages []string

	if got429 {
		detected = true
		confidence = 0.95
		messages = append(messages, "HTTP 429 received")
	}

	var avgDelay time.Duration
	for _, d := range delays {
		avgDelay += d
	}
	if len(delays) > 0 {
		avgDelay /= time.Duration(len(delays))
	}

	if avgDelay > 3*time.Second {
		detected = true
		confidence += 0.2
		messages = append(messages, "Slow response: "+avgDelay.String())
	}

	var status4xxCount, status5xxCount int
	for _, code := range statusCodes {
		if code >= 400 && code < 500 {
			status4xxCount++
		} else if code >= 500 {
			status5xxCount++
		}
	}

	if status4xxCount > 0 {
		detected = true
		confidence += 0.15 * float64(status4xxCount)
		messages = append(messages, fmt.Sprintf("4xx errors: %d", status4xxCount))
	}

	if status5xxCount > 0 {
		confidence += 0.1 * float64(status5xxCount)
		messages = append(messages, fmt.Sprintf("5xx errors: %d", status5xxCount))
	}

	details["request_count"] = len(statusCodes)
	details["avg_response_time"] = avgDelay.String()
	details["status_codes"] = statusCodes
	details["got_429"] = got429
	details["status_4xx_count"] = status4xxCount
	details["status_5xx_count"] = status5xxCount

	return ProbeResult{
		Dimension:  DimensionRateLimit,
		Detected:   detected,
		Confidence: float64(min(1, int(confidence*10)) / 10.0),
		Details:    details,
		Message:    strings.Join(messages, "; "),
		Duration:   time.Since(start),
	}
}
