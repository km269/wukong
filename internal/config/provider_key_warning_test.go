package config

import (
	"strings"
	"testing"
)

// Tests for the empty-api_key warning (cloud providers resolve to an
// empty key → requests go out without Authorization). Local backends
// and acp are exempt; the default provider gets a dedicated message.
func TestWarnings_EmptyAPIKey(t *testing.T) {
	t.Run("default provider warns prominently", func(t *testing.T) {
		cfg := &WukongConfig{
			DefaultProvider: "zhipu",
			Providers: []ProviderConfig{
				{Name: "zhipu", Type: "openai",
					BaseURL: "https://open.bigmodel.cn/api/paas/v4"},
			},
		}
		var found bool
		for _, w := range cfg.Warnings() {
			if strings.Contains(w, "default provider") &&
				strings.Contains(w, "zhipu") &&
				strings.Contains(w, "without Authorization") {
				found = true
			}
		}
		if !found {
			t.Fatal("no dedicated default-provider empty-key warning")
		}
	})

	t.Run("non-default cloud provider warns", func(t *testing.T) {
		cfg := &WukongConfig{
			DefaultProvider: "main",
			Providers: []ProviderConfig{
				{Name: "main", Type: "openai", APIKey: "sk-x"},
				{Name: "backup", Type: "openai", BaseURL: "https://x/v1"},
			},
		}
		var found bool
		for _, w := range cfg.Warnings() {
			if strings.Contains(w, "providers[backup].api_key is empty") {
				found = true
			}
		}
		if !found {
			t.Fatal("no empty-key warning for non-default provider")
		}
	})

	t.Run("local backends and configured keys do not warn", func(t *testing.T) {
		cfg := &WukongConfig{
			DefaultProvider: "ollama",
			Providers: []ProviderConfig{
				{Name: "ollama", Type: "ollama", BaseURL: "http://x/v1"},
				{Name: "main", Type: "openai", APIKey: "sk-x",
					BaseURL: "https://x/v1"},
			},
		}
		for _, w := range cfg.Warnings() {
			if strings.Contains(w, "api_key is empty") ||
				strings.Contains(w, "empty api_key") {
				t.Fatalf("unexpected empty-key warning: %s", w)
			}
		}
	})
}
