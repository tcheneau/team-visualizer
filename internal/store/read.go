package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/teamviz/team-visualizer/internal/model"
)

// ===== People =====

func (s *Store) ListPeople() ([]model.Person, error) {
	rows, err := s.db.Query(`SELECT id, name, role, sub_team, avatar_emoji, avatar_color, start_date, default_projects, status, archived_date, is_guest, ics_token FROM people ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var people []model.Person
	for rows.Next() {
		p, err := scanPerson(rows)
		if err != nil {
			return nil, err
		}
		people = append(people, p)
	}
	return people, nil
}

func (s *Store) GetPerson(id string) (*model.Person, error) {
	row := s.db.QueryRow(`SELECT id, name, role, sub_team, avatar_emoji, avatar_color, start_date, default_projects, status, archived_date, is_guest, ics_token FROM people WHERE id = ?`, id)
	p, err := scanPerson(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func scanPerson(r interface{ Scan(...any) error }) (model.Person, error) {
	var p model.Person
	var projectsJSON string
	var isGuest int
	err := r.Scan(&p.ID, &p.Name, &p.Role, &p.SubTeam, &p.AvatarEmoji, &p.AvatarColor, &p.StartDate, &projectsJSON, &p.Status, &p.ArchivedDate, &isGuest, &p.ICS_TOKEN)
	if err != nil {
		return p, err
	}
	p.IsGuest = isGuest != 0
	if projectsJSON != "" && projectsJSON != "[]" {
		json.Unmarshal([]byte(projectsJSON), &p.DefaultProjects)
	}
	return p, nil
}

// ===== Planning =====

func (s *Store) GetPlanning(startDate, endDate string) ([]model.PlanningEntry, error) {
	rows, err := s.db.Query(`
		SELECT person_id, date, slot, state, away_type, away_note, run, projects, remote, offsite, incident_text
		FROM planning
		WHERE date >= ? AND date <= ? AND state != 'not_filled'
		ORDER BY person_id, date, slot`, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []model.PlanningEntry
	for rows.Next() {
		var e model.PlanningEntry
		var awayType, awayNote, projectsJSON, incidentText string
		var run, remote, offsite int
		err := rows.Scan(&e.PersonID, &e.Date, &e.Slot, &e.Data.State, &awayType, &awayNote, &run, &projectsJSON, &remote, &offsite, &incidentText)
		if err != nil {
			return nil, err
		}
		if awayType != "" {
			e.Data.Away = &model.AwayData{Type: awayType, Note: awayNote}
		}
		if incidentText != "" {
			e.Data.Incident = &model.IncidentData{Text: incidentText}
		}
		e.Data.Run = run != 0
		e.Data.Remote = remote != 0
		e.Data.Offsite = offsite != 0
		if projectsJSON != "" && projectsJSON != "[]" {
			if err := json.Unmarshal([]byte(projectsJSON), &e.Data.Projects); err != nil {
				return nil, fmt.Errorf("scan planning projects: %w", err)
			}
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// GetPlanningForPerson returns all planning entries for a specific person in a date range.
func (s *Store) GetPlanningForPerson(personID, startDate, endDate string) ([]model.PlanningEntry, error) {
	rows, err := s.db.Query(`
		SELECT person_id, date, slot, state, away_type, away_note, run, projects, remote, offsite, incident_text
		FROM planning
		WHERE person_id = ? AND date >= ? AND date <= ? AND state != 'not_filled'
		ORDER BY date, slot`, personID, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []model.PlanningEntry
	for rows.Next() {
		var e model.PlanningEntry
		var awayType, awayNote, projectsJSON, incidentText string
		var run, remote, offsite int
		err := rows.Scan(&e.PersonID, &e.Date, &e.Slot, &e.Data.State, &awayType, &awayNote, &run, &projectsJSON, &remote, &offsite, &incidentText)
		if err != nil {
			return nil, err
		}
		if awayType != "" {
			e.Data.Away = &model.AwayData{Type: awayType, Note: awayNote}
		}
		if incidentText != "" {
			e.Data.Incident = &model.IncidentData{Text: incidentText}
		}
		e.Data.Run = run != 0
		e.Data.Remote = remote != 0
		e.Data.Offsite = offsite != 0
		if projectsJSON != "" && projectsJSON != "[]" {
			if err := json.Unmarshal([]byte(projectsJSON), &e.Data.Projects); err != nil {
				return nil, fmt.Errorf("scan person planning projects: %w", err)
			}
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// ===== Projects =====

func (s *Store) ListProjects() ([]model.Project, error) {
	rows, err := s.db.Query(`SELECT id, name, emoji, description, url, start_date, end_date, status, team_lead FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []model.Project
	for rows.Next() {
		var p model.Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Emoji, &p.Description, &p.URL, &p.StartDate, &p.EndDate, &p.Status, &p.TeamLead); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, nil
}

func (s *Store) GetProject(id string) (*model.Project, error) {
	var p model.Project
	err := s.db.QueryRow(`SELECT id, name, emoji, description, url, start_date, end_date, status, team_lead FROM projects WHERE id = ?`, id).
		Scan(&p.ID, &p.Name, &p.Emoji, &p.Description, &p.URL, &p.StartDate, &p.EndDate, &p.Status, &p.TeamLead)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// GetPeopleOnProject scans planning data to find people assigned to a project.
func (s *Store) GetPeopleOnProject(projectName string) ([]string, error) {
	// This is a scan operation — search planning entries for projects JSON containing the name
	rows, err := s.db.Query(`SELECT DISTINCT person_id FROM planning WHERE projects LIKE ?`, `%"`+projectName+`%"`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var personIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		personIDs = append(personIDs, id)
	}
	return personIDs, nil
}

// ===== On-Call =====

func (s *Store) GetOnCall(startDate, endDate string) (map[string][]string, error) {
	// Returns map of week_start → []person_id
	rows, err := s.db.Query(`SELECT person_id, week_start FROM oncall WHERE week_start >= ? AND week_start <= ?`, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string][]string)
	for rows.Next() {
		var personID, weekStart string
		if err := rows.Scan(&personID, &weekStart); err != nil {
			return nil, err
		}
		result[weekStart] = append(result[weekStart], personID)
	}
	return result, nil
}

// ===== Rotation =====

func (s *Store) GetRotation(startDate, endDate string) (map[string][]string, error) {
	rows, err := s.db.Query(`SELECT person_id, week_start FROM rotation WHERE week_start >= ? AND week_start <= ?`, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string][]string)
	for rows.Next() {
		var personID, weekStart string
		if err := rows.Scan(&personID, &weekStart); err != nil {
			return nil, err
		}
		result[weekStart] = append(result[weekStart], personID)
	}
	return result, nil
}

// ===== Holidays =====

func (s *Store) ListHolidays() ([]model.Holiday, error) {
	rows, err := s.db.Query(`SELECT date, label, country FROM holidays ORDER BY date`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var holidays []model.Holiday
	for rows.Next() {
		var h model.Holiday
		if err := rows.Scan(&h.Date, &h.Label, &h.Country); err != nil {
			return nil, err
		}
		holidays = append(holidays, h)
	}
	return holidays, nil
}

// ===== Export data (for TOML) =====

type ExportData struct {
	SchemaVersion int    `toml:"schema_version"`
	ExportedAt    string `toml:"exported_at"`
	// Settings is map[string]any (not map[string]string) so legacy TOML
	// exports — which carry integer settings such as window_weeks = 4 —
	// decode instead of failing with "cannot decode TOML integer into
	// string". Values are coerced back to strings on import, since the
	// settings table stores everything as text.
	Settings map[string]any   `toml:"settings"`
	People   []ExportPerson   `toml:"people"`
	Planning []ExportPlanning `toml:"planning"`
	Projects []model.Project  `toml:"projects"`
	OnCall   []ExportKeyVal   `toml:"oncall"`
	Rotation []ExportKeyVal   `toml:"rotation"`
}

type ExportPerson struct {
	ID              string   `toml:"id"`
	Name            string   `toml:"name"`
	Role            string   `toml:"role"`
	SubTeam         string   `toml:"sub_team"`
	AvatarEmoji     string   `toml:"avatar_emoji"`
	AvatarColor     string   `toml:"avatar_color"`
	StartDate       string   `toml:"start_date"`
	DefaultProjects []string `toml:"default_projects"`
	Status          string   `toml:"status"`
	ArchivedDate    string   `toml:"archived_date"`
	IsGuest         bool     `toml:"is_guest"`

	// Legacy-compat: older exports used a nested [avatar] table and the key
	// `guest` (instead of `is_guest`). Pointers + omitempty keep new exports
	// clean (nil is omitted) while still decoding legacy files.
	Avatar *legacyAvatar `toml:"avatar,omitempty"`
	Guest  *bool         `toml:"guest,omitempty"`
}

// legacyAvatar mirrors the nested `avatar = { emoji, color }` table used by
// the legacy export format.
type legacyAvatar struct {
	Emoji string `toml:"emoji"`
	Color string `toml:"color"`
}

type ExportPlanning struct {
	PersonID     string                `toml:"person_id"`
	Date         string                `toml:"date"`
	Slot         string                `toml:"slot"`
	State        string                `toml:"state"`
	AwayType     string                `toml:"away_type"`
	AwayNote     string                `toml:"away_note"`
	Run          bool                  `toml:"run"`
	Remote       bool                  `toml:"remote"`
	Offsite      bool                  `toml:"offsite"`
	IncidentText string                `toml:"incident_text"`
	Projects     []model.ProjectAssign `toml:"projects"`
}

type ExportKeyVal struct {
	PersonID  string `toml:"person_id"`
	WeekStart string `toml:"week_start"`
}

// GetExportData gathers all data for TOML export.
func (s *Store) GetExportData() (*ExportData, error) {
	// Settings
	settingsRows, err := s.db.Query("SELECT key, value FROM settings")
	if err != nil {
		return nil, fmt.Errorf("export settings: %w", err)
	}
	defer settingsRows.Close()
	settings := make(map[string]any)
	for settingsRows.Next() {
		var k, v string
		settingsRows.Scan(&k, &v)
		settings[k] = v
	}

	// People
	people, err := s.ListPeople()
	if err != nil {
		return nil, fmt.Errorf("export people: %w", err)
	}
	var exportPeople []ExportPerson
	for _, p := range people {
		exportPeople = append(exportPeople, ExportPerson{
			ID: p.ID, Name: p.Name, Role: p.Role, SubTeam: p.SubTeam,
			AvatarEmoji: p.AvatarEmoji, AvatarColor: p.AvatarColor,
			StartDate: p.StartDate, DefaultProjects: p.DefaultProjects,
			Status: p.Status, ArchivedDate: p.ArchivedDate, IsGuest: p.IsGuest,
		})
	}

	// Planning
	entries, err := s.GetPlanning("0000/01/01", "9999/12/31")
	if err != nil {
		return nil, fmt.Errorf("export planning: %w", err)
	}
	var exportPlanning []ExportPlanning
	for _, e := range entries {
		awayType, awayNote, incidentText := "", "", ""
		if e.Data.Away != nil {
			awayType = e.Data.Away.Type
			awayNote = e.Data.Away.Note
		}
		if e.Data.Incident != nil {
			incidentText = e.Data.Incident.Text
		}
		projs := e.Data.Projects
		if projs == nil {
			projs = []model.ProjectAssign{}
		}
		exportPlanning = append(exportPlanning, ExportPlanning{
			PersonID: e.PersonID, Date: e.Date, Slot: e.Slot,
			State: e.Data.State, AwayType: awayType, AwayNote: awayNote,
			Run: e.Data.Run, Remote: e.Data.Remote, Offsite: e.Data.Offsite,
			IncidentText: incidentText, Projects: projs,
		})
	}

	// Projects
	projects, err := s.ListProjects()
	if err != nil {
		return nil, fmt.Errorf("export projects: %w", err)
	}

	// On-call
	oncallMap, err := s.GetOnCall("0000/01/01", "9999/12/31")
	if err != nil {
		return nil, fmt.Errorf("export oncall: %w", err)
	}
	var exportOnCall []ExportKeyVal
	for weekStart, pids := range oncallMap {
		for _, pid := range pids {
			exportOnCall = append(exportOnCall, ExportKeyVal{PersonID: pid, WeekStart: weekStart})
		}
	}

	// Rotation
	rotMap, err := s.GetRotation("0000/01/01", "9999/12/31")
	if err != nil {
		return nil, fmt.Errorf("export rotation: %w", err)
	}
	var exportRot []ExportKeyVal
	for weekStart, pids := range rotMap {
		for _, pid := range pids {
			exportRot = append(exportRot, ExportKeyVal{PersonID: pid, WeekStart: weekStart})
		}
	}

	return &ExportData{
		SchemaVersion: 1,
		ExportedAt:    "now", // will be replaced by caller
		Settings:      settings,
		People:        exportPeople,
		Planning:      exportPlanning,
		Projects:      projects,
		OnCall:        exportOnCall,
		Rotation:      exportRot,
	}, nil
}

// IncidentEntry is a single incident occurrence with person details.
// Used by the Incidents view to list all incidents ordered by time.
type IncidentEntry struct {
	PersonID     string `json:"person_id"`
	PersonName   string `json:"person_name"`
	AvatarEmoji  string `json:"avatar_emoji"`
	Date         string `json:"date"`
	Slot         string `json:"slot"`
	IncidentText string `json:"incident_text"`
}

// ListIncidents returns all planning entries with a non-empty incident_text,
// joined with person details, ordered by date DESC then slot DESC (most recent first).
func (s *Store) ListIncidents() ([]IncidentEntry, error) {
	rows, err := s.db.Query(`
		SELECT p.person_id, pe.name, pe.avatar_emoji, p.date, p.slot, p.incident_text
		FROM planning p
		JOIN people pe ON p.person_id = pe.id
		WHERE p.incident_text != ''
		ORDER BY p.date DESC, p.slot DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var incidents []IncidentEntry
	for rows.Next() {
		var inc IncidentEntry
		if err := rows.Scan(&inc.PersonID, &inc.PersonName, &inc.AvatarEmoji, &inc.Date, &inc.Slot, &inc.IncidentText); err != nil {
			return nil, err
		}
		incidents = append(incidents, inc)
	}
	return incidents, nil
}
