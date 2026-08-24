-- 011_tentative_away.sql: tentative flag on away slots. An away slot marked
-- tentative shows an intent that still needs confirming (rendered hatched).
ALTER TABLE planning ADD COLUMN tentative INTEGER NOT NULL DEFAULT 0;