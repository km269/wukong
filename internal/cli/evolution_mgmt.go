// Package cli provides the "wukong evolution" command for
// evolution engine management.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/evolution"
	"github.com/km269/wukong/internal/util"
)

// newEvolutionCmd creates the "wukong evolution" command group.
func newEvolutionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "evolution",
		Short: "Manage the skill evolution engine",
		Long: `View status, history, and versions of the skill self-evolution
engine that analyzes execution traces and patches skills.

Subcommands:
  status    Show evolution engine configuration and status
  history   View evolution analysis history for a skill
  versions  List version history for a skill
  rollback  Roll back a skill to a previous version
  diff      Show differences between skill versions
  log       View evolution log entries for a skill`,
	}

	cmd.AddCommand(newEvolutionStatusCmd())
	cmd.AddCommand(newEvolutionHistoryCmd())
	cmd.AddCommand(newEvolutionVersionsCmd())
	cmd.AddCommand(newEvolutionRollbackCmd())
	cmd.AddCommand(newEvolutionDiffCmd())
	cmd.AddCommand(newEvolutionLogCmd())

	return cmd
}

func newEvolutionStatusCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show evolution engine status",
		Long: `Display the current evolution engine configuration and
operational parameters.

Examples:
  wukong evolution status`,
		RunE: runEvolutionStatus,
	}

	cmd.Flags().StringVarP(
		&configPath, "config", "c", "",
		"Path to config file")

	return cmd
}

func runEvolutionStatus(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")

	loader, err := config.NewLoader(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	ec := &wukongCfg.Evolution

	fmt.Println(strings.Repeat("─", 55))
	fmt.Println("  Evolution Engine Status")
	fmt.Println(strings.Repeat("─", 55))

	if !ec.Enabled {
		fmt.Println("\n  Status: disabled (experimental)")
		fmt.Println("\n  Enable with:")
		fmt.Println("    evolution:")
		fmt.Println("      enabled: true")
		fmt.Println("      analysis_provider: lmstudio")
		fmt.Println("      min_confidence: 0.7")
		return nil
	}

	fmt.Println("\n  Status: enabled")

	fmt.Println("\n  [Analysis]")
	fmt.Printf("  Auto Patch:        %v\n", ec.AutoPatch)
	fmt.Printf("  Min Confidence:    %.1f\n", ec.MinConfidence)
	fmt.Printf("  Analysis Timeout:  %s\n", ec.AnalysisTimeout)

	if ec.AnalysisProvider != "" {
		fmt.Printf("  Analysis Provider: %s\n", ec.AnalysisProvider)
	}
	if ec.AnalysisModel != "" {
		fmt.Printf("  Analysis Model:    %s\n", ec.AnalysisModel)
	}

	fmt.Println("\n  [Rate Limiting]")
	fmt.Printf("  Cooldown Period:   %s\n", ec.CooldownPeriod)
	fmt.Printf("  Max Patches/Day:    %d\n", ec.MaxPatchesPerDay)

	fmt.Println("\n  [Version Control]")
	fmt.Printf("  Max Versions Kept: %d\n", ec.MaxVersionsKept)
	fmt.Printf("  Max Patch Size:    %d chars\n", ec.MaxPatchSize)

	fmt.Println("\n  [Integration]")
	fmt.Printf("  Export JSON:       %v\n", ec.ExportJSON)

	fmt.Println("\n  [Problem Types Detected]")
	fmt.Println("  missing_prerequisite     — Skill lacks a necessary step")
	fmt.Println("  outdated_instruction     — References deprecated APIs")
	fmt.Println("  parameter_error          — Default parameters are wrong")
	fmt.Println("  ambiguous_wording        — Unclear instructions")
	fmt.Println("  missing_error_handling   — No failure handling guidance")

	fmt.Println()
	return nil
}

func newEvolutionHistoryCmd() *cobra.Command {
	var configPath string
	var limit int

	cmd := &cobra.Command{
		Use:   "history <skill-name>",
		Short: "View evolution analysis history",
		Long: `Display recent evolution analysis records for a skill,
showing issues detected and patches applied.

Examples:
  wukong evolution history code-reviewer
  wukong evolution history my-skill --limit 10`,
		Args: cobra.ExactArgs(1),
		RunE: runEvolutionHistory,
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to config file")
	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "Max number of records to show")

	return cmd
}

func runEvolutionHistory(cmd *cobra.Command, args []string) error {
	skillName := args[0]
	configPath, _ := cmd.Flags().GetString("config")
	limit, _ := cmd.Flags().GetInt("limit")

	loader, err := config.NewLoader(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	dbPool := util.NewDatabasePool(config.ResolvePath(wukongCfg.Session.DBPath))
	store, err := evolution.NewVersionStore(dbPool)
	if err != nil {
		return fmt.Errorf("create version store: %w", err)
	}

	records, err := store.ListRecentRecords(skillName, limit)
	if err != nil {
		return fmt.Errorf("list records: %w", err)
	}

	if len(records) == 0 {
		fmt.Printf("No evolution history found for skill %q\n", skillName)
		return nil
	}

	fmt.Printf("Evolution History for %q\n", skillName)
	fmt.Println(strings.Repeat("─", 70))

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIME\tISSUE\tPATCH\tCONFIDENCE\tREASON")
	fmt.Fprintln(w, "----\t-----\t-----t----------\t------")

	for _, rec := range records {
		issueMark := "✗"
		if rec.HasIssue {
			issueMark = "✓"
		}
		patchMark := "-"
		if rec.PatchApplied {
			patchMark = "✓"
		}
		confidence := "-"
		if rec.PatchConfidence > 0 {
			confidence = fmt.Sprintf("%.2f", rec.PatchConfidence)
		}
		reason := rec.PatchReason
		if len(reason) > 40 {
			reason = reason[:37] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			rec.CreatedAt.Format("2006-01-02 15:04"),
			issueMark, patchMark, confidence, reason,
		)
	}
	w.Flush()

	return nil
}

func newEvolutionVersionsCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "versions <skill-name>",
		Short: "List skill versions",
		Long: `Display all saved versions for a skill, showing the version
number, creation time, and reason for the change.

Examples:
  wukong evolution versions code-reviewer`,
		Args: cobra.ExactArgs(1),
		RunE: runEvolutionVersions,
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to config file")

	return cmd
}

func runEvolutionVersions(cmd *cobra.Command, args []string) error {
	skillName := args[0]
	configPath, _ := cmd.Flags().GetString("config")

	loader, err := config.NewLoader(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	dbPool := util.NewDatabasePool(config.ResolvePath(wukongCfg.Session.DBPath))
	store, err := evolution.NewVersionStore(dbPool)
	if err != nil {
		return fmt.Errorf("create version store: %w", err)
	}

	versions, err := store.ListVersions(skillName)
	if err != nil {
		return fmt.Errorf("list versions: %w", err)
	}

	if len(versions) == 0 {
		fmt.Printf("No versions found for skill %q\n", skillName)
		return nil
	}

	currentVer, err := store.GetCurrentVersion(skillName)
	if err != nil {
		currentVer = 0
	}

	fmt.Printf("Versions for %q\n", skillName)
	fmt.Println(strings.Repeat("─", 70))

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "VERSION\tCREATED\tREASON")
	fmt.Fprintln(w, "-------\t-------\t------")

	for _, v := range versions {
		marker := ""
		if v.VersionNumber == currentVer {
			marker = "*"
		}
		reason := v.PatchReason
		if reason == "" {
			reason = "(manual)"
		}
		if len(reason) > 45 {
			reason = reason[:42] + "..."
		}
		fmt.Fprintf(w, "%d%s\t%s\t%s\n",
			v.VersionNumber, marker,
			v.CreatedAt.Format("2006-01-02 15:04"),
			reason,
		)
	}
	w.Flush()

	fmt.Printf("\n* indicates current version\n")

	return nil
}

func newEvolutionRollbackCmd() *cobra.Command {
	var configPath string
	var force bool

	cmd := &cobra.Command{
		Use:   "rollback <skill-name> <version>",
		Short: "Roll back to a previous version",
		Long: `Restore a skill to a previous version from the backup.
This replaces the current SKILL.md with the backup content.

Examples:
  wukong evolution rollback code-reviewer 3
  wukong evolution rollback my-skill 1 --force`,
		Args: cobra.ExactArgs(2),
		RunE: runEvolutionRollback,
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to config file")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Skip confirmation prompt")

	return cmd
}

func runEvolutionRollback(cmd *cobra.Command, args []string) error {
	skillName := args[0]
	versionStr := args[1]
	configPath, _ := cmd.Flags().GetString("config")
	force, _ := cmd.Flags().GetBool("force")

	var version int
	fmt.Sscanf(versionStr, "%d", &version)
	if version <= 0 {
		return fmt.Errorf("invalid version number: %s", versionStr)
	}

	loader, err := config.NewLoader(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	dbPool := util.NewDatabasePool(config.ResolvePath(wukongCfg.Session.DBPath))
	store, err := evolution.NewVersionStore(dbPool)
	if err != nil {
		return fmt.Errorf("create version store: %w", err)
	}

	currentVersion, err := store.GetCurrentVersion(skillName)
	if err != nil {
		return fmt.Errorf("get current version: %w", err)
	}

	targetVer, err := store.GetVersion(skillName, version)
	if err != nil {
		return fmt.Errorf("get version %d: %w", version, err)
	}

	if !force {
		fmt.Printf("Are you sure you want to roll back %q from v%d to v%d?\n", skillName, currentVersion, version)
		fmt.Printf("Created: %s\n", targetVer.CreatedAt.Format(time.RFC1123))
		fmt.Printf("Reason: %s\n", targetVer.PatchReason)
		fmt.Print("Type 'yes' to confirm: ")
		var confirm string
		fmt.Scanln(&confirm)
		if confirm != "yes" {
			fmt.Println("Rollback cancelled")
			return nil
		}
	}

	if targetVer.BackupPath == "" {
		return fmt.Errorf("no backup file found for version %d", version)
	}

	backupContent, err := os.ReadFile(targetVer.BackupPath)
	if err != nil {
		return fmt.Errorf("read backup file: %w", err)
	}

	skillDir := wukongCfg.Skill.SkillsDir
	if skillDir == "" {
		return fmt.Errorf("skill directory not configured")
	}

	skillFilePath := fmt.Sprintf("%s/%s/SKILL.md", skillDir, skillName)
	err = os.WriteFile(skillFilePath, backupContent, 0644)
	if err != nil {
		return fmt.Errorf("write skill file: %w", err)
	}

	exportJSON := wukongCfg.Evolution.ExportJSON
	patcher := evolution.NewEvolutionPatcher(store, wukongCfg.Evolution.MaxVersionsKept, exportJSON)
	if err := patcher.RecordRollback(skillDir, skillName, currentVersion, version); err != nil {
		util.Logger.Warn("evolution: failed to record rollback",
			"skill", skillName,
			"error", err.Error())
	}

	fmt.Printf("Successfully rolled back %q from v%d to v%d\n", skillName, currentVersion, version)

	return nil
}

func newEvolutionDiffCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "diff <skill-name> <version1> <version2>",
		Short: "Show differences between versions",
		Long: `Display the differences between two versions of a skill.
If version2 is not specified, compares with the current version.

Examples:
  wukong evolution diff code-reviewer 1 2
  wukong evolution diff my-skill 2`,
		Args: cobra.RangeArgs(2, 3),
		RunE: runEvolutionDiff,
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to config file")

	return cmd
}

func runEvolutionDiff(cmd *cobra.Command, args []string) error {
	skillName := args[0]
	version1Str := args[1]
	var version2Str string
	if len(args) > 2 {
		version2Str = args[2]
	}
	configPath, _ := cmd.Flags().GetString("config")

	var version1, version2 int
	fmt.Sscanf(version1Str, "%d", &version1)
	if version1 <= 0 {
		return fmt.Errorf("invalid version number: %s", version1Str)
	}

	loader, err := config.NewLoader(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	dbPool := util.NewDatabasePool(config.ResolvePath(wukongCfg.Session.DBPath))
	store, err := evolution.NewVersionStore(dbPool)
	if err != nil {
		return fmt.Errorf("create version store: %w", err)
	}

	if version2Str == "" {
		currentVer, err := store.GetCurrentVersion(skillName)
		if err != nil {
			return fmt.Errorf("get current version: %w", err)
		}
		version2 = currentVer
	} else {
		fmt.Sscanf(version2Str, "%d", &version2)
		if version2 <= 0 {
			return fmt.Errorf("invalid version number: %s", version2Str)
		}
	}

	if version1 == version2 {
		fmt.Printf("Versions %d and %d are the same\n", version1, version2)
		return nil
	}

	v1, err := store.GetVersion(skillName, version1)
	if err != nil {
		return fmt.Errorf("get version %d: %w", version1, err)
	}

	v2, err := store.GetVersion(skillName, version2)
	if err != nil {
		return fmt.Errorf("get version %d: %w", version2, err)
	}

	if v1.BackupPath == "" || v2.BackupPath == "" {
		return fmt.Errorf("backup files not found for one or both versions")
	}

	content1, err := os.ReadFile(v1.BackupPath)
	if err != nil {
		return fmt.Errorf("read version %d: %w", version1, err)
	}

	content2, err := os.ReadFile(v2.BackupPath)
	if err != nil {
		return fmt.Errorf("read version %d: %w", version2, err)
	}

	fmt.Printf("Diff: %q version %d → %d\n", skillName, version1, version2)
	fmt.Println(strings.Repeat("─", 70))
	fmt.Println()

	lines1 := strings.Split(string(content1), "\n")
	lines2 := strings.Split(string(content2), "\n")

	maxLines := len(lines1)
	if len(lines2) > maxLines {
		maxLines = len(lines2)
	}

	for i := 0; i < maxLines; i++ {
		var line1, line2 string
		if i < len(lines1) {
			line1 = lines1[i]
		}
		if i < len(lines2) {
			line2 = lines2[i]
		}

		if line1 == line2 {
			fmt.Printf(" %5d: %s\n", i+1, line1)
		} else {
			if line1 != "" {
				fmt.Printf("-%5d: %s\n", i+1, line1)
			}
			if line2 != "" {
				fmt.Printf("+%5d: %s\n", i+1, line2)
			}
		}
	}

	return nil
}

func newEvolutionLogCmd() *cobra.Command {
	var configPath string
	var limit int

	cmd := &cobra.Command{
		Use:   "log <skill-name>",
		Short: "View evolution log entries",
		Long: `Display evolution log entries from log.json for a skill,
showing version changes, problems detected, and rollbacks.

Examples:
  wukong evolution log code-reviewer
  wukong evolution log my-skill --limit 10`,
		Args: cobra.ExactArgs(1),
		RunE: runEvolutionLog,
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to config file")
	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "Max number of entries to show")

	return cmd
}

func runEvolutionLog(cmd *cobra.Command, args []string) error {
	skillName := args[0]
	configPath, _ := cmd.Flags().GetString("config")
	limit, _ := cmd.Flags().GetInt("limit")

	loader, err := config.NewLoader(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	skillDir := wukongCfg.Skill.SkillsDir
	if skillDir == "" {
		skillDir = ".wukong/skills"
	}

	logPath := filepath.Join(skillDir, skillName, "log.json")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		fmt.Printf("No evolution log found for skill %q (log.json not found)\n", skillName)
		return nil
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		return fmt.Errorf("read log.json: %w", err)
	}

	type LogEntry struct {
		Version    int       `json:"version"`
		Timestamp  time.Time `json:"timestamp"`
		Type       string    `json:"type"`
		Reason     string    `json:"reason"`
		Confidence float64   `json:"confidence"`
		SkillName  string    `json:"skill_name"`
		PatchHash  string    `json:"patch_hash,omitempty"`
	}

	type LogData struct {
		SkillName string     `json:"skill_name"`
		Entries   []LogEntry `json:"entries"`
	}

	var logData LogData
	if err := json.Unmarshal(data, &logData); err != nil {
		return fmt.Errorf("unmarshal log.json: %w", err)
	}

	if len(logData.Entries) == 0 {
		fmt.Printf("No entries found in evolution log for skill %q\n", skillName)
		return nil
	}

	if limit > len(logData.Entries) {
		limit = len(logData.Entries)
	}

	fmt.Printf("Evolution Log for %q\n", skillName)
	fmt.Println(strings.Repeat("─", 75))

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIME\tVERSION\tTYPE\tCONFIDENCE\tREASON")
	fmt.Fprintln(w, "----\t-------\t----\t----------\t------")

	for i := 0; i < limit; i++ {
		entry := logData.Entries[i]
		typeMark := entry.Type
		if entry.Type == "rollback" {
			typeMark = "ROLLBACK"
		}
		confidence := "-"
		if entry.Confidence > 0 {
			confidence = fmt.Sprintf("%.2f", entry.Confidence)
		}
		reason := entry.Reason
		if len(reason) > 50 {
			reason = reason[:47] + "..."
		}
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\n",
			entry.Timestamp.Format("2006-01-02 15:04"),
			entry.Version, typeMark, confidence, reason,
		)
	}
	w.Flush()

	return nil
}
