-- 006_offsite_incident.sql: Off-site flag and incident text on planning slots
ALTER TABLE planning ADD COLUMN offsite INTEGER DEFAULT 0;
ALTER TABLE planning ADD COLUMN incident_text TEXT DEFAULT '';