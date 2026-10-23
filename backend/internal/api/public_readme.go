package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/github-radar/github-radar/internal/readmes"
)

// readmeService 提供按需拉取 + 24h 缓存的 README 文档。
type readmeService interface {
	Get(ctx context.Context, repoID int64, owner string, name string) (readmes.Document, error)
}

func (s *server) getRepoReadme(w http.ResponseWriter, r *http.Request) {
	repoID, fullName, err := s.visibleRepoID(r.Context(), r.PathValue("owner"), r.PathValue("repo"))
	if err != nil {
		writeError(w, r, http.StatusNotFound, "repo_not_found", "repository not found")
		return
	}
	if s.config.Readmes == nil {
		writeError(w, r, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "README service is not configured")
		return
	}
	document, err := s.config.Readmes.Get(r.Context(), repoID, r.PathValue("owner"), r.PathValue("repo"))
	if errors.Is(err, readmes.ErrUpstream) {
		writeError(w, r, http.StatusBadGateway, "README_UNAVAILABLE", "GitHub README is temporarily unavailable")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "failed to load README")
		return
	}
	payload := map[string]any{
		"repo":       fullName,
		"status":     document.Status,
		"format":     "markdown",
		"content":    document.Content,
		"truncated":  document.Truncated,
		"cached":     document.Cached,
		"stale":      document.Stale,
		"source":     "github",
		"fetched_at": document.FetchedAt.UTC().Format(time.RFC3339),
	}
	switch document.Status {
	case "missing":
		payload["content"] = ""
		payload["note"] = "该仓库没有 README 文件，GitHub 返回 404"
	default:
		if document.Stale {
			payload["note"] = "上游 GitHub 暂不可用，当前展示本地缓存副本，可能不是最新版本"
		} else if document.Truncated {
			payload["note"] = "README 超长，已截断到 60000 字符"
		}
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	writeData(w, http.StatusOK, payload)
}
