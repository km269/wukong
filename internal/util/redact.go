package util

import "strings"

// RedactSecrets replaces every occurrence of any secret value in text
// with "[REDACTED]". Secrets are matched longest-first so a longer
// secret is not partially masked by a shorter one contained in it
// (e.g. an API key used as the prefix of a full token). Empty or
// single-character secrets are ignored to avoid destroying normal
// text. The function is allocation-minimal for the common case where
// no secret is present (it returns text unchanged when nothing
// matches).
func RedactSecrets(text string, secrets []string) string {
	if text == "" || len(secrets) == 0 {
		return text
	}

	// Collect only secrets that are actually present, longest first.
	// Longer secrets must win over shorter ones that are their
	// substrings, so we scan for the longest match at each position.
	var present []string
	seen := make(map[string]bool)
	for _, s := range secrets {
		if len(s) < 2 || seen[s] || !strings.Contains(text, s) {
			continue
		}
		seen[s] = true
		present = append(present, s)
	}
	if len(present) == 0 {
		return text
	}
	sortByLenDesc(present)

	var sb strings.Builder
	sb.Grow(len(text))
	for len(text) > 0 {
		matched := false
		for _, s := range present {
			if strings.HasPrefix(text, s) {
				sb.WriteString("[REDACTED]")
				text = text[len(s):]
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		sb.WriteByte(text[0])
		text = text[1:]
	}
	return sb.String()
}

// sortByLenDesc orders secrets by descending length (stable), so that
// overlapping secrets always mask the longest first.
func sortByLenDesc(secrets []string) {
	for i := 1; i < len(secrets); i++ {
		for j := i; j > 0 && len(secrets[j]) > len(secrets[j-1]); j-- {
			secrets[j], secrets[j-1] = secrets[j-1], secrets[j]
		}
	}
}
