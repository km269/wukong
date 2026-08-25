package prober

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type WAFInfo struct {
	Name           string
	Headers        []string
	Cookies        []string
	HTMLPatterns   []string
	ServerPatterns []string
	Confidence     float64
}

var knownWAFs = []WAFInfo{
	{
		Name:    "Cloudflare WAF",
		Headers: []string{"cf-chl-bypass", "cf-chl-out", "cf-chl-proxied", "cf-captcha-bypass"},
		Cookies: []string{"__cf_chl_", "__cfruid", "cf_clearance"},
		HTMLPatterns: []string{
			"cf-challenge",
			"cf-turnstile",
			"__cf_chl",
			"challenges.cloudflare.com",
			"cloudflare challenge",
			"cloudflare captcha",
		},
		ServerPatterns: []string{},
		Confidence:     0.95,
	},
	{
		Name:    "Cloudflare CDN",
		Headers: []string{"cf-ray", "cf-cache-status", "cf-connecting-ip"},
		Cookies: []string{},
		HTMLPatterns: []string{
			"cloudflare",
		},
		ServerPatterns: []string{"cloudflare"},
		Confidence:     0.5,
	},
	{
		Name:    "Sucuri",
		Headers: []string{"x-sucuri-id", "x-sucuri-cache", "x-sucuri-info"},
		Cookies: []string{"sucuri_cloudproxy_uuid"},
		HTMLPatterns: []string{
			"sucuri",
			"sucuri.net",
			"sucuri firewall",
			"sucuri web firewall",
		},
		ServerPatterns: []string{"sucuri"},
		Confidence:     0.85,
	},
	{
		Name:    "Akamai",
		Headers: []string{"x-akamai-edge", "x-akamai-origin", "x-akamai-request-id", "x-akamai-cache-key"},
		Cookies: []string{"akamai_session", "akamai_xid"},
		HTMLPatterns: []string{
			"akamai",
			"akamai.net",
			"akamaihd.net",
			"akamai waf",
		},
		ServerPatterns: []string{"akamai", "akamaiedge", "akamaitechnologies"},
		Confidence:     0.8,
	},
	{
		Name:    "AWS WAF",
		Headers: []string{"x-aws-waf-token", "x-aws-waf-result", "x-amzn-trace-id", "x-waf-request-id"},
		Cookies: []string{},
		HTMLPatterns: []string{
			"aws waf",
			"aws.amazon.com/waf",
			"web application firewall",
		},
		ServerPatterns: []string{"aws", "amazonaws"},
		Confidence:     0.75,
	},
	{
		Name:    "Imperva",
		Headers: []string{"x-im-perva", "x-imperva-protection", "x-imperva-site-id", "x-imperva-xsrf"},
		Cookies: []string{"incap_ses_", "visid_incap_", "nlbi_", "imp_"},
		HTMLPatterns: []string{
			"imperva",
			"incapsula",
			"securiti",
			"incapsula incident",
		},
		ServerPatterns: []string{"imperva", "incapsula"},
		Confidence:     0.8,
	},
	{
		Name:    "Aliyun WAF",
		Headers: []string{"aliyun-waf-token", "x-aliyun-waf", "x-waf-request-id", "x-acw-trace-id"},
		Cookies: []string{"acw_tc", "acw_sc__v2"},
		HTMLPatterns: []string{
			"aliyun-waf",
			"aliyun waf",
			"acw_sc__v2",
			"aliyun web application firewall",
		},
		ServerPatterns: []string{"aliyun"},
		Confidence:     0.85,
	},
	{
		Name:    "QCloud WAF",
		Headers: []string{"x-qcloud-waf", "x-tencent-waf", "x-waf-version"},
		Cookies: []string{"qcloud_waf_session"},
		HTMLPatterns: []string{
			"qcloud-waf",
			"tencent-waf",
			"tencent web application firewall",
		},
		ServerPatterns: []string{"qcloud", "tencent"},
		Confidence:     0.75,
	},
	{
		Name:    "F5 BIG-IP ASM",
		Headers: []string{"x-wa-info", "x-bigip", "x-bigip-server", "x-f5-icontrol"},
		Cookies: []string{"BIGipServer", "TS", "F5"},
		HTMLPatterns: []string{
			"f5 big-ip",
			"big-ip asm",
			"request blocked by web application firewall",
		},
		ServerPatterns: []string{"bigip", "f5"},
		Confidence:     0.8,
	},
	{
		Name:    "ModSecurity",
		Headers: []string{"x-mod-security", "x-modsecurity", "x-nsfocus"},
		Cookies: []string{},
		HTMLPatterns: []string{
			"mod_security",
			"modsecurity",
			"nsfocus",
			"request rejected by modsecurity",
		},
		ServerPatterns: []string{"mod_security", "modsecurity"},
		Confidence:     0.7,
	},
	{
		Name:    "Wordfence",
		Headers: []string{"x-wordfence"},
		Cookies: []string{"wfvt_", "wordfence_lh", "wf_logout", "wordfence_verified"},
		HTMLPatterns: []string{
			"wordfence",
			"wordfence firewall",
			"wordfence blocked",
		},
		ServerPatterns: []string{},
		Confidence:     0.85,
	},
	{
		Name:    "Barracuda",
		Headers: []string{"x-barracuda", "x-barracuda-cookie", "x-barracuda-waf"},
		Cookies: []string{"barraCounterSession", "BARRACUDA"},
		HTMLPatterns: []string{
			"barracuda",
			"barracuda networks",
			"barracuda web application firewall",
		},
		ServerPatterns: []string{"barracuda"},
		Confidence:     0.75,
	},
	{
		Name:    "Citrix NetScaler",
		Headers: []string{"x-citrix", "x-netscaler", "x-cn-info"},
		Cookies: []string{"NSC_", "citrix_ns_id"},
		HTMLPatterns: []string{
			"citrix",
			"netscaler",
			"citrix netscaler",
		},
		ServerPatterns: []string{"citrix", "netscaler"},
		Confidence:     0.75,
	},
	{
		Name:    "Fortinet FortiWeb",
		Headers: []string{"x-fortiweb", "x-fortinet"},
		Cookies: []string{"FORTIWAFSID", "FWSESSION"},
		HTMLPatterns: []string{
			"fortiweb",
			"fortinet",
			"forti web application firewall",
		},
		ServerPatterns: []string{"fortiweb", "fortinet"},
		Confidence:     0.75,
	},
	{
		Name:    "Azure Front Door/WAF",
		Headers: []string{"x-azure-ref", "x-azure-requestid", "x-ms-edge-request-id", "x-ms-content-sha256"},
		Cookies: []string{"ARRAffinity"},
		HTMLPatterns: []string{
			"microsoft azure",
			"azure front door",
			"azure waf",
		},
		ServerPatterns: []string{"azure", "microsoft"},
		Confidence:     0.7,
	},
	{
		Name:    "Fastly",
		Headers: []string{"x-fastly-request-id", "x-fastly-debug", "x-served-by"},
		Cookies: []string{"fastly", "__cfduid"},
		HTMLPatterns: []string{
			"fastly",
			"fastly cache",
		},
		ServerPatterns: []string{"fastly"},
		Confidence:     0.65,
	},
	{
		Name:    "StackPath",
		Headers: []string{"x-stackpath", "x-sp-cache-status"},
		Cookies: []string{},
		HTMLPatterns: []string{
			"stackpath",
		},
		ServerPatterns: []string{"stackpath"},
		Confidence:     0.6,
	},
	{
		Name:    "Radware AppWall",
		Headers: []string{"x-pk", "x-radware", "x-appwall"},
		Cookies: []string{"radware_session"},
		HTMLPatterns: []string{
			"radware",
			"appwall",
			"radware appwall",
		},
		ServerPatterns: []string{"radware"},
		Confidence:     0.7,
	},
	{
		Name:    "Reblaze",
		Headers: []string{"x-reblaze", "x-reblaze-token", "x-reblaze-id"},
		Cookies: []string{"rbzid"},
		HTMLPatterns: []string{
			"reblaze",
			"reblaze waf",
		},
		ServerPatterns: []string{"reblaze"},
		Confidence:     0.75,
	},
	{
		Name:    "DataDome",
		Headers: []string{"x-datadome"},
		Cookies: []string{"datadome", "dd_cookie", "dd_session"},
		HTMLPatterns: []string{
			"datadome",
			"data dome",
			"datadome challenge",
		},
		ServerPatterns: []string{},
		Confidence:     0.85,
	},
	{
		Name:    "PerimeterX/HUMAN",
		Headers: []string{"x-px", "x-px-cache", "x-px-headers"},
		Cookies: []string{"_px3", "_pxCaptcha", "_pxvid", "_pxd", "pxcts"},
		HTMLPatterns: []string{
			"perimeterx",
			"px-captcha",
			"_px3",
			"human security",
		},
		ServerPatterns: []string{"perimeterx", "human"},
		Confidence:     0.85,
	},
	{
		Name:    "Shape Security",
		Headers: []string{"x-shape", "x-shape-id"},
		Cookies: []string{"SJIG_", "shape", "shp"},
		HTMLPatterns: []string{
			"shape security",
			"sjig_",
		},
		ServerPatterns: []string{"shape"},
		Confidence:     0.7,
	},
}

var wafBlockPagePatterns = []string{
	"your request has been blocked",
	"request blocked",
	"access denied",
	"security event",
	"incident id",
	"incident id:",
	"security policy",
	"violation of security",
	"unusual activity detected",
	"suspicious activity",
	"automated access",
	"bot detected",
	"potential security threat",
	"malicious request",
	"attack detected",
}

func WAFFingerprintProbe(ctx context.Context, targetURL string) ProbeResult {
	start := time.Now()

	client := NewHTTPClient()

	normalResult := probeWithHeaders(ctx, client, targetURL, map[string]string{
		"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
		"Accept":          "*/*",
		"Accept-Language": "en-US,en;q=0.9",
	})

	if normalResult.err != nil {
		return ProbeResult{
			Dimension: DimensionWAF,
			Error:     normalResult.err,
			Duration:  time.Since(start),
		}
	}

	challengeResult := probeWithHeaders(ctx, client, targetURL, map[string]string{
		"User-Agent": "curl/8.0.0",
		"Accept":     "*/*",
	})

	return analyzeWAFResults(normalResult, challengeResult, time.Since(start))
}

func probeWithHeaders(ctx context.Context, client *HTTPClient, targetURL string, headers map[string]string) *probeResponse {
	resp, err := client.GetWithHeaders(targetURL, headers)
	if err != nil {
		return &probeResponse{err: err}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	return &probeResponse{
		statusCode: resp.StatusCode,
		headers:    resp.Header,
		cookies:    resp.Header["Set-Cookie"],
		html:       html,
		lowerHTML:  strings.ToLower(html),
	}
}

type probeResponse struct {
	statusCode int
	headers    http.Header
	cookies    []string
	html       string
	lowerHTML  string
	err        error
}

func analyzeWAFResults(normal, challenge *probeResponse, duration time.Duration) ProbeResult {
	details := make(map[string]interface{})
	var detectedWAF string
	maxConfidence := 0.0
	var messages []string

	if challenge.err != nil && normal.err == nil {
		messages = append(messages, "Challenge request failed")
	}

	resultToCheck := normal
	if challenge.err == nil && challenge.statusCode != normal.statusCode {
		resultToCheck = challenge
		messages = append(messages, fmt.Sprintf("Status code changed: %d → %d", normal.statusCode, challenge.statusCode))
	}

	lowerCookies := make([]string, len(resultToCheck.cookies))
	for i, c := range resultToCheck.cookies {
		lowerCookies[i] = strings.ToLower(c)
	}

	for _, waf := range knownWAFs {
		matches := 0
		totalChecks := len(waf.Headers) + len(waf.HTMLPatterns) + len(waf.ServerPatterns) + len(waf.Cookies)
		if totalChecks == 0 {
			continue
		}

		for _, h := range waf.Headers {
			if resultToCheck.headers.Get(h) != "" {
				matches++
			}
		}

		for _, cookie := range waf.Cookies {
			for _, c := range lowerCookies {
				if strings.Contains(c, cookie) {
					matches++
					break
				}
			}
		}

		for _, pat := range waf.HTMLPatterns {
			if strings.Contains(resultToCheck.lowerHTML, pat) {
				matches++
			}
		}

		server := strings.ToLower(resultToCheck.headers.Get("Server"))
		for _, pat := range waf.ServerPatterns {
			if strings.Contains(server, pat) {
				matches++
			}
		}

		if matches > 0 {
			confidence := waf.Confidence * (float64(matches) / float64(totalChecks))

			if waf.Name == "Cloudflare CDN" && matches == 1 &&
				strings.Contains(resultToCheck.lowerHTML, "cloudflare") {
				confidence = 0.3
			}

			if confidence > maxConfidence {
				maxConfidence = confidence
				detectedWAF = waf.Name
			}
			messages = append(messages, fmt.Sprintf("%s (%d indicators)", waf.Name, matches))
		}
	}

	if challenge.err == nil && resultToCheck.statusCode == 503 &&
		strings.Contains(resultToCheck.lowerHTML, "cloudflare") {
		detectedWAF = "Cloudflare WAF"
		maxConfidence = 1.0
		messages = append(messages, "Cloudflare challenge triggered")
	}

	if detectedWAF != "" {
		details["waf"] = detectedWAF
		details["confidence"] = maxConfidence
		details["status_code"] = resultToCheck.statusCode
		details["server"] = resultToCheck.headers.Get("Server")

		if resultToCheck.statusCode == 403 {
			messages = append(messages, "Access denied")
		}

		if len(resultToCheck.html) < 15000 {
			for _, pat := range wafBlockPagePatterns {
				if strings.Contains(resultToCheck.lowerHTML, pat) {
					messages = append(messages, fmt.Sprintf("WAF block page pattern: %s", pat))
					maxConfidence = min(maxConfidence+0.1, 1.0)
					break
				}
			}
		}

		return ProbeResult{
			Dimension:  DimensionWAF,
			Detected:   true,
			Confidence: maxConfidence,
			Details:    details,
			Message:    strings.Join(messages, "; "),
			Duration:   duration,
		}
	}

	if len(resultToCheck.html) < 15000 {
		for _, pat := range wafBlockPagePatterns {
			if strings.Contains(resultToCheck.lowerHTML, pat) {
				details["waf"] = "Unknown WAF"
				details["confidence"] = 0.6
				details["block_pattern"] = pat
				details["status_code"] = resultToCheck.statusCode

				messages = append(messages, fmt.Sprintf("Generic WAF block page: %s", pat))

				return ProbeResult{
					Dimension:  DimensionWAF,
					Detected:   true,
					Confidence: 0.6,
					Details:    details,
					Message:    strings.Join(messages, "; "),
					Duration:   duration,
				}
			}
		}
	}

	details["status_code"] = resultToCheck.statusCode
	details["server"] = resultToCheck.headers.Get("Server")

	return ProbeResult{
		Dimension:  DimensionWAF,
		Detected:   false,
		Confidence: 0,
		Details:    details,
		Message:    "No known WAF detected",
		Duration:   duration,
	}
}
