package stealth

import (
	"fmt"
	"math/rand"
	"net/url"
	"regexp"
	"strings"
)

// GeoProfile describes a coherent geographic identity: the locale,
// timezone and platform variations that a real user in that region
// would present. When traffic exits through a proxy, the browser
// fingerprint MUST agree with the proxy's geography (timezone vs IP
// mismatch is a classic GeoIP-consistency check used by Cloudflare,
// DataDome and PerimeterX).
type GeoProfile struct {
	// Code is a short region selector, e.g. "cn", "us-east", "jp".
	Code string
	// Languages is the navigator.languages list, most preferred first.
	Languages []string
	// Timezone is the IANA zone name, e.g. "Asia/Shanghai".
	Timezone string
	// TZOffsetMinutes is what Date.prototype.getTimezoneOffset()
	// returns in that zone (minutes, inverted sign vs UTC — the JS
	// convention: UTC+8 → -480).
	TZOffsetMinutes int
}

// geoProfiles are locale↔timezone↔language coherent personas.
var geoProfiles = []GeoProfile{
	{Code: "cn", Languages: []string{"zh-CN", "zh", "en-US", "en"}, Timezone: "Asia/Shanghai", TZOffsetMinutes: -480},
	{Code: "us-east", Languages: []string{"en-US", "en"}, Timezone: "America/New_York", TZOffsetMinutes: 240},
	{Code: "us-west", Languages: []string{"en-US", "en"}, Timezone: "America/Los_Angeles", TZOffsetMinutes: 420},
	{Code: "us-central", Languages: []string{"en-US", "en"}, Timezone: "America/Chicago", TZOffsetMinutes: 300},
	{Code: "gb", Languages: []string{"en-GB", "en", "en-US"}, Timezone: "Europe/London", TZOffsetMinutes: 0},
	{Code: "de", Languages: []string{"de-DE", "de", "en-US", "en"}, Timezone: "Europe/Berlin", TZOffsetMinutes: -60},
	{Code: "fr", Languages: []string{"fr-FR", "fr", "en-US", "en"}, Timezone: "Europe/Paris", TZOffsetMinutes: -60},
	{Code: "jp", Languages: []string{"ja-JP", "ja", "en-US", "en"}, Timezone: "Asia/Tokyo", TZOffsetMinutes: -540},
	{Code: "kr", Languages: []string{"ko-KR", "ko", "en-US", "en"}, Timezone: "Asia/Seoul", TZOffsetMinutes: -540},
	{Code: "sg", Languages: []string{"en-SG", "en", "zh-CN", "zh"}, Timezone: "Asia/Singapore", TZOffsetMinutes: -480},
}

// GeoProfileByCode returns the geo profile for a region selector, or
// nil when unknown. Supported selectors: cn, us-east, us-west,
// us-central, gb, de, fr, jp, kr, sg.
func GeoProfileByCode(code string) *GeoProfile {
	for i := range geoProfiles {
		if geoProfiles[i].Code == code {
			return &geoProfiles[i]
		}
	}
	return nil
}

// geoParamRe matches the region-parameter conventions residential
// proxy providers embed in the proxy username/password, e.g.
//
//	http://user-country-jp:pass@host:port
//	http://user-cc-jp-sessid-xyz:pass@host:port
//	http://user:pass-region=us@host:port
//	socks5://gb.user:pass@host:port
var geoParamRe = regexp.MustCompile(`(?i)(?:country|region|cc|geo|zone)[=_-]([a-z]{2})(?:[-_.:]|$)`)

// geoTLDMap maps ISO-like country TLDs of the proxy hostname onto pool
// selectors. Only unambiguous TLDs are mapped; ".uk" is mapped to gb
// (ISO 3166 has no "uk"), ".us" maps to the most populous zone.
var geoTLDMap = map[string]string{
	"jp": "jp", "kr": "kr", "cn": "cn", "de": "de", "fr": "fr",
	"sg": "sg", "uk": "gb", "us": "us-east",
}

// GeoCodeFromProxyURL infers the exit region for a proxy URL and
// returns the matching geo profile code, or "" when unknown. The
// inference is best-effort by design: residential proxies identify
// the requested country inside their credentials (username/password)
// or expose it as the gateway hostname's TLD. A wrong guess is worse
// than none — an unknown region returns "" so the caller can fall
// back to an explicit config value or a random persona.
func GeoCodeFromProxyURL(proxyURL string) string {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return ""
	}
	// 1. Credential-embedded region parameter (most residential
	//    providers: user-country-jp, cc=kr, region-de ...).
	for _, cred := range []string{u.User.Username(), ""} {
		if cred == "" {
			if pw, has := u.User.Password(); has {
				cred = pw
			}
		}
		if cred == "" {
			continue
		}
		if m := geoParamRe.FindStringSubmatch(cred); m != nil {
			if code := normalizeGeoCode(m[1]); code != "" {
				return code
			}
		}
	}
	// 2. Gateway hostname TLD (jp.proxy-provider.com → jp).
	host := strings.ToLower(u.Hostname())
	if i := strings.LastIndex(host, "."); i >= 0 && i+3 >= len(host) {
		if code, ok := geoTLDMap[host[i+1:]]; ok {
			return code
		}
	}
	return ""
}

// normalizeGeoCode maps a raw 2-letter country code onto a pool
// selector, or "" when the region has no persona. "us" picks a
// representative zone (exact US zone selection needs the explicit
// config value).
func normalizeGeoCode(cc string) string {
	cc = strings.ToLower(strings.TrimSpace(cc))
	switch cc {
	case "us":
		return "us-east"
	case "uk", "gb":
		return "gb"
	case "cn", "de", "fr", "jp", "kr", "sg",
		"us-east", "us-west", "us-central":
		return cc
	}
	return ""
}

// ResolveGeoCode applies the geo selection precedence for
// fingerprint generation: explicit config wins, then the best-effort
// proxy inference, then "" (random persona). Unknown codes degrade to
// random with a message the caller can log.
func ResolveGeoCode(configured, proxyURL string) (code string) {
	if configured != "" {
		if c := normalizeGeoCode(configured); c != "" {
			return c
		}
		return ""
	}
	if proxyURL != "" {
		return GeoCodeFromProxyURL(proxyURL)
	}
	return ""
}

// GPUSpec is one plausible WebGL hardware configuration. Vendor and
// renderer strings follow the exact ANGLE/D3D11 formatting real
// Chrome reports on Windows.
type GPUSpec struct {
	Vendor   string
	Renderer string
	MaxTex   int
}

// gpuSpecs is the pool of realistic desktop GPUs.
var gpuSpecs = []GPUSpec{
	{Vendor: "Google Inc. (Intel)", Renderer: "ANGLE (Intel, Intel(R) UHD Graphics 620 Direct3D11 vs_5_0 ps_5_0, D3D11)", MaxTex: 16384},
	{Vendor: "Google Inc. (Intel)", Renderer: "ANGLE (Intel, Intel(R) Iris(R) Xe Graphics Direct3D11 vs_5_0 ps_5_0, D3D11)", MaxTex: 16384},
	{Vendor: "Google Inc. (NVIDIA)", Renderer: "ANGLE (NVIDIA, NVIDIA GeForce GTX 1650 Direct3D11 vs_5_0 ps_5_0, D3D11)", MaxTex: 16384},
	{Vendor: "Google Inc. (NVIDIA)", Renderer: "ANGLE (NVIDIA, NVIDIA GeForce RTX 3060 Direct3D11 vs_5_0 ps_5_0, D3D11)", MaxTex: 16384},
	{Vendor: "Google Inc. (AMD)", Renderer: "ANGLE (AMD, AMD Radeon(TM) Graphics Direct3D11 vs_5_0 ps_5_0, D3D11)", MaxTex: 8192},
	{Vendor: "Google Inc. (AMD)", Renderer: "ANGLE (AMD, AMD Radeon RX 580 Direct3D11 vs_5_0 ps_5_0, D3D11)", MaxTex: 16384},
}

type screenSpec struct {
	W, H            int
	TaskbarReserved int // typical reserved vertical px (taskbar/dock)
}

// screenSpecs is the pool of common desktop resolutions (Steam-style
// hardware survey distribution).
var screenSpecs = []screenSpec{
	{W: 1920, H: 1080, TaskbarReserved: 40},
	{W: 1920, H: 1080, TaskbarReserved: 56},
	{W: 2560, H: 1440, TaskbarReserved: 48},
	{W: 1366, H: 768, TaskbarReserved: 40},
	{W: 1440, H: 900, TaskbarReserved: 40},
	{W: 1536, H: 864, TaskbarReserved: 40},
	{W: 1600, H: 900, TaskbarReserved: 40},
	{W: 3840, H: 2160, TaskbarReserved: 56},
}

// Fingerprint is a complete, internally consistent browser identity.
// It is generated ONCE per browser pool (session) and baked into the
// injected stealth script, so every document in the session observes
// the same GPU, screen, timezone, languages and noise seeds.
// Per-document randomisation is a strong bot signal: a "user" whose
// GPU changes between two navigations does not exist.
type Fingerprint struct {
	// Geo identity (languages + timezone stay coherent).
	Geo GeoProfile

	// Screen geometry.
	ScreenWidth       int
	ScreenHeight      int
	ScreenAvailWidth  int
	ScreenAvailHeight int
	ColorDepth        int

	// Hardware.
	HardwareConcurrency int
	DeviceMemoryGB      int
	GPU                 GPUSpec

	// Network Information API.
	ConnectionRTT  int // ms
	ConnectionDown float64
	ConnectionType string

	// Battery API.
	BatteryLevel    float64
	BatteryCharging bool

	// Stable noise seeds (canvas pixels, audio, font metrics).
	CanvasSeed uint32
	AudioSeed  uint32
	FontSeed   uint32

	// Plugins selection index into the plugin sets (0..2).
	PluginSet int
}

// GenerateFingerprint builds a random-but-coherent fingerprint.
// When geo is non-nil the locale/timezone are pinned to that region
// (use the proxy exit region); otherwise a random persona is picked.
func GenerateFingerprint(rng *rand.Rand, geo *GeoProfile) *Fingerprint {
	if rng == nil {
		rng = rand.New(rand.NewSource(0))
	}
	g := geo
	if g == nil {
		g = &geoProfiles[rng.Intn(len(geoProfiles))]
	}

	sc := screenSpecs[rng.Intn(len(screenSpecs))]
	cores := []int{4, 6, 8, 12, 16}[rng.Intn(5)]
	// Device memory is reported in GB and clamped to power-of-two
	// values by Chrome; keep it plausible against core count.
	mem := []int{8, 8, 16, 16, 32}[rng.Intn(5)]

	fp := &Fingerprint{
		Geo:                 *g,
		ScreenWidth:         sc.W,
		ScreenHeight:        sc.H,
		ScreenAvailWidth:    sc.W,
		ScreenAvailHeight:   sc.H - sc.TaskbarReserved,
		ColorDepth:          24,
		HardwareConcurrency: cores,
		DeviceMemoryGB:      mem,
		GPU:                 gpuSpecs[rng.Intn(len(gpuSpecs))],
		ConnectionRTT:       30 + rng.Intn(70),
		ConnectionDown:      5 + rng.Float64()*20,
		ConnectionType:      "4g",
		BatteryLevel:        0.4 + rng.Float64()*0.6,
		BatteryCharging:     rng.Float64() > 0.5,
		CanvasSeed:          rng.Uint32(),
		AudioSeed:           rng.Uint32(),
		FontSeed:            rng.Uint32(),
		PluginSet:           rng.Intn(3),
	}
	return fp
}

// String returns a compact diagnostic rendering (no secrets).
func (f *Fingerprint) String() string {
	return fmt.Sprintf("geo=%s screen=%dx%d cores=%d mem=%dG gpu=%s tz=%s",
		f.Geo.Code, f.ScreenWidth, f.ScreenHeight,
		f.HardwareConcurrency, f.DeviceMemoryGB, f.GPU.Renderer, f.Geo.Timezone)
}
