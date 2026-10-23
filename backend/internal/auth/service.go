package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	timestampLayout = "2006-01-02T15:04:05Z"
	tokenBytes      = 32
	maxSessionTTL   = 30 * 24 * time.Hour
	maxTokenRetries = 3
)

var (
	ErrAdminExists        = errors.New("administrator already exists")
	ErrInvalidUsername    = errors.New("invalid administrator username")
	ErrInvalidPassword    = errors.New("invalid administrator password")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidTTL         = errors.New("invalid session ttl")
	ErrUnauthenticated    = errors.New("unauthenticated")
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9._-]{3,64}$`)

type Admin struct {
	ID          int64
	Username    string
	CreatedAt   time.Time
	LastLoginAt *time.Time
}

type Session struct {
	Token     string
	CSRFToken string
	ExpiresAt time.Time
	Admin     Admin
}

type Service struct {
	db        *sql.DB
	hasher    *passwordHasher
	dummyHash string
	now       func() time.Time
	random    io.Reader
}

func NewService(db *sql.DB) *Service {
	return newService(db, defaultPasswordHasher(), time.Now, rand.Reader)
}

func newService(db *sql.DB, hasher *passwordHasher, now func() time.Time, random io.Reader) *Service {
	return &Service{db: db, hasher: hasher, dummyHash: dummyHash(hasher.params), now: now, random: random}
}

func dummyHash(params PasswordParams) string {
	salt := []byte("radar-dummy-salt")
	key := argon2.IDKey([]byte("dummy-password-never-used"), salt, params.Iterations, params.Memory, params.Parallelism, params.KeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, params.Memory, params.Iterations, params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

func (s *Service) CreateAdmin(ctx context.Context, username, password string) (Admin, error) {
	normalized, err := normalizeUsername(username)
	if err != nil {
		return Admin{}, err
	}
	if err := validatePassword(password); err != nil {
		return Admin{}, err
	}
	passwordHash, err := s.hasher.Hash(password)
	if err != nil {
		return Admin{}, fmt.Errorf("hash administrator password: %w", err)
	}
	createdAt := canonicalTime(s.now())

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Admin{}, fmt.Errorf("begin administrator creation: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx,
		"INSERT INTO admin_users(username, password_hash, created_at) VALUES (?, ?, ?)",
		normalized, passwordHash, formatTime(createdAt),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: admin_users.username") {
			return Admin{}, ErrAdminExists
		}
		return Admin{}, fmt.Errorf("insert administrator: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Admin{}, fmt.Errorf("read administrator id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Admin{}, fmt.Errorf("commit administrator creation: %w", err)
	}
	return Admin{ID: id, Username: normalized, CreatedAt: createdAt}, nil
}

func (s *Service) Login(ctx context.Context, username, password string, ttl time.Duration) (Session, error) {
	if ttl <= 0 || ttl > maxSessionTTL {
		return Session{}, ErrInvalidTTL
	}
	normalized := strings.ToLower(strings.TrimSpace(username))
	var admin Admin
	var passwordHash, createdAt string
	err := s.db.QueryRowContext(ctx,
		"SELECT id, username, password_hash, created_at FROM admin_users WHERE username = ?",
		normalized,
	).Scan(&admin.ID, &admin.Username, &passwordHash, &createdAt)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Session{}, fmt.Errorf("find administrator: %w", err)
	}
	if !found {
		passwordHash = s.dummyHash
	}
	matched, verifyErr := s.hasher.Verify(password, passwordHash)
	if verifyErr != nil || !found || !matched {
		return Session{}, ErrInvalidCredentials
	}
	admin.CreatedAt, err = time.Parse(timestampLayout, createdAt)
	if err != nil {
		return Session{}, fmt.Errorf("parse administrator creation time: %w", err)
	}

	rawNow := s.now().UTC()
	now := canonicalTime(rawNow)
	absoluteExpiresAt := rawNow.Add(ttl)
	expiresAt := canonicalTime(absoluteExpiresAt)
	if absoluteExpiresAt.Nanosecond() != 0 {
		expiresAt = expiresAt.Add(time.Second)
	}
	if maxExpiresAt := now.Add(maxSessionTTL); expiresAt.After(maxExpiresAt) {
		expiresAt = maxExpiresAt
	}
	for attempt := 0; attempt < maxTokenRetries; attempt++ {
		token, err := randomToken(s.random)
		if err != nil {
			return Session{}, fmt.Errorf("generate session token: %w", err)
		}
		csrfToken, err := randomToken(s.random)
		if err != nil {
			return Session{}, fmt.Errorf("generate CSRF token: %w", err)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return Session{}, fmt.Errorf("begin login: %w", err)
		}
		if _, err = tx.ExecContext(ctx, "UPDATE admin_users SET last_login_at = ? WHERE id = ?", formatTime(now), admin.ID); err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO admin_sessions(admin_id, session_hash, csrf_hash, expires_at, created_at)
				VALUES (?, ?, ?, ?, ?)`, admin.ID, hashToken(token), hashToken(csrfToken), formatTime(expiresAt), formatTime(now))
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			_ = tx.Rollback()
		}
		if err == nil {
			admin.LastLoginAt = &now
			return Session{Token: token, CSRFToken: csrfToken, ExpiresAt: expiresAt, Admin: admin}, nil
		}
		if !isSessionHashCollision(err) {
			return Session{}, fmt.Errorf("store login session: %w", err)
		}
	}
	return Session{}, fmt.Errorf("store login session after %d token attempts", maxTokenRetries)
}

func (s *Service) Authenticate(ctx context.Context, token string) (Admin, error) {
	if !validToken(token) {
		return Admin{}, ErrUnauthenticated
	}
	var admin Admin
	var createdAt string
	var lastLoginAt sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT u.id, u.username, u.created_at, u.last_login_at
		FROM admin_sessions s JOIN admin_users u ON u.id = s.admin_id
		WHERE s.session_hash = ? AND s.revoked_at IS NULL AND s.expires_at > ?`,
		hashToken(token), formatTime(s.now()),
	).Scan(&admin.ID, &admin.Username, &createdAt, &lastLoginAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Admin{}, ErrUnauthenticated
	}
	if err != nil {
		return Admin{}, fmt.Errorf("authenticate session: %w", err)
	}
	admin.CreatedAt, err = time.Parse(timestampLayout, createdAt)
	if err != nil {
		return Admin{}, fmt.Errorf("parse administrator creation time: %w", err)
	}
	if lastLoginAt.Valid {
		value, err := time.Parse(timestampLayout, lastLoginAt.String)
		if err != nil {
			return Admin{}, fmt.Errorf("parse administrator last login time: %w", err)
		}
		admin.LastLoginAt = &value
	}
	return admin, nil
}

func (s *Service) VerifyCSRF(ctx context.Context, sessionToken, csrfToken string) error {
	if !validToken(sessionToken) || !validToken(csrfToken) {
		return ErrUnauthenticated
	}
	var storedHash sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT csrf_hash FROM admin_sessions
		WHERE session_hash = ? AND revoked_at IS NULL AND expires_at > ?`,
		hashToken(sessionToken), formatTime(s.now()),
	).Scan(&storedHash)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !storedHash.Valid {
		return ErrUnauthenticated
	}
	if err != nil {
		return fmt.Errorf("verify CSRF session: %w", err)
	}
	providedHash := hashToken(csrfToken)
	if subtle.ConstantTimeCompare([]byte(storedHash.String), []byte(providedHash)) != 1 {
		return ErrUnauthenticated
	}
	return nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if !validToken(token) {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE admin_sessions SET revoked_at = ?
		WHERE session_hash = ? AND revoked_at IS NULL`, formatTime(s.now()), hashToken(token))
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

func validToken(token string) bool {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(token)
	return err == nil && len(decoded) == tokenBytes
}

func randomToken(source io.Reader) (string, error) {
	value := make([]byte, tokenBytes)
	if _, err := io.ReadFull(source, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func hashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func isSessionHashCollision(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed: admin_sessions.session_hash")
}

func normalizeUsername(username string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(username))
	if !usernamePattern.MatchString(normalized) {
		return "", ErrInvalidUsername
	}
	return normalized, nil
}

func validatePassword(password string) error {
	length := utf8.RuneCountInString(password)
	if !utf8.ValidString(password) || length < 12 || length > 1024 {
		return ErrInvalidPassword
	}
	return nil
}

func canonicalTime(value time.Time) time.Time {
	return value.UTC().Truncate(time.Second)
}

func formatTime(value time.Time) string {
	return canonicalTime(value).Format(timestampLayout)
}
