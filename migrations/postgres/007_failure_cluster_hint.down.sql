ALTER TABLE teo.failure_clusters
    DROP COLUMN IF EXISTS hint_generated_at,
    DROP COLUMN IF EXISTS hint_confidence,
    DROP COLUMN IF EXISTS hint_category,
    DROP COLUMN IF EXISTS root_cause_hint;
