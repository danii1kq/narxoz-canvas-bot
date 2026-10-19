package store

import (
	"database/sql"
	"strings"
	"time"

	"narxoz-canvas-bot/internal/model"
)

// SyncAll applies a full sync in a single transaction, so a crash in the
// middle can never leave the database half updated.
//
// courses is the list Canvas returned: those are upserted as active and every
// other stored course becomes inactive (but is never deleted).
//
// assignments holds the rows of the courses whose assignments were fetched.
// syncedCourseIDs lists exactly those courses: only their assignments are
// deactivated before the new rows are written. A course whose fetch failed
// keeps its assignments untouched, so a transient Canvas error cannot empty
// the dataset and cannot hide upcoming deadlines.
func (s *Store) SyncAll(courses []model.Course, assignments []model.Assignment, syncedCourseIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := upsertCourses(tx, courses); err != nil {
		return err
	}
	if err := deactivateStaleAssignments(tx, courses, syncedCourseIDs); err != nil {
		return err
	}
	if err := upsertAssignments(tx, assignments); err != nil {
		return err
	}
	return tx.Commit()
}

// deactivateStaleAssignments marks the assignments inactive that must not be
// shown any more:
//
//   - the assignments of the courses that were really fetched (their current
//     rows are replaced by the new ones), and
//   - the assignments of the courses that are not in the Canvas list any more.
//
// The assignments of a course that is still listed but whose fetch failed stay
// untouched, so a transient Canvas error cannot empty the dataset.
func deactivateStaleAssignments(tx *sql.Tx, courses []model.Course, syncedCourseIDs []int64) error {
	var clauses []string
	var args []any
	if len(syncedCourseIDs) > 0 {
		clauses = append(clauses, "course_id IN ("+placeholders(len(syncedCourseIDs))+")")
		args = append(args, idsToAny(syncedCourseIDs)...)
	}
	current := make([]int64, 0, len(courses))
	for _, c := range courses {
		current = append(current, c.ID)
	}
	if len(current) == 0 {
		// Canvas does not know any course: everything that was not fetched
		// in this round is stale.
		clauses = append(clauses, "1=1")
	} else {
		clauses = append(clauses, "course_id NOT IN ("+placeholders(len(current))+")")
		args = append(args, idsToAny(current)...)
	}
	_, err := tx.Exec("UPDATE assignments SET active=0 WHERE "+strings.Join(clauses, " OR "), args...)
	return err
}

// placeholders returns "?,?,?" for n bound parameters.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// idsToAny converts course ids into bound parameters.
func idsToAny(ids []int64) []any {
	out := make([]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, id)
	}
	return out
}

// deactivateAllAssignments marks every stored assignment inactive.
func deactivateAllAssignments(tx *sql.Tx) error {
	_, err := tx.Exec("UPDATE assignments SET active=0")
	return err
}

// upsertAssignments writes (or replaces) the given assignment rows as active.
func upsertAssignments(tx *sql.Tx, rows []model.Assignment) error {
	if len(rows) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(assignmentUpsert)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, a := range rows {
		a.Active = true
		if a.LastSeen.IsZero() {
			a.LastSeen = nowUTC()
		}
		if err := execAssignment(stmt, a); err != nil {
			return err
		}
	}
	return nil
}

// upsertCourses writes the given courses as active and marks every other
// stored course inactive. The mute state of existing courses is preserved.
func upsertCourses(tx *sql.Tx, courses []model.Course) error {
	if _, err := tx.Exec("UPDATE courses SET active=0"); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO courses (id, name, code, active, muted, updated_at)
VALUES (?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, code=excluded.code,
    active=excluded.active, updated_at=excluded.updated_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := nowUTC().Format(time.RFC3339)
	for _, c := range courses {
		// Provided courses are by definition active (enrollment_state=active).
		active := 1
		muted := 0
		if c.Muted {
			muted = 1
		}
		if _, err := stmt.Exec(c.ID, c.Name, c.Code, active, muted, now); err != nil {
			return err
		}
	}
	return nil
}
