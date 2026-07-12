package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/teamviz/team-visualizer/internal/model"
)

// ===== People (write) =====

func (s *Store) AddPerson(p model.Person) (model.Person, error) {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	if p.AvatarEmoji == "" {
		p.AvatarEmoji = "👤"
	}
	if p.AvatarColor == "" {
		p.AvatarColor = "#4361ee"
	}
	if p.Status == "" {
		p.Status = "active"
	}
	projectsJSON, _ := json.Marshal(p.DefaultProjects)
	if string(projectsJSON) == "" || string(projectsJSON) == "null" {
		projectsJSON = []byte("[]")
	}
	isGuest := 0
	if p.IsGuest {
		isGuest = 1
	}
	_, err := s.db.Exec(`INSERT INTO people (id, name, role, sub_team, avatar_emoji, avatar_color, start_date, default_projects, status, archived_date, is_guest, ics_token) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Name, p.Role, p.SubTeam, p.AvatarEmoji, p.AvatarColor, p.StartDate, string(projectsJSON), p.Status, p.ArchivedDate, isGuest, "")
	if err != nil {
		return p, err
	}
	return p, nil
}

func (s *Store) UpdatePerson(id string, p model.Person) error {
	projectsJSON, _ := json.Marshal(p.DefaultProjects)
	if string(projectsJSON) == "" || string(projectsJSON) == "null" {
		projectsJSON = []byte("[]")
	}
	isGuest := 0
	if p.IsGuest {
		isGuest = 1
	}
	_, err := s.db.Exec(`UPDATE people SET name=?, role=?, sub_team=?, avatar_emoji=?, avatar_color=?, start_date=?, default_projects=?, status=?, archived_date=?, is_guest=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		p.Name, p.Role, p.SubTeam, p.AvatarEmoji, p.AvatarColor, p.StartDate, string(projectsJSON), p.Status, p.ArchivedDate, isGuest, id)
	return err
}

func (s *Store) DeletePerson(id string) error {
	_, err := s.db.Exec("DELETE FROM people WHERE id=?", id)
	return err
}

func (s *Store) ArchivePerson(id string) error {
	_, err := s.db.Exec("UPDATE people SET status='archived', archived_date=?, updated_at=CURRENT_TIMESTAMP WHERE id=?", time.Now().UTC().Format("2006/01/02"), id)
	return err
}

func (s *Store) UnarchivePerson(id string) error {
	_, err := s.db.Exec("UPDATE people SET status='active', archived_date='', updated_at=CURRENT_TIMESTAMP WHERE id=?", id)
	return err
}

// ===== Planning (write) =====

func (s *Store) SetSlot(personID, date, slot string, data model.SlotData) error {
	// If state is not_filled or data is empty, delete the entry
	if data.State == "not_filled" || (data.State == "" && data.Away == nil && len(data.Projects) == 0 && !data.Run && !data.Remote) {
		return s.ClearSlot(personID, date, slot)
	}

	awayType, awayNote := "", ""
	if data.Away != nil {
		awayType = data.Away.Type
		awayNote = data.Away.Note
	}
	projectsJSON, _ := json.Marshal(data.Projects)
	if string(projectsJSON) == "" || string(projectsJSON) == "null" {
		projectsJSON = []byte("[]")
	}
	runVal := 0
	if data.Run {
		runVal = 1
	}
	remoteVal := 0
	if data.Remote {
		remoteVal = 1
	}

	_, err := s.db.Exec(`INSERT OR REPLACE INTO planning (person_id, date, slot, state, away_type, away_note, run, projects, remote) VALUES (?,?,?,?,?,?,?,?,?)`,
		personID, date, slot, data.State, awayType, awayNote, runVal, string(projectsJSON), remoteVal)
	return err
}

func (s *Store) ClearSlot(personID, date, slot string) error {
	_, err := s.db.Exec("DELETE FROM planning WHERE person_id=? AND date=? AND slot=?", personID, date, slot)
	return err
}

// SlotRef identifies a half-day slot for a person.
type SlotRef struct {
	PersonID string `json:"person_id"`
	Date     string `json:"date"`
	Slot     string `json:"slot"`
}

// SetSlotRange applies the same data to multiple slots across multiple people.
func (s *Store) SetSlotRange(refs []SlotRef, data model.SlotData) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if data.State == "not_filled" || (data.State == "" && data.Away == nil && len(data.Projects) == 0 && !data.Run && !data.Remote) {
			_, err = tx.Exec("DELETE FROM planning WHERE person_id=? AND date=? AND slot=?", ref.PersonID, ref.Date, ref.Slot)
		} else {
			awayType, awayNote := "", ""
			if data.Away != nil {
				awayType = data.Away.Type
				awayNote = data.Away.Note
			}
			projectsJSON, _ := json.Marshal(data.Projects)
			if string(projectsJSON) == "" || string(projectsJSON) == "null" {
				projectsJSON = []byte("[]")
			}
			runVal := 0
			if data.Run {
				runVal = 1
			}
			remoteVal := 0
			if data.Remote {
				remoteVal = 1
			}
			_, err = tx.Exec(`INSERT OR REPLACE INTO planning (person_id, date, slot, state, away_type, away_note, run, projects, remote) VALUES (?,?,?,?,?,?,?,?,?)`,
				ref.PersonID, ref.Date, ref.Slot, data.State, awayType, awayNote, runVal, string(projectsJSON), remoteVal)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// ClearSlotRange deletes multiple slots.
func (s *Store) ClearSlotRange(refs []SlotRef) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if _, err = tx.Exec("DELETE FROM planning WHERE person_id=? AND date=? AND slot=?", ref.PersonID, ref.Date, ref.Slot); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// CopyWeek copies planning entries from one week to another for a person.
// Away entries are skipped. Existing entries at the target are not overwritten.
func (s *Store) CopyWeek(personID, fromWeekStart, toWeekStart string) (int, error) {
	// Parse dates
	fromStart, err := parseDateSimple(fromWeekStart)
	if err != nil {
		return 0, fmt.Errorf("invalid from_week_start: %w", err)
	}
	toStart, err := parseDateSimple(toWeekStart)
	if err != nil {
		return 0, fmt.Errorf("invalid to_week_start: %w", err)
	}

	copied := 0
	for dayOffset := 0; dayOffset < 7; dayOffset++ {
		isWeekend := dayOffset >= 5
		if isWeekend {
			continue // skip weekends
		}
		fromDate := addDays(fromStart, dayOffset)
		toDate := addDays(toStart, dayOffset)
		fromDateStr := formatDateSimple(fromDate)
		toDateStr := formatDateSimple(toDate)

		for _, slot := range []string{"am", "pm"} {
			// Read source entry
			var state, awayType, awayNote, projectsJSON string
			var run int
			err := s.db.QueryRow("SELECT state, away_type, away_note, run, projects FROM planning WHERE person_id=? AND date=? AND slot=?",
				personID, fromDateStr, slot).Scan(&state, &awayType, &awayNote, &run, &projectsJSON)
			if err == sql.ErrNoRows {
				continue
			}
			if err != nil {
				return copied, err
			}
			// Skip away entries
			if awayType != "" {
				continue
			}
			// Check if target already has data
			var existing int
			s.db.QueryRow("SELECT COUNT(*) FROM planning WHERE person_id=? AND date=? AND slot=?", personID, toDateStr, slot).Scan(&existing)
			if existing > 0 {
				continue // don't overwrite
			}
			// Insert at target
			_, err = s.db.Exec("INSERT INTO planning (person_id, date, slot, state, away_type, away_note, run, projects) VALUES (?,?,?,?,?,?,?,?)",
				personID, toDateStr, slot, state, "", "", run, projectsJSON)
			if err != nil {
				return copied, err
			}
			copied++
		}
	}
	return copied, nil
}

// PruneOldData deletes planning entries older than the given number of weeks.
func (s *Store) PruneOldData(weeksOld int) (int, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -weeksOld*7)
	cutoffStr := cutoff.Format("2006/01/02")
	res, err := s.db.Exec("DELETE FROM planning WHERE date < ?", cutoffStr)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ResetAllData clears all planning, people, projects, oncall, rotation (keeps settings + users).
func (s *Store) ResetAllData() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	tables := []string{"planning", "oncall", "rotation", "people", "projects"}
	for _, t := range tables {
		if _, err := tx.Exec("DELETE FROM " + t); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// ===== Projects (write) =====

func (s *Store) AddProject(p model.Project) (model.Project, error) {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	if p.Emoji == "" {
		p.Emoji = "📁"
	}
	if p.Status == "" {
		p.Status = "unstarted"
	}
	_, err := s.db.Exec(`INSERT INTO projects (id, name, emoji, description, url, start_date, end_date, status, team_lead) VALUES (?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Name, p.Emoji, p.Description, p.URL, p.StartDate, p.EndDate, p.Status, p.TeamLead)
	if err != nil {
		return p, err
	}
	return p, nil
}

func (s *Store) UpdateProject(id string, p model.Project) error {
	_, err := s.db.Exec(`UPDATE projects SET name=?, emoji=?, description=?, url=?, start_date=?, end_date=?, status=?, team_lead=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		p.Name, p.Emoji, p.Description, p.URL, p.StartDate, p.EndDate, p.Status, p.TeamLead, id)
	return err
}

func (s *Store) DeleteProject(id string) error {
	_, err := s.db.Exec("DELETE FROM projects WHERE id=?", id)
	return err
}

func (s *Store) GetProjectByName(name string) (*model.Project, error) {
	var p model.Project
	err := s.db.QueryRow(`SELECT id, name, emoji, description, url, start_date, end_date, status FROM projects WHERE name = ?`, name).
		Scan(&p.ID, &p.Name, &p.Emoji, &p.Description, &p.URL, &p.StartDate, &p.EndDate, &p.Status)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// UpsertProjectByName creates or updates a project by name (for CSV/TOML import).
func (s *Store) UpsertProjectByName(p model.Project) (bool, error) {
	existing, err := s.GetProjectByName(p.Name)
	if err != nil {
		return false, err
	}
	if existing != nil {
		err = s.UpdateProject(existing.ID, p)
		return false, err // false = updated
	}
	_, err = s.AddProject(p)
	return true, err // true = created
}

// ===== On-Call (write) =====

func (s *Store) SetOnCall(personID, weekStart string, isOnCall bool) error {
	if isOnCall {
		_, err := s.db.Exec("INSERT OR REPLACE INTO oncall (person_id, week_start) VALUES (?, ?)", personID, weekStart)
		return err
	}
	_, err := s.db.Exec("DELETE FROM oncall WHERE person_id=? AND week_start=?", personID, weekStart)
	return err
}

// ===== Rotation (write) =====

func (s *Store) SetRotation(personID, weekStart string, isRunPerson bool) error {
	if isRunPerson {
		_, err := s.db.Exec("INSERT OR REPLACE INTO rotation (person_id, week_start) VALUES (?, ?)", personID, weekStart)
		return err
	}
	_, err := s.db.Exec("DELETE FROM rotation WHERE person_id=? AND week_start=?", personID, weekStart)
	return err
}

// AssignRunPerson sets the rotation flag AND fills the week's schedule with RUN duty.
// Returns the count of overwritten slots.
func (s *Store) AssignRunPerson(personID, weekStart string) (int, error) {
	// Count existing filled slots
	weekStartParsed, err := parseDateSimple(weekStart)
	if err != nil {
		return 0, err
	}

	var filledCount int
	for dayOffset := 0; dayOffset < 7; dayOffset++ {
		if dayOffset >= 5 {
			continue // skip weekends
		}
		dateStr := formatDateSimple(addDays(weekStartParsed, dayOffset))
		for _, slot := range []string{"am", "pm"} {
			var state string
			err := s.db.QueryRow("SELECT state FROM planning WHERE person_id=? AND date=? AND slot=?", personID, dateStr, slot).Scan(&state)
			if err == nil && state != "not_filled" {
				filledCount++
			}
		}
	}

	// Set rotation flag
	if err := s.SetRotation(personID, weekStart, true); err != nil {
		return 0, err
	}

	// Fill all working half-days with RUN
	for dayOffset := 0; dayOffset < 7; dayOffset++ {
		if dayOffset >= 5 {
			continue
		}
		dateStr := formatDateSimple(addDays(weekStartParsed, dayOffset))
		for _, slot := range []string{"am", "pm"} {
			if err := s.SetSlot(personID, dateStr, slot, model.SlotData{
				State:    "filled",
				Away:     nil,
				Projects: []model.ProjectAssign{},
				Run:      true,
			}); err != nil {
				return filledCount, err
			}
		}
	}

	return filledCount, nil
}

// ===== Holidays (write) =====

func (s *Store) ImportHolidays(holidays []model.Holiday) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, h := range holidays {
		_, err := tx.Exec("INSERT OR REPLACE INTO holidays (date, label, country) VALUES (?, ?, ?)", h.Date, h.Label, h.Country)
		if err != nil {
			tx.Rollback()
			return count, err
		}
		count++
	}
	err = tx.Commit()
	if err != nil {
		return 0, err
	}
	return count, nil
}

// ===== TOML Import =====

func (s *Store) ImportTOMLData(data *ExportData, mode string) (created, updated int, err error) {
	if mode == "replace" {
		if e := s.ResetAllData(); e != nil {
			return 0, 0, e
		}
	}

	// Import settings (values may be int/bool/string in legacy exports)
	for k, v := range data.Settings {
		if k == "export_counter" || k == "_scrollOffset" {
			continue
		}
		if e := s.SetSetting(k, fmt.Sprint(v)); e != nil {
			return created, updated, fmt.Errorf("import setting %s: %w", k, e)
		}
	}

	// Import people
	for _, ep := range data.People {
		avatarEmoji, avatarColor := ep.AvatarEmoji, ep.AvatarColor
		if ep.Avatar != nil {
			if avatarEmoji == "" {
				avatarEmoji = ep.Avatar.Emoji
			}
			if avatarColor == "" {
				avatarColor = ep.Avatar.Color
			}
		}
		isGuest := ep.IsGuest
		if ep.Guest != nil {
			isGuest = *ep.Guest
		}
		p := model.Person{
			ID: ep.ID, Name: ep.Name, Role: ep.Role, SubTeam: ep.SubTeam,
			AvatarEmoji: avatarEmoji, AvatarColor: avatarColor,
			StartDate: ep.StartDate, DefaultProjects: ep.DefaultProjects,
			Status: ep.Status, ArchivedDate: ep.ArchivedDate, IsGuest: isGuest,
		}
		existing, e := s.GetPerson(ep.ID)
		if e != nil {
			return created, updated, fmt.Errorf("import get person %s: %w", ep.ID, e)
		}
		if existing != nil {
			if e := s.UpdatePerson(ep.ID, p); e != nil {
				return created, updated, fmt.Errorf("import update person %s: %w", ep.ID, e)
			}
			updated++
		} else {
			if p.ID == "" {
				p.ID = uuid.New().String()
			}
			if _, e := s.AddPerson(p); e != nil {
				return created, updated, fmt.Errorf("import add person %s: %w", p.ID, e)
			}
			created++
		}
	}

	// Import planning
	for _, ep := range data.Planning {
		away := (*model.AwayData)(nil)
		if ep.AwayType != "" {
			away = &model.AwayData{Type: ep.AwayType, Note: ep.AwayNote}
		}
		projs := ep.Projects
		if projs == nil {
			projs = []model.ProjectAssign{}
		}
		if e := s.SetSlot(ep.PersonID, ep.Date, ep.Slot, model.SlotData{
			State: ep.State, Away: away, Projects: projs, Run: ep.Run, Remote: ep.Remote,
		}); e != nil {
			return created, updated, fmt.Errorf("import planning %s %s %s: %w", ep.PersonID, ep.Date, ep.Slot, e)
		}
	}

	// Import projects
	for _, proj := range data.Projects {
		if _, e := s.UpsertProjectByName(proj); e != nil {
			return created, updated, fmt.Errorf("import project %s: %w", proj.Name, e)
		}
	}

	// Import on-call
	for _, oc := range data.OnCall {
		if e := s.SetOnCall(oc.PersonID, oc.WeekStart, true); e != nil {
			return created, updated, fmt.Errorf("import oncall %s %s: %w", oc.PersonID, oc.WeekStart, e)
		}
	}

	// Import rotation
	for _, rot := range data.Rotation {
		if e := s.SetRotation(rot.PersonID, rot.WeekStart, true); e != nil {
			return created, updated, fmt.Errorf("import rotation %s %s: %w", rot.PersonID, rot.WeekStart, e)
		}
	}

	return created, updated, nil
}

// ===== Date helpers =====

func parseDateSimple(s string) (time.Time, error) {
	return time.Parse("2006/01/02", s)
}

func formatDateSimple(t time.Time) string {
	return t.Format("2006/01/02")
}

func addDays(t time.Time, days int) time.Time {
	return t.AddDate(0, 0, days)
}
