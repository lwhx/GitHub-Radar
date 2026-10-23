-- README 缓存：按需抓取，24 小时内优先命中本地副本。
-- 只存经过净化的 Markdown 正文与抓取结果，不存任何凭据。
CREATE TABLE readme_cache (
    repo_id INTEGER PRIMARY KEY,
    status TEXT NOT NULL CHECK (status IN ('ok', 'missing')),
    content TEXT,
    media_type TEXT,
    fetched_at TEXT NOT NULL CHECK (length(fetched_at) = 20 AND fetched_at GLOB '????-??-??T??:??:??Z' AND fetched_at NOT GLOB '*[^0-9TZ:-]*' AND substr(fetched_at, 12, 2) BETWEEN '00' AND '23' AND strftime('%Y-%m-%dT%H:%M:%SZ', fetched_at) IS NOT NULL AND strftime('%Y-%m-%dT%H:%M:%SZ', fetched_at) = fetched_at),
    FOREIGN KEY (repo_id) REFERENCES repositories(id) ON DELETE CASCADE,
    CHECK ((status = 'ok' AND content IS NOT NULL) OR (status = 'missing' AND content IS NULL))
);
CREATE INDEX idx_readme_cache_fetched_at ON readme_cache(fetched_at);
