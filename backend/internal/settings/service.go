package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/github-radar/github-radar/internal/credentials"
)

// ErrInvalidSettings 表示设置值未通过服务端校验。
var ErrInvalidSettings = errors.New("invalid settings value")

const (
	defaultSiteName     = "GitHub Radar"
	defaultTimezone     = "UTC"
	defaultPageSize     = 20
	minPageSize         = 10
	maxPageSize         = 100
	maxSiteNameRunes    = 80
	maxDescriptionRunes = 500
)

// Snapshot 是脱敏后的完整设置视图；密钥永不回传，只暴露存在性。
type Snapshot struct {
	SiteName        string   `json:"site_name"`
	SiteDescription string   `json:"site_description"`
	Timezone        string   `json:"timezone"`
	DefaultPageSize int      `json:"default_page_size"`
	AIEnabled       bool     `json:"ai_enabled"`
	AIBaseURL       string   `json:"ai_base_url"`
	AIModel         string   `json:"ai_model"`
	HasAIAPIKey     bool     `json:"has_ai_api_key"`
	TrustedProxies  []string `json:"trusted_proxies"`
}

// Update 是局部更新载荷；nil 表示保持不变。AIAPIKey 特殊：nil 保留，空串清除。
type Update struct {
	SiteName        *string
	SiteDescription *string
	Timezone        *string
	DefaultPageSize *int
	AIEnabled       *bool
	AIBaseURL       *string
	AIModel         *string
	AIAPIKey        *string
	TrustedProxies  *[]string
}

// Service 读写 site_settings，负责默认值、校验与 AI Key 加密。
type Service struct {
	db     *sql.DB
	cipher *credentials.Cipher
	now    func() time.Time
}

func NewService(database *sql.DB, cipher *credentials.Cipher, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{db: database, cipher: cipher, now: now}
}

// Get 返回叠加默认值后的完整快照。
func (service *Service) Get(ctx context.Context) (Snapshot, error) {
	values, err := service.loadAll(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{
		SiteName:        defaultSiteName,
		Timezone:        defaultTimezone,
		DefaultPageSize: defaultPageSize,
		TrustedProxies:  []string{},
	}
	if value, ok := values["site_name"]; ok {
		snapshot.SiteName = value
	}
	if value, ok := values["site_description"]; ok {
		snapshot.SiteDescription = value
	}
	if value, ok := values["timezone"]; ok {
		snapshot.Timezone = value
	}
	if value, ok := values["default_page_size"]; ok {
		_, _ = fmt.Sscanf(value, "%d", &snapshot.DefaultPageSize)
	}
	if value, ok := values["ai_enabled"]; ok {
		snapshot.AIEnabled = value == "true"
	}
	if value, ok := values["ai_base_url"]; ok {
		snapshot.AIBaseURL = value
	}
	if value, ok := values["ai_model"]; ok {
		snapshot.AIModel = value
	}
	if value, ok := values["ai_api_key"]; ok && value != "" {
		snapshot.HasAIAPIKey = true
	}
	if value, ok := values["trusted_proxies"]; ok && value != "" {
		if err := json.Unmarshal([]byte(value), &snapshot.TrustedProxies); err != nil {
			return Snapshot{}, fmt.Errorf("decode trusted proxies: %w", err)
		}
	}
	return snapshot, nil
}

// Update 校验并落库；返回更新后的脱敏快照。
func (service *Service) Update(ctx context.Context, update Update) (Snapshot, error) {
	current, err := service.Get(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	next := current
	if update.SiteName != nil {
		name := strings.TrimSpace(*update.SiteName)
		if name == "" || utf8.RuneCountInString(name) > maxSiteNameRunes {
			return Snapshot{}, fmt.Errorf("%w: site_name must be 1-%d characters", ErrInvalidSettings, maxSiteNameRunes)
		}
		next.SiteName = name
	}
	if update.SiteDescription != nil {
		description := strings.TrimSpace(*update.SiteDescription)
		if utf8.RuneCountInString(description) > maxDescriptionRunes {
			return Snapshot{}, fmt.Errorf("%w: site_description must be at most %d characters", ErrInvalidSettings, maxDescriptionRunes)
		}
		next.SiteDescription = description
	}
	if update.Timezone != nil {
		if _, err := time.LoadLocation(strings.TrimSpace(*update.Timezone)); err != nil {
			return Snapshot{}, fmt.Errorf("%w: unknown timezone %q", ErrInvalidSettings, *update.Timezone)
		}
		next.Timezone = strings.TrimSpace(*update.Timezone)
	}
	if update.DefaultPageSize != nil {
		if *update.DefaultPageSize < minPageSize || *update.DefaultPageSize > maxPageSize {
			return Snapshot{}, fmt.Errorf("%w: default_page_size must be %d-%d", ErrInvalidSettings, minPageSize, maxPageSize)
		}
		next.DefaultPageSize = *update.DefaultPageSize
	}
	if update.AIEnabled != nil {
		next.AIEnabled = *update.AIEnabled
	}
	if update.AIBaseURL != nil {
		baseURL := strings.TrimSpace(*update.AIBaseURL)
		if baseURL != "" {
			parsed, err := url.Parse(baseURL)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
				return Snapshot{}, fmt.Errorf("%w: ai_base_url must be an absolute http(s) URL", ErrInvalidSettings)
			}
		}
		next.AIBaseURL = baseURL
	}
	if update.AIModel != nil {
		next.AIModel = strings.TrimSpace(*update.AIModel)
	}
	if update.TrustedProxies != nil {
		if len(*update.TrustedProxies) > 32 {
			return Snapshot{}, fmt.Errorf("%w: trusted_proxies accepts at most 32 entries", ErrInvalidSettings)
		}
		next.TrustedProxies = make([]string, 0, len(*update.TrustedProxies))
		seen := map[string]struct{}{}
		for _, raw := range *update.TrustedProxies {
			value := strings.TrimSpace(raw)
			if value == "" {
				continue
			}
			canonical := ""
			if prefix, err := netip.ParsePrefix(value); err == nil {
				canonical = prefix.Masked().String()
			} else if address, err := netip.ParseAddr(value); err == nil {
				canonical = address.Unmap().String()
			} else {
				return Snapshot{}, fmt.Errorf("%w: trusted proxy %q must be an IP address or CIDR", ErrInvalidSettings, value)
			}
			if _, exists := seen[canonical]; !exists {
				next.TrustedProxies = append(next.TrustedProxies, canonical)
				seen[canonical] = struct{}{}
			}
		}
	}
	trustedProxiesJSON, err := json.Marshal(next.TrustedProxies)
	if err != nil {
		return Snapshot{}, fmt.Errorf("encode trusted proxies: %w", err)
	}

	writes := map[string]string{
		"site_name":         next.SiteName,
		"site_description":  next.SiteDescription,
		"timezone":          next.Timezone,
		"default_page_size": fmt.Sprintf("%d", next.DefaultPageSize),
		"ai_enabled":        fmt.Sprintf("%t", next.AIEnabled),
		"ai_base_url":       next.AIBaseURL,
		"ai_model":          next.AIModel,
		"trusted_proxies":   string(trustedProxiesJSON),
	}
	for key, value := range writes {
		if err := service.write(ctx, key, value); err != nil {
			return Snapshot{}, err
		}
	}
	if update.AIAPIKey != nil {
		key := strings.TrimSpace(*update.AIAPIKey)
		if key == "" {
			if _, err := service.db.ExecContext(ctx, `DELETE FROM site_settings WHERE key = 'ai_api_key'`); err != nil {
				return Snapshot{}, fmt.Errorf("clear ai api key: %w", err)
			}
		} else {
			encrypted, err := service.cipher.Encrypt(key)
			if err != nil {
				return Snapshot{}, fmt.Errorf("encrypt ai api key: %w", err)
			}
			if err := service.write(ctx, "ai_api_key", encrypted); err != nil {
				return Snapshot{}, err
			}
		}
	}
	return service.Get(ctx)
}

func (service *Service) write(ctx context.Context, key, value string) error {
	_, err := service.db.ExecContext(ctx, `INSERT INTO site_settings (key, json_value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET json_value = excluded.json_value, updated_at = excluded.updated_at`,
		key, value, service.now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("write setting %q: %w", key, err)
	}
	return nil
}

func (service *Service) loadAll(ctx context.Context) (map[string]string, error) {
	rows, err := service.db.QueryContext(ctx, `SELECT key, json_value FROM site_settings`)
	if err != nil {
		return nil, fmt.Errorf("load site settings: %w", err)
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan site setting: %w", err)
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read site settings: %w", err)
	}
	return values, nil
}
