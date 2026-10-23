package api

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestAdminCategoryLifecycle(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	handler := newTestHandler(t, store)
	cookies := adminCookiesForLifecycle(t, handler, store)

	created := adminRequest(t, handler, http.MethodPost, "http://radar.test/api/v1/admin/categories",
		`{"slug":"ai-agents","title":"AI Agent","description":"智能体生态","sort_order":5,"enabled":true}`, cookies, true)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body=%q", created.Code, created.Body.String())
	}
	if !strings.Contains(created.Body.String(), `"slug":"ai-agents"`) || !strings.Contains(created.Body.String(), `"repo_count":0`) {
		t.Fatalf("create body = %q", created.Body.String())
	}

	list := adminRequest(t, handler, http.MethodGet, "http://radar.test/api/v1/admin/categories", "", cookies, false)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"ai-agents"`) {
		t.Fatalf("list status = %d; body=%q", list.Code, list.Body.String())
	}

	var id int64
	if err := store.SQL().QueryRow(`SELECT id FROM categories WHERE slug = 'ai-agents'`).Scan(&id); err != nil {
		t.Fatal(err)
	}

	patched := adminRequest(t, handler, http.MethodPatch, "http://radar.test/api/v1/admin/categories/"+strconv.FormatInt(id, 10),
		`{"title":"AI 智能体","enabled":false,"sort_order":9}`, cookies, true)
	if patched.Code != http.StatusOK || !strings.Contains(patched.Body.String(), `"enabled":false`) {
		t.Fatalf("patch status = %d; body=%q", patched.Code, patched.Body.String())
	}
	var title string
	var enabled int
	if err := store.SQL().QueryRow(`SELECT title, enabled FROM categories WHERE id = ?`, id).Scan(&title, &enabled); err != nil {
		t.Fatal(err)
	}
	if title != "AI 智能体" || enabled != 0 {
		t.Fatalf("stored title/enabled = %q/%d", title, enabled)
	}

	deleted := adminRequest(t, handler, http.MethodDelete, "http://radar.test/api/v1/admin/categories/"+strconv.FormatInt(id, 10), "", cookies, true)
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), `"deleted":true`) {
		t.Fatalf("delete status = %d; body=%q", deleted.Code, deleted.Body.String())
	}
	var remaining int
	if err := store.SQL().QueryRow(`SELECT COUNT(*) FROM categories WHERE id = ?`, id).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("category still present after delete: %d", remaining)
	}
}

func TestAdminCreateCategoryValidation(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	handler := newTestHandler(t, store)
	cookies := adminCookiesForLifecycle(t, handler, store)

	cases := []struct {
		name string
		body string
		code int
	}{
		{"bad slug", `{"slug":"Bad Slug","title":"x"}`, http.StatusBadRequest},
		{"empty title", `{"slug":"ok-slug","title":"   "}`, http.StatusBadRequest},
		{"long description", `{"slug":"ok-slug-2","title":"t","description":"` + strings.Repeat("a", 501) + `"}`, http.StatusBadRequest},
	}
	for _, item := range cases {
		recorder := adminRequest(t, handler, http.MethodPost, "http://radar.test/api/v1/admin/categories", item.body, cookies, true)
		if recorder.Code != item.code || !strings.Contains(recorder.Body.String(), "INVALID_CATEGORY") {
			t.Fatalf("%s status = %d; body=%q", item.name, recorder.Code, recorder.Body.String())
		}
	}

	seedCategory(t, store, 21, "dup", "Dup", 1, true)
	conflict := adminRequest(t, handler, http.MethodPost, "http://radar.test/api/v1/admin/categories",
		`{"slug":"dup","title":"重复分类"}`, cookies, true)
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "CONFLICT") {
		t.Fatalf("duplicate status = %d; body=%q", conflict.Code, conflict.Body.String())
	}
}

func TestAdminCategoryRequiresSessionAndCSRF(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	handler := newTestHandler(t, store)
	cookies := adminCookiesForLifecycle(t, handler, store)

	anonymous := adminRequest(t, handler, http.MethodGet, "http://radar.test/api/v1/admin/categories", "", nil, false)
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d", anonymous.Code)
	}
	noCSRF := adminRequest(t, handler, http.MethodPost, "http://radar.test/api/v1/admin/categories",
		`{"slug":"csrf-miss","title":"x"}`, cookies, false)
	if noCSRF.Code != http.StatusForbidden {
		t.Fatalf("no CSRF status = %d", noCSRF.Code)
	}
}

func TestAdminDeleteCategoryCascadesRepoLinks(t *testing.T) {
	store := newTestStore(t)
	createAdmin(t, store, "radar")
	handler := newTestHandler(t, store)
	cookies := adminCookiesForLifecycle(t, handler, store)

	seedCategory(t, store, 31, "cascade", "Cascade", 1, true)
	seedPublicRepo(t, store, 70001, "acme", "linked", 10)
	if _, err := store.SQL().Exec(`INSERT INTO repo_categories (repo_id, category_id, is_primary, source)
		SELECT r.id, 31, 0, 'auto' FROM repositories r WHERE r.github_id = 70001`); err != nil {
		t.Fatal(err)
	}

	if recorder := adminRequest(t, handler, http.MethodDelete, "http://radar.test/api/v1/admin/categories/31", "", cookies, true); recorder.Code != http.StatusOK {
		t.Fatalf("delete status = %d; body=%q", recorder.Code, recorder.Body.String())
	}
	var links int
	if err := store.SQL().QueryRow(`SELECT COUNT(*) FROM repo_categories WHERE category_id = 31`).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if links != 0 {
		t.Fatalf("repo links = %d, want 0 after cascade", links)
	}
}
