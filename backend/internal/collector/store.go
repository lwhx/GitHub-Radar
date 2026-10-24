package collector

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/github-radar/github-radar/internal/github"
)

const (
	statusPending    = "pending"
	statusCompleted  = "completed"
	statusSplit      = "split"
	statusIncomplete = "coverage_incomplete"
)

const timestampLayout = "2006-01-02T15:04:05Z"

func formatTime(value time.Time) string {
	return value.UTC().Truncate(time.Second).Format(timestampLayout)
}

// EnsureWindow creates or reuses the root window for one time slice.
func (collector *Collector) EnsureWindow(ctx context.Context, from, to time.Time, runID int64) (int64, error) {
	if !from.Before(to) {
		return 0, errors.New("collection window must have start before end")
	}
	return collector.insertWindow(ctx, from, to, nil, 0, runID)
}

type windowQueryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (collector *Collector) insertWindow(ctx context.Context, from, to time.Time, parentID *int64, depth int, runID int64) (int64, error) {
	return collector.insertWindowWith(ctx, collector.db, from, to, parentID, depth, runID)
}

func (collector *Collector) insertWindowWith(ctx context.Context, query windowQueryRower, from, to time.Time, parentID *int64, depth int, runID int64) (int64, error) {
	now := formatTime(collector.now())
	var jobRun any
	if runID > 0 {
		jobRun = runID
	}
	var parent any
	if parentID != nil {
		parent = *parentID
	}
	var windowID int64
	err := query.QueryRowContext(ctx, `INSERT INTO collection_windows
		(source, query_hash, time_from, time_to, page, coverage_status, job_run_id, parent_id, split_depth, attempt_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, 'pending', ?, ?, ?, 0, ?, ?)
		ON CONFLICT(source, query_hash, time_from, time_to) DO UPDATE SET updated_at = excluded.updated_at
		RETURNING id`,
		collector.source, collector.queryHash, formatTime(from), formatTime(to), jobRun, parent, depth, now, now).Scan(&windowID)
	if err != nil {
		return 0, fmt.Errorf("create collection window %s..%s: %w", formatTime(from), formatTime(to), err)
	}
	return windowID, nil
}

func (collector *Collector) splitWindow(ctx context.Context, item window, result github.SearchResult) error {
	midpoint := item.From.Add(item.To.Sub(item.From) / 2).Truncate(time.Second)
	if !midpoint.After(item.From) || !midpoint.Before(item.To) {
		return collector.markIncomplete(ctx, item.ID, "window cannot be split further")
	}
	tx, err := collector.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin window split: %w", err)
	}
	defer tx.Rollback()
	now := formatTime(collector.now())
	if _, err := tx.ExecContext(ctx, `UPDATE collection_windows
		SET coverage_status = ?, total_count = ?, incomplete_results = ?, next_url = NULL, updated_at = ?, completed_at = ?
		WHERE id = ?`,
		statusSplit, result.TotalCount, boolToInt(result.IncompleteResults), now, now, item.ID); err != nil {
		return fmt.Errorf("mark window %d split: %w", item.ID, err)
	}
	parent := item.ID
	if _, err := collector.insertWindowWith(ctx, tx, item.From, midpoint, &parent, item.Depth+1, item.RunID); err != nil {
		return err
	}
	if _, err := collector.insertWindowWith(ctx, tx, midpoint, item.To, &parent, item.Depth+1, item.RunID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit window split: %w", err)
	}
	return nil
}

type pageSummary struct {
	Created   int
	Updated   int
	Skipped   int
	Snapshots int
}

func (collector *Collector) storePage(ctx context.Context, windowID int64, item window, result github.SearchResult, nextURL string, firstPage bool, page int) (pageSummary, error) {
	var summary pageSummary
	storedIDs := make([]int64, 0, len(result.Items))
	tx, err := collector.db.BeginTx(ctx, nil)
	if err != nil {
		return summary, fmt.Errorf("begin page storage: %w", err)
	}
	defer tx.Rollback()
	now := formatTime(collector.now())
	for _, repository := range result.Items {
		if !isPublic(repository) {
			summary.Skipped++
			continue
		}
		repoID, created, snapshot, err := upsertRepository(ctx, tx, repository, now)
		if err != nil {
			if isFullNameConflict(err) {
				summary.Skipped++
				continue
			}
			return summary, fmt.Errorf("store repository %s: %w", repository.FullName, err)
		}
		storedIDs = append(storedIDs, repoID)
		if created {
			summary.Created++
		} else {
			summary.Updated++
		}
		if snapshot {
			summary.Snapshots++
		}
	}
	if err := tx.Commit(); err != nil {
		return summary, fmt.Errorf("commit page storage: %w", err)
	}
	// 已存在仓库的描述、topics、language 也可能随搜索结果更新，仍需重算以移除过期自动归类。
	if err := collector.classifyStored(ctx, storedIDs); err != nil {
		return summary, err
	}
	// 仅在仓库入库和归类都成功后推进游标；任一步失败时下次会重试同一页。
	var nullableNext any
	if nextURL != "" {
		nullableNext = nextURL
	}
	if firstPage {
		if _, err := collector.db.ExecContext(ctx, `UPDATE collection_windows
			SET page = ?, next_url = ?, total_count = ?, incomplete_results = ?, attempt_count = 0, updated_at = ?
			WHERE id = ?`,
			page+1, nullableNext, result.TotalCount, boolToInt(result.IncompleteResults), now, windowID); err != nil {
			return summary, fmt.Errorf("update collection window %d: %w", windowID, err)
		}
	} else {
		if _, err := collector.db.ExecContext(ctx, `UPDATE collection_windows
			SET page = ?, next_url = ?, attempt_count = 0, updated_at = ?
			WHERE id = ?`, page+1, nullableNext, now, windowID); err != nil {
			return summary, fmt.Errorf("update collection window %d: %w", windowID, err)
		}
	}
	return summary, nil
}

func upsertRepository(ctx context.Context, tx *sql.Tx, repository github.Repository, now string) (int64, bool, bool, error) {
	if repository.FullName == "" || repository.ID == 0 {
		return 0, false, false, errors.New("GitHub search result is missing id or full name")
	}
	owner, name, found := strings.Cut(repository.FullName, "/")
	if !found {
		return 0, false, false, errors.New("GitHub repository full name is malformed")
	}
	topics, err := json.Marshal(repository.Topics)
	if err != nil {
		return 0, false, false, fmt.Errorf("encode repository topics: %w", err)
	}
	var pushedAt any
	if repository.PushedAt != nil && !repository.PushedAt.IsZero() {
		pushedAt = formatTime(*repository.PushedAt)
	}
	var license any
	if repository.License != nil && repository.License.SPDXID != "" {
		license = repository.License.SPDXID
	}
	capturedAt, err := time.Parse(timestampLayout, now)
	if err != nil {
		return 0, false, false, fmt.Errorf("parse repository capture time: %w", err)
	}

	var existingID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM repositories WHERE github_id = ?`, repository.ID).Scan(&existingID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO repositories
			(github_id, owner, name, full_name, html_url, homepage_url, description, language, license_spdx,
			 default_branch, topics_json, stars_count, forks_count, open_issues_count, is_fork, is_archived,
			 is_disabled, github_created_at, github_updated_at, pushed_at, first_seen_at, last_synced_at,
			 last_successful_fetch_at, sync_status, visibility, next_sync_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', 'public', ?)`,
			repository.ID, owner, name, repository.FullName, repository.HTMLURL, nullString(repository.Homepage),
			nullString(repository.Description), nullString(repository.Language), license,
			nullString(&repository.DefaultBranch), string(topics), repository.StargazersCount, repository.ForksCount,
			repository.OpenIssuesCount, boolToInt(repository.Fork), boolToInt(repository.Archived), boolToInt(repository.Disabled),
			formatTime(repository.CreatedAt), formatTime(repository.UpdatedAt), pushedAt, now, now, now,
			formatTime(capturedAt.Add(initialDetailDelay)))
		if insertErr != nil {
			return 0, false, false, insertErr
		}
		repoID, idErr := result.LastInsertId()
		if idErr != nil {
			return 0, false, false, fmt.Errorf("read repository id: %w", idErr)
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO star_snapshots
			(repo_id, star_count, captured_at, source, sample_status) VALUES (?, ?, ?, 'search', 'ok')`,
			repoID, repository.StargazersCount, now); err != nil {
			return 0, false, false, fmt.Errorf("record initial star snapshot: %w", err)
		}
		return repoID, true, true, nil
	case err != nil:
		return 0, false, false, err
	}

	if _, err := tx.ExecContext(ctx, `UPDATE repositories
		SET owner = ?, name = ?, full_name = ?, html_url = ?, homepage_url = ?, description = ?, language = ?,
			license_spdx = ?, default_branch = ?, topics_json = ?, forks_count = ?,
			open_issues_count = ?, is_fork = ?, is_archived = ?, is_disabled = ?, github_updated_at = ?,
			pushed_at = ?, last_synced_at = ?,
			next_sync_at = CASE WHEN stars_count <> ? THEN ? ELSE next_sync_at END
		WHERE id = ?`,
		owner, name, repository.FullName, repository.HTMLURL, nullString(repository.Homepage),
		nullString(repository.Description), nullString(repository.Language), license,
		nullString(&repository.DefaultBranch), string(topics), repository.ForksCount,
		repository.OpenIssuesCount, boolToInt(repository.Fork), boolToInt(repository.Archived), boolToInt(repository.Disabled),
		formatTime(repository.UpdatedAt), pushedAt, now, repository.StargazersCount, now, existingID); err != nil {
		return 0, false, false, err
	}
	return existingID, false, false, nil
}

func (collector *Collector) markCompleted(ctx context.Context, windowID int64) error {
	now := formatTime(collector.now())
	_, err := collector.db.ExecContext(ctx, `UPDATE collection_windows
		SET coverage_status = ?, next_url = NULL, attempt_count = 0, updated_at = ?, completed_at = ?
		WHERE id = ?`, statusCompleted, now, now, windowID)
	if err != nil {
		return fmt.Errorf("complete collection window %d: %w", windowID, err)
	}
	return nil
}

func (collector *Collector) markIncomplete(ctx context.Context, windowID int64, reason string) error {
	now := formatTime(collector.now())
	_, err := collector.db.ExecContext(ctx, `UPDATE collection_windows
		SET coverage_status = ?, last_error = ?, next_url = NULL, updated_at = ?, completed_at = ?
		WHERE id = ?`, statusIncomplete, truncate(reason, 500), now, now, windowID)
	if err != nil {
		return fmt.Errorf("mark collection window %d incomplete: %w", windowID, err)
	}
	return nil
}

func (collector *Collector) markAttemptFailed(ctx context.Context, windowID int64, cause error) error {
	now := collector.now().UTC().Truncate(time.Second)
	tx, err := collector.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin collection window failure: %w", err)
	}
	defer tx.Rollback()
	var previousAttempts int
	if err := tx.QueryRowContext(ctx, `SELECT attempt_count FROM collection_windows WHERE id = ?`, windowID).Scan(&previousAttempts); err != nil {
		return fmt.Errorf("read collection window %d attempts: %w", windowID, err)
	}
	attempts := previousAttempts + 1
	status := statusPending
	var nextAttempt any = formatTime(windowRetryAt(cause, now, previousAttempts))
	var completedAt any
	if attempts >= maxWindowAttempts {
		status = statusIncomplete
		nextAttempt = nil
		completedAt = formatTime(now)
	}
	_, err = tx.ExecContext(ctx, `UPDATE collection_windows
		SET attempt_count = ?, coverage_status = ?, next_attempt_at = ?, completed_at = ?, last_error = ?, updated_at = ?
		WHERE id = ?`, attempts, status, nextAttempt, completedAt, truncate(cause.Error(), 500), formatTime(now), windowID)
	if err != nil {
		return fmt.Errorf("record collection window %d failure: %w", windowID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit collection window %d failure: %w", windowID, err)
	}
	return nil
}

func windowRetryAt(cause error, now time.Time, previousAttempts int) time.Time {
	var apiError *github.APIError
	if errors.As(cause, &apiError) && apiError.RetryAt.After(now) {
		return apiError.RetryAt
	}
	delay := windowRetryBase
	for attempt := 0; attempt < previousAttempts && delay < windowRetryMaximum; attempt++ {
		delay *= 2
	}
	if delay > windowRetryMaximum {
		delay = windowRetryMaximum
	}
	return now.Add(delay)
}

func isPublic(repository github.Repository) bool {
	if repository.Private {
		return false
	}
	return repository.Visibility == "" || repository.Visibility == "public"
}

func isFullNameConflict(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed: repositories.full_name")
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullString(value *string) any {
	if value == nil || *value == "" {
		return nil
	}
	return *value
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
