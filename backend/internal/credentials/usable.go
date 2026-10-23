package credentials

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrNoCredential = errors.New("no usable GitHub credential")

// RateLimitedError reports that every credential sharing one GitHub identity is cooling down.
type RateLimitedError struct {
	Resource     string
	CredentialID int64
	RetryAt      time.Time
}

func (err *RateLimitedError) Error() string {
	return fmt.Sprintf("GitHub %s quota exhausted until %s", err.Resource, err.RetryAt.Format(time.RFC3339))
}

type UsableCredential struct {
	Credential
	Token string `json:"-"`
}

type candidate struct {
	id           int64
	identity     string
	ciphertext   string
	priority     int
	cooldownTill *time.Time
}

func (service *Service) Usable(ctx context.Context, resource string) (UsableCredential, error) {
	candidates, err := service.candidates(ctx, resource)
	if err != nil {
		return UsableCredential{}, err
	}
	if len(candidates) == 0 {
		return UsableCredential{}, ErrNoCredential
	}
	now := service.now().UTC()
	groups := groupCooldowns(candidates, now)
	for _, item := range candidates {
		if _, blocked := groups[item.identity]; blocked {
			continue
		}
		token, err := service.cipher.Decrypt(item.ciphertext)
		if err != nil {
			return UsableCredential{}, fmt.Errorf("decrypt GitHub credential %d: %w", item.id, err)
		}
		return UsableCredential{Credential: Credential{ID: item.id, AuthIdentity: item.identity}, Token: token}, nil
	}
	return UsableCredential{}, blockedError(resource, candidates, groups)
}

func (service *Service) candidates(ctx context.Context, resource string) ([]candidate, error) {
	rows, err := service.db.QueryContext(ctx, `SELECT c.id, COALESCE(c.auth_identity, ''), c.token_ciphertext, c.priority, rl.cooldown_until
		FROM github_credentials c
		LEFT JOIN api_rate_limits rl ON rl.credential_id = c.id AND rl.resource = ?
		WHERE c.enabled = 1 AND c.status IN ('active', 'unchecked')
		ORDER BY c.priority DESC, c.id`, resource)
	if err != nil {
		return nil, fmt.Errorf("query usable GitHub credentials: %w", err)
	}
	defer rows.Close()
	var result []candidate
	for rows.Next() {
		var item candidate
		var cooldown sql.NullString
		if err := rows.Scan(&item.id, &item.identity, &item.ciphertext, &item.priority, &cooldown); err != nil {
			return nil, fmt.Errorf("scan usable GitHub credential: %w", err)
		}
		item.cooldownTill = parseOptionalTime(cooldown)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read usable GitHub credentials: %w", err)
	}
	return result, nil
}

// groupCooldowns returns, per GitHub identity, the cooldown that still blocks requests at now.
func groupCooldowns(candidates []candidate, now time.Time) map[string]time.Time {
	blocked := make(map[string]time.Time)
	for _, item := range candidates {
		if item.cooldownTill == nil || !item.cooldownTill.After(now) {
			continue
		}
		if existing, found := blocked[item.identity]; !found || item.cooldownTill.After(existing) {
			blocked[item.identity] = *item.cooldownTill
		}
	}
	return blocked
}

func blockedError(resource string, candidates []candidate, blocked map[string]time.Time) error {
	var earliest time.Time
	var credentialID int64
	for _, item := range candidates {
		until, found := blocked[item.identity]
		if !found {
			continue
		}
		if earliest.IsZero() || until.Before(earliest) {
			earliest = until
			credentialID = item.id
		}
	}
	if earliest.IsZero() {
		return ErrNoCredential
	}
	return &RateLimitedError{Resource: resource, CredentialID: credentialID, RetryAt: earliest}
}

func (service *Service) RecordCooldown(ctx context.Context, credentialID int64, resource string, until time.Time, reason string) error {
	if !until.After(service.now().UTC()) {
		return errors.New("credential cooldown must be in the future")
	}
	_, err := service.db.ExecContext(ctx, `UPDATE api_rate_limits
		SET cooldown_until = ?, cooldown_reason = ?, updated_at = ?
		WHERE credential_id = ? AND resource = ?`,
		formatTime(until), truncate(reason, 200), formatTime(service.now().UTC()), credentialID, resource)
	if err != nil {
		return fmt.Errorf("record GitHub %s cooldown: %w", resource, err)
	}
	return nil
}

func (service *Service) RecordRateLimit(ctx context.Context, credentialID int64, resource string, limit, remaining int, resetAt time.Time) error {
	if limit < 0 || remaining < 0 || !resetAt.After(service.now().UTC().Add(-time.Minute)) {
		return errors.New("invalid GitHub rate limit observation")
	}
	_, err := service.db.ExecContext(ctx, `UPDATE api_rate_limits
		SET limit_value = ?, remaining = ?, used = ?, reset_at = ?, updated_at = ?
		WHERE credential_id = ? AND resource = ?`,
		limit, remaining, max(limit-remaining, 0), formatTime(resetAt), formatTime(service.now().UTC()), credentialID, resource)
	if err != nil {
		return fmt.Errorf("record GitHub %s rate limit: %w", resource, err)
	}
	return nil
}

func (service *Service) SetEnabled(ctx context.Context, credentialID int64, enabled bool) error {
	value := 0
	if enabled {
		value = 1
	}
	result, err := service.db.ExecContext(ctx, `UPDATE github_credentials SET enabled = ?, updated_at = ? WHERE id = ?`,
		value, formatTime(service.now().UTC()), credentialID)
	if err != nil {
		return fmt.Errorf("update GitHub credential %d: %w", credentialID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated GitHub credential count: %w", err)
	}
	if affected == 0 {
		return ErrNoCredential
	}
	return nil
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
