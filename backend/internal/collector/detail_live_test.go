package collector

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/github-radar/github-radar/internal/github"
)

// TestLiveDetailSyncConditionalRequests exercises the real GitHub detail
// endpoint through the production synchronizer. It runs only when
// GITHUB_LIVE_TOKEN provides a read-only public token and never prints it.
func TestLiveDetailSyncConditionalRequests(t *testing.T) {
	token := os.Getenv("GITHUB_LIVE_TOKEN")
	if token == "" {
		t.Skip("set GITHUB_LIVE_TOKEN to call the real GitHub API")
	}
	client, err := github.NewClient(github.ClientConfig{Token: token})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	database := newDetailTestDatabase(t)
	now := time.Now().UTC()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Discover the numeric id with one unconditional request, then persist the
	// row without an ETag so the first synchronization must fetch a full body.
	repository, meta, err := client.GetRepository(ctx, "octocat", "Hello-World", "")
	if err != nil {
		t.Fatalf("GetRepository() error = %v", err)
	}
	if meta.StatusCode != 200 || repository.ID == 0 {
		t.Fatalf("discovery = status %d id %d", meta.StatusCode, repository.ID)
	}
	result, err := database.Exec(`INSERT INTO repositories
		(github_id, owner, name, full_name, html_url, topics_json, stars_count, github_created_at,
		 github_updated_at, first_seen_at, sync_status, visibility, next_sync_at)
		VALUES (?, 'octocat', 'Hello-World', 'octocat/Hello-World', 'https://github.com/octocat/Hello-World', '[]', ?,
		 '2011-01-27T00:00:00Z', ?, ?, 'pending', 'public', ?)`,
		repository.ID, repository.StargazersCount, formatTime(repository.UpdatedAt),
		formatTime(now), formatTime(now.Add(-time.Minute)))
	if err != nil {
		t.Fatalf("insert repository: %v", err)
	}
	repoID, _ := result.LastInsertId()
	runID := insertDetailRun(t, database)
	synchronizer := NewDetailSynchronizer(database, client, func() time.Time { return now })

	first, err := synchronizer.SyncDue(ctx, runID)
	if err != nil {
		t.Fatalf("first SyncDue() error = %v", err)
	}
	if first.Requests != 1 || first.NotModified != 0 || first.Snapshots != 1 || first.Failed != 0 {
		t.Fatalf("first summary = %+v, want one full fetch with one snapshot", first)
	}
	var etag string
	var stars int
	if err := database.QueryRow(`SELECT etag, stars_count FROM repositories WHERE id = ?`, repoID).Scan(&etag, &stars); err != nil {
		t.Fatal(err)
	}
	if etag == "" {
		t.Fatal("live detail 200 did not persist an ETag")
	}

	if _, err := database.Exec(`UPDATE repositories SET next_sync_at = ? WHERE id = ?`,
		formatTime(now.Add(-time.Minute)), repoID); err != nil {
		t.Fatal(err)
	}
	second, err := synchronizer.SyncDue(ctx, runID)
	if err != nil {
		t.Fatalf("second SyncDue() error = %v", err)
	}
	if second.Requests != 1 || second.Failed != 0 {
		t.Fatalf("second summary = %+v", second)
	}
	var snapshots int
	if err := database.QueryRow(`SELECT COUNT(*) FROM star_snapshots WHERE repo_id = ?`, repoID).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if second.NotModified == 1 {
		if snapshots != 1 {
			t.Fatalf("304 wrote a snapshot: total %d, want 1", snapshots)
		}
		t.Log("live conditional round-trip: first 200 with snapshot, then 304 without a new snapshot")
	} else {
		if second.Snapshots != 1 || snapshots != 2 {
			t.Fatalf("second 200 summary = %+v snapshots = %d, want one new snapshot (total 2)", second, snapshots)
		}
		t.Log("live conditional round-trip: repository changed between calls, second 200 wrote a fresh snapshot")
	}
	var source string
	if err := database.QueryRow(`SELECT source FROM star_snapshots WHERE repo_id = ? ORDER BY id DESC LIMIT 1`, repoID).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if source != "repository_detail" {
		t.Fatalf("snapshot source = %q, want repository_detail", source)
	}
	if stars <= 0 {
		t.Fatalf("stars = %d, want a positive live value", stars)
	}
}
