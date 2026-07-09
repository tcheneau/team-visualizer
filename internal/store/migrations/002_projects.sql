-- 002_projects.sql: Projects and holidays

CREATE TABLE IF NOT EXISTS projects (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    emoji TEXT DEFAULT '📁',
    description TEXT DEFAULT '',
    url TEXT DEFAULT '',
    start_date TEXT DEFAULT '',
    end_date TEXT DEFAULT '',
    status TEXT DEFAULT 'unstarted',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS holidays (
    date TEXT NOT NULL,
    label TEXT DEFAULT '',
    country TEXT DEFAULT '',
    PRIMARY KEY (date, country)
);