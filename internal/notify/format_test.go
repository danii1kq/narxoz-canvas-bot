package notify_test

import (
	"strings"
	"testing"
	"time"

	"narxoz-canvas-bot/internal/model"
	"narxoz-canvas-bot/internal/notify"
)

func TestDateTimeInAlmaty(t *testing.T) {
	f := notify.NewFormatter(time.FixedZone("Asia/Almaty", 5*3600))
	utc := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	if got := f.DateTime(&utc); got != "01.01.2026 15:00" {
		t.Fatalf("DateTime = %q, want 01.01.2026 15:00 (UTC+5)", got)
	}
	if got := f.DateTime(nil); got != "—" {
		t.Fatalf("DateTime(nil) = %q", got)
	}
}

func TestFormatEventNewAssignmentNoDeadlineEscapesHTML(t *testing.T) {
	f := notify.NewFormatter(time.UTC)
	pts := 10.0
	ev := model.Event{
		Kind:   model.EventNewAssignment,
		Course: "Course & <Test>",
		A:      model.Assignment{Name: "Assignment <b>", Points: &pts, HTMLURL: "https://canvas.narxoz.kz/x?a=1&b=2"},
	}
	s := f.FormatEvent(ev)
	if !strings.Contains(s, "Новое задание") {
		t.Errorf("missing title: %s", s)
	}
	if !strings.Contains(s, "без дедлайна") {
		t.Errorf("expected 'без дедлайна': %s", s)
	}
	if !strings.Contains(s, "Assignment &lt;b&gt;") {
		t.Errorf("assignment name not HTML-escaped: %s", s)
	}
	if !strings.Contains(s, "Course &amp; &lt;Test&gt;") {
		t.Errorf("course not HTML-escaped: %s", s)
	}
	if !strings.Contains(s, "10.00") && !strings.Contains(s, "10") {
		t.Errorf("points not rendered: %s", s)
	}
}

func TestFormatEventDeadlineEarlyUsesLead(t *testing.T) {
	f := notify.NewFormatter(time.UTC)
	due := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	ev := model.Event{Kind: model.EventDeadlineEarly, Lead: 12, Course: "C", A: model.Assignment{Name: "X", DueAt: &due}}
	s := f.FormatEvent(ev)
	if !strings.Contains(s, "Дедлайн через 12 ч") {
		t.Errorf("expected 'Дедлайн через 12 ч': %s", s)
	}
}

func TestStatusEmoji(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	mk := func(d time.Duration) *time.Time {
		v := now.Add(d)
		return &v
	}
	submitted := now.Add(-time.Minute)
	cases := []struct {
		name string
		a    model.Assignment
		want string
	}{
		{"overdue-unsubmitted", model.Assignment{DueAt: mk(-time.Hour)}, "⛔"},
		{"within-24h", model.Assignment{DueAt: mk(2 * time.Hour)}, "🔴"},
		{"within-3d", model.Assignment{DueAt: mk(48 * time.Hour)}, "🟡"},
		{"far-future", model.Assignment{DueAt: mk(96 * time.Hour)}, "🟢"},
		{"submitted", model.Assignment{DueAt: mk(time.Hour), SubmittedAt: &submitted}, "✅"},
		{"no-deadline", model.Assignment{}, "⚪"},
	}
	for _, tt := range cases {
		if got := notify.StatusEmoji(tt.a, now); got != tt.want {
			t.Errorf("%s: got %q want %q", tt.name, got, tt.want)
		}
	}
}
