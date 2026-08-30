package knowledge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/okf"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// --- resolveEmbedderCredentials ---

func TestResolveEmbedderCredentials_Priority(t *testing.T) {
	prov := func(name, apiKey, baseURL string) config.ProviderConfig {
		return config.ProviderConfig{Name: name, APIKey: apiKey, BaseURL: baseURL}
	}
	mkCfg := func(providers []config.ProviderConfig) *config.WukongConfig {
		return &config.WukongConfig{
			DefaultProvider: "default-llm",
			Providers: append(providers,
				prov("default-llm", "default-key", "default-url")),
		}
	}

	tests := []struct {
		name         string
		providerName string
		cfg          *config.WukongConfig
		wantKey      string
		wantURL      string
	}{
		{
			name:         "explicit provider wins",
			providerName: "emb",
			cfg: func() *config.WukongConfig {
				c := mkCfg([]config.ProviderConfig{prov("emb", "emb-key", "emb-url")})
				c.Cortex.Enabled = true
				c.Cortex.EmbeddingBaseURL = "cortex-url"
				return c
			}(),
			wantKey: "emb-key",
			wantURL: "emb-url",
		},
		{
			name:         "cortex fallback when provider missing and cortex enabled",
			providerName: "missing",
			cfg: func() *config.WukongConfig {
				c := mkCfg(nil)
				c.Cortex.Enabled = true
				c.Cortex.EmbeddingAPIKey = "cortex-key"
				c.Cortex.EmbeddingBaseURL = "cortex-url"
				return c
			}(),
			wantKey: "cortex-key",
			wantURL: "cortex-url",
		},
		{
			name:         "cortex disabled skips fallback",
			providerName: "missing",
			cfg: func() *config.WukongConfig {
				c := mkCfg(nil)
				c.Cortex.Enabled = false
				c.Cortex.EmbeddingBaseURL = "cortex-url"
				return c
			}(),
			wantKey: "default-key",
			wantURL: "default-url",
		},
		{
			name:         "cortex enabled but empty base URL skips fallback",
			providerName: "missing",
			cfg: func() *config.WukongConfig {
				c := mkCfg(nil)
				c.Cortex.Enabled = true
				c.Cortex.EmbeddingAPIKey = "cortex-key"
				return c
			}(),
			wantKey: "default-key",
			wantURL: "default-url",
		},
		{
			name:         "default provider fallback",
			providerName: "",
			cfg:          mkCfg(nil),
			wantKey:      "default-key",
			wantURL:      "default-url",
		},
		{
			name:         "empty provider and no default returns empty",
			providerName: "",
			cfg: &config.WukongConfig{
				Providers: []config.ProviderConfig{
					{Name: "other", APIKey: "other-key"},
				},
			},
			wantKey: "",
			wantURL: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, url := resolveEmbedderCredentials(tt.providerName, tt.cfg)
			if key != tt.wantKey || url != tt.wantURL {
				t.Errorf("resolveEmbedderCredentials() = (%q, %q), want (%q, %q)",
					key, url, tt.wantKey, tt.wantURL)
			}
		})
	}
}

// --- collectSources ---

func TestCollectSources(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()

	m := &Manager{cfg: &config.KnowledgeConfig{
		Sources:    []string{dirA, "", dirB},
		SourceURLs: []string{"https://example.com/doc", "", "https://example.org/other"},
	}}

	sources, err := m.collectSources()
	if err != nil {
		t.Fatalf("collectSources() error = %v", err)
	}
	// 2 dir sources + 2 url sources; empty entries skipped.
	if len(sources) != 4 {
		t.Fatalf("collectSources() len = %d, want 4", len(sources))
	}
}

func TestCollectSources_Empty(t *testing.T) {
	m := &Manager{cfg: &config.KnowledgeConfig{}}

	sources, err := m.collectSources()
	if err != nil {
		t.Fatalf("collectSources() error = %v", err)
	}
	if len(sources) != 0 {
		t.Errorf("collectSources() len = %d, want 0", len(sources))
	}
}

// --- pure helpers ---

func TestIsExportableExt(t *testing.T) {
	okExts := []string{".md", ".txt", ".json", ".yaml", ".yml", ".csv", ".html", ".xml", ".log"}
	for _, ext := range okExts {
		if !isExportableExt(ext) {
			t.Errorf("isExportableExt(%q) = false, want true", ext)
		}
	}
	badExts := []string{"", ".pdf", ".png", ".go", ".MD", ".mdx", ".docx", ".zip"}
	for _, ext := range badExts {
		if isExportableExt(ext) {
			t.Errorf("isExportableExt(%q) = true, want false", ext)
		}
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "url replaces separators", in: "https://example.com/a?b&c", want: "https_example.com_a_b_c"},
		{name: "windows path", in: "C:/temp\\file.txt", want: "C__temp_file.txt"},
		{name: "empty", in: "", want: ""},
		{name: "plain", in: "plain.txt", want: "plain.txt"},
		{
			name: "long truncated",
			in:   strings.Repeat("a", 100),
			want: strings.Repeat("a", 80),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeFilename(tt.in); got != tt.want {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSanitizeConceptID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "strips extension", in: "docs/guide.md", want: "docs/guide"},
		{name: "normalizes separators", in: `sub\dir\concept.md`, want: "sub/dir/concept"},
		{name: "spaces to hyphens", in: "my concept.md", want: "my-concept"},
		{name: "no extension", in: "plain", want: "plain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeConceptID(tt.in); got != tt.want {
				t.Errorf("sanitizeConceptID(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSanitizeConceptID_WindowsBackslash(t *testing.T) {
	// Build a path with the OS separator so the expectation holds
	// cross-platform: separators must normalize to "/" and the
	// extension must be stripped.
	in := filepath.Join("a", "b.md")
	if got := sanitizeConceptID(in); got != "a/b" {
		t.Errorf("sanitizeConceptID(%q) = %q, want %q", in, got, "a/b")
	}
}

// --- gracefulSearchTool ---

type fakeCallableTool struct {
	result any
	err    error
	gotArg []byte
}

func (f *fakeCallableTool) Declaration() *tool.Declaration {
	return nil
}

func (f *fakeCallableTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	f.gotArg = append([]byte(nil), jsonArgs...)
	return f.result, f.err
}

func TestGracefulSearchTool_NoResultsTreatedAsNormal(t *testing.T) {
	inner := &fakeCallableTool{
		err: errors.New("no relevant documents found"),
	}
	g := &gracefulSearchTool{inner: inner}

	result, err := g.Call(context.Background(), []byte(`{"query":"x"}`))
	if err != nil {
		t.Fatalf("Call() error = %v, want nil for no-results", err)
	}
	msg, ok := result.(string)
	if !ok || !strings.Contains(msg, "No relevant documents found") {
		t.Errorf("result = %v, want graceful no-results message", result)
	}
	if !strings.Contains(msg, "web search") {
		t.Errorf("result should suggest web search: %v", msg)
	}
	if string(inner.gotArg) != `{"query":"x"}` {
		t.Errorf("inner args = %s", inner.gotArg)
	}
}

func TestGracefulSearchTool_PassesThroughOtherErrors(t *testing.T) {
	boom := errors.New("embedder exploded")
	inner := &fakeCallableTool{err: boom}
	g := &gracefulSearchTool{inner: inner}

	_, err := g.Call(context.Background(), nil)
	if err != boom {
		t.Errorf("Call() error = %v, want %v", err, boom)
	}
}

func TestGracefulSearchTool_PassesThroughResults(t *testing.T) {
	inner := &fakeCallableTool{result: "some docs"}
	g := &gracefulSearchTool{inner: inner}

	result, err := g.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if result != "some docs" {
		t.Errorf("result = %v, want %v", result, "some docs")
	}
}

// --- Manager nil-safe methods ---

func TestManager_Methods_NilReceiver(t *testing.T) {
	var m *Manager

	if got := m.SearchTool(); got != nil {
		t.Errorf("SearchTool() = %v, want nil", got)
	}
	if m.IsEnabled() {
		t.Error("IsEnabled() = true, want false")
	}
	if err := m.Close(); err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}
	if _, err := m.ImportBundle(t.TempDir()); err == nil {
		t.Error("ImportBundle() error = nil, want not-initialized error")
	}
	if _, err := m.ExportBundle(t.TempDir()); err == nil {
		t.Error("ExportBundle() error = nil, want not-initialized error")
	}
}

// --- NewManager disabled branch ---

func TestNewManager_Disabled(t *testing.T) {
	m, err := NewManager(
		&config.KnowledgeConfig{Enabled: false},
		&config.WukongConfig{},
	)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if m != nil {
		t.Errorf("NewManager(disabled) = %v, want nil", m)
	}
}

// --- exportDir / okf write round trip (no network) ---

func TestExportBundle_FromLocalDir(t *testing.T) {
	srcDir := t.TempDir()
	// A non-OKF document, a file with unsupported ext (must be skipped),
	// and a directory (must be skipped).
	os.WriteFile(filepath.Join(srcDir, "guide.txt"), []byte("# Guide"), 0o644)
	os.WriteFile(filepath.Join(srcDir, "image.png"), []byte("png"), 0o644)
	os.Mkdir(filepath.Join(srcDir, "subdir"), 0o755)

	m := &Manager{cfg: &config.KnowledgeConfig{Sources: []string{srcDir}}, kb: knowledge.New()}
	outDir := t.TempDir()

	exported, err := m.ExportBundle(outDir)
	if err != nil {
		t.Fatalf("ExportBundle() error = %v", err)
	}
	if exported != 1 {
		t.Fatalf("ExportBundle() exported = %d, want 1", exported)
	}

	for _, f := range []string{"guide.md", "index.md", "log.md"} {
		if _, err := os.Stat(filepath.Join(outDir, f)); err != nil {
			t.Errorf("missing exported file %s: %v", f, err)
		}
	}
	content, err := os.ReadFile(filepath.Join(outDir, "guide.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "type: document") ||
		!strings.Contains(string(content), "# Guide") {
		t.Errorf("guide.md content = %s", content)
	}
}

func TestExportBundle_WithSourceURLs(t *testing.T) {
	m := &Manager{cfg: &config.KnowledgeConfig{
		SourceURLs: []string{"https://example.com/doc?x=1"},
	}, kb: knowledge.New()}
	outDir := t.TempDir()

	exported, err := m.ExportBundle(outDir)
	if err != nil {
		t.Fatalf("ExportBundle() error = %v", err)
	}
	if exported != 1 {
		t.Fatalf("ExportBundle() exported = %d, want 1", exported)
	}

	// The URL concept is written under urls/<sanitized>.md.
	name := sanitizeFilename("https://example.com/doc?x=1")
	if _, err := os.Stat(filepath.Join(outDir, "urls", name+".md")); err != nil {
		t.Errorf("missing URL concept: %v", err)
	}
}

func TestExportDir_SkipsUnsupportedExt(t *testing.T) {
	srcDir := t.TempDir()
	os.WriteFile(filepath.Join(srcDir, "arch.bin"), []byte("bin"), 0o644)

	bundleDir := t.TempDir()
	bundle := &okf.Bundle{RootDir: bundleDir, Concepts: []*okf.Concept{}}

	count, err := (&Manager{}).exportDir(srcDir, bundle)
	if err != nil {
		t.Fatalf("exportDir() error = %v", err)
	}
	if count != 0 {
		t.Errorf("exportDir() count = %d, want 0", count)
	}
}

func TestExportDir_ReadsTextFile(t *testing.T) {
	// exportDir only walks the given dir; a readable text file becomes
	// one exported concept.
	srcDir := t.TempDir()
	os.WriteFile(filepath.Join(srcDir, "a.md"), []byte("# A"), 0o644)

	bundleDir := t.TempDir()
	bundle := &okf.Bundle{RootDir: bundleDir, Concepts: []*okf.Concept{}}

	count, err := (&Manager{}).exportDir(srcDir, bundle)
	if err != nil {
		t.Fatalf("exportDir() error = %v", err)
	}
	if count != 1 {
		t.Errorf("exportDir() count = %d, want 1", count)
	}
}
