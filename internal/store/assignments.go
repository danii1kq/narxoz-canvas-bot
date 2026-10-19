package store

import (
	"database/sql"
	"time"

	"narxoz-canvas-bot/internal/model"
)

// assignmentCols lists the stored assignment columns in a fixed order that
// matches scanAssignment and the upsert statement.
const assignmentCols = `course_id, assignment_id, name, points, due_at,
submitted_at, graded_at, score, grade, workflow_state, html_url, active,
notified_new, notified_early, notified_1h, notified_overdue, last_seen`

const assignmentUpsert = `INSERT OR REPLACE INTO assignments (` + assignmentCols + `)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// LoadAssignments returns every stored assignment row.
func (s *Store) LoadAssignments() ([]model.Assignment, error) {
	rows, err := s.db.Query("SELECT " + assignmentCols + " FROM assignments")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Assignment
	for rows.Next() {
		a, err := scanAssignment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpsertAssignments writes (or replaces) full assignment rows in one
// transaction. The caller supplies all data fields AND the notification flags,
// so flags explicitly mutated by the poller/reminder job are preserved.
func (s *Store) UpsertAssignments(rows []model.Assignment) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(assignmentUpsert)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, a := range rows {
		if a.LastSeen.IsZero() {
			a.LastSeen = nowUTC()
		}
		if err := execAssignment(stmt, a); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SyncAssignments marks every stored assignment inactive and then reactivates
// (or inserts) the provided rows as active, all within one transaction. This
// keeps rows for assignments/courses that disappeared without ever deleting
// them, so spurious "new assignment" notifications cannot be triggered by an
// API glitch. Notification flags of existing rows are preserved by
// INSERT OR REPLACE because the poller passes back the mutated rows.
//
// The poller uses SyncAll instead, which only deactivates the courses it could
// really fetch, so a single failing course cannot empty the dataset.
func (s *Store) SyncAssignments(rows []model.Assignment) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := deactivateAllAssignments(tx); err != nil {
		return err
	}
	if err := upsertAssignments(tx, rows); err != nil {
		return err
	}
	return tx.Commit()
}

// execAssignment binds a model.Assignment to the fixed column list.
func execAssignment(stmt *sql.Stmt, a model.Assignment) error {
	lastSeen := a.LastSeen
	if lastSeen.IsZero() {
		lastSeen = nowUTC()
	}
	_, err := stmt.Exec(
		a.CourseID, a.ID, a.Name,
		floatPtr(a.Points), timePtr(a.DueAt),
		timePtr(a.SubmittedAt), timePtr(a.GradedAt),
		floatPtr(a.Score), strPtr(a.Grade),
		a.WorkflowState, a.HTMLURL,
		boolInt(a.Active),
		boolInt(a.NotifiedNew), boolInt(a.NotifiedEarly),
		boolInt(a.Notified1h), boolInt(a.NotifiedOverdue),
		lastSeen.UTC().Format(time.RFC3339),
	)
	return err
}

func scanAssignment(sc scanner) (model.Assignment, error) {
	var a model.Assignment
	var points, score sql.NullFloat64
	var dueAt, submittedAt, gradedAt, grade, lastSeen sql.NullString
	var active, nn, ne, n1, no int
	if err := sc.Scan(
		&a.CourseID, &a.ID, &a.Name,
		&points, &dueAt, &submittedAt, &gradedAt,
		&score, &grade, &a.WorkflowState, &a.HTMLURL, &active,
		&nn, &ne, &n1, &no, &lastSeen,
	); err != nil {
		return a, err
	}
	a.Points = nullFloat(points)
	a.Score = nullFloat(score)
	a.DueAt = nullTime(dueAt)
	a.SubmittedAt = nullTime(submittedAt)
	a.GradedAt = nullTime(gradedAt)
	if grade.Valid {
		g := grade.String
		a.Grade = &g
	}
	a.Active = active != 0
	a.NotifiedNew = nn != 0
	a.NotifiedEarly = ne != 0
	a.Notified1h = n1 != 0
	a.NotifiedOverdue = no != 0
	if t, err := time.Parse(time.RFC3339, lastSeen.String); err == nil {
		a.LastSeen = t
	}
	return a, nil
}

// --- nullable helpers ---

func nullFloat(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}

func nullTime(v sql.NullString) *time.Time {
	if !v.Valid || v.String == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, v.String)
	if err != nil {
		return nil
	}
	return &t
}

func floatPtr(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}

func strPtr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func timePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
