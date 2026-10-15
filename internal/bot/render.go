package bot

import (
	"sort"
	"time"

	"narxoz-canvas-bot/internal/model"
)

// SortUpcoming returns a copy of items sorted by due_at ascending (nearest
// deadline first). Assignments without a due date are placed last. Ties are
// broken by name then id for a stable, deterministic order.
func SortUpcoming(items []model.Assignment) []model.Assignment {
	out := append([]model.Assignment(nil), items...)
	sort.SliceStable(out, func(i, j int) bool {
		di, dj := out[i].DueAt, out[j].DueAt
		switch {
		case di == nil || dj == nil:
			if di == nil && dj == nil {
				return lessByKey(out[i], out[j])
			}
			return di != nil // present due dates sort before nil
		case !di.Equal(*dj):
			return di.Before(*dj)
		default:
			return lessByKey(out[i], out[j])
		}
	})
	return out
}

// SortGrades returns a copy of items sorted by graded_at descending (most
// recent grade first). Ungraded items are placed last.
func SortGrades(items []model.Assignment) []model.Assignment {
	out := append([]model.Assignment(nil), items...)
	sort.SliceStable(out, func(i, j int) bool {
		gi, gj := out[i].GradedAt, out[j].GradedAt
		switch {
		case gi == nil || gj == nil:
			if gi == nil && gj == nil {
				return lessByKey(out[i], out[j])
			}
			return gi != nil // graded items sort before ungraded
		case !gi.Equal(*gj):
			return gi.After(*gj)
		default:
			return lessByKey(out[i], out[j])
		}
	})
	return out
}

func lessByKey(a, b model.Assignment) bool {
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	return a.ID < b.ID
}

// IsMissing reports whether an assignment is active, unsubmitted and has a
// deadline in the future.
func IsMissing(a model.Assignment, now time.Time) bool {
	return a.Active && !a.Submitted() && a.DueAt != nil && a.DueAt.After(now)
}

// IsOverdue reports whether an assignment is active, unsubmitted and past due.
func IsOverdue(a model.Assignment, now time.Time) bool {
	return a.Active && !a.Submitted() && a.DueAt != nil && a.DueAt.Before(now)
}

// IsGraded reports whether an active assignment has a score recorded.
func IsGraded(a model.Assignment) bool {
	return a.Active && a.Score != nil
}
