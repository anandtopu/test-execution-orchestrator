-- Enforce Idempotency-Key uniqueness per repo at the schema level so two
-- concurrent POSTs that race the application-level SELECT can't both insert.
-- Partial index ignores rows without an idempotency_key (the common case).
--
-- The handler still does a SELECT-first to return 200 OK on a clean replay;
-- this index turns the race-window window into a Postgres unique-violation
-- the handler catches and recovers from.

CREATE UNIQUE INDEX IF NOT EXISTS runs_idempotency_key_uniq
    ON teo.runs (repo_id, (meta->>'idempotency_key'))
    WHERE meta ? 'idempotency_key';
