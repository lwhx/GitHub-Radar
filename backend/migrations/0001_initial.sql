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
    github_created_at TEXT NOT NULL CHECK (length(github_created_at) = 20 AND github_created_at GLOB '????-??-??T??:??:??Z' AND github_created_at NOT GLOB '*[^0-9TZ:-]*' AND substr(github_created_at, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', github_created_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', github_created_at) = github_created_at),
    github_updated_at TEXT NOT NULL CHECK (length(github_updated_at) = 20 AND github_updated_at GLOB '????-??-??T??:??:??Z' AND github_updated_at NOT GLOB '*[^0-9TZ:-]*' AND substr(github_updated_at, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', github_updated_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', github_updated_at) = github_updated_at),
    pushed_at TEXT CHECK (pushed_at IS NULL OR (length(pushed_at) = 20 AND pushed_at GLOB '????-??-??T??:??:??Z' AND pushed_at NOT GLOB '*[^0-9TZ:-]*' AND substr(pushed_at, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', pushed_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', pushed_at) = pushed_at)),
    first_seen_at TEXT NOT NULL CHECK (length(first_seen_at) = 20 AND first_seen_at GLOB '????-??-??T??:??:??Z' AND first_seen_at NOT GLOB '*[^0-9TZ:-]*' AND substr(first_seen_at, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', first_seen_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', first_seen_at) = first_seen_at),
    last_synced_at TEXT CHECK (last_synced_at IS NULL OR (length(last_synced_at) = 20 AND last_synced_at GLOB '????-??-??T??:??:??Z' AND last_synced_at NOT GLOB '*[^0-9TZ:-]*' AND substr(last_synced_at, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', last_synced_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', last_synced_at) = last_synced_at)),
    last_successful_fetch_at TEXT CHECK (last_successful_fetch_at IS NULL OR (length(last_successful_fetch_at) = 20 AND last_successful_fetch_at GLOB '????-??-??T??:??:??Z' AND last_successful_fetch_at NOT GLOB '*[^0-9TZ:-]*' AND substr(last_successful_fetch_at, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', last_successful_fetch_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', last_successful_fetch_at) = last_successful_fetch_at)),
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
    captured_at TEXT NOT NULL CHECK (length(captured_at) = 20 AND captured_at GLOB '????-??-??T??:??:??Z' AND captured_at NOT GLOB '*[^0-9TZ:-]*' AND substr(captured_at, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', captured_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', captured_at) = captured_at),
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
    time_from TEXT NOT NULL CHECK (length(time_from) = 20 AND time_from GLOB '????-??-??T??:??:??Z' AND time_from NOT GLOB '*[^0-9TZ:-]*' AND substr(time_from, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', time_from) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', time_from) = time_from),
    time_to TEXT NOT NULL CHECK (length(time_to) = 20 AND time_to GLOB '????-??-??T??:??:??Z' AND time_to NOT GLOB '*[^0-9TZ:-]*' AND substr(time_to, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', time_to) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', time_to) = time_to),
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
    next_run_at TEXT CHECK (next_run_at IS NULL OR (length(next_run_at) = 20 AND next_run_at GLOB '????-??-??T??:??:??Z' AND next_run_at NOT GLOB '*[^0-9TZ:-]*' AND substr(next_run_at, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', next_run_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', next_run_at) = next_run_at)),
    priority INTEGER NOT NULL DEFAULT 0,
    config_json TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_collector_jobs_schedule ON collector_jobs(enabled, next_run_at, priority DESC);

CREATE TABLE job_runs (
    id INTEGER PRIMARY KEY,
    job_id INTEGER NOT NULL,
    started_at TEXT NOT NULL CHECK (length(started_at) = 20 AND started_at GLOB '????-??-??T??:??:??Z' AND started_at NOT GLOB '*[^0-9TZ:-]*' AND substr(started_at, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', started_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', started_at) = started_at),
    finished_at TEXT CHECK (finished_at IS NULL OR (length(finished_at) = 20 AND finished_at GLOB '????-??-??T??:??:??Z' AND finished_at NOT GLOB '*[^0-9TZ:-]*' AND substr(finished_at, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', finished_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', finished_at) = finished_at)),
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
    last_checked_at TEXT CHECK (last_checked_at IS NULL OR (length(last_checked_at) = 20 AND last_checked_at GLOB '????-??-??T??:??:??Z' AND last_checked_at NOT GLOB '*[^0-9TZ:-]*' AND substr(last_checked_at, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', last_checked_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', last_checked_at) = last_checked_at))
);
CREATE INDEX idx_github_credentials_enabled_status ON github_credentials(enabled, status);

CREATE TABLE api_rate_limits (
    credential_id INTEGER NOT NULL,
    resource TEXT NOT NULL,
    limit_value INTEGER NOT NULL CHECK (limit_value >= 0),
    remaining INTEGER NOT NULL CHECK (remaining >= 0),
    reset_at TEXT NOT NULL CHECK (length(reset_at) = 20 AND reset_at GLOB '????-??-??T??:??:??Z' AND reset_at NOT GLOB '*[^0-9TZ:-]*' AND substr(reset_at, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', reset_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', reset_at) = reset_at),
    cooldown_until TEXT CHECK (cooldown_until IS NULL OR (length(cooldown_until) = 20 AND cooldown_until GLOB '????-??-??T??:??:??Z' AND cooldown_until NOT GLOB '*[^0-9TZ:-]*' AND substr(cooldown_until, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', cooldown_until) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', cooldown_until) = cooldown_until)),
    updated_at TEXT NOT NULL CHECK (length(updated_at) = 20 AND updated_at GLOB '????-??-??T??:??:??Z' AND updated_at NOT GLOB '*[^0-9TZ:-]*' AND substr(updated_at, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', updated_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', updated_at) = updated_at),
    PRIMARY KEY (credential_id, resource),
    FOREIGN KEY (credential_id) REFERENCES github_credentials(id) ON DELETE CASCADE
);
CREATE INDEX idx_api_rate_limits_reset ON api_rate_limits(reset_at, cooldown_until);

CREATE TABLE admin_users (
    id INTEGER PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TEXT NOT NULL CHECK (length(created_at) = 20 AND created_at GLOB '????-??-??T??:??:??Z' AND created_at NOT GLOB '*[^0-9TZ:-]*' AND substr(created_at, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', created_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', created_at) = created_at),
    last_login_at TEXT CHECK (last_login_at IS NULL OR (length(last_login_at) = 20 AND last_login_at GLOB '????-??-??T??:??:??Z' AND last_login_at NOT GLOB '*[^0-9TZ:-]*' AND substr(last_login_at, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', last_login_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', last_login_at) = last_login_at))
);

CREATE TABLE admin_sessions (
    id INTEGER PRIMARY KEY,
    admin_id INTEGER NOT NULL,
    session_hash TEXT NOT NULL UNIQUE,
    csrf_hash TEXT,
    expires_at TEXT NOT NULL CHECK (length(expires_at) = 20 AND expires_at GLOB '????-??-??T??:??:??Z' AND expires_at NOT GLOB '*[^0-9TZ:-]*' AND substr(expires_at, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', expires_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', expires_at) = expires_at),
    created_at TEXT NOT NULL CHECK (length(created_at) = 20 AND created_at GLOB '????-??-??T??:??:??Z' AND created_at NOT GLOB '*[^0-9TZ:-]*' AND substr(created_at, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', created_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', created_at) = created_at),
    revoked_at TEXT CHECK (revoked_at IS NULL OR (length(revoked_at) = 20 AND revoked_at GLOB '????-??-??T??:??:??Z' AND revoked_at NOT GLOB '*[^0-9TZ:-]*' AND substr(revoked_at, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', revoked_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', revoked_at) = revoked_at)),
    FOREIGN KEY (admin_id) REFERENCES admin_users(id) ON DELETE CASCADE
);
CREATE INDEX idx_admin_sessions_admin ON admin_sessions(admin_id, revoked_at, expires_at);
CREATE INDEX idx_admin_sessions_expires ON admin_sessions(expires_at);

CREATE TABLE site_settings (
    key TEXT NOT NULL PRIMARY KEY,
    json_value TEXT NOT NULL,
    updated_at TEXT NOT NULL CHECK (length(updated_at) = 20 AND updated_at GLOB '????-??-??T??:??:??Z' AND updated_at NOT GLOB '*[^0-9TZ:-]*' AND substr(updated_at, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', updated_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', updated_at) = updated_at)
);

CREATE TABLE audit_logs (
    id INTEGER PRIMARY KEY,
    admin_id INTEGER,
    action TEXT NOT NULL,
    target TEXT NOT NULL,
    result TEXT NOT NULL,
    created_at TEXT NOT NULL CHECK (length(created_at) = 20 AND created_at GLOB '????-??-??T??:??:??Z' AND created_at NOT GLOB '*[^0-9TZ:-]*' AND substr(created_at, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', created_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', created_at) = created_at),
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
