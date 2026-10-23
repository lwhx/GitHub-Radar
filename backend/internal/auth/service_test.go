package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/github-radar/github-radar/internal/db"
)

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	store, err := db.Open(context.Background(), t.TempDir()+"/radar.db")
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func testService(t *testing.T, now time.Time, randomBytes []byte) (*Service, *db.DB) {
	t.Helper()
	store := openTestDB(t)
	hasher := newPasswordHasher(PasswordParams{
		Memory: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32,
	}, bytes.NewReader(randomBytes))
	service := newService(store.SQL(), hasher, func() time.Time { return now }, bytes.NewReader(randomBytes))
	return service, store
}

func TestCreateAdminNormalizesUsernameAndStoresCanonicalUTCSecond(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 34, 56, 789000000, time.FixedZone("offset", 8*60*60))
	service, store := testService(t, now, bytes.Repeat([]byte{0x21}, 128))

	admin, err := service.CreateAdmin(context.Background(), "  Radar.Admin  ", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	if admin.Username != "radar.admin" {
		t.Errorf("admin username = %q, want radar.admin", admin.Username)
	}
	if got, want := admin.CreatedAt, time.Date(2026, 10, 23, 4, 34, 56, 0, time.UTC); !got.Equal(want) || got.Location() != time.UTC {
		t.Errorf("admin CreatedAt = %v (%v), want %v UTC", got, got.Location(), want)
	}

	var username, passwordHash, createdAt string
	if err := store.SQL().QueryRow("SELECT username, password_hash, created_at FROM admin_users WHERE id = ?", admin.ID).Scan(&username, &passwordHash, &createdAt); err != nil {
		t.Fatalf("query admin: %v", err)
	}
	if username != "radar.admin" || passwordHash == "correct horse battery staple" || createdAt != "2026-10-23T04:34:56Z" {
		t.Errorf("stored admin = username %q hash %q created_at %q", username, passwordHash, createdAt)
	}
}

func TestCreateAdminReturnsSentinelForNormalizedDuplicate(t *testing.T) {
	service, _ := testService(t, time.Now(), bytes.Repeat([]byte{0x22}, 128))
	if _, err := service.CreateAdmin(context.Background(), "Radar", "correct horse battery staple"); err != nil {
		t.Fatalf("first CreateAdmin() error = %v", err)
	}
	if _, err := service.CreateAdmin(context.Background(), " radar ", "another secure password"); !errors.Is(err, ErrAdminExists) {
		t.Fatalf("duplicate CreateAdmin() error = %v, want ErrAdminExists", err)
	}
}

func TestCreateAdminRejectsInvalidUsername(t *testing.T) {
	service, store := testService(t, time.Now(), bytes.Repeat([]byte{0x23}, 128))
	for _, username := range []string{"ab", "a b c", "管理员", "admin!", strings.Repeat("a", 65)} {
		if _, err := service.CreateAdmin(context.Background(), username, "correct horse battery staple"); !errors.Is(err, ErrInvalidUsername) {
			t.Errorf("CreateAdmin(%q) error = %v, want ErrInvalidUsername", username, err)
		}
	}
	var count int
	if err := store.SQL().QueryRow("SELECT COUNT(*) FROM admin_users").Scan(&count); err != nil || count != 0 {
		t.Fatalf("admin count = %d, error = %v, want 0", count, err)
	}
}

func TestCreateAdminRequiresTwelveToOneThousandTwentyFourUnicodeCharacters(t *testing.T) {
	service, store := testService(t, time.Now(), bytes.Repeat([]byte{0x24}, 128))
	for _, password := range []string{strings.Repeat("密", 11), strings.Repeat("密", 1025), string([]byte{0xff})} {
		if _, err := service.CreateAdmin(context.Background(), "radar", password); !errors.Is(err, ErrInvalidPassword) {
			t.Errorf("CreateAdmin() error = %v, want ErrInvalidPassword", err)
		}
	}
	if _, err := service.CreateAdmin(context.Background(), "radar", strings.Repeat("密", 12)); err != nil {
		t.Fatalf("CreateAdmin() with 12 Unicode characters error = %v", err)
	}
	var count int
	if err := store.SQL().QueryRow("SELECT COUNT(*) FROM admin_users").Scan(&count); err != nil || count != 1 {
		t.Fatalf("admin count = %d, error = %v, want 1", count, err)
	}
}

func TestLoginCreatesHashedSessionAndUpdatesLastLoginAtomically(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 34, 56, 999000000, time.FixedZone("offset", 8*60*60))
	service, store := testService(t, now, bytes.Repeat([]byte{0x31}, 256))
	if _, err := service.CreateAdmin(context.Background(), "Radar", "correct horse battery staple"); err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}

	session, err := service.Login(context.Background(), " RADAR ", "correct horse battery staple", 24*time.Hour)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if session.Admin.Username != "radar" || session.Token == "" || session.CSRFToken == "" {
		t.Fatalf("Login() session = %+v", session)
	}
	for name, token := range map[string]string{"session": session.Token, "csrf": session.CSRFToken} {
		decoded, err := base64.RawURLEncoding.Strict().DecodeString(token)
		if err != nil || len(decoded) != tokenBytes || strings.Contains(token, "=") {
			t.Errorf("%s token is not %d-byte unpadded base64url: %q, error=%v", name, tokenBytes, token, err)
		}
	}
	if got := session.ExpiresAt.Format(timestampLayout); got != "2026-10-24T04:34:56Z" {
		t.Errorf("ExpiresAt = %q", got)
	}

	var sessionHash, csrfHash, expiresAt, createdAt, lastLoginAt string
	if err := store.SQL().QueryRow(`SELECT s.session_hash, s.csrf_hash, s.expires_at, s.created_at, u.last_login_at
		FROM admin_sessions s JOIN admin_users u ON u.id = s.admin_id`).Scan(&sessionHash, &csrfHash, &expiresAt, &createdAt, &lastLoginAt); err != nil {
		t.Fatalf("query session: %v", err)
	}
	if sessionHash == session.Token || csrfHash == session.CSRFToken || len(sessionHash) != 64 || len(csrfHash) != 64 {
		t.Errorf("stored token hashes are unsafe: session=%q csrf=%q", sessionHash, csrfHash)
	}
	if expiresAt != "2026-10-24T04:34:56Z" || createdAt != "2026-10-23T04:34:56Z" || lastLoginAt != createdAt {
		t.Errorf("stored times = expires %q created %q last_login %q", expiresAt, createdAt, lastLoginAt)
	}
}

func TestLoginUsesUnifiedCredentialErrorAndValidatesTTL(t *testing.T) {
	service, store := testService(t, time.Now(), bytes.Repeat([]byte{0x32}, 256))
	if _, err := service.CreateAdmin(context.Background(), "radar", "correct horse battery staple"); err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	if _, err := store.SQL().Exec("UPDATE admin_users SET password_hash = 'malformed' WHERE username = 'radar'"); err != nil {
		t.Fatalf("corrupt password hash: %v", err)
	}
	if _, err := service.Login(context.Background(), "radar", "correct horse battery staple", time.Hour); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() malformed hash error = %v, want ErrInvalidCredentials", err)
	}
	if _, err := store.SQL().Exec("DELETE FROM admin_users"); err != nil {
		t.Fatalf("delete malformed administrator: %v", err)
	}
	if _, err := service.CreateAdmin(context.Background(), "radar", "correct horse battery staple"); err != nil {
		t.Fatalf("recreate administrator: %v", err)
	}
	for name, credentials := range map[string][2]string{
		"wrong password": {"radar", "wrong password"},
		"unknown user":   {"missing", "wrong password"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.Login(context.Background(), credentials[0], credentials[1], time.Hour); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
			}
		})
	}
	for _, ttl := range []time.Duration{0, -time.Second, maxSessionTTL + time.Second} {
		if _, err := service.Login(context.Background(), "radar", "correct horse battery staple", ttl); !errors.Is(err, ErrInvalidTTL) {
			t.Errorf("Login(ttl=%v) error = %v, want ErrInvalidTTL", ttl, err)
		}
	}
}

func TestAuthenticateReturnsAdminForActiveSession(t *testing.T) {
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	service, _ := testService(t, now, bytes.Repeat([]byte{0x33}, 256))
	if _, err := service.CreateAdmin(context.Background(), "radar", "correct horse battery staple"); err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	session, err := service.Login(context.Background(), "radar", "correct horse battery staple", time.Hour)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	admin, err := service.Authenticate(context.Background(), session.Token)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if admin.ID != session.Admin.ID || admin.Username != "radar" {
		t.Errorf("Authenticate() admin = %+v, want %+v", admin, session.Admin)
	}
}

func TestLoginDummyHashUsesInjectedArgon2Cost(t *testing.T) {
	service, _ := testService(t, time.Now(), bytes.Repeat([]byte{0x35}, 256))
	if !strings.Contains(service.dummyHash, "$m=64,t=1,p=1$") {
		t.Fatalf("dummy hash does not use injected cost: %q", service.dummyHash)
	}
}

func TestAuthenticateRejectsMalformedUnknownAndExpiredTokens(t *testing.T) {
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	current := now
	service, _ := testService(t, now, bytes.Repeat([]byte{0x34}, 256))
	service.now = func() time.Time { return current }
	if _, err := service.CreateAdmin(context.Background(), "radar", "correct horse battery staple"); err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	session, err := service.Login(context.Background(), "radar", "correct horse battery staple", time.Hour)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	unknown := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x99}, tokenBytes))
	for _, token := range []string{"", "not-base64!", unknown} {
		if _, err := service.Authenticate(context.Background(), token); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("Authenticate(%q) error = %v, want ErrUnauthenticated", token, err)
		}
	}
	current = session.ExpiresAt
	if _, err := service.Authenticate(context.Background(), session.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("Authenticate(expired) error = %v, want ErrUnauthenticated", err)
	}
}

func TestVerifyCSRFChecksActiveSessionAndToken(t *testing.T) {
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	current := now
	service, _ := testService(t, now, bytes.Repeat([]byte{0x36}, 256))
	service.now = func() time.Time { return current }
	if _, err := service.CreateAdmin(context.Background(), "radar", "correct horse battery staple"); err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	session, err := service.Login(context.Background(), "radar", "correct horse battery staple", time.Hour)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if err := service.VerifyCSRF(context.Background(), session.Token, session.CSRFToken); err != nil {
		t.Fatalf("VerifyCSRF() error = %v", err)
	}
	wrong := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x98}, tokenBytes))
	for _, tokens := range [][2]string{{session.Token, wrong}, {wrong, session.CSRFToken}, {"bad", "bad"}} {
		if err := service.VerifyCSRF(context.Background(), tokens[0], tokens[1]); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("VerifyCSRF() error = %v, want ErrUnauthenticated", err)
		}
	}
	current = session.ExpiresAt
	if err := service.VerifyCSRF(context.Background(), session.Token, session.CSRFToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("VerifyCSRF(expired) error = %v, want ErrUnauthenticated", err)
	}
}

func TestVerifyCSRFRejectsSessionWithoutCSRFHash(t *testing.T) {
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	service, store := testService(t, now, bytes.Repeat([]byte{0x3a}, 256))
	admin, err := service.CreateAdmin(context.Background(), "radar", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	sessionToken := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x3a}, tokenBytes))
	csrfToken := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x3b}, tokenBytes))
	if _, err := store.SQL().Exec(`INSERT INTO admin_sessions(admin_id, session_hash, csrf_hash, expires_at, created_at)
		VALUES (?, ?, NULL, ?, ?)`, admin.ID, hashToken(sessionToken), "2026-10-24T00:00:00Z", "2026-10-23T00:00:00Z"); err != nil {
		t.Fatalf("insert session without CSRF hash: %v", err)
	}
	if err := service.VerifyCSRF(context.Background(), sessionToken, csrfToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("VerifyCSRF() error = %v, want ErrUnauthenticated", err)
	}
}

func TestLogoutRevokesSessionAndIsIdempotent(t *testing.T) {
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	service, store := testService(t, now, bytes.Repeat([]byte{0x37}, 256))
	if _, err := service.CreateAdmin(context.Background(), "radar", "correct horse battery staple"); err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	session, err := service.Login(context.Background(), "radar", "correct horse battery staple", time.Hour)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := service.Logout(context.Background(), session.Token); err != nil {
			t.Fatalf("Logout() attempt %d error = %v", i+1, err)
		}
	}
	if _, err := service.Authenticate(context.Background(), session.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("Authenticate(revoked) error = %v, want ErrUnauthenticated", err)
	}
	if err := service.VerifyCSRF(context.Background(), session.Token, session.CSRFToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("VerifyCSRF(revoked) error = %v, want ErrUnauthenticated", err)
	}
	var revokedAt string
	if err := store.SQL().QueryRow("SELECT revoked_at FROM admin_sessions").Scan(&revokedAt); err != nil || revokedAt != "2026-10-23T00:00:00Z" {
		t.Fatalf("revoked_at = %q, error = %v", revokedAt, err)
	}
	if err := service.Logout(context.Background(), "bad"); err != nil {
		t.Fatalf("Logout(malformed) error = %v, want idempotent nil", err)
	}
}

func TestLoginReturnsRandomErrorWithoutPersistingSession(t *testing.T) {
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	service, store := testService(t, now, bytes.Repeat([]byte{0x38}, 256))
	if _, err := service.CreateAdmin(context.Background(), "radar", "correct horse battery staple"); err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	service.random = errReader{}
	if _, err := service.Login(context.Background(), "radar", "correct horse battery staple", time.Hour); err == nil {
		t.Fatal("Login() error = nil, want random source error")
	}
	var sessions int
	var lastLogin any
	if err := store.SQL().QueryRow("SELECT COUNT(*) FROM admin_sessions").Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := store.SQL().QueryRow("SELECT last_login_at FROM admin_users").Scan(&lastLogin); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 || lastLogin != nil {
		t.Fatalf("failed login persisted sessions=%d last_login=%v", sessions, lastLogin)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestLoginStopsAfterBoundedSessionTokenCollisions(t *testing.T) {
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	randomBytes := bytes.Repeat([]byte{0x39}, 512)
	service, store := testService(t, now, randomBytes)
	admin, err := service.CreateAdmin(context.Background(), "radar", "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	collisionToken := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x39}, tokenBytes))
	if _, err := store.SQL().Exec(`INSERT INTO admin_sessions(admin_id, session_hash, csrf_hash, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?)`, admin.ID, hashToken(collisionToken), hashToken(collisionToken), "2026-10-24T00:00:00Z", "2026-10-23T00:00:00Z"); err != nil {
		t.Fatalf("seed colliding session: %v", err)
	}
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x39}, maxTokenRetries*tokenBytes*2))

	if _, err := service.Login(context.Background(), "radar", "correct horse battery staple", time.Hour); err == nil || !strings.Contains(err.Error(), "3 token attempts") {
		t.Fatalf("Login() collision error = %v", err)
	}
	var count int
	if err := store.SQL().QueryRow("SELECT COUNT(*) FROM admin_sessions").Scan(&count); err != nil || count != 1 {
		t.Fatalf("session count = %d, error = %v, want 1", count, err)
	}
}
