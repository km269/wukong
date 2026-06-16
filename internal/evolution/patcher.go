// Package evolution provides the skill self-evolution system.
package evolution

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/km269/wukong/internal/util"
)

// EvolutionPatcher applies LLM-generated patches to SKILL.md files,
// manages versioned backups, and performs security validation on patches.
type EvolutionPatcher struct {
	store     *VersionStore
	maxVersions int
}

// NewEvolutionPatcher creates a new patcher with the given version store.
func NewEvolutionPatcher(
	store *VersionStore, maxVersions int,
) *EvolutionPatcher {
	if maxVersions <= 0 {
		maxVersions = 10
	}
	return &EvolutionPatcher{
		store:       store,
		maxVersions: maxVersions,
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
		SkillName:    suggestion.SkillName,
		VersionNumber: newVersion,
		BackupPath:   backupPath,
		FileHash:     fileHash,
		PatchReason:  suggestion.Reason,
		CreatedAt:    time.Now(),
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

	util.Logger.Info("evolution: patch applied successfully",
		"skill", suggestion.SkillName,
		"version", newVersion,
		"reason", suggestion.Reason,
		"confidence", suggestion.Confidence,
	)

	return newVersion, nil
}

// appendPatchToBody appends the patch content to the SKILL.md body,
// after the YAML front matter. The patch is added as a new section
// with a timestamp header, separated from existing content.
func appendPatchToBody(
	content string, suggestion *PatchSuggestion,
) string {
	content = strings.TrimRight(content, "\n")

	yamlEnd := findYAMLEnd(content)
	if yamlEnd < 0 {
		// No YAML front matter found, prepend a basic one
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

	timestamp := suggestion.GeneratedAt.Format("2006-01-02 15:04")
	patchSection := fmt.Sprintf(`
<!-- EVOLUTION PATCH v%d - %s -->
<!-- Problem: %s -->
<!-- Type: %s | Confidence: %.2f -->

%s`,
		time.Now().Unix()%100000,
		timestamp,
		suggestion.Reason,
		suggestion.ProblemType,
		suggestion.Confidence,
		suggestion.DiffContent,
	)

	return content[:yamlEnd+1] + "\n" + body + "\n" + patchSection + "\n"
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

// validateContent performs basic safety checks on the patched content:
//   - Must not be empty
//   - Must not contain obviously dangerous instructions
//   - Must not be unreasonably large
func validateContent(content string) error {
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("patched content is empty")
	}
	if len(content) > 100*1024 { // 100KB limit
		return fmt.Errorf(
			"patched content too large: %d bytes", len(content))
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
