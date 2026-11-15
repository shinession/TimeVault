-- TimeVault schema, version 2: per-source backup schedule.
--
-- schedule_minutes is the interval between automatic backups of a source; NULL means
-- backups are started by hand only. last_run_at is when the last backup run of the
-- source ended, whatever its outcome, so a source that keeps failing is retried once per
-- interval instead of in a loop.

ALTER TABLE sources
    ADD COLUMN schedule_minutes integer CHECK (schedule_minutes BETWEEN 1 AND 43200),
    ADD COLUMN last_run_at      timestamptz;
