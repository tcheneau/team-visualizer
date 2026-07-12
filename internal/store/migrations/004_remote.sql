-- 004_remote.sql: Remote work flag on planning slots
ALTER TABLE planning ADD COLUMN remote INTEGER DEFAULT 0;