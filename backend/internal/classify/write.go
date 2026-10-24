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

// Backfill 对全库仓库分批归类：每批一个事务，INSERT OR IGNORE 保证重复执行幂等。
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

// ClassifyOne 归类单个仓库（采集集成入口）：重复执行幂等，
// 已存在的归类行（含 source='manual' 的人工归类）不会被覆盖。
func ClassifyOne(ctx context.Context, database *sql.DB, engine *Engine, repoID int64) error {
	if engine == nil {
		return errors.New("classify engine is nil")
	}
	record, found, err := loadRepo(ctx, database, repoID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	_, _, err = storeRepoBatch(ctx, database, engine, []repoRecord{record})
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

func loadRepo(ctx context.Context, database *sql.DB, repoID int64) (repoRecord, bool, error) {
	row := database.QueryRowContext(ctx, `
		SELECT id, name, full_name, COALESCE(description, ''), COALESCE(language, ''), topics_json
		FROM repositories WHERE id = ?`, repoID)
	record, err := scanRepoRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return repoRecord{}, false, nil
	}
	if err != nil {
		return repoRecord{}, false, err
	}
	return record, true, nil
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

// storeRepoBatch 在一个事务内写入一批归类行，返回（新增行数, 命中仓库数, 错误）。
func storeRepoBatch(ctx context.Context, database *sql.DB, engine *Engine, records []repoRecord) (int, int, error) {
	var assignments, categorized int
	err := retryBusy(ctx, func() error {
		tx, err := database.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin classify transaction: %w", err)
		}
		defer tx.Rollback()

		assignments, categorized = 0, 0
		for _, record := range records {
			matches := engine.ClassifyRepo(record.repo)
			if len(matches) == 0 {
				continue
			}
			categorized++
			for _, match := range matches {
				inserted, err := tx.ExecContext(ctx, `
					INSERT OR IGNORE INTO repo_categories (repo_id, category_id, is_primary, source)
					VALUES (?, ?, ?, 'auto')`, record.id, match.CategoryID, boolToInt(match.IsPrimary))
				if err != nil {
					return fmt.Errorf("store category %s for repository %d: %w", match.Slug, record.id, err)
				}
				if affected, err := inserted.RowsAffected(); err == nil && affected > 0 {
					assignments++
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
