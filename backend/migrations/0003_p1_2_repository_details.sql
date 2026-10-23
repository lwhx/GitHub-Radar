-- P1.2 repository detail synchronization counters and due-work index.
ALTER TABLE job_runs ADD COLUMN snapshot_count INTEGER NOT NULL DEFAULT 0 CHECK (snapshot_count >= 0);
ALTER TABLE job_runs ADD COLUMN not_modified_count INTEGER NOT NULL DEFAULT 0 CHECK (not_modified_count >= 0);

CREATE INDEX idx_repositories_due_detail_sync
ON repositories(next_sync_at, consecutive_failures, id)
WHERE visibility = 'public' AND is_hidden = 0;
