package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSandboxConfig_DefaultsZero verifies that the sandbox subsystem
// defaults to all-zero (no limits, no lifecycle binding) so legacy
// deployments are unaffected unless an operator explicitly enables
// caps. This mirrors the contract documented in defaults.go and
// config.yaml.
func TestSandboxConfig_DefaultsZero(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	yamlContent := `
default_provider: test
providers:
  - name: test
    type: openai
    base_url: http://localhost:8080/v1
    api_key: key
    model: gpt-4o
`
	if err := os.WriteFile(
		configPath, []byte(yamlContent), 0644,
	); err != nil {
		t.Fatalf("write config: %v", err)
	}

	loader, err := NewLoader(configPath)
	if err != nil {
		t.Fatalf("NewLoader failed: %v", err)
	}
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	sb := cfg.Security.Sandbox
	if sb.KillOnParentExit {
		t.Error(
			"expected kill_on_parent_exit to default to false",
		)
	}
	if sb.Limits.MaxCPUSeconds != 0 {
		t.Errorf(
			"expected MaxCPUSeconds=0, got %d",
			sb.Limits.MaxCPUSeconds,
		)
	}
	if sb.Limits.MaxMemoryBytes != 0 {
		t.Errorf(
			"expected MaxMemoryBytes=0, got %d",
			sb.Limits.MaxMemoryBytes,
		)
	}
	if sb.Limits.MaxFileBytes != 0 {
		t.Errorf(
			"expected MaxFileBytes=0, got %d",
			sb.Limits.MaxFileBytes,
		)
	}
	if sb.Limits.MaxProcesses != 0 {
		t.Errorf(
			"expected MaxProcesses=0, got %d",
			sb.Limits.MaxProcesses,
		)
	}
}

// TestSandboxConfig_ExplicitValues verifies that explicit resource
// caps and the kill flag are decoded from YAML into the config struct.
func TestSandboxConfig_ExplicitValues(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	yamlContent := `
default_provider: test
providers:
  - name: test
    type: openai
    base_url: http://localhost:8080/v1
    api_key: key
    model: gpt-4o
security:
  sandbox:
    kill_on_parent_exit: true
    limits:
      max_cpu_seconds: 30
      max_memory_bytes: 536870912   # 512 MiB
      max_file_bytes: 10485760      # 10 MiB
      max_processes: 4
`
	if err := os.WriteFile(
		configPath, []byte(yamlContent), 0644,
	); err != nil {
		t.Fatalf("write config: %v", err)
	}

	loader, err := NewLoader(configPath)
	if err != nil {
		t.Fatalf("NewLoader failed: %v", err)
	}
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	sb := cfg.Security.Sandbox
	if !sb.KillOnParentExit {
		t.Error("expected kill_on_parent_exit=true")
	}
	if sb.Limits.MaxCPUSeconds != 30 {
		t.Errorf(
			"expected MaxCPUSeconds=30, got %d",
			sb.Limits.MaxCPUSeconds,
		)
	}
	if sb.Limits.MaxMemoryBytes != 536870912 {
		t.Errorf(
			"expected MaxMemoryBytes=536870912, got %d",
			sb.Limits.MaxMemoryBytes,
		)
	}
	if sb.Limits.MaxFileBytes != 10485760 {
		t.Errorf(
			"expected MaxFileBytes=10485760, got %d",
			sb.Limits.MaxFileBytes,
		)
	}
	if sb.Limits.MaxProcesses != 4 {
		t.Errorf(
			"expected MaxProcesses=4, got %d",
			sb.Limits.MaxProcesses,
		)
	}
}
