package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/github-radar/github-radar/internal/backups"
	"github.com/go-chi/chi/v5"
)

type backupService interface {
	Create(context.Context, backups.Kind) (backups.Backup, error)
	List(context.Context) ([]backups.Backup, error)
	Open(string) (string, error)
	Verify(context.Context, string) (bool, error)
}

func (s *server) listBackups(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.config.Backups == nil {
		writeError(w, r, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Backup service is not configured")
		return
	}
	items, err := s.config.Backups.List(r.Context())
	if err != nil {
		s.config.Logger.Error("list backups", "request_id", requestID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to list backups")
		return
	}
	for index := range items {
		items[index].DownloadURL = "/api/v1/admin/backups/" + url.PathEscape(items[index].Name)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeData(w, http.StatusOK, map[string]any{
		"items":     items,
		"retention": map[string]int{"daily": 7, "weekly": 4},
	})
}

func (s *server) createBackup(w http.ResponseWriter, r *http.Request) {
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
	if s.config.Backups == nil {
		writeError(w, r, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Backup service is not configured")
		return
	}
	item, err := s.config.Backups.Create(r.Context(), backups.KindManual)
	if err != nil {
		s.audit(r.Context(), &admin.ID, "admin.backup.create", "database", "failure")
		s.config.Logger.Error("create backup", "request_id", requestID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "BACKUP_FAILED", "Unable to create backup")
		return
	}
	s.audit(r.Context(), &admin.ID, "admin.backup.create", item.Name, "success")
	item.DownloadURL = "/api/v1/admin/backups/" + url.PathEscape(item.Name)
	writeData(w, http.StatusCreated, item)
}

func (s *server) verifyBackup(w http.ResponseWriter, r *http.Request) {
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
	if s.config.Backups == nil {
		writeError(w, r, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Backup service is not configured")
		return
	}
	name := chi.URLParam(r, "name")
	verified, err := s.config.Backups.Verify(r.Context(), name)
	if err != nil {
		result := "failure"
		s.audit(r.Context(), &admin.ID, "admin.backup.verify", name, result)
		if errors.Is(err, backups.ErrInvalidName) {
			writeError(w, r, http.StatusBadRequest, "INVALID_BACKUP_NAME", "Invalid backup name")
			return
		}
		if errors.Is(err, backups.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "BACKUP_NOT_FOUND", "Backup not found")
			return
		}
		writeData(w, http.StatusUnprocessableEntity, map[string]any{"name": name, "verified": false})
		return
	}
	s.audit(r.Context(), &admin.ID, "admin.backup.verify", name, "success")
	writeData(w, http.StatusOK, map[string]any{"name": name, "verified": verified})
}

func (s *server) downloadBackup(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.config.Backups == nil {
		writeError(w, r, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Backup service is not configured")
		return
	}
	name := chi.URLParam(r, "name")
	path, err := s.config.Backups.Open(name)
	if err != nil {
		if errors.Is(err, backups.ErrInvalidName) {
			writeError(w, r, http.StatusBadRequest, "INVALID_BACKUP_NAME", "Invalid backup name")
			return
		}
		if errors.Is(err, backups.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "BACKUP_NOT_FOUND", "Backup not found")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to open backup")
		return
	}
	file, err := os.Open(path)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to open backup")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to read backup")
		return
	}
	if filepath.Base(path) != name || !info.Mode().IsRegular() {
		writeError(w, r, http.StatusBadRequest, "INVALID_BACKUP_NAME", "Invalid backup name")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, name, info.ModTime().UTC().Truncate(time.Second), file)
}
