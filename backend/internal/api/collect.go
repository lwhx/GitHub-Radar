package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/github-radar/github-radar/internal/auth"
	"github.com/github-radar/github-radar/internal/credentials"
	"github.com/github-radar/github-radar/internal/scheduler"
)

const adminBodyLimit = 8 * 1024

// credentialService is the credential management surface used by the admin API.
type credentialService interface {
	List(context.Context) ([]credentials.CredentialDetail, error)
	Create(context.Context, credentials.CreateInput) (credentials.Credential, error)
	SetEnabled(context.Context, int64, bool) error
	Usable(context.Context, string) (credentials.UsableCredential, error)
	Test(context.Context, int64) (credentials.Credential, error)
	Delete(context.Context, int64) error
}

// schedulerService is the collection job surface used by the admin API.
type schedulerService interface {
	EnsureJob(context.Context) error
	Precheck(context.Context) error
	Jobs(context.Context) ([]scheduler.Job, error)
	RunOnce(context.Context, string) (scheduler.Run, error)
	JobRunHistory(context.Context, int) ([]scheduler.JobRun, error)
	RunEvents(context.Context, int64, int) ([]scheduler.RunEvent, error)
	Run(context.Context, int64) (scheduler.JobRun, error)
	SetJobPolicy(context.Context, int64, *bool, *int) (scheduler.Job, error)
}

func (s *server) requireAdmin(w http.ResponseWriter, r *http.Request) (auth.Admin, bool) {
	cookie, err := r.Cookie("radar_admin_session")
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required")
		return auth.Admin{}, false
	}
	admin, err := s.config.Auth.Authenticate(r.Context(), cookie.Value)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required")
		return auth.Admin{}, false
	}
	return admin, true
}

func (s *server) requireCSRF(w http.ResponseWriter, r *http.Request) bool {
	csrfCookie, err := r.Cookie("radar_csrf")
	headerToken := r.Header.Get("X-CSRF-Token")
	if err != nil || headerToken == "" || !constantTimeTokenEqual(headerToken, csrfCookie.Value) {
		writeError(w, r, http.StatusForbidden, "INVALID_CSRF", "CSRF validation failed")
		return false
	}
	if err := s.config.Auth.VerifyCSRF(r.Context(), sessionCookieValue(r), headerToken); err != nil {
		writeError(w, r, http.StatusForbidden, "INVALID_CSRF", "CSRF validation failed")
		return false
	}
	return true
}

func sessionCookieValue(r *http.Request) string {
	cookie, err := r.Cookie("radar_admin_session")
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (s *server) credentialsUnavailable(w http.ResponseWriter, r *http.Request) bool {
	if s.config.Credentials == nil {
		writeError(w, r, http.StatusServiceUnavailable, "NOT_CONFIGURED", "GitHub credentials are not configured")
		return true
	}
	return false
}

func (s *server) schedulerUnavailable(w http.ResponseWriter, r *http.Request) bool {
	if s.config.Scheduler == nil {
		writeError(w, r, http.StatusServiceUnavailable, "NOT_CONFIGURED", "Collection scheduling is not configured")
		return true
	}
	return false
}

func (s *server) listCredentials(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.credentialsUnavailable(w, r) {
		return
	}
	items, err := s.config.Credentials.List(r.Context())
	if err != nil {
		s.config.Logger.Error("list GitHub credentials", "request_id", requestID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to load credentials")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeData(w, http.StatusOK, map[string]any{"credentials": items})
}

type createCredentialPayload struct {
	Label    string `json:"label"`
	Token    string `json:"token"`
	Priority int    `json:"priority"`
}

func (s *server) createCredential(w http.ResponseWriter, r *http.Request) {
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
	if s.credentialsUnavailable(w, r) {
		return
	}
	payload, decodeErr := decodeAdminBody[createCredentialPayload](w, r)
	if decodeErr != nil {
		writeError(w, r, decodeErr.status, decodeErr.code, decodeErr.message)
		return
	}
	if err := validateCredentialInput(payload); err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_CREDENTIAL", "Credential label or token is invalid")
		return
	}
	credential, err := s.config.Credentials.Create(r.Context(), credentials.CreateInput{
		Label: payload.Label, Token: payload.Token, Priority: payload.Priority,
	})
	if err != nil {
		s.audit(r.Context(), &admin.ID, "admin.credential.create", "github_credential", "failure")
		switch {
		case errors.Is(err, credentials.ErrInvalidLabel), errors.Is(err, credentials.ErrInvalidToken):
			writeError(w, r, http.StatusBadRequest, "INVALID_CREDENTIAL", "Credential label or token is invalid")
		default:
			s.config.Logger.Error("create GitHub credential", "request_id", requestID(r.Context()), "error", err)
			writeError(w, r, http.StatusBadGateway, "GITHUB_VERIFICATION_FAILED", "GitHub rejected or did not answer the credential check")
		}
		return
	}
	s.audit(r.Context(), &admin.ID, "admin.credential.create", credential.AuthLogin, "success")
	w.Header().Set("Cache-Control", "no-store")
	writeData(w, http.StatusCreated, credential)
}

type updateCredentialPayload struct {
	Enabled *bool `json:"enabled"`
}

func (s *server) updateCredential(w http.ResponseWriter, r *http.Request) {
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
	if s.credentialsUnavailable(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	payload, decodeErr := decodeAdminBody[updateCredentialPayload](w, r)
	if decodeErr != nil {
		writeError(w, r, decodeErr.status, decodeErr.code, decodeErr.message)
		return
	}
	if payload.Enabled == nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "enabled is required")
		return
	}
	if err := s.config.Credentials.SetEnabled(r.Context(), id, *payload.Enabled); err != nil {
		s.audit(r.Context(), &admin.ID, "admin.credential.update", strconv.FormatInt(id, 10), "failure")
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Credential not found")
		return
	}
	s.audit(r.Context(), &admin.ID, "admin.credential.update", strconv.FormatInt(id, 10), "success")
	w.Header().Set("Cache-Control", "no-store")
	writeData(w, http.StatusOK, map[string]any{"id": id, "enabled": *payload.Enabled})
}

type updateJobPayload struct {
	Enabled         *bool `json:"enabled"`
	IntervalSeconds *int  `json:"interval_seconds"`
}

func (s *server) updateJob(w http.ResponseWriter, r *http.Request) {
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
	if s.schedulerUnavailable(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	payload, decodeErr := decodeAdminBody[updateJobPayload](w, r)
	if decodeErr != nil {
		writeError(w, r, decodeErr.status, decodeErr.code, decodeErr.message)
		return
	}
	if payload.Enabled == nil && payload.IntervalSeconds == nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "enabled or interval_seconds is required")
		return
	}
	if payload.IntervalSeconds != nil && *payload.IntervalSeconds < 60 {
		writeError(w, r, http.StatusBadRequest, "INVALID_INTERVAL", "interval_seconds must be at least 60")
		return
	}
	job, err := s.config.Scheduler.SetJobPolicy(r.Context(), id, payload.Enabled, payload.IntervalSeconds)
	if err != nil {
		s.audit(r.Context(), &admin.ID, "admin.job.update", strconv.FormatInt(id, 10), "failure")
		switch {
		case errors.Is(err, scheduler.ErrJobMissing):
			writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Job not found")
		case errors.Is(err, scheduler.ErrInvalidInterval):
			writeError(w, r, http.StatusBadRequest, "INVALID_INTERVAL", "interval_seconds must be at least 60")
		default:
			s.config.Logger.Error("update collector job", "request_id", requestID(r.Context()), "error", err)
			writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to update job")
		}
		return
	}
	s.audit(r.Context(), &admin.ID, "admin.job.update", job.JobType, "success")
	writeData(w, http.StatusOK, map[string]any{"job": job})
}

func (s *server) testCredential(w http.ResponseWriter, r *http.Request) {
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
	if s.credentialsUnavailable(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	credential, err := s.config.Credentials.Test(r.Context(), id)
	if err != nil {
		s.audit(r.Context(), &admin.ID, "admin.credential.test", strconv.FormatInt(id, 10), "failure")
		if errors.Is(err, credentials.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Credential not found")
			return
		}
		s.config.Logger.Error("re-check GitHub credential", "request_id", requestID(r.Context()), "error", err)
		writeError(w, r, http.StatusBadGateway, "GITHUB_VERIFICATION_FAILED", "GitHub rejected or did not answer the credential check")
		return
	}
	s.audit(r.Context(), &admin.ID, "admin.credential.test", credential.AuthLogin, "success")
	w.Header().Set("Cache-Control", "no-store")
	writeData(w, http.StatusOK, map[string]any{"credential": credential})
}

func (s *server) deleteCredential(w http.ResponseWriter, r *http.Request) {
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
	if s.credentialsUnavailable(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.config.Credentials.Delete(r.Context(), id); err != nil {
		s.audit(r.Context(), &admin.ID, "admin.credential.delete", strconv.FormatInt(id, 10), "failure")
		if errors.Is(err, credentials.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Credential not found")
			return
		}
		s.config.Logger.Error("delete GitHub credential", "request_id", requestID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to delete credential")
		return
	}
	s.audit(r.Context(), &admin.ID, "admin.credential.delete", strconv.FormatInt(id, 10), "success")
	w.Header().Set("Cache-Control", "no-store")
	writeData(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

func (s *server) listJobs(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.schedulerUnavailable(w, r) {
		return
	}
	jobs, err := s.config.Scheduler.Jobs(r.Context())
	if err != nil {
		s.config.Logger.Error("list collection jobs", "request_id", requestID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to load jobs")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeData(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (s *server) runJob(w http.ResponseWriter, r *http.Request) {
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
	if s.schedulerUnavailable(w, r) {
		return
	}
	if err := s.config.Scheduler.EnsureJob(r.Context()); err != nil {
		s.config.Logger.Error("ensure collection job", "request_id", requestID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to prepare collection job")
		return
	}
	if err := s.config.Scheduler.Precheck(r.Context()); err != nil {
		if errors.Is(err, scheduler.ErrNoCredential) {
			writeError(w, r, http.StatusConflict, "CREDENTIAL_REQUIRED", "Add and verify a GitHub token before starting collection")
			return
		}
		s.config.Logger.Error("precheck collection", "request_id", requestID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to prepare collection job")
		return
	}
	jobScheduler := s.config.Scheduler
	s.audit(r.Context(), &admin.ID, "admin.job.run", "github_collection", "success")
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Minute)
		defer cancel()
		if _, err := jobScheduler.RunOnce(ctx, "manual"); err != nil {
			s.config.Logger.Error("manual collection run failed", "error", err)
		}
	}()
	w.Header().Set("Cache-Control", "no-store")
	writeData(w, http.StatusAccepted, map[string]any{"status": "accepted"})
}

func (s *server) listJobRuns(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.schedulerUnavailable(w, r) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := s.config.Scheduler.JobRunHistory(r.Context(), limit)
	if err != nil {
		s.config.Logger.Error("list collection runs", "request_id", requestID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to load runs")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeData(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *server) jobRunDetail(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.schedulerUnavailable(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	run, err := s.config.Scheduler.Run(r.Context(), id)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Run not found")
		return
	}
	events, err := s.config.Scheduler.RunEvents(r.Context(), id, 200)
	if err != nil {
		s.config.Logger.Error("list collection run events", "request_id", requestID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to load run events")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeData(w, http.StatusOK, map[string]any{"run": run, "events": events})
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	value := strings.TrimSpace(r.PathValue("id"))
	if value == "" {
		segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		value = segments[len(segments)-1]
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, r, http.StatusBadRequest, "INVALID_ID", "Identifier must be a positive integer")
		return 0, false
	}
	return id, true
}

func validateCredentialInput(payload createCredentialPayload) error {
	label := strings.TrimSpace(payload.Label)
	if label == "" || len([]rune(label)) > 80 {
		return credentials.ErrInvalidLabel
	}
	if len(payload.Token) < 8 || len(payload.Token) > 1024 {
		return credentials.ErrInvalidToken
	}
	return nil
}

type bodyError struct {
	status  int
	code    string
	message string
}

func decodeAdminBody[T any](w http.ResponseWriter, r *http.Request) (T, *bodyError) {
	var payload T
	r.Body = http.MaxBytesReader(w, r.Body, adminBodyLimit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return payload, &bodyError{status: http.StatusRequestEntityTooLarge, code: "REQUEST_TOO_LARGE", message: "Request body is too large"}
		}
		return payload, &bodyError{status: http.StatusBadRequest, code: "INVALID_REQUEST", message: "Invalid request body"}
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return payload, &bodyError{status: http.StatusBadRequest, code: "INVALID_REQUEST", message: "Invalid request body"}
	}
	return payload, nil
}
