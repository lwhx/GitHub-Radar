package collector

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/github-radar/github-radar/internal/github"
)

func TestProcessPendingAssignsAutoCategoriesToStoredRepositories(t *testing.T) {
	t.Parallel()

	searcher := newFakeSearcher()
	from := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	item := repo(7, "delta/mcp-hub", 5)
	item.Topics = []string{"mcp", "self-hosted"}
	language := "Go"
	description := "Self-hosted MCP gateway"
	item.Language = &language
	item.Description = &description
	searcher.windows[windowKey(from, to)] = []page{{
		result: github.SearchResult{TotalCount: 1, Items: []github.Repository{item}},
	}}

	worker, database := newTestCollector(t, searcher)
	ctx := context.Background()
	if _, err := worker.EnsureWindow(ctx, from, to, 0); err != nil {
		t.Fatalf("EnsureWindow() error = %v", err)
	}
	summary, err := worker.ProcessPending(ctx, 0)
	if err != nil {
		t.Fatalf("ProcessPending() error = %v", err)
	}
	if summary.Created != 1 {
		t.Fatalf("summary = %+v", summary)
	}

	var repoID int64
	if err := database.QueryRow(`SELECT id FROM repositories WHERE github_id = 7`).Scan(&repoID); err != nil {
		t.Fatalf("load stored repository: %v", err)
	}
	assertAssignments(t, database, repoID, map[string]int{"mcp": 1, "self-hosted": 0, "backend": 0})

	// 重复归类必须幂等。
	worker.classifyStored(ctx, []int64{repoID})
	assertAssignments(t, database, repoID, map[string]int{"mcp": 1, "self-hosted": 0, "backend": 0})
}

func assertAssignments(t *testing.T, database *sql.DB, repoID int64, want map[string]int) {
	t.Helper()
	rows, err := database.Query(`SELECT c.slug, rc.is_primary FROM repo_categories rc
		JOIN categories c ON c.id = rc.category_id WHERE rc.repo_id = ? ORDER BY c.slug`, repoID)
	if err != nil {
		t.Fatalf("query repo_categories: %v", err)
	}
	defer rows.Close()
	got := map[string]int{}
	for rows.Next() {
		var slug string
		var primary int
		if err := rows.Scan(&slug, &primary); err != nil {
			t.Fatalf("scan assignment: %v", err)
		}
		got[slug] = primary
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate assignments: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("assignments = %v, want %v", got, want)
	}
	for slug, primary := range want {
		if got[slug] != primary {
			t.Fatalf("assignments = %v, want %v", got, want)
		}
	}
}
