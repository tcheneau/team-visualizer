package store

import (
	"database/sql"
	_ "embed"
	"fmt"
	"time"

	"github.com/teamviz/team-visualizer/internal/model"
)

//go:embed migrations/001_init.sql
var migration001 string

//go:embed migrations/002_projects.sql
var migration002 string

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
	migrations := []string{migration001, migration002}
	for i, m := range migrations {
		if _, err := s.db.Exec(m); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
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