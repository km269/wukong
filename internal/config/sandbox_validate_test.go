package config

import (
	"runtime"
	"strings"
	"testing"
)

// TestValidate_SandboxLimits_MemoryFloorFatal verifies that
// MaxMemoryBytes below the 1 MiB floor is a fatal error (the
// limit is too small to ever let a shell start).
func TestValidate_SandboxLimits_MemoryFloorFatal(t *testing.T) {
	cfg := &WukongConfig{
		DefaultProvider: "openai",
		Providers:       []ProviderConfig{{Name: "openai"}},
		Security: SecurityConfig{
			Sandbox: SandboxConfig{
				Limits: SandboxLimitsConfig{
					MaxMemoryBytes: 1 << 19, // 512 KiB — below floor
				},
			},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for max_memory_bytes below floor")
	}
}

// TestValidate_SandboxLimits_MemoryFloorAtBoundary verifies that
// exactly the floor (1 MiB) passes — boundary is inclusive.
func TestValidate_SandboxLimits_MemoryFloorAtBoundary(t *testing.T) {
	cfg := &WukongConfig{
		DefaultProvider: "openai",
		Providers:       []ProviderConfig{{Name: "openai"}},
		Security: SecurityConfig{
			Sandbox: SandboxConfig{
				Limits: SandboxLimitsConfig{
					MaxMemoryBytes: 1 << 20, // exactly 1 MiB
				},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected no error at floor, got: %v", err)
	}
}

// TestValidate_SandboxLimits_FileBytesFloorFatal verifies that
// MaxFileBytes below 512 bytes is a fatal error.
func TestValidate_SandboxLimits_FileBytesFloorFatal(t *testing.T) {
	cfg := &WukongConfig{
		DefaultProvider: "openai",
		Providers:       []ProviderConfig{{Name: "openai"}},
		Security: SecurityConfig{
			Sandbox: SandboxConfig{
				Limits: SandboxLimitsConfig{
					MaxFileBytes: 100, // below 512
				},
			},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for max_file_bytes below floor")
	}
}

// TestValidate_SandboxLimits_FileBytesFloorAtBoundary verifies that
// exactly 512 bytes passes — boundary is inclusive.
func TestValidate_SandboxLimits_FileBytesFloorAtBoundary(t *testing.T) {
	cfg := &WukongConfig{
		DefaultProvider: "openai",
		Providers:       []ProviderConfig{{Name: "openai"}},
		Security: SecurityConfig{
			Sandbox: SandboxConfig{
				Limits: SandboxLimitsConfig{
					MaxFileBytes: 512,
				},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected no error at floor, got: %v", err)
	}
}

// TestValidate_SandboxLimits_ZeroValuesPass verifies that the
// default (zero/unlimited) config passes validation — no false
// positives on the legacy behavior.
func TestValidate_SandboxLimits_ZeroValuesPass(t *testing.T) {
	cfg := &WukongConfig{
		DefaultProvider: "openai",
		Providers:       []ProviderConfig{{Name: "openai"}},
		Security:        SecurityConfig{Sandbox: SandboxConfig{}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected no error for zero limits, got: %v", err)
	}
}

// TestValidate_SandboxLimits_GenerousValuesPass verifies that
// sensible, generous values (well above floors) pass validation.
func TestValidate_SandboxLimits_GenerousValuesPass(t *testing.T) {
	cfg := &WukongConfig{
		DefaultProvider: "openai",
		Providers:       []ProviderConfig{{Name: "openai"}},
		Security: SecurityConfig{
			Sandbox: SandboxConfig{
				KillOnParentExit: true,
				Limits: SandboxLimitsConfig{
					MaxCPUSeconds:   30,
					MaxMemoryBytes:  1 << 30, // 1 GiB
					MaxFileBytes:    1 << 24, // 16 MiB
					MaxProcesses:    4,
				},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected no error for generous limits, got: %v", err)
	}
}

// TestWarnings_SandboxLimits_SmallMemoryWarn verifies that a
// MaxMemoryBytes between the floor and the 16 MiB recommended
// floor produces a warning (legal but likely to break shells).
func TestWarnings_SandboxLimits_SmallMemoryWarn(t *testing.T) {
	cfg := &WukongConfig{
		Security: SecurityConfig{
			Sandbox: SandboxConfig{
				Limits: SandboxLimitsConfig{
					MaxMemoryBytes: 2 << 20, // 2 MiB — above floor, below recommended
				},
			},
		},
	}
	warnings := cfg.Warnings()
	found := false
	for _, w := range warnings {
		if contains(w, "max_memory_bytes") && contains(w, "16 MiB") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf(
			"expected small-memory warning, got %v", warnings,
		)
	}
}

// TestWarnings_SandboxLimits_SmallMemoryNoWarnAtBoundary verifies
// that exactly the recommended 16 MiB does NOT produce a warning
// (boundary is exclusive on the warning side).
func TestWarnings_SandboxLimits_SmallMemoryNoWarnAtBoundary(t *testing.T) {
	cfg := &WukongConfig{
		Security: SecurityConfig{
			Sandbox: SandboxConfig{
				Limits: SandboxLimitsConfig{
					MaxMemoryBytes: 1 << 24, // exactly 16 MiB
				},
			},
		},
	}
	for _, w := range cfg.Warnings() {
		if contains(w, "max_memory_bytes") && contains(w, "16 MiB") {
			t.Errorf(
				"did not expect small-memory warning at boundary, got %q",
				w,
			)
		}
	}
}

// TestWarnings_SandboxLimits_MaxProcessesOneWarns verifies that
// MaxProcesses==1 produces a warning (shell cannot fork helpers).
func TestWarnings_SandboxLimits_MaxProcessesOneWarns(t *testing.T) {
	cfg := &WukongConfig{
		Security: SecurityConfig{
			Sandbox: SandboxConfig{
				Limits: SandboxLimitsConfig{
					MaxProcesses: 1,
				},
			},
		},
	}
	warnings := cfg.Warnings()
	found := false
	for _, w := range warnings {
		if contains(w, "max_processes=1") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf(
			"expected max_processes=1 warning, got %v", warnings,
		)
	}
}

// TestWarnings_SandboxLimits_MaxProcessesZeroDoesNotWarn verifies
// that MaxProcesses==0 (unlimited) does not produce a warning.
func TestWarnings_SandboxLimits_MaxProcessesZeroDoesNotWarn(t *testing.T) {
	cfg := &WukongConfig{
		Security: SecurityConfig{
			Sandbox: SandboxConfig{
				Limits: SandboxLimitsConfig{
					MaxProcesses: 0,
				},
			},
		},
	}
	for _, w := range cfg.Warnings() {
		if contains(w, "max_processes") {
			t.Errorf(
				"did not expect max_processes warning for 0, got %q",
				w,
			)
		}
	}
}

// TestWarnings_SandboxLimits_PlatformCoverage verifies that the
// platform-coverage warnings fire on the right OS:
//   - windows + MaxFileBytes > 0 → warn (Windows ignores FSIZE)
//   - darwin + any non-zero limit → warn (sandbox-exec doesn't enforce)
//   - linux → no platform-coverage warning
func TestWarnings_SandboxLimits_PlatformCoverage(t *testing.T) {
	// Windows: MaxFileBytes is silently ignored — surface it.
	if runtime.GOOS == "windows" {
		cfg := &WukongConfig{
			Security: SecurityConfig{
				Sandbox: SandboxConfig{
					Limits: SandboxLimitsConfig{
						MaxFileBytes: 1 << 20, // 1 MiB — above floor
					},
				},
			},
		}
		warnings := cfg.Warnings()
		found := false
		for _, w := range warnings {
			if contains(w, "max_file_bytes") &&
				contains(w, "Windows") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf(
				"expected Windows FSIZE-ignored warning, got %v",
				warnings,
			)
		}
	}
	// macOS: any non-zero limit is a no-op under sandbox-exec.
	if runtime.GOOS == "darwin" {
		cfg := &WukongConfig{
			Security: SecurityConfig{
				Sandbox: SandboxConfig{
					Limits: SandboxLimitsConfig{
						MaxCPUSeconds: 30,
					},
				},
			},
		}
		warnings := cfg.Warnings()
		found := false
		for _, w := range warnings {
			if contains(w, "macOS sandbox-exec") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf(
				"expected macOS no-enforce warning, got %v",
				warnings,
			)
		}
	}
	// Linux: no platform-coverage warning expected.
	if runtime.GOOS == "linux" {
		cfg := &WukongConfig{
			Security: SecurityConfig{
				Sandbox: SandboxConfig{
					Limits: SandboxLimitsConfig{
						MaxFileBytes: 1 << 20, // enforced by RLIMIT_FSIZE
						MaxMemoryBytes: 1 << 24,
					},
				},
			},
		}
		for _, w := range cfg.Warnings() {
			if strings.Contains(w, "sandbox-exec") ||
				strings.Contains(w, "Windows has no equivalent") {
				t.Errorf(
					"did not expect platform-coverage warning on linux, got %q",
					w,
				)
			}
		}
	}
}
