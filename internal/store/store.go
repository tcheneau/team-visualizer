package store

import (
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
	"time"

	"github.com/teamviz/team-visualizer/internal/model"
)

//go:embed migrations/001_init.sql
var migration001 string

//go:embed migrations/002_projects.sql
var migration002 string

//go:embed migrations/003_features.sql
var migration003 string

//go:embed migrations/004_remote.sql
var migration004 string

//go:embed migrations/005_team_lead.sql
var migration005 string

type Store struct {
	db *sql.DB
}

func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Enable foreign keys and WAL mode for better concurrency
	if _, err := db.Exec("PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set pragmas: %w", err)
	}

	// Connection pool settings for SQLite
	db.SetMaxOpenConns(1) // SQLite doesn't handle concurrent writes well
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	migrations := []string{migration001, migration002, migration003, migration004, migration005}
	for i, m := range migrations {
		// Split into individual statements and run each one. This makes
		// ALTER TABLE / CREATE TABLE migrations idempotent: if a column or
		// table already exists (e.g. from a previous run), the error is
		// ignored instead of aborting the whole migration.
		statements := strings.Split(m, ";")
		for _, stmt := range statements {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if _, err := s.db.Exec(stmt); err != nil {
				msg := err.Error()
				if strings.Contains(msg, "duplicate column name") ||
					strings.Contains(msg, "already exists") {
					continue // already applied — idempotent
				}
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
		}
	}
	return nil
}

// ===== Settings =====

func (s *Store) GetSettings() (model.Settings, error) {
	rows, err := s.db.Query("SELECT key, value FROM settings")
	if err != nil {
		return model.Settings{}, err
	}
	defer rows.Close()

	m := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return model.Settings{}, err
		}
		m[k] = v
	}

	return settingsFromMap(m), nil
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", key, value)
	return err
}

// ===== Users =====

func (s *Store) UpsertUser(username string, role model.Role) (*model.User, error) {
	now := time.Now().UTC()

	// Try to find existing user
	var u model.User
	err := s.db.QueryRow("SELECT id, username, role, created_at, last_seen FROM users WHERE username = ?", username).
		Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt, &u.LastSeen)

	if err == sql.ErrNoRows {
		// Create new user
		_, err = s.db.Exec(
			"INSERT INTO users (username, role, created_at, last_seen) VALUES (?, ?, ?, ?)",
			username, string(role), now, now,
		)
		if err != nil {
			return nil, err
		}
		// Fetch back
		err = s.db.QueryRow("SELECT id, username, role, created_at, last_seen FROM users WHERE username = ?", username).
			Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt, &u.LastSeen)
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else {
		// Update last_seen and role (role may change if proxy groups change)
		_, err = s.db.Exec("UPDATE users SET last_seen = ?, role = ? WHERE id = ?", now, string(role), u.ID)
		if err != nil {
			return nil, err
		}
		u.Role = role
		u.LastSeen = now
	}

	return &u, nil
}

func (s *Store) GetUser(username string) (*model.User, error) {
	var u model.User
	err := s.db.QueryRow("SELECT id, username, role, created_at, last_seen FROM users WHERE username = ?", username).
		Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt, &u.LastSeen)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// ===== Helpers =====

func settingsFromMap(m map[string]string) model.Settings {
	return model.Settings{
		WindowWeeks:      atoiOr(m["window_weeks"], 4),
		PruneWeeks:       atoiOr(m["prune_weeks"], 12),
		WeekStarts:       orDefault(m["week_starts"], "monday"),
		RunMode:          orDefault(m["run_mode"], "ratio"),
		RunTargetPersons: atoiOr(m["run_target_persons"], 3),
		Theme:            orDefault(m["theme"], "dracula"),
		ExportCounter:    atoiOr(m["export_counter"], 1),
		HolidayCountry:   orDefault(m["holiday_country"], ""),
	}
}

func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	var n int
	fmt.Sscanf(s, "%d", &n)
	if n == 0 {
		return def
	}
	return n
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// ===== Activity / Audit Log =====

type ActivityEvent struct {
	Id     int    `json:"id"`
	Ts     string `json:"ts"`
	Actor  string `json:"actor"`
	Action string `json:"action"`
	Target string `json:"target"`
	Detail string `json:"detail"`
}

func (s *Store) RecordEvent(actor, action, target, detail string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec("INSERT INTO audit_log (ts, actor, action, target, detail) VALUES (?, ?, ?, ?, ?)",
		now, actor, action, target, detail)
	return err
}

func (s *Store) ListEvents(limit int) ([]ActivityEvent, error) {
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.db.Query("SELECT id, ts, actor, action, target, detail FROM audit_log ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []ActivityEvent
	for rows.Next() {
		var e ActivityEvent
		if err := rows.Scan(&e.Id, &e.Ts, &e.Actor, &e.Action, &e.Target, &e.Detail); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, nil
}

// ===== User-Person Mapping =====

func (s *Store) GetUserPerson(username string) (string, error) {
	var personID string
	err := s.db.QueryRow("SELECT selected_person_id FROM users WHERE username = ?", username).Scan(&personID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return personID, nil
}

func (s *Store) SetUserPerson(username, personID string) error {
	_, err := s.db.Exec("UPDATE users SET selected_person_id = ? WHERE username = ?", personID, username)
	return err
}

func (s *Store) SetUserPersonByID(userID int64, personID string) error {
	_, err := s.db.Exec("UPDATE users SET selected_person_id = ? WHERE id = ?", personID, userID)
	return err
}

type UserRow struct {
	Id        int64  `json:"id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
	LastSeen  string `json:"last_seen"`
	PersonID  string `json:"person_id"`
}

func (s *Store) ListUsers() ([]UserRow, error) {
	rows, err := s.db.Query("SELECT id, username, role, created_at, last_seen, selected_person_id FROM users ORDER BY last_seen DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []UserRow
	for rows.Next() {
		var u UserRow
		if err := rows.Scan(&u.Id, &u.Username, &u.Role, &u.CreatedAt, &u.LastSeen, &u.PersonID); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, nil
}

// ===== ICS Token =====

func (s *Store) GetPersonByICSToken(token string) (*model.Person, error) {
	row := s.db.QueryRow(`SELECT id, name, role, sub_team, avatar_emoji, avatar_color, start_date, default_projects, status, archived_date, is_guest, ics_token FROM people WHERE ics_token = ?`, token)
	p, err := scanPerson(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) SetPersonICSToken(personID, token string) error {
	_, err := s.db.Exec("UPDATE people SET ics_token = ? WHERE id = ?", token, personID)
	return err
}

func (s *Store) ClearPersonICSToken(personID string) error {
	_, err := s.db.Exec("UPDATE people SET ics_token = '' WHERE id = ?", personID)
	return err
}
