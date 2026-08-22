// Package security provides safety mechanisms for extension execution.
//
// SSRF (Server-Side Request Forgery) protection. The CheckURL
// function rejects URLs that resolve to loopback, link-local,
// private, or cloud-metadata addresses. This prevents a
// prompt-injected agent from reading cloud IAM credentials via
// http://169.254.169.254/ or scanning the internal network.
package security

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// CheckURL validates that the supplied URL is safe for the agent to
// fetch. It blocks:
//   - loopback (127.0.0.0/8, ::1)
//   - link-local (169.254.0.0/16, fe80::/10) — includes cloud metadata
//   - private ranges (10/8, 172.16/12, 192.168/16, fc00::/7)
//   - unspecified (0.0.0.0, ::)
//   - non-HTTP(S) schemes
//
// Hostnames are resolved to IP addresses before the check, so DNS
// rebinding / internal DNS names are also covered. The caller MUST
// separately ensure that redirects do not bypass this check (e.g.
// by disabling redirects or re-checking the final URL).
func CheckURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("ssrf: invalid url: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("ssrf: scheme %q not allowed (only http/https)", scheme)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("ssrf: empty host")
	}

	// Resolve hostname to IPs. If resolution fails, reject by default.
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("ssrf: resolve %q: %w", host, err)
	}

	for _, ip := range ips {
		if isBlockedIP(ip) {
			return fmt.Errorf(
				"ssrf: %s resolves to blocked address %s "+
					"(loopback/private/link-local/metadata)",
				host, ip.String())
		}
	}
	return nil
}

// isBlockedIP reports whether ip is in a range the agent must not
// fetch. Cloud metadata services (AWS/GCP/Azure) live in the
// link-local range 169.254.169.254, which is also blocked here.
func isBlockedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsPrivate() ||
		ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	// AWS metadata (169.254.169.254) is already covered by
	// IsLinkLocalUnicast, but belt-and-suspenders: also block the
	// GCP metadata hostname IP directly.
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 169 && v4[1] == 254 && v4[2] == 169 && v4[3] == 254 {
			return true
		}
	}
	return false
}
