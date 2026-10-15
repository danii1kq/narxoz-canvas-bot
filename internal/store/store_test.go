package store_test

import (
	"path/filepath"
	"sort"
	"testing"
	"time"

	"narxoz-canvas-bot/internal/model"
	"narxoz-canvas-bot/internal/store"
)

func openTemp(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSyncCourses_MarksMissingInactiveAndPreservesMute(t *testing.T) {
	st := openTemp(t)
	if err := st.SyncCourses([]model.Course{{ID: 1, Name: "Math"}, {ID: 2, Name: "Phys"}, {ID: 3, Name: "Chem"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCourseMuted(2, true); err != nil {
		t.Fatal(err)
	}
	// Course 3 disappears -> must become inactive, not deleted.
	if err := st.SyncCourses([]model.Course{{ID: 1, Name: "Math"}, {ID: 2, Name: "Phys"}}); err != nil {
		t.Fatal(err)
	}
	courses, _ := st.LoadCourses(false)
	active, muted := map[int64]bool{}, map[int64]bool{}
	found := false
	for _, c := range courses {
		active[c.ID] = c.Active
		muted[c.ID] = c.Muted
		if c.ID == 3 {
			found = true
		}
	}
	if !found {
		t.Fatal("course 3 must be kept (not deleted)")
	}
	if !active[1] || !active[2] || active[3] {
		t.Fatalf("active = %v", active)
	}
	if muted[1] || !muted[2] {
		t.Fatalf("mute state not preserved: %v", muted)
	}
}

func TestToggleCourseMute(t *testing.T) {
	st := openTemp(t)
	if err := st.SyncCourses([]model.Course{{ID: 5, Name: "History"}}); err != nil {
		t.Fatal(err)
	}
	m1, err := st.ToggleCourseMute(5)
	if err != nil || !m1 {
		t.Fatalf("first toggle => true, got %v (%v)", m1, err)
	}
	m2, err := st.ToggleCourseMute(5)
	if err != nil || m2 {
		t.Fatalf("second toggle => false, got %v (%v)", m2, err)
	}
}

func TestSyncAssignments_ActiveAndFlagsPreserved(t *testing.T) {
	st := openTemp(t)
	due := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	if err := st.SyncAssignments([]model.Assignment{
		{CourseID: 1, ID: 10, Name: "A", DueAt: &due, NotifiedNew: true, NotifiedOverdue: true},
		{CourseID: 1, ID: 11, Name: "B"},
	}); err != nil {
		t.Fatal(err)
	}
	// Assignment 10 disappears -> inactive; 11 becomes active.
	if err := st.SyncAssignments([]model.Assignment{{CourseID: 1, ID: 11, Name: "B"}}); err != nil {
		t.Fatal(err)
	}
	byID := loadByID(t, st)
	if byID[10].Active {
		t.Fatal("assignment 10 must be inactive")
	}
	if !byID[11].Active {
		t.Fatal("assignment 11 must be active")
	}
	if !byID[10].NotifiedNew || !byID[10].NotifiedOverdue {
		t.Fatalf("flags must be preserved on inactive row: %+v", byID[10])
	}
}

func TestUpsertAssignments_NullableRoundTrip(t *testing.T) {
	st := openTemp(t)
	// Row with null points/score/due.
	row := model.Assignment{CourseID: 1, ID: 30, Name: "C", HTMLURL: "https://x/y"}
	if err := st.UpsertAssignments([]model.Assignment{row}); err != nil {
		t.Fatal(err)
	}
	got := loadByID(t, st)[30]
	if got.Points != nil || got.Score != nil || got.DueAt != nil || got.Grade != nil {
		t.Fatalf("expected nulls to round-trip, got %+v", got)
	}
	// Now with values.
	pts := 12.5
	score := 10.0
	grade := "A"
	due := time.Date(2026, 5, 1, 8, 30, 0, 0, time.UTC)
	row2 := model.Assignment{CourseID: 1, ID: 30, Name: "C", HTMLURL: "https://x/y", Points: &pts, Score: &score, Grade: &grade, DueAt: &due, NotifiedEarly: true}
	if err := st.UpsertAssignments([]model.Assignment{row2}); err != nil {
		t.Fatal(err)
	}
	got2 := loadByID(t, st)[30]
	if got2.Points == nil || *got2.Points != pts {
		t.Fatalf("points not round-tripped: %v", got2.Points)
	}
	if got2.Score == nil || *got2.Score != score {
		t.Fatalf("score not round-tripped")
	}
	if got2.Grade == nil || *got2.Grade != grade {
		t.Fatalf("grade not round-tripped")
	}
	if got2.DueAt == nil || !got2.DueAt.Equal(due) {
		t.Fatalf("due not round-tripped: %v", got2.DueAt)
	}
	if !got2.NotifiedEarly {
		t.Fatalf("NotifiedEarly should be true")
	}
}

func TestSettingsAndSeed(t *testing.T) {
	st := openTemp(t)
	if v := st.GetSetting("missing", "def"); v != "def" {
		t.Fatalf("default missing key: %q", v)
	}
	if err := st.SetSetting("missing", "val"); err != nil {
		t.Fatal(err)
	}
	if v := st.GetSetting("missing", "def"); v != "val" {
		t.Fatalf("expected val, got %q", v)
	}
	if st.GetLeadHours(24) != 24 {
		t.Fatal("default lead hours should be 24")
	}
	if err := st.SetLeadHours(6); err != nil || st.GetLeadHours(24) != 6 {
		t.Fatal("lead hours set/get failed")
	}
	if err := st.SetLeadHours(99); err != nil {
		t.Fatal(err)
	}
	if st.GetLeadHours(24) != 24 {
		t.Fatal("invalid stored lead hours must fall back to default")
	}
	if st.IsSeeded() {
		t.Fatal("must not be seeded initially")
	}
	if err := st.SetSeeded(); err != nil {
		t.Fatal(err)
	}
	if !st.IsSeeded() {
		t.Fatal("expected seeded")
	}
}

func TestSubscribersIdempotent(t *testing.T) {
	st := openTemp(t)
	_ = st.AddSubscriber(111)
	_ = st.AddSubscriber(111)
	_ = st.AddSubscriber(222)
	got := st.Subscribers()
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	if len(got) != 2 || got[0] != 111 || got[1] != 222 {
		t.Fatalf("subscribers = %v", got)
	}
}

func TestCourseNameMapAndMutes(t *testing.T) {
	st := openTemp(t)
	_ = st.SyncCourses([]model.Course{{ID: 1, Name: "Alpha"}, {ID: 2, Name: "Beta"}})
	_ = st.SetCourseMuted(2, true)
	names := st.CourseNameMap()
	if names[1] != "Alpha" || names[2] != "Beta" {
		t.Fatalf("names = %v", names)
	}
	mutes := st.LoadCourseMutes()
	if mutes[1] || !mutes[2] {
		t.Fatalf("mutes = %v", mutes)
	}
}

func loadByID(t *testing.T, st *store.Store) map[int64]model.Assignment {
	t.Helper()
	rows, err := st.LoadAssignments()
	if err != nil {
		t.Fatal(err)
	}
	m := make(map[int64]model.Assignment, len(rows))
	for _, a := range rows {
		m[a.ID] = a
	}
	return m
}
