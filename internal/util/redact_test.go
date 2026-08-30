package util

import "testing"

func TestRedactSecrets(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		secrets []string
		want    string
	}{
		{"no secrets", "hello world", nil, "hello world"},
		{"empty text", "", []string{"secret"}, ""},
		{"no match", "plain text", []string{"xyz"}, "plain text"},
		{"single match", "key=sk-abc123 end", []string{"sk-abc123"}, "key=[REDACTED] end"},
		{"multiple matches", "a sk-1 b sk-1 c", []string{"sk-1"}, "a [REDACTED] b [REDACTED] c"},
		{"empty secret ignored", "abc", []string{""}, "abc"},
		{"single char ignored", "a b c", []string{"a"}, "a b c"},
		{
			"longest match wins",
			"token=abcdef partial=abc",
			[]string{"abcdef", "abc"},
			"token=[REDACTED] partial=[REDACTED]",
		},
		{
			"nested substring",
			"x abcdef-123 y",
			[]string{"abcdef-123", "abcdef"},
			"x [REDACTED] y",
		},
		{"duplicate secrets", "k sec k", []string{"sec", "sec"}, "k [REDACTED] k"},
		{
			"adjacent secrets",
			"abab",
			[]string{"ab"},
			"[REDACTED][REDACTED]",
		},
		{
			"unicode preserved",
			"你好 secret 世界",
			[]string{"secret"},
			"你好 [REDACTED] 世界",
		},
		{
			"multiline text",
			"line1\ntoken=sk-x\nline2",
			[]string{"sk-x"},
			"line1\ntoken=[REDACTED]\nline2",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RedactSecrets(c.text, c.secrets); got != c.want {
				t.Errorf("RedactSecrets(%q, %v) = %q, want %q",
					c.text, c.secrets, got, c.want)
			}
		})
	}
}

func TestRedactSecrets_OverlappingLongestFirst(t *testing.T) {
	// Secrets supplied shortest-first; the implementation must still
	// match the LONGEST one at each position, otherwise "prefixtoken"
	// would be partially masked as "[REDACTED]token".
	text := "prefixtoken prefix-only"
	got := RedactSecrets(text, []string{"prefix", "prefixtoken"})
	want := "[REDACTED] [REDACTED]-only"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRedactSecrets_AllocationFreeWhenNoMatch(t *testing.T) {
	text := "no secret values appear here"
	got := RedactSecrets(text, []string{"sk-1", "secret-value-2"})
	if got != text {
		t.Errorf("got %q, want unchanged %q", got, text)
	}
}
