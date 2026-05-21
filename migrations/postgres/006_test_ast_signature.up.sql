-- S-14-01 / S-06-01: per-test AST signature, computed at discovery and folded
-- into the fingerprint. Stored separately so a future feature can link a moved
-- or renamed test to its prior identity by matching the signature.
ALTER TABLE teo.tests
    ADD COLUMN IF NOT EXISTS ast_signature TEXT NOT NULL DEFAULT '';

-- Lookup index for "find tests with this body" (move/rename linking).
CREATE INDEX IF NOT EXISTS tests_ast_signature_idx
    ON teo.tests (repo_id, ast_signature)
    WHERE ast_signature <> '';
