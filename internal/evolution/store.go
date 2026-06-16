// Package evolution provides the skill self-evolution system.
package evolution

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/km269/wukong/internal/util"
)

// VersionStore manages skill version history and evolution records
// using the shared SQLite database pool.
type VersionStore struct {
	dbPool *util.DatabasePool
}

// NewVersionStore creates a new version store backed by the shared
// database pool. The pool must already be initialized (connected).
func NewVersionStore(dbPool *util.DatabasePool) (*VersionStore, error) {
	vs := &VersionStore{dbPool: dbPool}
	if err := vs.initSchema(); err != nil {
		return nil, fmt.Errorf("init evolution schema: %w", err)
	}
	return vs, nil
}

// initSchema creates the evolution tables if they don't exist.
func (vs *VersionStore) initSchema() error {
	db, err := vs.dbPool.GetDB()
	if err != nil {
		return fmt.Errorf("get db: %w", err)
	}

	schema := `
	CREATE TABLE IF NOT EXISTS evolution_history (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		skill_name    TEXT NOT NULL,
		session_id    TEXT NOT NULL DEFAULT '',
		trace_json    TEXT NOT NULL DEFAULT '{}',
		has_issue     INTEGER NOT NULL DEFAULT 0,
		patch_applied INTEGER NOT NULL DEFAULT 0,
		patch_reason  TEXT NOT NULL DEFAULT '',
		patch_confidence REAL NOT NULL DEFAULT 0.0,
		version_before INTEGER NOT NULL DEFAULT 0,
		version_after  INTEGER NOT NULL DEFAULT 0,
		created_at    TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE INDEX IF NOT EXISTS idx_evolution_history_skill
		ON evolution_history(skill_name, created_at);

	CREATE TABLE IF NOT EXISTS evolution_versions (
		id             INTEGER PRIMARY KEY AUTOINCREMENT,
		skill_name     TEXT NOT NULL,
		version_number INTEGER NOT NULL,
		backup_path    TEXT NOT NULL DEFAULT '',
		file_hash      TEXT NOT NULL DEFAULT '',
		patch_reason   TEXT NOT NULL DEFAULT '',
		created_at     TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE INDEX IF NOT EXISTS idx_evolution_versions_skill
		ON evolution_versions(skill_name, version_number);
	`

	_, err = db.Exec(schema)
	if err != nil {
		return fmt.Errorf("create evolution tables: %w", err)
	}
	return nil
}

// RecordEvolution inserts a new evolution analysis record
// into evolution_history.
func (vs *VersionStore) RecordEvolution(rec *EvolutionRecord) error {
	db, err := vs.dbPool.GetDB()
	if err != nil {
		return fmt.Errorf("get db: %w", err)
	}

	hasIssue := 0
	if rec.HasIssue {
		hasIssue = 1
	}
	patchApplied := 0
	if rec.PatchApplied {
		patchApplied = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)

	_, err = db.Exec(
		`INSERT INTO evolution_history
			(skill_name, session_id, trace_json, has_issue,
			 patch_applied, patch_reason, patch_confidence,
			 version_before, version_after, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.SkillName, rec.SessionID, rec.TraceJSON,
		hasIssue, patchApplied, rec.PatchReason,
		rec.PatchConfidence, rec.VersionBefore,
		rec.VersionAfter, now,
	)
	if err != nil {
		return fmt.Errorf("insert evolution record: %w", err)
	}

	return nil
}

// CreateVersion inserts a new version snapshot record.
func (vs *VersionStore) CreateVersion(ver *SkillVersion) error {
	db, err := vs.dbPool.GetDB()
	if err != nil {
		return fmt.Errorf("get db: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	result, err := db.Exec(
		`INSERT INTO evolution_versions
			(skill_name, version_number, backup_path,
			 file_hash, patch_reason, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		ver.SkillName, ver.VersionNumber, ver.BackupPath,
		ver.FileHash, ver.PatchReason, now,
	)
	if err != nil {
		return fmt.Errorf("insert version: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get last insert id: %w", err)
	}
	ver.ID = id
	ver.CreatedAt, _ = time.Parse(time.RFC3339, now)
	return nil
}

// GetCurrentVersion returns the highest version number for a skill,
// or 0 if no versions exist yet.
func (vs *VersionStore) GetCurrentVersion(skillName string) (int, error) {
	db, err := vs.dbPool.GetDB()
	if err != nil {
		return 0, fmt.Errorf("get db: %w", err)
	}

	var version int
	err = db.QueryRow(
		`SELECT COALESCE(MAX(version_number), 0)
		 FROM evolution_versions WHERE skill_name = ?`,
		skillName,
	).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("get current version: %w", err)
	}
	return version, nil
}

// ListVersions returns all version records for a skill,
// ordered by version number descending (newest first).
func (vs *VersionStore) ListVersions(
	skillName string,
) ([]SkillVersion, error) {
	db, err := vs.dbPool.GetDB()
	if err != nil {
		return nil, fmt.Errorf("get db: %w", err)
	}

	rows, err := db.Query(
		`SELECT id, skill_name, version_number, backup_path,
		        file_hash, patch_reason, created_at
		 FROM evolution_versions
		 WHERE skill_name = ?
		 ORDER BY version_number DESC`,
		skillName,
	)
	if err != nil {
		return nil, fmt.Errorf("list versions: %w", err)
	}
	defer rows.Close()

	var versions []SkillVersion
	for rows.Next() {
		var v SkillVersion
		var createdAt string
		if err := rows.Scan(
			&v.ID, &v.SkillName, &v.VersionNumber,
			&v.BackupPath, &v.FileHash,
			&v.PatchReason, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan version: %w", err)
		}
		v.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		versions = append(versions, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}
	return versions, nil
}

// GetVersion retrieves a specific version by skill name and version number.
func (vs *VersionStore) GetVersion(
	skillName string, versionNumber int,
) (*SkillVersion, error) {
	db, err := vs.dbPool.GetDB()
	if err != nil {
		return nil, fmt.Errorf("get db: %w", err)
	}

	var v SkillVersion
	var createdAt string
	err = db.QueryRow(
		`SELECT id, skill_name, version_number, backup_path,
		        file_hash, patch_reason, created_at
		 FROM evolution_versions
		 WHERE skill_name = ? AND version_number = ?`,
		skillName, versionNumber,
	).Scan(
		&v.ID, &v.SkillName, &v.VersionNumber,
		&v.BackupPath, &v.FileHash,
		&v.PatchReason, &createdAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf(
			"version %d not found for skill %q",
			versionNumber, skillName,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("get version: %w", err)
	}
	v.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &v, nil
}

// PruneOldVersions removes versions beyond maxVersions for a skill,
// keeping only the most recent ones. Returns the number of deleted rows.
func (vs *VersionStore) PruneOldVersions(
	skillName string, maxVersions int,
) (int, error) {
	if maxVersions <= 0 {
		return 0, nil
	}

	db, err := vs.dbPool.GetDB()
	if err != nil {
		return 0, fmt.Errorf("get db: %w", err)
	}

	result, err := db.Exec(
		`DELETE FROM evolution_versions
		 WHERE skill_name = ? AND id NOT IN (
		     SELECT id FROM evolution_versions
		     WHERE skill_name = ?
		     ORDER BY version_number DESC
		     LIMIT ?
		 )`,
		skillName, skillName, maxVersions,
	)
	if err != nil {
		return 0, fmt.Errorf("prune versions: %w", err)
	}

	deleted, _ := result.RowsAffected()
	return int(deleted), nil
}

// CountPatchesToday returns the number of patches applied to a skill
// within the last 24 hours.
func (vs *VersionStore) CountPatchesToday(
	skillName string,
) (int, error) {
	db, err := vs.dbPool.GetDB()
	if err != nil {
		return 0, fmt.Errorf("get db: %w", err)
	}

	var count int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM evolution_history
		 WHERE skill_name = ?
		   AND patch_applied = 1
		   AND created_at > datetime('now', '-1 day')`,
		skillName,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count patches today: %w", err)
	}
	return count, nil
}

// GetLastPatchTime returns the timestamp of the last applied patch
// for a skill, or the zero time if no patches have been applied.
func (vs *VersionStore) GetLastPatchTime(
	skillName string,
) (time.Time, error) {
	db, err := vs.dbPool.GetDB()
	if err != nil {
		return time.Time{}, fmt.Errorf("get db: %w", err)
	}

	var createdAt string
	err = db.QueryRow(
		`SELECT created_at FROM evolution_history
		 WHERE skill_name = ? AND patch_applied = 1
		 ORDER BY created_at DESC LIMIT 1`,
		skillName,
	).Scan(&createdAt)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("get last patch time: %w", err)
	}

	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return time.Time{}, fmt.Errorf(
			"parse patch time: %w", err)
	}
	return t, nil
}

// ListRecentRecords returns recent evolution records for a skill.
func (vs *VersionStore) ListRecentRecords(
	skillName string, limit int,
) ([]EvolutionRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	db, err := vs.dbPool.GetDB()
	if err != nil {
		return nil, fmt.Errorf("get db: %w", err)
	}

	rows, err := db.Query(
		`SELECT id, skill_name, session_id, trace_json,
		        has_issue, patch_applied, patch_reason,
		        patch_confidence, version_before,
		        version_after, created_at
		 FROM evolution_history
		 WHERE skill_name = ?
		 ORDER BY created_at DESC
		 LIMIT ?`,
		skillName, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list records: %w", err)
	}
	defer rows.Close()

	var records []EvolutionRecord
	for rows.Next() {
		var r EvolutionRecord
		var hasIssue, patchApplied int
		var createdAt string
		if err := rows.Scan(
			&r.ID, &r.SkillName, &r.SessionID,
			&r.TraceJSON, &hasIssue, &patchApplied,
			&r.PatchReason, &r.PatchConfidence,
			&r.VersionBefore, &r.VersionAfter,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan record: %w", err)
		}
		r.HasIssue = hasIssue != 0
		r.PatchApplied = patchApplied != 0
		r.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}
	return records, nil
}
