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
	detailSyncInterval     = 6 * time.Hour
	initialDetailDelay     = 30 * time.Minute
	inactiveSyncInterval   = 24 * time.Hour
	detailRetryBase        = 15 * time.Minute
	detailRetryMaximum     = 6 * time.Hour
	inaccessibleRetryDelay = 24 * time.Hour
	goneRetryDelay         = 7 * 24 * time.Hour
)

// RepositoryGetter is the conditional repository-detail surface used by the synchronizer.
type RepositoryGetter interface {
	GetRepository(context.Context, string, string, string) (github.Repository, github.ResponseMeta, error)
}

type DetailSummary struct {
	Requests    int
	Updated     int
	Snapshots   int
	NotModified int
	Failed      int
	RateLimited int
}

type DetailSynchronizer struct {
	db     *sql.DB
	getter RepositoryGetter
	now    func() time.Time
	limit  int
}

type dueRepository struct {
	ID                 int64
	GitHubID           int64
	Owner              string
	Name               string
	FullName           string
	ETag               string
	ConsecutiveFailure int
}

func NewDetailSynchronizer(database *sql.DB, getter RepositoryGetter, clocks ...func() time.Time) *DetailSynchronizer {
	now := time.Now
	if len(clocks) > 0 {
		now = clocks[0]
	}
	return newDetailSynchronizer(database, getter, now, 100)
}

func newDetailSynchronizer(database *sql.DB, getter RepositoryGetter, now func() time.Time, limit int) *DetailSynchronizer {
	return &DetailSynchronizer{db: database, getter: getter, now: now, limit: limit}
}

func (synchronizer *DetailSynchronizer) SyncDue(ctx context.Context, runID int64) (DetailSummary, error) {
	items, err := synchronizer.dueRepositories(ctx)
	if err != nil {
		return DetailSummary{}, err
	}
	var summary DetailSummary
	for _, item := range items {
		repository, meta, getErr := synchronizer.getter.GetRepository(ctx, item.Owner, item.Name, item.ETag)
		summary.Requests++
		if getErr != nil {
			stop, handleErr := synchronizer.handleFailure(ctx, runID, item, getErr)
			if handleErr != nil {
				return summary, errors.Join(getErr, handleErr)
			}
			if stop {
				summary.RateLimited++
				return summary, getErr
			}
			summary.Failed++
			continue
		}
		if meta.NotModified {
			if err := synchronizer.storeNotModified(ctx, item, meta); err != nil {
				return summary, err
			}
			summary.NotModified++
			continue
		}
		if err := validateDetail(item, repository); err != nil {
			if _, handleErr := synchronizer.handleFailure(ctx, runID, item, err); handleErr != nil {
				return summary, errors.Join(err, handleErr)
			}
			summary.Failed++
			continue
		}
		if err := synchronizer.storeChanged(ctx, runID, item, repository, meta); err != nil {
			return summary, err
		}
		summary.Updated++
		summary.Snapshots++
	}
	return summary, nil
}

func (synchronizer *DetailSynchronizer) dueRepositories(ctx context.Context) ([]dueRepository, error) {
	now := formatTime(synchronizer.now())
	rows, err := synchronizer.db.QueryContext(ctx, `SELECT id, github_id, owner, name, full_name, COALESCE(etag, ''), consecutive_failures
		FROM repositories
		WHERE visibility = 'public' AND is_hidden = 0 AND (next_sync_at IS NULL OR next_sync_at <= ?)
		ORDER BY CASE WHEN next_sync_at IS NULL THEN 0 ELSE 1 END, next_sync_at, consecutive_failures, id
		LIMIT ?`, now, synchronizer.limit)
	if err != nil {
		return nil, fmt.Errorf("query repositories due for detail sync: %w", err)
	}
	defer rows.Close()
	items := make([]dueRepository, 0, synchronizer.limit)
	for rows.Next() {
		var item dueRepository
		if err := rows.Scan(&item.ID, &item.GitHubID, &item.Owner, &item.Name, &item.FullName, &item.ETag, &item.ConsecutiveFailure); err != nil {
			return nil, fmt.Errorf("scan repository due for detail sync: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read repositories due for detail sync: %w", err)
	}
	return items, nil
}

func validateDetail(item dueRepository, repository github.Repository) error {
	if repository.ID != item.GitHubID {
		return errors.New("repository identity mismatch")
	}
	if !isPublic(repository) {
		return errors.New("repository detail is not public")
	}
	if repository.FullName == "" || repository.Name == "" || repository.Owner.Login == "" {
		return errors.New("repository detail is missing its canonical name")
	}
	return nil
}

func (synchronizer *DetailSynchronizer) storeChanged(ctx context.Context, runID int64, item dueRepository, repository github.Repository, meta github.ResponseMeta) error {
	tx, err := synchronizer.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin repository detail update: %w", err)
	}
	defer tx.Rollback()
	now := synchronizer.now().UTC().Truncate(time.Second)
	nextInterval := detailSyncInterval
	if repository.Archived || repository.Disabled {
		nextInterval = inactiveSyncInterval
	}
	topics, err := json.Marshal(repository.Topics)
	if err != nil {
		return fmt.Errorf("encode repository topics: %w", err)
	}
	var license any
	if repository.License != nil && repository.License.SPDXID != "" {
		license = repository.License.SPDXID
	}
	var pushedAt any
	if repository.PushedAt != nil && !repository.PushedAt.IsZero() {
		pushedAt = formatTime(*repository.PushedAt)
	}
	result, err := tx.ExecContext(ctx, `UPDATE repositories SET
		owner = ?, name = ?, full_name = ?, html_url = ?, homepage_url = ?, description = ?, language = ?,
		license_spdx = ?, default_branch = ?, topics_json = ?, stars_count = ?, forks_count = ?,
		open_issues_count = ?, is_fork = ?, is_archived = ?, is_disabled = ?, github_updated_at = ?, pushed_at = ?,
		etag = ?, sync_status = 'synced', last_synced_at = ?, last_successful_fetch_at = ?, next_sync_at = ?,
		last_error_code = NULL, consecutive_failures = 0
		WHERE id = ? AND github_id = ?`,
		repository.Owner.Login, repository.Name, repository.FullName, repository.HTMLURL,
		nullString(repository.Homepage), nullString(repository.Description), nullString(repository.Language), license,
		nullString(&repository.DefaultBranch), string(topics), repository.StargazersCount, repository.ForksCount,
		repository.OpenIssuesCount, boolToInt(repository.Fork), boolToInt(repository.Archived), boolToInt(repository.Disabled),
		formatTime(repository.UpdatedAt), pushedAt, nullText(meta.ETag), formatTime(now), formatTime(now),
		formatTime(now.Add(nextInterval)), item.ID, item.GitHubID)
	if err != nil {
		return fmt.Errorf("update repository detail %s: %w", item.FullName, err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return fmt.Errorf("update repository detail %s affected %d rows: %w", item.FullName, rows, err)
	}
	snapshotAt, err := nextSnapshotTime(ctx, tx, item.ID, now)
	if err != nil {
		return fmt.Errorf("choose repository detail snapshot time %s: %w", item.FullName, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO star_snapshots
		(repo_id, star_count, captured_at, source, sample_status) VALUES (?, ?, ?, 'repository_detail', 'ok')`,
		item.ID, repository.StargazersCount, formatTime(snapshotAt)); err != nil {
		return fmt.Errorf("insert repository detail snapshot %s: %w", item.FullName, err)
	}
	if runID > 0 && !strings.EqualFold(item.FullName, repository.FullName) {
		if err := insertDetailEvent(ctx, tx, runID, item.ID, now, "info", "repository_renamed", "Repository renamed from "+item.FullName+" to "+repository.FullName, 0); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit repository detail update %s: %w", item.FullName, err)
	}
	return nil
}

func (synchronizer *DetailSynchronizer) storeNotModified(ctx context.Context, item dueRepository, meta github.ResponseMeta) error {
	now := synchronizer.now().UTC().Truncate(time.Second)
	_, err := synchronizer.db.ExecContext(ctx, `UPDATE repositories SET
		etag = COALESCE(NULLIF(?, ''), etag), sync_status = 'synced', last_synced_at = ?, next_sync_at = ?,
		last_error_code = NULL, consecutive_failures = 0
		WHERE id = ? AND github_id = ?`,
		meta.ETag, formatTime(now), formatTime(now.Add(detailSyncInterval)), item.ID, item.GitHubID)
	if err != nil {
		return fmt.Errorf("store repository not-modified result %s: %w", item.FullName, err)
	}
	return nil
}

func (synchronizer *DetailSynchronizer) handleFailure(ctx context.Context, runID int64, item dueRepository, cause error) (bool, error) {
	var apiError *github.APIError
	if errors.As(cause, &apiError) && (apiError.Kind == github.ErrorPrimaryRateLimit || apiError.Kind == github.ErrorSecondaryRateLimit) {
		return true, nil
	}
	code, status, delay, level := detailFailureState(item, cause)
	now := synchronizer.now().UTC().Truncate(time.Second)
	tx, err := synchronizer.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin repository detail failure: %w", err)
	}
	defer tx.Rollback()
	clearETag := code == "not_found" || code == "gone" || code == "not_public"
	_, err = tx.ExecContext(ctx, `UPDATE repositories SET sync_status = ?, last_error_code = ?,
		consecutive_failures = consecutive_failures + 1, next_sync_at = ?,
		etag = CASE WHEN ? THEN NULL ELSE etag END WHERE id = ?`,
		status, code, formatTime(now.Add(delay)), boolToInt(clearETag), item.ID)
	if err != nil {
		return false, fmt.Errorf("store repository detail failure %s: %w", item.FullName, err)
	}
	if runID > 0 {
		message := truncateDetailMessage(cause.Error(), 2000)
		if err := insertDetailEvent(ctx, tx, runID, item.ID, now, level, "repository_"+code, message, item.ConsecutiveFailure+1); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit repository detail failure %s: %w", item.FullName, err)
	}
	return false, nil
}

func detailFailureState(item dueRepository, cause error) (code string, status string, delay time.Duration, level string) {
	var apiError *github.APIError
	if errors.As(cause, &apiError) {
		switch apiError.Kind {
		case github.ErrorNotFound:
			return "not_found", "inaccessible", inaccessibleRetryDelay, "warning"
		case github.ErrorGone:
			return "gone", "inaccessible", goneRetryDelay, "warning"
		case github.ErrorAuthentication:
			return "authentication", "retrying", detailRetryDelay(item.ConsecutiveFailure), "error"
		case github.ErrorForbidden:
			return "forbidden", "retrying", detailRetryDelay(item.ConsecutiveFailure), "warning"
		}
	}
	if strings.Contains(cause.Error(), "identity mismatch") {
		return "identity_mismatch", "retrying", detailRetryDelay(item.ConsecutiveFailure), "error"
	}
	if strings.Contains(cause.Error(), "not public") {
		return "not_public", "inaccessible", inaccessibleRetryDelay, "warning"
	}
	return "detail_fetch_failed", "retrying", detailRetryDelay(item.ConsecutiveFailure), "warning"
}

func detailRetryDelay(previousFailures int) time.Duration {
	delay := detailRetryBase
	for attempt := 0; attempt < previousFailures && delay < detailRetryMaximum; attempt++ {
		delay *= 2
	}
	if delay > detailRetryMaximum {
		return detailRetryMaximum
	}
	return delay
}

func nextSnapshotTime(ctx context.Context, tx *sql.Tx, repoID int64, observedAt time.Time) (time.Time, error) {
	var latest sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT MAX(captured_at) FROM star_snapshots WHERE repo_id = ?`, repoID).Scan(&latest); err != nil {
		return time.Time{}, err
	}
	if !latest.Valid {
		return observedAt, nil
	}
	latestAt, err := time.Parse(timestampLayout, latest.String)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse latest snapshot time: %w", err)
	}
	if !latestAt.Before(observedAt) {
		return latestAt.Add(time.Second), nil
	}
	return observedAt, nil
}

func insertDetailEvent(ctx context.Context, tx *sql.Tx, runID, repoID int64, now time.Time, level, code, message string, attempt int) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO job_run_events
		(run_id, repo_id, occurred_at, level, event_code, message, attempt, details_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, '{}')`, runID, repoID, formatTime(now), level, code, message, attempt)
	if err != nil {
		return fmt.Errorf("insert repository detail event: %w", err)
	}
	return nil
}

func truncateDetailMessage(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func nullText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
