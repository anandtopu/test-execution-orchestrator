# TEO — Entity-Relationship Diagram (Postgres)

**Status:** Current — matches `migrations/postgres/001..006`.
**Companion:** [`schema.md`](schema.md) (column-level detail + ClickHouse + S3).

This is the **Postgres** (`teo` schema) relational model. ClickHouse tables (`test_runs`, `span_events`, `flake_observations`, `mv_run_summary`) are analytical and have **no foreign keys** — they join to these entities by id at query time, so they are intentionally omitted here (see [`schema.md` §2](schema.md)).

Crow's-foot notation: `||--o{` = one-to-many (optional child), `||--o|` = one-to-zero-or-one. `PK` = primary key, `FK` = foreign key, `UK` = unique.

```mermaid
erDiagram
    repos ||--o{ runs : "has"
    repos ||--o{ tests : "owns"
    repos ||--o{ failure_clusters : "groups"
    repos ||--o{ user_roles : "scopes"
    runs ||--o{ shards : "splits into"
    runs ||--o| run_plans : "archives"
    runs ||--o{ runs : "rerun of (parent_run_id)"
    shards ||--o{ test_executions : "records"
    tests ||--o{ test_executions : "executed as"
    tests ||--o| flake_records : "scored by"
    tests ||--o{ unquarantine_tokens : "linked by"
    failure_clusters ||--o{ test_executions : "clusters"
    users ||--o{ api_keys : "created"
    users ||--o{ user_roles : "granted"
    users ||--o{ audit_log : "acted"
    users ||--o{ unquarantine_tokens : "consumed"
    api_keys ||--o{ audit_log : "acted"
    github_installations ||--o{ runs : "soft-links"

    repos {
        uuid id PK
        text vcs "CHECK github"
        text full_name "UK(vcs,full_name)"
        text default_branch
        bool enabled
        bool auto_quarantine_enabled
    }

    runs {
        uuid id PK
        uuid repo_id FK
        text commit_sha
        text branch
        text triggered_by
        text status "CHECK 8 states"
        int  budget_seconds
        numeric worker_minutes_used
        numeric spot_minutes
        numeric on_demand_minutes
        int  preemption_count
        uuid parent_run_id FK "self"
        bigint github_check_run_id
        bigint github_installation_id
        jsonb meta "UK(repo_id, idempotency_key)"
    }

    shards {
        uuid id PK
        uuid run_id FK
        int  index "UK(run_id,index)"
        text worker_id
        int  predicted_duration_ms
        int  actual_duration_ms
        text status "CHECK incl lost,preempted"
        int  test_count
        jsonb meta
    }

    run_plans {
        uuid run_id PK "FK to runs"
        jsonb plan
        text plan_version
    }

    tests {
        uuid id PK
        uuid repo_id FK
        text fingerprint "UK(repo_id,fingerprint)"
        text path
        text name
        text params_hash
        text runner
        text owner_team
        text status "CHECK active/quarantined/broken/deleted"
        text ast_signature
    }

    test_executions {
        uuid id PK
        uuid shard_id FK
        uuid test_id FK
        int  attempt "UK(shard_id,test_id,attempt)"
        text outcome "CHECK 6 outcomes"
        int  duration_ms
        text otel_trace_id
        uuid failure_cluster_id FK
        text log_object_key
    }

    failure_clusters {
        uuid id PK
        uuid repo_id FK
        text stack_fingerprint "UK(repo_id,fp)"
        text representative_message
        text representative_stack
        bigint occurrences
    }

    flake_records {
        uuid test_id PK "FK to tests"
        numeric flake_rate
        numeric wilson_lower
        numeric wilson_upper
        int  sample_size
        text category
        int  github_issue_number
        int  consecutive_passes
        timestamptz last_nudged_at
        timestamptz unquarantine_proposed_at
        jsonb evidence
    }

    unquarantine_tokens {
        text token PK
        uuid test_id FK
        timestamptz expires_at
        timestamptz consumed_at
        uuid consumed_by FK
    }

    users {
        uuid id PK
        text email UK
        text display_name
        text oidc_subject UK
        bool digest_opt_out
    }

    api_keys {
        uuid id PK
        text prefix UK
        text hash "argon2id"
        uuid created_by FK
        timestamptz expires_at
        timestamptz revoked_at
        text_array scopes
    }

    user_roles {
        uuid user_id FK
        text role "CHECK admin/engineer/read_only"
        uuid repo_id FK "NULL = global"
    }

    audit_log {
        bigserial id PK
        timestamptz at
        uuid actor_user_id FK
        uuid actor_api_key_id FK
        text action
        text target_type
        text target_id
        jsonb meta
    }

    github_installations {
        bigint id PK
        text account_login
        text account_type
        bool suspended
    }
```

## Reading notes

- **`test_executions` is the hub.** It is the only table that joins the *physical* execution lineage (`run → shard`) to the *logical* test identity (`tests`), and optionally to a `failure_clusters` row. One row per `(shard, test, attempt)`.
- **`runs.parent_run_id`** is a self-reference capturing rerun lineage (e.g. "rerun failed tests" creates a child run).
- **`user_roles`** has no single-column PK — uniqueness is enforced by two *partial* unique indexes (global rows where `repo_id IS NULL`, and per-repo rows otherwise). The crow's-foot links are still `users`/`repos` FKs.
- **`github_installations ⇢ runs`** is drawn as a relationship for clarity, but it is a **soft link by value** (`runs.github_installation_id BIGINT`), not a declared FK constraint.
- **Idempotency**: `runs` carries no dedicated column — the idempotency key lives in `meta->>'idempotency_key'` with a partial unique index (migration 005).
