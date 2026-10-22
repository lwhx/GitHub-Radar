package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

var configEnvKeys = []string{
	"APP_ENV",
	"APP_PORT",
	"DATABASE_PATH",
	"PUBLIC_BASE_URL",
	"APP_SECRET",
	"APP_ENCRYPTION_KEY",
	"APP_TIMEZONE",
	"STATIC_DIR",
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range configEnvKeys {
		t.Setenv(key, "")
	}
}

func setValidProductionEnv(t *testing.T) {
	t.Helper()
	clearConfigEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("PUBLIC_BASE_URL", "https://radar.example.com")
	t.Setenv("APP_SECRET", strings.Repeat("s", 32))
	t.Setenv("APP_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	t.Setenv("APP_TIMEZONE", "Asia/Shanghai")
}

func TestLoadDevelopmentDefaults(t *testing.T) {
	clearConfigEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.AppEnv != "development" {
		t.Errorf("AppEnv = %q, want development", cfg.AppEnv)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.DatabasePath != "./data/radar.db" {
		t.Errorf("DatabasePath = %q, want ./data/radar.db", cfg.DatabasePath)
	}
	if cfg.Timezone != "Asia/Shanghai" {
		t.Errorf("Timezone = %q, want Asia/Shanghai", cfg.Timezone)
	}
	if cfg.StaticDir != "../frontend/dist" {
		t.Errorf("StaticDir = %q, want ../frontend/dist", cfg.StaticDir)
	}
	if cfg.AppSecret == "" || len(cfg.EncryptionKey) != 32 {
		t.Error("development keys were not generated")
	}
	if cfg.CookieSecure {
		t.Error("CookieSecure = true, want false for default development URL")
	}
	if len(cfg.Warnings) != 2 {
		t.Errorf("len(Warnings) = %d, want 2", len(cfg.Warnings))
	}
}

func TestLoadProductionConfiguration(t *testing.T) {
	setValidProductionEnv(t)
	t.Setenv("APP_PORT", "9090")
	t.Setenv("DATABASE_PATH", "/app/data/radar.db")
	t.Setenv("STATIC_DIR", "/app/static")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Port != 9090 || cfg.DatabasePath != "/app/data/radar.db" || cfg.StaticDir != "/app/static" {
		t.Errorf("overrides not loaded: %+v", cfg)
	}
	if !cfg.CookieSecure {
		t.Error("CookieSecure = false, want true in production")
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", cfg.Warnings)
	}
}

func TestLoadUsesSecureCookieForHTTPSDevelopmentURL(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("PUBLIC_BASE_URL", "https://radar.example.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.CookieSecure {
		t.Error("CookieSecure = false, want true for HTTPS URL")
	}
}

func TestLoadRejectsInvalidProductionConfiguration(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{name: "missing public URL", key: "PUBLIC_BASE_URL", value: "", want: "PUBLIC_BASE_URL"},
		{name: "relative public URL", key: "PUBLIC_BASE_URL", value: "/radar", want: "PUBLIC_BASE_URL"},
		{name: "unsupported public URL scheme", key: "PUBLIC_BASE_URL", value: "ftp://radar.example.com", want: "PUBLIC_BASE_URL"},
		{name: "missing secret", key: "APP_SECRET", value: "", want: "APP_SECRET"},
		{name: "short secret", key: "APP_SECRET", value: "too-short", want: "APP_SECRET"},
		{name: "short multibyte secret", key: "APP_SECRET", value: strings.Repeat("密", 16), want: "APP_SECRET"},
		{name: "example secret", key: "APP_SECRET", value: "replace-with-long-random-secret", want: "APP_SECRET"},
		{name: "missing encryption key", key: "APP_ENCRYPTION_KEY", value: "", want: "APP_ENCRYPTION_KEY"},
		{name: "invalid encryption key encoding", key: "APP_ENCRYPTION_KEY", value: "not-base64", want: "APP_ENCRYPTION_KEY"},
		{name: "wrong encryption key length", key: "APP_ENCRYPTION_KEY", value: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 31))), want: "APP_ENCRYPTION_KEY"},
		{name: "invalid timezone", key: "APP_TIMEZONE", value: "Mars/Olympus", want: "APP_TIMEZONE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setValidProductionEnv(t)
			t.Setenv(tt.key, tt.value)

			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load() error = %v, want error containing %q", err, tt.want)
			}
		})
	}
}
