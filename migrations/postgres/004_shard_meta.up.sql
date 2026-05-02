-- E-13: shard-level metadata so the reschedule sweep can dedupe.
ALTER TABLE teo.shards
    ADD COLUMN IF NOT EXISTS meta JSONB NOT NULL DEFAULT '{}';
