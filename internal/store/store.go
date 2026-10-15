// Package store implements the SQLite persistence layer (pure Go via
// modernc.org/sqlite, no CGO). It keeps courses, assignments, per-course mute
// state, general settings and subscribers.
package store

import (
	"database/sql"
	"fmt"
	"strings"

	// Pure-Go SQLite driver (no CGO).
	_ "modernc.org/sqlite"
)

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// schema is executed on startup (idempotent).
const schema = `
CREATE TABLE IF NOT EXISTS courses (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL DEFAULT '',
    code       TEXT    NOT NULL DEFAULT '',
    active     INTEGER NOT NULL DEFAULT 1,
    muted      INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS assignments (
    course_id        INTEGER NOT NULL,
    assignment_id    INTEGER NOT NULL,
    name             TEXT    NOT NULL DEFAULT '',
    points           REAL,
    due_at           TEXT,
    submitted_at     TEXT,
    graded_at        TEXT,
    score            REAL,
    grade            TEXT,
    workflow_state   TEXT    NOT NULL DEFAULT '',
    html_url         TEXT    NOT NULL DEFAULT '',
    active           INTEGER NOT NULL DEFAULT 1,
    notified_new     INTEGER NOT NULL DEFAULT 0,
    notified_early   INTEGER NOT NULL DEFAULT 0,
    notified_1h      INTEGER NOT NULL DEFAULT 0,
    notified_overdue INTEGER NOT NULL DEFAULT 0,
    last_seen        TEXT    NOT NULL,
    PRIMARY KEY (course_id, assignment_id)
);
CREATE INDEX IF NOT EXISTS idx_assign_course ON assignments(course_id);

CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS subscribers (
    chat_id    INTEGER PRIMARY KEY,
    created_at TEXT NOT NULL
);

-- Canvas API tokens pasted by users inside the chat (/setup). The token is
-- stored sealed (see internal/secrets); name/login/email describe the Canvas
-- user it authenticated as and are only used for display.
CREATE TABLE IF NOT EXISTS chat_tokens (
    chat_id  INTEGER PRIMARY KEY,
    token    TEXT    NOT NULL,
    name     TEXT    NOT NULL DEFAULT '',
    login    TEXT    NOT NULL DEFAULT '',
    email    TEXT    NOT NULL DEFAULT '',
    saved_at TEXT    NOT NULL
);
`

// Open opens (creating if needed) the SQLite database at path, runs the
// migrations and returns a Store. Use ":memory:" for an ephemeral database.
func Open(path string) (*Store, error) {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	dsn := fmt.Sprintf("%s%s_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)", path, sep)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %q: %w", path, err)
	}
	// Serialise writers to avoid SQLITE_BUSY; this bot is single-writer.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}
