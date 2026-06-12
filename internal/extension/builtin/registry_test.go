package builtin

import (
	"testing"

	"github.com/km269/wukong/internal/config"
)

func TestRegisterBuiltins(t *testing.T) {
	// Start with empty config
	cfg := &config.WukongConfig{
		Browser: config.BrowserConfig{
			Enabled: true,
		},
		Visualiser: config.VisualiserConfig{
			Enabled: true,
		},
		Tutorial: config.TutorialConfig{
			Enabled: true,
		},
		TopOfMind: config.TopOfMindConfig{
			Enabled: true,
		},
		CodeMode: config.CodeModeConfig{
			Enabled: true,
		},
		Apps: config.AppsConfig{
			Enabled: true,
		},
	}

	RegisterBuiltins(cfg)

	// Should have all 8 builtins registered
	expectedNames := []string{
		"developer",
		"computer_controller",
		"memory",
		"auto_visualiser",
		"tutorial",
		"top_of_mind",
		"code_mode",
		"apps",
	}

	for _, name := range expectedNames {
		ext := cfg.FindExtension(name)
		if ext == nil {
			t.Errorf("expected extension %q to be registered", name)
		}
	}
}

func TestRegisterBuiltins_NoDuplicates(t *testing.T) {
	// Pre-register "developer" manually
	cfg := &config.WukongConfig{
		Extensions: []config.ExtensionConfig{
			{
				Name:    "developer",
				Type:    "builtin",
				Enabled: false,
			},
		},
		Browser: config.BrowserConfig{
			Enabled: false,
		},
	}

	RegisterBuiltins(cfg)

	// Developer should still be present only once
	count := 0
	for _, ext := range cfg.Extensions {
		if ext.Name == "developer" {
			count++
		}
	}
	if count != 1 {
		t.Errorf(
			"expected 1 developer extension, got %d", count,
		)
	}

	// Existing developer should keep its disabled state
	ext := cfg.FindExtension("developer")
	if ext == nil {
		t.Fatal("developer extension not found")
	}
	if ext.Enabled {
		t.Error("existing developer should remain disabled")
	}
}

func TestRegisterBuiltins_Idempotent(t *testing.T) {
	cfg := &config.WukongConfig{}

	// Register twice
	RegisterBuiltins(cfg)
	firstCount := len(cfg.Extensions)
	RegisterBuiltins(cfg)
	secondCount := len(cfg.Extensions)

	if firstCount != secondCount {
		t.Errorf(
			"registerBuiltins should be idempotent: "+
				"first=%d, second=%d",
			firstCount, secondCount,
		)
	}
}
