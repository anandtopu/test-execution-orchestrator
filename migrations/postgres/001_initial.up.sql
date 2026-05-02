-- TEO initial Postgres schema. Source of truth: docs/architecture/data-model.md §1.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE SCHEMA IF NOT EXISTS teo;

-- Reusable updated_at trigger
CREATE OR REPLACE FUNCTION teo.set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- ---------------------------------------------------------------- repos
CREATE TABLE teo.repos (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vcs TEXT NOT NULL CHECK (vcs IN ('github')),
    full_name TEXT NOT NULL,
    default_branch TEXT NOT NULL DEFAULT 'main',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    auto_quarantine_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(vcs, full_name)
);
CREATE TRIGGER repos_set_updated_at BEFORE UPDATE ON teo.repos
    FOR EACH ROW EXECUTE FUNCTION teo.set_updated_at();

-- ---------------------------------------------------------------- runs
CREATE TABLE teo.runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    repo_id UUID NOT NULL REFERENCES teo.repos(id) ON DELETE CASCADE,
    commit_sha TEXT NOT NULL,
    branch TEXT NOT NULL,
    triggered_by TEXT NOT NULL,
    trigger_actor TEXT,
    trigger_pr_number INT,
    status TEXT NOT NULL CHECK (status IN (
        'pending','planning','dispatching','running',
        'finalizing','succeeded','failed','cancelled')),
    budget_seconds INT,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    total_duration_ms INT,
    worker_minutes_used NUMERIC(10,2),
    spot_minutes NUMERIC(10,2) NOT NULL DEFAULT 0,
    on_demand_minutes NUMERIC(10,2) NOT NULL DEFAULT 0,
    preemption_count INT NOT NULL DEFAULT 0,
    parent_run_id UUID REFERENCES teo.runs(id),
    meta JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX runs_repo_started_idx ON teo.runs(repo_id, started_at DESC);
CREATE INDEX runs_commit_idx ON teo.runs(commit_sha);
CREATE INDEX runs_status_idx ON teo.runs(status) WHERE status NOT IN ('succeeded','failed','cancelled');
CREATE TRIGGER runs_set_updated_at BEFORE UPDATE ON teo.runs
    FOR EACH ROW EXECUTE FUNCTION teo.set_updated_at();

-- ---------------------------------------------------------------- shards
CREATE TABLE teo.shards (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES teo.runs(id) ON DELETE CASCADE,
    index INT NOT NULL,
    worker_id TEXT,
    predicted_duration_ms INT NOT NULL,
    actual_duration_ms INT,
    status TEXT NOT NULL CHECK (status IN (
        'pending','running','succeeded','failed','lost','preempted')),
    test_count INT NOT NULL,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(run_id, index)
);
CREATE INDEX shards_run_idx ON teo.shards(run_id);
CREATE TRIGGER shards_set_updated_at BEFORE UPDATE ON teo.shards
    FOR EACH ROW EXECUTE FUNCTION teo.set_updated_at();

-- ---------------------------------------------------------------- tests
CREATE TABLE teo.tests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    repo_id UUID NOT NULL REFERENCES teo.repos(id) ON DELETE CASCADE,
    fingerprint TEXT NOT NULL,
    path TEXT NOT NULL,
    name TEXT NOT NULL,
    params_hash TEXT NOT NULL DEFAULT '',
    runner TEXT NOT NULL DEFAULT 'pytest',
    owner_team TEXT,
    tags TEXT[] NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','quarantined','broken','deleted')),
    quarantined_at TIMESTAMPTZ,
    quarantine_reason TEXT,
    first_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(repo_id, fingerprint)
);
CREATE INDEX tests_repo_path_idx ON teo.tests(repo_id, path);
CREATE INDEX tests_quarantined_idx ON teo.tests(repo_id) WHERE status = 'quarantined';
CREATE INDEX tests_owner_idx ON teo.tests(owner_team) WHERE owner_team IS NOT NULL;
CREATE TRIGGER tests_set_updated_at BEFORE UPDATE ON teo.tests
    FOR EACH ROW EXECUTE FUNCTION teo.set_updated_at();

-- ---------------------------------------------------------------- failure_clusters
CREATE TABLE teo.failure_clusters (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    repo_id UUID NOT NULL REFERENCES teo.repos(id) ON DELETE CASCADE,
    stack_fingerprint TEXT NOT NULL,
    representative_message TEXT NOT NULL,
    representative_stack TEXT NOT NULL,
    first_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
    occurrences BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(repo_id, stack_fingerprint)
);
CREATE INDEX failure_clusters_recency_idx ON teo.failure_clusters(repo_id, last_seen DESC);
CREATE TRIGGER failure_clusters_set_updated_at BEFORE UPDATE ON teo.failure_clusters
    FOR EACH ROW EXECUTE FUNCTION teo.set_updated_at();

-- ---------------------------------------------------------------- test_executions
CREATE TABLE teo.test_executions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shard_id UUID NOT NULL REFERENCES teo.shards(id) ON DELETE CASCADE,
    test_id UUID NOT NULL REFERENCES teo.tests(id),
    attempt INT NOT NULL DEFAULT 1,
    outcome TEXT NOT NULL CHECK (outcome IN ('passed','failed','skipped','errored','timed_out','interrupted')),
    duration_ms INT NOT NULL,
    otel_trace_id TEXT,
    failure_cluster_id UUID REFERENCES teo.failure_clusters(id),
    log_object_key TEXT,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(shard_id, test_id, attempt)
);
CREATE INDEX te_test_started_idx ON teo.test_executions(test_id, started_at DESC);
CREATE INDEX te_outcome_idx ON teo.test_executions(outcome) WHERE outcome IN ('failed','errored','timed_out');
CREATE INDEX te_shard_idx ON teo.test_executions(shard_id);

-- ---------------------------------------------------------------- flake_records
CREATE TABLE teo.flake_records (
    test_id UUID PRIMARY KEY REFERENCES teo.tests(id) ON DELETE CASCADE,
    flake_rate NUMERIC(5,4) NOT NULL,
    wilson_lower NUMERIC(5,4) NOT NULL,
    wilson_upper NUMERIC(5,4) NOT NULL,
    sample_size INT NOT NULL,
    category TEXT,
    quarantined_at TIMESTAMPTZ,
    unquarantined_at TIMESTAMPTZ,
    github_issue_number INT,
    github_issue_url TEXT,
    last_recomputed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    evidence JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TRIGGER flake_records_set_updated_at BEFORE UPDATE ON teo.flake_records
    FOR EACH ROW EXECUTE FUNCTION teo.set_updated_at();

-- ---------------------------------------------------------------- users
CREATE TABLE teo.users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    oidc_subject TEXT UNIQUE,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    digest_opt_out BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TRIGGER users_set_updated_at BEFORE UPDATE ON teo.users
    FOR EACH ROW EXECUTE FUNCTION teo.set_updated_at();

-- ---------------------------------------------------------------- api_keys
CREATE TABLE teo.api_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    prefix TEXT NOT NULL UNIQUE,
    hash TEXT NOT NULL,
    name TEXT NOT NULL,
    created_by UUID REFERENCES teo.users(id),
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    scopes TEXT[] NOT NULL,
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX api_keys_active_idx ON teo.api_keys(prefix) WHERE revoked_at IS NULL;
CREATE TRIGGER api_keys_set_updated_at BEFORE UPDATE ON teo.api_keys
    FOR EACH ROW EXECUTE FUNCTION teo.set_updated_at();

-- ---------------------------------------------------------------- user_roles
-- A user can hold a role globally (repo_id NULL) or scoped to a specific repo.
-- Postgres rejects function calls in PRIMARY KEY (so the original
-- COALESCE-based PK was invalid SQL); two partial unique indexes enforce the
-- same semantics: (user, role) is unique among global rows, and
-- (user, role, repo) is unique among per-repo rows.
CREATE TABLE teo.user_roles (
    user_id UUID NOT NULL REFERENCES teo.users(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('admin','engineer','read_only')),
    repo_id UUID REFERENCES teo.repos(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX user_roles_global_idx
    ON teo.user_roles (user_id, role)
    WHERE repo_id IS NULL;
CREATE UNIQUE INDEX user_roles_per_repo_idx
    ON teo.user_roles (user_id, role, repo_id)
    WHERE repo_id IS NOT NULL;

-- ---------------------------------------------------------------- audit_log
CREATE TABLE teo.audit_log (
    id BIGSERIAL PRIMARY KEY,
    at TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_user_id UUID REFERENCES teo.users(id),
    actor_api_key_id UUID REFERENCES teo.api_keys(id),
    action TEXT NOT NULL,
    target_type TEXT,
    target_id TEXT,
    meta JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_at_idx ON teo.audit_log(at);
CREATE INDEX audit_action_idx ON teo.audit_log(action, at);

-- ---------------------------------------------------------------- github_installations
CREATE TABLE teo.github_installations (
    id BIGINT PRIMARY KEY,
    account_login TEXT NOT NULL,
    account_type TEXT NOT NULL,
    suspended BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TRIGGER github_installations_set_updated_at BEFORE UPDATE ON teo.github_installations
    FOR EACH ROW EXECUTE FUNCTION teo.set_updated_at();

-- ---------------------------------------------------------------- run_plans (assignment-plan archive)
CREATE TABLE teo.run_plans (
    run_id UUID PRIMARY KEY REFERENCES teo.runs(id) ON DELETE CASCADE,
    plan JSONB NOT NULL,
    plan_version TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
