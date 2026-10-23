package api

import (
	"context"
	"database/sql"
	"net/http"
	"time"
)

// star 时间序列支持的窗口；只允许显式枚举，避免任意跨度扫全表。
var starWindowDurations = map[string]time.Duration{
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
	"90d": 90 * 24 * time.Hour,
}

const starSeriesMethod = "每次仓库详情同步写入一条真实 Star 快照（约 6 小时一轮）；此处不做采样、插值和估算，样本少于 2 个时 history_available=false"

type starPoint struct {
	CapturedAt string `json:"captured_at"`
	StarCount  int    `json:"star_count"`
}

func (s *server) getRepoStars(w http.ResponseWriter, r *http.Request) {
	window := r.URL.Query().Get("window")
	if window == "" {
		window = "30d"
	}
	duration, ok := starWindowDurations[window]
	if !ok {
		writeError(w, r, http.StatusBadRequest, "INVALID_WINDOW", "window must be one of 7d, 30d or 90d")
		return
	}
	repoID, fullName, err := s.visibleRepoID(r.Context(), r.PathValue("owner"), r.PathValue("repo"))
	if err != nil {
		writeError(w, r, http.StatusNotFound, "repo_not_found", "repository not found")
		return
	}
	cutoff := s.config.Now().UTC().Add(-duration).Format(time.RFC3339)
	rows, err := s.config.DB.SQL().QueryContext(r.Context(), `SELECT star_count, captured_at FROM star_snapshots
		WHERE repo_id = ? AND captured_at >= ? ORDER BY captured_at`, repoID, cutoff)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "failed to read star snapshots")
		return
	}
	defer rows.Close()
	points := []starPoint{}
	for rows.Next() {
		var point starPoint
		if err := rows.Scan(&point.StarCount, &point.CapturedAt); err != nil {
			writeError(w, r, http.StatusInternalServerError, "internal_error", "failed to read star snapshots")
			return
		}
		points = append(points, point)
	}
	if err := rows.Err(); err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "failed to read star snapshots")
		return
	}
	var dataUpdatedAt any
	if len(points) > 0 {
		dataUpdatedAt = points[len(points)-1].CapturedAt
	}
	writeData(w, http.StatusOK, map[string]any{
		"repo":              fullName,
		"window":            window,
		"points":            points,
		"sample_count":      len(points),
		"history_available": len(points) >= 2,
		"method":            starSeriesMethod,
		"data_updated_at":   dataUpdatedAt,
	})
}

// visibleRepoID 只解析未隐藏仓库；隐藏仓库对外恒为 404。
func (s *server) visibleRepoID(ctx context.Context, owner, name string) (int64, string, error) {
	if owner == "" || name == "" {
		return 0, "", sql.ErrNoRows
	}
	var id int64
	var fullName string
	err := s.config.DB.SQL().QueryRowContext(ctx, `SELECT id, full_name FROM repositories
		WHERE owner = ?1 COLLATE NOCASE AND name = ?2 COLLATE NOCASE AND is_hidden = 0`, owner, name).Scan(&id, &fullName)
	return id, fullName, err
}
