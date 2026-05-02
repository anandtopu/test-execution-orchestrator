DROP INDEX IF EXISTS teo.runs_github_check_run_idx;
ALTER TABLE teo.runs
    DROP COLUMN IF EXISTS github_check_run_id,
    DROP COLUMN IF EXISTS github_installation_id;
