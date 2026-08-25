package prober

import (
	"context"
	"io"
	"strings"
	"time"
)

var jsChallengePatterns = []string{
	"captcha",
	"verify you are human",
	"are you a human",
	"please verify",
	"security check",
	"checking your browser",
	"cf-challenge",
	"cf-turnstile",
	"turnstile",
	"__cf_chl",
	"challenges.cloudflare.com",
	"javascript is required",
	"please enable javascript",
	"automated access",
	"suspicious activity",
	"browser fingerprint",
	"device fingerprint",
	"canvas fingerprint",
	"webgl fingerprint",
	"navigator.webdriver",
	"chrome.runtime",
	"webdriver",
	"puppeteer",
	"playwright",
}

var jsChallengeScripts = []string{
	"cloudflare",
	"turnstile",
	"captcha",
	"hcaptcha",
	"recaptcha",
	"arkose",
	"fingerprint",
}

func JSChallengeProbeFactory(browserURL string) ProbeFunc {
	return func(ctx context.Context, targetURL string) ProbeResult {
		return JSChallengeProbe(ctx, targetURL)
	}
}

func JSChallengeProbe(ctx context.Context, targetURL string) ProbeResult {
	start := time.Now()

	headers := map[string]string{
		"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
		"Accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		"Accept-Language": "en-US,en;q=0.9",
	}

	client := NewHTTPClient()
	resp, err := client.GetWithHeaders(targetURL, headers)
	if err != nil {
		return ProbeResult{
			Dimension: DimensionJSChallenge,
			Error:     err,
			Duration:  time.Since(start),
		}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	lowerHTML := strings.ToLower(html)

	details := make(map[string]interface{})
	detected := false
	confidence := 0.0
	var messages []string

	matches := 0
	for _, pat := range jsChallengePatterns {
		if strings.Contains(lowerHTML, pat) {
			matches++
			messages = append(messages, "pattern: "+pat)
		}
	}

	scriptMatches := 0
	for _, script := range jsChallengeScripts {
		if strings.Contains(lowerHTML, script) {
			scriptMatches++
		}
	}

	if matches > 0 {
		detected = true
		confidence = float64(min(matches, 5)) * 0.2
	}

	if scriptMatches > 0 {
		detected = true
		confidence += float64(min(scriptMatches, 3)) * 0.15
	}

	if resp.StatusCode == 503 {
		detected = true
		confidence += 0.3
		messages = append(messages, "HTTP 503 (likely challenge)")
	}

	if len(html) < 5000 && (matches > 0 || scriptMatches > 0) {
		confidence += 0.2
		messages = append(messages, "short challenge page")
	}

	details["pattern_matches"] = matches
	details["script_matches"] = scriptMatches
	details["html_size"] = len(html)
	details["status_code"] = resp.StatusCode

	return ProbeResult{
		Dimension:  DimensionJSChallenge,
		Detected:   detected,
		Confidence: float64(min(1, int(confidence*10)) / 10.0),
		Details:    details,
		Message:    strings.Join(messages, "; "),
		Duration:   time.Since(start),
	}
}
