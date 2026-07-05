package prober

import (
	"context"
	"strings"
	"time"
)

var commonWAFHeaders = []string{
	"cf-ray",
	"cf-chl-bypass",
	"x-sucuri-id",
	"x-sucuri-cache",
	"x-iinfo",
	"x-cdn",
	"x-waf-request-id",
	"x-akamai-edge",
	"x-akamai-origin",
	"x-aws-waf-token",
	"x-aws-waf-result",
	"x-im-perva",
	"x-imperva-protection",
	"aliyun-waf-token",
	"x-aliyun-waf",
	"x-qcloud-waf",
	"x-csrf-token",
}

var securityHeaders = []string{
	"strict-transport-security",
	"content-security-policy",
	"x-content-type-options",
	"x-frame-options",
	"x-xss-protection",
	"referrer-policy",
	"permissions-policy",
}

func HTTPHeaderProbe(ctx context.Context, targetURL string) ProbeResult {
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
			Dimension: DimensionHTTPHeader,
			Error:     err,
			Duration:  time.Since(start),
		}
	}
	defer resp.Body.Close()

	details := make(map[string]interface{})
	detected := false
	confidence := 0.0
	var messages []string

	wafHeadersFound := []string{}
	securityHeadersFound := []string{}
	cookieHeaders := []string{}

	for k, v := range resp.Header {
		for _, wafHeader := range commonWAFHeaders {
			if strings.EqualFold(k, wafHeader) {
				wafHeadersFound = append(wafHeadersFound, k)
				detected = true
				confidence += 0.15
			}
		}

		for _, secHeader := range securityHeaders {
			if strings.EqualFold(k, secHeader) {
				securityHeadersFound = append(securityHeadersFound, k)
			}
		}

		if strings.EqualFold(k, "Set-Cookie") {
			for _, cookie := range v {
				cookieHeaders = append(cookieHeaders, cookie)
			}
		}
	}

	if len(wafHeadersFound) > 0 {
		messages = append(messages, "WAF headers found: "+strings.Join(wafHeadersFound, ", "))
	}

	if resp.StatusCode >= 400 {
		detected = true
		confidence += 0.2
		messages = append(messages, "HTTP status: "+resp.Status)
	}

	details["status_code"] = resp.StatusCode
	details["waf_headers"] = wafHeadersFound
	details["security_headers"] = securityHeadersFound
	details["set_cookie_count"] = len(cookieHeaders)
	details["server"] = resp.Header.Get("Server")
	details["x_powered_by"] = resp.Header.Get("X-Powered-By")

	return ProbeResult{
		Dimension:  DimensionHTTPHeader,
		Detected:   detected,
		Confidence: float64(min(1, int(confidence*10)) / 10.0),
		Details:    details,
		Message:    strings.Join(messages, "; "),
		Duration:   time.Since(start),
	}
}
