DROP TABLE IF EXISTS teo.unquarantine_tokens;
ALTER TABLE teo.flake_records
    DROP COLUMN IF EXISTS last_nudged_at,
    DROP COLUMN IF EXISTS unquarantine_proposed_at,
    DROP COLUMN IF EXISTS consecutive_passes;
