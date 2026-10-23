package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

type adminCategory struct {
	ID          int64   `json:"id"`
	Slug        string  `json:"slug"`
	Title       string  `json:"title"`
	Description *string `json:"description"`
	SortOrder   int     `json:"sort_order"`
	Enabled     bool    `json:"enabled"`
	RepoCount   int     `json:"repo_count"`
}

const adminCategoryQuery = `
	SELECT c.id, c.slug, c.title, c.description, c.sort_order, c.enabled,
		(SELECT COUNT(*) FROM repo_categories rc
		 JOIN repositories vis ON vis.id = rc.repo_id
		 WHERE rc.category_id = c.id AND vis.is_hidden = 0)
	FROM categories c`

func scanAdminCategory(scanner interface{ Scan(...any) error }) (adminCategory, error) {
	var item adminCategory
	var description sql.NullString
	var enabled int
	if err := scanner.Scan(&item.ID, &item.Slug, &item.Title, &description, &item.SortOrder, &enabled, &item.RepoCount); err != nil {
		return adminCategory{}, err
	}
	item.Description = nullStringPtr(description)
	item.Enabled = enabled == 1
	return item, nil
}

func (s *server) listAdminCategories(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	rows, err := s.config.DB.SQL().Query(adminCategoryQuery + ` ORDER BY c.sort_order, c.id`)
	if err != nil {
		s.config.Logger.Error("list admin categories", "request_id", requestID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to load categories")
		return
	}
	defer rows.Close()
	items := []adminCategory{}
	for rows.Next() {
		item, err := scanAdminCategory(rows)
		if err != nil {
			s.config.Logger.Error("read admin categories", "request_id", requestID(r.Context()), "error", err)
			writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to read categories")
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to read categories")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeData(w, http.StatusOK, map[string]any{"items": items})
}

type createCategoryPayload struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	SortOrder   int    `json:"sort_order"`
	Enabled     *bool  `json:"enabled"`
}

func (s *server) createAdminCategory(w http.ResponseWriter, r *http.Request) {
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
	payload, decodeErr := decodeAdminBody[createCategoryPayload](w, r)
	if decodeErr != nil {
		writeError(w, r, decodeErr.status, decodeErr.code, decodeErr.message)
		return
	}
	slug := strings.TrimSpace(payload.Slug)
	title, description, ok := normalizeCategoryFields(w, r, payload.Title, payload.Description)
	if !ok {
		return
	}
	if !validCategorySlug(slug) {
		writeError(w, r, http.StatusBadRequest, "INVALID_CATEGORY", "slug must be 2-64 lowercase letters, digits or hyphens")
		return
	}
	enabled := 1
	if payload.Enabled != nil && !*payload.Enabled {
		enabled = 0
	}
	result, err := s.config.DB.SQL().ExecContext(r.Context(), `INSERT INTO categories (slug, title, description, sort_order, enabled)
		VALUES (?, ?, ?, ?, ?)`, slug, title, description, payload.SortOrder, enabled)
	if err != nil {
		s.audit(r.Context(), &admin.ID, "admin.category.create", slug, "failure")
		if strings.Contains(err.Error(), "UNIQUE constraint failed: categories.slug") {
			writeError(w, r, http.StatusConflict, "CONFLICT", "Category slug already exists")
			return
		}
		s.config.Logger.Error("create category", "request_id", requestID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to create category")
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to create category")
		return
	}
	s.audit(r.Context(), &admin.ID, "admin.category.create", slug, "success")
	item, err := s.readAdminCategory(r.Context(), id)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to read category")
		return
	}
	writeData(w, http.StatusCreated, item)
}

type updateCategoryPayload struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	SortOrder   *int    `json:"sort_order"`
	Enabled     *bool   `json:"enabled"`
}

func (s *server) updateAdminCategory(w http.ResponseWriter, r *http.Request) {
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
	payload, decodeErr := decodeAdminBody[updateCategoryPayload](w, r)
	if decodeErr != nil {
		writeError(w, r, decodeErr.status, decodeErr.code, decodeErr.message)
		return
	}
	if payload.Title == nil && payload.Description == nil && payload.SortOrder == nil && payload.Enabled == nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "at least one field is required")
		return
	}
	current, err := s.readAdminCategory(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Category not found")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to read category")
		return
	}
	title := current.Title
	description := ""
	if current.Description != nil {
		description = *current.Description
	}
	if payload.Title != nil {
		title = *payload.Title
	}
	if payload.Description != nil {
		description = *payload.Description
	}
	normalizedTitle, normalizedDescription, ok := normalizeCategoryFields(w, r, title, description)
	if !ok {
		return
	}
	sortOrder := current.SortOrder
	if payload.SortOrder != nil {
		sortOrder = *payload.SortOrder
	}
	enabled := 0
	if current.Enabled {
		enabled = 1
	}
	if payload.Enabled != nil {
		enabled = 0
		if *payload.Enabled {
			enabled = 1
		}
	}
	if _, err := s.config.DB.SQL().ExecContext(r.Context(), `UPDATE categories
		SET title = ?, description = ?, sort_order = ?, enabled = ? WHERE id = ?`,
		normalizedTitle, normalizedDescription, sortOrder, enabled, id); err != nil {
		s.audit(r.Context(), &admin.ID, "admin.category.update", strconv.FormatInt(id, 10), "failure")
		s.config.Logger.Error("update category", "request_id", requestID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to update category")
		return
	}
	s.audit(r.Context(), &admin.ID, "admin.category.update", strconv.FormatInt(id, 10), "success")
	item, err := s.readAdminCategory(r.Context(), id)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to read category")
		return
	}
	writeData(w, http.StatusOK, item)
}

func (s *server) deleteAdminCategory(w http.ResponseWriter, r *http.Request) {
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
	result, err := s.config.DB.SQL().ExecContext(r.Context(), `DELETE FROM categories WHERE id = ?`, id)
	if err != nil {
		s.audit(r.Context(), &admin.ID, "admin.category.delete", strconv.FormatInt(id, 10), "failure")
		s.config.Logger.Error("delete category", "request_id", requestID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to delete category")
		return
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to delete category")
		return
	}
	if deleted == 0 {
		s.audit(r.Context(), &admin.ID, "admin.category.delete", strconv.FormatInt(id, 10), "failure")
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Category not found")
		return
	}
	s.audit(r.Context(), &admin.ID, "admin.category.delete", strconv.FormatInt(id, 10), "success")
	writeData(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

func (s *server) readAdminCategory(ctx context.Context, id int64) (adminCategory, error) {
	return scanAdminCategory(s.config.DB.SQL().QueryRowContext(ctx, adminCategoryQuery+` WHERE c.id = ?`, id))
}

func normalizeCategoryFields(w http.ResponseWriter, r *http.Request, rawTitle, rawDescription string) (string, any, bool) {
	title := strings.TrimSpace(rawTitle)
	if title == "" || utf8.RuneCountInString(title) > 80 {
		writeError(w, r, http.StatusBadRequest, "INVALID_CATEGORY", "title must be 1-80 characters")
		return "", nil, false
	}
	description := strings.TrimSpace(rawDescription)
	if utf8.RuneCountInString(description) > 500 {
		writeError(w, r, http.StatusBadRequest, "INVALID_CATEGORY", "description must be at most 500 characters")
		return "", nil, false
	}
	var stored any
	if description != "" {
		stored = description
	}
	return title, stored, true
}

func validCategorySlug(slug string) bool {
	if len(slug) < 2 || len(slug) > 64 {
		return false
	}
	for _, char := range slug {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9', char == '-':
		default:
			return false
		}
	}
	return !strings.HasPrefix(slug, "-") && !strings.HasSuffix(slug, "-")
}
