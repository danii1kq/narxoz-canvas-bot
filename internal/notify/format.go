package notify

import (
	"html"
	"strconv"
	"strings"
	"time"

	"narxoz-canvas-bot/internal/model"
)

// Formatter produces Russian-language text and HTML-escapes all dynamic input.
type Formatter struct {
	Loc *time.Location
}

// NewFormatter returns a Formatter that renders timestamps in loc.
func NewFormatter(loc *time.Location) Formatter {
	if loc == nil {
		loc = time.UTC
	}
	return Formatter{Loc: loc}
}

// esc HTML-escapes a string for use with parse mode HTML.
func esc(s string) string { return html.EscapeString(s) }

// DateTime renders a UTC instant in the configured location, or "—" for nil.
func (f Formatter) DateTime(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return t.In(f.Loc).Format("02.01.2006 15:04")
}

// dueText renders a due date, or the literal "без дедлайна" (no deadline).
func (f Formatter) dueText(t *time.Time) string {
	if t == nil {
		return "без дедлайна"
	}
	return f.DateTime(t)
}

func fmtFloat(f *float64) string {
	if f == nil {
		return "—"
	}
	return strconv.FormatFloat(*f, 'f', -1, 64)
}

func fmtGrade(g *string) string {
	if g == nil || *g == "" {
		return "—"
	}
	return *g
}

// link renders an HTML anchor, or an empty string when there is no URL.
func link(u string) string {
	if u == "" {
		return ""
	}
	return `<a href="` + esc(u) + `">🔗 Открыть в Canvas</a>`
}

// StatusEmoji returns the list status icon for an assignment relative to now.
func StatusEmoji(a model.Assignment, now time.Time) string {
	if a.Submitted() {
		return "✅"
	}
	if a.DueAt == nil {
		return "⚪"
	}
	remaining := a.DueAt.Sub(now)
	switch {
	case remaining < 0:
		return "⛔"
	case remaining < 24*time.Hour:
		return "🔴"
	case remaining < 72*time.Hour:
		return "🟡"
	default:
		return "🟢"
	}
}

// FormatEvent renders a notification message for a poller event.
func (f Formatter) FormatEvent(e model.Event) string {
	a := e.A
	var b strings.Builder
	switch e.Kind {
	case model.EventNewAssignment:
		b.WriteString("🆕 <b>Новое задание</b>\n\n")
		writeCourse(&b, e.Course)
		b.WriteString("📝 " + esc(a.Name) + "\n")
		b.WriteString("🎯 Баллы: " + fmtFloat(a.Points) + "\n")
		b.WriteString("⏰ Дедлайн: " + f.dueText(a.DueAt) + " (UTC+5)\n")
		if u := link(a.HTMLURL); u != "" {
			b.WriteString("\n" + u + "\n")
		}

	case model.EventNewGrade:
		b.WriteString("✅ <b>Оценка поставлена</b>\n\n")
		writeCourse(&b, e.Course)
		b.WriteString("📝 " + esc(a.Name) + "\n")
		b.WriteString("🎯 Результат: " + fmtFloat(a.Score) + " / " + fmtFloat(a.Points) + "\n")
		b.WriteString("🏅 Оценка: " + esc(fmtGrade(a.Grade)) + "\n")
		if u := link(a.HTMLURL); u != "" {
			b.WriteString("\n" + u + "\n")
		}

	case model.EventSubmitted:
		b.WriteString("📤 <b>Работа отправлена</b>\n\n")
		writeCourse(&b, e.Course)
		b.WriteString("📝 " + esc(a.Name) + "\n")
		if a.Late() {
			b.WriteString("⚠️ Сдано с опозданием\n")
		}
		b.WriteString("⏰ Отправлено: " + f.DateTime(a.SubmittedAt) + "\n")
		if u := link(a.HTMLURL); u != "" {
			b.WriteString("\n" + u + "\n")
		}

	case model.EventDeadlineEarly:
		b.WriteString("⏰ <b>Дедлайн через " + strconv.Itoa(e.Lead) + " ч</b>\n\n")
		writeCourse(&b, e.Course)
		b.WriteString("📝 " + esc(a.Name) + "\n")
		b.WriteString("🎯 Баллы: " + fmtFloat(a.Points) + "\n")
		b.WriteString("⏰ Дедлайн: " + f.dueText(a.DueAt) + "\n")
		if u := link(a.HTMLURL); u != "" {
			b.WriteString("\n" + u + "\n")
		}

	case model.EventDeadline1h:
		b.WriteString("🔥 <b>Дедлайн через час!</b>\n\n")
		writeCourse(&b, e.Course)
		b.WriteString("📝 " + esc(a.Name) + "\n")
		b.WriteString("🎯 Баллы: " + fmtFloat(a.Points) + "\n")
		b.WriteString("⏰ Дедлайн: " + f.dueText(a.DueAt) + "\n")
		if u := link(a.HTMLURL); u != "" {
			b.WriteString("\n" + u + "\n")
		}

	case model.EventOverdue:
		b.WriteString("⚠️ <b>Дедлайн прошёл, задание не сдано</b>\n\n")
		writeCourse(&b, e.Course)
		b.WriteString("📝 " + esc(a.Name) + "\n")
		b.WriteString("🎯 Баллы: " + fmtFloat(a.Points) + "\n")
		b.WriteString("⏰ Дедлайн был: " + f.dueText(a.DueAt) + "\n")
		if u := link(a.HTMLURL); u != "" {
			b.WriteString("\n" + u + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// Line renders a compact list entry for an assignment.
func (f Formatter) Line(a model.Assignment, course string, now time.Time) string {
	var b strings.Builder
	b.WriteString(StatusEmoji(a, now) + " ")
	if course != "" {
		b.WriteString(esc(course) + " · ")
	}
	b.WriteString("<b>" + esc(a.Name) + "</b>")
	b.WriteString(" (" + fmtFloat(a.Points) + " б.)\n")
	if a.DueAt != nil {
		b.WriteString("    ⏰ " + f.DateTime(a.DueAt))
	} else {
		b.WriteString("    ⏰ без дедлайна")
	}
	if a.Submitted() && a.Late() {
		b.WriteString(" · сдано с опозданием")
	}
	if a.Score != nil {
		b.WriteString("\n    🎯 " + fmtFloat(a.Score) + "/" + fmtFloat(a.Points) + " · " + esc(fmtGrade(a.Grade)))
	}
	if u := link(a.HTMLURL); u != "" {
		b.WriteString("\n    " + u)
	}
	return b.String()
}

func writeCourse(b *strings.Builder, course string) {
	if course == "" {
		return
	}
	b.WriteString("📚 Курс: <b>" + esc(course) + "</b>\n")
}
