package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

const (
	rankingWindow24h  = 24
	rankingWindow7d   = 168
	potentialMaxStars = 3000
	potentialMaxAge   = 30 * 24 * time.Hour
	// 基线容差：详情同步约 6 小时一轮，基线快照允许比窗口截止时间早一个同步周期。
	// 没有带内基线的仓库不进入榜单——避免 5 天前的快照把 5 天增长冒充成 24h 净增。
	rankingBaselineGrace = 6 * time.Hour
	timeLayoutUTC        = "2006-01-02T15:04:05Z"
)

// rankedRepo 在公共仓库字段之上携带榜单口径字段
// （star_delta / score / score_reason 在最新榜中为 null）。
type rankedRepo struct {
	publicRepo
	StarDelta   *int     `json:"star_delta"`
	Score       *float64 `json:"score"`
	ScoreReason *string  `json:"score_reason"`
}

type rankingsMeta struct {
	Type             string  `json:"type"`
	WindowHours      int     `json:"window_hours"`
	Page             int     `json:"page"`
	Limit            int     `json:"limit"`
	Total            int     `json:"total"`
	DataUpdatedAt    *string `json:"data_updated_at"`
	HistoryAvailable bool    `json:"history_available"`
	Note             string  `json:"note"`
}

// growthCTE 取每个仓库的最新快照与「截止时间之前且距截止时间不超过一个同步周期的最新快照」作基线，
// 净增长 = 最新 - 基线；基线超出容差带或缺失的仓库不进入榜单（而不是伪造 0 或夸大的增长）。
const growthCTE = `
WITH latest AS (
	SELECT repo_id, star_count FROM (
		SELECT repo_id, star_count, ROW_NUMBER() OVER (PARTITION BY repo_id ORDER BY captured_at DESC) AS rn
		FROM star_snapshots
	) WHERE rn = 1
),
baseline AS (
	SELECT repo_id, star_count FROM (
		SELECT repo_id, star_count, ROW_NUMBER() OVER (PARTITION BY repo_id ORDER BY captured_at DESC) AS rn
		FROM star_snapshots WHERE captured_at <= ? AND captured_at >= ?
	) WHERE rn = 1
)`

// rankScoreExpr 与 Go 侧 calculateRankScore 必须保持同一系数。
const rankScoreExpr = `((latest.star_count - baseline.star_count) * (1
	+ CASE WHEN r.description IS NOT NULL AND TRIM(r.description) <> '' THEN 0.05 ELSE 0 END
	+ CASE WHEN r.language IS NOT NULL AND TRIM(r.language) <> '' THEN 0.03 ELSE 0 END
	+ CASE WHEN r.topics_json IS NOT NULL AND r.topics_json NOT IN ('', '[]') THEN 0.02 ELSE 0 END))`

const rankFlagExpr = `
	CASE WHEN r.description IS NOT NULL AND TRIM(r.description) <> '' THEN 1 ELSE 0 END,
	CASE WHEN r.language IS NOT NULL AND TRIM(r.language) <> '' THEN 1 ELSE 0 END,
	CASE WHEN r.topics_json IS NOT NULL AND r.topics_json NOT IN ('', '[]') THEN 1 ELSE 0 END`

func calculateRankScore(delta, hasDescription, hasLanguage, hasTopics int) float64 {
	bonus := 0.0
	if hasDescription == 1 {
		bonus += 0.05
	}
	if hasLanguage == 1 {
		bonus += 0.03
	}
	if hasTopics == 1 {
		bonus += 0.02
	}
	return math.Round(float64(delta)*(1+bonus)*10) / 10
}

func describeRankScore(windowHours int, delta int, hasDescription, hasLanguage, hasTopics int) string {
	window := fmt.Sprintf("近 %d 小时", windowHours)
	if windowHours == rankingWindow7d {
		window = "近 7 天"
	}
	parts := make([]string, 0, 3)
	if hasDescription == 1 {
		parts = append(parts, "描述")
	}
	if hasLanguage == 1 {
		parts = append(parts, "语言")
	}
	if hasTopics == 1 {
		parts = append(parts, "话题")
	}
	reason := fmt.Sprintf("%s净增 %d Star", window, delta)
	if len(parts) > 0 {
		reason += "；内容完整度加分：" + strings.Join(parts, "、")
	}
	return reason
}

func (s *server) listRankings(w http.ResponseWriter, r *http.Request) {
	page, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}
	rankType := strings.TrimSpace(r.URL.Query().Get("type"))
	if rankType == "" {
		rankType = "latest"
	}
	switch rankType {
	case "latest", "24h", "7d", "potential":
	default:
		writeError(w, r, http.StatusBadRequest, "invalid_type", "unknown ranking type")
		return
	}

	where, filterArgs, ok := s.rankingFilters(w, r)
	if !ok {
		return
	}

	store := s.config.DB.SQL()
	updatedAt := s.rankingDataUpdatedAt(store)

	if rankType == "latest" {
		meta := rankingsMeta{
			Type: rankType, WindowHours: 0, Page: page, Limit: limit,
			DataUpdatedAt: updatedAt, HistoryAvailable: true,
			Note: "按 GitHub 创建时间降序，同时间按 github_id 稳定排序",
		}
		s.writeLatestRanking(w, r, store, where, filterArgs, page, limit, meta)
		return
	}

	windowHours := rankingWindow24h
	if rankType == "7d" || rankType == "potential" {
		windowHours = rankingWindow7d
	}
	cutoff := time.Now().UTC().Add(-time.Duration(windowHours) * time.Hour).Format(timeLayoutUTC)
	graceCutoff := time.Now().UTC().Add(-(time.Duration(windowHours)*time.Hour + rankingBaselineGrace)).Format(timeLayoutUTC)
	meta := rankingsMeta{
		Type: rankType, WindowHours: windowHours, Page: page, Limit: limit,
		DataUpdatedAt: updatedAt,
		Note:          s.rankingNote(rankType, windowHours),
	}
	meta.HistoryAvailable = s.rankingHasHistory(store, cutoff, graceCutoff)
	if !meta.HistoryAvailable {
		meta.Note = "数据不足：增长窗口无有效历史样本，快照积累后自动填充（不会显示 0）"
	}
	s.writeGrowthRanking(w, r, store, where, filterArgs, page, limit, rankType, windowHours, cutoff, graceCutoff, meta)
}

func (s *server) rankingFilters(w http.ResponseWriter, r *http.Request) (string, []any, bool) {
	where := []string{"r.is_hidden = 0"}
	args := []any{}
	if slug := strings.TrimSpace(r.URL.Query().Get("category")); slug != "" {
		var categoryID int64
		if err := s.config.DB.SQL().QueryRow(
			`SELECT id FROM categories WHERE slug = ? COLLATE NOCASE AND enabled = 1`, slug,
		).Scan(&categoryID); err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_category", "unknown category")
			return "", nil, false
		}
		where = append(where, "EXISTS (SELECT 1 FROM repo_categories rc WHERE rc.repo_id = r.id AND rc.category_id = ?)")
		args = append(args, categoryID)
	}
	if language := strings.TrimSpace(r.URL.Query().Get("language")); language != "" {
		where = append(where, "r.language = ? COLLATE NOCASE")
		args = append(args, language)
	}
	return strings.Join(where, " AND "), args, true
}

func (s *server) rankingDataUpdatedAt(store *sql.DB) *string {
	var updatedAt sql.NullString
	_ = store.QueryRow(`SELECT MAX(captured_at) FROM star_snapshots`).Scan(&updatedAt)
	return nullStringPtr(updatedAt)
}

func (s *server) rankingHasHistory(store *sql.DB, cutoff, graceCutoff string) bool {
	var exists int
	if err := store.QueryRow(`SELECT EXISTS (SELECT 1 FROM star_snapshots WHERE captured_at <= ? AND captured_at >= ?)`, cutoff, graceCutoff).Scan(&exists); err != nil {
		return false
	}
	return exists == 1
}

func (s *server) rankingNote(rankType string, windowHours int) string {
	if rankType == "potential" {
		return "30 天内创建、总 Star ≤ 3000、近 7 天有净增长；分数 = 净增 × (1 + 内容完整度加分)"
	}
	return fmt.Sprintf("按滚动 %d 小时净 Star 变化降序，样本来自已采集的 Star 快照", windowHours)
}

func (s *server) writeLatestRanking(w http.ResponseWriter, r *http.Request, store *sql.DB, where string, filterArgs []any, page, limit int, meta rankingsMeta) {
	var total int
	if err := store.QueryRow(`SELECT COUNT(*) FROM repositories r WHERE `+where, filterArgs...).Scan(&total); err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "failed to count rankings")
		return
	}
	args := append(append([]any{}, filterArgs...), limit, (page-1)*limit)
	rows, err := store.Query(`SELECT `+publicRepoColumns+` FROM repositories r WHERE `+where+`
		ORDER BY r.github_created_at DESC, r.github_id ASC
		LIMIT ? OFFSET ?`, args...)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "failed to list rankings")
		return
	}
	defer rows.Close()
	items := []rankedRepo{}
	for rows.Next() {
		repo, err := scanPublicRepo(rows)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "internal_error", "failed to read rankings")
			return
		}
		items = append(items, rankedRepo{publicRepo: repo})
	}
	if err := rows.Err(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "failed to read rankings")
		return
	}
	meta.Total = total
	writeData(w, http.StatusOK, map[string]any{"items": items, "meta": meta})
}

func (s *server) writeGrowthRanking(w http.ResponseWriter, r *http.Request, store *sql.DB, where string, filterArgs []any, page, limit int, rankType string, windowHours int, cutoff, graceCutoff string, meta rankingsMeta) {
	extraWhere := ""
	extraArgs := []any{}
	orderExpr := "star_delta DESC, r.stars_count DESC, r.github_id ASC"
	if rankType == "potential" {
		createdCutoff := time.Now().UTC().Add(-potentialMaxAge).Format(timeLayoutUTC)
		extraWhere = " AND r.github_created_at >= ? AND r.stars_count <= ? AND (latest.star_count - baseline.star_count) > 0"
		extraArgs = append(extraArgs, createdCutoff, potentialMaxStars)
		orderExpr = "rank_score DESC, r.stars_count DESC, r.github_id ASC"
	}

	countArgs := append([]any{cutoff, graceCutoff}, filterArgs...)
	countArgs = append(countArgs, extraArgs...)
	var total int
	if err := store.QueryRow(growthCTE+` SELECT COUNT(*) FROM repositories r
		JOIN latest ON latest.repo_id = r.id
		JOIN baseline ON baseline.repo_id = r.id
		WHERE `+where+extraWhere, countArgs...).Scan(&total); err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "failed to count rankings")
		return
	}

	selectArgs := append([]any{cutoff, graceCutoff}, filterArgs...)
	selectArgs = append(selectArgs, extraArgs...)
	selectArgs = append(selectArgs, limit, (page-1)*limit)
	rows, err := store.Query(growthCTE+`
		SELECT `+publicRepoColumns+`,
			latest.star_count - baseline.star_count AS star_delta,
			`+rankFlagExpr+`,
			`+rankScoreExpr+` AS rank_score
		FROM repositories r
		JOIN latest ON latest.repo_id = r.id
		JOIN baseline ON baseline.repo_id = r.id
		WHERE `+where+extraWhere+`
		ORDER BY `+orderExpr+`
		LIMIT ? OFFSET ?`, selectArgs...)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "failed to list rankings")
		return
	}
	defer rows.Close()
	items := []rankedRepo{}
	for rows.Next() {
		var (
			repo                        publicRepo
			topicsJSON                  string
			homepage, description       sql.NullString
			language, license, pushedAt sql.NullString
			lastSynced                  sql.NullString
			isFork, isArchived          int
			delta                       int
			hasDescription              int
			hasLanguage                 int
			hasTopics                   int
			rankScore                   float64
		)
		if err := rows.Scan(&repo.FullName, &repo.Owner, &repo.Name, &repo.HtmlURL, &homepage, &description,
			&language, &license, &topicsJSON, &repo.StarsCount, &repo.ForksCount, &repo.OpenIssuesCount,
			&isFork, &isArchived, &repo.GithubCreatedAt, &repo.GithubUpdatedAt, &pushedAt, &repo.FirstSeenAt, &lastSynced,
			&delta, &hasDescription, &hasLanguage, &hasTopics, &rankScore); err != nil {
			writeError(w, r, http.StatusInternalServerError, "internal_error", "failed to read rankings")
			return
		}
		repo.HomepageURL = nullStringPtr(homepage)
		repo.Description = nullStringPtr(description)
		repo.Language = nullStringPtr(language)
		repo.LicenseSPDX = nullStringPtr(license)
		repo.PushedAt = nullStringPtr(pushedAt)
		repo.LastSyncedAt = nullStringPtr(lastSynced)
		repo.IsFork = isFork == 1
		repo.IsArchived = isArchived == 1
		repo.Topics = []string{}
		if err := json.Unmarshal([]byte(topicsJSON), &repo.Topics); err != nil {
			repo.Topics = []string{}
		}

		starDelta := delta
		item := rankedRepo{publicRepo: repo, StarDelta: &starDelta}
		if rankType == "potential" {
			score := calculateRankScore(delta, hasDescription, hasLanguage, hasTopics)
			reason := describeRankScore(windowHours, delta, hasDescription, hasLanguage, hasTopics)
			item.Score = &score
			item.ScoreReason = &reason
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "failed to read rankings")
		return
	}
	meta.Total = total
	writeData(w, http.StatusOK, map[string]any{"items": items, "meta": meta})
}
