package poller

import (
	"time"

	"narxoz-canvas-bot/internal/model"
)

// DetectRealtime diffs the previous stored assignment rows against the freshly
// fetched current rows and returns the realtime notification events (new
// assignment, new grade, submission seen) together with the next state to
// persist.
//
// Rules:
//   - An assignment id not present in prev is "new": emit EventNewAssignment
//     and mark NotifiedNew (so it is not re-detected).
//   - A known assignment whose score / graded_at / grade changed emits
//     EventNewGrade.
//   - A known assignment whose submitted_at transitioned from nil to a value
//     emits EventSubmitted. If it was submitted after the deadline, the
//     deadline/overdue notification flags are reset so a later re-open can
//     re-notify.
//   - If a previously submitted assignment becomes unsubmitted again (teacher
//     reopened submissions), the overdue flag is reset.
//
// Deadline/overdue notifications are NOT computed here; they are handled by
// DetectReminders on a separate schedule. This function only carries those
// flags forward (or resets them on re-submission).
func DetectRealtime(prev, current []model.Assignment, now time.Time) ([]model.Event, []model.Assignment) {
	_ = now // kept for signature symmetry / future use
	prevByID := make(map[int64]model.Assignment, len(prev))
	for _, p := range prev {
		prevByID[p.ID] = p
	}

	next := make([]model.Assignment, 0, len(current))
	var events []model.Event

	for _, cur := range current {
		n := cur
		n.Active = true

		p, existed := prevByID[cur.ID]
		if !existed {
			// Brand-new assignment.
			n.NotifiedNew = true
			n.NotifiedEarly = false
			n.Notified1h = false
			n.NotifiedOverdue = false
			events = append(events, model.Event{Kind: model.EventNewAssignment, A: cur})
			next = append(next, n)
			continue
		}

		// Known assignment: carry flags forward.
		n.NotifiedNew = true
		n.NotifiedEarly = p.NotifiedEarly
		n.Notified1h = p.Notified1h
		n.NotifiedOverdue = p.NotifiedOverdue

		// New grade: score, graded_at or grade label changed.
		if !equalFloatPtr(p.Score, cur.Score) ||
			!equalTimePtr(p.GradedAt, cur.GradedAt) ||
			!equalStrPtr(p.Grade, cur.Grade) {
			oldScore, oldGrade := p.Score, p.Grade
			events = append(events, model.Event{Kind: model.EventNewGrade, A: cur, OldScore: oldScore, OldGrade: oldGrade})
		}

		// Submission appeared.
		if p.SubmittedAt == nil && cur.SubmittedAt != nil {
			events = append(events, model.Event{Kind: model.EventSubmitted, A: cur})
			if cur.DueAt != nil && cur.SubmittedAt.After(*cur.DueAt) {
				// Submitted after the deadline: reset so a re-open re-notifies.
				n.NotifiedOverdue = false
				n.NotifiedEarly = false
				n.Notified1h = false
			}
		}

		// Became unsubmitted again (submission reopened): allow overdue to fire.
		if p.SubmittedAt != nil && cur.SubmittedAt == nil {
			n.NotifiedOverdue = false
		}

		next = append(next, n)
	}

	return events, next
}

// DetectReminders fires deadline and overdue reminders for UNSBUMITTED,
// active, future-or-past assignments. Each reminder fires at most once thanks
// to the persisted flags.
//
//   - overdue (due < now): one EventOverdue, then NotifiedOverdue stays set.
//   - last hour (now < due - now <= 1h): one EventDeadline1h.
//   - early window (1h < due - now <= lead): one EventDeadlineEarly.
//
// Submitted assignments (SubmittedAt set, or workflow graded/submitted) are
// skipped entirely.
func DetectReminders(state []model.Assignment, now time.Time, lead time.Duration) ([]model.Event, []model.Assignment) {
	out := make([]model.Assignment, 0, len(state))
	var events []model.Event

	for _, a := range state {
		n := a
		if !a.Active || a.Submitted() || a.DueAt == nil {
			out = append(out, n)
			continue
		}
		remaining := a.DueAt.Sub(now)
		switch {
		case remaining <= 0:
			if !a.NotifiedOverdue {
				n.NotifiedOverdue = true
				events = append(events, model.Event{Kind: model.EventOverdue, A: a})
			}
		case remaining <= time.Hour:
			if !a.Notified1h {
				n.Notified1h = true
				events = append(events, model.Event{Kind: model.EventDeadline1h, A: a})
			}
		case remaining <= lead:
			if !a.NotifiedEarly {
				n.NotifiedEarly = true
				events = append(events, model.Event{Kind: model.EventDeadlineEarly, A: a, Lead: int(lead.Hours())})
			}
		}
		out = append(out, n)
	}
	return events, out
}

func equalFloatPtr(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func equalTimePtr(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

func equalStrPtr(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}
