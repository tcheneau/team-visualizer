-- 008_is_incident.sql: explicit incident flag on planning slots so that an
-- incident with empty text is still recognised as an incident (the incident
-- status used to be inferred solely from incident_text != '', which dropped
-- empty-text incidents from the Incidents view and from the grid after a
-- reload). Existing rows with non-empty text are backfilled.
ALTER TABLE planning ADD COLUMN is_incident INTEGER NOT NULL DEFAULT 0;
UPDATE planning SET is_incident = 1 WHERE incident_text != '';