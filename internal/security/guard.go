// Package security provides safety mechanisms for extension execution.
// Implements Goose's security features:
// - Multi-mode permission control (auto/manual/smart/chat_only)
// - Malware scanning for external extensions
// - Tool execution timeout enforcement
// - Dangerous command blocking
// - Fine-grained tool allowlist/denylist
// - User approval for destructive operations
package security

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/km269/wukong/internal/config"
)

// Guard provides security enforcement for tool execution.
type Guard struct {
	mu  sync.RWMutex
	cfg *config.SecurityConfig

	// Runtime state
	approvedCommands map[string]bool
	blockedCount     int

	// IgnoreMatcher provides file-access blacklisting via
	// .wukongignore (gitignore-compatible syntax).
	ignoreMatcher *IgnoreMatcher
}

// NewGuard creates a new security guard.
func NewGuard(cfg *config.SecurityConfig) *Guard {
	if cfg == nil {
		cfg = &config.SecurityConfig{
			DefaultTimeout:        30 * time.Second,
			MaxTimeout:            300 * time.Second,
			BlockDangerousCommands: true,
			PermissionMode:        config.PermissionSmart,
		}
	}
	if cfg.PermissionMode == "" {
		cfg.PermissionMode = config.PermissionSmart
	}
	im := NewIgnoreMatcher(cfg.IgnoreFile, cfg.IgnoreFileEnabled)

	return &Guard{
		cfg:              cfg,
		approvedCommands: make(map[string]bool),
		ignoreMatcher:    im,
	}
}

// GetPermissionMode returns the current permission mode.
func (g *Guard) GetPermissionMode() config.PermissionMode {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.cfg.PermissionMode
}

// SetPermissionMode changes the permission mode at runtime.
func (g *Guard) SetPermissionMode(mode config.PermissionMode) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cfg.PermissionMode = mode
}

// CheckToolPermission verifies if a tool is allowed to execute
// based on fine-grained permissions, allowlist/denylist, and the
// current permission mode.
func (g *Guard) CheckToolPermission(
	toolName string, permissions []config.ToolPermission,
) error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	// Chat-only mode: block all tool calls
	if g.cfg.PermissionMode == config.PermissionChatOnly {
		return fmt.Errorf(
			"tool %q blocked: chat_only permission mode active",
			toolName,
		)
	}

	// Check denylist first (case-insensitive)
	toolLower := strings.ToLower(toolName)
	for _, blocked := range g.cfg.Denylist {
		if strings.ToLower(blocked) == toolLower || blocked == "*" {
			return fmt.Errorf(
				"tool %q is in the denylist", toolName,
			)
		}
	}

	// Check allowlist if configured (case-insensitive)
	if len(g.cfg.Allowlist) > 0 {
		allowed := false
		for _, a := range g.cfg.Allowlist {
			if strings.ToLower(a) == toolLower || a == "*" {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf(
				"tool %q is not in the allowlist", toolName,
			)
		}
	}

	// Check explicit permissions
	if len(permissions) == 0 {
		return nil // No restrictions
	}

	for _, perm := range permissions {
		if perm.Tool == toolName || perm.Tool == "*" {
			if !perm.Allowed {
				return fmt.Errorf(
					"tool %q is blocked by permission policy",
					toolName,
				)
			}
			return nil
		}
	}

	// Default: if permissions exist but tool not listed, allow
	return nil
}

// NeedsApproval checks whether a tool call requires user approval
// based on the current permission mode and the risk level of the
// operation.
func (g *Guard) NeedsApproval(toolName string, argsJSON []byte) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	switch g.cfg.PermissionMode {
	case config.PermissionAuto:
		return false
	case config.PermissionManual:
		return true
	case config.PermissionChatOnly:
		return true
	case config.PermissionSmart:
		// In smart mode, only high-risk operations need approval
		return g.isHighRiskOperation(toolName, argsJSON)
	default:
		// Default to smart behavior
		return g.isHighRiskOperation(toolName, argsJSON)
	}
}

// isHighRiskOperation determines if a tool call is high-risk and
// should require user approval in smart mode.
func (g *Guard) isHighRiskOperation(toolName string, argsJSON []byte) bool {
	// High-risk tool categories (case-insensitive).
	toolLower := strings.ToLower(toolName)
	highRiskTools := map[string]bool{
		"bash":               true,
		"execute_command":    true,
		"run_command":        true,
		"shell":              true,
		"terminal":           true,
		"command":            true,
		"command_execute":    true,
		"file_write":         true,
		"file_replace":       true,
		"file_delete":        true,
		"browser_navigate":   true,
		"browser_screenshot": true,
		"browser_click":      true,
		"browser_fill":       true,
		"web_fetch":          true,
		"apps_create":        true,
		"apps_deploy":        true,
	}

	if highRiskTools[toolLower] {
		// For command tools, check the actual command content
		if isCommandTool(toolName) && len(argsJSON) > 0 {
			cmd := extractCommandFromArgs(argsJSON)
			if cmd != "" {
				return isDangerousCommand(cmd)
			}
		}
		return true
	}

	return false
}

// isDangerousCommand checks if a command contains high-risk patterns.
func isDangerousCommand(command string) bool {
	cmdLower := strings.ToLower(command)
	dangerous := []string{
		"rm -rf", "sudo ", "chmod 777", "chown",
		"dd if=", "mkfs.", "format",
		"curl | sh", "wget -O - | sh",
		">/dev/sda", ">/dev/sdb",
		"git push --force", "git push -f",
		"docker rm -f", "docker system prune",
	}
	for _, d := range dangerous {
		if strings.Contains(cmdLower, strings.ToLower(d)) {
			return true
		}
	}
	return false
}

// ValidateCommand checks if a command is safe to execute.
// The read lock is released before returning a blocking result to allow
// incrementBlockedCount to acquire a write lock without deadlock.
func (g *Guard) ValidateCommand(command string) error {
	g.mu.RLock()

	if !g.cfg.BlockDangerousCommands {
		g.mu.RUnlock()
		return nil
	}

	cmdLower := strings.ToLower(command)
	for _, blocked := range g.cfg.BlockedCommands {
		if strings.Contains(cmdLower, strings.ToLower(blocked)) {
			g.mu.RUnlock()
			g.incrementBlockedCount()
			return fmt.Errorf(
				"command blocked by security policy: "+
					"contains dangerous pattern %q", blocked,
			)
		}
	}

	g.mu.RUnlock()
	return nil
}

// incrementBlockedCount atomically increments the blocked command counter.
func (g *Guard) incrementBlockedCount() {
	g.mu.Lock()
	g.blockedCount++
	g.mu.Unlock()
}

// GetTimeout returns the appropriate timeout for a tool execution.
func (g *Guard) GetTimeout(requested time.Duration) time.Duration {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if requested <= 0 {
		return g.cfg.DefaultTimeout
	}

	if requested > g.cfg.MaxTimeout {
		return g.cfg.MaxTimeout
	}

	return requested
}

// RequireApproval checks if the operation requires user approval.
// This is the legacy interface; prefer NeedsApproval for new code.
func (g *Guard) RequireApproval(command string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	// Manual mode: everything needs approval
	if g.cfg.PermissionMode == config.PermissionManual {
		return true
	}

	// Auto mode: nothing needs approval
	if g.cfg.PermissionMode == config.PermissionAuto {
		return false
	}

	// Chat-only mode: tools blocked entirely
	if g.cfg.PermissionMode == config.PermissionChatOnly {
		return true
	}

	// Legacy flag support
	if !g.cfg.RequireApproval {
		return false
	}

	return isDangerousCommand(command)
}

// ScanExtension performs malware scanning on an extension's command.
// This is a multi-layered check that validates:
// 1. Known dangerous command patterns
// 2. Suspicious binary paths (e.g., piping to shell)
// 3. Network exfiltration attempts
// 4. File system destruction patterns
func (g *Guard) ScanExtension(
	command string, args []string,
) error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if !g.cfg.MalwareScanEnabled {
		return nil
	}

	// Layer 1: Check for known dangerous patterns in command/args
	fullCommand := command + " " + strings.Join(args, " ")

	suspiciousPatterns := []struct {
		pattern string
		desc    string
	}{
		{"rm -rf /", "destructive recursive deletion of root"},
		{"dd if=/dev/zero", "disk zeroing attempt"},
		{"curl | sh", "pipe curl output to shell"},
		{"wget -O - | sh", "pipe wget output to shell"},
		{"eval(", "dynamic code evaluation"},
		{">/dev/sda", "direct disk overwrite"},
		{"mkfs.", "filesystem format attempt"},
		{"nc -e", "netcat backdoor"},
		{"python -c 'import os", "Python code execution via CLI"},
		{"ssh -o StrictHostKeyChecking=no", "insecure SSH connection"},
		{"curl.*-F.*@", "potential file exfiltration via curl"},
		{"base64.*-d.*|", "base64 decoded pipe execution"},
	}

	cmdLower := strings.ToLower(fullCommand)
	for _, sp := range suspiciousPatterns {
		if strings.Contains(cmdLower, strings.ToLower(sp.pattern)) {
			g.incrementBlockedCount()
			return fmt.Errorf(
				"malware scan blocked: %s (pattern: %q)",
				sp.desc, sp.pattern,
			)
		}
	}

	// Layer 2: Check binary path for suspicious locations
	if strings.Contains(command, "/tmp/") ||
		strings.Contains(command, "\\Temp\\") {
		g.incrementBlockedCount()
		return fmt.Errorf(
			"malware scan blocked: binary in temporary directory")
	}

	// Layer 3: Check for hidden file access patterns
	if strings.Contains(fullCommand, "/.") &&
		strings.Contains(cmdLower, "rm ") {
		g.incrementBlockedCount()
		return fmt.Errorf(
			"malware scan blocked: deletion of hidden files")
	}

	return nil
}

// ApproveCommand marks a command as user-approved.
func (g *Guard) ApproveCommand(command string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.approvedCommands[command] = true
}

// IsApproved checks if a command was previously approved.
func (g *Guard) IsApproved(command string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.approvedCommands[command]
}

// GetBlockedCount returns the number of blocked commands.
func (g *Guard) GetBlockedCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.blockedCount
}

// CheckFilePath validates a file path against the .wukongignore
// blacklist. Returns an IgnoreError if the path should be blocked.
func (g *Guard) CheckFilePath(filePath string) error {
	if g.ignoreMatcher == nil {
		return nil
	}
	return g.ignoreMatcher.CheckFilePath(filePath)
}

// ExecuteSafe runs a command with security checks.
func (g *Guard) ExecuteSafe(
	ctx context.Context, command string,
	workDir string, timeout time.Duration,
) (string, string, int, error) {
	// Validate command
	if err := g.ValidateCommand(command); err != nil {
		return "", "", -1, err
	}

	// Check approval
	if g.RequireApproval(command) && !g.IsApproved(command) {
		return "", "", -1, fmt.Errorf(
			"command %q requires user approval", command,
		)
	}

	// Apply timeout
	actualTimeout := g.GetTimeout(timeout)
	execCtx, cancel := context.WithTimeout(ctx, actualTimeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(execCtx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(execCtx, "sh", "-c", command)
	}
	if workDir != "" {
		cmd.Dir = workDir
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return "", "", -1, fmt.Errorf(
				"command execution failed: %w", err,
			)
		}
	}

	return stdout.String(), stderr.String(), exitCode, nil
}

// Shared helpers for command extraction used by both Guard and agent loop.

// isCommandTool checks if a tool name corresponds to a command execution tool.
func isCommandTool(toolName string) bool {
	commandTools := []string{
		"bash", "execute_command", "run_command",
		"shell", "terminal", "command",
		"command_execute",
	}
	for _, t := range commandTools {
		if strings.EqualFold(toolName, t) {
			return true
		}
	}
	return false
}

// extractCommandFromArgs extracts a command string from tool arguments JSON.
func extractCommandFromArgs(args []byte) string {
	var data map[string]any
	if err := json.Unmarshal(args, &data); err != nil {
		return ""
	}
	for _, key := range []string{"command", "cmd", "shell", "script"} {
		if val, ok := data[key]; ok {
			if s, ok := val.(string); ok {
				return s
			}
		}
	}
	return ""
}
