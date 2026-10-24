package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/github-radar/github-radar/internal/auth"
	"github.com/github-radar/github-radar/internal/db"
	"github.com/go-chi/chi/v5"
)

const requestIDHeader = "X-Request-ID"

const (
	loginBodyLimit = 16 * 1024
	sessionTTL     = 24 * time.Hour
)

type authService interface {
	Login(context.Context, string, string, time.Duration) (auth.Session, error)
	Authenticate(context.Context, string) (auth.Admin, error)
	VerifyCSRF(context.Context, string, string) error
	Logout(context.Context, string) error
}

// Config contains the API dependencies and externally visible security settings.
type Config struct {
	DB            *db.DB
	Auth          authService
	Credentials   credentialService
	Scheduler     schedulerService
	Readmes       readmeService
	Settings      settingsService
	Backups       backupService
	Repositories  repositoryAdminService
	PublicBaseURL string
	CookieSecure  bool
	StaticDir     string
	AppEnv        string
	Now           func() time.Time
	Logger        *slog.Logger
}

type server struct {
	config Config
	logins *loginLimiter
}

type contextKey string

const requestIDKey contextKey = "request_id"

// New constructs the complete HTTP handler.
func New(config Config) http.Handler {
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	s := &server{config: config, logins: newLoginLimiter(config.Now)}
	router := chi.NewRouter()
	router.Use(s.requestID, s.recoverer, s.securityHeaders)
	router.Get("/healthz", s.health)
	router.Get("/readyz", s.ready)
	router.Get("/api/v1/stats", s.stats)
	router.Get("/api/v1/repos", s.listRepos)
	router.Get("/api/v1/repos/{owner}/{repo}", s.getRepo)
	router.Get("/api/v1/repos/{owner}/{repo}/stars", s.getRepoStars)
	router.Get("/api/v1/repos/{owner}/{repo}/readme", s.getRepoReadme)
	router.Get("/api/v1/categories", s.listCategories)
	router.Get("/api/v1/categories/{slug}", s.getCategory)
	router.Get("/api/v1/rankings", s.listRankings)
	router.Post("/api/v1/admin/auth/login", s.login)
	router.Get("/api/v1/admin/auth/session", s.session)
	router.Post("/api/v1/admin/auth/logout", s.logout)
	router.Get("/api/v1/admin/overview", s.overview)
	router.Get("/api/v1/admin/credentials", s.listCredentials)
	router.Post("/api/v1/admin/credentials", s.createCredential)
	router.Patch("/api/v1/admin/credentials/{id}", s.updateCredential)
	router.Post("/api/v1/admin/credentials/{id}/test", s.testCredential)
	router.Delete("/api/v1/admin/credentials/{id}", s.deleteCredential)
	router.Get("/api/v1/admin/categories", s.listAdminCategories)
	router.Post("/api/v1/admin/categories", s.createAdminCategory)
	router.Patch("/api/v1/admin/categories/{id}", s.updateAdminCategory)
	router.Delete("/api/v1/admin/categories/{id}", s.deleteAdminCategory)
	router.Get("/api/v1/admin/repos", s.listAdminRepositories)
	router.Post("/api/v1/admin/repos/import", s.importAdminRepository)
	router.Patch("/api/v1/admin/repos/{id}", s.updateAdminRepository)
	router.Get("/api/v1/admin/jobs", s.listJobs)
	router.Get("/api/v1/admin/settings", s.getAdminSettings)
	router.Patch("/api/v1/admin/settings", s.updateAdminSettings)
	router.Get("/api/v1/admin/audit-logs", s.listAuditLogs)
	router.Get("/api/v1/admin/backups", s.listBackups)
	router.Post("/api/v1/admin/backups", s.createBackup)
	router.Post("/api/v1/admin/backups/{name}/verify", s.verifyBackup)
	router.Get("/api/v1/admin/backups/{name}", s.downloadBackup)
	router.Post("/api/v1/admin/jobs/run", s.runJob)
	router.Patch("/api/v1/admin/jobs/{id}", s.updateJob)
	router.Get("/api/v1/admin/job-runs", s.listJobRuns)
	router.Get("/api/v1/admin/job-runs/{id}", s.jobRunDetail)
	router.NotFound(s.notFound)
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed")
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	})
	return router
}

func (s *server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := make([]byte, 16)
		if _, err := rand.Read(value); err != nil {
			s.config.Logger.Error("generate request id", "error", err)
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}
		requestID := hex.EncodeToString(value)
		w.Header().Set(requestIDHeader, requestID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, requestID)))
	})
}

func (s *server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.config.Logger.Error("http panic recovered", "request_id", requestID(r.Context()))
				writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; img-src 'self' data: https:; object-src 'none'; base-uri 'self'")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if strings.HasPrefix(r.URL.Path, "/api/v1/admin/") {
			w.Header().Set("X-Robots-Tag", "noindex,nofollow")
		}
		if s.config.AppEnv == "production" || s.config.CookieSecure {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	writeData(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if s.config.DB == nil || s.config.DB.Ready(ctx) != nil {
		writeError(w, r, http.StatusServiceUnavailable, "NOT_READY", "Service is not ready")
		return
	}
	writeData(w, http.StatusOK, map[string]string{"status": "ready"})
}

type statsResponse struct {
	Repositories  int64   `json:"repositories"`
	StarSnapshots int64   `json:"star_snapshots"`
	Categories    int64   `json:"categories"`
	LatestSyncAt  *string `json:"latest_sync_at"`
	DataUpdatedAt *string `json:"data_updated_at"`
}

func (s *server) stats(w http.ResponseWriter, r *http.Request) {
	var response statsResponse
	var latestSync, dataUpdated sql.NullString
	err := s.config.DB.SQL().QueryRowContext(r.Context(), `SELECT
		(SELECT COUNT(*) FROM repositories),
		(SELECT COUNT(*) FROM star_snapshots),
		(SELECT COUNT(*) FROM categories WHERE enabled = 1),
		(SELECT MAX(last_successful_fetch_at) FROM repositories),
		(SELECT MAX(value) FROM (
			SELECT last_successful_fetch_at AS value FROM repositories WHERE last_successful_fetch_at IS NOT NULL
			UNION ALL SELECT first_seen_at FROM repositories
		))`).Scan(&response.Repositories, &response.StarSnapshots, &response.Categories, &latestSync, &dataUpdated)
	if err != nil {
		s.config.Logger.Error("query public stats", "request_id", requestID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to load statistics")
		return
	}
	if latestSync.Valid {
		response.LatestSyncAt = &latestSync.String
	}
	if dataUpdated.Valid {
		response.DataUpdatedAt = &dataUpdated.String
	}
	writeData(w, http.StatusOK, response)
}

type loginAttempt struct {
	failures  int
	expiresAt time.Time
}

type loginLimiter struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]loginAttempt
}

func newLoginLimiter(now func() time.Time) *loginLimiter {
	return &loginLimiter{now: now, entries: make(map[string]loginAttempt)}
}

func (l *loginLimiter) limited(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cleanup()
	entry, exists := l.entries[ip]
	return exists && entry.failures >= 5
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cleanup()
	entry := l.entries[ip]
	entry.failures++
	if entry.expiresAt.IsZero() {
		entry.expiresAt = l.now().Add(5 * time.Minute)
	}
	l.entries[ip] = entry
}

func (l *loginLimiter) succeed(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, ip)
}

func (l *loginLimiter) cleanup() {
	now := l.now()
	for ip, entry := range l.entries {
		if !entry.expiresAt.After(now) {
			delete(l.entries, ip)
		}
	}
	const maxEntries = 10_000
	for len(l.entries) >= maxEntries {
		for ip := range l.entries {
			delete(l.entries, ip)
			break
		}
	}
}

type loginPayload struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	if !s.validOrigin(r) {
		s.audit(r.Context(), nil, "admin.login", "login", "failure")
		writeError(w, r, http.StatusForbidden, "INVALID_ORIGIN", "Request origin is not allowed")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		s.audit(r.Context(), nil, "admin.login", "login", "failure")
		writeError(w, r, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Content-Type must be application/json")
		return
	}
	ip := s.clientIP(r)
	if s.logins.limited(ip) {
		s.audit(r.Context(), nil, "admin.login", "login", "rate_limited")
		writeError(w, r, http.StatusTooManyRequests, "RATE_LIMITED", "Too many login attempts")
		return
	}
	payload, decodeStatus, decodeCode, decodeMessage := decodeLogin(w, r)
	if decodeStatus != 0 {
		s.audit(r.Context(), nil, "admin.login", "login", "failure")
		writeError(w, r, decodeStatus, decodeCode, decodeMessage)
		return
	}
	if len(payload.Username) > 64 {
		s.audit(r.Context(), nil, "admin.login", "login", "failure")
		writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
		return
	}
	session, err := s.config.Auth.Login(r.Context(), payload.Username, payload.Password, sessionTTL)
	if err != nil {
		s.logins.fail(ip)
		s.audit(r.Context(), nil, "admin.login", "login", "failure")
		writeError(w, r, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Invalid username or password")
		return
	}
	s.logins.succeed(ip)
	s.setAuthCookies(w, session)
	s.audit(r.Context(), &session.Admin.ID, "admin.login", session.Admin.Username, "success")
	writeData(w, http.StatusOK, map[string]any{
		"admin":      map[string]string{"username": session.Admin.Username},
		"csrf_token": session.CSRFToken,
		"expires_at": session.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

func decodeLogin(w http.ResponseWriter, r *http.Request) (loginPayload, int, string, string) {
	r.Body = http.MaxBytesReader(w, r.Body, loginBodyLimit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var payload loginPayload
	if err := decoder.Decode(&payload); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return loginPayload{}, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "Request body is too large"
		}
		return loginPayload{}, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body"
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return loginPayload{}, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "Request body is too large"
		}
		return loginPayload{}, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body"
	}
	return payload, 0, "", ""
}

func (s *server) session(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("radar_admin_session")
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required")
		return
	}
	admin, err := s.config.Auth.Authenticate(r.Context(), cookie.Value)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeData(w, http.StatusOK, map[string]any{"admin": map[string]string{"username": admin.Username}})
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if !s.validOrigin(r) {
		s.audit(r.Context(), nil, "admin.logout", "session", "failure")
		writeError(w, r, http.StatusForbidden, "INVALID_ORIGIN", "Request origin is not allowed")
		return
	}
	sessionCookie, err := r.Cookie("radar_admin_session")
	if err != nil {
		s.audit(r.Context(), nil, "admin.logout", "session", "failure")
		writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required")
		return
	}
	admin, err := s.config.Auth.Authenticate(r.Context(), sessionCookie.Value)
	if err != nil {
		s.audit(r.Context(), nil, "admin.logout", "session", "failure")
		writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required")
		return
	}
	csrfCookie, cookieErr := r.Cookie("radar_csrf")
	headerToken := r.Header.Get("X-CSRF-Token")
	if cookieErr != nil || headerToken == "" || !constantTimeTokenEqual(headerToken, csrfCookie.Value) {
		s.audit(r.Context(), &admin.ID, "admin.logout", admin.Username, "failure")
		writeError(w, r, http.StatusForbidden, "INVALID_CSRF", "CSRF validation failed")
		return
	}
	if err := s.config.Auth.VerifyCSRF(r.Context(), sessionCookie.Value, headerToken); err != nil {
		s.audit(r.Context(), &admin.ID, "admin.logout", admin.Username, "failure")
		writeError(w, r, http.StatusForbidden, "INVALID_CSRF", "CSRF validation failed")
		return
	}
	if err := s.config.Auth.Logout(r.Context(), sessionCookie.Value); err != nil {
		s.audit(r.Context(), &admin.ID, "admin.logout", admin.Username, "failure")
		s.config.Logger.Error("logout session", "request_id", requestID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to log out")
		return
	}
	s.clearAuthCookies(w)
	s.audit(r.Context(), &admin.ID, "admin.logout", admin.Username, "success")
	w.WriteHeader(http.StatusNoContent)
}

type overviewResponse struct {
	RepositoryCount    int64 `json:"repository_count"`
	SnapshotCount      int64 `json:"snapshot_count"`
	CategoryCount      int64 `json:"category_count"`
	AdminCount         int64 `json:"admin_count"`
	ActiveSessionCount int64 `json:"active_session_count"`
	DatabaseReady      bool  `json:"database_ready"`
}

func (s *server) overview(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("radar_admin_session")
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required")
		return
	}
	if _, err := s.config.Auth.Authenticate(r.Context(), cookie.Value); err != nil {
		writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required")
		return
	}
	var response overviewResponse
	err = s.config.DB.SQL().QueryRowContext(r.Context(), `SELECT
		(SELECT COUNT(*) FROM repositories),
		(SELECT COUNT(*) FROM star_snapshots),
		(SELECT COUNT(*) FROM categories),
		(SELECT COUNT(*) FROM admin_users),
		(SELECT COUNT(*) FROM admin_sessions WHERE revoked_at IS NULL AND expires_at > ?)`,
		s.config.Now().UTC().Truncate(time.Second).Format(time.RFC3339),
	).Scan(&response.RepositoryCount, &response.SnapshotCount, &response.CategoryCount, &response.AdminCount, &response.ActiveSessionCount)
	if err != nil {
		s.config.Logger.Error("query admin overview", "request_id", requestID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to load overview")
		return
	}
	readyCtx, cancel := context.WithTimeout(r.Context(), time.Second)
	response.DatabaseReady = s.config.DB.Ready(readyCtx) == nil
	cancel()
	w.Header().Set("Cache-Control", "no-store")
	writeData(w, http.StatusOK, response)
}

func (s *server) validOrigin(r *http.Request) bool {
	value := r.Header.Get("Origin")
	if value == "" || value == "null" {
		return false
	}
	provided, err := url.Parse(value)
	if err != nil || provided.Scheme == "" || provided.Host == "" || provided.User != nil || provided.Path != "" || provided.RawQuery != "" || provided.Fragment != "" {
		return false
	}
	expectedValue := s.config.PublicBaseURL
	if expectedValue == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		expectedValue = scheme + "://" + r.Host
	}
	expected, err := url.Parse(expectedValue)
	return err == nil && strings.EqualFold(provided.Scheme, expected.Scheme) && strings.EqualFold(provided.Host, expected.Host)
}

func remoteIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	return remoteAddr
}

// clientIP only accepts X-Forwarded-For when the immediate TCP peer is in the
// administrator-controlled trusted proxy list. Invalid chains fail closed.
func (s *server) clientIP(r *http.Request) string {
	peerText := remoteIP(r.RemoteAddr)
	peer, err := netip.ParseAddr(peerText)
	if err != nil || s.config.Settings == nil {
		return peerText
	}
	snapshot, err := s.config.Settings.Get(r.Context())
	if err != nil {
		s.config.Logger.Warn("load trusted proxies", "request_id", requestID(r.Context()), "error", err)
		return peerText
	}
	trusted := func(address netip.Addr) bool {
		address = address.Unmap()
		for _, raw := range snapshot.TrustedProxies {
			if prefix, parseErr := netip.ParsePrefix(raw); parseErr == nil && prefix.Contains(address) {
				return true
			}
			if exact, parseErr := netip.ParseAddr(raw); parseErr == nil && exact.Unmap() == address {
				return true
			}
		}
		return false
	}
	if !trusted(peer) {
		return peerText
	}
	chain := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	if len(chain) == 1 && strings.TrimSpace(chain[0]) == "" {
		return peerText
	}
	for index := len(chain) - 1; index >= 0; index-- {
		address, parseErr := netip.ParseAddr(strings.TrimSpace(chain[index]))
		if parseErr != nil {
			return peerText
		}
		address = address.Unmap()
		if !trusted(address) {
			return address.String()
		}
	}
	leftmost, parseErr := netip.ParseAddr(strings.TrimSpace(chain[0]))
	if parseErr != nil {
		return peerText
	}
	return leftmost.Unmap().String()
}

func constantTimeTokenEqual(first, second string) bool {
	firstHash := sha256.Sum256([]byte(first))
	secondHash := sha256.Sum256([]byte(second))
	return subtle.ConstantTimeCompare(firstHash[:], secondHash[:]) == 1
}

func (s *server) setAuthCookies(w http.ResponseWriter, session auth.Session) {
	maxAge := int(time.Until(session.ExpiresAt).Seconds())
	if fixed := s.config.Now(); fixed.Before(session.ExpiresAt) {
		maxAge = int(session.ExpiresAt.Sub(fixed).Seconds())
	}
	base := http.Cookie{Path: "/", Secure: s.config.CookieSecure, SameSite: http.SameSiteLaxMode, Expires: session.ExpiresAt, MaxAge: maxAge}
	sessionCookie := base
	sessionCookie.Name = "radar_admin_session"
	sessionCookie.Value = session.Token
	sessionCookie.HttpOnly = true
	http.SetCookie(w, &sessionCookie)
	csrfCookie := base
	csrfCookie.Name = "radar_csrf"
	csrfCookie.Value = session.CSRFToken
	http.SetCookie(w, &csrfCookie)
}

func (s *server) clearAuthCookies(w http.ResponseWriter) {
	for _, item := range []struct {
		name     string
		httpOnly bool
	}{{"radar_admin_session", true}, {"radar_csrf", false}} {
		http.SetCookie(w, &http.Cookie{Name: item.name, Value: "", Path: "/", HttpOnly: item.httpOnly, Secure: s.config.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(0, 0).UTC()})
	}
}

func (s *server) audit(ctx context.Context, adminID *int64, action, target, result string) {
	if s.config.DB == nil {
		return
	}
	_, err := s.config.DB.SQL().ExecContext(ctx, `INSERT INTO audit_logs(admin_id, action, target, result, created_at, request_id)
		VALUES (?, ?, ?, ?, ?, ?)`, adminID, action, target, result, s.config.Now().UTC().Truncate(time.Second).Format(time.RFC3339), requestID(ctx))
	if err != nil {
		s.config.Logger.Error("write audit log", "request_id", requestID(ctx), "action", action, "error", err)
	}
}

func (s *server) notFound(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "API endpoint not found")
		return
	}
	s.serveStatic(w, r)
}

func (s *server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if s.config.StaticDir == "" {
		http.Error(w, "web application is unavailable", http.StatusServiceUnavailable)
		return
	}
	cleaned := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(r.URL.Path, "/")))
	if cleaned == "." {
		cleaned = "index.html"
	}
	root, err := filepath.Abs(s.config.StaticDir)
	if err != nil {
		http.Error(w, "web application is unavailable", http.StatusServiceUnavailable)
		return
	}
	candidate := filepath.Join(root, cleaned)
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		writeError(w, r, http.StatusBadRequest, "INVALID_PATH", "Invalid path")
		return
	}
	info, err := os.Stat(candidate)
	if err != nil || !info.Mode().IsRegular() {
		candidate = filepath.Join(root, "index.html")
		info, err = os.Stat(candidate)
	}
	if err != nil || !info.Mode().IsRegular() {
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.config.Logger.Error("inspect static asset", "request_id", requestID(r.Context()), "error", err)
		}
		http.Error(w, "web application is unavailable", http.StatusServiceUnavailable)
		return
	}
	resolvedRoot, rootErr := filepath.EvalSymlinks(root)
	resolvedCandidate, candidateErr := filepath.EvalSymlinks(candidate)
	resolvedRelative, relativeErr := filepath.Rel(resolvedRoot, resolvedCandidate)
	if rootErr != nil || candidateErr != nil || relativeErr != nil || resolvedRelative == ".." || strings.HasPrefix(resolvedRelative, ".."+string(filepath.Separator)) {
		writeError(w, r, http.StatusBadRequest, "INVALID_PATH", "Invalid path")
		return
	}
	if contentType := mime.TypeByExtension(filepath.Ext(candidate)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	http.ServeFile(w, r, candidate)
}

func requestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func writeData(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, map[string]any{"data": data})
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error":      map[string]string{"code": code, "message": message},
		"request_id": requestID(r.Context()),
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		panic(fmt.Errorf("encode JSON response: %w", err))
	}
}
