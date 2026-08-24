-- 010_oncall_comment.sql: per-(week, person) free-text comment on on-call
-- assignments (e.g. to note that the on-call was triggered / a ticket number).
ALTER TABLE oncall ADD COLUMN comment TEXT NOT NULL DEFAULT '';