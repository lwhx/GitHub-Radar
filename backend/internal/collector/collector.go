package collector

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/github-radar/github-radar/internal/classify"
	"github.com/github-radar/github-radar/internal/github"
)

const (
	windowSource         = "github_search"
	windowQueryHash      = "created-public-v1"
	recoveryWindowSource = "github_recovery"
	recoveryWindowHash   = "created-public-recovery-v1"
	maxSearchPages       = 10
	maxWindowAttempts    = 3
	windowRetryBase      = 5 * time.Minute
	windowRetryMaximum   = time.Hour

	// defaultMaxResults keeps a window under GitHub's 1,000-result search cap.
	defaultMaxResults = 900
	// defaultMaxSplitDepth is a safety net only: real GitHub creates ~9k public
	// repositories per hour, so bisection must continue below depth six. The
	// terminating condition is the one-minute width floor in window.splittable;
	// a 7-day window needs at most depth 14 to reach it, 20 leaves margin.
	defaultMaxSplitDepth = 20
)

// Searcher is the subset of the GitHub client the collector needs.
type Searcher interface {
	Search(ctx context.Context, options github.SearchOptions) (github.SearchResult, github.ResponseMeta, error)
	SearchNext(ctx context.Context, nextURL string) (github.SearchResult, github.ResponseMeta, error)
}

type Collector struct {
	db         *sql.DB
	search     Searcher
	now        func() time.Time
	maxResults int
	maxDepth   int
	maxPages   int
	source     string
	queryHash  string

	logger *slog.Logger
}

type Summary struct {
	Windows           int
	Pages             int
	Created           int
	Updated           int
	Skipped           int
	Snapshots         int
	SplitWindows      int
	IncompleteWindows int
	FailedWindows     int
}

func (summary *Summary) add(other Summary) {
	summary.Windows += other.Windows
	summary.Pages += other.Pages
	summary.Created += other.Created
	summary.Updated += other.Updated
	summary.Skipped += other.Skipped
	summary.Snapshots += other.Snapshots
	summary.SplitWindows += other.SplitWindows
	summary.IncompleteWindows += other.IncompleteWindows
	summary.FailedWindows += other.FailedWindows
}

func New(database *sql.DB, search Searcher) *Collector {
	return newCollector(database, search, time.Now, defaultMaxResults, defaultMaxSplitDepth)
}

// NewRecovery creates a collector whose coverage windows are isolated from the incremental cursor.
func NewRecovery(database *sql.DB, search Searcher) *Collector {
	collector := newCollector(database, search, time.Now, defaultMaxResults, defaultMaxSplitDepth)
	collector.source = recoveryWindowSource
	collector.queryHash = recoveryWindowHash
	return collector
}

func newCollector(database *sql.DB, search Searcher, now func() time.Time, maxResults int, maxDepth int) *Collector {
	return &Collector{
		db: database, search: search, now: now, maxResults: maxResults, maxDepth: maxDepth,
		maxPages: maxSearchPages, source: windowSource, queryHash: windowQueryHash,
	}
}

// log 返回采集器日志器；未注入时退回全局默认日志器。
func (collector *Collector) log() *slog.Logger {
	if collector.logger != nil {
		return collector.logger
	}
	return slog.Default()
}

// classifyStored 每批重新加载规则并一次性归类全部仓库。
// 错误必须返回给页面状态机，确保游标不推进、下次可重试同一批仓库。
func (collector *Collector) classifyStored(ctx context.Context, repoIDs []int64) error {
	if len(repoIDs) == 0 {
		return nil
	}
	engine, err := classify.LoadEngine(ctx, collector.db)
	if err != nil {
		collector.log().Error("load classify rules", "error", err)
		return fmt.Errorf("load classify rules: %w", err)
	}
	if err := classify.ClassifyMany(ctx, collector.db, engine, repoIDs); err != nil {
		collector.log().Warn("classify repositories", "repo_count", len(repoIDs), "error", err)
		return fmt.Errorf("classify repositories: %w", err)
	}
	return nil
}

// ProcessPending drains every pending window belonging to runID (0 means unassigned) in time order.
func (collector *Collector) ProcessPending(ctx context.Context, runID int64) (Summary, error) {
	var total Summary
	seen := make(map[int64]bool)
	for {
		windowID, found, err := collector.nextPendingWindow(ctx, runID)
		if err != nil {
			return total, err
		}
		if !found || seen[windowID] {
			return total, nil
		}
		seen[windowID] = true
		summary, err := collector.ProcessWindow(ctx, windowID)
		total.add(summary)
		if err != nil {
			return total, err
		}
	}
}

func (collector *Collector) ProcessWindow(ctx context.Context, windowID int64) (Summary, error) {
	window, err := collector.loadWindow(ctx, windowID)
	if err != nil {
		return Summary{}, err
	}
	if window.Status == statusCompleted || window.Status == statusSplit || window.Status == statusIncomplete {
		return Summary{}, nil
	}
	summary := Summary{Windows: 1}
	nextURL := window.NextURL
	page := 1
	if nextURL != "" {
		page = 2
	}
	for {
		if page > collector.maxPages {
			if err := collector.markIncomplete(ctx, windowID, "search page limit reached"); err != nil {
				return summary, err
			}
			summary.IncompleteWindows++
			return summary, nil
		}
		var result github.SearchResult
		var meta github.ResponseMeta
		if nextURL == "" {
			result, meta, err = collector.search.Search(ctx, github.SearchOptions{From: window.From, To: window.To, Page: page})
		} else {
			result, meta, err = collector.search.SearchNext(ctx, nextURL)
		}
		summary.Pages++
		if err != nil {
			if markErr := collector.markAttemptFailed(ctx, windowID, err); markErr != nil {
				return summary, errors.Join(err, markErr)
			}
			summary.FailedWindows++
			return summary, fmt.Errorf("collect window %d: %w", windowID, err)
		}
		isFirstPage := page == 1
		if isFirstPage && (result.TotalCount > collector.maxResults || result.IncompleteResults) {
			if window.Depth >= collector.maxDepth || !window.splittable() {
				if err := collector.markIncomplete(ctx, windowID, "window cannot be split further"); err != nil {
					return summary, err
				}
				summary.IncompleteWindows++
				return summary, nil
			}
			if err := collector.splitWindow(ctx, window, result); err != nil {
				return summary, err
			}
			summary.SplitWindows++
			return summary, nil
		}
		pageSummary, err := collector.storePage(ctx, windowID, window, result, meta.NextURL, isFirstPage, page)
		summary.Created += pageSummary.Created
		summary.Updated += pageSummary.Updated
		summary.Skipped += pageSummary.Skipped
		summary.Snapshots += pageSummary.Snapshots
		if err != nil {
			return summary, err
		}
		if meta.NextURL == "" {
			if err := collector.markCompleted(ctx, windowID); err != nil {
				return summary, err
			}
			return summary, nil
		}
		nextURL = meta.NextURL
		page++
	}
}

type window struct {
	ID       int64
	RunID    int64
	From     time.Time
	To       time.Time
	Depth    int
	Status   string
	NextURL  string
	Attempts int
}

func (item window) splittable() bool {
	return item.To.Sub(item.From) > time.Minute
}

func (collector *Collector) loadWindow(ctx context.Context, windowID int64) (window, error) {
	var item window
	var from, to string
	var nextURL sql.NullString
	var runID sql.NullInt64
	err := collector.db.QueryRowContext(ctx, `SELECT id, job_run_id, time_from, time_to, split_depth, coverage_status, next_url, attempt_count
		FROM collection_windows WHERE id = ?`, windowID).Scan(&item.ID, &runID, &from, &to, &item.Depth, &item.Status, &nextURL, &item.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return window{}, fmt.Errorf("collection window %d does not exist", windowID)
	}
	if err != nil {
		return window{}, fmt.Errorf("load collection window %d: %w", windowID, err)
	}
	item.From, err = time.Parse(time.RFC3339, from)
	if err != nil {
		return window{}, fmt.Errorf("parse collection window %d start: %w", windowID, err)
	}
	item.To, err = time.Parse(time.RFC3339, to)
	if err != nil {
		return window{}, fmt.Errorf("parse collection window %d end: %w", windowID, err)
	}
	item.NextURL = nextURL.String
	item.RunID = runID.Int64
	return item, nil
}

func (collector *Collector) nextPendingWindow(ctx context.Context, runID int64) (int64, bool, error) {
	var windowID int64
	var err error
	if runID > 0 {
		err = collector.db.QueryRowContext(ctx, `UPDATE collection_windows
			SET job_run_id = ?, updated_at = ?
			WHERE id = (
				SELECT id FROM collection_windows
				WHERE source = ? AND query_hash = ? AND coverage_status = 'pending' AND attempt_count < 3
					AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
				ORDER BY time_from, id LIMIT 1
			)
			RETURNING id`, runID, formatTime(collector.now()), collector.source, collector.queryHash, formatTime(collector.now())).Scan(&windowID)
	} else {
		err = collector.db.QueryRowContext(ctx, `SELECT id FROM collection_windows
			WHERE source = ? AND query_hash = ? AND coverage_status = 'pending' AND attempt_count < 3
				AND job_run_id IS NULL AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
			ORDER BY time_from, id LIMIT 1`, collector.source, collector.queryHash, formatTime(collector.now())).Scan(&windowID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("select pending collection window: %w", err)
	}
	return windowID, true, nil
}
