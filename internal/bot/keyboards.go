package bot

import (
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Reply-keyboard button labels (Russian, always visible under the input box).
const (
	btnAssignments = "📋 Задания"
	btnMissing     = "⚠️ Не сдано"
	btnGrades      = "📝 Оценки"
	btnOverdue     = "⛔ Просрочено"
	btnCourses     = "📚 Курсы"
	btnSettings    = "⚙️ Настройки"
	btnRefresh     = "🔄 Обновить"
	btnSetup       = "🔑 Canvas API"
)

// Pagination sizes.
const (
	assignmentsPerPage = 7
	coursesPerPage     = 8
)

// MainMenuKeyboard returns the persistent reply keyboard shown after /start.
func MainMenuKeyboard() tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(btnAssignments),
			tgbotapi.NewKeyboardButton(btnMissing),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(btnGrades),
			tgbotapi.NewKeyboardButton(btnOverdue),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(btnCourses),
			tgbotapi.NewKeyboardButton(btnSettings),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(btnRefresh),
			tgbotapi.NewKeyboardButton(btnSetup),
		),
	)
	kb.OneTimeKeyboard = false
	kb.ResizeKeyboard = true
	return kb
}

// --- callback data encoding (<= 64 bytes each) ---

const (
	cbNoop     = "noop"
	cbSettings = "s:open"
	cbInterval = "s:interval"
	cbPing     = "s:ping"
	cbToken    = "s:token"
	cbSync     = "s:sync"
)

func cbMute(courseID int64) string {
	return "s:mute:" + strconv.FormatInt(courseID, 10)
}

func cbPage(view string, param int64, page int) string {
	return "p:" + view + ":" + strconv.FormatInt(param, 10) + ":" + strconv.Itoa(page)
}

// cbSyncRefresh encodes a "refresh from Canvas, then show this view" action.
func cbSyncRefresh(view string, param int64, page int) string {
	return cbSync + ":" + view + ":" + strconv.FormatInt(param, 10) + ":" + strconv.Itoa(page)
}

// callback is a parsed inline-button action.
type callback struct {
	kind  string // page | settings | interval | ping | mute | token | sync | noop
	view  string
	param int64
	page  int
}

// parseCallback parses raw callback_data into a typed action.
func parseCallback(data string) (callback, bool) {
	switch data {
	case cbNoop:
		return callback{kind: "noop"}, true
	case cbSettings:
		return callback{kind: "settings"}, true
	case cbInterval:
		return callback{kind: "interval"}, true
	case cbPing:
		return callback{kind: "ping"}, true
	case cbToken:
		return callback{kind: "token"}, true
	}
	if hasPrefix(data, "s:mute:") {
		if id, err := strconv.ParseInt(trimPrefix(data, "s:mute:"), 10, 64); err == nil {
			return callback{kind: "mute", param: id}, true
		}
	}
	if hasPrefix(data, "p:") {
		view, param, page, ok := splitViewParamPage(data, "p:")
		if !ok {
			return callback{}, false
		}
		return callback{kind: "page", view: view, param: param, page: page}, true
	}
	if hasPrefix(data, cbSync+":") {
		view, param, page, ok := splitViewParamPage(data, cbSync+":")
		if !ok {
			return callback{}, false
		}
		return callback{kind: "sync", view: view, param: param, page: page}, true
	}
	return callback{}, false
}

// splitViewParamPage parses "<prefix>view:param:page" where param may be 0.
func splitViewParamPage(data, prefix string) (string, int64, int, bool) {
	rest := data[len(prefix):]
	first := indexByte(rest, ':')
	if first < 0 {
		return "", 0, 0, false
	}
	view := rest[:first]
	rest = rest[first+1:]
	second := indexByte(rest, ':')
	if second < 0 {
		return "", 0, 0, false
	}
	param := rest[:second]
	page := rest[second+1:]
	var p64 int64
	if param != "" {
		v, err := strconv.ParseInt(param, 10, 64)
		if err != nil {
			return "", 0, 0, false
		}
		p64 = v
	}
	pg, err := strconv.Atoi(page)
	if err != nil || pg < 0 {
		return "", 0, 0, false
	}
	if view == "" {
		return "", 0, 0, false
	}
	return view, p64, pg, true
}

// refreshRow builds the [🔄 Обновить из Canvas] row for a view.
func refreshRow(view string, param int64, page int) []tgbotapi.InlineKeyboardButton {
	return tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔄 Обновить из Canvas", cbSyncRefresh(view, param, page)),
	)
}

// totalPagesOf returns the number of pages for total items of the given size.
func totalPagesOf(total, size int) int {
	if size <= 0 || total <= 0 {
		return 1
	}
	return (total + size - 1) / size
}

// clampPage constrains a requested page index to the valid range.
func clampPage(page, size, total int) int {
	if total <= 0 || size <= 0 {
		return 0
	}
	maxPage := (total - 1) / size
	if page < 0 {
		return 0
	}
	if page > maxPage {
		return maxPage
	}
	return page
}

// sliceRange returns the [start, end) range for a page of n items.
func sliceRange(n, page, size int) (int, int) {
	start := 0
	if size > 0 {
		start = page * size
	}
	if start < 0 {
		start = 0
	}
	if start > n {
		start = n
	}
	end := start + size
	if end > n {
		end = n
	}
	return start, end
}

// paginationRow builds a [◀️ Назад][x/y][Вперёд ▶️] row.
func paginationRow(make func(page int) string, page, total, size int) []tgbotapi.InlineKeyboardButton {
	totalPages := totalPagesOf(total, size)
	prev := tgbotapi.NewInlineKeyboardButtonData("◀️ Назад", cbNoop)
	if page > 0 {
		prev = tgbotapi.NewInlineKeyboardButtonData("◀️ Назад", make(page-1))
	}
	counter := tgbotapi.NewInlineKeyboardButtonData(strconv.Itoa(page+1)+"/"+strconv.Itoa(totalPages), cbNoop)
	next := tgbotapi.NewInlineKeyboardButtonData("Вперёд ▶️", cbNoop)
	if page+1 < totalPages {
		next = tgbotapi.NewInlineKeyboardButtonData("Вперёд ▶️", make(page+1))
	}
	return tgbotapi.NewInlineKeyboardRow(prev, counter, next)
}

// small local helpers to avoid an extra strings import.
func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }
func trimPrefix(s, p string) string {
	if hasPrefix(s, p) {
		return s[len(p):]
	}
	return s
}
func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
