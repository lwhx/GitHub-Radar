package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/github-radar/github-radar/internal/db"
)

func setRepoCreatedAt(t *testing.T, store *db.DB, githubID int64, at time.Time) {
	t.Helper()
	if _, err := store.SQL().Exec(`UPDATE repositories SET github_created_at = ? WHERE github_id = ?`,
		at.UTC().Format("2006-01-02T15:04:05Z"), githubID); err != nil {
		t.Fatalf("set created_at for %d: %v", githubID, err)
	}
}

func setRepoRankMeta(t *testing.T, store *db.DB, githubID int64, description, language, topicsJSON string) {
	t.Helper()
	if _, err := store.SQL().Exec(`UPDATE repositories SET description = ?, language = ?, topics_json = ? WHERE github_id = ?`,
		description, language, topicsJSON, githubID); err != nil {
		t.Fatalf("set metadata for %d: %v", githubID, err)
	}
}

func hideRankRepo(t *testing.T, store *db.DB, githubID int64) {
	t.Helper()
	if _, err := store.SQL().Exec(`UPDATE repositories SET is_hidden = 1 WHERE github_id = ?`, githubID); err != nil {
		t.Fatalf("hide repo %d: %v", githubID, err)
	}
}

func seedRankSnapshot(t *testing.T, store *db.DB, githubID int64, starCount int, at time.Time) {
	t.Helper()
	if _, err := store.SQL().Exec(`INSERT INTO star_snapshots (repo_id, star_count, captured_at, source, sample_status)
		SELECT id, ?, ?, 'search', 'ok' FROM repositories WHERE github_id = ?`,
		starCount, at.UTC().Format("2006-01-02T15:04:05Z"), githubID); err != nil {
		t.Fatalf("seed snapshot %d: %v", githubID, err)
	}
}

type rankingsPayload struct {
	Data struct {
		Items []struct {
			FullName    string   `json:"full_name"`
			StarsCount  int      `json:"stars_count"`
			StarDelta   *int     `json:"star_delta"`
			Score       *float64 `json:"score"`
			ScoreReason *string  `json:"score_reason"`
		} `json:"items"`
		Meta struct {
			Type             string  `json:"type"`
			WindowHours      int     `json:"window_hours"`
			Page             int     `json:"page"`
			Limit            int     `json:"limit"`
			Total            int     `json:"total"`
			DataUpdatedAt    *string `json:"data_updated_at"`
			HistoryAvailable bool    `json:"history_available"`
			Note             string  `json:"note"`
		} `json:"meta"`
	} `json:"data"`
}

func decodeRankings(t *testing.T, recorder *httptest.ResponseRecorder) rankingsPayload {
	t.Helper()
	var payload rankingsPayload
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode rankings: %v body=%s", err, recorder.Body.String())
	}
	return payload
}

func TestListRankingsLatestOrdersByGithubCreationDate(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()
	seedPublicRepo(t, store, 901, "acme", "mid-a", 10)
	seedPublicRepo(t, store, 903, "acme", "mid-b", 20)
	seedPublicRepo(t, store, 905, "acme", "old", 30)
	seedPublicRepo(t, store, 907, "acme", "hidden-new", 40)
	setRepoCreatedAt(t, store, 901, now.Add(-24*time.Hour))
	setRepoCreatedAt(t, store, 903, now.Add(-24*time.Hour))
	setRepoCreatedAt(t, store, 905, now.Add(-72*time.Hour))
	setRepoCreatedAt(t, store, 907, now)
	hideRankRepo(t, store, 907)
	handler := newTestHandler(t, store)

	recorder := request(t, handler, http.MethodGet, "/api/v1/rankings?type=latest&limit=10")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	payload := decodeRankings(t, recorder)
	if payload.Data.Meta.Total != 3 {
		t.Fatalf("total = %d, want 3 (hidden excluded)", payload.Data.Meta.Total)
	}
	if payload.Data.Meta.WindowHours != 0 || payload.Data.Meta.Type != "latest" {
		t.Fatalf("meta = %+v, want latest with window_hours 0", payload.Data.Meta)
	}
	want := []string{"acme/mid-a", "acme/mid-b", "acme/old"}
	if len(payload.Data.Items) != len(want) {
		t.Fatalf("items = %d, want %d", len(payload.Data.Items), len(want))
	}
	for index, name := range want {
		if payload.Data.Items[index].FullName != name {
			t.Fatalf("item %d = %s, want %s (same created_at tie-breaks by github_id ASC)",
				index, payload.Data.Items[index].FullName, name)
		}
	}
	if payload.Data.Items[0].StarDelta != nil {
		t.Fatalf("latest board must not report star_delta, got %v", *payload.Data.Items[0].StarDelta)
	}
}

func TestListRankingsComputesRollingStarGrowth(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()
	seedPublicRepo(t, store, 911, "acme", "hot", 180)
	seedPublicRepo(t, store, 912, "acme", "warm", 70)
	seedPublicRepo(t, store, 913, "acme", "fresh", 40)
	seedPublicRepo(t, store, 914, "acme", "hidden-hot", 999)
	seedRankSnapshot(t, store, 911, 100, now.Add(-25*time.Hour))
	seedRankSnapshot(t, store, 911, 180, now.Add(-1*time.Hour))
	seedRankSnapshot(t, store, 912, 50, now.Add(-28*time.Hour))
	seedRankSnapshot(t, store, 912, 70, now.Add(-2*time.Hour))
	seedRankSnapshot(t, store, 913, 40, now.Add(-1*time.Hour))
	seedRankSnapshot(t, store, 914, 100, now.Add(-25*time.Hour))
	seedRankSnapshot(t, store, 914, 999, now.Add(-1*time.Hour))
	hideRankRepo(t, store, 914)
	handler := newTestHandler(t, store)

	recorder := request(t, handler, http.MethodGet, "/api/v1/rankings?type=24h&limit=10")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	payload := decodeRankings(t, recorder)
	meta := payload.Data.Meta
	if meta.Type != "24h" || meta.WindowHours != 24 || meta.Total != 2 || !meta.HistoryAvailable {
		t.Fatalf("meta = %+v, want 24h/window24/total2/history", meta)
	}
	if meta.DataUpdatedAt == nil {
		t.Fatal("meta.data_updated_at must be set when snapshots exist")
	}
	if len(payload.Data.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(payload.Data.Items))
	}
	if payload.Data.Items[0].FullName != "acme/hot" || payload.Data.Items[0].StarDelta == nil || *payload.Data.Items[0].StarDelta != 80 {
		t.Fatalf("first item = %+v, want acme/hot +80", payload.Data.Items[0])
	}
	if payload.Data.Items[1].FullName != "acme/warm" || payload.Data.Items[1].StarDelta == nil || *payload.Data.Items[1].StarDelta != 20 {
		t.Fatalf("second item = %+v, want acme/warm +20", payload.Data.Items[1])
	}
	for _, item := range payload.Data.Items {
		if item.FullName == "acme/fresh" || item.FullName == "acme/hidden-hot" {
			t.Fatalf("ineligible repo leaked into ranking: %s", item.FullName)
		}
	}
}

func TestListRankingsFlagsInsufficientHistory(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()
	seedPublicRepo(t, store, 921, "acme", "lonely", 50)
	seedRankSnapshot(t, store, 921, 50, now.Add(-1*time.Hour))
	handler := newTestHandler(t, store)

	recorder := request(t, handler, http.MethodGet, "/api/v1/rankings?type=24h")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	payload := decodeRankings(t, recorder)
	if payload.Data.Meta.HistoryAvailable {
		t.Fatal("history_available must be false when no repo has a baseline snapshot")
	}
	if payload.Data.Meta.Total != 0 || len(payload.Data.Items) != 0 {
		t.Fatalf("total=%d items=%d, want empty board (not fake zeros)", payload.Data.Meta.Total, len(payload.Data.Items))
	}
	if !strings.Contains(payload.Data.Meta.Note, "数据不足") {
		t.Fatalf("note = %q, want an explicit insufficient-data explanation", payload.Data.Meta.Note)
	}
}

func TestListRankingsPotentialKeepsYoungGrowingRepos(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()

	seedPublicRepo(t, store, 931, "acme", "rising", 520)
	setRepoCreatedAt(t, store, 931, now.AddDate(0, 0, -10))
	setRepoRankMeta(t, store, 931, "一个快速上升的项目", "Go", `["ai","agents"]`)
	seedRankSnapshot(t, store, 931, 400, now.Add(-170*time.Hour))
	seedRankSnapshot(t, store, 931, 520, now.Add(-1*time.Hour))

	seedPublicRepo(t, store, 932, "acme", "mature", 500)
	setRepoCreatedAt(t, store, 932, now.AddDate(0, 0, -60))
	seedRankSnapshot(t, store, 932, 400, now.Add(-170*time.Hour))
	seedRankSnapshot(t, store, 932, 500, now.Add(-1*time.Hour))

	seedPublicRepo(t, store, 933, "acme", "huge", 4000)
	setRepoCreatedAt(t, store, 933, now.AddDate(0, 0, -10))
	seedRankSnapshot(t, store, 933, 3900, now.Add(-170*time.Hour))
	seedRankSnapshot(t, store, 933, 4000, now.Add(-1*time.Hour))

	seedPublicRepo(t, store, 934, "acme", "flat", 300)
	setRepoCreatedAt(t, store, 934, now.AddDate(0, 0, -10))
	seedRankSnapshot(t, store, 934, 300, now.Add(-170*time.Hour))
	seedRankSnapshot(t, store, 934, 300, now.Add(-1*time.Hour))
	handler := newTestHandler(t, store)

	recorder := request(t, handler, http.MethodGet, "/api/v1/rankings?type=potential")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	payload := decodeRankings(t, recorder)
	if payload.Data.Meta.Total != 1 || len(payload.Data.Items) != 1 {
		t.Fatalf("total=%d items=%d, want only the young growing repo", payload.Data.Meta.Total, len(payload.Data.Items))
	}
	item := payload.Data.Items[0]
	if item.FullName != "acme/rising" {
		t.Fatalf("item = %s, want acme/rising", item.FullName)
	}
	if item.StarDelta == nil || *item.StarDelta != 120 {
		t.Fatalf("star_delta = %v, want 120", item.StarDelta)
	}
	if item.Score == nil || *item.Score != 132 {
		t.Fatalf("score = %v, want 132 (120 * 1.10 completeness bonus)", item.Score)
	}
	if item.ScoreReason == nil || *item.ScoreReason == "" {
		t.Fatal("potential board must explain the score")
	}
}

func TestListRankingsDefaultsToLatestAndRejectsUnknownType(t *testing.T) {
	store := newTestStore(t)
	seedPublicRepo(t, store, 941, "acme", "one", 5)
	handler := newTestHandler(t, store)

	defaulted := request(t, handler, http.MethodGet, "/api/v1/rankings")
	if defaulted.Code != http.StatusOK {
		t.Fatalf("status = %d", defaulted.Code)
	}
	if payload := decodeRankings(t, defaulted); payload.Data.Meta.Type != "latest" {
		t.Fatalf("type = %q, want latest default", payload.Data.Meta.Type)
	}

	unknown := request(t, handler, http.MethodGet, "/api/v1/rankings?type=weekly")
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown type status = %d, want 400", unknown.Code)
	}
}

// 口径缺陷回归：基线快照必须在窗口截止时间附近（容差 = 一个同步周期 6h）。
// 否则 5 天前的快照会把 5 天的增长冒充成「24h 净增」。
func TestListRankingsExcludesBaselineOlderThanWindowGrace(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()
	seedPublicRepo(t, store, 951, "acme", "stale", 100)
	seedRankSnapshot(t, store, 951, 20, now.Add(-120*time.Hour))
	seedRankSnapshot(t, store, 951, 100, now.Add(-1*time.Hour))
	handler := newTestHandler(t, store)

	recorder := request(t, handler, http.MethodGet, "/api/v1/rankings?type=24h")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	payload := decodeRankings(t, recorder)
	if payload.Data.Meta.Total != 0 || len(payload.Data.Items) != 0 {
		t.Fatalf("total=%d items=%d, want empty board: a 5-day-old snapshot must not back a 24h delta",
			payload.Data.Meta.Total, len(payload.Data.Items))
	}
	if payload.Data.Meta.HistoryAvailable {
		t.Fatal("history_available must be false when no snapshot falls in the window band")
	}
}

// 口径：基线可在截止时间前一个同步周期（6h）内，超出则不可。
// 注意快照时间要离带边缘留出余量——handler 的 time.Now() 比测试的 now 晚几毫秒，
// 贴着 ±6h 边界写用例会随时钟漂移变成间歇失败。
func TestListRankingsAcceptsBaselineWithinOneSyncInterval(t *testing.T) {
	store := newTestStore(t)
	now := time.Now().UTC()
	seedPublicRepo(t, store, 952, "acme", "edge", 130)
	seedPublicRepo(t, store, 953, "acme", "too-old", 130)
	seedRankSnapshot(t, store, 952, 100, now.Add(-29*time.Hour))
	seedRankSnapshot(t, store, 952, 130, now.Add(-1*time.Hour))
	seedRankSnapshot(t, store, 953, 100, now.Add(-32*time.Hour))
	seedRankSnapshot(t, store, 953, 130, now.Add(-1*time.Hour))
	handler := newTestHandler(t, store)

	recorder := request(t, handler, http.MethodGet, "/api/v1/rankings?type=24h")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	payload := decodeRankings(t, recorder)
	if payload.Data.Meta.Total != 1 || len(payload.Data.Items) != 1 {
		t.Fatalf("total=%d items=%d, want only acme/edge", payload.Data.Meta.Total, len(payload.Data.Items))
	}
	item := payload.Data.Items[0]
	if item.FullName != "acme/edge" || item.StarDelta == nil || *item.StarDelta != 30 {
		t.Fatalf("item = %+v, want acme/edge +30", item)
	}
	if !payload.Data.Meta.HistoryAvailable {
		t.Fatal("history_available must be true when a baseline sits inside the band")
	}
}
