package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	appdb "github.com/github-radar/github-radar/internal/db"
)

func insertClassifyFixtureRepo(t *testing.T, databasePath, fullName, topicsJSON string) {
	t.Helper()
	store, err := appdb.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	defer store.Close()
	owner, name, _ := strings.Cut(fullName, "/")
	if _, err := store.SQL().Exec(`INSERT INTO repositories (
		github_id, owner, name, full_name, html_url, topics_json,
		github_created_at, github_updated_at, first_seen_at, sync_status
	) VALUES (424242, ?, ?, ?, ?, ?, '2026-10-23T00:00:00Z', '2026-10-23T00:00:00Z', '2026-10-23T00:00:00Z', 'ok')`,
		owner, name, fullName, "https://example.test/"+fullName, topicsJSON); err != nil {
		t.Fatalf("insert fixture repository: %v", err)
	}
}

func TestClassifyBackfillPrintsCategoryCounts(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	databasePath := filepath.Join(t.TempDir(), "radar.db")
	t.Setenv("DATABASE_PATH", databasePath)
	insertClassifyFixtureRepo(t, databasePath, "me/mcp-gateway", `["mcp"]`)

	var output bytes.Buffer
	if err := run(context.Background(), []string{"classify", "backfill"}, strings.NewReader(""), &output, func(int) ([]byte, error) { return nil, nil }); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	got := output.String()
	if !strings.Contains(got, "mcp") || !strings.Contains(got, "self-hosted") {
		t.Fatalf("output = %q, want per-category counts", got)
	}
	if !strings.Contains(got, "1") {
		t.Fatalf("output = %q, want a matched repository count", got)
	}

	// 重复执行必须幂等。
	var repeat bytes.Buffer
	if err := run(context.Background(), []string{"classify", "backfill"}, strings.NewReader(""), &repeat, func(int) ([]byte, error) { return nil, nil }); err != nil {
		t.Fatalf("second run() error = %v", err)
	}
	if !strings.Contains(repeat.String(), "mcp") {
		t.Fatalf("second output = %q", repeat.String())
	}
}

func TestClassifyBackfillRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{{"classify"}, {"classify", "unknown"}, {"classify", "backfill", "extra"}} {
		var output bytes.Buffer
		err := run(context.Background(), args, strings.NewReader(""), &output, func(int) ([]byte, error) { return nil, nil })
		if err == nil || !strings.Contains(err.Error(), "usage") {
			t.Fatalf("run(%v) error = %v, want usage error", args, err)
		}
	}
}
