package rodbackend

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"regexp"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/km269/wukong/internal/browser/antibot"
	"github.com/km269/wukong/internal/browser/behavior"
	"github.com/km269/wukong/internal/browser/stealth"
	"github.com/km269/wukong/pkg/logutil"
)

// binaryChromeVersion extracts the Chrome version from a
// Browser.getVersion product string such as "HeadlessChrome/132.0.6834.83"
// → "132.0.6834.83". Returns "" when no version is present.
func binaryChromeVersion(product string) string {
	if i := strings.LastIndex(product, "/"); i >= 0 && i+1 < len(product) {
		v := product[i+1:]
		if v != "" && v[0] >= '0' && v[0] <= '9' {
			return v
		}
	}
	return ""
}

// uaVerRe matches Chrome-family version tokens inside a UA string
// ("Chrome/130.0.0.0", "Edg/130.0.0.0", "Chromium/130...").
var uaVerRe = regexp.MustCompile(`(Chrome|Chromium|Edg)/\d+(\.\d+)*`)

// pickBrowserUA selects the UA persona for browser contexts:
// Chrome-family only, and Edge personas are excluded on distro-
// Chromium binaries — such a build cannot back Edge-specific
// behaviour, so claiming "Edg/" would be a falsifiable persona.
func (p *Pool) pickBrowserUA() *antibot.UAProfile {
	for i := 0; i < 8; i++ {
		ua := p.escalator.RotateChromeUA()
		if ua == nil {
			return nil
		}
		if !p.binaryIsChromium || !strings.Contains(ua.UserAgent, "Edg/") {
			return ua
		}
	}
	// Edge is ~1/5 of the Chrome-only pool; after 8 redraws the odds
	// of still holding it are negligible. Accept whatever we have.
	return p.escalator.RotateChromeUA()
}

// alignUAWithBinary rewrites the persona's Chrome-family version to
// the version of the binary that actually makes the connection.
// The UA string, the client-hints metadata and all version-linked
// binary behaviour (TLS details, JS/feature set) then agree by
// construction. Claiming "Chrome/130" from a Chrome 132 binary is
// self-defeating: servers can infer the real version from
// version-linked behaviour, and the contradiction is a stronger bot
// signal than a plain UA read.
//
// realVer == "" (detection failed) leaves the persona untouched.
func (p *Pool) alignUAWithBinary(ua *antibot.UAProfile) *antibot.UAProfile {
	if ua == nil || p.realChromeVer == "" {
		return ua
	}
	major, _, _ := strings.Cut(p.realChromeVer, ".")
	if major == "" {
		return ua
	}
	// Real Chrome freezes the UA-string version to major.0.0.0;
	// high-entropy hints carry the full version — mimic exactly that.
	frozen := major + ".0.0.0"
	clone := *ua
	clone.UserAgent = uaVerRe.ReplaceAllString(ua.UserAgent, "$1/"+frozen)
	clone.FullVersion = p.realChromeVer
	return &clone
}

// uaMajorVersion extracts the Chrome major version ("130") from a
// Chrome-family User-Agent string, or "" when not found.
func uaMajorVersion(ua string) string {
	for _, marker := range []string{"Chrome/", "Edg/", "CriOS/"} {
		if i := strings.Index(ua, marker); i >= 0 {
			rest := ua[i+len(marker):]
			end := strings.IndexAny(rest, ". ")
			if end < 0 {
				end = len(rest)
			}
			if v := rest[:end]; v != "" {
				return v
			}
		}
	}
	return ""
}

// uaPlatform strips the JSON quoting from a UAProfile's
// SecChUaPlatform field: `"Windows"` → Windows.
func uaPlatform(p *antibot.UAProfile) string {
	if p == nil {
		return ""
	}
	return strings.Trim(p.SecChUaPlatform, `"`)
}

// uaIdentityFor converts an antibot UA profile into the stealth
// package's client-hints identity so the injected script's
// navigator.userAgentData / navigator.platform stay in sync with the
// UA string override (a mismatch between the two is a classic
// high-confidence bot signal).
func uaIdentityFor(p *antibot.UAProfile) *stealth.UAIdentity {
	if p == nil || !p.ChromeOnly() {
		// Non-Chrome personas (or nil) are never applied to browser
		// contexts — keep the browser's genuine, consistent values.
		return nil
	}
	major := uaMajorVersion(p.UserAgent)
	if major == "" {
		return nil
	}
	full := p.FullVersion
	if full == "" {
		full = major + ".0.0.0"
	}
	brands := [][2]string{
		{"Chromium", major},
		{"Google Chrome", major},
		{"Not?A_Brand", "99"},
	}
	if strings.Contains(p.UserAgent, "Edg/") {
		brands = [][2]string{
			{"Chromium", major},
			{"Microsoft Edge", major},
			{"Not?A_Brand", "99"},
		}
	}
	fullList := make([][2]string, len(brands))
	for i, b := range brands {
		v := full
		if b[0] == "Not?A_Brand" {
			v = "99"
		}
		fullList[i] = [2]string{b[0], v}
	}
	platformVersion := p.PlatformVersion
	if platformVersion == "" {
		switch uaPlatform(p) {
		case "Windows":
			platformVersion = "15.0.0"
		case "macOS":
			platformVersion = "10.15.7"
		default:
			platformVersion = "6.8.0"
		}
	}
	return &stealth.UAIdentity{
		Brands:          brands,
		FullVersion:     full,
		FullVersionList: fullList,
		Platform:        uaPlatform(p),
		PlatformVersion: platformVersion,
		Mobile:          p.SecChUaMobile == "?1",
		Architecture:    "x86",
		Bitness:         "64",
	}
}

// buildStealthScript renders the session-stable stealth payload for
// the given UA persona. The fingerprint itself is owned by the pool
// and never regenerates mid-session (a GPU that changes between two
// navigations does not exist), while UA rotation rebuilds only the
// identity section.
func (p *Pool) buildStealthScript(ua *antibot.UAProfile) string {
	return stealth.BuildScript(p.stealthFP, uaIdentityFor(ua))
}

// uaOverrideFor builds the complete CDP user-agent override: UA
// string, Accept-Language coherent with the geo persona, platform,
// and the full UserAgentMetadata so Chrome sends matching Sec-CH-UA-*
// headers. Overriding only the UA string leaves Chrome's genuine
// client hints in place — the mismatch flags the session instantly.
func (p *Pool) uaOverrideFor(ua *antibot.UAProfile) proto.NetworkSetUserAgentOverride {
	o := proto.NetworkSetUserAgentOverride{UserAgent: ua.UserAgent}
	if p.stealthFP != nil && len(p.stealthFP.Geo.Languages) > 0 {
		o.AcceptLanguage = strings.Join(p.stealthFP.Geo.Languages, ",")
	}
	if plat := uaPlatform(ua); plat != "" {
		o.Platform = plat
	}
	if id := uaIdentityFor(ua); id != nil {
		meta := &proto.EmulationUserAgentMetadata{
			Platform:        id.Platform,
			PlatformVersion: id.PlatformVersion,
			Architecture:    id.Architecture,
			Mobile:          id.Mobile,
			Bitness:         id.Bitness,
			FullVersion:     id.FullVersion,
		}
		for _, b := range id.Brands {
			meta.Brands = append(meta.Brands, &proto.EmulationUserAgentBrandVersion{
				Brand: b[0], Version: b[1]})
		}
		for _, b := range id.FullVersionList {
			meta.FullVersionList = append(meta.FullVersionList, &proto.EmulationUserAgentBrandVersion{
				Brand: b[0], Version: b[1]})
		}
		o.UserAgentMetadata = meta
	}
	return o
}

// simulateHumanBehavior performs trusted-event human interaction on
// the page: a bezier-curve mouse sweep dispatched through CDP
// (Input.dispatchMouseEvent → isTrusted=true, unlike synthetic JS
// MouseEvent dispatches which are permanently isTrusted=false and a
// well-known automation tell) followed by eased scrolling through the
// same trusted input channel.
func (p *Pool) simulateHumanBehavior(page *rod.Page) {
	defer func() {
		if r := recover(); r != nil {
			logutil.Debug("[rod] human behavior simulation recovered",
				slog.Any("panic", r))
		}
	}()

	vw, vh := p.viewportSize(page)

	// 1. Trusted bezier mouse sweep across a random chord of the
	//    viewport, with velocity easing and micro-pauses.
	start := behavior.Point{
		X: float64(p.behaviorSimRand(0, vw/4)),
		Y: float64(p.behaviorSimRand(vh/3, vh*2/3)),
	}
	end := behavior.Point{
		X: float64(p.behaviorSimRand(vw/2, vw*9/10)),
		Y: float64(p.behaviorSimRand(vh/4, vh*3/4)),
	}
	path := p.behaviorSimulator.MouseMove(start, end, 0)
	total := p.behaviorSimulator.MouseMoveDuration(start, end)
	if len(path) < 2 || total <= 0 {
		return
	}
	step := total / time.Duration(len(path))
	if step <= 0 {
		step = 12 * time.Millisecond
	}
	for _, pt := range path {
		if err := page.Mouse.MoveTo(proto.Point{X: pt.X, Y: pt.Y}); err != nil {
			return
		}
		time.Sleep(step)
	}
	// Brief reading-style hover.
	time.Sleep(time.Duration(150+p.behaviorSimRand(0, 350)) * time.Millisecond)

	// 2. Eased scroll: accelerate → uniform → decelerate, in wheel
	//    notches (a real wheel notch is ~80-120px).
	totalScroll := p.behaviorSimRand(220, 600)
	notches := totalScroll / 100
	if notches < 2 {
		notches = 2
	}
	for i := 0; i < notches; i++ {
		// Ease-in then ease-out via triangular delay profile.
		progress := float64(i) / float64(notches-1)
		delayMs := 40 + int(90*(1-progress*progress)) + p.behaviorSimRand(0, 45)
		if err := page.Mouse.Scroll(0, float64(p.behaviorSimRand(80, 120)), 1); err != nil {
			return
		}
		time.Sleep(time.Duration(delayMs) * time.Millisecond)
	}
}

// viewportSize returns the CSS viewport dimensions of the page,
// falling back to the fingerprint's screen size on failure.
func (p *Pool) viewportSize(page *rod.Page) (int, int) {
	res, err := page.Eval(`() => [window.innerWidth, window.innerHeight]`)
	if err == nil && res != nil && len(res.Value.Arr()) == 2 {
		arr := res.Value.Arr()
		w := int(arr[0].Int())
		h := int(arr[1].Int())
		if w > 0 && h > 0 {
			return w, h
		}
	}
	if p.stealthFP != nil {
		return p.stealthFP.ScreenWidth, p.stealthFP.ScreenHeight
	}
	return 1920, 1080
}

// behaviorSimRand is a light convenience wrapper for simulation jitter.
func (p *Pool) behaviorSimRand(min, max int) int {
	if max <= min {
		return min
	}
	return min + rand.Intn(max-min+1)
}

// selfCheckRow is one coherence assertion evaluated inside the page.
type selfCheckRow struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// selfCheckJS runs once per session inside a real page after load and
// reports one row per stealth coherence assertion. It turns invisible
// drift (a persona field that did not survive, a binary capability
// that contradicts the persona) into an observable signal.
const selfCheckJS = `() => {
	const rows = [];
	const add = (name, ok, detail) => rows.push({name, ok: !!ok, detail: String(detail)});
	try {
		add('webdriver', navigator.webdriver === undefined || navigator.webdriver === false,
			typeof navigator.webdriver + '=' + navigator.webdriver);
		add('chrome-runtime', typeof window.chrome === 'object' && window.chrome !== null,
			typeof window.chrome);
		add('plugins', (navigator.plugins || {length: 0}).length > 0,
			(navigator.plugins || {length: 0}).length);
		add('languages', Array.isArray(navigator.languages) && navigator.languages.length > 0,
			(navigator.languages || []).join(','));
		add('outer-size', window.outerWidth > 0 && window.outerHeight > 0,
			window.outerWidth + 'x' + window.outerHeight);
		add('screen-size', screen.width > 0 && screen.height > 0,
			screen.width + 'x' + screen.height);
		const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
		add('timezone', !!tz, tz);
		const uad = navigator.userAgentData;
		add('ua-ch', !!uad && Array.isArray(uad.brands) && uad.brands.length > 0,
			uad ? uad.platform + ' ' + uad.brands.length + ' brands' : 'missing');
		try {
			const f = navigator.permissions.query;
			add('native-tostring', Function.prototype.toString.call(f).includes('[native code]'),
				Function.prototype.toString.call(f).slice(0, 40));
		} catch (e) { add('native-tostring', false, 'err:' + e.message); }
		try {
			const gl = document.createElement('canvas').getContext('webgl');
			const ext = gl.getExtension('WEBGL_debug_renderer_info');
			add('gpu', !!ext, ext ? gl.getParameter(ext.UNMASKED_RENDERER) : 'no-ext');
		} catch (e) { add('gpu', false, 'err:' + e.message); }
		// Codec capability is JS-visible and binary-determined: official
		// Chrome answers "probably", codec-stripped Chromium builds "".
		try {
			add('codec-h264', document.createElement('video')
				.canPlayType('video/mp4; codecs="avc1.42E01E"') !== '',
				document.createElement('video').canPlayType('video/mp4; codecs="avc1.42E01E"'));
		} catch (e) { add('codec-h264', false, 'err:' + e.message); }
	} catch (e) {
		add('suite', false, 'err:' + e.message);
	}
	return rows;
}`

// runStealthSelfCheck evaluates the coherence suite on a live page and
// cross-checks the page-side values against the session fingerprint.
// Failures surface at warn level; the full report at debug level. Runs
// once per pool lifetime (see Pool.selfCheckOnce).
func (p *Pool) runStealthSelfCheck(page *rod.Page) {
	res, err := page.Eval(selfCheckJS)
	if err != nil {
		logutil.Debug("[stealth] self-check skipped", slog.Any("error", err))
		return
	}
	raw, err := json.Marshal(res.Value)
	if err != nil {
		return
	}
	var rows []selfCheckRow
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) == 0 {
		return
	}

	byName := make(map[string]selfCheckRow, len(rows))
	for _, r := range rows {
		byName[r.Name] = r
	}

	// Cross-checks against the Go-side fingerprint persona.
	if fp := p.stealthFP; fp != nil {
		if r, ok := byName["timezone"]; ok && r.Detail != fp.Geo.Timezone {
			logutil.Warn("[stealth] timezone mismatch",
				slog.String("page", r.Detail), slog.String("persona", fp.Geo.Timezone))
		}
		if r, ok := byName["screen-size"]; ok {
			if want := fmt.Sprintf("%dx%d", fp.ScreenWidth, fp.ScreenHeight); r.Detail != want {
				logutil.Warn("[stealth] screen mismatch",
					slog.String("page", r.Detail), slog.String("persona", want))
			}
		}
	}

	failed := 0
	for _, r := range rows {
		if !r.OK {
			failed++
			logutil.Warn("[stealth] self-check failed",
				slog.String("check", r.Name), slog.String("detail", r.Detail))
		} else {
			logutil.Debug("[stealth] self-check ok",
				slog.String("check", r.Name), slog.String("detail", r.Detail))
		}
	}
	// An empty h264 codec answer means a codec-stripped Chromium build
	// (e.g. Alpine/Distroless packages): its JA3 delta and missing
	// proprietary codecs are binary-level gaps that no flag can close —
	// point the operator at the real fix.
	if r, ok := byName["codec-h264"]; ok && !r.OK {
		logutil.Warn("[stealth] binary looks like codec-stripped Chromium; " +
			"JA3/codec downgrade mode — install official Google Chrome " +
			"(browser.path) for full coherence")
	}
	logutil.Info("[stealth] self-check complete",
		slog.Int("total", len(rows)), slog.Int("failed", failed))
}
