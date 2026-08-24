-- 009_run_note.sql: optional free-text note on run-duty slots (e.g. a ticket
-- number or a heads-up for colleagues to steer interruptions). The run flag
-- is already a proper boolean column, so this only adds the note text.
ALTER TABLE planning ADD COLUMN run_note TEXT NOT NULL DEFAULT '';