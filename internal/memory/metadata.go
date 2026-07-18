package memory

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

const (
	ImportanceLow    = "low"
	ImportanceMedium = "medium"
	ImportanceHigh   = "high"
)

type MemoryMetadata struct {
	MemoryID       string    `json:"memory_id"`
	UserID         string    `json:"user_id"`
	ReferenceCount int       `json:"reference_count"`
	Importance     string    `json:"importance"`
	LastReferenced time.Time `json:"last_referenced_at"`
	CreatedAt      time.Time `json:"created_at"`
}

type MetadataManager struct {
	db *sql.DB
}

func NewMetadataManager(db *sql.DB) *MetadataManager {
	mm := &MetadataManager{db: db}
	mm.initSchema()
	return mm
}

func (m *MetadataManager) initSchema() {
	_, err := m.db.Exec(`
		CREATE TABLE IF NOT EXISTS memory_metadata (
			memory_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			reference_count INTEGER DEFAULT 0,
			importance TEXT DEFAULT 'low',
			last_referenced_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (memory_id, user_id)
		);
		CREATE INDEX IF NOT EXISTS idx_metadata_user
			ON memory_metadata(user_id);
		CREATE INDEX IF NOT EXISTS idx_metadata_ref_count
			ON memory_metadata(reference_count DESC);
		CREATE INDEX IF NOT EXISTS idx_metadata_importance
			ON memory_metadata(importance);
	`)
	if err != nil {
		util.Logger.Warn("memory: init metadata schema failed",
			slog.String("error", err.Error()))
	}
}

func (m *MetadataManager) RecordReference(
	ctx context.Context,
	memoryID string,
	userID string,
) error {
	_, err := m.db.Exec(`
		INSERT OR REPLACE INTO memory_metadata
			(memory_id, user_id, reference_count, last_referenced_at, created_at)
		VALUES (?, ?, 
			COALESCE((SELECT reference_count FROM memory_metadata 
				WHERE memory_id = ? AND user_id = ?), 0) + 1,
			?,
			COALESCE((SELECT created_at FROM memory_metadata 
				WHERE memory_id = ? AND user_id = ?), CURRENT_TIMESTAMP)
		)`,
		memoryID, userID,
		memoryID, userID,
		time.Now(),
		memoryID, userID,
	)
	if err != nil {
		util.Logger.Warn("memory: record reference failed",
			slog.String("memory_id", memoryID),
			slog.String("error", err.Error()))
	}
	return err
}

func (m *MetadataManager) BatchRecordReferences(
	ctx context.Context,
	memoryIDs []string,
	userID string,
) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for _, memoryID := range memoryIDs {
		_, err := tx.Exec(`
			INSERT OR REPLACE INTO memory_metadata
				(memory_id, user_id, reference_count, last_referenced_at, created_at)
			VALUES (?, ?, 
				COALESCE((SELECT reference_count FROM memory_metadata 
					WHERE memory_id = ? AND user_id = ?), 0) + 1,
				?,
				COALESCE((SELECT created_at FROM memory_metadata 
					WHERE memory_id = ? AND user_id = ?), CURRENT_TIMESTAMP)
			)`,
			memoryID, userID,
			memoryID, userID,
			time.Now(),
			memoryID, userID,
		)
		if err != nil {
			return fmt.Errorf("record reference for %s: %w", memoryID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func (m *MetadataManager) MarkImportance(
	ctx context.Context,
	memoryID string,
	userID string,
	importance string,
) error {
	if importance != ImportanceLow &&
		importance != ImportanceMedium &&
		importance != ImportanceHigh {
		return fmt.Errorf("invalid importance level: %s", importance)
	}

	_, err := m.db.Exec(`
		INSERT OR REPLACE INTO memory_metadata
			(memory_id, user_id, importance, created_at)
		VALUES (?, ?, ?, 
			COALESCE((SELECT created_at FROM memory_metadata 
				WHERE memory_id = ? AND user_id = ?), CURRENT_TIMESTAMP)
		)`,
		memoryID, userID, importance,
		memoryID, userID,
	)
	if err != nil {
		util.Logger.Warn("memory: mark importance failed",
			slog.String("memory_id", memoryID),
			slog.String("importance", importance),
			slog.String("error", err.Error()))
	}
	return err
}

func (m *MetadataManager) GetMetadata(
	ctx context.Context,
	memoryID string,
	userID string,
) (*MemoryMetadata, error) {
	var md MemoryMetadata
	err := m.db.QueryRow(`
		SELECT memory_id, user_id, reference_count, importance,
			last_referenced_at, created_at
		FROM memory_metadata
		WHERE memory_id = ? AND user_id = ?`,
		memoryID, userID,
	).Scan(
		&md.MemoryID,
		&md.UserID,
		&md.ReferenceCount,
		&md.Importance,
		&md.LastReferenced,
		&md.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return &MemoryMetadata{
			MemoryID:       memoryID,
			UserID:         userID,
			ReferenceCount: 0,
			Importance:     ImportanceLow,
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get metadata: %w", err)
	}
	return &md, nil
}

func (m *MetadataManager) BatchGetMetadata(
	ctx context.Context,
	memoryIDs []string,
	userID string,
) (map[string]*MemoryMetadata, error) {
	result := make(map[string]*MemoryMetadata)

	if len(memoryIDs) == 0 {
		return result, nil
	}

	placeholders := make([]string, len(memoryIDs))
	for i := range memoryIDs {
		placeholders[i] = "?"
	}

	args := make([]interface{}, 0, len(memoryIDs)+1)
	args = append(args, userID)
	for _, id := range memoryIDs {
		args = append(args, id)
	}

	query := fmt.Sprintf(`
		SELECT memory_id, user_id, reference_count, importance,
			last_referenced_at, created_at
		FROM memory_metadata
		WHERE user_id = ? AND memory_id IN (%s)`,
		strings.Join(placeholders, ", "),
	)

	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("batch get metadata: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var md MemoryMetadata
		if err := rows.Scan(
			&md.MemoryID,
			&md.UserID,
			&md.ReferenceCount,
			&md.Importance,
			&md.LastReferenced,
			&md.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan metadata: %w", err)
		}
		result[md.MemoryID] = &md
	}

	for _, id := range memoryIDs {
		if _, ok := result[id]; !ok {
			result[id] = &MemoryMetadata{
				MemoryID:       id,
				UserID:         userID,
				ReferenceCount: 0,
				Importance:     ImportanceLow,
			}
		}
	}

	return result, nil
}

func (m *MetadataManager) DeleteMetadata(
	ctx context.Context,
	memoryID string,
	userID string,
) error {
	_, err := m.db.Exec(`
		DELETE FROM memory_metadata
		WHERE memory_id = ? AND user_id = ?`,
		memoryID, userID,
	)
	return err
}

func (m *MetadataManager) AdjustTTLByReference(
	ctx context.Context,
	memoryID string,
	userID string,
	baseTTL time.Duration,
) time.Duration {
	md, err := m.GetMetadata(ctx, memoryID, userID)
	if err != nil {
		return baseTTL
	}

	switch {
	case md.ReferenceCount >= 5:
		return baseTTL * 2
	case md.ReferenceCount >= 2:
		return baseTTL * 3 / 2
	case md.ReferenceCount == 0:
		return baseTTL / 2
	default:
		return baseTTL
	}
}

func (m *MetadataManager) GetImportanceScore(importance string) float64 {
	switch importance {
	case ImportanceHigh:
		return 1.0
	case ImportanceMedium:
		return 0.5
	case ImportanceLow:
		return 0.2
	default:
		return 0.2
	}
}

func (m *MetadataManager) GetReferenceScore(referenceCount int) float64 {
	return float64(referenceCount) / 10.0
}

func (m *MetadataManager) QueryHighImportance(
	ctx context.Context,
	userID string,
	limit int,
) ([]*MemoryMetadata, error) {
	rows, err := m.db.Query(`
		SELECT memory_id, user_id, reference_count, importance,
			last_referenced_at, created_at
		FROM memory_metadata
		WHERE user_id = ? AND importance = ?
		ORDER BY reference_count DESC, last_referenced_at DESC
		LIMIT ?`,
		userID, ImportanceHigh, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query high importance: %w", err)
	}
	defer rows.Close()

	var result []*MemoryMetadata
	for rows.Next() {
		var md MemoryMetadata
		if err := rows.Scan(
			&md.MemoryID,
			&md.UserID,
			&md.ReferenceCount,
			&md.Importance,
			&md.LastReferenced,
			&md.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan high importance: %w", err)
		}
		result = append(result, &md)
	}
	return result, nil
}

func (m *MetadataManager) QueryLowReference(
	ctx context.Context,
	userID string,
	maxReferences int,
	limit int,
) ([]*MemoryMetadata, error) {
	rows, err := m.db.Query(`
		SELECT memory_id, user_id, reference_count, importance,
			last_referenced_at, created_at
		FROM memory_metadata
		WHERE user_id = ? AND reference_count <= ?
		ORDER BY reference_count ASC, last_referenced_at ASC
		LIMIT ?`,
		userID, maxReferences, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query low reference: %w", err)
	}
	defer rows.Close()

	var result []*MemoryMetadata
	for rows.Next() {
		var md MemoryMetadata
		if err := rows.Scan(
			&md.MemoryID,
			&md.UserID,
			&md.ReferenceCount,
			&md.Importance,
			&md.LastReferenced,
			&md.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan low reference: %w", err)
		}
		result = append(result, &md)
	}
	return result, nil
}

func (m *MetadataManager) GetStats(
	ctx context.Context,
	userID string,
) (map[string]int, error) {
	result := make(map[string]int)

	rows, err := m.db.Query(`
		SELECT importance, COUNT(*) as cnt
		FROM memory_metadata
		WHERE user_id = ?
		GROUP BY importance`, userID)
	if err != nil {
		return nil, fmt.Errorf("get stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var importance string
		var cnt int
		if err := rows.Scan(&importance, &cnt); err != nil {
			return nil, fmt.Errorf("scan stats: %w", err)
		}
		result[importance] = cnt
	}

	rows, err = m.db.Query(`
		SELECT COUNT(*) as total
		FROM memory_metadata
		WHERE user_id = ?`, userID)
	if err != nil {
		return nil, fmt.Errorf("get total: %w", err)
	}
	defer rows.Close()

	if rows.Next() {
		var total int
		if err := rows.Scan(&total); err != nil {
			return nil, fmt.Errorf("scan total: %w", err)
		}
		result["total"] = total
	}

	return result, nil
}

func (m *MetadataManager) GetMemoryKey(entry *memory.Entry, userKey memory.UserKey) (memory.Key, error) {
	return memory.Key{
		AppName:  userKey.AppName,
		UserID:   userKey.UserID,
		MemoryID: entry.ID,
	}, nil
}
