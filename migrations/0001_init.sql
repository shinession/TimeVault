-- TimeVault schema, version 1.
--
-- History model: every path is a `nodes` row (a slot identified by its parent and exact
-- name). The states a slot goes through are `node_versions` rows, each valid for the
-- recovery-point sequence numbers first_point..last_point (last_point NULL = still the
-- newest state). A recovery point only writes rows for what changed, so unchanged files
-- cost nothing and a renamed folder does not copy its children.
--
-- A build writes rows under the sequence number of the point it is creating. They stay
-- invisible because readers only query committed sequence numbers, and anything a failed
-- build left behind is removed before the next build starts.

CREATE TABLE sources (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    root_path   text        NOT NULL,
    root_key    text        NOT NULL,
    excludes    text[]      NOT NULL DEFAULT '{}',
    enabled     boolean     NOT NULL DEFAULT true,
    created_by  text        NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT sources_name_uq UNIQUE (name),
    CONSTRAINT sources_root_uq UNIQUE (root_key)
);

CREATE TABLE objects (
    sha256      bytea       PRIMARY KEY CHECK (octet_length(sha256) = 32),
    size        bigint      NOT NULL CHECK (size >= 0),
    created_at  timestamptz NOT NULL DEFAULT now(),
    verified_at timestamptz
);

CREATE TABLE recovery_points (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_id        bigint      NOT NULL REFERENCES sources (id),
    seq              integer     NOT NULL CHECK (seq > 0),
    kind             text        NOT NULL CHECK (kind IN ('full', 'incremental')),
    state            text        NOT NULL CHECK (state IN ('building', 'committed', 'failed', 'aborted')),
    consistency      text        NOT NULL DEFAULT 'live_scan' CHECK (consistency IN ('live_scan', 'vss')),
    captured_at      timestamptz NOT NULL DEFAULT now(),
    committed_at     timestamptz,
    file_count       bigint      NOT NULL DEFAULT 0,
    dir_count        bigint      NOT NULL DEFAULT 0,
    total_bytes      bigint      NOT NULL DEFAULT 0,
    added_count      bigint      NOT NULL DEFAULT 0,
    modified_count   bigint      NOT NULL DEFAULT 0,
    deleted_count    bigint      NOT NULL DEFAULT 0,
    new_object_count bigint      NOT NULL DEFAULT 0,
    new_object_bytes bigint      NOT NULL DEFAULT 0,
    skipped_count    bigint      NOT NULL DEFAULT 0,
    manifest_sha256  bytea,
    verification     text        NOT NULL DEFAULT 'pending' CHECK (verification IN ('pending', 'validated', 'verified', 'failed')),
    verified_at      timestamptz,
    failure_reason   text,
    CONSTRAINT recovery_points_seq_uq UNIQUE (source_id, seq)
);
CREATE UNIQUE INDEX recovery_points_one_building_uq ON recovery_points (source_id) WHERE state = 'building';
CREATE INDEX recovery_points_committed_idx ON recovery_points (source_id, seq DESC) WHERE state = 'committed';

CREATE TABLE nodes (
    id        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_id bigint NOT NULL REFERENCES sources (id),
    parent_id bigint REFERENCES nodes (id),
    name      text   NOT NULL COLLATE "C",
    name_key  text   NOT NULL COLLATE "C"
);
CREATE UNIQUE INDEX nodes_child_uq ON nodes (parent_id, name) WHERE parent_id IS NOT NULL;
CREATE UNIQUE INDEX nodes_root_uq ON nodes (source_id) WHERE parent_id IS NULL;
CREATE INDEX nodes_children_idx ON nodes (parent_id, name_key, name) WHERE parent_id IS NOT NULL;

CREATE TABLE node_versions (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    node_id     bigint  NOT NULL REFERENCES nodes (id),
    source_id   bigint  NOT NULL REFERENCES sources (id),
    first_point integer NOT NULL CHECK (first_point > 0),
    last_point  integer,
    is_dir      boolean NOT NULL,
    state       text    NOT NULL DEFAULT 'ok' CHECK (state IN ('ok', 'skipped')),
    skip_reason text,
    size        bigint  NOT NULL DEFAULT 0 CHECK (size >= 0),
    mtime_ns    bigint  NOT NULL DEFAULT 0,
    attrs       integer NOT NULL DEFAULT 0,
    sha256      bytea   REFERENCES objects (sha256),
    CONSTRAINT node_versions_range_ck CHECK (last_point IS NULL OR last_point >= first_point),
    CONSTRAINT node_versions_content_ck CHECK (
        (is_dir AND sha256 IS NULL)
        OR (NOT is_dir AND ((state = 'skipped' AND sha256 IS NULL) OR (state = 'ok' AND sha256 IS NOT NULL)))
    )
);
CREATE INDEX node_versions_node_idx ON node_versions (node_id, first_point DESC);
CREATE INDEX node_versions_first_idx ON node_versions (source_id, first_point);
CREATE INDEX node_versions_last_idx ON node_versions (source_id, last_point) WHERE last_point IS NOT NULL;

CREATE TABLE jobs (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind            text        NOT NULL CHECK (kind IN ('backup', 'verify', 'restore')),
    source_id       bigint      REFERENCES sources (id),
    point_id        bigint      REFERENCES recovery_points (id),
    state           text        NOT NULL CHECK (state IN ('queued', 'running', 'succeeded', 'failed', 'canceled')),
    phase           text        NOT NULL DEFAULT '',
    params          jsonb       NOT NULL DEFAULT '{}',
    progress        jsonb       NOT NULL DEFAULT '{}',
    error           text,
    requested_by    text        NOT NULL,
    idempotency_key text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    started_at      timestamptz,
    finished_at     timestamptz
);
CREATE UNIQUE INDEX jobs_idempotency_uq ON jobs (kind, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX jobs_one_active_backup_uq ON jobs (source_id) WHERE kind = 'backup' AND state IN ('queued', 'running');
CREATE UNIQUE INDEX jobs_one_active_verify_uq ON jobs (point_id) WHERE kind = 'verify' AND state IN ('queued', 'running');
CREATE INDEX jobs_queue_idx ON jobs (id) WHERE state = 'queued';
CREATE INDEX jobs_source_idx ON jobs (source_id, id DESC);

CREATE TABLE job_events (
    id      bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    job_id  bigint      NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    at      timestamptz NOT NULL DEFAULT now(),
    level   text        NOT NULL CHECK (level IN ('info', 'warn', 'error')),
    message text        NOT NULL
);
CREATE INDEX job_events_job_idx ON job_events (job_id, id);

CREATE TABLE restore_items (
    id       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    job_id   bigint NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    rel_path text   NOT NULL,
    is_dir   boolean NOT NULL DEFAULT false,
    action   text   NOT NULL,
    status   text   NOT NULL CHECK (status IN ('ok', 'skipped', 'failed')),
    bytes    bigint NOT NULL DEFAULT 0,
    message  text
);
CREATE INDEX restore_items_job_idx ON restore_items (job_id, id);

CREATE TABLE audit_events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at          timestamptz NOT NULL DEFAULT now(),
    actor       text        NOT NULL,
    action      text        NOT NULL,
    target_type text        NOT NULL DEFAULT '',
    target_id   text        NOT NULL DEFAULT '',
    source_id   bigint,
    result      text        NOT NULL DEFAULT 'ok' CHECK (result IN ('ok', 'denied', 'failed')),
    remote_addr text        NOT NULL DEFAULT '',
    detail      jsonb       NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_events_action_idx ON audit_events (action, id DESC);

CREATE FUNCTION audit_events_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_events is append-only';
END;
$$;

CREATE TRIGGER audit_events_no_change
    BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_events_immutable();
