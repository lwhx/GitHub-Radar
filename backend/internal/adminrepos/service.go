package adminrepos

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/github-radar/github-radar/internal/github"
)

var (
	ErrInvalidURL        = errors.New("invalid GitHub repository URL")
	ErrNotPublic         = errors.New("GitHub repository is not public")
	ErrInvalidRepository = errors.New("GitHub repository response is invalid")
	ErrNotFound          = errors.New("repository not found")
	ErrCategoryNotFound  = errors.New("category not found")
	ErrInvalidUpdate     = errors.New("invalid repository update")
	ErrConflict          = errors.New("repository identity conflicts with existing data")
)

var pathSegment = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

const timestampLayout = "2006-01-02T15:04:05Z"

type RepositoryGetter interface {
	GetRepository(context.Context, string, string, string) (github.Repository, github.ResponseMeta, error)
}

type GetterSource func(context.Context) (RepositoryGetter, error)

type Service struct {
	db     *sql.DB
	source GetterSource
	now    func() time.Time
}

type Repository struct {
	ID           int64   `json:"id"`
	GitHubID     int64   `json:"github_id"`
	FullName     string  `json:"full_name"`
	HTMLURL      string  `json:"html_url"`
	Description  *string `json:"description"`
	Language     *string `json:"language"`
	StarsCount   int     `json:"stars_count"`
	Hidden       bool    `json:"hidden"`
	SyncStatus   string  `json:"sync_status"`
	LastSyncedAt *string `json:"last_synced_at"`
	CategoryIDs  []int64 `json:"category_ids"`
}

type ImportResult struct {
	Repository Repository `json:"repository"`
	Created    bool       `json:"created"`
}

type Update struct {
	Hidden      *bool
	CategoryIDs *[]int64
	Resync      bool
}

func New(database *sql.DB, source GetterSource, clocks ...func() time.Time) *Service {
	now := time.Now
	if len(clocks) > 0 && clocks[0] != nil {
		now = clocks[0]
	}
	return &Service{db: database, source: source, now: now}
}

func (service *Service) Import(ctx context.Context, rawURL string) (ImportResult, error) {
	owner, name, err := parseRepositoryURL(rawURL)
	if err != nil {
		return ImportResult{}, err
	}
	if service.source == nil {
		return ImportResult{}, errors.New("repository getter source is unavailable")
	}
	getter, err := service.source(ctx)
	if err != nil {
		return ImportResult{}, fmt.Errorf("select GitHub credential: %w", err)
	}
	repository, meta, err := getter.GetRepository(ctx, owner, name, "")
	if err != nil {
		return ImportResult{}, fmt.Errorf("fetch GitHub repository: %w", err)
	}
	if repository.Private || (repository.Visibility != "" && repository.Visibility != "public") {
		return ImportResult{}, ErrNotPublic
	}
	if err := validateRepository(repository); err != nil {
		return ImportResult{}, err
	}
	return service.storeImport(ctx, repository, meta)
}

func parseRepositoryURL(raw string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Host, "github.com") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", ErrInvalidURL
	}
	parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(parts) != 2 {
		return "", "", ErrInvalidURL
	}
	owner, ownerErr := url.PathUnescape(parts[0])
	name, nameErr := url.PathUnescape(parts[1])
	name = strings.TrimSuffix(name, ".git")
	if ownerErr != nil || nameErr != nil || !pathSegment.MatchString(owner) || !pathSegment.MatchString(name) {
		return "", "", ErrInvalidURL
	}
	return owner, name, nil
}

func validateRepository(repository github.Repository) error {
	if repository.ID <= 0 || repository.Owner.Login == "" || repository.Name == "" || repository.FullName == "" || repository.HTMLURL == "" || repository.CreatedAt.IsZero() || repository.UpdatedAt.IsZero() {
		return ErrInvalidRepository
	}
	owner, name, found := strings.Cut(repository.FullName, "/")
	if !found || !strings.EqualFold(owner, repository.Owner.Login) || !strings.EqualFold(name, repository.Name) {
		return ErrInvalidRepository
	}
	return nil
}

func (service *Service) storeImport(ctx context.Context, repository github.Repository, meta github.ResponseMeta) (ImportResult, error) {
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return ImportResult{}, fmt.Errorf("begin repository import: %w", err)
	}
	defer tx.Rollback()
	now := service.now().UTC().Truncate(time.Second)
	nowText := now.Format(timestampLayout)
	topics, err := json.Marshal(repository.Topics)
	if err != nil {
		return ImportResult{}, fmt.Errorf("encode repository topics: %w", err)
	}
	var existingID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM repositories WHERE github_id = ?`, repository.ID).Scan(&existingID)
	created := false
	switch {
	case errors.Is(err, sql.ErrNoRows):
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO repositories
			(github_id, owner, name, full_name, html_url, homepage_url, description, language, license_spdx,
			 default_branch, topics_json, stars_count, forks_count, open_issues_count, is_fork, is_archived,
			 is_disabled, github_created_at, github_updated_at, pushed_at, first_seen_at, last_synced_at,
			 last_successful_fetch_at, sync_status, visibility, etag, next_sync_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'synced', 'public', ?, ?)`,
			repository.ID, repository.Owner.Login, repository.Name, repository.FullName, repository.HTMLURL,
			nullString(repository.Homepage), nullString(repository.Description), nullString(repository.Language), licenseValue(repository),
			nullText(repository.DefaultBranch), string(topics), repository.StargazersCount, repository.ForksCount,
			repository.OpenIssuesCount, boolInt(repository.Fork), boolInt(repository.Archived), boolInt(repository.Disabled),
			formatTime(repository.CreatedAt), formatTime(repository.UpdatedAt), optionalTime(repository.PushedAt), nowText, nowText,
			nowText, nullText(meta.ETag), formatTime(now.Add(6*time.Hour)))
		if insertErr != nil {
			return ImportResult{}, mapConflict("insert repository", insertErr)
		}
		existingID, err = result.LastInsertId()
		if err != nil {
			return ImportResult{}, fmt.Errorf("read imported repository id: %w", err)
		}
		created = true
	case err != nil:
		return ImportResult{}, fmt.Errorf("find imported repository: %w", err)
	default:
		_, updateErr := tx.ExecContext(ctx, `UPDATE repositories SET
			owner = ?, name = ?, full_name = ?, html_url = ?, homepage_url = ?, description = ?, language = ?,
			license_spdx = ?, default_branch = ?, topics_json = ?, stars_count = ?, forks_count = ?, open_issues_count = ?,
			is_fork = ?, is_archived = ?, is_disabled = ?, github_created_at = ?, github_updated_at = ?, pushed_at = ?,
			last_synced_at = ?, last_successful_fetch_at = ?, sync_status = 'synced', visibility = 'public', etag = ?,
			next_sync_at = ?, last_error_code = NULL, consecutive_failures = 0 WHERE id = ?`,
			repository.Owner.Login, repository.Name, repository.FullName, repository.HTMLURL,
			nullString(repository.Homepage), nullString(repository.Description), nullString(repository.Language), licenseValue(repository),
			nullText(repository.DefaultBranch), string(topics), repository.StargazersCount, repository.ForksCount,
			repository.OpenIssuesCount, boolInt(repository.Fork), boolInt(repository.Archived), boolInt(repository.Disabled),
			formatTime(repository.CreatedAt), formatTime(repository.UpdatedAt), optionalTime(repository.PushedAt), nowText, nowText,
			nullText(meta.ETag), formatTime(now.Add(6*time.Hour)), existingID)
		if updateErr != nil {
			return ImportResult{}, mapConflict("update repository", updateErr)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO star_snapshots
		(repo_id, star_count, captured_at, source, sample_status) VALUES (?, ?, ?, 'manual_import', 'ok')`,
		existingID, repository.StargazersCount, nowText); err != nil {
		return ImportResult{}, fmt.Errorf("record manual import snapshot: %w", err)
	}
	item, err := repositoryByID(ctx, tx, existingID)
	if err != nil {
		return ImportResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ImportResult{}, fmt.Errorf("commit repository import: %w", err)
	}
	return ImportResult{Repository: item, Created: created}, nil
}

func (service *Service) Update(ctx context.Context, repositoryID int64, update Update) (Repository, error) {
	if repositoryID <= 0 || (update.Hidden == nil && update.CategoryIDs == nil && !update.Resync) {
		return Repository{}, ErrInvalidUpdate
	}
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return Repository{}, fmt.Errorf("begin repository update: %w", err)
	}
	defer tx.Rollback()
	var hiddenValue int
	if err := tx.QueryRowContext(ctx, `SELECT is_hidden FROM repositories WHERE id = ?`, repositoryID).Scan(&hiddenValue); errors.Is(err, sql.ErrNoRows) {
		return Repository{}, ErrNotFound
	} else if err != nil {
		return Repository{}, fmt.Errorf("find repository: %w", err)
	}
	effectiveHidden := hiddenValue == 1
	if update.Hidden != nil {
		effectiveHidden = *update.Hidden
	}
	if update.Resync && effectiveHidden {
		return Repository{}, ErrInvalidUpdate
	}
	if update.CategoryIDs != nil {
		for _, categoryID := range *update.CategoryIDs {
			if categoryID <= 0 {
				return Repository{}, ErrInvalidUpdate
			}
		}
		categoryIDs := uniquePositive(*update.CategoryIDs)
		for _, categoryID := range categoryIDs {
			var categoryExists int
			if err := tx.QueryRowContext(ctx, `SELECT 1 FROM categories WHERE id = ?`, categoryID).Scan(&categoryExists); errors.Is(err, sql.ErrNoRows) {
				return Repository{}, ErrCategoryNotFound
			} else if err != nil {
				return Repository{}, fmt.Errorf("find category %d: %w", categoryID, err)
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM repo_categories WHERE repo_id = ?`, repositoryID); err != nil {
			return Repository{}, fmt.Errorf("replace repository categories: %w", err)
		}
		for index, categoryID := range categoryIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO repo_categories(repo_id, category_id, is_primary, source) VALUES (?, ?, ?, 'manual')`, repositoryID, categoryID, boolInt(index == 0)); err != nil {
				return Repository{}, fmt.Errorf("assign repository category: %w", err)
			}
		}
	}
	if update.Hidden != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE repositories SET is_hidden = ? WHERE id = ?`, boolInt(*update.Hidden), repositoryID); err != nil {
			return Repository{}, fmt.Errorf("update repository visibility: %w", err)
		}
	}
	if update.Resync {
		if _, err := tx.ExecContext(ctx, `UPDATE repositories SET sync_status = 'pending', next_sync_at = ?, last_error_code = NULL WHERE id = ?`, formatTime(service.now()), repositoryID); err != nil {
			return Repository{}, fmt.Errorf("queue repository resync: %w", err)
		}
	}
	item, err := repositoryByID(ctx, tx, repositoryID)
	if err != nil {
		return Repository{}, err
	}
	if err := tx.Commit(); err != nil {
		return Repository{}, fmt.Errorf("commit repository update: %w", err)
	}
	return item, nil
}

type ListOptions struct {
	Query    string
	Page     int
	PageSize int
}

type ListResult struct {
	Items    []Repository `json:"items"`
	Total    int64        `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
}

func (service *Service) List(ctx context.Context, options ListOptions) (ListResult, error) {
	if options.Page < 1 || options.PageSize < 1 || options.PageSize > 100 {
		return ListResult{}, ErrInvalidUpdate
	}
	query := strings.TrimSpace(options.Query)
	pattern := "%" + escapeLike(query) + "%"
	where := `(? = '' OR full_name LIKE ? ESCAPE '\' COLLATE NOCASE OR COALESCE(description, '') LIKE ? ESCAPE '\' COLLATE NOCASE)`
	var total int64
	if err := service.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM repositories WHERE `+where, query, pattern, pattern).Scan(&total); err != nil {
		return ListResult{}, fmt.Errorf("count managed repositories: %w", err)
	}
	rows, err := service.db.QueryContext(ctx, `SELECT id, github_id, full_name, html_url, description, language, stars_count,
		is_hidden, sync_status, last_synced_at FROM repositories WHERE `+where+` ORDER BY id DESC LIMIT ? OFFSET ?`,
		query, pattern, pattern, options.PageSize, (options.Page-1)*options.PageSize)
	if err != nil {
		return ListResult{}, fmt.Errorf("query managed repositories: %w", err)
	}
	items := make([]Repository, 0, options.PageSize)
	for rows.Next() {
		item, err := scanRepository(rows)
		if err != nil {
			rows.Close()
			return ListResult{}, fmt.Errorf("scan managed repository: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ListResult{}, fmt.Errorf("read managed repositories: %w", err)
	}
	if err := rows.Close(); err != nil {
		return ListResult{}, fmt.Errorf("close managed repository rows: %w", err)
	}
	if err := service.loadCategories(ctx, items); err != nil {
		return ListResult{}, err
	}
	return ListResult{Items: items, Total: total, Page: options.Page, PageSize: options.PageSize}, nil
}

func (service *Service) loadCategories(ctx context.Context, items []Repository) error {
	if len(items) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(items)), ",")
	arguments := make([]any, len(items))
	indexes := make(map[int64]int, len(items))
	for index := range items {
		arguments[index] = items[index].ID
		indexes[items[index].ID] = index
	}
	rows, err := service.db.QueryContext(ctx, `SELECT repo_id, category_id FROM repo_categories WHERE repo_id IN (`+placeholders+`) ORDER BY repo_id, is_primary DESC, category_id`, arguments...)
	if err != nil {
		return fmt.Errorf("query managed repository categories: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var repositoryID, categoryID int64
		if err := rows.Scan(&repositoryID, &categoryID); err != nil {
			return fmt.Errorf("scan managed repository category: %w", err)
		}
		if index, found := indexes[repositoryID]; found {
			items[index].CategoryIDs = append(items[index].CategoryIDs, categoryID)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read managed repository categories: %w", err)
	}
	return nil
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type rowScanner interface {
	Scan(...any) error
}

func scanRepository(scanner rowScanner) (Repository, error) {
	var item Repository
	var description, language, lastSynced sql.NullString
	var hidden int
	if err := scanner.Scan(
		&item.ID, &item.GitHubID, &item.FullName, &item.HTMLURL, &description, &language, &item.StarsCount,
		&hidden, &item.SyncStatus, &lastSynced); err != nil {
		return Repository{}, err
	}
	item.Description = stringPointer(description)
	item.Language = stringPointer(language)
	item.LastSyncedAt = stringPointer(lastSynced)
	item.Hidden = hidden == 1
	item.CategoryIDs = []int64{}
	return item, nil
}

func repositoryByID(ctx context.Context, query queryRower, repositoryID int64) (Repository, error) {
	item, err := scanRepository(query.QueryRowContext(ctx, `SELECT id, github_id, full_name, html_url, description, language, stars_count,
		is_hidden, sync_status, last_synced_at FROM repositories WHERE id = ?`, repositoryID))
	if errors.Is(err, sql.ErrNoRows) {
		return Repository{}, ErrNotFound
	}
	if err != nil {
		return Repository{}, fmt.Errorf("read repository: %w", err)
	}
	rows, err := query.QueryContext(ctx, `SELECT category_id FROM repo_categories WHERE repo_id = ? ORDER BY is_primary DESC, category_id`, repositoryID)
	if err != nil {
		return Repository{}, fmt.Errorf("query repository categories: %w", err)
	}
	defer rows.Close()
	item.CategoryIDs = []int64{}
	for rows.Next() {
		var categoryID int64
		if err := rows.Scan(&categoryID); err != nil {
			return Repository{}, fmt.Errorf("scan repository category: %w", err)
		}
		item.CategoryIDs = append(item.CategoryIDs, categoryID)
	}
	if err := rows.Err(); err != nil {
		return Repository{}, fmt.Errorf("read repository categories: %w", err)
	}
	return item, nil
}

func uniquePositive(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	result := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, found := seen[value]; found {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func mapConflict(action string, err error) error {
	if strings.Contains(err.Error(), "UNIQUE constraint failed: repositories.full_name") || strings.Contains(err.Error(), "UNIQUE constraint failed: repositories.github_id") {
		return fmt.Errorf("%s: %w", action, ErrConflict)
	}
	return fmt.Errorf("%s: %w", action, err)
}

func nullString(value *string) any {
	if value == nil || *value == "" {
		return nil
	}
	return *value
}

func nullText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func licenseValue(repository github.Repository) any {
	if repository.License == nil || repository.License.SPDXID == "" {
		return nil
	}
	return repository.License.SPDXID
}

func optionalTime(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return formatTime(*value)
}

func formatTime(value time.Time) string {
	return value.UTC().Truncate(time.Second).Format(timestampLayout)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func stringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	copy := value.String
	return &copy
}
