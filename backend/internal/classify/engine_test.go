package classify_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/github-radar/github-radar/internal/classify"
	appdb "github.com/github-radar/github-radar/internal/db"
	"github.com/github-radar/github-radar/migrations"
)

func openTestDB(t *testing.T) *appdb.DB {
	t.Helper()
	store, err := appdb.Open(context.Background(), filepath.Join(t.TempDir(), "radar.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return store
}

func TestDefaultCategoriesSeedIsIdempotent(t *testing.T) {
	t.Parallel()

	store := openTestDB(t)
	database := store.SQL()

	wantSlugs := []string{
		"ai", "ai-agent", "mcp", "docker", "self-hosted", "nas", "cloudflare",
		"telegram", "dev-tools", "productivity", "security", "data-database",
		"frontend", "backend",
	}
	rows, err := database.Query(`SELECT slug, sort_order FROM categories ORDER BY sort_order, id`)
	if err != nil {
		t.Fatalf("query categories: %v", err)
	}
	defer rows.Close()
	var gotSlugs []string
	var gotOrders []int
	for rows.Next() {
		var slug string
		var order int
		if err := rows.Scan(&slug, &order); err != nil {
			t.Fatalf("scan category: %v", err)
		}
		gotSlugs = append(gotSlugs, slug)
		gotOrders = append(gotOrders, order)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate categories: %v", err)
	}
	if len(gotSlugs) != len(wantSlugs) {
		t.Fatalf("seeded category count = %d, want %d (%v)", len(gotSlugs), len(wantSlugs), gotSlugs)
	}
	for index, want := range wantSlugs {
		if gotSlugs[index] != want {
			t.Errorf("category at sort position %d = %q, want %q", index+1, gotSlugs[index], want)
		}
		if gotOrders[index] != index+1 {
			t.Errorf("category %q sort_order = %d, want %d", gotSlugs[index], gotOrders[index], index+1)
		}
	}

	var rulesBefore, rulesAfter int
	if err := database.QueryRow(`SELECT COUNT(*) FROM category_rules`).Scan(&rulesBefore); err != nil {
		t.Fatalf("count rules: %v", err)
	}
	if rulesBefore == 0 {
		t.Fatal("seed must insert rules, got 0")
	}

	// 再次执行迁移 SQL 本体：不允许出现重复分类或重复规则。
	seedSQL, err := migrations.Files.ReadFile("0006_default_categories.sql")
	if err != nil {
		t.Fatalf("read seed migration: %v", err)
	}
	if _, err := database.Exec(string(seedSQL)); err != nil {
		t.Fatalf("re-execute seed migration: %v", err)
	}

	var categories int
	if err := database.QueryRow(`SELECT COUNT(*) FROM categories`).Scan(&categories); err != nil {
		t.Fatalf("count categories: %v", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM category_rules`).Scan(&rulesAfter); err != nil {
		t.Fatalf("count rules after reseed: %v", err)
	}
	if categories != len(wantSlugs) {
		t.Errorf("category count after reseed = %d, want %d", categories, len(wantSlugs))
	}
	if rulesAfter != rulesBefore {
		t.Errorf("rule count changed after reseed: before = %d, after = %d", rulesBefore, rulesAfter)
	}
}

func TestClassifyRepoMatchesTopicsKeywordsAndLanguages(t *testing.T) {
	t.Parallel()

	store := openTestDB(t)
	engine, err := classify.LoadEngine(context.Background(), store.SQL())
	if err != nil {
		t.Fatalf("LoadEngine() error = %v", err)
	}

	tests := []struct {
		name string
		repo classify.Repo
		want []string
	}{
		{
			name: "topic 精确命中",
			repo: classify.Repo{Name: "server", FullName: "me/server", Topics: []string{"mcp"}},
			want: []string{"mcp"},
		},
		{
			name: "topic 大小写不敏感",
			repo: classify.Repo{Name: "stack", FullName: "me/stack", Topics: []string{"Docker"}},
			want: []string{"docker"},
		},
		{
			name: "keyword 名称大小写不敏感",
			repo: classify.Repo{Name: "FreeChatGPT", FullName: "me/FreeChatGPT"},
			want: []string{"ai"},
		},
		{
			name: "keyword 中文描述命中",
			repo: classify.Repo{Name: "marks", FullName: "me/marks", Description: "自托管的书签管理工具"},
			want: []string{"self-hosted"},
		},
		{
			name: "language 大小写不敏感精确匹配",
			repo: classify.Repo{Name: "svc", FullName: "me/svc", Language: "go"},
			want: []string{"backend"},
		},
		{
			name: "language 首字母大写写法",
			repo: classify.Repo{Name: "web", FullName: "me/web", Language: "JavaScript"},
			want: []string{"frontend"},
		},
		{
			name: "无命中返回空",
			repo: classify.Repo{Name: "random-thing", FullName: "me/random-thing", Description: "misc"},
			want: nil,
		},
		{
			name: "多分类按优先级排序",
			repo: classify.Repo{
				Name: "backup", FullName: "me/backup", Topics: []string{"docker"},
				Description: "PostgreSQL 数据库备份工具", Language: "Python",
			},
			want: []string{"docker", "data-database", "backend"},
		},
		{
			name: "同优先级取 sort_order 较小者为主分类",
			repo: classify.Repo{
				Name: "vault", FullName: "me/vault",
				Description: "self-hosted password manager", Language: "go",
			},
			want: []string{"self-hosted", "security", "backend"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			matches := engine.ClassifyRepo(test.repo)
			if len(matches) != len(test.want) {
				t.Fatalf("ClassifyRepo() slugs = %v, want %v", matchSlugs(matches), test.want)
			}
			for index, match := range matches {
				if match.Slug != test.want[index] {
					t.Errorf("match[%d] slug = %q, want %q", index, match.Slug, test.want[index])
				}
				if match.IsPrimary != (index == 0) {
					t.Errorf("match[%d] %s is_primary = %v", index, match.Slug, match.IsPrimary)
				}
			}
		})
	}
}

func matchSlugs(matches []classify.Match) []string {
	slugs := make([]string, 0, len(matches))
	for _, match := range matches {
		slugs = append(slugs, match.Slug)
	}
	return slugs
}

func TestEngineSkipsDisabledRulesAndCategories(t *testing.T) {
	t.Parallel()

	store := openTestDB(t)
	database := store.SQL()
	if _, err := database.Exec(`UPDATE categories SET enabled = 0 WHERE slug = 'telegram'`); err != nil {
		t.Fatalf("disable category: %v", err)
	}
	if _, err := database.Exec(`UPDATE category_rules SET enabled = 0
		WHERE category_id = (SELECT id FROM categories WHERE slug = 'ai') AND pattern = 'ollama'`); err != nil {
		t.Fatalf("disable rule: %v", err)
	}
	engine, err := classify.LoadEngine(context.Background(), database)
	if err != nil {
		t.Fatalf("LoadEngine() error = %v", err)
	}

	if matches := engine.ClassifyRepo(classify.Repo{Name: "tg", FullName: "me/tg", Topics: []string{"telegram"}}); len(matches) != 0 {
		t.Errorf("disabled category still matched: %v", matchSlugs(matches))
	}
	if matches := engine.ClassifyRepo(classify.Repo{Name: "locallama", FullName: "me/locallama", Description: "Run ollama models locally"}); len(matches) != 0 {
		t.Errorf("disabled rule still matched: %v", matchSlugs(matches))
	}
}
