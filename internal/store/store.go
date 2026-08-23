package store

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
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

//go:embed migrations/006_offsite_incident.sql
var migration006 string

//go:embed migrations/007_audit_meta.sql
var migration007 string

//go:embed migrations/008_is_incident.sql
var migration008 string

//go:embed migrations/009_run_note.sql
var migration009 string

//go:embed migrations/010_oncall_comment.sql
var migration010 string

//go:embed migrations/011_tentative_away.sql
var migration011 string

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
	migrations := []string{migration001, migration002, migration003, migration004, migration005, migration006, migration007, migration008, migration009, migration010, migration011}
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
	if err := s.backfillAuditMeta(); err != nil {
		return fmt.Errorf("backfill audit meta: %w", err)
	}
	return nil
}

// ===== Activity / Audit Log =====

type ActivityEvent struct {
	Id     int             `json:"id"`
	Ts     string          `json:"ts"`
	Actor  string          `json:"actor"`
	Action string          `json:"action"`
	Target string          `json:"target"`
	Detail string          `json:"detail"`
	Meta   json.RawMessage `json:"meta"`
}

func (s *Store) RecordEvent(actor, action, target, detail, metaJSON string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if metaJSON == "" {
		metaJSON = "{}"
	}
	_, err := s.db.Exec("INSERT INTO audit_log (ts, actor, action, target, detail, meta) VALUES (?, ?, ?, ?, ?, ?)",
		now, actor, action, target, detail, metaJSON)
	return err
}

func (s *Store) ListEvents(limit int) ([]ActivityEvent, error) {
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.db.Query("SELECT id, ts, actor, action, target, detail, meta FROM audit_log ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []ActivityEvent
	for rows.Next() {
		var e ActivityEvent
		var metaStr string
		if err := rows.Scan(&e.Id, &e.Ts, &e.Actor, &e.Action, &e.Target, &e.Detail, &metaStr); err != nil {
			return nil, err
		}
		if metaStr == "" {
			e.Meta = json.RawMessage("{}")
		} else {
			e.Meta = json.RawMessage(metaStr)
		}
		events = append(events, e)
	}
	return events, nil
}

// backfillAuditMeta parses legacy target/detail strings of existing audit_log
// rows into the structured meta JSON column. It is best-effort: rows it cannot
// parse are left with an empty meta and fall back to the text fields on the
// frontend. New rows are always recorded with rich meta.
func (s *Store) backfillAuditMeta() error {
	rows, err := s.db.Query("SELECT id, action, target, detail FROM audit_log WHERE meta = '' OR meta IS NULL")
	if err != nil {
		return err
	}
	defer rows.Close()

	type pending struct {
		id   int
		meta map[string]any
	}
	var updates []pending
	for rows.Next() {
		var id int
		var action, target, detail string
		if err := rows.Scan(&id, &action, &target, &detail); err != nil {
			return err
		}
		m := parseLegacyAuditMeta(action, target, detail)
		if m != nil {
			updates = append(updates, pending{id: id, meta: m})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, u := range updates {
		b, err := json.Marshal(u.meta)
		if err != nil {
			continue
		}
		if _, err := s.db.Exec("UPDATE audit_log SET meta = ? WHERE id = ?", string(b), u.id); err != nil {
			return err
		}
	}
	return nil
}

// parseLegacyAuditMeta reconstructs a structured meta map from the legacy
// target/detail text strings for a given action. Returns nil when nothing
// useful can be recovered.
func parseLegacyAuditMeta(action, target, detail string) map[string]any {
	switch action {
	case "planning_set", "planning_clear":
		// target = "PID DATE SLOT"
		parts := strings.SplitN(target, " ", 3)
		if len(parts) < 1 {
			return nil
		}
		m := map[string]any{"person_ids": []string{parts[0]}}
		if len(parts) >= 2 {
			m["date"] = parts[1]
		}
		if len(parts) >= 3 {
			m["slot"] = parts[2]
		}
		if detail != "" {
			applyDetailMeta(m, detail)
		}
		return m
	case "planning_range", "planning_range_clear":
		// target = "STARTDATE-STARTSLOT ENDDATE-ENDSLOT"
		m := map[string]any{}
		rngParts := strings.SplitN(target, " ", 2)
		if len(rngParts) == 2 {
			s := strings.SplitN(rngParts[0], "-", 2)
			e := strings.SplitN(rngParts[1], "-", 2)
			if len(s) >= 1 {
				m["start_date"] = s[0]
			}
			if len(s) == 2 {
				m["start_slot"] = s[1]
			}
			if len(e) >= 1 {
				m["end_date"] = e[0]
			}
			if len(e) == 2 {
				m["end_slot"] = e[1]
			}
		}
		if n := extractFirstInt(detail); n >= 0 {
			m["people_count"] = n
		}
		return m
	case "planning_copy":
		// target = "FROM -> TO", detail = "person PID"
		m := map[string]any{}
		if pid := strings.TrimSpace(strings.TrimPrefix(detail, "person")); pid != "" {
			m["person_ids"] = []string{pid}
		}
		rngParts := strings.SplitN(target, " -> ", 2)
		if len(rngParts) == 2 {
			m["from_week"] = rngParts[0]
			m["to_week"] = rngParts[1]
		}
		return m
	case "oncall_set", "oncall_remove":
		// target = "PID WEEKSTART"
		parts := strings.SplitN(target, " ", 2)
		if len(parts) < 1 {
			return nil
		}
		m := map[string]any{"person_ids": []string{parts[0]}}
		if len(parts) == 2 {
			m["week_start"] = parts[1]
		}
		return m
	case "person_delete", "person_archive", "person_unarchive":
		if target == "" {
			return nil
		}
		return map[string]any{"person_ids": []string{target}}
	case "person_add", "person_update":
		if target == "" {
			return nil
		}
		return map[string]any{"person_name": target}
	case "project_add", "project_update":
		if target == "" {
			return nil
		}
		return map[string]any{"project_name": target}
	case "project_delete":
		if target == "" {
			return nil
		}
		return map[string]any{"project_id": target}
	case "prune":
		m := map[string]any{}
		if n := extractFirstInt(target); n >= 0 {
			m["weeks_old"] = n
		}
		if n := extractFirstInt(detail); n >= 0 {
			m["deleted"] = n
		}
		return m
	case "project_import_csv":
		m := map[string]any{}
		if c := strings.Index(detail, "created"); c >= 0 {
			if n := extractFirstInt(detail[c:]); n >= 0 {
				m["created"] = n
			}
		}
		if u := strings.Index(detail, "updated"); u >= 0 {
			if n := extractFirstInt(detail[u:]); n >= 0 {
				m["updated"] = n
			}
		}
		return m
	}
	return nil
}

// applyDetailMeta interprets a planning_set detail string into state fields.
func applyDetailMeta(m map[string]any, detail string) {
	switch {
	case strings.HasPrefix(detail, "away:"):
		m["state"] = "away"
		m["away_type"] = strings.TrimPrefix(detail, "away:")
	case strings.HasPrefix(detail, "incident:"):
		m["state"] = "incident"
		m["incident_text"] = strings.TrimPrefix(detail, "incident:")
	case detail == "run":
		m["state"] = "run"
	case strings.Contains(detail, "+run"):
		m["state"] = "project"
		m["projects"] = strings.Split(strings.TrimSuffix(detail, "+run"), ",")
		m["run"] = true
	case detail != "":
		m["state"] = "project"
		m["projects"] = strings.Split(detail, ",")
	}
}

// extractFirstInt returns the first run of decimal digits found anywhere in
// the string as a non-negative integer, or -1 if none is found.
func extractFirstInt(s string) int {
	start := -1
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			if start < 0 {
				start = i
			}
		} else if start >= 0 {
			n, _ := strconv.Atoi(s[start:i])
			return n
		}
	}
	if start >= 0 {
		n, _ := strconv.Atoi(s[start:])
		return n
	}
	return -1
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
