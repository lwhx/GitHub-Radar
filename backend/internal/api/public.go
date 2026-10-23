package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

const (
	reposDefaultLimit = 20
	reposMaximumLimit = 100
)

var repoSortOptions = map[string]string{
	"":             "stars_count DESC, id DESC",
	"stars_desc":   "stars_count DESC, id DESC",
	"stars_asc":    "stars_count ASC, id ASC",
	"created_desc": "github_created_at DESC, id DESC",
	"updated_desc": "github_updated_at DESC, id DESC",
}

type publicRepo struct {
	FullName        string   `json:"full_name"`
	Owner           string   `json:"owner"`
	Name            string   `json:"name"`
	HtmlURL         string   `json:"html_url"`
	HomepageURL     *string  `json:"homepage_url"`
	Description     *string  `json:"description"`
	Language        *string  `json:"language"`
	LicenseSPDX     *string  `json:"license_spdx"`
	Topics          []string `json:"topics"`
	StarsCount      int      `json:"stars_count"`
	ForksCount      int      `json:"forks_count"`
	OpenIssuesCount int      `json:"open_issues_count"`
	IsFork          bool     `json:"is_fork"`
	IsArchived      bool     `json:"is_archived"`
	GithubCreatedAt string   `json:"github_created_at"`
	GithubUpdatedAt string   `json:"github_updated_at"`
	PushedAt        *string  `json:"pushed_at"`
	FirstSeenAt     string   `json:"first_seen_at"`
	LastSyncedAt    *string  `json:"last_synced_at"`
}

const publicRepoColumns = `full_name, owner, name, html_url, homepage_url, description, language, license_spdx,
	topics_json, stars_count, forks_count, open_issues_count, is_fork, is_archived,
	github_created_at, github_updated_at, pushed_at, first_seen_at, last_synced_at`

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	text := value.String
	return &text
}

func scanPublicRepo(scanner interface{ Scan(dest ...any) error }) (publicRepo, error) {
	var repo publicRepo
	var topicsJSON string
	var homepage, description, language, license, pushedAt, lastSynced sql.NullString
	var isFork, isArchived int
	if err := scanner.Scan(&repo.FullName, &repo.Owner, &repo.Name, &repo.HtmlURL, &homepage, &description,
		&language, &license, &topicsJSON, &repo.StarsCount, &repo.ForksCount, &repo.OpenIssuesCount,
		&isFork, &isArchived, &repo.GithubCreatedAt, &repo.GithubUpdatedAt, &pushedAt, &repo.FirstSeenAt, &lastSynced); err != nil {
		return publicRepo{}, err
	}
	repo.HomepageURL = nullStringPtr(homepage)
	repo.Description = nullStringPtr(description)
	repo.Language = nullStringPtr(language)
	repo.LicenseSPDX = nullStringPtr(license)
	repo.PushedAt = nullStringPtr(pushedAt)
	repo.LastSyncedAt = nullStringPtr(lastSynced)
	repo.IsFork = isFork == 1
	repo.IsArchived = isArchived == 1
	repo.Topics = []string{}
	if err := json.Unmarshal([]byte(topicsJSON), &repo.Topics); err != nil {
		repo.Topics = []string{}
	}
	return repo, nil
}

func parsePagination(w http.ResponseWriter, r *http.Request) (page, limit int, ok bool) {
	page = 1
	limit = reposDefaultLimit
	if raw := r.URL.Query().Get("page"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			writeError(w, r, http.StatusBadRequest, "invalid_page", "page must be a positive integer")
			return 0, 0, false
		}
		page = value
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > reposMaximumLimit {
			writeError(w, r, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 100")
			return 0, 0, false
		}
		limit = value
	}
	return page, limit, true
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

func (s *server) getRepo(w http.ResponseWriter, r *http.Request) {
	owner := chi.URLParam(r, "owner")
	name := chi.URLParam(r, "repo")
	if owner == "" || name == "" {
		writeError(w, r, http.StatusNotFound, "repo_not_found", "repository not found")
		return
	}
	row := s.config.DB.SQL().QueryRow(`SELECT `+publicRepoColumns+` FROM repositories
		WHERE owner = ?1 COLLATE NOCASE AND name = ?2 COLLATE NOCASE AND is_hidden = 0`, owner, name)
	repo, err := scanPublicRepo(row)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "repo_not_found", "repository not found")
		return
	}
	writeData(w, http.StatusOK, repo)
}

func (s *server) listRepos(w http.ResponseWriter, r *http.Request) {
	page, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}
	orderBy, knownSort := repoSortOptions[r.URL.Query().Get("sort")]
	if !knownSort {
		writeError(w, r, http.StatusBadRequest, "unsupported_sort", "sort must be one of: stars_desc, stars_asc, created_desc, updated_desc")
		return
	}
	store := s.config.DB.SQL()
	where := "is_hidden = 0"
	args := []any{}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		pattern := "%" + escapeLike(q) + "%"
		where += " AND (full_name LIKE ? ESCAPE '\\' OR description LIKE ? ESCAPE '\\')"
		args = append(args, pattern, pattern)
	}
	var total int
	if err := store.QueryRow("SELECT COUNT(*) FROM repositories WHERE "+where, args...).Scan(&total); err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "failed to count repositories")
		return
	}
	offset := (page - 1) * limit
	rows, err := store.Query(`SELECT `+publicRepoColumns+` FROM repositories WHERE `+where+
		" ORDER BY "+orderBy+" LIMIT ? OFFSET ?", append(args, limit, offset)...)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "failed to list repositories")
		return
	}
	defer rows.Close()
	items := []publicRepo{}
	for rows.Next() {
		repo, err := scanPublicRepo(rows)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "internal_error", "failed to read repositories")
			return
		}
		items = append(items, repo)
	}
	if err := rows.Err(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "failed to read repositories")
		return
	}
	var dataUpdatedAt sql.NullString
	_ = store.QueryRow("SELECT MAX(last_synced_at) FROM repositories WHERE is_hidden = 0").Scan(&dataUpdatedAt)
	writeData(w, http.StatusOK, map[string]any{
		"items": items,
		"meta": map[string]any{
			"total":           total,
			"page":            page,
			"limit":           limit,
			"data_updated_at": nullStringPtr(dataUpdatedAt),
		},
	})
}
