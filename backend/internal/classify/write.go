package classify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// defaultBatchSize 是 Backfill 的默认每批仓库数。
const defaultBatchSize = 500

// busyRetryWindow 是遇到 SQLITE_BUSY 时的重试时间窗。
const busyRetryWindow = 15 * time.Second

// BackfillResult 汇总一次回填的统计。
type BackfillResult struct {
	Scanned      int // 扫描的仓库总数
	Categorized  int // 命中至少一个分类的仓库数
	Unclassified int // 无命中的仓库数（保留在「全部项目」）
	Assignments  int // 本次新插入的归类行数
}

type repoRecord struct {
	id   int64
	repo Repo
}

// Backfill 对全库仓库分批归类：每批一个事务，并按当前规则协调自动归类。
func Backfill(ctx context.Context, database *sql.DB, engine *Engine, batchSize int) (BackfillResult, error) {
	var result BackfillResult
	if engine == nil {
		return result, errors.New("classify engine is nil")
	}
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	var lastID int64
	for {
		records, err := loadRepoBatch(ctx, database, lastID, batchSize)
		if err != nil {
			return result, err
		}
		if len(records) == 0 {
			return result, nil
		}
		lastID = records[len(records)-1].id
		assignments, categorized, err := storeRepoBatch(ctx, database, engine, records)
		result.Scanned += len(records)
		result.Assignments += assignments
		result.Categorized += categorized
		result.Unclassified += len(records) - categorized
		if err != nil {
			return result, err
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
	}
}

// ClassifyOne 归类单个仓库（采集集成入口），复用批量归类路径。
func ClassifyOne(ctx context.Context, database *sql.DB, engine *Engine, repoID int64) error {
	return ClassifyMany(ctx, database, engine, []int64{repoID})
}

// ClassifyMany 批量加载并归类仓库。重复 ID 只处理一次；所有写入共用一个事务。
func ClassifyMany(ctx context.Context, database *sql.DB, engine *Engine, repoIDs []int64) error {
	if engine == nil {
		return errors.New("classify engine is nil")
	}
	records, err := loadRepos(ctx, database, repoIDs)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}
	_, _, err = storeRepoBatch(ctx, database, engine, records)
	return err
}

// CategoryCount 是单个分类的归类仓库数。
type CategoryCount struct {
	Slug      string
	Title     string
	SortOrder int
	Repos     int
}

// CategoryCounts 返回全部分类的归类仓库数（含 0），按 sort_order 排序。
func CategoryCounts(ctx context.Context, database *sql.DB) ([]CategoryCount, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT c.slug, c.title, c.sort_order, COUNT(rc.repo_id)
		FROM categories c
		LEFT JOIN repo_categories rc ON rc.category_id = c.id
		GROUP BY c.id
		ORDER BY c.sort_order, c.id`)
	if err != nil {
		return nil, fmt.Errorf("count category assignments: %w", err)
	}
	defer rows.Close()
	counts := []CategoryCount{}
	for rows.Next() {
		var count CategoryCount
		if err := rows.Scan(&count.Slug, &count.Title, &count.SortOrder, &count.Repos); err != nil {
			return nil, fmt.Errorf("scan category count: %w", err)
		}
		counts = append(counts, count)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate category counts: %w", err)
	}
	return counts, nil
}

func loadRepoBatch(ctx context.Context, database *sql.DB, afterID int64, limit int) ([]repoRecord, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, name, full_name, COALESCE(description, ''), COALESCE(language, ''), topics_json
		FROM repositories WHERE id > ? ORDER BY id LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("load repository batch after %d: %w", afterID, err)
	}
	defer rows.Close()
	records := []repoRecord{}
	for rows.Next() {
		record, err := scanRepoRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate repository batch: %w", err)
	}
	return records, nil
}

func loadRepos(ctx context.Context, database *sql.DB, repoIDs []int64) ([]repoRecord, error) {
	unique := make([]int64, 0, len(repoIDs))
	seen := make(map[int64]struct{}, len(repoIDs))
	for _, repoID := range repoIDs {
		if repoID <= 0 {
			continue
		}
		if _, exists := seen[repoID]; exists {
			continue
		}
		seen[repoID] = struct{}{}
		unique = append(unique, repoID)
	}
	if len(unique) == 0 {
		return nil, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(unique)), ",")
	arguments := make([]any, len(unique))
	for index, repoID := range unique {
		arguments[index] = repoID
	}
	rows, err := database.QueryContext(ctx, `
		SELECT id, name, full_name, COALESCE(description, ''), COALESCE(language, ''), topics_json
		FROM repositories WHERE id IN (`+placeholders+`) ORDER BY id`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("load repositories for classification: %w", err)
	}
	defer rows.Close()

	records := make([]repoRecord, 0, len(unique))
	for rows.Next() {
		record, err := scanRepoRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate repositories for classification: %w", err)
	}
	return records, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRepoRecord(row rowScanner) (repoRecord, error) {
	var record repoRecord
	var topicsJSON string
	if err := row.Scan(&record.id, &record.repo.Name, &record.repo.FullName,
		&record.repo.Description, &record.repo.Language, &topicsJSON); err != nil {
		return repoRecord{}, err
	}
	topics, err := parseTopics(topicsJSON)
	if err != nil {
		return repoRecord{}, fmt.Errorf("parse topics_json for repository %d: %w", record.id, err)
	}
	record.repo.Topics = topics
	return record, nil
}

func parseTopics(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var topics []string
	if err := json.Unmarshal([]byte(raw), &topics); err != nil {
		return nil, err
	}
	return topics, nil
}

type storedAssignment struct {
	categoryID int64
	source     string
}

// storeRepoBatch 在一个事务内协调一批归类行，返回（新增行数, 命中仓库数, 错误）。
func storeRepoBatch(ctx context.Context, database *sql.DB, engine *Engine, records []repoRecord) (int, int, error) {
	var assignments, categorized int
	err := retryBusy(ctx, func() error {
		tx, err := database.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin classify transaction: %w", err)
		}
		defer tx.Rollback()

		existing, err := loadStoredAssignments(ctx, tx, records)
		if err != nil {
			return err
		}
		assignments, categorized = 0, 0
		for _, record := range records {
			stored := existing[record.id]
			hasManual := false
			for _, assignment := range stored {
				if assignment.source == "manual" {
					hasManual = true
					break
				}
			}
			if hasManual {
				if _, err := tx.ExecContext(ctx, `DELETE FROM repo_categories
					WHERE repo_id = ? AND source = 'auto'`, record.id); err != nil {
					return fmt.Errorf("remove automatic categories for manually classified repository %d: %w", record.id, err)
				}
				continue
			}

			matches := engine.ClassifyRepo(record.repo)
			if len(matches) > 0 {
				categorized++
			}
			matched := make(map[int64]Match, len(matches))
			for _, match := range matches {
				matched[match.CategoryID] = match
			}
			for _, assignment := range stored {
				if assignment.source != "auto" {
					continue
				}
				if _, keep := matched[assignment.categoryID]; keep {
					continue
				}
				if _, err := tx.ExecContext(ctx, `DELETE FROM repo_categories
					WHERE repo_id = ? AND category_id = ? AND source = 'auto'`, record.id, assignment.categoryID); err != nil {
					return fmt.Errorf("remove stale category for repository %d: %w", record.id, err)
				}
			}

			for _, match := range matches {
				_, alreadyStored := stored[match.CategoryID]
				result, err := tx.ExecContext(ctx, `
					INSERT INTO repo_categories (repo_id, category_id, is_primary, source)
					VALUES (?, ?, ?, 'auto')
					ON CONFLICT(repo_id, category_id) DO UPDATE SET is_primary = excluded.is_primary
					WHERE repo_categories.source = 'auto'
						AND repo_categories.is_primary <> excluded.is_primary`, record.id, match.CategoryID, boolToInt(match.IsPrimary))
				if err != nil {
					return fmt.Errorf("store category %s for repository %d: %w", match.Slug, record.id, err)
				}
				if !alreadyStored {
					if affected, err := result.RowsAffected(); err == nil && affected > 0 {
						assignments++
					}
				}
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit classify transaction: %w", err)
		}
		return nil
	})
	return assignments, categorized, err
}

func loadStoredAssignments(ctx context.Context, tx *sql.Tx, records []repoRecord) (map[int64]map[int64]storedAssignment, error) {
	stored := make(map[int64]map[int64]storedAssignment, len(records))
	if len(records) == 0 {
		return stored, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(records)), ",")
	arguments := make([]any, len(records))
	for index, record := range records {
		arguments[index] = record.id
		stored[record.id] = make(map[int64]storedAssignment)
	}
	rows, err := tx.QueryContext(ctx, `SELECT repo_id, category_id, source FROM repo_categories
		WHERE repo_id IN (`+placeholders+`)`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("load stored category assignments: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var repoID int64
		var assignment storedAssignment
		if err := rows.Scan(&repoID, &assignment.categoryID, &assignment.source); err != nil {
			return nil, fmt.Errorf("scan stored category assignment: %w", err)
		}
		stored[repoID][assignment.categoryID] = assignment
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stored category assignments: %w", err)
	}
	return stored, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// retryBusy 在 SQLITE_BUSY / database is locked 时按退避重试，
// 数据库 DSN 已设置 busy_timeout，这里兜底并发写入窗口。
func retryBusy(ctx context.Context, operation func() error) error {
	deadline := time.Now().Add(busyRetryWindow)
	for delay := 10 * time.Millisecond; ; delay *= 2 {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := operation()
		if err == nil || !isBusyError(err) || time.Now().After(deadline) {
			return err
		}
		if delay > 200*time.Millisecond {
			delay = 200 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func isBusyError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database is locked")
}
