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

func TestClassifyStoredRetriesRuleLoadingAfterFailure(t *testing.T) {
	t.Parallel()

	worker, database := newTestCollector(t, newFakeSearcher())
	ctx := context.Background()
	item := repo(70, "delta/mcp-retry", 1)
	item.Topics = []string{"mcp"}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin repository insert: %v", err)
	}
	repoID, _, _, err := upsertRepository(ctx, tx, item, formatTime(worker.now()))
	if err != nil {
		t.Fatalf("insert repository: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit repository insert: %v", err)
	}
	if _, err := database.Exec(`ALTER TABLE category_rules RENAME TO category_rules_unavailable`); err != nil {
		t.Fatalf("hide category_rules: %v", err)
	}
	worker.classifyStored(ctx, []int64{repoID})
	if _, err := database.Exec(`ALTER TABLE category_rules_unavailable RENAME TO category_rules`); err != nil {
		t.Fatalf("restore category_rules: %v", err)
	}

	worker.classifyStored(ctx, []int64{repoID})
	assertAssignments(t, database, repoID, map[string]int{"mcp": 1})
}

func TestClassifyStoredReloadsChangedRulesAndReconcilesAssignments(t *testing.T) {
	t.Parallel()

	worker, database := newTestCollector(t, newFakeSearcher())
	ctx := context.Background()
	item := repo(71, "delta/docker-tool", 1)
	item.Topics = []string{"docker"}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin repository insert: %v", err)
	}
	repoID, _, _, err := upsertRepository(ctx, tx, item, formatTime(worker.now()))
	if err != nil {
		t.Fatalf("insert repository: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit repository insert: %v", err)
	}

	worker.classifyStored(ctx, []int64{repoID})
	assertAssignments(t, database, repoID, map[string]int{"docker": 1})
	if _, err := database.Exec(`UPDATE category_rules SET enabled = 0
		WHERE category_id = (SELECT id FROM categories WHERE slug = 'docker')`); err != nil {
		t.Fatalf("disable docker rules: %v", err)
	}
	worker.classifyStored(ctx, []int64{repoID})
	assertAssignments(t, database, repoID, map[string]int{})
}

func TestClassifyStoredLoadsRulesAddedAfterEmptyBatch(t *testing.T) {
	t.Parallel()

	worker, database := newTestCollector(t, newFakeSearcher())
	ctx := context.Background()
	item := repo(74, "delta/later-rule", 1)
	item.Topics = []string{"later-topic"}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin repository insert: %v", err)
	}
	repoID, _, _, err := upsertRepository(ctx, tx, item, formatTime(worker.now()))
	if err != nil {
		t.Fatalf("insert repository: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit repository insert: %v", err)
	}
	if _, err := database.Exec(`UPDATE category_rules SET enabled = 0`); err != nil {
		t.Fatalf("disable all rules: %v", err)
	}
	worker.classifyStored(ctx, []int64{repoID})
	assertAssignments(t, database, repoID, map[string]int{})

	if _, err := database.Exec(`
		INSERT INTO categories (slug, title, sort_order, enabled) VALUES ('later', 'Later', 100, 1);
		INSERT INTO category_rules (category_id, match_type, pattern, priority, enabled)
		VALUES ((SELECT id FROM categories WHERE slug = 'later'), 'topic', 'later-topic', 10, 1);
	`); err != nil {
		t.Fatalf("insert later rule: %v", err)
	}
	worker.classifyStored(ctx, []int64{repoID})
	assertAssignments(t, database, repoID, map[string]int{"later": 1})
}

func TestProcessPendingClassifiesMultipleRepositoriesFromOnePage(t *testing.T) {
	t.Parallel()

	searcher := newFakeSearcher()
	from := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	mcp := repo(72, "delta/mcp-service", 2)
	mcp.Topics = []string{"mcp"}
	docker := repo(73, "delta/container", 3)
	docker.Topics = []string{"docker"}
	searcher.windows[windowKey(from, to)] = []page{{
		result: github.SearchResult{TotalCount: 2, Items: []github.Repository{mcp, docker}},
	}}
	worker, database := newTestCollector(t, searcher)
	ctx := context.Background()
	if _, err := worker.EnsureWindow(ctx, from, to, 0); err != nil {
		t.Fatalf("EnsureWindow() error = %v", err)
	}
	if _, err := worker.ProcessPending(ctx, 0); err != nil {
		t.Fatalf("ProcessPending() error = %v", err)
	}

	for githubID, want := range map[int64]map[string]int{
		72: {"mcp": 1},
		73: {"docker": 1},
	} {
		var repoID int64
		if err := database.QueryRow(`SELECT id FROM repositories WHERE github_id = ?`, githubID).Scan(&repoID); err != nil {
			t.Fatalf("load repository %d: %v", githubID, err)
		}
		assertAssignments(t, database, repoID, want)
	}
}

func TestProcessPendingRetriesSamePageAfterClassificationFailure(t *testing.T) {
	t.Parallel()

	searcher := newFakeSearcher()
	from := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	item := repo(75, "delta/retry-mcp", 2)
	item.Topics = []string{"mcp"}
	searcher.windows[windowKey(from, to)] = []page{{
		result: github.SearchResult{TotalCount: 1, Items: []github.Repository{item}},
	}}
	worker, database := newTestCollector(t, searcher)
	ctx := context.Background()
	windowID, err := worker.EnsureWindow(ctx, from, to, 0)
	if err != nil {
		t.Fatalf("EnsureWindow() error = %v", err)
	}
	if _, err := database.Exec(`ALTER TABLE category_rules RENAME TO category_rules_unavailable`); err != nil {
		t.Fatalf("hide category_rules: %v", err)
	}
	if _, err := worker.ProcessPending(ctx, 0); err == nil {
		t.Fatal("ProcessPending() succeeded while classification rules were unavailable")
	}
	var pageNumber int
	var status string
	if err := database.QueryRow(`SELECT page, coverage_status FROM collection_windows WHERE id = ?`, windowID).Scan(&pageNumber, &status); err != nil {
		t.Fatalf("read window after classification failure: %v", err)
	}
	if pageNumber != 1 || status != statusPending {
		t.Fatalf("window after classification failure = page %d status %q, want page 1 pending", pageNumber, status)
	}
	if _, err := database.Exec(`ALTER TABLE category_rules_unavailable RENAME TO category_rules`); err != nil {
		t.Fatalf("restore category_rules: %v", err)
	}
	if _, err := worker.ProcessPending(ctx, 0); err != nil {
		t.Fatalf("retry ProcessPending() error = %v", err)
	}
	var repoID int64
	if err := database.QueryRow(`SELECT id FROM repositories WHERE github_id = 75`).Scan(&repoID); err != nil {
		t.Fatalf("load retried repository: %v", err)
	}
	assertAssignments(t, database, repoID, map[string]int{"mcp": 1})
	if searcher.calls != 2 {
		t.Fatalf("search calls = %d, want 2 so the failed page was fetched again", searcher.calls)
	}
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
