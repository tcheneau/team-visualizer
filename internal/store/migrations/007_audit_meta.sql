-- 007_audit_meta.sql: structured metadata column on the audit log so the
-- Activity tab can resolve targeted team members and render rich info.
-- The column stores a JSON string. Old rows are backfilled in Go (store.go)
-- because the parsing of existing target/detail strings is non-trivial.
ALTER TABLE audit_log ADD COLUMN meta TEXT NOT NULL DEFAULT ''