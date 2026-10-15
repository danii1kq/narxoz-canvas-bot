package poller_test

import (
	"testing"
	"time"

	"narxoz-canvas-bot/internal/model"
	"narxoz-canvas-bot/internal/poller"
)

func f64(v float64) *float64 { return &v }

func at(base time.Time, d time.Duration) *time.Time {
	t := base.Add(d).UTC()
	return &t
}

func TestDetectRealtime_NewAssignment(t *testing.T) {
	now := time.Now().UTC()
	cur := []model.Assignment{{CourseID: 1, ID: 100, Name: "HW1", DueAt: at(now, 48*time.Hour), Active: true}}

	events, next := poller.DetectRealtime(nil, cur, now)
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	if events[0].Kind != model.EventNewAssignment {
		t.Fatalf("want EventNewAssignment, got %v", events[0].Kind)
	}
	if len(next) != 1 || !next[0].NotifiedNew {
		t.Fatalf("next assignment must mark NotifiedNew")
	}
}

func TestDetectRealtime_NewGrade(t *testing.T) {
	now := time.Now().UTC()
	prev := []model.Assignment{{CourseID: 1, ID: 1, Name: "A", Score: f64(5), Active: true}}
	cur := []model.Assignment{{CourseID: 1, ID: 1, Name: "A", Score: f64(9), GradedAt: at(now, -time.Hour), Active: true}}

	events, _ := poller.DetectRealtime(prev, cur, now)
	if len(events) != 1 || events[0].Kind != model.EventNewGrade {
		t.Fatalf("want 1 EventNewGrade, got %+v", events)
	}
	if events[0].OldScore == nil || *events[0].OldScore != 5 {
		t.Fatalf("want old score 5, got %v", events[0].OldScore)
	}
}

func TestDetectRealtime_NoGradeChangeWhenEqual(t *testing.T) {
	now := time.Now().UTC()
	grade := "A"
	row := model.Assignment{CourseID: 1, ID: 1, Name: "A", Score: f64(9), Grade: &grade, GradedAt: at(now, -time.Hour), Active: true}
	events, _ := poller.DetectRealtime([]model.Assignment{row}, []model.Assignment{row}, now)
	if len(events) != 0 {
		t.Fatalf("want no events, got %d: %+v", len(events), events)
	}
}

func TestDetectReminders_EarlyUnsubmittedFiresOnce(t *testing.T) {
	now := time.Now().UTC()
	state := []model.Assignment{{CourseID: 1, ID: 1, Name: "A", DueAt: at(now, 2*time.Hour), Active: true}}

	events, updated := poller.DetectReminders(state, now, 24*time.Hour)
	if len(events) != 1 || events[0].Kind != model.EventDeadlineEarly {
		t.Fatalf("want 1 EventDeadlineEarly, got %+v", events)
	}
	if events[0].Lead != 24 {
		t.Fatalf("want Lead=24, got %d", events[0].Lead)
	}
	events2, _ := poller.DetectReminders(updated, now, 24*time.Hour)
	if len(events2) != 0 {
		t.Fatalf("early reminder must fire only once, got %d", len(events2))
	}
}

func TestDetectReminders_SkipsSubmitted(t *testing.T) {
	now := time.Now().UTC()
	state := []model.Assignment{{
		CourseID: 1, ID: 1, Name: "A",
		DueAt: at(now, 2*time.Hour), SubmittedAt: at(now, -time.Minute), Active: true,
	}}
	events, _ := poller.DetectReminders(state, now, 24*time.Hour)
	if len(events) != 0 {
		t.Fatalf("submitted assignments must not get reminders, got %d", len(events))
	}
}

func TestDetectReminders_OverdueFiresExactlyOnce(t *testing.T) {
	now := time.Now().UTC()
	state := []model.Assignment{{CourseID: 1, ID: 1, Name: "A", DueAt: at(now, -time.Minute), Active: true}}

	events, updated := poller.DetectReminders(state, now, 24*time.Hour)
	if len(events) != 1 || events[0].Kind != model.EventOverdue {
		t.Fatalf("want 1 EventOverdue, got %+v", events)
	}
	events2, _ := poller.DetectReminders(updated, now, 24*time.Hour)
	if len(events2) != 0 {
		t.Fatalf("overdue must fire exactly once, got %d", len(events2))
	}
}

func TestSubmitBeforeDeadlineStopsReminders(t *testing.T) {
	now := time.Now().UTC()
	due := at(now, 2*time.Hour)
	prev := []model.Assignment{{CourseID: 1, ID: 1, Name: "A", DueAt: due, Active: true}}

	// First reminder fires for the unsubmitted assignment.
	events, _ := poller.DetectReminders(prev, now, 24*time.Hour)
	if len(events) != 1 {
		t.Fatalf("precondition: want 1 early reminder")
	}

	// User submits before the deadline.
	cur := []model.Assignment{{CourseID: 1, ID: 1, Name: "A", DueAt: due, SubmittedAt: at(now, time.Minute), Active: true}}
	_, next := poller.DetectRealtime(prev, cur, now)

	// No more reminders.
	events2, _ := poller.DetectReminders(next, now, 24*time.Hour)
	if len(events2) != 0 {
		t.Fatalf("after submission there must be no reminders, got %+v", events2)
	}
}

func TestSubmitAfterDeadlineResetsOverdue(t *testing.T) {
	now := time.Now().UTC()
	due := at(now, -time.Hour)
	prev := []model.Assignment{{CourseID: 1, ID: 1, Name: "A", DueAt: due, Active: true, NotifiedOverdue: true}}

	// Submitted after the deadline.
	cur := []model.Assignment{{CourseID: 1, ID: 1, Name: "A", DueAt: due, SubmittedAt: at(now, time.Minute), Active: true}}
	events, next := poller.DetectRealtime(prev, cur, now)
	if len(events) != 1 || events[0].Kind != model.EventSubmitted {
		t.Fatalf("want EventSubmitted, got %+v", events)
	}
	if next[0].NotifiedOverdue {
		t.Fatalf("NotifiedOverdue must be reset after late submission")
	}

	// Teacher reopens submissions -> unsubmitted again -> overdue re-fires.
	reopened := []model.Assignment{{CourseID: 1, ID: 1, Name: "A", DueAt: due, Active: true}}
	_, next2 := poller.DetectRealtime(next, reopened, now)
	events2, _ := poller.DetectReminders(next2, now, 24*time.Hour)
	if len(events2) != 1 || events2[0].Kind != model.EventOverdue {
		t.Fatalf("overdue must re-fire after reopen, got %+v", events2)
	}
}
