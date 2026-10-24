package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/github-radar/github-radar/internal/adminrepos"
	"github.com/github-radar/github-radar/internal/credentials"
	"github.com/github-radar/github-radar/internal/github"
)

type repositoryAdminService interface {
	List(context.Context, adminrepos.ListOptions) (adminrepos.ListResult, error)
	Import(context.Context, string) (adminrepos.ImportResult, error)
	Update(context.Context, int64, adminrepos.Update) (adminrepos.Repository, error)
}

func (s *server) listAdminRepositories(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	page, ok := positiveQueryInt(r, "page", 1, 100000)
	if !ok {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "page must be a positive integer")
		return
	}
	pageSize, ok := positiveQueryInt(r, "page_size", 20, 100)
	if !ok {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "page_size must be between 1 and 100")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if utf8.RuneCountInString(query) > 200 {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "q must be at most 200 characters")
		return
	}
	if s.config.Repositories == nil {
		writeError(w, r, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Repository management is unavailable")
		return
	}
	result, err := s.config.Repositories.List(r.Context(), adminrepos.ListOptions{Query: query, Page: page, PageSize: pageSize})
	if err != nil {
		s.config.Logger.Error("list managed repositories", "request_id", requestID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to load repositories")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeData(w, http.StatusOK, result)
}

func positiveQueryInt(r *http.Request, key string, fallback, maximum int) (int, bool) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	return value, err == nil && value >= 1 && value <= maximum
}

type importRepositoryPayload struct {
	URL string `json:"url"`
}

func (s *server) importAdminRepository(w http.ResponseWriter, r *http.Request) {
	if !s.validOrigin(r) {
		writeError(w, r, http.StatusForbidden, "INVALID_ORIGIN", "Request origin is not allowed")
		return
	}
	admin, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if !s.requireCSRF(w, r) {
		return
	}
	payload, decodeErr := decodeAdminBody[importRepositoryPayload](w, r)
	if decodeErr != nil {
		writeError(w, r, decodeErr.status, decodeErr.code, decodeErr.message)
		return
	}
	target := strings.TrimSpace(payload.URL)
	auditTarget := "repository-import"
	if target == "" || utf8.RuneCountInString(target) > 500 {
		s.audit(r.Context(), &admin.ID, "admin.repository.import", auditTarget, "failure")
		writeError(w, r, http.StatusBadRequest, "INVALID_REPOSITORY_URL", "A valid GitHub repository URL is required")
		return
	}
	if s.config.Repositories == nil {
		s.audit(r.Context(), &admin.ID, "admin.repository.import", auditTarget, "failure")
		writeError(w, r, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Repository management is unavailable")
		return
	}
	result, err := s.config.Repositories.Import(r.Context(), target)
	if err != nil {
		s.audit(r.Context(), &admin.ID, "admin.repository.import", auditTarget, "failure")
		s.writeRepositoryError(w, r, err)
		return
	}
	s.audit(r.Context(), &admin.ID, "admin.repository.import", result.Repository.FullName, "success")
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeData(w, status, result)
}

type updateRepositoryPayload struct {
	Hidden      *bool    `json:"hidden"`
	CategoryIDs *[]int64 `json:"category_ids"`
	Resync      bool     `json:"resync"`
}

func (s *server) updateAdminRepository(w http.ResponseWriter, r *http.Request) {
	if !s.validOrigin(r) {
		writeError(w, r, http.StatusForbidden, "INVALID_ORIGIN", "Request origin is not allowed")
		return
	}
	admin, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if !s.requireCSRF(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	payload, decodeErr := decodeAdminBody[updateRepositoryPayload](w, r)
	if decodeErr != nil {
		writeError(w, r, decodeErr.status, decodeErr.code, decodeErr.message)
		return
	}
	if payload.Hidden == nil && payload.CategoryIDs == nil && !payload.Resync {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "at least one field is required")
		return
	}
	if payload.CategoryIDs != nil && len(*payload.CategoryIDs) > 50 {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "category_ids must contain at most 50 entries")
		return
	}
	target := strconv.FormatInt(id, 10)
	if s.config.Repositories == nil {
		s.audit(r.Context(), &admin.ID, "admin.repository.update", target, "failure")
		writeError(w, r, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Repository management is unavailable")
		return
	}
	item, err := s.config.Repositories.Update(r.Context(), id, adminrepos.Update{Hidden: payload.Hidden, CategoryIDs: payload.CategoryIDs, Resync: payload.Resync})
	if err != nil {
		s.audit(r.Context(), &admin.ID, "admin.repository.update", target, "failure")
		s.writeRepositoryError(w, r, err)
		return
	}
	s.audit(r.Context(), &admin.ID, "admin.repository.update", item.FullName, "success")
	writeData(w, http.StatusOK, item)
}

func (s *server) writeRepositoryError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, adminrepos.ErrInvalidURL):
		writeError(w, r, http.StatusBadRequest, "INVALID_REPOSITORY_URL", "A valid GitHub repository URL is required")
	case errors.Is(err, adminrepos.ErrInvalidUpdate), errors.Is(err, adminrepos.ErrCategoryNotFound):
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Repository update is invalid")
	case errors.Is(err, adminrepos.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Repository not found")
	case errors.Is(err, adminrepos.ErrNotPublic):
		writeError(w, r, http.StatusUnprocessableEntity, "REPOSITORY_NOT_PUBLIC", "Only public GitHub repositories can be imported")
	case errors.Is(err, adminrepos.ErrConflict):
		writeError(w, r, http.StatusConflict, "CONFLICT", "Repository conflicts with an existing record")
	case errors.Is(err, credentials.ErrNoCredential):
		writeError(w, r, http.StatusServiceUnavailable, "NO_GITHUB_CREDENTIAL", "No usable GitHub credential is configured")
	default:
		var limited *credentials.RateLimitedError
		if errors.As(err, &limited) {
			writeError(w, r, http.StatusTooManyRequests, "GITHUB_RATE_LIMITED", "GitHub API quota is temporarily unavailable")
			return
		}
		var apiError *github.APIError
		if errors.As(err, &apiError) {
			if apiError.Kind == github.ErrorNotFound || apiError.Kind == github.ErrorGone {
				writeError(w, r, http.StatusNotFound, "GITHUB_REPOSITORY_NOT_FOUND", "GitHub repository was not found")
				return
			}
			writeError(w, r, http.StatusBadGateway, "GITHUB_API_ERROR", "GitHub API request failed")
			return
		}
		if errors.Is(err, adminrepos.ErrInvalidRepository) {
			writeError(w, r, http.StatusBadGateway, "INVALID_GITHUB_RESPONSE", "GitHub returned an invalid repository")
			return
		}
		s.config.Logger.Error("manage repository", "request_id", requestID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to manage repository")
	}
}
