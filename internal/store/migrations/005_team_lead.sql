-- 005_team_lead.sql: Team lead field on projects
ALTER TABLE projects ADD COLUMN team_lead TEXT DEFAULT '';
