-- Time columns store UTC RFC3339 text (for example 2026-10-23T12:34:56Z).
CREATE TABLE repositories (
    id INTEGER PRIMARY KEY,
    github_id INTEGER NOT NULL UNIQUE,
    owner TEXT NOT NULL,
    name TEXT NOT NULL,
    full_name TEXT NOT NULL UNIQUE,
    html_url TEXT NOT NULL,
    homepage_url TEXT,
    description TEXT,
    description_zh TEXT,
    language TEXT,
    license_spdx TEXT,
    default_branch TEXT,
    topics_json TEXT NOT NULL DEFAULT '[]',
    stars_count INTEGER NOT NULL DEFAULT 0 CHECK (stars_count >= 0),
    forks_count INTEGER NOT NULL DEFAULT 0 CHECK (forks_count >= 0),
    open_issues_count INTEGER NOT NULL DEFAULT 0 CHECK (open_issues_count >= 0),
    is_fork INTEGER NOT NULL DEFAULT 0 CHECK (is_fork IN (0, 1)),
    is_archived INTEGER NOT NULL DEFAULT 0 CHECK (is_archived IN (0, 1)),
    is_disabled INTEGER NOT NULL DEFAULT 0 CHECK (is_disabled IN (0, 1)),
    is_hidden INTEGER NOT NULL DEFAULT 0 CHECK (is_hidden IN (0, 1)),
    github_created_at TEXT NOT NULL CHECK ((github_created_at GLOB '????-??-??T??:??:??Z' OR github_created_at GLOB '????-??-??T??:??:??.*Z') AND github_created_at NOT GLOB '*[^0-9TZ:.-]*' AND (length(github_created_at) = 20 OR (length(github_created_at) >= 22 AND substr(github_created_at, 21, length(github_created_at) - 21) NOT GLOB '*[^0-9]*')) AND strftime('%Y-%m-%dT%H:%M:%S', github_created_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', github_created_at) = substr(github_created_at, 1, 19)),
    github_updated_at TEXT NOT NULL CHECK ((github_updated_at GLOB '????-??-??T??:??:??Z' OR github_updated_at GLOB '????-??-??T??:??:??.*Z') AND github_updated_at NOT GLOB '*[^0-9TZ:.-]*' AND (length(github_updated_at) = 20 OR (length(github_updated_at) >= 22 AND substr(github_updated_at, 21, length(github_updated_at) - 21) NOT GLOB '*[^0-9]*')) AND strftime('%Y-%m-%dT%H:%M:%S', github_updated_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', github_updated_at) = substr(github_updated_at, 1, 19)),
    pushed_at TEXT CHECK (pushed_at IS NULL OR ((pushed_at GLOB '????-??-??T??:??:??Z' OR pushed_at GLOB '????-??-??T??:??:??.*Z') AND pushed_at NOT GLOB '*[^0-9TZ:.-]*' AND (length(pushed_at) = 20 OR (length(pushed_at) >= 22 AND substr(pushed_at, 21, length(pushed_at) - 21) NOT GLOB '*[^0-9]*')) AND strftime('%Y-%m-%dT%H:%M:%S', pushed_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', pushed_at) = substr(pushed_at, 1, 19))),
    first_seen_at TEXT NOT NULL CHECK ((first_seen_at GLOB '????-??-??T??:??:??Z' OR first_seen_at GLOB '????-??-??T??:??:??.*Z') AND first_seen_at NOT GLOB '*[^0-9TZ:.-]*' AND (length(first_seen_at) = 20 OR (length(first_seen_at) >= 22 AND substr(first_seen_at, 21, length(first_seen_at) - 21) NOT GLOB '*[^0-9]*')) AND strftime('%Y-%m-%dT%H:%M:%S', first_seen_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', first_seen_at) = substr(first_seen_at, 1, 19)),
    last_synced_at TEXT CHECK (last_synced_at IS NULL OR ((last_synced_at GLOB '????-??-??T??:??:??Z' OR last_synced_at GLOB '????-??-??T??:??:??.*Z') AND last_synced_at NOT GLOB '*[^0-9TZ:.-]*' AND (length(last_synced_at) = 20 OR (length(last_synced_at) >= 22 AND substr(last_synced_at, 21, length(last_synced_at) - 21) NOT GLOB '*[^0-9]*')) AND strftime('%Y-%m-%dT%H:%M:%S', last_synced_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', last_synced_at) = substr(last_synced_at, 1, 19))),
    last_successful_fetch_at TEXT CHECK (last_successful_fetch_at IS NULL OR ((last_successful_fetch_at GLOB '????-??-??T??:??:??Z' OR last_successful_fetch_at GLOB '????-??-??T??:??:??.*Z') AND last_successful_fetch_at NOT GLOB '*[^0-9TZ:.-]*' AND (length(last_successful_fetch_at) = 20 OR (length(last_successful_fetch_at) >= 22 AND substr(last_successful_fetch_at, 21, length(last_successful_fetch_at) - 21) NOT GLOB '*[^0-9]*')) AND strftime('%Y-%m-%dT%H:%M:%S', last_successful_fetch_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', last_successful_fetch_at) = substr(last_successful_fetch_at, 1, 19))),
    sync_status TEXT NOT NULL DEFAULT 'pending'
);
CREATE INDEX idx_repositories_github_created_at ON repositories(github_created_at DESC);
CREATE INDEX idx_repositories_stars_count ON repositories(stars_count DESC);
CREATE INDEX idx_repositories_last_synced_at ON repositories(last_synced_at);
CREATE INDEX idx_repositories_language ON repositories(language);
CREATE INDEX idx_repositories_is_hidden ON repositories(is_hidden);

CREATE TABLE star_snapshots (
    id INTEGER PRIMARY KEY,
    repo_id INTEGER NOT NULL,
    star_count INTEGER NOT NULL CHECK (star_count >= 0),
    captured_at TEXT NOT NULL CHECK ((captured_at GLOB '????-??-??T??:??:??Z' OR captured_at GLOB '????-??-??T??:??:??.*Z') AND captured_at NOT GLOB '*[^0-9TZ:.-]*' AND (length(captured_at) = 20 OR (length(captured_at) >= 22 AND substr(captured_at, 21, length(captured_at) - 21) NOT GLOB '*[^0-9]*')) AND strftime('%Y-%m-%dT%H:%M:%S', captured_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', captured_at) = substr(captured_at, 1, 19)),
    source TEXT NOT NULL,
    sample_status TEXT NOT NULL,
    FOREIGN KEY (repo_id) REFERENCES repositories(id) ON DELETE CASCADE,
    UNIQUE (repo_id, captured_at)
);
CREATE INDEX idx_star_snapshots_repo_captured ON star_snapshots(repo_id, captured_at DESC);

CREATE TABLE categories (
    id INTEGER PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL,
    description TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1))
);
CREATE INDEX idx_categories_enabled_sort ON categories(enabled, sort_order, id);

CREATE TABLE category_rules (
    id INTEGER PRIMARY KEY,
    category_id INTEGER NOT NULL,
    match_type TEXT NOT NULL CHECK (match_type IN ('topic', 'keyword', 'language')),
    pattern TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE CASCADE,
    UNIQUE (category_id, match_type, pattern)
);
CREATE INDEX idx_category_rules_category_priority ON category_rules(category_id, enabled, priority DESC);

CREATE TABLE repo_categories (
    repo_id INTEGER NOT NULL,
    category_id INTEGER NOT NULL,
    is_primary INTEGER NOT NULL DEFAULT 0 CHECK (is_primary IN (0, 1)),
    source TEXT NOT NULL CHECK (source IN ('auto', 'manual')),
    PRIMARY KEY (repo_id, category_id),
    FOREIGN KEY (repo_id) REFERENCES repositories(id) ON DELETE CASCADE,
    FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE CASCADE
);
CREATE INDEX idx_repo_categories_category ON repo_categories(category_id, is_primary DESC, repo_id);

CREATE TABLE collection_windows (
    id INTEGER PRIMARY KEY,
    source TEXT NOT NULL,
    query_hash TEXT NOT NULL,
    time_from TEXT NOT NULL CHECK ((time_from GLOB '????-??-??T??:??:??Z' OR time_from GLOB '????-??-??T??:??:??.*Z') AND time_from NOT GLOB '*[^0-9TZ:.-]*' AND (length(time_from) = 20 OR (length(time_from) >= 22 AND substr(time_from, 21, length(time_from) - 21) NOT GLOB '*[^0-9]*')) AND strftime('%Y-%m-%dT%H:%M:%S', time_from) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', time_from) = substr(time_from, 1, 19)),
    time_to TEXT NOT NULL CHECK ((time_to GLOB '????-??-??T??:??:??Z' OR time_to GLOB '????-??-??T??:??:??.*Z') AND time_to NOT GLOB '*[^0-9TZ:.-]*' AND (length(time_to) = 20 OR (length(time_to) >= 22 AND substr(time_to, 21, length(time_to) - 21) NOT GLOB '*[^0-9]*')) AND strftime('%Y-%m-%dT%H:%M:%S', time_to) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', time_to) = substr(time_to, 1, 19)),
    page INTEGER NOT NULL DEFAULT 1 CHECK (page >= 1),
    cursor_json TEXT,
    coverage_status TEXT NOT NULL,
    last_error TEXT,
    UNIQUE (source, query_hash, time_from, time_to, page)
);
CREATE INDEX idx_collection_windows_coverage ON collection_windows(coverage_status, time_from, time_to);

CREATE TABLE collector_jobs (
    id INTEGER PRIMARY KEY,
    job_type TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    interval_seconds INTEGER NOT NULL CHECK (interval_seconds > 0),
    next_run_at TEXT CHECK (next_run_at IS NULL OR ((next_run_at GLOB '????-??-??T??:??:??Z' OR next_run_at GLOB '????-??-??T??:??:??.*Z') AND next_run_at NOT GLOB '*[^0-9TZ:.-]*' AND (length(next_run_at) = 20 OR (length(next_run_at) >= 22 AND substr(next_run_at, 21, length(next_run_at) - 21) NOT GLOB '*[^0-9]*')) AND strftime('%Y-%m-%dT%H:%M:%S', next_run_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', next_run_at) = substr(next_run_at, 1, 19))),
    priority INTEGER NOT NULL DEFAULT 0,
    config_json TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_collector_jobs_schedule ON collector_jobs(enabled, next_run_at, priority DESC);

CREATE TABLE job_runs (
    id INTEGER PRIMARY KEY,
    job_id INTEGER NOT NULL,
    started_at TEXT NOT NULL CHECK ((started_at GLOB '????-??-??T??:??:??Z' OR started_at GLOB '????-??-??T??:??:??.*Z') AND started_at NOT GLOB '*[^0-9TZ:.-]*' AND (length(started_at) = 20 OR (length(started_at) >= 22 AND substr(started_at, 21, length(started_at) - 21) NOT GLOB '*[^0-9]*')) AND strftime('%Y-%m-%dT%H:%M:%S', started_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', started_at) = substr(started_at, 1, 19)),
    finished_at TEXT CHECK (finished_at IS NULL OR ((finished_at GLOB '????-??-??T??:??:??Z' OR finished_at GLOB '????-??-??T??:??:??.*Z') AND finished_at NOT GLOB '*[^0-9TZ:.-]*' AND (length(finished_at) = 20 OR (length(finished_at) >= 22 AND substr(finished_at, 21, length(finished_at) - 21) NOT GLOB '*[^0-9]*')) AND strftime('%Y-%m-%dT%H:%M:%S', finished_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', finished_at) = substr(finished_at, 1, 19))),
    status TEXT NOT NULL,
    request_count INTEGER NOT NULL DEFAULT 0 CHECK (request_count >= 0),
    created_count INTEGER NOT NULL DEFAULT 0 CHECK (created_count >= 0),
    updated_count INTEGER NOT NULL DEFAULT 0 CHECK (updated_count >= 0),
    error_summary TEXT,
    FOREIGN KEY (job_id) REFERENCES collector_jobs(id) ON DELETE CASCADE
);
CREATE INDEX idx_job_runs_job_started ON job_runs(job_id, started_at DESC);
CREATE INDEX idx_job_runs_status_started ON job_runs(status, started_at DESC);

CREATE TABLE github_credentials (
    id INTEGER PRIMARY KEY,
    label TEXT NOT NULL,
    auth_identity TEXT,
    token_ciphertext TEXT NOT NULL,
    token_hint TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    status TEXT NOT NULL DEFAULT 'unchecked',
    last_checked_at TEXT CHECK (last_checked_at IS NULL OR ((last_checked_at GLOB '????-??-??T??:??:??Z' OR last_checked_at GLOB '????-??-??T??:??:??.*Z') AND last_checked_at NOT GLOB '*[^0-9TZ:.-]*' AND (length(last_checked_at) = 20 OR (length(last_checked_at) >= 22 AND substr(last_checked_at, 21, length(last_checked_at) - 21) NOT GLOB '*[^0-9]*')) AND strftime('%Y-%m-%dT%H:%M:%S', last_checked_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', last_checked_at) = substr(last_checked_at, 1, 19)))
);
CREATE INDEX idx_github_credentials_enabled_status ON github_credentials(enabled, status);

CREATE TABLE api_rate_limits (
    credential_id INTEGER NOT NULL,
    resource TEXT NOT NULL,
    limit_value INTEGER NOT NULL CHECK (limit_value >= 0),
    remaining INTEGER NOT NULL CHECK (remaining >= 0),
    reset_at TEXT NOT NULL CHECK ((reset_at GLOB '????-??-??T??:??:??Z' OR reset_at GLOB '????-??-??T??:??:??.*Z') AND reset_at NOT GLOB '*[^0-9TZ:.-]*' AND (length(reset_at) = 20 OR (length(reset_at) >= 22 AND substr(reset_at, 21, length(reset_at) - 21) NOT GLOB '*[^0-9]*')) AND strftime('%Y-%m-%dT%H:%M:%S', reset_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', reset_at) = substr(reset_at, 1, 19)),
    cooldown_until TEXT CHECK (cooldown_until IS NULL OR ((cooldown_until GLOB '????-??-??T??:??:??Z' OR cooldown_until GLOB '????-??-??T??:??:??.*Z') AND cooldown_until NOT GLOB '*[^0-9TZ:.-]*' AND (length(cooldown_until) = 20 OR (length(cooldown_until) >= 22 AND substr(cooldown_until, 21, length(cooldown_until) - 21) NOT GLOB '*[^0-9]*')) AND strftime('%Y-%m-%dT%H:%M:%S', cooldown_until) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', cooldown_until) = substr(cooldown_until, 1, 19))),
    updated_at TEXT NOT NULL CHECK ((updated_at GLOB '????-??-??T??:??:??Z' OR updated_at GLOB '????-??-??T??:??:??.*Z') AND updated_at NOT GLOB '*[^0-9TZ:.-]*' AND (length(updated_at) = 20 OR (length(updated_at) >= 22 AND substr(updated_at, 21, length(updated_at) - 21) NOT GLOB '*[^0-9]*')) AND strftime('%Y-%m-%dT%H:%M:%S', updated_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', updated_at) = substr(updated_at, 1, 19)),
    PRIMARY KEY (credential_id, resource),
    FOREIGN KEY (credential_id) REFERENCES github_credentials(id) ON DELETE CASCADE
);
CREATE INDEX idx_api_rate_limits_reset ON api_rate_limits(reset_at, cooldown_until);

CREATE TABLE admin_users (
    id INTEGER PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TEXT NOT NULL CHECK ((created_at GLOB '????-??-??T??:??:??Z' OR created_at GLOB '????-??-??T??:??:??.*Z') AND created_at NOT GLOB '*[^0-9TZ:.-]*' AND (length(created_at) = 20 OR (length(created_at) >= 22 AND substr(created_at, 21, length(created_at) - 21) NOT GLOB '*[^0-9]*')) AND strftime('%Y-%m-%dT%H:%M:%S', created_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', created_at) = substr(created_at, 1, 19)),
    last_login_at TEXT CHECK (last_login_at IS NULL OR ((last_login_at GLOB '????-??-??T??:??:??Z' OR last_login_at GLOB '????-??-??T??:??:??.*Z') AND last_login_at NOT GLOB '*[^0-9TZ:.-]*' AND (length(last_login_at) = 20 OR (length(last_login_at) >= 22 AND substr(last_login_at, 21, length(last_login_at) - 21) NOT GLOB '*[^0-9]*')) AND strftime('%Y-%m-%dT%H:%M:%S', last_login_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', last_login_at) = substr(last_login_at, 1, 19)))
);

CREATE TABLE admin_sessions (
    id INTEGER PRIMARY KEY,
    admin_id INTEGER NOT NULL,
    session_hash TEXT NOT NULL UNIQUE,
    csrf_hash TEXT,
    expires_at TEXT NOT NULL CHECK ((expires_at GLOB '????-??-??T??:??:??Z' OR expires_at GLOB '????-??-??T??:??:??.*Z') AND expires_at NOT GLOB '*[^0-9TZ:.-]*' AND (length(expires_at) = 20 OR (length(expires_at) >= 22 AND substr(expires_at, 21, length(expires_at) - 21) NOT GLOB '*[^0-9]*')) AND strftime('%Y-%m-%dT%H:%M:%S', expires_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', expires_at) = substr(expires_at, 1, 19)),
    created_at TEXT NOT NULL CHECK ((created_at GLOB '????-??-??T??:??:??Z' OR created_at GLOB '????-??-??T??:??:??.*Z') AND created_at NOT GLOB '*[^0-9TZ:.-]*' AND (length(created_at) = 20 OR (length(created_at) >= 22 AND substr(created_at, 21, length(created_at) - 21) NOT GLOB '*[^0-9]*')) AND strftime('%Y-%m-%dT%H:%M:%S', created_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', created_at) = substr(created_at, 1, 19)),
    revoked_at TEXT CHECK (revoked_at IS NULL OR ((revoked_at GLOB '????-??-??T??:??:??Z' OR revoked_at GLOB '????-??-??T??:??:??.*Z') AND revoked_at NOT GLOB '*[^0-9TZ:.-]*' AND (length(revoked_at) = 20 OR (length(revoked_at) >= 22 AND substr(revoked_at, 21, length(revoked_at) - 21) NOT GLOB '*[^0-9]*')) AND strftime('%Y-%m-%dT%H:%M:%S', revoked_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', revoked_at) = substr(revoked_at, 1, 19))),
    FOREIGN KEY (admin_id) REFERENCES admin_users(id) ON DELETE CASCADE
);
CREATE INDEX idx_admin_sessions_admin ON admin_sessions(admin_id, revoked_at, expires_at);
CREATE INDEX idx_admin_sessions_expires ON admin_sessions(expires_at);

CREATE TABLE site_settings (
    key TEXT PRIMARY KEY,
    json_value TEXT NOT NULL,
    updated_at TEXT NOT NULL CHECK ((updated_at GLOB '????-??-??T??:??:??Z' OR updated_at GLOB '????-??-??T??:??:??.*Z') AND updated_at NOT GLOB '*[^0-9TZ:.-]*' AND (length(updated_at) = 20 OR (length(updated_at) >= 22 AND substr(updated_at, 21, length(updated_at) - 21) NOT GLOB '*[^0-9]*')) AND strftime('%Y-%m-%dT%H:%M:%S', updated_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', updated_at) = substr(updated_at, 1, 19))
);

CREATE TABLE audit_logs (
    id INTEGER PRIMARY KEY,
    admin_id INTEGER,
    action TEXT NOT NULL,
    target TEXT NOT NULL,
    result TEXT NOT NULL,
    created_at TEXT NOT NULL CHECK ((created_at GLOB '????-??-??T??:??:??Z' OR created_at GLOB '????-??-??T??:??:??.*Z') AND created_at NOT GLOB '*[^0-9TZ:.-]*' AND (length(created_at) = 20 OR (length(created_at) >= 22 AND substr(created_at, 21, length(created_at) - 21) NOT GLOB '*[^0-9]*')) AND strftime('%Y-%m-%dT%H:%M:%S', created_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%S', created_at) = substr(created_at, 1, 19)),
    request_id TEXT,
    FOREIGN KEY (admin_id) REFERENCES admin_users(id) ON DELETE SET NULL
);
CREATE INDEX idx_audit_logs_created ON audit_logs(created_at DESC);
CREATE INDEX idx_audit_logs_admin_created ON audit_logs(admin_id, created_at DESC);
CREATE INDEX idx_audit_logs_request_id ON audit_logs(request_id);

CREATE VIRTUAL TABLE repositories_fts USING fts5(
    full_name,
    owner,
    name,
    description,
    description_zh,
    topics_json,
    content='repositories',
    content_rowid='id'
);

CREATE TRIGGER repositories_fts_insert AFTER INSERT ON repositories BEGIN
    INSERT INTO repositories_fts(rowid, full_name, owner, name, description, description_zh, topics_json)
    VALUES (new.id, new.full_name, new.owner, new.name, new.description, new.description_zh, new.topics_json);
END;

CREATE TRIGGER repositories_fts_delete AFTER DELETE ON repositories BEGIN
    INSERT INTO repositories_fts(repositories_fts, rowid, full_name, owner, name, description, description_zh, topics_json)
    VALUES ('delete', old.id, old.full_name, old.owner, old.name, old.description, old.description_zh, old.topics_json);
END;

CREATE TRIGGER repositories_fts_update AFTER UPDATE ON repositories BEGIN
    INSERT INTO repositories_fts(repositories_fts, rowid, full_name, owner, name, description, description_zh, topics_json)
    VALUES ('delete', old.id, old.full_name, old.owner, old.name, old.description, old.description_zh, old.topics_json);
    INSERT INTO repositories_fts(rowid, full_name, owner, name, description, description_zh, topics_json)
    VALUES (new.id, new.full_name, new.owner, new.name, new.description, new.description_zh, new.topics_json);
END;
