-- P1.2 collector state. Historical migrations are immutable; this file only appends schema.
ALTER TABLE repositories ADD COLUMN visibility TEXT NOT NULL DEFAULT 'public' CHECK (visibility = 'public');
ALTER TABLE repositories ADD COLUMN etag TEXT;
ALTER TABLE repositories ADD COLUMN next_sync_at TEXT CHECK (next_sync_at IS NULL OR (length(next_sync_at) = 20 AND strftime('%Y-%m-%dT%H:%M:%SZ', next_sync_at) = next_sync_at));
ALTER TABLE repositories ADD COLUMN last_error_code TEXT;
ALTER TABLE repositories ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0);
CREATE INDEX idx_repositories_next_sync ON repositories(next_sync_at, consecutive_failures);

ALTER TABLE github_credentials ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;
ALTER TABLE github_credentials ADD COLUMN auth_login TEXT;
ALTER TABLE github_credentials ADD COLUMN created_at TEXT CHECK (created_at IS NULL OR (length(created_at) = 20 AND strftime('%Y-%m-%dT%H:%M:%SZ', created_at) = created_at));
ALTER TABLE github_credentials ADD COLUMN updated_at TEXT CHECK (updated_at IS NULL OR (length(updated_at) = 20 AND strftime('%Y-%m-%dT%H:%M:%SZ', updated_at) = updated_at));
ALTER TABLE github_credentials ADD COLUMN last_error_code TEXT;
CREATE INDEX idx_github_credentials_priority ON github_credentials(enabled, status, priority DESC, id);

ALTER TABLE api_rate_limits ADD COLUMN used INTEGER CHECK (used IS NULL OR used >= 0);
ALTER TABLE api_rate_limits ADD COLUMN cooldown_reason TEXT;

ALTER TABLE collection_windows ADD COLUMN job_run_id INTEGER REFERENCES job_runs(id) ON DELETE SET NULL;
ALTER TABLE collection_windows ADD COLUMN parent_id INTEGER REFERENCES collection_windows(id) ON DELETE SET NULL;
ALTER TABLE collection_windows ADD COLUMN split_depth INTEGER NOT NULL DEFAULT 0 CHECK (split_depth >= 0);
ALTER TABLE collection_windows ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0);
ALTER TABLE collection_windows ADD COLUMN next_attempt_at TEXT CHECK (next_attempt_at IS NULL OR (length(next_attempt_at) = 20 AND strftime('%Y-%m-%dT%H:%M:%SZ', next_attempt_at) = next_attempt_at));
ALTER TABLE collection_windows ADD COLUMN total_count INTEGER CHECK (total_count IS NULL OR total_count >= 0);
ALTER TABLE collection_windows ADD COLUMN incomplete_results INTEGER CHECK (incomplete_results IS NULL OR incomplete_results IN (0, 1));
ALTER TABLE collection_windows ADD COLUMN next_url TEXT;
ALTER TABLE collection_windows ADD COLUMN created_at TEXT CHECK (created_at IS NULL OR (length(created_at) = 20 AND strftime('%Y-%m-%dT%H:%M:%SZ', created_at) = created_at));
ALTER TABLE collection_windows ADD COLUMN updated_at TEXT CHECK (updated_at IS NULL OR (length(updated_at) = 20 AND strftime('%Y-%m-%dT%H:%M:%SZ', updated_at) = updated_at));
ALTER TABLE collection_windows ADD COLUMN completed_at TEXT CHECK (completed_at IS NULL OR (length(completed_at) = 20 AND strftime('%Y-%m-%dT%H:%M:%SZ', completed_at) = completed_at));
CREATE INDEX idx_collection_windows_retry ON collection_windows(coverage_status, next_attempt_at, time_from);
CREATE INDEX idx_collection_windows_run ON collection_windows(job_run_id, id);

ALTER TABLE collector_jobs ADD COLUMN lease_owner TEXT;
ALTER TABLE collector_jobs ADD COLUMN lease_expires_at TEXT CHECK (lease_expires_at IS NULL OR (length(lease_expires_at) = 20 AND strftime('%Y-%m-%dT%H:%M:%SZ', lease_expires_at) = lease_expires_at));
CREATE UNIQUE INDEX idx_collector_jobs_type_unique ON collector_jobs(job_type);
CREATE INDEX idx_collector_jobs_lease ON collector_jobs(lease_expires_at);

ALTER TABLE job_runs ADD COLUMN trigger TEXT NOT NULL DEFAULT 'schedule' CHECK (trigger IN ('schedule', 'manual', 'recovery'));
ALTER TABLE job_runs ADD COLUMN heartbeat_at TEXT CHECK (heartbeat_at IS NULL OR (length(heartbeat_at) = 20 AND strftime('%Y-%m-%dT%H:%M:%SZ', heartbeat_at) = heartbeat_at));
ALTER TABLE job_runs ADD COLUMN retry_count INTEGER NOT NULL DEFAULT 0 CHECK (retry_count >= 0);
ALTER TABLE job_runs ADD COLUMN rate_limited_count INTEGER NOT NULL DEFAULT 0 CHECK (rate_limited_count >= 0);
ALTER TABLE job_runs ADD COLUMN failed_count INTEGER NOT NULL DEFAULT 0 CHECK (failed_count >= 0);
ALTER TABLE job_runs ADD COLUMN coverage_incomplete_count INTEGER NOT NULL DEFAULT 0 CHECK (coverage_incomplete_count >= 0);
ALTER TABLE job_runs ADD COLUMN last_error_code TEXT;

CREATE TABLE job_run_events (
    id INTEGER PRIMARY KEY,
    run_id INTEGER NOT NULL,
    window_id INTEGER,
    repo_id INTEGER,
    occurred_at TEXT NOT NULL CHECK (length(occurred_at) = 20 AND strftime('%Y-%m-%dT%H:%M:%SZ', occurred_at) = occurred_at),
    level TEXT NOT NULL CHECK (level IN ('info', 'warning', 'error')),
    event_code TEXT NOT NULL CHECK (length(event_code) BETWEEN 1 AND 80),
    message TEXT NOT NULL CHECK (length(message) <= 2000),
    attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    details_json TEXT NOT NULL DEFAULT '{}',
    FOREIGN KEY (run_id) REFERENCES job_runs(id) ON DELETE CASCADE,
    FOREIGN KEY (window_id) REFERENCES collection_windows(id) ON DELETE SET NULL,
    FOREIGN KEY (repo_id) REFERENCES repositories(id) ON DELETE SET NULL
);
CREATE INDEX idx_job_run_events_run_id ON job_run_events(run_id, id);
CREATE INDEX idx_job_run_events_window_id ON job_run_events(window_id, id);
