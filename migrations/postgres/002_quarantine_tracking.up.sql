-- Track sweep state on flake_records so SLA nudges and un-quarantine
-- proposals are idempotent (S-15-03, S-15-04).

ALTER TABLE teo.flake_records
    ADD COLUMN IF NOT EXISTS last_nudged_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS unquarantine_proposed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS consecutive_passes INT NOT NULL DEFAULT 0;

-- Magic-link tokens for one-click un-quarantine confirmation.
CREATE TABLE IF NOT EXISTS teo.unquarantine_tokens (
    token TEXT PRIMARY KEY,
    test_id UUID NOT NULL REFERENCES teo.tests(id) ON DELETE CASCADE,
    issued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    consumed_by UUID REFERENCES teo.users(id)
);
CREATE INDEX IF NOT EXISTS unquarantine_tokens_test_idx ON teo.unquarantine_tokens(test_id);
