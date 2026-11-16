-- TimeVault schema, version 4: audit duration, version limit, partial recovery points, run timing.
--
-- audit_events.duration_ms is how long the audited operation ran; NULL when it has no duration
-- (sign-ins, requests) and for rows written before this version.
--
-- sources.max_versions is the most versions of one file that are kept; NULL means unlimited. A
-- prune deletes the older versions of a file once it has more. A recovery point that lost versions
-- that way is marked partial: it no longer shows the whole folder as it was.
--
-- sources.last_run_ms, last_walk_at and last_walk_items feed the scheduler: how long the last backup run
-- took, when the last complete scan of the folder started, and how many entries the search engine held
-- below the folder then (a deletion only shows as a smaller number).
--
-- The version index lets a prune ask which versions still refer to an object, so an object that no
-- version needs any more can be deleted.

ALTER TABLE audit_events
    ADD COLUMN duration_ms bigint CHECK (duration_ms IS NULL OR duration_ms >= 0);

ALTER TABLE sources
    ADD COLUMN max_versions    integer CHECK (max_versions IS NULL OR max_versions BETWEEN 1 AND 10000),
    ADD COLUMN last_run_ms     bigint  CHECK (last_run_ms IS NULL OR last_run_ms >= 0),
    ADD COLUMN last_walk_at    timestamptz,
    ADD COLUMN last_walk_items bigint  CHECK (last_walk_items IS NULL OR last_walk_items >= 0);

ALTER TABLE recovery_points
    ADD COLUMN partial boolean NOT NULL DEFAULT false;

ALTER TABLE jobs DROP CONSTRAINT jobs_kind_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_kind_check CHECK (kind IN ('backup', 'verify', 'restore', 'prune'));
CREATE UNIQUE INDEX jobs_one_active_prune_uq ON jobs (source_id) WHERE kind = 'prune' AND state IN ('queued', 'running');

CREATE INDEX node_versions_sha_idx ON node_versions (sha256) WHERE sha256 IS NOT NULL;
