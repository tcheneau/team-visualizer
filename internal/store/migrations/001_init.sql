-- 001_init.sql: Initial schema

-- Users (synced from reverse proxy headers)
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT UNIQUE NOT NULL,
    role TEXT NOT NULL DEFAULT 'readonly',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_seen DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Settings (key-value store)
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- People (team members + guests)
CREATE TABLE IF NOT EXISTS people (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    role TEXT DEFAULT '',
    sub_team TEXT DEFAULT '',
    avatar_emoji TEXT DEFAULT '👤',
    avatar_color TEXT DEFAULT '#4361ee',
    start_date TEXT DEFAULT '',
    default_projects TEXT DEFAULT '[]',
    status TEXT DEFAULT 'active',
    archived_date TEXT DEFAULT '',
    is_guest INTEGER DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Planning entries (half-day slots)
CREATE TABLE IF NOT EXISTS planning (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    person_id TEXT NOT NULL,
    date TEXT NOT NULL,
    slot TEXT NOT NULL,
    state TEXT DEFAULT 'not_filled',
    away_type TEXT DEFAULT '',
    away_note TEXT DEFAULT '',
    run INTEGER DEFAULT 0,
    projects TEXT DEFAULT '[]',
    FOREIGN KEY (person_id) REFERENCES people(id) ON DELETE CASCADE,
    UNIQUE (person_id, date, slot)
);

-- Run rotation (person ↔ week)
CREATE TABLE IF NOT EXISTS rotation (
    person_id TEXT NOT NULL,
    week_start TEXT NOT NULL,
    PRIMARY KEY (person_id, week_start),
    FOREIGN KEY (person_id) REFERENCES people(id) ON DELETE CASCADE
);

-- On-call (person ↔ week)
CREATE TABLE IF NOT EXISTS oncall (
    person_id TEXT NOT NULL,
    week_start TEXT NOT NULL,
    PRIMARY KEY (person_id, week_start),
    FOREIGN KEY (person_id) REFERENCES people(id) ON DELETE CASCADE
);

-- Default settings
INSERT OR IGNORE INTO settings (key, value) VALUES ('window_weeks', '4');
INSERT OR IGNORE INTO settings (key, value) VALUES ('prune_weeks', '12');
INSERT OR IGNORE INTO settings (key, value) VALUES ('week_starts', 'monday');
INSERT OR IGNORE INTO settings (key, value) VALUES ('run_mode', 'ratio');
INSERT OR IGNORE INTO settings (key, value) VALUES ('run_target_persons', '3');
INSERT OR IGNORE INTO settings (key, value) VALUES ('theme', 'dracula');
INSERT OR IGNORE INTO settings (key, value) VALUES ('export_counter', '1');