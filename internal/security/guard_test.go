package security

import (
	"testing"
	"time"

	"github.com/km269/wukong/internal/config"
)

func TestGuard_New(t *testing.T) {
	cfg := &config.SecurityConfig{
		DefaultTimeout:        30 * time.Second,
		MaxTimeout:            300 * time.Second,
		BlockDangerousCommands: true,
		BlockedCommands: []string{
			"rm -rf /",
			"dd if=/dev/zero",
		},
		RequireApproval:    false,
		MalwareScanEnabled: true,
	}

	guard := NewGuard(cfg)
	if guard == nil {
		t.Fatal("expected non-nil guard")
	}
	if guard.cfg != cfg {
		t.Error("guard config not set correctly")
	}
}

func TestGuard_ValidateCommand(t *testing.T) {
	cfg := &config.SecurityConfig{
		BlockDangerousCommands: true,
		BlockedCommands: []string{
			"rm -rf /",
			"dd if=/dev/zero",
			"mkfs.",
		},
	}
	guard := NewGuard(cfg)

	tests := []struct {
		name        string
		command     string
		expectError bool
	}{
		{"safe command", "ls -la", false},
		{"blocked rm", "rm -rf /", true},
		{"blocked dd", "dd if=/dev/zero", true},
		{"blocked mkfs", "mkfs.ext4 /dev/sda", true},
		{"normal echo", "echo hello", false},
		{"empty command", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := guard.ValidateCommand(tt.command)
			if tt.expectError && err == nil {
				t.Errorf("ValidateCommand(%q) expected error", tt.command)
			}
			if !tt.expectError && err != nil {
				t.Errorf("ValidateCommand(%q) unexpected error: %v", tt.command, err)
			}
		})
	}
}

func TestGuard_ValidateCommand_Disabled(t *testing.T) {
	cfg := &config.SecurityConfig{
		BlockDangerousCommands: false,
		BlockedCommands:        []string{"rm -rf /"},
	}
	guard := NewGuard(cfg)

	if err := guard.ValidateCommand("rm -rf /"); err != nil {
		t.Error("expected no error when BlockDangerousCommands is disabled")
	}
}

func TestGuard_GetTimeout(t *testing.T) {
	cfg := &config.SecurityConfig{
		DefaultTimeout: 30 * time.Second,
		MaxTimeout:     300 * time.Second,
	}
	guard := NewGuard(cfg)

	if got := guard.GetTimeout(0); got != 30*time.Second {
		t.Errorf("GetTimeout(0) = %v, want 30s", got)
	}
	if got := guard.GetTimeout(60 * time.Second); got != 60*time.Second {
		t.Errorf("GetTimeout(60s) = %v, want 60s", got)
	}
	if got := guard.GetTimeout(600 * time.Second); got != 300*time.Second {
		t.Errorf("GetTimeout(600s) = %v, want 300s (max)", got)
	}
}

func TestGuard_RequireApproval(t *testing.T) {
	cfg := &config.SecurityConfig{
		RequireApproval: true,
	}
	guard := NewGuard(cfg)

	// Dangerous commands should require approval
	if !guard.RequireApproval("rm -rf /tmp/test") {
		t.Error("expected rm -rf to require approval")
	}
	if !guard.RequireApproval("sudo apt install") {
		t.Error("expected sudo to require approval")
	}

	// Safe commands should not
	if guard.RequireApproval("echo hello") {
		t.Error("expected echo to not require approval")
	}
}

func TestGuard_RequireApproval_Disabled(t *testing.T) {
	cfg := &config.SecurityConfig{
		RequireApproval: false,
	}
	guard := NewGuard(cfg)

	if guard.RequireApproval("rm -rf /") {
		t.Error("expected no approval required when disabled")
	}
}

func TestGuard_CheckToolPermission(t *testing.T) {
	cfg := &config.SecurityConfig{}
	guard := NewGuard(cfg)

	// No permissions = allowed
	if err := guard.CheckToolPermission("read", nil); err != nil {
		t.Errorf("expected allowed with no permissions: %v", err)
	}

	// Blocked tool
	permissions := []config.ToolPermission{
		{Tool: "write", Allowed: false},
	}
	if err := guard.CheckToolPermission("write", permissions); err == nil {
		t.Error("expected blocked write tool")
	}

	// Allowed tool
	if err := guard.CheckToolPermission("read", permissions); err != nil {
		t.Errorf("expected allowed read tool: %v", err)
	}

	// Wildcard block
	permissions = []config.ToolPermission{
		{Tool: "*", Allowed: false},
	}
	if err := guard.CheckToolPermission("anything", permissions); err == nil {
		t.Error("expected blocked by wildcard")
	}
}

func TestGuard_ScanExtension(t *testing.T) {
	cfg := &config.SecurityConfig{
		MalwareScanEnabled: true,
	}
	guard := NewGuard(cfg)

	tests := []struct {
		name        string
		command     string
		args        []string
		expectError bool
	}{
		{"safe", "echo", []string{"hello"}, false},
		{"suspicious", "rm -rf /", nil, true},
		{"curl pipe sh", "curl", []string{"|", "sh"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := guard.ScanExtension(tt.command, tt.args)
			if tt.expectError && err == nil {
				t.Error("expected scan error")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected scan error: %v", err)
			}
		})
	}
}

func TestGuard_ApproveAndIsApproved(t *testing.T) {
	cfg := &config.SecurityConfig{}
	guard := NewGuard(cfg)

	if guard.IsApproved("test-cmd") {
		t.Error("expected not approved initially")
	}

	guard.ApproveCommand("test-cmd")
	if !guard.IsApproved("test-cmd") {
		t.Error("expected approved after ApproveCommand")
	}
}

func TestGuard_GetBlockedCount(t *testing.T) {
	cfg := &config.SecurityConfig{
		BlockDangerousCommands: true,
		BlockedCommands:        []string{"rm -rf /"},
	}
	guard := NewGuard(cfg)

	if guard.GetBlockedCount() != 0 {
		t.Error("expected 0 blocked initially")
	}

	guard.ValidateCommand("rm -rf /")
	guard.ValidateCommand("rm -rf /")

	if guard.GetBlockedCount() != 2 {
		t.Errorf("expected 2 blocked, got %d", guard.GetBlockedCount())
	}
}

func TestGuard_PermissionMode_Auto(t *testing.T) {
	cfg := &config.SecurityConfig{
		PermissionMode: config.PermissionAuto,
	}
	guard := NewGuard(cfg)

	if guard.GetPermissionMode() != config.PermissionAuto {
		t.Errorf("expected auto mode, got %s", guard.GetPermissionMode())
	}

	// Auto mode: no tool needs approval
	if guard.NeedsApproval("bash", []byte(`{"command":"rm -rf /"}`)) {
		t.Error("expected no approval in auto mode")
	}
	if guard.NeedsApproval("file_write", nil) {
		t.Error("expected no approval for file_write in auto mode")
	}
}

func TestGuard_PermissionMode_Manual(t *testing.T) {
	cfg := &config.SecurityConfig{
		PermissionMode: config.PermissionManual,
	}
	guard := NewGuard(cfg)

	// Manual mode: every tool needs approval
	if !guard.NeedsApproval("read_file", nil) {
		t.Error("expected read_file to need approval in manual mode")
	}
	if !guard.NeedsApproval("search_code", nil) {
		t.Error("expected search_code to need approval in manual mode")
	}
}

func TestGuard_PermissionMode_ChatOnly(t *testing.T) {
	cfg := &config.SecurityConfig{
		PermissionMode: config.PermissionChatOnly,
	}
	guard := NewGuard(cfg)

	// Chat-only mode: all tools blocked
	err := guard.CheckToolPermission("read_file", nil)
	if err == nil {
		t.Error("expected tool blocked in chat_only mode")
	}

	err = guard.CheckToolPermission("any_tool", nil)
	if err == nil {
		t.Error("expected any_tool blocked in chat_only mode")
	}
}

func TestGuard_PermissionMode_Smart(t *testing.T) {
	cfg := &config.SecurityConfig{
		PermissionMode: config.PermissionSmart,
	}
	guard := NewGuard(cfg)

	// Smart mode: only high-risk tools need approval
	if guard.NeedsApproval("read_file", nil) {
		t.Error("expected read_file to not need approval in smart mode")
	}

	// Command tools with dangerous commands need approval
	if !guard.NeedsApproval("bash", []byte(`{"command":"rm -rf /tmp"}`)) {
		t.Error("expected bash with rm -rf to need approval in smart mode")
	}

	// Command tools with safe commands do not need approval
	if guard.NeedsApproval("bash", []byte(`{"command":"ls -la"}`)) {
		t.Error("expected bash with ls to not need approval in smart mode")
	}

	// File operations are high-risk
	if !guard.NeedsApproval("file_write", nil) {
		t.Error("expected file_write to need approval in smart mode")
	}
	if !guard.NeedsApproval("file_delete", nil) {
		t.Error("expected file_delete to need approval in smart mode")
	}
}

func TestGuard_SetPermissionMode(t *testing.T) {
	cfg := &config.SecurityConfig{
		PermissionMode: config.PermissionSmart,
	}
	guard := NewGuard(cfg)

	guard.SetPermissionMode(config.PermissionAuto)
	if guard.GetPermissionMode() != config.PermissionAuto {
		t.Error("expected auto mode after SetPermissionMode")
	}

	guard.SetPermissionMode(config.PermissionManual)
	if guard.GetPermissionMode() != config.PermissionManual {
		t.Error("expected manual mode after SetPermissionMode")
	}
}

func TestGuard_Allowlist(t *testing.T) {
	cfg := &config.SecurityConfig{
		PermissionMode: config.PermissionSmart,
		Allowlist:      []string{"read_file", "search_code"},
	}
	guard := NewGuard(cfg)

	// Allowed tools pass
	if err := guard.CheckToolPermission("read_file", nil); err != nil {
		t.Errorf("expected read_file allowed: %v", err)
	}

	// Non-allowed tools are blocked
	if err := guard.CheckToolPermission("file_write", nil); err == nil {
		t.Error("expected file_write blocked by allowlist")
	}
}

func TestGuard_Denylist(t *testing.T) {
	cfg := &config.SecurityConfig{
		PermissionMode: config.PermissionSmart,
		Denylist:       []string{"unsafe_tool", "dangerous_script"},
	}
	guard := NewGuard(cfg)

	// Normal tools pass
	if err := guard.CheckToolPermission("read_file", nil); err != nil {
		t.Errorf("expected read_file allowed: %v", err)
	}

	// Denylisted tools blocked
	if err := guard.CheckToolPermission("unsafe_tool", nil); err == nil {
		t.Error("expected unsafe_tool blocked by denylist")
	}
	if err := guard.CheckToolPermission("dangerous_script", nil); err == nil {
		t.Error("expected dangerous_script blocked by denylist")
	}
}

func TestGuard_DenylistWildcard(t *testing.T) {
	cfg := &config.SecurityConfig{
		PermissionMode: config.PermissionSmart,
		Denylist:       []string{"*"},
	}
	guard := NewGuard(cfg)

	if err := guard.CheckToolPermission("any_tool", nil); err == nil {
		t.Error("expected all tools blocked by wildcard denylist")
	}
}

func TestGuard_AllowlistWildcard(t *testing.T) {
	cfg := &config.SecurityConfig{
		PermissionMode: config.PermissionSmart,
		Allowlist:      []string{"*"},
	}
	guard := NewGuard(cfg)

	if err := guard.CheckToolPermission("any_tool", nil); err != nil {
		t.Errorf("expected any_tool allowed by wildcard allowlist: %v", err)
	}
}

func TestGuard_NilConfig(t *testing.T) {
	guard := NewGuard(nil)
	if guard == nil {
		t.Fatal("expected non-nil guard")
	}
	if guard.GetPermissionMode() != config.PermissionSmart {
		t.Errorf("expected default smart mode, got %s",
			guard.GetPermissionMode())
	}
}
