package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/github-radar/github-radar/internal/settings"
)

// settingsService 提供站点设置的读写（密钥永不回传）。
type settingsService interface {
	Get(ctx context.Context) (settings.Snapshot, error)
	Update(ctx context.Context, update settings.Update) (settings.Snapshot, error)
}

func (s *server) getAdminSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.config.Settings == nil {
		writeError(w, r, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Settings service is not configured")
		return
	}
	snapshot, err := s.config.Settings.Get(r.Context())
	if err != nil {
		s.config.Logger.Error("load settings", "request_id", requestID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to load settings")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeData(w, http.StatusOK, snapshot)
}

type updateSettingsPayload struct {
	SiteName        *string `json:"site_name"`
	SiteDescription *string `json:"site_description"`
	Timezone        *string `json:"timezone"`
	DefaultPageSize *int    `json:"default_page_size"`
	AIEnabled       *bool   `json:"ai_enabled"`
	AIBaseURL       *string `json:"ai_base_url"`
	AIModel         *string `json:"ai_model"`
	AIAPIKey        *string `json:"ai_api_key"`
}

func (s *server) updateAdminSettings(w http.ResponseWriter, r *http.Request) {
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
	if s.config.Settings == nil {
		writeError(w, r, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Settings service is not configured")
		return
	}
	payload, decodeErr := decodeAdminBody[updateSettingsPayload](w, r)
	if decodeErr != nil {
		writeError(w, r, decodeErr.status, decodeErr.code, decodeErr.message)
		return
	}
	if payload.SiteName == nil && payload.SiteDescription == nil && payload.Timezone == nil &&
		payload.DefaultPageSize == nil && payload.AIEnabled == nil && payload.AIBaseURL == nil &&
		payload.AIModel == nil && payload.AIAPIKey == nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "at least one field is required")
		return
	}
	snapshot, err := s.config.Settings.Update(r.Context(), settings.Update{
		SiteName:        payload.SiteName,
		SiteDescription: payload.SiteDescription,
		Timezone:        payload.Timezone,
		DefaultPageSize: payload.DefaultPageSize,
		AIEnabled:       payload.AIEnabled,
		AIBaseURL:       payload.AIBaseURL,
		AIModel:         payload.AIModel,
		AIAPIKey:        payload.AIAPIKey,
	})
	if err != nil {
		s.audit(r.Context(), &admin.ID, "admin.settings.update", "site", "failure")
		if errors.Is(err, settings.ErrInvalidSettings) {
			writeError(w, r, http.StatusBadRequest, "INVALID_SETTINGS", err.Error())
			return
		}
		s.config.Logger.Error("update settings", "request_id", requestID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to update settings")
		return
	}
	s.audit(r.Context(), &admin.ID, "admin.settings.update", "site", "success")
	writeData(w, http.StatusOK, snapshot)
}

type auditLogEntry struct {
	ID        int64   `json:"id"`
	AdminID   *int64  `json:"admin_id"`
	Action    string  `json:"action"`
	Target    string  `json:"target"`
	Result    string  `json:"result"`
	CreatedAt string  `json:"created_at"`
	RequestID *string `json:"request_id"`
}

func (s *server) listAuditLogs(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 200 {
			writeError(w, r, http.StatusBadRequest, "INVALID_LIMIT", "limit must be an integer between 1 and 200")
			return
		}
		limit = parsed
	}
	action := r.URL.Query().Get("action")

	query := `SELECT id, admin_id, action, target, result, created_at, request_id FROM audit_logs`
	var args []any
	if action != "" {
		query += ` WHERE action = ?`
		args = append(args, action)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.config.DB.SQL().QueryContext(r.Context(), query, args...)
	if err != nil {
		s.config.Logger.Error("list audit logs", "request_id", requestID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to load audit logs")
		return
	}
	defer rows.Close()
	items := []auditLogEntry{}
	for rows.Next() {
		var item auditLogEntry
		var adminID sql.NullInt64
		var requestID sql.NullString
		if err := rows.Scan(&item.ID, &adminID, &item.Action, &item.Target, &item.Result, &item.CreatedAt, &requestID); err != nil {
			writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to read audit logs")
			return
		}
		if adminID.Valid {
			value := adminID.Int64
			item.AdminID = &value
		}
		if requestID.Valid {
			value := requestID.String
			item.RequestID = &value
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to read audit logs")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeData(w, http.StatusOK, map[string]any{"items": items})
}
