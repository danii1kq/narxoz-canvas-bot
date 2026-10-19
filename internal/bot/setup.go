package bot

import (
	"context"
	"errors"
	"html"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"narxoz-canvas-bot/internal/canvasconn"
	"narxoz-canvas-bot/internal/model"
	"narxoz-canvas-bot/internal/store"
)

// setupPrompt explains how to obtain a Canvas access token and what to do
// with it. It is shown when the user asks for /setup or presses the button.
const setupPrompt = "🔑 <b>Подключение Canvas API</b>\n\n" +
	"1. Откройте https://canvas.narxoz.kz и войдите под своим логином.\n" +
	"2. Аватар → <b>Account</b> → <b>Settings</b>.\n" +
	"3. Прокрутите до <b>Approved Integrations</b> → <b>New Access Token</b>.\n" +
	"4. Укажите назначение (например <code>tg-bot</code>) и нажмите <b>Generate Token</b>.\n" +
	"5. Скопируйте токен и отправьте его <b>одним сообщением</b> в этот чат.\n\n" +
	"⚠️ Canvas показывает токен только один раз. Сообщение с токеном будет удалено сразу после проверки.\n\n" +
	"Отмена — /cancel"

// minTokenLen is a sanity bound on a pasted token before calling Canvas.
const minTokenLen = 10

// startSetup asks the chat to paste its Canvas API token. The token dialog
// only works in a private chat: in a group the bot would read and delete
// messages of other people.
func (b *Bot) startSetup(chatID int64, private bool) {
	if !private {
		b.tg.SendHTML(chatID, "🔑 Токен Canvas можно подключить только в личном чате с ботом: "+
			"в группе бот не должен читать и удалять чужие сообщения.\n\n"+
			"Откройте бота в личке и отправьте /setup ещё раз.")
		return
	}
	b.setPending(chatID, true)
	if tok, row, ok := b.conn.ChatToken(chatID); ok {
		b.tg.SendHTML(chatID, "🔁 Сейчас подключён токен <code>"+html.EscapeString(maskToken(tok))+
			"</code> ("+html.EscapeString(canvasUserLabel(chatTokenUser(row)))+").\n\n"+setupPrompt+
			"\n\nОтправьте новый токен, чтобы заменить текущий.")
		return
	}
	b.tg.SendHTML(chatID, setupPrompt)
}

// cancelSetup leaves the waiting-for-token state.
func (b *Bot) cancelSetup(chatID int64) {
	if !b.isPending(chatID) {
		b.tg.SendHTML(chatID, "Сейчас нечего отменять.")
		return
	}
	b.setPending(chatID, false)
	b.tg.SendHTML(chatID, "❌ Подключение отменено. Повторить — /setup")
}

// promptSetup nudges a chat that has no Canvas token yet.
func (b *Bot) promptSetup(chatID int64) {
	b.tg.SendHTML(chatID, "🔑 <b>Canvas API не подключён.</b>\n\n"+
		"Отправьте /setup — бот попросит вставить токен доступа Canvas, проверит его "+
		"и сразу покажет курсы, задания и оценки.")
}

// handlePastedToken validates a token pasted as a plain message and, when
// Canvas accepts it, stores it for the chat.
func (b *Bot) handlePastedToken(chatID int64, m *tgbotapi.Message) {
	token := cleanToken(m.Text)
	if !plausibleToken(token) {
		b.tg.SendHTML(chatID, "⚠️ Это не похоже на токен Canvas.\n\n"+
			"Токен — одна длинная строка без пробелов (её выдаёт Canvas в "+
			"Account → Settings → Approved Integrations). Отправьте токен ещё раз или /cancel.")
		return
	}

	b.tg.SendHTML(chatID, "⏳ Проверяю токен в Canvas…")

	ctx, cancel := context.WithTimeout(context.Background(), b.requestTimeout())
	defer cancel()
	u, err := b.conn.Probe(ctx, token)

	// The message carried a secret: erase it as soon as it has been read.
	if derr := b.tg.DeleteMessage(chatID, m.MessageID); derr != nil {
		b.log.Warn("could not delete token message", "chat", chatID, "err", derr)
	}

	switch {
	case err == nil:
		if perr := b.conn.Persist(chatID, token, u, true, true); perr != nil {
			b.log.Error("save canvas token failed", "chat", chatID, "err", perr)
			b.tg.SendHTML(chatID, "🔴 Не удалось сохранить токен: "+html.EscapeString(perr.Error()))
			return
		}
		b.finishSetup(chatID, token, u)
	case canvasconn.IsUnauthorized(err):
		b.log.Warn("canvas token rejected", "chat", chatID)
		b.tg.SendHTML(chatID, "🔴 <b>Canvas отклонил токен (401/403).</b>\n\n"+
			"Проверьте, что:\n"+
			"• токен скопирован целиком, без кавычек и пробелов;\n"+
			"• он создан на "+html.EscapeString(b.conn.BaseURL())+";\n"+
			"• срок его действия не истёк.\n\n"+
			"Отправьте токен ещё раз или /cancel.")
	default:
		// Canvas itself is unreachable (timeout, DNS, 5xx). The token may still
		// be good, so it is stored for background verification instead of
		// asking the user to paste it again.
		b.log.Warn("canvas probe failed, storing token for background verification",
			"chat", chatID, "err", err)
		b.finishSetupUnverified(chatID, token, err)
	}
}

// finishSetup announces a verified token and pulls live data into the chat.
func (b *Bot) finishSetup(chatID int64, token string, u model.User) {
	b.setPending(chatID, false)
	b.conn.AdoptTokenIfOwner(chatID, token)

	storage := "обфусцирован в базе"
	if b.conn.Encrypted() {
		storage = "зашифрован в базе (AES-256-GCM)"
	}
	var sb strings.Builder
	sb.WriteString("✅ <b>Canvas API подключён</b>\n\n")
	sb.WriteString("👤 Пользователь: <b>" + html.EscapeString(canvasUserLabel(u)) + "</b>\n")
	if u.Login != "" {
		sb.WriteString("🆔 Логин: " + html.EscapeString(u.Login) + "\n")
	}
	if u.Email != "" {
		sb.WriteString("📧 Email: " + html.EscapeString(u.Email) + "\n")
	}
	sb.WriteString("🔐 Токен " + storage + ", сообщение с токеном удалено.\n\n")
	sb.WriteString("⏳ Загружаю курсы и задания из Canvas…")
	b.tg.SendHTML(chatID, sb.String())

	// Ask Canvas for real data right away so the chat shows live content.
	// The sync runs in the background: the bot must not freeze while Canvas
	// is being walked.
	b.runSync(chatID, func(err error) {
		if err != nil {
			if !errors.Is(err, errSyncInProgress) {
				b.reportSyncError(chatID, err)
			}
			return
		}
		b.sendView(chatID, "assign", 0, 0)
	})
}

// finishSetupUnverified stores a token that could not be checked because
// Canvas was unreachable. The background poll verifies it later.
func (b *Bot) finishSetupUnverified(chatID int64, token string, cause error) {
	if err := b.conn.Persist(chatID, token, model.User{}, false, true); err != nil {
		b.log.Error("save canvas token failed", "chat", chatID, "err", err)
		b.tg.SendHTML(chatID, "🔴 Не удалось сохранить токен в базе: "+html.EscapeString(err.Error()))
		return
	}
	b.setPending(chatID, false)

	var sb strings.Builder
	sb.WriteString("⚠️ <b>Canvas не ответил на проверку.</b>\n\n")
	sb.WriteString(probeFailureText(cause, b.requestTimeout()) + "\n\n")
	sb.WriteString("Токен <b>сохранён</b> и будет проверяться автоматически. ")
	sb.WriteString("Если окажется, что он недействителен, я сообщу — тогда пришлите новый.\n\n")
	sb.WriteString("Повторить проверку сейчас: /refresh.")
	b.tg.SendHTML(chatID, sb.String())
}

// commandRefresh triggers a live Canvas refresh from the chat. The refresh
// runs in the background, so the bot keeps answering other messages.
func (b *Bot) commandRefresh(chatID int64) {
	if !b.conn.EnsureSyncToken(chatID) {
		b.promptSetup(chatID)
		return
	}
	b.tg.SendHTML(chatID, "⏳ Опрашиваю Canvas…")
	b.runSync(chatID, func(err error) {
		switch {
		case err == nil:
			b.sendView(chatID, "assign", 0, 0)
		case errors.Is(err, errSyncInProgress):
			b.sendHTML(chatID, "⏳ Обновление уже выполняется, результат придёт в чат.")
		default:
			b.reportSyncError(chatID, err)
		}
	})
}

// sendCanvasStatus reports the Canvas connection state for this chat.
func (b *Bot) sendCanvasStatus(chatID int64) {
	snap := b.stats.Snapshot()

	var sb strings.Builder
	sb.WriteString("🔑 <b>Canvas API</b>\n\n")
	sb.WriteString("🌐 " + html.EscapeString(b.conn.BaseURL()) + "\n")
	sb.WriteString("🔑 Токен: " + b.conn.StateLine(chatID) + "\n")
	if tok, row, ok := b.conn.ChatToken(chatID); ok {
		sb.WriteString("🗝 Сохранённый токен: <code>" + html.EscapeString(maskToken(tok)) + "</code>")
		if row.Verified {
			sb.WriteString(" — подтверждён ✅\n")
		} else {
			sb.WriteString(" — ждёт проверки ⏳\n")
		}
	}
	sb.WriteString("🔗 Связь: " + canvasStatusLine(snap.CanvasOK, snap.CanvasErr) + "\n")
	lastPoll := "ещё не было"
	if !snap.LastPoll.IsZero() {
		lastPoll = b.fm.DateTime(&snap.LastPoll)
	}
	sb.WriteString("🕒 Последний опрос: " + lastPoll + "\n\n")
	sb.WriteString("📚 Активных курсов: " + strconv.Itoa(len(b.activeCourses())) + "\n")
	sb.WriteString("📝 Отслеживается заданий: " + strconv.Itoa(len(b.activeAssignments())) + "\n")

	text := strings.TrimRight(sb.String(), "\n")
	if b.appURL != "" {
		// Everything interactive lives in the Mini App now.
		_ = b.tg.SendHTML(chatID, text+"\n\n📱 Открыть приложение — кнопка <b>Open</b> у поля ввода.")
		return
	}
	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Обновить сейчас", cbSyncRefresh("assign", 0, 0)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔑 Вставить / заменить токен", cbToken),
		),
	}
	b.sendInline(chatID, text, tgbotapi.NewInlineKeyboardMarkup(rows...))
}

// disconnectCanvas drops the token stored for this chat.
func (b *Bot) disconnectCanvas(chatID int64) {
	if !b.conn.HasToken(chatID) {
		b.tg.SendHTML(chatID, "Для этого чата токен не сохранён — отключать нечего.")
		return
	}
	stopped, err := b.conn.Drop(chatID)
	if err != nil {
		b.tg.SendHTML(chatID, "🔴 Не удалось удалить токен: "+html.EscapeString(err.Error()))
		return
	}
	b.setPending(chatID, false)

	msg := "🗑 <b>Токен Canvas удалён</b> из базы и больше не используется."
	if stopped {
		msg += "\nАвтоматический опрос Canvas остановлен."
	}
	msg += "\n\nПодключить заново — /setup"
	b.tg.SendHTML(chatID, msg)
}

// reportSyncError explains a failed live refresh in chat-friendly Russian.
func (b *Bot) reportSyncError(chatID int64, err error) {
	switch {
	case canvasconn.IsNoToken(err):
		b.promptSetup(chatID)
	case canvasconn.IsUnauthorized(err):
		b.tg.SendHTML(chatID, "🔴 <b>Canvas отклонил токен (401/403).</b>\n\n"+
			"Обновите его: /setup")
	default:
		b.tg.SendHTML(chatID, "🔴 <b>Canvas не ответил.</b>\n\n"+
			probeFailureText(err, b.requestTimeout())+"\n\n"+
			"Попробовать ещё раз: /refresh")
	}
}

// probeFailureText explains why a Canvas request did not answer, with a hint.
func probeFailureText(err error, timeout time.Duration) string {
	switch {
	case canvasconn.IsProxyError(err):
		return "<code>" + html.EscapeString(err.Error()) + "</code>\n\n" +
			"Не удалось подключиться к прокси из <code>CANVAS_PROXY</code>: проверьте, что там настоящий " +
			"адрес прокси (не placeholder) и что прокси доступен с этой машины. " +
			"Если прокси не нужен — оставьте <code>CANVAS_PROXY</code> пустым и перезапустите бота."
	case canvasconn.IsTimeout(err):
		return "<code>" + html.EscapeString(err.Error()) + "</code>\n\n" +
			"Ответа не было за " + canvasconn.TimeoutText(timeout) + ". " +
			"Проверьте, что инстанс Canvas доступен из сети, где запущен бот, и не блокирует его " +
			"(при необходимости укажите <code>CANVAS_PROXY</code>)."
	default:
		return "<code>" + html.EscapeString(err.Error()) + "</code>\n\n" +
			"Проверьте, что инстанс Canvas доступен из сети, где запущен бот."
	}
}

// --- small helpers ---

// chatTokenUser maps stored token metadata to a Canvas user for display.
func chatTokenUser(row store.ChatToken) model.User {
	return model.User{Name: row.Name, Login: row.Login, Email: row.Email}
}

// renderNoToken tells the user to connect Canvas and offers the button.
func (b *Bot) renderNoToken() (string, tgbotapi.InlineKeyboardMarkup) {
	text := "🔑 <b>Canvas API не подключён</b>\n\n" +
		"Чтобы бот видел курсы, задания и оценки, нужен токен доступа Canvas.\n\n" +
		"Отправьте /setup — я попрошу вставить токен, проверю его и сразу загружу данные."
	if b.appURL != "" {
		text += "\n\n📱 Или откройте приложение (кнопка <b>Open</b>) и подключите Canvas там."
	}
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔑 Подключить Canvas API", cbToken),
		),
	)
	return text, kb
}

// canvasStatusLine renders the last known connectivity state.
func canvasStatusLine(ok bool, errMsg string) string {
	switch {
	case ok:
		return "✅ доступен"
	case errMsg == "no token":
		return "🔴 не подключён"
	case errMsg == "unauthorized":
		return "🔴 токен отклонён"
	case errMsg == "":
		return "⚪ ещё не проверяли"
	default:
		return "🔴 ошибка (" + html.EscapeString(errMsg) + ")"
	}
}

// canvasUserLabel returns a display name for a Canvas user.
func canvasUserLabel(u model.User) string {
	if name := strings.TrimSpace(u.Name); name != "" {
		return name
	}
	if login := strings.TrimSpace(u.Login); login != "" {
		return login
	}
	return "пользователь Canvas"
}

// maskToken hides the middle of a token so it can be shown safely.
func maskToken(token string) string {
	if len(token) <= 8 {
		return "••••"
	}
	return token[:4] + "…" + token[len(token)-2:]
}

// cleanToken strips copy/paste noise from a pasted token: an
// "Authorization: Bearer " style prefix, surrounding quotes, whitespace and
// trailing punctuation.
func cleanToken(raw string) string {
	s := strings.TrimSpace(raw)
	lower := strings.ToLower(s)
	for _, p := range []string{"authorization: bearer ", "bearer ", "token: ", "canvas_api_token="} {
		if strings.HasPrefix(lower, p) {
			s = strings.TrimSpace(s[len(p):])
			break
		}
	}
	// Quotes and punctuation can wrap each other (token". / "token"), so
	// trim until the value stops changing.
	for i := 0; i < 4; i++ {
		trimmed := strings.TrimSpace(strings.Trim(s, "\"'`"))
		trimmed = strings.TrimRight(trimmed, ";,.")
		trimmed = strings.TrimSpace(strings.Trim(trimmed, "\"'`"))
		if trimmed == s {
			break
		}
		s = trimmed
	}
	return s
}

// plausibleToken rejects obviously wrong input before calling Canvas.
func plausibleToken(s string) bool {
	if len(s) < minTokenLen {
		return false
	}
	// A pasted line of text, a URL or HTML markup is not a token.
	if strings.ContainsAny(s, " \t\r\n<>") {
		return false
	}
	if strings.Contains(s, "://") {
		return false
	}
	return true
}

// --- pending dialog state ---

// setPending marks (or clears) a chat as waiting for a Canvas token.
func (b *Bot) setPending(chatID int64, pending bool) {
	b.pendMu.Lock()
	defer b.pendMu.Unlock()
	if b.pending == nil {
		b.pending = map[int64]bool{}
	}
	if pending {
		b.pending[chatID] = true
		return
	}
	delete(b.pending, chatID)
}

// isPending reports whether the chat is waiting to paste a Canvas token.
func (b *Bot) isPending(chatID int64) bool {
	b.pendMu.Lock()
	defer b.pendMu.Unlock()
	return b.pending[chatID]
}
