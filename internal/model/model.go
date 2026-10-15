// Package model contains the shared domain types used across the bot.
//
// All timestamps (DueAt, SubmittedAt, GradedAt, UpdatedAt, LastSeen) are
// stored in UTC. Timezone conversion to Asia/Almaty happens only at
// formatting time (see internal/notify.Formatter).
package model

import "time"

// User mirrors the authenticated Canvas user returned by GET /api/v1/users/self.
type User struct {
	ID    int64
	Name  string
	Login string
	Email string
}

// Course is a Canvas course plus locally tracked notification/mute state.
type Course struct {
	ID   int64
	Name string
	Code string
	// Active is false when the course disappeared from the API response.
	// Rows are never deleted; the flag is flipped instead (see store.SyncCourses).
	Active bool
	// Muted silences all notifications for this course.
	Muted     bool
	UpdatedAt time.Time
}

// Assignment is a Canvas assignment plus locally tracked notification flags.
//
// The Canvas-owned fields (Name, Points, DueAt, SubmittedAt, GradedAt, Score,
// Grade, WorkflowState, HTMLURL) are refreshed by the poller. The Notified*
// flags and LastSeen are local bookkeeping persisted in the SQLite store.
type Assignment struct {
	CourseID      int64
	ID            int64
	Name          string
	Points        *float64
	DueAt         *time.Time
	SubmittedAt   *time.Time
	GradedAt      *time.Time
	Score         *float64
	Grade         *string
	WorkflowState string
	HTMLURL       string

	// Active is false when the assignment (or its course) is no longer present
	// in the API response. Inactive assignments are ignored by reminders and lists.
	Active bool

	// Notification bookkeeping (persisted only).
	NotifiedNew     bool
	NotifiedEarly   bool // reminder at the configurable lead time (default 24h)
	Notified1h      bool
	NotifiedOverdue bool
	LastSeen        time.Time
}

// Submitted reports whether a submission has been recorded or graded.
// It is used to gate deadline/overdue reminders.
func (a Assignment) Submitted() bool {
	if a.SubmittedAt != nil {
		return true
	}
	switch a.WorkflowState {
	case "graded", "submitted", "pending_review":
		return true
	default:
		return false
	}
}

// Late reports whether the assignment was submitted after its deadline.
func (a Assignment) Late() bool {
	return a.SubmittedAt != nil && a.DueAt != nil && a.SubmittedAt.After(*a.DueAt)
}

// EventKind enumerates the notification types the diff logic can produce.
type EventKind int

const (
	EventNewAssignment EventKind = iota
	EventNewGrade
	EventSubmitted
	EventDeadlineEarly
	EventDeadline1h
	EventOverdue
)

// Event is a single notification produced by the poller diff logic.
// Course (course name) and Lead (reminder lead hours) are filled by the
// poller just before formatting/delivery.
type Event struct {
	Kind     EventKind
	Course   string
	Lead     int
	A        Assignment
	OldScore *float64 // previous score, for EventNewGrade context
	OldGrade *string
}
