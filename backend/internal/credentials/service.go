package credentials

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/github-radar/github-radar/internal/github"
)

const timestampLayout = "2006-01-02T15:04:05Z"

var (
	ErrInvalidLabel = errors.New("invalid credential label")
	ErrInvalidToken = errors.New("invalid GitHub token")
	// ErrNotFound 表示凭据不存在（已被删除或 id 非法）。
	ErrNotFound = errors.New("credential not found")
)

type githubClient interface {
	GetAuthenticatedUser(context.Context) (github.AuthenticatedUser, github.ResponseMeta, error)
	GetRateLimitStatus(context.Context) (github.RateLimitStatus, github.ResponseMeta, error)
}

type githubClientFactory func(string) (githubClient, error)

type Service struct {
	db        *sql.DB
	cipher    *Cipher
	newClient githubClientFactory
	now       func() time.Time
}

type CreateInput struct {
	Label    string
	Token    string
	Priority int
}

type Credential struct {
	ID            int64      `json:"id"`
	Label         string     `json:"label"`
	AuthIdentity  string     `json:"auth_identity"`
	AuthLogin     string     `json:"auth_login"`
	TokenHint     string     `json:"token_hint"`
	Enabled       bool       `json:"enabled"`
	Status        string     `json:"status"`
	Priority      int        `json:"priority"`
	LastCheckedAt *time.Time `json:"last_checked_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type RateLimitSnapshot struct {
	Resource      string     `json:"resource"`
	Limit         int        `json:"limit"`
	Used          int        `json:"used"`
	Remaining     int        `json:"remaining"`
	ResetAt       time.Time  `json:"reset_at"`
	CooldownUntil *time.Time `json:"cooldown_until"`
}

type CredentialDetail struct {
	Credential
	RateLimits []RateLimitSnapshot `json:"rate_limits"`
}

// Option customises the credential service.
type Option func(*serviceOptions)

type serviceOptions struct {
	githubBaseURL string
}

// WithGitHubBaseURL points credential verification at an Enterprise or stubbed
// API root; without it verification always calls the public api.github.com.
func WithGitHubBaseURL(baseURL string) Option {
	return func(options *serviceOptions) {
		options.githubBaseURL = baseURL
	}
}

func NewService(database *sql.DB, key []byte, options ...Option) (*Service, error) {
	settings := serviceOptions{}
	for _, apply := range options {
		apply(&settings)
	}
	cipher, err := NewCipher(key, nil)
	if err != nil {
		return nil, err
	}
	factory := func(token string) (githubClient, error) {
		return github.NewClient(github.ClientConfig{Token: token, BaseURL: settings.githubBaseURL})
	}
	return newService(database, cipher, factory, time.Now), nil
}

func newService(database *sql.DB, cipher *Cipher, factory githubClientFactory, now func() time.Time) *Service {
	return &Service{db: database, cipher: cipher, newClient: factory, now: now}
}

func (service *Service) Create(ctx context.Context, input CreateInput) (Credential, error) {
	label, err := normalizeLabel(input.Label)
	if err != nil {
		return Credential{}, err
	}
	if !validToken(input.Token) {
		return Credential{}, ErrInvalidToken
	}
	client, err := service.newClient(input.Token)
	if err != nil {
		return Credential{}, fmt.Errorf("create GitHub client: %w", err)
	}
	user, _, err := client.GetAuthenticatedUser(ctx)
	if err != nil {
		return Credential{}, fmt.Errorf("verify GitHub credential: %w", err)
	}
	limits, _, err := client.GetRateLimitStatus(ctx)
	if err != nil {
		return Credential{}, fmt.Errorf("read GitHub rate limits: %w", err)
	}
	ciphertext, err := service.cipher.Encrypt(input.Token)
	if err != nil {
		return Credential{}, fmt.Errorf("encrypt GitHub credential: %w", err)
	}
	now := service.now().UTC().Truncate(time.Second)
	credential := Credential{
		Label: label, AuthIdentity: fmt.Sprint(user.ID), AuthLogin: user.Login,
		TokenHint: tokenHint(input.Token), Enabled: true, Status: "active", Priority: input.Priority,
		LastCheckedAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	if err := service.insert(ctx, &credential, ciphertext, limits); err != nil {
		return Credential{}, err
	}
	return credential, nil
}

func (service *Service) insert(ctx context.Context, credential *Credential, ciphertext string, limits github.RateLimitStatus) error {
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin GitHub credential creation: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO github_credentials
		(label, auth_identity, token_ciphertext, token_hint, enabled, status, last_checked_at, priority, auth_login, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, 'active', ?, ?, ?, ?, ?)`,
		credential.Label, credential.AuthIdentity, ciphertext, credential.TokenHint, formatTime(*credential.LastCheckedAt),
		credential.Priority, credential.AuthLogin, formatTime(credential.CreatedAt), formatTime(credential.UpdatedAt))
	if err != nil {
		return fmt.Errorf("insert GitHub credential: %w", err)
	}
	credential.ID, err = result.LastInsertId()
	if err != nil {
		return fmt.Errorf("read GitHub credential id: %w", err)
	}
	for resource, limit := range map[string]github.RateLimitResource{"core": limits.Resources.Core, "search": limits.Resources.Search} {
		if err := insertRateLimit(ctx, tx, credential.ID, resource, limit, credential.UpdatedAt); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit GitHub credential creation: %w", err)
	}
	return nil
}

func insertRateLimit(ctx context.Context, tx *sql.Tx, credentialID int64, resource string, limit github.RateLimitResource, now time.Time) error {
	resetAt := limit.ResetAt()
	if resetAt.IsZero() {
		return fmt.Errorf("GitHub %s rate limit reset is missing", resource)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO api_rate_limits
		(credential_id, resource, limit_value, remaining, reset_at, updated_at, used)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, credentialID, resource, limit.Limit, limit.Remaining, formatTime(resetAt), formatTime(now), limit.Used)
	if err != nil {
		return fmt.Errorf("insert GitHub %s rate limit: %w", resource, err)
	}
	return nil
}

func (service *Service) List(ctx context.Context) ([]CredentialDetail, error) {
	rows, err := service.db.QueryContext(ctx, `SELECT id, label, auth_identity, auth_login, token_hint,
			enabled, status, priority, last_checked_at, created_at, updated_at
		FROM github_credentials ORDER BY priority DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("query GitHub credentials: %w", err)
	}
	defer rows.Close()

	credentials := make([]CredentialDetail, 0, 4)
	byID := make(map[int64]int, 4)
	for rows.Next() {
		var (
			item                                 Credential
			enabled                              int
			authIdentity, authLogin, lastChecked sql.NullString
			createdAt, updatedAt                 sql.NullString
		)
		if err := rows.Scan(&item.ID, &item.Label, &authIdentity, &authLogin, &item.TokenHint,
			&enabled, &item.Status, &item.Priority, &lastChecked, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan GitHub credential: %w", err)
		}
		item.Enabled = enabled == 1
		item.AuthIdentity = authIdentity.String
		item.AuthLogin = authLogin.String
		item.LastCheckedAt = parseOptionalTime(lastChecked)
		item.CreatedAt = parseTimeOrZero(createdAt)
		item.UpdatedAt = parseTimeOrZero(updatedAt)
		byID[item.ID] = len(credentials)
		credentials = append(credentials, CredentialDetail{Credential: item, RateLimits: []RateLimitSnapshot{}})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read GitHub credentials: %w", err)
	}
	if len(credentials) == 0 {
		return credentials, nil
	}
	if err := service.attachRateLimits(ctx, credentials, byID); err != nil {
		return nil, err
	}
	return credentials, nil
}

func (service *Service) attachRateLimits(ctx context.Context, credentials []CredentialDetail, byID map[int64]int) error {
	rows, err := service.db.QueryContext(ctx, `SELECT credential_id, resource, limit_value, used, remaining, reset_at, cooldown_until
		FROM api_rate_limits ORDER BY credential_id, resource`)
	if err != nil {
		return fmt.Errorf("query GitHub rate limits: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			credentialID           int64
			resource               string
			limit, used, remaining int
			resetAt                string
			cooldownUntil          sql.NullString
		)
		if err := rows.Scan(&credentialID, &resource, &limit, &used, &remaining, &resetAt, &cooldownUntil); err != nil {
			return fmt.Errorf("scan GitHub rate limit: %w", err)
		}
		index, known := byID[credentialID]
		if !known {
			continue
		}
		snapshot := RateLimitSnapshot{
			Resource: resource, Limit: limit, Used: used, Remaining: remaining,
			ResetAt:       parseTimeOrZero(sql.NullString{String: resetAt, Valid: true}),
			CooldownUntil: parseOptionalTime(cooldownUntil),
		}
		credentials[index].RateLimits = append(credentials[index].RateLimits, snapshot)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read GitHub rate limits: %w", err)
	}
	return nil
}

func parseOptionalTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	parsed, err := time.Parse(timestampLayout, value.String)
	if err != nil {
		return nil
	}
	return &parsed
}

func parseTimeOrZero(value sql.NullString) time.Time {
	parsed := parseOptionalTime(value)
	if parsed == nil {
		return time.Time{}
	}
	return *parsed
}

func normalizeLabel(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) < 1 || utf8.RuneCountInString(value) > 80 {
		return "", ErrInvalidLabel
	}
	return value, nil
}

func validToken(value string) bool {
	if len(value) < 8 || len(value) > 1024 || !utf8.ValidString(value) {
		return false
	}
	return strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) == -1
}

func tokenHint(value string) string {
	if len(value) <= 4 {
		return "…" + value
	}
	return "…" + value[len(value)-4:]
}

func formatTime(value time.Time) string {
	return value.UTC().Truncate(time.Second).Format(timestampLayout)
}

// Test 用已存储的 Token 重新向 GitHub 验证身份与配额，并刷新记录。
// Token 已被撤销或换号时返回错误，由调用方区分 4xx/5xx。
func (service *Service) Test(ctx context.Context, credentialID int64) (Credential, error) {
	var ciphertext string
	err := service.db.QueryRowContext(ctx, `SELECT token_ciphertext FROM github_credentials WHERE id = ?`, credentialID).Scan(&ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return Credential{}, ErrNotFound
	}
	if err != nil {
		return Credential{}, fmt.Errorf("load GitHub credential: %w", err)
	}
	token, err := service.cipher.Decrypt(ciphertext)
	if err != nil {
		return Credential{}, fmt.Errorf("decrypt GitHub credential: %w", err)
	}
	client, err := service.newClient(token)
	if err != nil {
		return Credential{}, fmt.Errorf("create GitHub client: %w", err)
	}
	user, _, err := client.GetAuthenticatedUser(ctx)
	if err != nil {
		return Credential{}, fmt.Errorf("verify GitHub credential: %w", err)
	}
	limits, _, err := client.GetRateLimitStatus(ctx)
	if err != nil {
		return Credential{}, fmt.Errorf("read GitHub rate limits: %w", err)
	}
	now := service.now().UTC().Truncate(time.Second)
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return Credential{}, fmt.Errorf("begin GitHub credential re-check: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE github_credentials
		SET auth_identity = ?, auth_login = ?, status = 'active', last_checked_at = ?, updated_at = ?
		WHERE id = ?`, fmt.Sprint(user.ID), user.Login, formatTime(now), formatTime(now), credentialID); err != nil {
		return Credential{}, fmt.Errorf("refresh GitHub credential: %w", err)
	}
	for resource, limit := range map[string]github.RateLimitResource{"core": limits.Resources.Core, "search": limits.Resources.Search} {
		resetAt := limit.ResetAt()
		if resetAt.IsZero() {
			return Credential{}, fmt.Errorf("GitHub %s rate limit reset is missing", resource)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO api_rate_limits
			(credential_id, resource, limit_value, remaining, reset_at, updated_at, used)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(credential_id, resource) DO UPDATE SET
				limit_value = excluded.limit_value, remaining = excluded.remaining,
				reset_at = excluded.reset_at, updated_at = excluded.updated_at, used = excluded.used`,
			credentialID, resource, limit.Limit, limit.Remaining, formatTime(resetAt), formatTime(now), limit.Used); err != nil {
			return Credential{}, fmt.Errorf("refresh GitHub %s rate limit: %w", resource, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Credential{}, fmt.Errorf("commit GitHub credential re-check: %w", err)
	}
	items, err := service.List(ctx)
	if err != nil {
		return Credential{}, err
	}
	for _, item := range items {
		if item.ID == credentialID {
			return item.Credential, nil
		}
	}
	return Credential{}, ErrNotFound
}

// Delete 永久删除凭据；api_rate_limits 随外键级联删除。
func (service *Service) Delete(ctx context.Context, credentialID int64) error {
	result, err := service.db.ExecContext(ctx, `DELETE FROM github_credentials WHERE id = ?`, credentialID)
	if err != nil {
		return fmt.Errorf("delete GitHub credential: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check deleted GitHub credential: %w", err)
	}
	if deleted == 0 {
		return ErrNotFound
	}
	return nil
}
