-- 003_features.sql: Activity log, user-person mapping, ICS tokens

ALTER TABLE users ADD COLUMN selected_person_id TEXT NOT NULL DEFAULT '';
ALTER TABLE people ADD COLUMN ics_token TEXT NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ts TEXT NOT NULL,
    actor TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL DEFAULT '',
    target TEXT NOT NULL DEFAULT '',
    detail TEXT NOT NULL DEFAULT ''
);
