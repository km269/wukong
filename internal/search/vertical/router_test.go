package vertical

import "testing"

func TestDetectIntent(t *testing.T) {
	d := NewIntentDetector()
	cases := []struct {
		query string
		want  Intent
	}{
		// Academic / arXiv
		{"latest paper on transformer architectures", IntentAcademic},
		{"arxiv LLM research on reasoning", IntentAcademic},
		{"论文 关于大模型", IntentAcademic},
		// Code / GitHub
		{"go http client example code", IntentCode},
		{"github repo for web scraper", IntentCode},
		{"python function snippet for sorting", IntentCode},
		{"代码 实现 排序", IntentCode},
		// Encyclopedia / Wikipedia
		{"what is quantum entanglement", IntentEncyclopedia},
		{"who was Alan Turing", IntentEncyclopedia},
		{"define recursion", IntentEncyclopedia},
		{"什么是 量子计算", IntentEncyclopedia},
		// Discussion / Reddit
		{"reddit discussion on rust vs go", IntentDiscussion},
		{"hacker news thread about AI", IntentDiscussion},
		{"opinion on framework X", IntentDiscussion},
		// General (no match)
		{"hello world", IntentGeneral},
		{"", IntentGeneral},
		{"buy milk", IntentGeneral},
	}
	for _, c := range cases {
		got := d.Detect(c.query)
		if got != c.want {
			t.Errorf("Detect(%q) = %q; want %q", c.query, got, c.want)
		}
	}
}

func TestSanitizeQuery(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"  hello   world  ", "hello world"},
		{"single", "single"},
		{"", ""},
	}
	for _, c := range cases {
		got := SanitizeQuery(c.in)
		if got != c.want {
			t.Errorf("SanitizeQuery(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestEffectiveMergeMode(t *testing.T) {
	cases := []struct {
		mode string
		want string
	}{
		{"", MergePrepend},
		{"prepend", MergePrepend},
		{"append", MergeAppend},
		{"replace", MergeReplace},
		{"invalid", MergePrepend},
	}
	for _, c := range cases {
		cfg := Config{MergeMode: c.mode}
		got := cfg.EffectiveMergeMode()
		if got != c.want {
			t.Errorf("EffectiveMergeMode(%q) = %q; want %q", c.mode, got, c.want)
		}
	}
}

func TestNewRouterDisabled(t *testing.T) {
	// Disabled config returns nil router.
	r := NewRouter(Config{Enabled: false})
	if r != nil {
		t.Error("expected nil router for disabled config")
	}
	// Enabled config returns non-nil router.
	r = NewRouter(Config{Enabled: true, TopN: 3})
	if r == nil {
		t.Fatal("expected non-nil router for enabled config")
	}
	if !r.Enabled() {
		t.Error("Enabled() should be true")
	}
	if r.cfg.TopN != 3 {
		t.Errorf("TopN = %d; want 3", r.cfg.TopN)
	}
	if len(r.backends) != 4 {
		t.Errorf("backends count = %d; want 4", len(r.backends))
	}
}

func TestRouterDetectIntent(t *testing.T) {
	r := NewRouter(Config{Enabled: true})
	if r == nil {
		t.Fatal("router is nil")
	}
	if r.DetectIntent("latest paper on X") != IntentAcademic {
		t.Error("expected academic intent")
	}
	if r.DetectIntent("random query") != IntentGeneral {
		t.Error("expected general intent")
	}
}
