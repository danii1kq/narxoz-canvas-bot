package bot

import (
	"context"
	"html"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"narxoz-canvas-bot/internal/canvasconn"
	"narxoz-canvas-bot/internal/model"
)

// listSpec describes a paginated assignment list to render.
type listSpec struct {
	title string // HTML-safe title
	items []model.Assignment
	page  int
	size  int
	now   time.Time
	view  string
	param int64
	back  bool // show a "back to courses" button
	empty string
	names map[int64]string
}

// renderList renders a paginated list of assignments and its inline keyboard.
func (b *Bot) renderList(s listSpec) (string, tgbotapi.InlineKeyboardMarkup) {
	page := clampPage(s.page, s.size, len(s.items))
	start, end := sliceRange(len(s.items), page, s.size)

	var sb strings.Builder
	sb.WriteString("<b>" + s.title + "</b>\n")
	sb.WriteString("Всего: " + strconv.Itoa(len(s.items)) + "\n")
	if len(s.items) == 0 {
		sb.WriteString("\n" + s.empty + "\n")
	} else {
		for i := start; i < end; i++ {
			a := s.items[i]
			sb.WriteString("\n" + b.fm.Line(a, s.names[a.CourseID], s.now) + "\n")
		}
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	if s.back {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Назад к курсам", cbPage("courses", 0, 0)),
		))
	}
	view, param := s.view, s.param
	rows = append(rows, paginationRow(func(p int) string { return cbPage(view, param, p) }, page, len(s.items), s.size))
	rows = append(rows, refreshRow(view, param, page))
	return strings.TrimRight(sb.String(), "\n"), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// renderCoursesIndex renders the interactive list of active courses.
func (b *Bot) renderCoursesIndex(page int) (string, tgbotapi.InlineKeyboardMarkup) {
	courses := b.activeCourses()
	counts := map[int64]int{}
	for _, a := range b.activeAssignments() {
		counts[a.CourseID]++
	}

	page = clampPage(page, coursesPerPage, len(courses))
	start, end := sliceRange(len(courses), page, coursesPerPage)

	var sb strings.Builder
	sb.WriteString("<b>📚 Курсы</b>\n")
	sb.WriteString("Активных курсов: " + strconv.Itoa(len(courses)) + "\n")
	if len(courses) == 0 {
		sb.WriteString("\nАктивных курсов не найдено.\n")
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	var grid []tgbotapi.InlineKeyboardButton
	for i := start; i < end; i++ {
		c := courses[i]
		label := truncate(c.Name, 22)
		if n := counts[c.ID]; n > 0 {
			label += " · " + strconv.Itoa(n)
		}
		grid = append(grid, tgbotapi.NewInlineKeyboardButtonData(label, cbPage("course", c.ID, 0)))
		if len(grid) == 2 {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(grid...))
			grid = nil
		}
	}
	if len(grid) > 0 {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(grid...))
	}
	rows = append(rows, paginationRow(func(p int) string { return cbPage("courses", 0, p) }, page, len(courses), coursesPerPage))
	rows = append(rows, refreshRow("courses", 0, page))
	return strings.TrimRight(sb.String(), "\n"), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// renderView renders one of the data views (or the courses index / settings).
func (b *Bot) renderView(chatID int64, view string, param int64, page int) (string, tgbotapi.InlineKeyboardMarkup) {
	if view == "settings" {
		return b.renderSettings(chatID)
	}
	if !b.conn.Configured(chatID) {
		return b.renderNoToken()
	}
	if view == "courses" {
		return b.renderCoursesIndex(page)
	}
	now := time.Now().UTC()
	items := b.activeAssignments()
	names := b.store.CourseNameMap()

	switch view {
	case "assign":
		return b.renderList(listSpec{
			title: "📋 Задания", items: SortUpcoming(items), page: page,
			size: assignmentsPerPage, now: now, view: "assign",
			empty: "Заданий нет.", names: names,
		})
	case "missing":
		mf := filterAssignments(items, func(a model.Assignment) bool { return IsMissing(a, now) })
		return b.renderList(listSpec{
			title: "⚠️ Не сдано", items: SortUpcoming(mf), page: page,
			size: assignmentsPerPage, now: now, view: "missing",
			empty: "Все задания с крайним сроком сданы. Отлично! 🎉", names: names,
		})
	case "overdue":
		of := filterAssignments(items, func(a model.Assignment) bool { return IsOverdue(a, now) })
		return b.renderList(listSpec{
			title: "⛔ Просрочено", items: SortUpcoming(of), page: page,
			size: assignmentsPerPage, now: now, view: "overdue",
			empty: "Просроченных заданий нет.", names: names,
		})
	case "grades":
		gf := filterAssignments(items, func(a model.Assignment) bool { return IsGraded(a) })
		sorted := SortGrades(gf)
		if len(sorted) > 10 {
			sorted = sorted[:10]
		}
		return b.renderList(listSpec{
			title: "📝 Оценки (последние 10)", items: sorted, page: page,
			size: assignmentsPerPage, now: now, view: "grades",
			empty: "Оценок пока нет.", names: names,
		})
	case "course":
		cf := filterAssignments(items, func(a model.Assignment) bool { return a.CourseID == param })
		title := "📚 Задания курса"
		if cn := names[param]; cn != "" {
			title = "📚 " + html.EscapeString(cn)
		}
		return b.renderList(listSpec{
			title: title, items: SortUpcoming(cf), page: page,
			size: assignmentsPerPage, now: now, view: "course", param: param,
			back: true, empty: "Заданий в этом курсе нет.", names: names,
		})
	}
	return "Раздел недоступен.", tgbotapi.NewInlineKeyboardMarkup()
}

// send/edit wrappers.

// appEnabled reports whether the Mini App is configured for this deployment.
// When it is, interactive lists are not sent to the chat: the chat stays a
// notification channel and the Open button leads to the app.
func (b *Bot) appEnabled() bool { return b.appURL != "" }

// appHint renders the compact "look in the app" reply with a text summary.
func (b *Bot) appHint(title string) string {
	now := time.Now().UTC()
	items := b.activeAssignments()
	var missing, overdue, graded int
	for _, a := range items {
		switch {
		case IsOverdue(a, now):
			overdue++
		case IsMissing(a, now):
			missing++
		}
		if IsGraded(a) {
			graded++
		}
	}
	var sb strings.Builder
	sb.WriteString("<b>" + title + "</b>\n\n")
	sb.WriteString("📚 Курсов: " + strconv.Itoa(len(b.activeCourses())) +
		" · 📝 Заданий: " + strconv.Itoa(len(items)) + "\n")
	sb.WriteString("⚠️ Не сдано: " + strconv.Itoa(missing) +
		" · ⛔ Просрочено: " + strconv.Itoa(overdue) +
		" · ✅ Оценок: " + strconv.Itoa(graded) + "\n\n")
	sb.WriteString("📱 Список — в приложении: кнопка <b>Open</b> у поля ввода.")
	return sb.String()
}

// viewTitle maps a view id to its human title.
func viewTitle(view string) string {
	switch view {
	case "assign":
		return "📋 Задания"
	case "missing":
		return "⚠️ Не сдано"
	case "overdue":
		return "⛔ Просрочено"
	case "grades":
		return "📝 Оценки"
	case "courses":
		return "📚 Курсы"
	case "course":
		return "📚 Задания курса"
	case "settings":
		return "⚙️ Настройки"
	default:
		return "📊 Данные"
	}
}

func (b *Bot) sendView(chatID int64, view string, param int64, page int) {
	if b.appEnabled() {
		b.tg.SendHTML(chatID, b.appHint(viewTitle(view)))
		return
	}
	text, kb := b.renderView(chatID, view, param, page)
	_ = b.tg.SendInline(chatID, text, kb)
}

func (b *Bot) editView(chatID int64, msgID int, view string, param int64, page int) {
	text, kb := b.renderView(chatID, view, param, page)
	_ = b.tg.EditInline(chatID, msgID, text, kb)
}

func (b *Bot) sendCourses(chatID int64) {
	if b.appEnabled() {
		b.tg.SendHTML(chatID, b.appHint(viewTitle("courses")))
		return
	}
	text, kb := b.renderView(chatID, "courses", 0, 0)
	_ = b.tg.SendInline(chatID, text, kb)
}

// renderSettings builds the settings menu: Canvas connection, per-course mute
// toggles, reminder interval cycle and the Canvas connectivity check.
func (b *Bot) renderSettings(chatID int64) (string, tgbotapi.InlineKeyboardMarkup) {
	courses := b.activeCourses()
	lead := b.store.GetLeadHours(24)

	var sb strings.Builder
	sb.WriteString("<b>⚙️ Настройки</b>\n\n")
	sb.WriteString("🔑 Canvas API: " + b.conn.StateLine(chatID) + "\n\n")
	sb.WriteString("Нажмите курс, чтобы включить/выключить уведомления по нему (🔔/🔕).\n")

	var rows [][]tgbotapi.InlineKeyboardButton
	if len(courses) == 0 {
		sb.WriteString("\nАктивных курсов нет.\n")
	}
	for _, c := range courses {
		icon := "🔔"
		if c.Muted {
			icon = "🔕"
		}
		label := icon + " " + truncate(c.Name, 30)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(label, cbMute(c.ID))))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("⏰ Напоминание: за "+strconv.Itoa(lead)+" ч", cbInterval),
	))
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🧪 Проверить связь", cbPing),
	))
	rows = append(rows, refreshRow("settings", 0, 0))
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔑 Подключить / заменить Canvas API", cbToken),
	))
	return strings.TrimRight(sb.String(), "\n"), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// doInterval cycles the reminder lead time 24 -> 12 -> 6 -> 24.
func (b *Bot) doInterval(chatID int64, msgID int, cbID string) {
	cur := b.store.GetLeadHours(24)
	var next int
	switch cur {
	case 24:
		next = 12
	case 12:
		next = 6
	default:
		next = 24
	}
	if err := b.store.SetLeadHours(next); err != nil {
		b.tg.AnswerCallback(cbID, "Не удалось сохранить настройку", true)
		return
	}
	text, kb := b.renderSettings(chatID)
	_ = b.tg.EditInline(chatID, msgID, text, kb)
	b.tg.AnswerCallback(cbID, "⏰ Напоминание будет за "+strconv.Itoa(next)+" ч", false)
}

// doToggleMute flips the mute flag for a course and re-renders the settings.
func (b *Bot) doToggleMute(chatID int64, msgID int, courseID int64, cbID string) {
	muted, err := b.store.ToggleCourseMute(courseID)
	if err != nil {
		b.tg.AnswerCallback(cbID, "Не удалось изменить настройку курса", true)
		return
	}
	text, kb := b.renderSettings(chatID)
	_ = b.tg.EditInline(chatID, msgID, text, kb)
	if muted {
		b.tg.AnswerCallback(cbID, "🔕 Уведомления курса выключены", false)
	} else {
		b.tg.AnswerCallback(cbID, "🔔 Уведомления курса включены", false)
	}
}

// doPing checks Canvas token validity and reports the result via an alert.
func (b *Bot) doPing(chatID int64, msgID int, cbID string) {
	ctx, cancel := context.WithTimeout(context.Background(), b.requestTimeout())
	defer cancel()
	u, err := b.conn.Client(chatID).User(ctx)
	switch {
	case err == nil:
		name := u.Name
		if name == "" {
			name = u.Login
		}
		b.stats.SetCanvasOK(true, "", time.Now().UTC())
		b.tg.AnswerCallback(cbID, "✅ Токен действителен. Пользователь: "+name, true)
	case canvasconn.IsNoToken(err):
		b.stats.SetCanvasOK(false, "no token", time.Now().UTC())
		b.tg.AnswerCallback(cbID, "🔑 Canvas не подключён. Нажмите /setup", true)
	case canvasconn.IsUnauthorized(err):
		b.stats.SetCanvasOK(false, "unauthorized", time.Now().UTC())
		b.tg.AnswerCallback(cbID, "🔴 Токен Canvas недействителен (401/403). Обновите: /setup", true)
	default:
		b.stats.SetCanvasOK(false, err.Error(), time.Now().UTC())
		b.tg.AnswerCallback(cbID, "🔴 Ошибка соединения с Canvas: "+shortErr(err), true)
	}
}

// doSync asks Canvas for fresh data and re-renders the current view in place.
func (b *Bot) doSync(chatID int64, msgID int, cbID, view string, param int64, page int) {
	if !b.conn.EnsureSyncToken(chatID) {
		b.tg.AnswerCallback(cbID, "🔑 Сначала подключите Canvas API", true)
		return
	}
	b.tg.AnswerCallback(cbID, "⏳ Опрашиваю Canvas…", false)
	ctx, cancel := context.WithTimeout(context.Background(), b.syncTimeout())
	defer cancel()
	if err := b.conn.Refresh(ctx, chatID); err != nil {
		b.tg.AnswerCallback(cbID, "🔴 "+shortErr(err), true)
		return
	}
	text, kb := b.renderView(chatID, view, param, page)
	_ = b.tg.EditInline(chatID, msgID, text, kb)
	b.tg.AnswerCallback(cbID, "✅ Данные обновлены", false)
}

func (b *Bot) sendSettings(chatID int64) {
	if b.appEnabled() {
		b.tg.SendHTML(chatID, b.settingsHint(chatID))
		return
	}
	text, kb := b.renderSettings(chatID)
	_ = b.tg.SendInline(chatID, text, kb)
}

// settingsHint is the compact settings summary sent when the Mini App owns the
// interactive controls.
func (b *Bot) settingsHint(chatID int64) string {
	var muted int
	all, _ := b.store.LoadCourses(false)
	for _, c := range all {
		if c.Active && c.Muted {
			muted++
		}
	}
	var sb strings.Builder
	sb.WriteString("<b>⚙️ Настройки</b>\n\n")
	sb.WriteString("🔑 Canvas API: " + b.conn.StateLine(chatID) + "\n")
	sb.WriteString("⏰ Напоминание: за " + strconv.Itoa(b.store.GetLeadHours(24)) + " ч\n")
	sb.WriteString("🔕 Курсов без уведомлений: " + strconv.Itoa(muted) + "\n\n")
	sb.WriteString("📱 Мьюты, интервал и подключение Canvas — в приложении: кнопка <b>Open</b>.")
	return sb.String()
}

func (b *Bot) editSettings(chatID int64, msgID int) {
	text, kb := b.renderSettings(chatID)
	_ = b.tg.EditInline(chatID, msgID, text, kb)
}

// searchCourse finds courses (and, as a fallback, assignments) by substring.
func (b *Bot) searchCourse(chatID int64, query string) {
	if !b.conn.Configured(chatID) {
		b.promptSetup(chatID)
		return
	}
	q := strings.ToLower(strings.TrimSpace(query))
	courses := b.activeCourses()
	var matches []model.Course
	for _, c := range courses {
		if strings.Contains(strings.ToLower(c.Name), q) || strings.Contains(strings.ToLower(c.Code), q) {
			matches = append(matches, c)
		}
	}

	switch {
	case len(matches) == 1:
		if b.appEnabled() {
			b.tg.SendHTML(chatID, "📚 Найден курс: <b>"+html.EscapeString(matches[0].Name)+"</b>\n\n"+
				"Задания курса — в приложении: кнопка <b>Open</b>.")
			return
		}
		text, kb := b.renderView(chatID, "course", matches[0].ID, 0)
		_ = b.tg.SendInline(chatID, text, kb)
	case len(matches) > 1:
		if b.appEnabled() {
			var sb strings.Builder
			sb.WriteString("🔎 <b>Найдено курсов:</b> " + strconv.Itoa(len(matches)) + "\n")
			for _, c := range matches {
				sb.WriteString("• " + html.EscapeString(c.Name) + "\n")
			}
			sb.WriteString("\nЗадания курса — в приложении: кнопка <b>Open</b>.")
			b.tg.SendHTML(chatID, strings.TrimRight(sb.String(), "\n"))
			return
		}
		var rows [][]tgbotapi.InlineKeyboardButton
		for _, c := range matches {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(truncate(c.Name, 30), cbPage("course", c.ID, 0)),
			))
		}
		_ = b.tg.SendInline(chatID, "🔎 Найдено курсов: "+strconv.Itoa(len(matches)), tgbotapi.NewInlineKeyboardMarkup(rows...))
	default:
		// Fall back to searching assignment names.
		now := time.Now().UTC()
		var hits []model.Assignment
		for _, a := range b.activeAssignments() {
			if strings.Contains(strings.ToLower(a.Name), q) {
				hits = append(hits, a)
			}
		}
		if len(hits) == 0 {
			_ = b.tg.SendHTML(chatID, "Ничего не найдено по запросу «"+html.EscapeString(query)+"».")
			return
		}
		names := b.store.CourseNameMap()
		var sb strings.Builder
		sb.WriteString("🔎 <b>Найдено заданий:</b> " + strconv.Itoa(len(hits)) + "\n")
		for _, a := range SortUpcoming(hits) {
			sb.WriteString("\n" + b.fm.Line(a, names[a.CourseID], now) + "\n")
		}
		_ = b.tg.SendHTML(chatID, strings.TrimRight(sb.String(), "\n"))
	}
}

// commandMute mutes/unmutes one or more courses matched by substring.
func (b *Bot) commandMute(chatID int64, arg string, mute bool) {
	q := strings.ToLower(strings.TrimSpace(arg))
	if q == "" {
		b.tg.SendHTML(chatID, "Укажите часть названия курса: /mute математика или /unmute математика")
		return
	}
	var changed []string
	for _, c := range b.activeCourses() {
		if strings.Contains(strings.ToLower(c.Name), q) || strings.Contains(strings.ToLower(c.Code), q) {
			if err := b.store.SetCourseMuted(c.ID, mute); err == nil {
				changed = append(changed, c.Name)
			}
		}
	}
	if len(changed) == 0 {
		b.tg.SendHTML(chatID, "Курс по запросу «"+html.EscapeString(q)+"» не найден.")
		return
	}
	state := "🔔 включены"
	if mute {
		state = "🔕 выключены"
	}
	var sb strings.Builder
	sb.WriteString("Уведомления " + state + " для:\n")
	for _, n := range changed {
		sb.WriteString("• " + html.EscapeString(n) + "\n")
	}
	b.tg.SendHTML(chatID, strings.TrimRight(sb.String(), "\n"))
}

// sendStatus reports a quick summary of the current state.
func (b *Bot) sendStatus(chatID int64) {
	snap := b.stats.Snapshot()
	now := time.Now().UTC()
	asgs := b.activeAssignments()
	courses := b.activeCourses()

	var missing, overdue, graded int
	for _, a := range asgs {
		switch {
		case IsOverdue(a, now):
			overdue++
		case IsMissing(a, now):
			missing++
		}
		if IsGraded(a) {
			graded++
		}
	}

	var muted int
	allCourses, _ := b.store.LoadCourses(false)
	for _, c := range allCourses {
		if c.Active && c.Muted {
			muted++
		}
	}

	status := "✅ доступен"
	if !snap.CanvasOK {
		switch snap.CanvasErr {
		case "no token":
			status = "🔴 не подключён (см. /setup)"
		case "unauthorized":
			status = "🔴 токен отклонён (см. /setup)"
		default:
			status = "🔴 ошибка (" + html.EscapeString(snap.CanvasErr) + ")"
		}
	}
	lastPoll := "ещё не было"
	if !snap.LastPoll.IsZero() {
		lastPoll = b.fm.DateTime(&snap.LastPoll)
	}

	var sb strings.Builder
	sb.WriteString("📊 <b>Статус</b>\n\n")
	sb.WriteString("🔗 Canvas: " + status + "\n")
	sb.WriteString("🔑 Токен: " + b.conn.StateLine(chatID) + "\n")
	sb.WriteString("🕒 Последний опрос: " + lastPoll + "\n")
	sb.WriteString("📚 Активных курсов: " + strconv.Itoa(len(courses)) + " (🔕 " + strconv.Itoa(muted) + ")\n")
	sb.WriteString("📝 Отслеживается заданий: " + strconv.Itoa(len(asgs)) + "\n")
	sb.WriteString("⚠️ Не сдано: " + strconv.Itoa(missing) + "\n")
	sb.WriteString("⛔ Просрочено: " + strconv.Itoa(overdue) + "\n")
	sb.WriteString("✅ Оценок: " + strconv.Itoa(graded) + "\n")
	sb.WriteString("⏰ Напоминание: за " + strconv.Itoa(b.store.GetLeadHours(24)) + " ч\n")
	sb.WriteString("🔁 Опросов: " + strconv.Itoa(snap.PollCount) + " · уведомлений: " + strconv.Itoa(snap.Notifications) + "\n")

	b.tg.SendHTML(chatID, strings.TrimRight(sb.String(), "\n"))
}

// --- small helpers ---

func filterAssignments(items []model.Assignment, keep func(model.Assignment) bool) []model.Assignment {
	out := make([]model.Assignment, 0, len(items))
	for _, a := range items {
		if keep(a) {
			out = append(out, a)
		}
	}
	return out
}

// requestTimeout bounds a single Canvas call made from a chat handler.
func (b *Bot) requestTimeout() time.Duration {
	if b.reqTimeout > 0 {
		return b.reqTimeout
	}
	return 30 * time.Second
}

// syncTimeout bounds a full "fetch everything from Canvas" refresh. It is
// generous because it walks every course and every pagination page.
func (b *Bot) syncTimeout() time.Duration {
	if b.reqTimeout > 60*time.Second {
		return b.reqTimeout * 4
	}
	return 5 * time.Minute
}

// shortErr renders an error for a Telegram callback alert (max ~200 chars).
func shortErr(err error) string {
	msg := err.Error()
	if len(msg) > 180 {
		msg = msg[:180] + "…"
	}
	return msg
}

// truncate shortens s to at most n runes, appending an ellipsis when trimmed.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:1])
	}
	return string(r[:n-1]) + "…"
}
