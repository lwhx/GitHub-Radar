package classify_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/github-radar/github-radar/internal/classify"
)

func insertRepo(t *testing.T, database *sql.DB, fullName, description, language string, topics []string) int64 {
	t.Helper()
	owner, name, _ := strings.Cut(fullName, "/")
	topicsJSON, err := json.Marshal(topics)
	if err != nil {
		t.Fatalf("marshal topics: %v", err)
	}
	var descriptionValue, languageValue any
	if description != "" {
		descriptionValue = description
	}
	if language != "" {
		languageValue = language
	}
	result, err := database.Exec(`INSERT INTO repositories (
		github_id, owner, name, full_name, html_url, topics_json, description, language,
		github_created_at, github_updated_at, first_seen_at, sync_status
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, '2026-10-23T00:00:00Z', '2026-10-23T00:00:00Z', '2026-10-23T00:00:00Z', 'ok')`,
		nextGithubID(database), owner, name, fullName, "https://example.test/"+fullName, string(topicsJSON), descriptionValue, languageValue)
	if err != nil {
		t.Fatalf("insert repository %s: %v", fullName, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read repository id: %v", err)
	}
	return id
}

func nextGithubID(database *sql.DB) int64 {
	var maxID sql.NullInt64
	if err := database.QueryRow(`SELECT MAX(github_id) FROM repositories`).Scan(&maxID); err != nil {
		return 1
	}
	return maxID.Int64 + 1
}

func assignedCategories(t *testing.T, database *sql.DB, repoID int64) map[string]int {
	t.Helper()
	rows, err := database.Query(`SELECT c.slug, rc.is_primary FROM repo_categories rc
		JOIN categories c ON c.id = rc.category_id WHERE rc.repo_id = ? ORDER BY c.slug`, repoID)
	if err != nil {
		t.Fatalf("query repo_categories: %v", err)
	}
	defer rows.Close()
	assigned := map[string]int{}
	for rows.Next() {
		var slug string
		var primary int
		if err := rows.Scan(&slug, &primary); err != nil {
			t.Fatalf("scan assignment: %v", err)
		}
		assigned[slug] = primary
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate assignments: %v", err)
	}
	return assigned
}

func TestBackfillClassifiesRepositoriesAndIsIdempotent(t *testing.T) {
	t.Parallel()

	store := openTestDB(t)
	database := store.SQL()
	first := insertRepo(t, database, "me/mcp-gateway", "Self-hosted MCP server", "Go", []string{"mcp"})
	second := insertRepo(t, database, "me/daily", "一个效率工具：待办清单和笔记", "", nil)
	insertRepo(t, database, "me/unknown", "", "", nil)

	engine, err := classify.LoadEngine(context.Background(), database)
	if err != nil {
		t.Fatalf("LoadEngine() error = %v", err)
	}
	result, err := classify.Backfill(context.Background(), database, engine, 2)
	if err != nil {
		t.Fatalf("Backfill() error = %v", err)
	}
	if result.Scanned != 3 || result.Categorized != 2 || result.Unclassified != 1 {
		t.Fatalf("Backfill() result = %+v, want scanned 3, categorized 2, unclassified 1", result)
	}

	if assigned := assignedCategories(t, database, first); assigned["mcp"] != 1 || assigned["self-hosted"] != 0 || assigned["backend"] != 0 || len(assigned) != 3 {
		t.Fatalf("first repo assignments = %v, want mcp primary + self-hosted + backend", assigned)
	}
	if assigned := assignedCategories(t, database, second); assigned["productivity"] != 1 || len(assigned) != 1 {
		t.Fatalf("second repo assignments = %v, want productivity primary only", assigned)
	}

	counts, err := classify.CategoryCounts(context.Background(), database)
	if err != nil {
		t.Fatalf("CategoryCounts() error = %v", err)
	}
	if len(counts) != 14 {
		t.Fatalf("CategoryCounts() length = %d, want 14", len(counts))
	}
	bySlug := map[string]int{}
	for _, count := range counts {
		bySlug[count.Slug] = count.Repos
	}
	if bySlug["mcp"] != 1 || bySlug["self-hosted"] != 1 || bySlug["backend"] != 1 || bySlug["productivity"] != 1 || bySlug["frontend"] != 0 {
		t.Fatalf("per-category counts = %v", bySlug)
	}
	if counts[0].Slug != "ai" || counts[13].Slug != "backend" {
		t.Fatalf("counts not ordered by sort_order: first = %s, last = %s", counts[0].Slug, counts[13].Slug)
	}

	// 重复回填必须幂等：不新增归类行，统计数量不变。
	repeat, err := classify.Backfill(context.Background(), database, engine, 500)
	if err != nil {
		t.Fatalf("second Backfill() error = %v", err)
	}
	if repeat.Scanned != 3 || repeat.Assignments != 0 || repeat.Categorized != 2 {
		t.Fatalf("second Backfill() result = %+v, want scanned 3, assignments 0, categorized 2", repeat)
	}
	var total int
	if err := database.QueryRow(`SELECT COUNT(*) FROM repo_categories`).Scan(&total); err != nil {
		t.Fatalf("count repo_categories: %v", err)
	}
	if total != 4 {
		t.Fatalf("repo_categories rows after second backfill = %d, want 4", total)
	}
}

func TestClassifyManyReconcilesChangedRulesAndDeduplicatesRepositories(t *testing.T) {
	t.Parallel()

	store := openTestDB(t)
	database := store.SQL()
	repoID := insertRepo(t, database, "me/relay", "Telegram bot running in docker", "", []string{"telegram", "docker"})
	engine, err := classify.LoadEngine(context.Background(), database)
	if err != nil {
		t.Fatalf("LoadEngine() error = %v", err)
	}
	if _, err := database.Exec(`
		CREATE TABLE classify_write_audit (operation TEXT NOT NULL);
		CREATE TRIGGER audit_repo_category_insert AFTER INSERT ON repo_categories
		BEGIN INSERT INTO classify_write_audit(operation) VALUES ('insert'); END;
		CREATE TRIGGER audit_repo_category_update AFTER UPDATE ON repo_categories
		BEGIN INSERT INTO classify_write_audit(operation) VALUES ('update'); END;
	`); err != nil {
		t.Fatalf("create classification write audit: %v", err)
	}
	if err := classify.ClassifyMany(context.Background(), database, engine, []int64{repoID, repoID}); err != nil {
		t.Fatalf("initial ClassifyMany() error = %v", err)
	}
	var initialWrites int
	if err := database.QueryRow(`SELECT COUNT(*) FROM classify_write_audit`).Scan(&initialWrites); err != nil {
		t.Fatalf("count initial classification writes: %v", err)
	}
	if initialWrites != 2 {
		t.Fatalf("classification writes for duplicate repository IDs = %d, want 2 (one per matched category)", initialWrites)
	}
	if assigned := assignedCategories(t, database, repoID); assigned["docker"] != 1 || assigned["telegram"] != 0 || len(assigned) != 2 {
		t.Fatalf("initial assignments = %v, want docker primary + telegram", assigned)
	}

	if _, err := database.Exec(`UPDATE category_rules SET priority = 20
		WHERE category_id = (SELECT id FROM categories WHERE slug = 'telegram') AND match_type = 'topic' AND pattern = 'telegram'`); err != nil {
		t.Fatalf("raise telegram priority: %v", err)
	}
	engine, err = classify.LoadEngine(context.Background(), database)
	if err != nil {
		t.Fatalf("reload engine: %v", err)
	}
	if err := classify.ClassifyMany(context.Background(), database, engine, []int64{repoID, repoID}); err != nil {
		t.Fatalf("ClassifyMany() after priority change error = %v", err)
	}
	if assigned := assignedCategories(t, database, repoID); assigned["telegram"] != 1 || assigned["docker"] != 0 || len(assigned) != 2 {
		t.Fatalf("assignments after priority change = %v, want telegram primary + docker secondary", assigned)
	}

	if _, err := database.Exec(`UPDATE category_rules SET enabled = 0
		WHERE category_id = (SELECT id FROM categories WHERE slug = 'docker')`); err != nil {
		t.Fatalf("disable docker rules: %v", err)
	}
	engine, err = classify.LoadEngine(context.Background(), database)
	if err != nil {
		t.Fatalf("reload engine after disable: %v", err)
	}
	if err := classify.ClassifyMany(context.Background(), database, engine, []int64{repoID, repoID}); err != nil {
		t.Fatalf("ClassifyMany() after disabling rules error = %v", err)
	}
	if assigned := assignedCategories(t, database, repoID); assigned["telegram"] != 1 || len(assigned) != 1 {
		t.Fatalf("assignments after disabling docker = %v, want only telegram primary", assigned)
	}
	if _, err := database.Exec(`DELETE FROM classify_write_audit`); err != nil {
		t.Fatalf("clear classification write audit: %v", err)
	}
	if err := classify.ClassifyMany(context.Background(), database, engine, []int64{repoID, repoID}); err != nil {
		t.Fatalf("idempotent ClassifyMany() error = %v", err)
	}
	if assigned := assignedCategories(t, database, repoID); assigned["telegram"] != 1 || len(assigned) != 1 {
		t.Fatalf("assignments after repeat = %v, want only telegram primary", assigned)
	}
	var repeatWrites int
	if err := database.QueryRow(`SELECT COUNT(*) FROM classify_write_audit`).Scan(&repeatWrites); err != nil {
		t.Fatalf("count repeated classification writes: %v", err)
	}
	if repeatWrites != 0 {
		t.Fatalf("classification writes on idempotent repeat = %d, want 0", repeatWrites)
	}
}

func TestClassifyManyManualAssignmentOverridesAndRemovesAutoAssignments(t *testing.T) {
	t.Parallel()

	store := openTestDB(t)
	database := store.SQL()
	repoID := insertRepo(t, database, "me/tunnel-page", "Deploy on Cloudflare Pages with vue", "JavaScript", []string{"cloudflare-pages"})
	engine, err := classify.LoadEngine(context.Background(), database)
	if err != nil {
		t.Fatalf("LoadEngine() error = %v", err)
	}
	if err := classify.ClassifyMany(context.Background(), database, engine, []int64{repoID}); err != nil {
		t.Fatalf("initial ClassifyMany() error = %v", err)
	}
	if _, err := database.Exec(`INSERT INTO repo_categories (repo_id, category_id, is_primary, source)
		VALUES (?, (SELECT id FROM categories WHERE slug = 'security'), 1, 'manual')`, repoID); err != nil {
		t.Fatalf("insert manual assignment: %v", err)
	}

	if err := classify.ClassifyMany(context.Background(), database, engine, []int64{repoID}); err != nil {
		t.Fatalf("ClassifyMany() with manual assignment error = %v", err)
	}
	if assigned := assignedCategories(t, database, repoID); assigned["security"] != 1 || len(assigned) != 1 {
		t.Fatalf("assignments = %v, want only manual security primary", assigned)
	}
	var source string
	if err := database.QueryRow(`SELECT source FROM repo_categories WHERE repo_id = ?`, repoID).Scan(&source); err != nil {
		t.Fatalf("read remaining assignment source: %v", err)
	}
	if source != "manual" {
		t.Fatalf("remaining assignment source = %q, want manual", source)
	}
}

func TestClassifyOneIsIdempotentAndKeepsManualAssignments(t *testing.T) {
	t.Parallel()

	store := openTestDB(t)
	database := store.SQL()
	engine, err := classify.LoadEngine(context.Background(), database)
	if err != nil {
		t.Fatalf("LoadEngine() error = %v", err)
	}

	bot := insertRepo(t, database, "me/relay", "Telegram bot running in docker", "", []string{"telegram", "docker"})
	if err := classify.ClassifyOne(context.Background(), database, engine, bot); err != nil {
		t.Fatalf("ClassifyOne() error = %v", err)
	}
	if assigned := assignedCategories(t, database, bot); assigned["docker"] != 1 || assigned["telegram"] != 0 || len(assigned) != 2 {
		t.Fatalf("bot assignments = %v, want docker primary + telegram", assigned)
	}
	if err := classify.ClassifyOne(context.Background(), database, engine, bot); err != nil {
		t.Fatalf("second ClassifyOne() error = %v", err)
	}
	if assigned := assignedCategories(t, database, bot); len(assigned) != 2 {
		t.Fatalf("assignments after repeat = %v, want exactly 2 rows", assigned)
	}

	page := insertRepo(t, database, "me/tunnel-page", "Deploy on Cloudflare Pages with vue", "JavaScript", []string{"cloudflare-pages"})
	if _, err := database.Exec(`INSERT INTO repo_categories (repo_id, category_id, is_primary, source)
		VALUES (?, (SELECT id FROM categories WHERE slug = 'frontend'), 1, 'manual')`, page); err != nil {
		t.Fatalf("insert manual assignment: %v", err)
	}
	if err := classify.ClassifyOne(context.Background(), database, engine, page); err != nil {
		t.Fatalf("ClassifyOne() for page error = %v", err)
	}
	var source string
	var primary int
	if err := database.QueryRow(`SELECT source, is_primary FROM repo_categories
		WHERE repo_id = ? AND category_id = (SELECT id FROM categories WHERE slug = 'frontend')`, page).Scan(&source, &primary); err != nil {
		t.Fatalf("read manual assignment: %v", err)
	}
	if source != "manual" || primary != 1 {
		t.Fatalf("manual assignment overwritten: source = %q, primary = %d", source, primary)
	}
	if assigned := assignedCategories(t, database, page); assigned["frontend"] != 1 || len(assigned) != 1 {
		t.Fatalf("page assignments = %v, want only manual frontend primary", assigned)
	}
}

func TestClassifyOneMissingRepositoryIsNoop(t *testing.T) {
	t.Parallel()

	store := openTestDB(t)
	database := store.SQL()
	engine, err := classify.LoadEngine(context.Background(), database)
	if err != nil {
		t.Fatalf("LoadEngine() error = %v", err)
	}
	if err := classify.ClassifyOne(context.Background(), database, engine, 9999); err != nil {
		t.Fatalf("ClassifyOne() for missing repo error = %v", err)
	}
	var total int
	if err := database.QueryRow(`SELECT COUNT(*) FROM repo_categories`).Scan(&total); err != nil {
		t.Fatalf("count repo_categories: %v", err)
	}
	if total != 0 {
		t.Fatalf("repo_categories rows = %d, want 0", total)
	}
}
