package stealth

import (
	"math/rand"
	"testing"
)

func randSource(seed int64) *rand.Rand { return rand.New(rand.NewSource(seed)) }

func TestGeoCodeFromProxyURLCredentials(t *testing.T) {
	cases := map[string]string{
		// Dash-form region parameter in username (Luminati/Bright Data style).
		"http://user-country-jp:pass@gate.provider.com:8000": "jp",
		// cc prefix form.
		"http://user-cc-kr-session-abc:pass@gate.io:8080": "kr",
		// Equals form in password.
		"http://user:pass-region=de@gate.provider.com:3128": "de",
		// Session suffix after the country code must still parse.
		"http://customer-cc-fr-sessid-xyz:pw@1.2.3.4:80": "fr",
		// UK TLD normalizes to gb persona.
		"http://user:pw@residential.uk:8080": "gb",
		// Country TLD on gateway host.
		"http://user:pw@gateway.provider.jp:8080": "jp",
		// Unrelated TLD / no signal → unknown.
		"http://user:pw@1.2.3.4:8080":                "",
		"http://plainuser:pw@gate.provider.com:8000": "",
		// Unsupported country → unknown (no persona to back it).
		"http://user-country-br:pass@gate.com:80": "",
	}
	for in, want := range cases {
		if got := GeoCodeFromProxyURL(in); got != want {
			t.Errorf("GeoCodeFromProxyURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveGeoCodePrecedence(t *testing.T) {
	// Explicit config wins over proxy inference.
	if got := ResolveGeoCode("sg", "http://user-country-jp:pw@g.com:80"); got != "sg" {
		t.Errorf("config override lost: got %q, want sg", got)
	}
	// Proxy inference applies when config empty.
	if got := ResolveGeoCode("", "http://user-country-jp:pw@g.com:80"); got != "jp" {
		t.Errorf("proxy inference lost: got %q, want jp", got)
	}
	// Case-insensitive and us → us-east normalization.
	if got := ResolveGeoCode("US", ""); got != "us-east" {
		t.Errorf("US not normalized: got %q, want us-east", got)
	}
	if got := ResolveGeoCode("UK", ""); got != "gb" {
		t.Errorf("UK not normalized: got %q, want gb", got)
	}
	// Unknown config code falls through to proxy inference? No — an
	// explicitly wrong value degrades to random (""), never silently
	// overridden by a guess.
	if got := ResolveGeoCode("zz", "http://user-country-jp:pw@g.com:80"); got != "" {
		t.Errorf("unknown config should degrade to random, got %q", got)
	}
	// Nothing to go on → random (empty selector).
	if got := ResolveGeoCode("", ""); got != "" {
		t.Errorf("no signal should give empty selector, got %q", got)
	}
}

func TestGenerateFingerprintHonorsGeo(t *testing.T) {
	fp := GenerateFingerprint(randSource(7), GeoProfileByCode("jp"))
	if fp.Geo.Code != "jp" || fp.Geo.Timezone != "Asia/Tokyo" {
		t.Fatalf("geo persona not applied: %+v", fp.Geo)
	}
	if fp.Geo.Languages[0] != "ja-JP" {
		t.Errorf("languages not coherent with jp persona: %v", fp.Geo.Languages)
	}
	// Unknown selector → random persona (non-nil, valid zone).
	fp2 := GenerateFingerprint(randSource(7), GeoProfileByCode("not-a-code"))
	if fp2.Geo.Timezone == "" {
		t.Error("random fallback produced empty timezone")
	}
}
