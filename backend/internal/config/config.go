package config

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultPort         = 8080
	defaultDatabasePath = "./data/radar.db"
	defaultTimezone     = "Asia/Shanghai"
	defaultStaticDir    = "../frontend/dist"
)

type Config struct {
	AppEnv        string
	Port          int
	DatabasePath  string
	PublicBaseURL string
	AppSecret     string
	EncryptionKey []byte
	Timezone      string
	StaticDir     string
	CookieSecure  bool
	Warnings      []string
}

func Load() (Config, error) {
	cfg := Config{
		AppEnv:        valueOrDefault("APP_ENV", "development"),
		DatabasePath:  valueOrDefault("DATABASE_PATH", defaultDatabasePath),
		PublicBaseURL: os.Getenv("PUBLIC_BASE_URL"),
		AppSecret:     os.Getenv("APP_SECRET"),
		Timezone:      valueOrDefault("APP_TIMEZONE", defaultTimezone),
		StaticDir:     valueOrDefault("STATIC_DIR", defaultStaticDir),
	}

	port, err := loadPort()
	if err != nil {
		return Config{}, err
	}
	cfg.Port = port

	switch cfg.AppEnv {
	case "development", "production":
	default:
		return Config{}, fmt.Errorf("APP_ENV must be either development or production")
	}

	if _, err := time.LoadLocation(cfg.Timezone); err != nil {
		return Config{}, fmt.Errorf("APP_TIMEZONE %q cannot be loaded: %w", cfg.Timezone, err)
	}

	production := cfg.AppEnv == "production"
	publicURL, err := loadPublicURL(cfg.PublicBaseURL, production)
	if err != nil {
		return Config{}, err
	}
	cfg.CookieSecure = production || publicURL != nil && publicURL.Scheme == "https"

	if cfg.AppSecret == "" {
		if production {
			return Config{}, fmt.Errorf("APP_SECRET is required in production")
		}
		cfg.AppSecret, err = randomBase64(32)
		if err != nil {
			return Config{}, fmt.Errorf("generate APP_SECRET: %w", err)
		}
		cfg.Warnings = append(cfg.Warnings, "APP_SECRET is unset; using an in-process temporary secret")
	}
	if production && (utf8.RuneCountInString(cfg.AppSecret) < 32 || isExampleValue(cfg.AppSecret)) {
		return Config{}, fmt.Errorf("APP_SECRET must be at least 32 characters and not an example value in production")
	}

	encryptionKey := os.Getenv("APP_ENCRYPTION_KEY")
	if encryptionKey == "" {
		if production {
			return Config{}, fmt.Errorf("APP_ENCRYPTION_KEY is required in production")
		}
		cfg.EncryptionKey = make([]byte, 32)
		if _, err := rand.Read(cfg.EncryptionKey); err != nil {
			return Config{}, fmt.Errorf("generate APP_ENCRYPTION_KEY: %w", err)
		}
		cfg.Warnings = append(cfg.Warnings, "APP_ENCRYPTION_KEY is unset; using an in-process temporary key")
	} else {
		if production && isExampleValue(encryptionKey) {
			return Config{}, fmt.Errorf("APP_ENCRYPTION_KEY must not be an example value in production")
		}
		cfg.EncryptionKey, err = base64.StdEncoding.DecodeString(encryptionKey)
		if err != nil || len(cfg.EncryptionKey) != 32 {
			return Config{}, fmt.Errorf("APP_ENCRYPTION_KEY must be Base64-encoded and decode to exactly 32 bytes")
		}
	}

	return cfg, nil
}

func loadPort() (int, error) {
	value := os.Getenv("APP_PORT")
	if value == "" {
		return defaultPort, nil
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("APP_PORT must be an integer between 1 and 65535")
	}
	return port, nil
}

func loadPublicURL(value string, required bool) (*url.URL, error) {
	if value == "" {
		if required {
			return nil, fmt.Errorf("PUBLIC_BASE_URL is required in production")
		}
		return nil, nil
	}

	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("PUBLIC_BASE_URL must be an absolute http or https URL")
	}
	return parsed, nil
}

func valueOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func randomBase64(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func isExampleValue(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(normalized, "replace-with-") ||
		strings.Contains(normalized, "example") ||
		strings.Contains(normalized, "changeme")
}
