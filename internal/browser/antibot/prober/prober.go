package prober

import (
	"context"
	"time"
)

type ProbeDimension string

const (
	DimensionHTTPHeader  ProbeDimension = "http_header"
	DimensionRobots      ProbeDimension = "robots"
	DimensionWAF         ProbeDimension = "waf"
	DimensionJSChallenge ProbeDimension = "js_challenge"
	DimensionRateLimit   ProbeDimension = "rate_limit"
)

type ProbeResult struct {
	Dimension  ProbeDimension
	Detected   bool
	Confidence float64
	Details    map[string]interface{}
	Message    string
	Duration   time.Duration
	Error      error
}

type AntibotLevel int

const (
	LevelNone     AntibotLevel = 0
	LevelLow      AntibotLevel = 1
	LevelMedium   AntibotLevel = 2
	LevelHigh     AntibotLevel = 3
	LevelCritical AntibotLevel = 4
)

type AntibotProfile struct {
	TargetURL       string
	Level           AntibotLevel
	LevelReason     string
	ProbeResults    []ProbeResult
	WAF             string
	WAFConfidence   float64
	HasJSChallenge  bool
	HasRateLimit    bool
	Recommendations []string
}

type ProbeFunc func(ctx context.Context, targetURL string) ProbeResult

type Prober struct {
	probes []ProbeFunc
	client *HTTPClient
}

func NewProber() *Prober {
	return &Prober{
		probes: []ProbeFunc{
			HTTPHeaderProbe,
			RobotsProbe,
			WAFFingerprintProbe,
			RateLimitProbe,
		},
		client: NewHTTPClient(),
	}
}

func (p *Prober) WithJSChallengeProbe(browserURL string) *Prober {
	p.probes = append(p.probes, JSChallengeProbeFactory(browserURL))
	return p
}

func (p *Prober) Probe(ctx context.Context, targetURL string) AntibotProfile {
	results := make([]ProbeResult, 0, len(p.probes))
	sem := make(chan struct{}, 3)

	done := make(chan ProbeResult, len(p.probes))
	for _, probe := range p.probes {
		go func(probe ProbeFunc) {
			sem <- struct{}{}
			defer func() { <-sem }()
			done <- probe(ctx, targetURL)
		}(probe)
	}

	for i := 0; i < len(p.probes); i++ {
		results = append(results, <-done)
	}

	return analyzeResults(targetURL, results)
}

func analyzeResults(targetURL string, results []ProbeResult) AntibotProfile {
	profile := AntibotProfile{
		TargetURL:    targetURL,
		ProbeResults: results,
	}

	var detectedWAF string
	var detectedJS bool
	var detectedRateLimit bool
	var maxConfidence float64

	for _, r := range results {
		if r.Error != nil {
			continue
		}

		if r.Detected && r.Confidence > maxConfidence {
			maxConfidence = r.Confidence
		}

		switch r.Dimension {
		case DimensionWAF:
			if waf, ok := r.Details["waf"].(string); ok && waf != "" {
				detectedWAF = waf
				profile.WAF = waf
			}
			if conf, ok := r.Details["confidence"].(float64); ok {
				profile.WAFConfidence = conf
			}
		case DimensionJSChallenge:
			if r.Detected {
				detectedJS = true
				profile.HasJSChallenge = true
			}
		case DimensionRateLimit:
			if r.Detected {
				detectedRateLimit = true
				profile.HasRateLimit = true
			}
		}
	}

	isCloudflareCDNOnly := detectedWAF == "Cloudflare CDN" && profile.WAFConfidence < 0.5

	switch {
	case detectedWAF != "" && detectedJS && !isCloudflareCDNOnly:
		profile.Level = LevelCritical
		profile.LevelReason = "WAF + JS challenge detected"
	case detectedWAF != "" && !isCloudflareCDNOnly:
		profile.Level = LevelHigh
		profile.LevelReason = "WAF detected: " + detectedWAF
	case detectedWAF == "Cloudflare CDN" && profile.WAFConfidence >= 0.5:
		profile.Level = LevelMedium
		profile.LevelReason = "Cloudflare WAF likely present"
	case detectedWAF == "Cloudflare CDN" && profile.WAFConfidence < 0.5:
		profile.Level = LevelLow
		profile.LevelReason = "Cloudflare CDN detected (no active WAF challenge)"
	case detectedJS:
		profile.Level = LevelMedium
		profile.LevelReason = "JS challenge detected"
	case detectedRateLimit:
		profile.Level = LevelLow
		profile.LevelReason = "Rate limiting detected"
	case maxConfidence > 0.5:
		profile.Level = LevelLow
		profile.LevelReason = "Suspicious patterns detected"
	default:
		profile.Level = LevelNone
		profile.LevelReason = "No anti-bot measures detected"
	}

	profile.Recommendations = generateRecommendations(profile)

	return profile
}

func generateRecommendations(p AntibotProfile) []string {
	var recs []string

	switch p.Level {
	case LevelCritical:
		recs = append(recs,
			"Enable stealth mode (--stealth)",
			"Use real browser profile",
			"Consider human verification bypass",
			"Reduce crawl speed significantly",
		)
	case LevelHigh:
		recs = append(recs,
			"Enable stealth mode (--stealth)",
			"Rotate user agents",
			"Use browser automation (rod/chromedp)",
			"Add request delays",
		)
	case LevelMedium:
		recs = append(recs,
			"Enable stealth mode if issues occur",
			"Use browser rendering",
			"Add random delays between requests",
		)
	case LevelLow:
		recs = append(recs,
			"Add reasonable crawl delays",
			"Respect robots.txt directives",
		)
	default:
		recs = append(recs,
			"Standard crawl settings should work",
			"Respect robots.txt directives",
		)
	}

	if p.WAF != "" {
		recs = append(recs, "WAF-specific handling may be needed: "+p.WAF)
	}

	return recs
}
