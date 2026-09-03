package skill

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/km269/wukong/internal/config"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

// --- calculateQualityScore ---

func TestCalculateQualityScore(t *testing.T) {
	trace := func(success bool, outLen, errCount, toolCalls int) *SkillExecutionTrace {
		return &SkillExecutionTrace{
			Success:      success,
			OutputLength: outLen,
			ErrorCount:   errCount,
			ToolCalls:    make([]SkillToolCallRecord, toolCalls),
		}
	}

	tests := []struct {
		name  string
		trace *SkillExecutionTrace
		want  float64
	}{
		{name: "failed always 0.3", trace: trace(false, 1000, 0, 0), want: 0.3},
		{name: "success empty output", trace: trace(true, 0, 0, 0), want: 0.7},
		{name: "success short output", trace: trace(true, 10, 0, 0), want: 0.7},
		{name: "success short output with error", trace: trace(true, 10, 1, 0), want: 0.5},
		{name: "medium output threshold 20", trace: trace(true, 20, 0, 0), want: 0.8},
		{name: "medium output", trace: trace(true, 99, 0, 0), want: 0.8},
		{name: "long output threshold 100", trace: trace(true, 100, 0, 0), want: 0.9},
		{name: "long output with error", trace: trace(true, 100, 1, 0), want: 0.7},
		{name: "long output plus tool calls clamps to 1.0", trace: trace(true, 100, 0, 1), want: 1.0},
		{name: "short output plus tool calls", trace: trace(true, 5, 0, 1), want: 0.8},
		{name: "overflow clamps", trace: trace(true, 500, 0, 3), want: 1.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateQualityScore(tt.trace)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("calculateQualityScore() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- captureEvolutionTrace ---

type fakeExecutionHook struct {
	traces []*SkillExecutionTrace
}

func (h *fakeExecutionHook) RecordExecution(trace *SkillExecutionTrace) {
	h.traces = append(h.traces, trace)
}

func TestCaptureEvolutionTrace_Full(t *testing.T) {
	hook := &fakeExecutionHook{}
	start := time.Now().Add(-90 * time.Second)

	inv := &agent.Invocation{}
	inv.SetState("evo_start_at", start.UnixNano())
	inv.SetState("evo_llm_calls", 3)
	inv.SetState("evo_tool_calls", []map[string]string{
		{"name": "web_search", "args": `{"query":"x"}`},
		{"name": "calc", "args": "1+1"},
	})
	inv.SetState("last_response", []byte("final output"))

	captureEvolutionTrace(
		context.Background(),
		&agent.AfterAgentArgs{Invocation: inv, Error: errors.New("boom")},
		"greet", hook,
	)

	if len(hook.traces) != 1 {
		t.Fatalf("hook traces = %d, want 1", len(hook.traces))
	}
	tr := hook.traces[0]

	if tr.SkillName != "greet" {
		t.Errorf("SkillName = %q, want greet", tr.SkillName)
	}
	if tr.StartTime.UnixNano() != start.UnixNano() {
		t.Errorf("StartTime = %v, want %v", tr.StartTime, start)
	}
	if tr.Duration <= 0 {
		t.Errorf("Duration = %v, want > 0", tr.Duration)
	}
	if tr.EndTime.IsZero() {
		t.Error("EndTime is zero")
	}
	if tr.LLMCalls != 3 {
		t.Errorf("LLMCalls = %d, want 3", tr.LLMCalls)
	}
	if len(tr.ToolCalls) != 2 {
		t.Fatalf("ToolCalls len = %d, want 2", len(tr.ToolCalls))
	}
	if tr.ToolCalls[0].Name != "web_search" ||
		tr.ToolCalls[0].Sequence != 1 {
		t.Errorf("ToolCalls[0] = %+v", tr.ToolCalls[0])
	}
	if tr.ToolCalls[1].Name != "calc" || tr.ToolCalls[1].Sequence != 2 {
		t.Errorf("ToolCalls[1] = %+v", tr.ToolCalls[1])
	}
	if tr.FinalOutput != "final output" || tr.OutputLength != 12 {
		t.Errorf("FinalOutput = %q len %d, want %q len 12",
			tr.FinalOutput, tr.OutputLength, "final output")
	}
	if tr.Error != "boom" || tr.ErrorCount != 1 {
		t.Errorf("Error = %q count %d, want boom/1", tr.Error, tr.ErrorCount)
	}
	if tr.Success {
		t.Error("Success = true, want false (error present)")
	}
	if tr.QualityScore != 0.3 {
		t.Errorf("QualityScore = %v, want 0.3", tr.QualityScore)
	}
}

func TestCaptureEvolutionTrace_TypeTolerance(t *testing.T) {
	hook := &fakeExecutionHook{}
	start := time.Now().Add(-5 * time.Minute)

	inv := &agent.Invocation{}
	inv.SetState("evo_start_at", start)
	inv.SetState("evo_llm_calls", "3") // non-int ignored
	inv.SetState("evo_tool_calls", []string{"nope"})
	inv.SetState("last_response", "text-out")

	captureEvolutionTrace(context.Background(),
		&agent.AfterAgentArgs{Invocation: inv}, "x", hook)

	tr := hook.traces[0]
	if tr.StartTime.UnixNano() != start.UnixNano() {
		t.Errorf("StartTime = %v, want %v", tr.StartTime, start)
	}
	if tr.Duration <= 0 {
		t.Errorf("Duration = %v, want > 0", tr.Duration)
	}
	if tr.LLMCalls != 0 {
		t.Errorf("LLMCalls = %d, want 0 (non-int ignored)", tr.LLMCalls)
	}
	if len(tr.ToolCalls) != 0 {
		t.Errorf("ToolCalls = %d, want 0 (wrong type ignored)", len(tr.ToolCalls))
	}
	if tr.FinalOutput != "text-out" || tr.OutputLength != 8 {
		t.Errorf("FinalOutput = %q len %d", tr.FinalOutput, tr.OutputLength)
	}
	if !tr.Success {
		t.Error("Success = false, want true (no error, output present)")
	}
}

func TestCaptureEvolutionTrace_ZeroStateDefaults(t *testing.T) {
	hook := &fakeExecutionHook{}

	// No state at all: start falls back to now, output empty -> failure.
	captureEvolutionTrace(context.Background(),
		&agent.AfterAgentArgs{Invocation: &agent.Invocation{}}, "x", hook)

	tr := hook.traces[0]
	if tr.StartTime.IsZero() {
		t.Error("StartTime is zero, want fallback to now")
	}
	if tr.OutputLength != 0 || tr.Success {
		t.Errorf("Success = %v, want false with empty output", tr.Success)
	}
	if tr.QualityScore != 0.3 {
		t.Errorf("QualityScore = %v, want 0.3", tr.QualityScore)
	}
}

func TestCaptureEvolutionTrace_NilGuard(t *testing.T) {
	hook := &fakeExecutionHook{}

	captureEvolutionTrace(context.Background(), nil, "x", hook)
	captureEvolutionTrace(context.Background(),
		&agent.AfterAgentArgs{}, "x", hook)

	if len(hook.traces) != 0 {
		t.Errorf("hook traces = %d, want 0 (nil args must be skipped)", len(hook.traces))
	}
}

// --- SkillsDir / Manager basics ---

func TestSkillsDir_DefaultAndCustom(t *testing.T) {
	if got := (&Manager{}).SkillsDir(); got != ".wukong/skills" {
		t.Errorf("SkillsDir() = %q, want default", got)
	}
	m := &Manager{cfg: config.SkillConfig{SkillsDir: "/tmp/skills"}}
	if got := m.SkillsDir(); got != "/tmp/skills" {
		t.Errorf("SkillsDir() = %q, want /tmp/skills", got)
	}
}

func TestInitialize_Disabled(t *testing.T) {
	m := NewManager(config.SkillConfig{Enabled: false})
	if err := m.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize(disabled) error = %v", err)
	}
	if m.repository != nil {
		t.Error("repository = non-nil, want nil when disabled")
	}
}

func writeTestSkill(t *testing.T, skillsDir, name, frontmatter, body string) {
	t.Helper()
	dir := filepath.Join(skillsDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := frontmatter + body
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newInitializedManager(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	writeTestSkill(t, dir, "greet",
		"---\ntype: skill\nname: greet\ndescription: Greets the user\n---\n",
		"# Greet\n\nSay hello.\n")
	m := NewManager(config.SkillConfig{Enabled: true, SkillsDir: dir})
	if err := m.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	return m, dir
}

func TestInitialize_IndexesSkills(t *testing.T) {
	m, _ := newInitializedManager(t)

	if m.SkillCount() != 1 {
		t.Errorf("SkillCount() = %d, want 1", m.SkillCount())
	}
	if !m.HasSkill("greet") {
		t.Error("HasSkill(greet) = false, want true")
	}
	if m.HasSkill("nope") {
		t.Error("HasSkill(nope) = true, want false")
	}
	sums := m.ListSummaries()
	if len(sums) != 1 || sums[0].Name != "greet" ||
		sums[0].Description != "Greets the user" {
		t.Errorf("ListSummaries() = %+v", sums)
	}

	sk, err := m.GetSkill(context.Background(), "greet")
	if err != nil {
		t.Fatalf("GetSkill() error = %v", err)
	}
	if !strings.Contains(sk.Body, "# Greet") {
		t.Errorf("GetSkill() body = %q", sk.Body)
	}

	if _, err := m.GetSkill(context.Background(), "missing"); err == nil {
		t.Error("GetSkill(missing) error = nil, want error")
	}
}

func TestGetSkill_NotInitialized(t *testing.T) {
	_, err := (&Manager{}).GetSkill(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("GetSkill() error = %v, want not-initialized", err)
	}
}

func TestRefresh(t *testing.T) {
	// Uninitialized manager.
	if err := (&Manager{}).Refresh(); err == nil {
		t.Error("Refresh() error = nil, want not-initialized")
	}

	// Initialized manager picks up newly added skills.
	m, dir := newInitializedManager(t)
	writeTestSkill(t, dir, "new-skill",
		"---\nname: new-skill\n---\n", "body\n")
	if err := m.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if m.SkillCount() != 2 || !m.HasSkill("new-skill") {
		t.Errorf("after Refresh: count = %d, want 2 with new-skill",
			m.SkillCount())
	}
}

func TestCreateSkillAgent_NotInitialized(t *testing.T) {
	_, err := (&Manager{}).CreateSkillAgent(
		context.Background(), "x", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "load skill") {
		t.Errorf("CreateSkillAgent() error = %v, want load-skill error", err)
	}
}

// --- ExportSkillsAsOKF ---

func TestExportSkillsAsOKF(t *testing.T) {
	m, _ := newInitializedManager(t)
	out := t.TempDir()

	if err := m.ExportSkillsAsOKF(out); err != nil {
		t.Fatalf("ExportSkillsAsOKF() error = %v", err)
	}

	for _, f := range []string{"index.md", "log.md",
		filepath.Join("skills", "greet", "SKILL.md")} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("missing exported file %s: %v", f, err)
		}
	}

	content, err := os.ReadFile(filepath.Join(out, "skills", "greet", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "type: skill") ||
		!strings.Contains(string(content), "# Greet") {
		t.Errorf("exported SKILL.md = %s", content)
	}
}

func TestExportSkillsAsOKF_NotInitialized(t *testing.T) {
	if err := (&Manager{}).ExportSkillsAsOKF(t.TempDir()); err == nil {
		t.Error("ExportSkillsAsOKF() error = nil, want not-initialized")
	}
}

func TestEnsureAllOKFCompliant(t *testing.T) {
	// Uninitialized manager.
	if _, err := (&Manager{}).EnsureAllOKFCompliant(); err == nil {
		t.Error("EnsureAllOKFCompliant() error = nil, want not-initialized")
	}

	// One compliant skill, one missing the type field.
	m, dir := newInitializedManager(t)
	writeTestSkill(t, dir, "no-type",
		"---\nname: no-type\n---\n", "body\n")

	modified, err := m.EnsureAllOKFCompliant()
	if err != nil {
		t.Fatalf("EnsureAllOKFCompliant() error = %v", err)
	}
	if modified != 1 {
		t.Errorf("modified = %d, want 1", modified)
	}

	content, err := os.ReadFile(
		filepath.Join(dir, "no-type", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "type: skill") {
		t.Errorf("no-type SKILL.md not patched: %s", content)
	}

	// Second run is idempotent.
	modified, err = m.EnsureAllOKFCompliant()
	if err != nil {
		t.Fatalf("EnsureAllOKFCompliant() second run error = %v", err)
	}
	if modified != 0 {
		t.Errorf("second run modified = %d, want 0", modified)
	}
}

// --- ImportOKFSkills ---

func TestImportOKFSkills(t *testing.T) {
	bundleDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bundleDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Skill with explicit title.
	os.WriteFile(filepath.Join(bundleDir, "greeting.md"),
		[]byte("---\ntype: skill\ntitle: greeting\ndescription: Greets\n---\n# Greet\n"), 0o644)
	// Skill falling back to concept ID last segment.
	os.WriteFile(filepath.Join(bundleDir, "sub", "fallback.md"),
		[]byte("---\ntype: skill\n---\n# Fallback\n"), 0o644)
	// Non-skill concept must be skipped.
	os.WriteFile(filepath.Join(bundleDir, "note.md"),
		[]byte("---\ntype: concept\n---\nnote\n"), 0o644)

	skillsDir := filepath.Join(t.TempDir(), "skills")
	m := NewManager(config.SkillConfig{SkillsDir: skillsDir})

	imported, err := m.ImportOKFSkills(bundleDir)
	if err != nil {
		t.Fatalf("ImportOKFSkills() error = %v", err)
	}
	if imported != 2 {
		t.Fatalf("imported = %d, want 2", imported)
	}

	for _, skillName := range []string{"greeting", "fallback"} {
		path := filepath.Join(skillsDir, skillName, "SKILL.md")
		content, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("missing imported skill %s: %v", path, err)
			continue
		}
		if !strings.Contains(string(content), "type: skill") {
			t.Errorf("%s content = %s, want type: skill", path, content)
		}
	}

	if _, err := os.Stat(filepath.Join(skillsDir, "note", "SKILL.md")); !os.IsNotExist(err) {
		t.Error("non-skill concept was imported")
	}
}

func TestImportOKFSkills_MissingBundle(t *testing.T) {
	m := NewManager(config.SkillConfig{
		SkillsDir: filepath.Join(t.TempDir(), "skills"),
	})
	imported, err := m.ImportOKFSkills(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("ImportOKFSkills() error = %v", err)
	}
	if imported != 0 {
		t.Errorf("imported = %d, want 0", imported)
	}
}

func TestImportOKFSkills_RefreshesRepository(t *testing.T) {
	bundleDir := t.TempDir()
	os.WriteFile(filepath.Join(bundleDir, "s.md"),
		[]byte("---\ntype: skill\n---\n# S\n"), 0o644)

	m, _ := newInitializedManager(t)
	imported, err := m.ImportOKFSkills(bundleDir)
	if err != nil {
		t.Fatalf("ImportOKFSkills() error = %v", err)
	}
	if imported != 1 {
		t.Errorf("imported = %d, want 1", imported)
	}
	// The import must have refreshed the repository: the bundled
	// skill (concept ID "s" without title) is now findable.
	if !m.HasSkill("s") {
		t.Error("HasSkill(s) = false after import refresh")
	}
	if m.SkillCount() != 2 {
		t.Errorf("SkillCount() = %d, want 2 after import refresh",
			m.SkillCount())
	}
}

// --- EnsureOKFType ---

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEnsureOKFType(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name       string
		content    string
		wantChange bool
		wantErr    bool
		check      func(t *testing.T, content string)
	}{
		{
			name:    "already has type stays unchanged",
			content: "---\ntype: skill\n---\nbody\n",
			check: func(t *testing.T, content string) {
				if !strings.Contains(content, "type: skill") {
					t.Errorf("content = %s", content)
				}
			},
		},
		{
			name:       "no frontmatter gets prefixed",
			content:    "# Title\n\nplain body",
			wantChange: true,
			check: func(t *testing.T, content string) {
				if !strings.HasPrefix(content, "---\ntype: skill\n---\n") {
					t.Errorf("content = %s", content)
				}
			},
		},
		{
			name:       "frontmatter without type gets patched",
			content:    "---\nname: x\n---\nbody\n",
			wantChange: true,
			check: func(t *testing.T, content string) {
				if !strings.Contains(content, "type: skill") ||
					!strings.Contains(content, "name: x") {
					t.Errorf("content = %s", content)
				}
			},
		},
		{
			name:       "empty type value gets patched",
			content:    "---\ntype: \"\"\n---\nbody\n",
			wantChange: true,
			check: func(t *testing.T, content string) {
				if !strings.Contains(content, "type: skill") {
					t.Errorf("content = %s", content)
				}
			},
		},
		{
			name:       "non-string type gets patched",
			content:    "---\ntype: 123\n---\nbody\n",
			wantChange: true,
			check: func(t *testing.T, content string) {
				if !strings.Contains(content, "type: skill") {
					t.Errorf("content = %s", content)
				}
			},
		},
		{
			name:    "malformed frontmatter skipped",
			content: "---\nno closing\n",
			check: func(t *testing.T, content string) {
				if !strings.Contains(content, "no closing") {
					t.Errorf("content modified: %s", content)
				}
			},
		},
		{
			name:    "unparseable yaml skipped",
			content: "---\n: : :\n---\nbody\n",
			check: func(t *testing.T, content string) {
				if strings.Contains(content, "type: skill") {
					t.Errorf("content modified: %s", content)
				}
			},
		},
		{
			name:       "crlf frontmatter patched",
			content:    "---\r\nname: x\r\n---\r\nbody\r\n",
			wantChange: true,
			check: func(t *testing.T, content string) {
				if !strings.Contains(content, "type: skill") {
					t.Errorf("content = %s", content)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := writeFile(t, dir, "SKILL.md", tt.content)
			changed, err := EnsureOKFType(p)
			if tt.wantErr {
				if err == nil {
					t.Fatal("EnsureOKFType() error = nil, want err")
				}
				return
			}
			if err != nil {
				t.Fatalf("EnsureOKFType() error = %v", err)
			}
			if changed != tt.wantChange {
				t.Errorf("changed = %v, want %v", changed, tt.wantChange)
			}
			got, _ := os.ReadFile(p)
			tt.check(t, string(got))
		})
	}
}

func TestEnsureOKFType_MissingFile(t *testing.T) {
	_, err := EnsureOKFType(filepath.Join(t.TempDir(), "SKILL.md"))
	if err == nil {
		t.Error("EnsureOKFType() error = nil, want read error")
	}
}
