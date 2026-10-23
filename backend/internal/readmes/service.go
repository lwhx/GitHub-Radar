package readmes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/github-radar/github-radar/internal/github"
)

// CacheTTL 是 README 本地副本的有效期；过期后按需重新拉取。
const CacheTTL = 24 * time.Hour

// ErrUpstream 表示上游 GitHub 不可用且本地没有可降级的副本。
var ErrUpstream = errors.New("readme upstream unavailable")

// Provider 拉取仓库 README 原文。
type Provider interface {
	Readme(ctx context.Context, owner string, name string) (github.Readme, error)
}

// Document 是缓存与接口共用的 README 快照。
type Document struct {
	Status    string
	Content   string
	MediaType string
	FetchedAt time.Time
	Cached    bool
	Stale     bool
	Truncated bool
}

// Service 负责 README 的缓存、净化与陈旧降级。
type Service struct {
	db       *sql.DB
	provider Provider
	now      func() time.Time
	logger   *slog.Logger
}

func NewService(database *sql.DB, provider Provider, now func() time.Time, logger *slog.Logger) *Service {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{db: database, provider: provider, now: now, logger: logger}
}

// Get 返回有效期内的缓存；需要刷新时拉取 GitHub，失败则降级到陈旧副本。
func (service *Service) Get(ctx context.Context, repoID int64, owner string, name string) (Document, error) {
	cached, found, err := service.load(ctx, repoID)
	if err != nil {
		return Document{}, err
	}
	if found && service.now().UTC().Sub(cached.FetchedAt) < CacheTTL {
		cached.Cached = true
		return cached, nil
	}
	readme, err := service.provider.Readme(ctx, owner, name)
	if err != nil {
		if isNotFound(err) {
			document := Document{Status: "missing", FetchedAt: service.now().UTC()}
			if storeErr := service.store(ctx, repoID, document); storeErr != nil {
				service.logger.Warn("cache missing readme", "repo_id", repoID, "error", storeErr)
			}
			return document, nil
		}
		if found && cached.Status == "ok" {
			cached.Cached = true
			cached.Stale = true
			return cached, nil
		}
		return Document{}, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	content, truncated := Sanitize(readme.Content)
	document := Document{Status: "ok", Content: content, MediaType: readme.MediaType, FetchedAt: service.now().UTC(), Truncated: truncated}
	if err := service.store(ctx, repoID, document); err != nil {
		return Document{}, err
	}
	return document, nil
}

func (service *Service) load(ctx context.Context, repoID int64) (Document, bool, error) {
	var document Document
	var content, mediaType sql.NullString
	var fetchedAt string
	err := service.db.QueryRowContext(ctx, `SELECT status, content, media_type, fetched_at FROM readme_cache WHERE repo_id = ?`, repoID).
		Scan(&document.Status, &content, &mediaType, &fetchedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Document{}, false, nil
	}
	if err != nil {
		return Document{}, false, fmt.Errorf("load readme cache: %w", err)
	}
	document.Content = content.String
	document.MediaType = mediaType.String
	parsed, err := time.Parse(time.RFC3339, fetchedAt)
	if err != nil {
		return Document{}, false, fmt.Errorf("parse readme fetched_at: %w", err)
	}
	document.FetchedAt = parsed
	return document, true, nil
}

func (service *Service) store(ctx context.Context, repoID int64, document Document) error {
	var content any
	if document.Status == "ok" {
		content = document.Content
	}
	var mediaType any
	if document.MediaType != "" {
		mediaType = document.MediaType
	}
	_, err := service.db.ExecContext(ctx, `INSERT INTO readme_cache (repo_id, status, content, media_type, fetched_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(repo_id) DO UPDATE SET status = excluded.status, content = excluded.content,
			media_type = excluded.media_type, fetched_at = excluded.fetched_at`,
		repoID, document.Status, content, mediaType, document.FetchedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("store readme cache: %w", err)
	}
	return nil
}

func isNotFound(err error) bool {
	var apiErr *github.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Kind == github.ErrorNotFound
	}
	return false
}
