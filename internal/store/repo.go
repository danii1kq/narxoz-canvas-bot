package store

import (
	"strconv"
	"time"

	"narxoz-canvas-bot/internal/model"
)

// nowUTC returns the current time in UTC.
func nowUTC() time.Time { return time.Now().UTC() }

// --- Courses ---

// LoadCourses returns stored courses ordered by name. When activeOnly is true
// only courses currently active in Canvas are returned.
func (s *Store) LoadCourses(activeOnly bool) ([]model.Course, error) {
	q := "SELECT id, name, code, active, muted, updated_at FROM courses"
	if activeOnly {
		q += " WHERE active=1"
	}
	q += " ORDER BY name COLLATE NOCASE"
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Course
	for rows.Next() {
		var c model.Course
		var active, muted int
		var upd string
		if err := rows.Scan(&c.ID, &c.Name, &c.Code, &active, &muted, &upd); err != nil {
			return nil, err
		}
		c.Active = active != 0
		c.Muted = muted != 0
		if t, err := time.Parse(time.RFC3339, upd); err == nil {
			c.UpdatedAt = t
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CourseNameMap returns a map of active course id -> name.
func (s *Store) CourseNameMap() map[int64]string {
	courses, err := s.LoadCourses(false)
	if err != nil {
		return map[int64]string{}
	}
	m := make(map[int64]string, len(courses))
	for _, c := range courses {
		m[c.ID] = c.Name
	}
	return m
}

// LoadCourseMutes returns a map of course id -> muted for all courses.
func (s *Store) LoadCourseMutes() map[int64]bool {
	courses, err := s.LoadCourses(false)
	if err != nil {
		return map[int64]bool{}
	}
	m := make(map[int64]bool, len(courses))
	for _, c := range courses {
		if c.Muted {
			m[c.ID] = true
		}
	}
	return m
}

// SyncCourses upserts the provided courses (marking them active) and marks
// every other stored course inactive within a single transaction. Mute state
// of existing courses is preserved.
func (s *Store) SyncCourses(courses []model.Course) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := upsertCourses(tx, courses); err != nil {
		return err
	}
	return tx.Commit()
}

// SetCourseMuted sets the mute flag for a course.
func (s *Store) SetCourseMuted(id int64, muted bool) error {
	m := 0
	if muted {
		m = 1
	}
	_, err := s.db.Exec("UPDATE courses SET muted=? WHERE id=?", m, id)
	return err
}

// ToggleCourseMute flips the mute flag for a course and returns the new state.
func (s *Store) ToggleCourseMute(id int64) (bool, error) {
	var muted int
	if err := s.db.QueryRow("SELECT muted FROM courses WHERE id=?", id).Scan(&muted); err != nil {
		return false, err
	}
	next := muted == 0
	if err := s.SetCourseMuted(id, next); err != nil {
		return false, err
	}
	return next, nil
}

// --- Settings ---

// GetSetting returns the value for key, or def when unset.
func (s *Store) GetSetting(key, def string) string {
	var v string
	if err := s.db.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&v); err != nil {
		return def
	}
	return v
}

// SetSetting stores a key/value setting.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// GetLeadHours returns the configured early-reminder lead time in hours.
func (s *Store) GetLeadHours(def int) int {
	n, err := strconv.Atoi(s.GetSetting("lead_hours", ""))
	if err != nil {
		return def
	}
	switch n {
	case 6, 12, 24:
		return n
	}
	return def
}

// SetLeadHours persists the early-reminder lead time in hours.
func (s *Store) SetLeadHours(n int) error {
	return s.SetSetting("lead_hours", strconv.Itoa(n))
}

// IsSeeded reports whether the initial (silent) seeding pass has run.
func (s *Store) IsSeeded() bool {
	return s.GetSetting("seeded", "0") == "1"
}

// SetSeeded marks the initial seeding pass as completed.
func (s *Store) SetSeeded() error {
	return s.SetSetting("seeded", "1")
}

// --- Subscribers ---

// AddSubscriber registers a chat id to receive notifications (idempotent).
func (s *Store) AddSubscriber(chatID int64) error {
	_, err := s.db.Exec(`INSERT INTO subscribers (chat_id, created_at) VALUES (?,?)
ON CONFLICT(chat_id) DO NOTHING`, chatID, nowUTC().Format(time.RFC3339))
	return err
}

// Subscribers returns all registered subscriber chat ids.
func (s *Store) Subscribers() []int64 {
	rows, err := s.db.Query("SELECT chat_id FROM subscribers")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return out
		}
		out = append(out, id)
	}
	return out
}
