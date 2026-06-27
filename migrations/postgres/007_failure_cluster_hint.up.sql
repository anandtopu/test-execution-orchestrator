-- ADR-0021: LLM-generated root-cause hint per failure cluster. Populated by the
-- opt-in `result-pipeline llm-hints` cron. All columns are nullable so a cluster
-- without a hint (feature disabled, generation failed, or not yet run) is a
-- first-class state every read surface degrades to gracefully.
ALTER TABLE teo.failure_clusters
    ADD COLUMN IF NOT EXISTS root_cause_hint   TEXT,
    ADD COLUMN IF NOT EXISTS hint_category     TEXT,
    ADD COLUMN IF NOT EXISTS hint_confidence   REAL,
    ADD COLUMN IF NOT EXISTS hint_generated_at TIMESTAMPTZ;
