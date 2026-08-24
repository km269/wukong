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
	{
		Name: "chrome_aggressive",
		Flags: []ChromeFlag{
			{Name: "tls-max-version", Value: "tls1.3"},
			{Name: "enable-features", Value: "AsyncTLS,EncryptedClientHello"},
		},
		Description: "TLS 1.3 with HTTP/2 negotiation preferred",
	},
	{
		Name: "chrome_privacy",
		Flags: []ChromeFlag{
			{Name: "tls-max-version", Value: "tls1.3"},
			{Name: "disable-features", Value: "NetworkService,UseDnsHttpsSvcb"},
			{Name: "force-webrtc-ip-handling-policy", Value: "disable_non_proxied_udp"},
		},
		Description: "TLS with privacy-oriented settings",
	},
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
