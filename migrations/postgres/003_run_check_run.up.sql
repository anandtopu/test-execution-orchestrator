-- E-10: link a TEO run to its GitHub Check Run so the observer can update it
-- as the run progresses (S-10-02, S-10-03).

ALTER TABLE teo.runs
    ADD COLUMN IF NOT EXISTS github_check_run_id BIGINT,
    ADD COLUMN IF NOT EXISTS github_installation_id BIGINT;

CREATE INDEX IF NOT EXISTS runs_github_check_run_idx
    ON teo.runs(github_check_run_id) WHERE github_check_run_id IS NOT NULL;
