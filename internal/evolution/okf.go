// Package evolution OKF log.md change tracking.
//
// This file extends the Evolution system to track knowledge file
// changes in OKF Bundles via the log.md convention.
package evolution

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/km269/wukong/internal/okf"
	"github.com/km269/wukong/internal/util"
)

// ChangeAction represents the type of change made to a
// knowledge file in an OKF Bundle.
type ChangeAction string

const (
	ChangeAdded    ChangeAction = "Added"
	ChangeModified ChangeAction = "Modified"
	ChangeRemoved  ChangeAction = "Removed"
	ChangePatched  ChangeAction = "Patched"
)

// KnowledgeChange represents a single change entry to be
// recorded in an OKF Bundle's log.md.
type KnowledgeChange struct {
	Action    ChangeAction
	FilePath  string
	Reason    string
	Timestamp string
}

// RecordKnowledgeChange appends a change entry to the OKF
// Bundle's log.md file. If log.md doesn't exist, it is created.
func RecordKnowledgeChange(
	bundleDir string,
	change KnowledgeChange,
) error {
	if change.Timestamp == "" {
		change.Timestamp = okf.FormatNow()
	}

	if err := okf.AppendLogEntry(
		bundleDir,
		string(change.Action),
		change.FilePath,
		change.Reason,
	); err != nil {
		return fmt.Errorf("append log entry: %w", err)
	}

	util.Logger.Debug("evolution: recorded knowledge change",
		"action", change.Action,
		"file", change.FilePath,
		"bundle", bundleDir)

	return nil
}

// RecordSkillPatchAsKnowledge records a skill patch in both
// the Evolution version store and the OKF Bundle's log.md.
func RecordSkillPatchAsKnowledge(
	bundleDir, skillName, reason string,
	versionFrom, versionTo int,
) error {
	filePath := filepath.Join("skills", skillName, "SKILL.md")
	changeReason := fmt.Sprintf(
		"patched v%d->v%d: %s",
		versionFrom, versionTo, reason)

	return RecordKnowledgeChange(bundleDir, KnowledgeChange{
		Action:   ChangePatched,
		FilePath: filePath,
		Reason:   changeReason,
	})
}

// GetChangeHistory reads the change history from an OKF Bundle's
// log.md file.
func GetChangeHistory(bundleDir string) ([]KnowledgeChange, error) {
	logPath := filepath.Join(bundleDir, okf.LogFile)

	content, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read log: %w", err)
	}

	return parseLogEntries(string(content)), nil
}

// parseLogEntries parses a log.md file content into entries.
func parseLogEntries(content string) []KnowledgeChange {
	var changes []KnowledgeChange
	var currentDate string

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "## ") {
			currentDate = strings.TrimPrefix(trimmed, "## ")
			continue
		}

		if !strings.HasPrefix(trimmed, "- ") {
			continue
		}

		entry := strings.TrimPrefix(trimmed, "- ")
		change := parseChangeEntry(entry, currentDate)
		if change.Action != "" {
			changes = append(changes, change)
		}
	}

	return changes
}

// parseChangeEntry parses a single log entry line.
func parseChangeEntry(entry, date string) KnowledgeChange {
	var change KnowledgeChange
	change.Timestamp = date

	idx := strings.Index(entry, ": ")
	if idx < 0 {
		return change
	}

	actionStr := entry[:idx]
	rest := entry[idx+2:]
	change.Action = ChangeAction(actionStr)

	if parenIdx := strings.LastIndex(rest, " ("); parenIdx >= 0 {
		change.FilePath = rest[:parenIdx]
		reason := rest[parenIdx+2:]
		change.Reason = strings.TrimSuffix(reason, ")")
	} else {
		change.FilePath = rest
	}

	return change
}

// GetRecentChanges returns changes from the last N days.
func GetRecentChanges(
	bundleDir string, days int,
) ([]KnowledgeChange, error) {
	all, err := GetChangeHistory(bundleDir)
	if err != nil {
		return nil, err
	}

	cutoff := time.Now().AddDate(0, 0, -days)
	var recent []KnowledgeChange

	for _, c := range all {
		t, err := time.Parse("2006-01-02", c.Timestamp)
		if err != nil {
			continue
		}
		if t.After(cutoff) || t.Equal(cutoff) {
			recent = append(recent, c)
		}
	}

	return recent, nil
}
