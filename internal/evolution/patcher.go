// Package evolution provides the skill self-evolution system.
package evolution

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/km269/wukong/internal/util"
)

// EvolutionPatcher applies LLM-generated patches to SKILL.md files,
// manages versioned backups, and performs security validation on patches.
type EvolutionPatcher struct {
	store       *VersionStore
	maxVersions int
	exportJSON  bool
	mu          sync.Mutex
}

// NewEvolutionPatcher creates a new patcher with the given version store.
func NewEvolutionPatcher(
	store *VersionStore, maxVersions int, exportJSON bool,
) *EvolutionPatcher {
	if maxVersions <= 0 {
		maxVersions = 10
	}
	return &EvolutionPatcher{
		store:       store,
		maxVersions: maxVersions,
		exportJSON:  exportJSON,
	}
}

// ApplyPatch applies a patch suggestion to a SKILL.md file.
// It performs the following steps:
//  1. Read the current SKILL.md content
//  2. Create a versioned backup (SKILL.vNNN.md)
//  3. Append the patch content to the SKILL.md body
//  4. Write the updated SKILL.md
//  5. Record the new version in the database
//
// Returns the new version number, or an error if any step fails.
func (p *EvolutionPatcher) ApplyPatch(
	suggestion *PatchSuggestion,
	skillDir string,
) (int, error) {
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if _, err := os.Stat(skillPath); os.IsNotExist(err) {
		return 0, fmt.Errorf(
			"skill file not found: %s", skillPath)
	}

	// Step 1: Read current content
	currentContent, err := os.ReadFile(skillPath)
	if err != nil {
		return 0, fmt.Errorf("read skill file: %w", err)
	}

	// Step 2: Determine version number
	currentVersion, err := p.store.GetCurrentVersion(
		suggestion.SkillName,
	)
	if err != nil {
		return 0, fmt.Errorf("get current version: %w", err)
	}
	newVersion := currentVersion + 1

	// Step 3: Create versioned backup
	backupName := fmt.Sprintf("SKILL.v%03d.md", newVersion)
	backupPath := filepath.Join(skillDir, backupName)
	if err := os.WriteFile(
		backupPath, currentContent, 0644,
	); err != nil {
		return 0, fmt.Errorf("create backup: %w", err)
	}

	// Step 4: Compute file hash
	hash := sha256.Sum256(currentContent)
	fileHash := fmt.Sprintf("%x", hash)

	// Step 5: Append the patch to the SKILL.md body
	updatedContent := appendPatchToBody(
		string(currentContent), suggestion,
	)

	// Step 6: Validate the new content is safe
	if err := validateContent(updatedContent); err != nil {
		// Remove the backup on validation failure
		_ = os.Remove(backupPath)
		return 0, fmt.Errorf("content validation failed: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Step 7: Write updated SKILL.md
	if err := os.WriteFile(
		skillPath, []byte(updatedContent), 0644,
	); err != nil {
		// Restore from backup
		_ = os.WriteFile(
			skillPath, currentContent, 0644)
		_ = os.Remove(backupPath)
		return 0, fmt.Errorf("write updated skill: %w", err)
	}

	// Step 8: Record version in database
	ver := &SkillVersion{
		SkillName:     suggestion.SkillName,
		VersionNumber: newVersion,
		BackupPath:    backupPath,
		FileHash:      fileHash,
		PatchReason:   suggestion.Reason,
		CreatedAt:     time.Now(),
	}
	if err := p.store.CreateVersion(ver); err != nil {
		// Non-fatal: version record failure doesn't undo the patch
		util.Logger.Warn("evolution: failed to record version",
			"skill", suggestion.SkillName,
			"version", newVersion,
			"error", err.Error(),
		)
	}

	// Step 9: Prune old versions
	deleted, err := p.store.PruneOldVersions(
		suggestion.SkillName, p.maxVersions,
	)
	if err != nil {
		util.Logger.Warn("evolution: failed to prune versions",
			"skill", suggestion.SkillName,
			"error", err.Error(),
		)
	} else if deleted > 0 {
		// Remove old backup files from disk
		p.cleanupOldBackups(skillDir, suggestion.SkillName)
	}

	// Step 10: Update OKF log.md
	if err := p.updateOKFLog(skillDir, suggestion, newVersion); err != nil {
		util.Logger.Warn("evolution: failed to update OKF log",
			"skill", suggestion.SkillName,
			"error", err.Error(),
		)
	}

	util.Logger.Info("evolution: patch applied successfully",
		"skill", suggestion.SkillName,
		"version", newVersion,
		"reason", suggestion.Reason,
		"confidence", suggestion.Confidence,
	)

	return newVersion, nil
}

const maxPatchSections = 5
const maxLogEntries = 100

// appendPatchToBody appends the patch content to the SKILL.md body,
// after the YAML front matter. The patch is added as a new section
// with a timestamp header, separated from existing content.
// It includes deduplication logic: if a patch with the same reason
// and problem type already exists, it replaces the old one instead
// of appending. It also limits the number of patch sections to prevent
// SKILL.md from growing indefinitely.
func appendPatchToBody(
	content string, suggestion *PatchSuggestion,
) string {
	content = strings.TrimRight(content, "\n")

	yamlEnd := findYAMLEnd(content)
	if yamlEnd < 0 {
		header := fmt.Sprintf(`---
name: %s
description: Auto-evolved skill
---
`, suggestion.SkillName)
		content = header + content
		yamlEnd = len(header) - 1
	}

	body := content[yamlEnd+1:]
	body = strings.TrimSpace(body)

	newPatchHash := patchHash(suggestion.Reason, suggestion.ProblemType)
	body, _ = removeExistingPatch(body, newPatchHash)

	body, removedCount := limitPatchSections(body, maxPatchSections)
	if removedCount > 0 {
		util.Logger.Info("evolution: removed old patch sections",
			slog.String("skill", suggestion.SkillName),
			slog.Int("removed", removedCount),
		)
	}

	timestamp := suggestion.GeneratedAt.Format("2006-01-02 15:04")
	patchSection := fmt.Sprintf(`
<!-- EVOLUTION PATCH %s - %s -->
<!-- Problem: %s -->
<!-- Type: %s | Confidence: %.2f -->

%s`,
		newPatchHash,
		timestamp,
		suggestion.Reason,
		suggestion.ProblemType,
		suggestion.Confidence,
		suggestion.DiffContent,
	)

	return content[:yamlEnd+1] + "\n" + body + "\n" + patchSection + "\n"
}

func patchHash(reason, problemType string) string {
	hash := 0
	for _, c := range reason + problemType {
		hash = ((hash << 5) - hash) + int(c)
	}
	if hash < 0 {
		hash = -hash
	}
	return fmt.Sprintf("%08x", hash)
}

func removeExistingPatch(body, targetHash string) (string, bool) {
	patchStart := strings.Index(body, "<!-- EVOLUTION PATCH")
	if patchStart < 0 {
		return body, false
	}

	lines := strings.Split(body, "\n")
	var newLines []string
	inPatch := false
	removed := false

	for _, line := range lines {
		if strings.HasPrefix(line, "<!-- EVOLUTION PATCH") {
			if strings.Contains(line, targetHash) {
				inPatch = true
				removed = true
				continue
			}
			inPatch = true
		}
		if inPatch {
			if strings.HasPrefix(line, "<!-- EVOLUTION PATCH") && !strings.Contains(line, targetHash) {
				inPatch = false
				newLines = append(newLines, line)
				continue
			}
			if line == "" && !strings.HasPrefix(line, "<!--") && !strings.HasPrefix(line, "%s") {
				continue
			}
			if !removed {
				newLines = append(newLines, line)
			}
			continue
		}
		newLines = append(newLines, line)
	}

	return strings.Join(newLines, "\n"), removed
}

func limitPatchSections(body string, maxSections int) (string, int) {
	patchCount := strings.Count(body, "<!-- EVOLUTION PATCH")
	if patchCount <= maxSections {
		return body, 0
	}

	lines := strings.Split(body, "\n")
	var newLines []string
	inPatch := false
	currentSection := 0

	for _, line := range lines {
		if strings.HasPrefix(line, "<!-- EVOLUTION PATCH") {
			currentSection++
			if currentSection <= patchCount-maxSections {
				inPatch = true
				continue
			}
			inPatch = false
		}
		if inPatch {
			continue
		}
		newLines = append(newLines, line)
	}

	return strings.Join(newLines, "\n"), patchCount - maxSections
}

// OKFLogEntry represents a single evolution change record in OKF format.
// Used for JSON export to enable external system consumption.
type OKFLogEntry struct {
	Version    int       `json:"version"`
	Timestamp  time.Time `json:"timestamp"`
	Type       string    `json:"type"`
	Reason     string    `json:"reason"`
	Confidence float64   `json:"confidence"`
	SkillName  string    `json:"skill_name"`
	PatchHash  string    `json:"patch_hash,omitempty"`
}

// OKFLog represents the complete evolution log in OKF format.
type OKFLog struct {
	SkillName string        `json:"skill_name"`
	Entries   []OKFLogEntry `json:"entries"`
}

// updateOKFLog updates the OKF log.md file and optionally exports JSON format
// in the skill directory with a record of the evolution change. This follows
// the Open Knowledge Format specification for tracking knowledge file change history.
func (p *EvolutionPatcher) updateOKFLog(
	skillDir string,
	suggestion *PatchSuggestion,
	version int,
) error {
	timestamp := time.Now()
	patchHash := patchHash(suggestion.Reason, suggestion.ProblemType)

	logEntry := OKFLogEntry{
		Version:    version,
		Timestamp:  timestamp,
		Type:       suggestion.ProblemType,
		Reason:     suggestion.Reason,
		Confidence: suggestion.Confidence,
		SkillName:  suggestion.SkillName,
		PatchHash:  patchHash,
	}

	if err := p.updateMarkdownLog(skillDir, logEntry); err != nil {
		return fmt.Errorf("update markdown log: %w", err)
	}

	if p.exportJSON {
		if err := p.updateJSONLog(skillDir, logEntry); err != nil {
			return fmt.Errorf("update JSON log: %w", err)
		}
	}

	return nil
}

// updateMarkdownLog updates the log.md file with the new entry.
func (p *EvolutionPatcher) updateMarkdownLog(
	skillDir string,
	entry OKFLogEntry,
) error {
	logPath := filepath.Join(skillDir, "log.md")

	var logContent string
	if _, err := os.Stat(logPath); err == nil {
		data, err := os.ReadFile(logPath)
		if err != nil {
			return fmt.Errorf("read log.md: %w", err)
		}
		logContent = string(data)
	} else {
		logContent = fmt.Sprintf(`# Change Log

Auto-generated by Wukong evolution engine.

## [v%d] %s

- **Type**: %s
- **Reason**: %s
- **Confidence**: %.2f
`, entry.Version, entry.Timestamp.Format("2006-01-02 15:04"),
			entry.Type, entry.Reason, entry.Confidence)
		return os.WriteFile(logPath, []byte(logContent), 0644)
	}

	newEntry := fmt.Sprintf(`
## [v%d] %s

- **Type**: %s
- **Reason**: %s
- **Confidence**: %.2f
`, entry.Version, entry.Timestamp.Format("2006-01-02 15:04"),
		entry.Type, entry.Reason, entry.Confidence)

	logContent = newEntry + logContent

	return os.WriteFile(logPath, []byte(logContent), 0644)
}

// updateJSONLog exports the evolution log to JSON format for external system consumption.
func (p *EvolutionPatcher) updateJSONLog(
	skillDir string,
	entry OKFLogEntry,
) error {
	logPath := filepath.Join(skillDir, "log.json")

	var okfLog OKFLog
	if _, err := os.Stat(logPath); err == nil {
		data, err := os.ReadFile(logPath)
		if err != nil {
			return fmt.Errorf("read log.json: %w", err)
		}
		if err := json.Unmarshal(data, &okfLog); err != nil {
			return fmt.Errorf("unmarshal log.json: %w", err)
		}
	} else {
		okfLog.SkillName = entry.SkillName
	}

	okfLog.Entries = append([]OKFLogEntry{entry}, okfLog.Entries...)

	if len(okfLog.Entries) > maxLogEntries {
		okfLog.Entries = okfLog.Entries[:maxLogEntries]
	}

	jsonData, err := json.MarshalIndent(okfLog, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal log.json: %w", err)
	}

	return os.WriteFile(logPath, jsonData, 0644)
}

// RecordRollback records a rollback event in the OKF log files.
// This provides an audit trail for skill rollback operations.
func (p *EvolutionPatcher) RecordRollback(
	skillDir string,
	skillName string,
	fromVersion int,
	toVersion int,
) error {
	timestamp := time.Now()

	entry := OKFLogEntry{
		Version:    toVersion,
		Timestamp:  timestamp,
		Type:       "rollback",
		Reason:     fmt.Sprintf("Rolled back from v%d to v%d", fromVersion, toVersion),
		Confidence: 1.0,
		SkillName:  skillName,
		PatchHash:  "",
	}

	if err := p.updateMarkdownLog(skillDir, entry); err != nil {
		return fmt.Errorf("update markdown log: %w", err)
	}

	if p.exportJSON {
		if err := p.updateJSONLog(skillDir, entry); err != nil {
			return fmt.Errorf("update JSON log: %w", err)
		}
	}

	return nil
}

// findYAMLEnd finds the end of the YAML front matter (the closing ---).
// Returns the byte offset of the last '-' of the closing delimiter,
// or -1 if not found. The caller can use content[yamlEnd+1:] to get
// the body content.
func findYAMLEnd(content string) int {
	if !strings.HasPrefix(content, "---") {
		return -1
	}
	// Find the closing "---" after the opening one.
	// content[3:] skips the opening "---".
	end := strings.Index(content[3:], "\n---")
	if end < 0 {
		return -1
	}
	// Return position of last '-' of closing "---":
	//  3 (opening "---") + end ("\n---" offset in remaining)
	//  + 3 (length of "---") - 1 (last char)
	return 3 + end + 3
}

// validateContent performs safety checks on the patched content:
//   - Must not be empty
//   - Must not contain dangerous instructions (system commands, file deletion)
//   - Must not contain prompt injection patterns
//   - Must not be unreasonably large
//   - Must have valid YAML front matter if present
func validateContent(content string) error {
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("patched content is empty")
	}
	if len(content) > 100*1024 {
		return fmt.Errorf("patched content too large: %d bytes", len(content))
	}

	if err := checkDangerousInstructions(content); err != nil {
		return fmt.Errorf("dangerous content detected: %w", err)
	}

	if err := checkPromptInjection(content); err != nil {
		return fmt.Errorf("prompt injection detected: %w", err)
	}

	return nil
}

// checkDangerousInstructions detects obviously dangerous commands and patterns
// that should not appear in skill instructions.
func checkDangerousInstructions(content string) error {
	dangerousPatterns := []string{
		";rm ", "; rm ", ";rm -", "; rm -",
		"rm -rf", "rm -rf/",
		"system(", "os.system(", "exec(", "subprocess.",
		"chmod ", "chown ", "sudo ", "su -",
		"> /dev/null", "2>&1",
		"curl ", "wget ", "fetch ",
		"shutdown", "reboot", "halt",
		"format ", "mkfs.",
		":(){ :|:& };:",
		"$(rm -rf", "`rm -rf",
	}

	for _, pattern := range dangerousPatterns {
		if strings.Contains(content, pattern) {
			return fmt.Errorf("contains dangerous pattern: %q", pattern)
		}
	}

	return nil
}

// checkPromptInjection detects common prompt injection patterns that
// could manipulate the AI's behavior or escape instructions.
func checkPromptInjection(content string) error {
	injectionPatterns := []struct {
		pattern string
		reason  string
	}{
		{"ignore previous", "attempt to override prior instructions"},
		{"disregard previous", "attempt to override prior instructions"},
		{"disregard prior", "attempt to override prior instructions"},
		{"forget previous", "attempt to override prior instructions"},
		{"you are not", "role manipulation attempt"},
		{"ignore all", "attempt to override system prompt"},
		{"break out", "jailbreak attempt"},
		{"escape from", "jailbreak attempt"},
		{"i am a human", "identity deception"},
		{"please ignore", "instruction override"},
		{"### system:", "attempt to inject system prompt"},
		{"system prompt:", "attempt to inject system prompt"},
	}

	lowerContent := strings.ToLower(content)
	for _, ip := range injectionPatterns {
		if strings.Contains(lowerContent, ip.pattern) {
			return fmt.Errorf("%s: %q", ip.reason, ip.pattern)
		}
	}

	return nil
}

// cleanupOldBackups removes backup files that are no longer tracked
// in the version database (pruned versions). This keeps the skill
// directory clean.
func (p *EvolutionPatcher) cleanupOldBackups(
	skillDir string, skillName string,
) {
	versions, err := p.store.ListVersions(skillName)
	if err != nil {
		return
	}

	// Build a set of valid backup paths
	validBackups := make(map[string]bool)
	for _, v := range versions {
		validBackups[filepath.Base(v.BackupPath)] = true
	}

	// Remove backup files not in the valid set
	entries, err := os.ReadDir(skillDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "SKILL.v") &&
			strings.HasSuffix(name, ".md") &&
			!validBackups[name] {
			fullPath := filepath.Join(skillDir, name)
			if err := os.Remove(fullPath); err != nil {
				util.Logger.Warn(
					"evolution: failed to remove old backup",
					"file", fullPath,
					"error", err.Error(),
				)
			}
		}
	}
}
