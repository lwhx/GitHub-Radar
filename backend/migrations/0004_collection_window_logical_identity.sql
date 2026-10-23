-- Collection progress changes page, so page cannot participate in the logical window identity.
-- Reconcile duplicates created by the old page-sensitive key before enforcing the corrected key.
CREATE TEMP TABLE collection_window_dedup_map AS
SELECT
    id AS duplicate_id,
    FIRST_VALUE(id) OVER (
        PARTITION BY source, query_hash, time_from, time_to
        ORDER BY
            CASE coverage_status
                WHEN 'completed' THEN 4
                WHEN 'split' THEN 3
                WHEN 'pending' THEN 2
                WHEN 'coverage_incomplete' THEN 1
                ELSE 0
            END DESC,
            page DESC,
            COALESCE(updated_at, created_at, '') DESC,
            id ASC
    ) AS keeper_id
FROM collection_windows;

UPDATE job_run_events
SET window_id = (
    SELECT keeper_id
    FROM collection_window_dedup_map
    WHERE duplicate_id = job_run_events.window_id
)
WHERE window_id IN (
    SELECT duplicate_id
    FROM collection_window_dedup_map
    WHERE duplicate_id <> keeper_id
);

UPDATE collection_windows
SET parent_id = (
    SELECT keeper_id
    FROM collection_window_dedup_map
    WHERE duplicate_id = collection_windows.parent_id
)
WHERE parent_id IN (
    SELECT duplicate_id
    FROM collection_window_dedup_map
    WHERE duplicate_id <> keeper_id
);

DELETE FROM collection_windows
WHERE id IN (
    SELECT duplicate_id
    FROM collection_window_dedup_map
    WHERE duplicate_id <> keeper_id
);

DROP TABLE collection_window_dedup_map;

CREATE UNIQUE INDEX idx_collection_windows_logical_unique
ON collection_windows(source, query_hash, time_from, time_to);
