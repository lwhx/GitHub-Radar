package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Job describes the scheduled collection job and its current lease.
type Job struct {
	ID             int64      `json:"id"`
	JobType        string     `json:"job_type"`
	Enabled        bool       `json:"enabled"`
	IntervalSecond int        `json:"interval_seconds"`
	NextRunAt      *time.Time `json:"next_run_at"`
	LeaseOwner     *string    `json:"lease_owner"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at"`
}

// JobRun is a read model for the run history API.
type JobRun struct {
	ID                      int64      `json:"id"`
	JobID                   int64      `json:"job_id"`
	Status                  string     `json:"status"`
	Trigger                 string     `json:"trigger"`
	StartedAt               time.Time  `json:"started_at"`
	FinishedAt              *time.Time `json:"finished_at"`
	RequestCount            int        `json:"request_count"`
	CreatedCount            int        `json:"created_count"`
	UpdatedCount            int        `json:"updated_count"`
	SnapshotCount           int        `json:"snapshot_count"`
	NotModifiedCount        int        `json:"not_modified_count"`
	RetryCount              int        `json:"retry_count"`
	RateLimitedCount        int        `json:"rate_limited_count"`
	FailedCount             int        `json:"failed_count"`
	CoverageIncompleteCount int        `json:"coverage_incomplete_count"`
	LastErrorCode           *string    `json:"last_error_code"`
	ErrorSummary            *string    `json:"error_summary"`
}

// RunEvent is one structured collection log line.
type RunEvent struct {
	ID         int64     `json:"id"`
	RunID      int64     `json:"run_id"`
	WindowID   *int64    `json:"window_id"`
	RepoID     *int64    `json:"repo_id"`
	OccurredAt time.Time `json:"occurred_at"`
	Level      string    `json:"level"`
	EventCode  string    `json:"event_code"`
	Message    string    `json:"message"`
	Attempt    int       `json:"attempt"`
}

func (service *Service) Jobs(ctx context.Context) ([]Job, error) {
	rows, err := service.db.QueryContext(ctx, `SELECT id, job_type, enabled, interval_seconds, next_run_at, lease_owner, lease_expires_at
		FROM collector_jobs ORDER BY priority DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("query collector jobs: %w", err)
	}
	defer rows.Close()
	var jobs []Job
	for rows.Next() {
		var item Job
		var enabled int
		var nextRun, leaseExpiry, leaseOwner sql.NullString
		if err := rows.Scan(&item.ID, &item.JobType, &enabled, &item.IntervalSecond, &nextRun, &leaseOwner, &leaseExpiry); err != nil {
			return nil, fmt.Errorf("scan collector job: %w", err)
		}
		item.Enabled = enabled == 1
		item.NextRunAt = parseTime(nextRun)
		item.LeaseExpiresAt = parseTime(leaseExpiry)
		if leaseOwner.Valid {
			owner := leaseOwner.String
			item.LeaseOwner = &owner
		}
		jobs = append(jobs, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read collector jobs: %w", err)
	}
	return jobs, nil
}

func (service *Service) JobRunHistory(ctx context.Context, limit int) ([]JobRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := service.db.QueryContext(ctx, `SELECT id, job_id, status, trigger, started_at, finished_at,
			request_count, created_count, updated_count, snapshot_count, not_modified_count, retry_count,
			rate_limited_count, failed_count, coverage_incomplete_count, last_error_code, error_summary
		FROM job_runs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("query job runs: %w", err)
	}
	defer rows.Close()
	var runs []JobRun
	for rows.Next() {
		var item JobRun
		var startedAt string
		var finishedAt, lastError, summary sql.NullString
		if err := rows.Scan(&item.ID, &item.JobID, &item.Status, &item.Trigger, &startedAt, &finishedAt,
			&item.RequestCount, &item.CreatedCount, &item.UpdatedCount, &item.SnapshotCount,
			&item.NotModifiedCount, &item.RetryCount, &item.RateLimitedCount, &item.FailedCount,
			&item.CoverageIncompleteCount, &lastError, &summary); err != nil {
			return nil, fmt.Errorf("scan job run: %w", err)
		}
		started := parseTime(sql.NullString{String: startedAt, Valid: true})
		if started != nil {
			item.StartedAt = *started
		}
		item.FinishedAt = parseTime(finishedAt)
		if lastError.Valid {
			value := lastError.String
			item.LastErrorCode = &value
		}
		if summary.Valid {
			value := summary.String
			item.ErrorSummary = &value
		}
		runs = append(runs, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read job runs: %w", err)
	}
	return runs, nil
}

func (service *Service) RunEvents(ctx context.Context, runID int64, limit int) ([]RunEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := service.db.QueryContext(ctx, `SELECT id, run_id, window_id, repo_id, occurred_at, level, event_code, message, attempt
		FROM job_run_events WHERE run_id = ? ORDER BY id LIMIT ?`, runID, limit)
	if err != nil {
		return nil, fmt.Errorf("query job run events: %w", err)
	}
	defer rows.Close()
	var events []RunEvent
	for rows.Next() {
		var item RunEvent
		var occurredAt string
		var windowID, repoID sql.NullInt64
		if err := rows.Scan(&item.ID, &item.RunID, &windowID, &repoID, &occurredAt, &item.Level, &item.EventCode, &item.Message, &item.Attempt); err != nil {
			return nil, fmt.Errorf("scan job run event: %w", err)
		}
		occurred := parseTime(sql.NullString{String: occurredAt, Valid: true})
		if occurred != nil {
			item.OccurredAt = *occurred
		}
		if windowID.Valid {
			value := windowID.Int64
			item.WindowID = &value
		}
		if repoID.Valid {
			value := repoID.Int64
			item.RepoID = &value
		}
		events = append(events, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read job run events: %w", err)
	}
	return events, nil
}

func (service *Service) Run(ctx context.Context, runID int64) (JobRun, error) {
	runs, err := service.JobRunHistory(ctx, 200)
	if err != nil {
		return JobRun{}, err
	}
	for _, item := range runs {
		if item.ID == runID {
			return item, nil
		}
	}
	return JobRun{}, errors.New("job run not found")
}

func parseTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	parsed, err := time.Parse(timestampLayout, value.String)
	if err != nil {
		return nil
	}
	return &parsed
}
