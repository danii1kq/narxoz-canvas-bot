package bot

import (
	"testing"
	"time"

	"narxoz-canvas-bot/internal/model"
)

func ids(items []model.Assignment) []int64 {
	out := make([]int64, len(items))
	for i, a := range items {
		out[i] = a.ID
	}
	return out
}

func TestSortUpcoming(t *testing.T) {
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	d1 := base.Add(48 * time.Hour)
	d3 := base.Add(1 * time.Hour)
	d4 := base.Add(3 * time.Hour)
	items := []model.Assignment{
		{ID: 1, DueAt: &d1},
		{ID: 2, DueAt: nil}, // no deadline -> last
		{ID: 3, DueAt: &d3}, // nearest -> first
		{ID: 4, DueAt: &d4},
	}
	got := ids(SortUpcoming(items))
	want := []int64{3, 4, 1, 2}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestSortGrades(t *testing.T) {
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	g1 := base.Add(1 * time.Hour)
	g2 := base.Add(3 * time.Hour) // most recent
	items := []model.Assignment{
		{ID: 1, GradedAt: &g1, Score: f64(7)},
		{ID: 2, GradedAt: &g2, Score: f64(9)},
		{ID: 3, GradedAt: nil, Score: nil}, // ungraded -> last
	}
	got := ids(SortGrades(items))
	want := []int64{2, 1, 3}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestIsMissingOverdueGraded(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	future := now.Add(2 * time.Hour)
	past := now.Add(-2 * time.Hour)
	submitted := now.Add(-time.Minute)

	tests := []struct {
		name        string
		a           model.Assignment
		wantMissing bool
		wantOverdue bool
		wantGraded  bool
	}{
		{
			name:        "future unsubmitted active",
			a:           model.Assignment{DueAt: &future, Active: true},
			wantMissing: true,
		},
		{
			name:        "future submitted",
			a:           model.Assignment{DueAt: &future, SubmittedAt: &submitted, Active: true},
			wantMissing: false,
		},
		{
			name:        "past unsubmitted active",
			a:           model.Assignment{DueAt: &past, Active: true},
			wantOverdue: true,
		},
		{
			name:        "inactive ignored",
			a:           model.Assignment{DueAt: &future, Active: false},
			wantMissing: false,
		},
		{
			name:       "graded",
			a:          model.Assignment{Score: f64(10), Active: true},
			wantGraded: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsMissing(tt.a, now); got != tt.wantMissing {
				t.Errorf("IsMissing = %v, want %v", got, tt.wantMissing)
			}
			if got := IsOverdue(tt.a, now); got != tt.wantOverdue {
				t.Errorf("IsOverdue = %v, want %v", got, tt.wantOverdue)
			}
			if got := IsGraded(tt.a); got != tt.wantGraded {
				t.Errorf("IsGraded = %v, want %v", got, tt.wantGraded)
			}
		})
	}
}

func f64(v float64) *float64 { return &v }
