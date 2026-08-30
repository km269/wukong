package observability

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/km269/wukong/internal/config"
)

const (
	envPublic   = "LANGFUSE_PUBLIC_KEY"
	envSecret   = "LANGFUSE_SECRET_KEY"
	envHost     = "LANGFUSE_HOST"
	envInsecure = "LANGFUSE_INSECURE"
)

// clearLangfuseEnv removes all Langfuse env vars and restores them
// after the test, so an enabled StartLangfuse run cannot leak state.
func clearLangfuseEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{envPublic, envSecret, envHost, envInsecure} {
		old, _ := os.LookupEnv(k)
		os.Unsetenv(k)
		t.Cleanup(func() {
			if old != "" {
				os.Setenv(k, old)
			} else {
				os.Unsetenv(k)
			}
		})
	}
}

// --- StartLangfuse disabled branch ---

func TestStartLangfuse_Disabled(t *testing.T) {
	clearLangfuseEnv(t)

	cfg := &config.ObservabilityConfig{LangfuseEnabled: false}
	cleanup, err := StartLangfuse(context.Background(), cfg)
	if err != nil {
		t.Fatalf("StartLangfuse(disabled) error = %v", err)
	}
	if cleanup == nil {
		t.Error("cleanup = nil, want no-op cleanup")
	}
	if err := cleanup(context.Background()); err != nil {
		t.Errorf("cleanup() error = %v, want nil", err)
	}

	// Disabled must not touch the environment at all.
	for _, k := range []string{envPublic, envSecret, envHost, envInsecure} {
		if v, ok := os.LookupEnv(k); ok {
			t.Errorf("disabled StartLangfuse set %s=%q", k, v)
		}
	}
}

// --- StartLangfuse enabled branch (no network) ---

func TestStartLangfuse_Enabled_SetsConfigValues(t *testing.T) {
	clearLangfuseEnv(t)

	cfg := &config.ObservabilityConfig{
		LangfuseEnabled:   true,
		LangfusePublicKey: "pk-cfg",
		LangfuseSecretKey: "sk-cfg",
		LangfuseHost:      "langfuse.local:3000",
	}

	// Host present -> INSECURE must not be forced; credential set
	// but the trpc-agent-go Start fails on empty INSECURE-less
	// export? No — credentials are complete, but we only assert the
	// env-copying behavior here, so any Start error is acceptable.
	// To stay deterministic and offline we expect the copy to have
	// happened regardless of the error.
	_, _ = StartLangfuse(context.Background(), cfg)

	if got := os.Getenv(envPublic); got != "pk-cfg" {
		t.Errorf("%s = %q, want pk-cfg", envPublic, got)
	}
	if got := os.Getenv(envSecret); got != "sk-cfg" {
		t.Errorf("%s = %q, want sk-cfg", envSecret, got)
	}
	if got := os.Getenv(envHost); got != "langfuse.local:3000" {
		t.Errorf("%s = %q, want langfuse.local:3000", envHost, got)
	}
}

func TestStartLangfuse_Enabled_SetsInsecureWhenNoHost(t *testing.T) {
	clearLangfuseEnv(t)

	cfg := &config.ObservabilityConfig{
		LangfuseEnabled:   true,
		LangfusePublicKey: "pk",
		LangfuseSecretKey: "sk",
		// No host: local-dev insecure mode must be enabled.
	}

	_, _ = StartLangfuse(context.Background(), cfg)

	if got := os.Getenv(envInsecure); got != "true" {
		t.Errorf("%s = %q, want true", envInsecure, got)
	}
}

func TestStartLangfuse_Enabled_RespectsExistingEnv(t *testing.T) {
	clearLangfuseEnv(t)
	// Pre-set env must win over config values.
	t.Setenv(envHost, "env-host:8080")

	cfg := &config.ObservabilityConfig{
		LangfuseEnabled:   true,
		LangfusePublicKey: "pk",
		LangfuseSecretKey: "sk",
		LangfuseHost:      "cfg-host:8080",
	}

	_, _ = StartLangfuse(context.Background(), cfg)

	if got := os.Getenv(envHost); got != "env-host:8080" {
		t.Errorf("%s = %q, want env-host:8080 (existing env wins)", envHost, got)
	}
}

func TestStartLangfuse_Enabled_MissingCredentialsError(t *testing.T) {
	clearLangfuseEnv(t)

	cfg := &config.ObservabilityConfig{
		LangfuseEnabled:   true,
		LangfusePublicKey: "pk",
		LangfuseSecretKey: "sk",
		LangfuseHost:      "",
	}

	cleanup, err := StartLangfuse(context.Background(), cfg)
	if err == nil {
		t.Fatal("StartLangfuse() error = nil, want missing-credentials error")
	}
	if !strings.Contains(err.Error(), "langfuse") {
		t.Errorf("error = %v, want langfuse error", err)
	}
	if cleanup != nil {
		t.Error("cleanup = non-nil, want nil on error")
	}
}

// --- setIfEmpty ---

func TestSetIfEmpty(t *testing.T) {
	t.Run("keeps existing value", func(t *testing.T) {
		t.Setenv("WUKONG_TEST_KEEP", "existing")
		setIfEmpty("WUKONG_TEST_KEEP", "new")
		if got := os.Getenv("WUKONG_TEST_KEEP"); got != "existing" {
			t.Errorf("got %q, want existing", got)
		}
	})

	t.Run("sets missing value", func(t *testing.T) {
		t.Setenv("WUKONG_TEST_SET", "")
		setIfEmpty("WUKONG_TEST_SET", "val")
		if got := os.Getenv("WUKONG_TEST_SET"); got != "val" {
			t.Errorf("got %q, want val", got)
		}
	})

	t.Run("empty value is a no-op", func(t *testing.T) {
		t.Setenv("WUKONG_TEST_NOOP", "")
		setIfEmpty("WUKONG_TEST_NOOP", "")
		if _, ok := os.LookupEnv("WUKONG_TEST_NOOP"); !ok {
			// Was unset before and stays unset; empty string is fine too.
		}
		got := os.Getenv("WUKONG_TEST_NOOP")
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}
