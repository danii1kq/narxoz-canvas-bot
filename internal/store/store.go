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
-- user it authenticated as and are only used for display. verified=0 means the
-- token was stored while Canvas was unreachable and is still being checked.
CREATE TABLE IF NOT EXISTS chat_tokens (
    chat_id  INTEGER PRIMARY KEY,
    token    TEXT    NOT NULL,
    name     TEXT    NOT NULL DEFAULT '',
    login    TEXT    NOT NULL DEFAULT '',
    email    TEXT    NOT NULL DEFAULT '',
    verified INTEGER NOT NULL DEFAULT 0,
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
	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("store: migrate columns: %w", err)
	}
	return &Store{db: db}, nil
}

// migrate adds columns introduced after the first release so an existing
// database keeps working without manual intervention (CREATE TABLE IF NOT
// EXISTS does not touch tables that already exist).
func migrate(db *sql.DB) error {
	// chat_tokens.verified: added when tokens became verifiable in the
	// background (a probe that hit a network error still stores the token).
	ok, err := columnExists(db, "chat_tokens", "verified")
	if err != nil || ok {
		return err
	}
	_, err = db.Exec("ALTER TABLE chat_tokens ADD COLUMN verified INTEGER NOT NULL DEFAULT 0")
	return err
}

// columnExists reports whether table has the given column.
func columnExists(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query("SELECT 1 FROM pragma_table_info(?) WHERE name=?", table, column)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	return rows.Next(), rows.Err()
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}
