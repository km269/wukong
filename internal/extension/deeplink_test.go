package extension

import (
	"testing"

	"github.com/km269/wukong/internal/config"
)

func TestParseDeeplink_ValidStdio(t *testing.T) {
	rawURL := "wukong://extension?name=github&type=external&transport=stdio&command=npx&args=-y&args=@modelcontextprotocol/server-github"

	ext, err := parseDeeplink(rawURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ext.Name != "github" {
		t.Errorf("expected name=github, got %s", ext.Name)
	}
	if ext.Type != "external" {
		t.Errorf("expected type=external, got %s", ext.Type)
	}
	if ext.Transport != "stdio" {
		t.Errorf("expected transport=stdio, got %s", ext.Transport)
	}
	if ext.Command != "npx" {
		t.Errorf("expected command=npx, got %s", ext.Command)
	}
	if len(ext.Args) != 2 {
		t.Errorf("expected 2 args, got %d", len(ext.Args))
	}
	if !ext.Enabled {
		t.Error("expected enabled=true")
	}
}

func TestParseDeeplink_MissingName(t *testing.T) {
	_, err := parseDeeplink("wukong://extension?type=external")
	if err == nil {
		t.Error("expected error for missing name")
	}
}

func TestParseDeeplink_DefaultType(t *testing.T) {
	rawURL := "wukong://extension?name=test&transport=stdio&command=echo"

	ext, err := parseDeeplink(rawURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ext.Type != "external" {
		t.Errorf("expected default type=external, got %s", ext.Type)
	}
}

func TestParseDeeplink_SSETransport(t *testing.T) {
	rawURL := "wukong://extension?name=test&type=external&transport=sse&url=http://localhost:8080"

	ext, err := parseDeeplink(rawURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ext.URL != "http://localhost:8080" {
		t.Errorf("expected URL, got %s", ext.URL)
	}
}

func TestParseDeeplink_SSEMissingURL(t *testing.T) {
	_, err := parseDeeplink("wukong://extension?name=test&transport=sse")
	if err == nil {
		t.Error("expected error for missing URL in SSE transport")
	}
}

func TestParseDeeplink_StdioMissingCommand(t *testing.T) {
	_, err := parseDeeplink("wukong://extension?name=test&transport=stdio")
	if err == nil {
		t.Error("expected error for missing command in stdio transport")
	}
}

func TestParseDeeplink_EnvVariables(t *testing.T) {
	rawURL := "wukong://extension?name=test&transport=stdio&command=echo&env.GITHUB_TOKEN=abc123&env.DEBUG=true"

	ext, err := parseDeeplink(rawURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ext.Env["GITHUB_TOKEN"] != "abc123" {
		t.Errorf("expected GITHUB_TOKEN=abc123, got %s", ext.Env["GITHUB_TOKEN"])
	}
	if ext.Env["DEBUG"] != "true" {
		t.Errorf("expected DEBUG=true, got %s", ext.Env["DEBUG"])
	}
}

func TestParseDeeplink_ExpandEnv(t *testing.T) {
	t.Setenv("TEST_VAR", "expanded_value")

	rawURL := "wukong://extension?name=test&transport=stdio&command=echo&args=${TEST_VAR}&expand_env=true"

	ext, err := parseDeeplink(rawURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(ext.Args) == 0 || ext.Args[0] != "expanded_value" {
		t.Errorf("expected expanded arg, got %v", ext.Args)
	}
}

func TestExpandEnvVar(t *testing.T) {
	t.Setenv("HOME", "/home/test")

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"no vars", "hello world", "hello world"},
		{"single var", "path: ${HOME}/data", "path: /home/test/data"},
		{"unknown var", "${UNKNOWN}", ""},
		{"multiple vars", "${HOME}/a ${HOME}/b", "/home/test/a /home/test/b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandEnvVar(tt.input)
			if got != tt.expected {
				t.Errorf("expandEnvVar(%q) = %q, want %q",
					tt.input, got, tt.expected)
			}
		})
	}
}

func TestExtensionInfo(t *testing.T) {
	info := ExtensionInfo{
		Name:        "test",
		Type:        "builtin",
		Status:      StatusEnabled,
		Transport:   "stdio",
		ToolCount:   3,
		Permissions: []config.ToolPermission{{Tool: "read", Allowed: true}},
	}

	if info.Name != "test" {
		t.Errorf("expected name=test, got %s", info.Name)
	}
	if info.Status != StatusEnabled {
		t.Errorf("expected status=enabled, got %s", info.Status)
	}
	if info.ToolCount != 3 {
		t.Errorf("expected tool_count=3, got %d", info.ToolCount)
	}
}
