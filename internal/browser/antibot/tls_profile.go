package antibot

import (
	"math/rand"
	"sync"
)

type ChromeFlag struct {
	Name  string
	Value string
}

type TLSProfile struct {
	Name        string
	Flags       []ChromeFlag
	Description string
}

// tlsProfiles rotates Chrome's TLS-version knobs. NOTE (2026 reality
// check): the browser's real fingerprint defense is launching the
// SYSTEM Chrome binary (rod's FindChromePath), whose ClientHello,
// cipher-suite order, GREASE values and HTTP/2 SETTINGS frames match
// genuine Chrome. Chromium builds differ subtly — no launcher flag
// can fix that, so these profiles only tweak negotiation bounds and
// must never pretend to "fix" the TLS layer. Fake feature toggles
// (e.g. the removed "AsyncTLS,EncryptedClientHello" enable-features
// value, which are not real Chrome feature names) were removed.
var tlsProfiles = []TLSProfile{
	{
		Name: "chrome_modern",
		Flags: []ChromeFlag{
			{Name: "tls-max-version", Value: "tls1.3"},
		},
		Description: "Modern Chrome TLS 1.3 with strong ciphers",
	},
	{
		Name: "chrome_conservative",
		Flags: []ChromeFlag{
			{Name: "tls-max-version", Value: "tls1.2"},
		},
		Description: "TLS 1.2 for broader compatibility",
	},
	{
		Name: "chrome_legacy_compatible",
		Flags: []ChromeFlag{
			{Name: "ssl-version-min", Value: "tls1.1"},
		},
		Description: "TLS 1.1+ fallback for legacy sites",
	},
}

// WebRTCProtectionFlags returns launcher flags that stop WebRTC from
// leaking the machine's real local/public IP while traffic is proxied
// (a classic de-anonymisation vector: proxy in Tokyo, WebRTC ICE
// candidate reveals a Shanghai residential IP).
func WebRTCProtectionFlags() []ChromeFlag {
	return []ChromeFlag{
		{Name: "force-webrtc-ip-handling-policy", Value: "disable_non_proxied_udp"},
		{Name: "webrtc-ip-handling-policy", Value: "disable_non_proxied_udp"},
	}
}

type TLSProfileManager struct {
	mu      sync.RWMutex
	current int
}

func NewTLSProfileManager() *TLSProfileManager {
	return &TLSProfileManager{current: 0}
}

func (m *TLSProfileManager) Current() TLSProfile {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return tlsProfiles[m.current]
}

func (m *TLSProfileManager) Rotate() TLSProfile {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current = rand.Intn(len(tlsProfiles))
	return tlsProfiles[m.current]
}

func (m *TLSProfileManager) RotateTo(index int) TLSProfile {
	m.mu.Lock()
	defer m.mu.Unlock()
	if index >= 0 && index < len(tlsProfiles) {
		m.current = index
	}
	return tlsProfiles[m.current]
}

func (m *TLSProfileManager) Profiles() []TLSProfile {
	return tlsProfiles
}

func (m *TLSProfileManager) FlagsForLevel(level Level) []ChromeFlag {
	if level < LevelAggressive {
		return nil
	}
	return m.Rotate().Flags
}
