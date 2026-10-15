package bot

import (
	"context"
	"errors"
	"html"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"narxoz-canvas-bot/internal/canvas"
	"narxoz-canvas-bot/internal/model"
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

// startSetup asks the chat to paste its Canvas API token.
func (b *Bot) startSetup(chatID int64) {
	b.setPending(chatID, true)
	if tok, u, ok := b.chatToken(chatID); ok {
		b.tg.SendHTML(chatID, "🔁 Сейчас подключён токен <code>"+html.EscapeString(maskToken(tok))+
			"</code> ("+html.EscapeString(canvasUserLabel(u))+").\n\n"+setupPrompt+
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
	u, err := b.canvas.WithToken(token).User(ctx)

	// The message carried a secret: erase it as soon as it has been read.
	if derr := b.tg.DeleteMessage(chatID, m.MessageID); derr != nil {
		b.log.Warn("could not delete token message", "chat", chatID, "err", derr)
	}

	switch {
	case err == nil:
		b.finishSetup(chatID, token, u)
	case errors.Is(err, canvas.ErrUnauthorized):
		b.log.Warn("canvas token rejected", "chat", chatID)
		b.tg.SendHTML(chatID, "🔴 <b>Canvas отклонил токен (401/403).</b>\n\n"+
			"Проверьте, что:\n"+
			"• токен скопирован целиком, без кавычек и пробелов;\n"+
			"• он создан на "+html.EscapeString(b.canvas.BaseURL())+";\n"+
			"• срок его действия не истёк.\n\n"+
			"Отправьте токен ещё раз или /cancel.")
	default:
		b.log.Warn("canvas probe failed", "chat", chatID, "err", err)
		b.tg.SendHTML(chatID, "🔴 <b>Не удалось связаться с Canvas.</b>\n\n"+
			"<code>"+html.EscapeString(err.Error())+"</code>\n\n"+
			"Отправьте токен ещё раз или /cancel.")
	}
}

// finishSetup persists a verified token, applies it to the background sync
// when needed and pulls live data into the chat.
func (b *Bot) finishSetup(chatID int64, token string, u model.User) {
	sealed, err := b.box.Seal(token)
	if err != nil {
		b.log.Error("seal canvas token failed", "chat", chatID, "err", err)
		b.tg.SendHTML(chatID, "🔴 Не удалось надёжно сохранить токен: "+html.EscapeString(err.Error()))
		return
	}
	if err := b.store.SaveChatToken(chatID, sealed, u); err != nil {
		b.log.Error("save canvas token failed", "chat", chatID, "err", err)
		b.tg.SendHTML(chatID, "🔴 Не удалось сохранить токен в базе: "+html.EscapeString(err.Error()))
		return
	}
	b.setPending(chatID, false)

	// Without a token from .env the first chat token drives the background
	// sync (the store holds one shared dataset).
	if !b.canvas.HasToken() {
		b.canvas.SetToken(token)
	}

	storage := "обфусцирован в базе"
	if b.box.Enabled() {
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
	if !b.ensureSyncToken(chatID) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), b.syncTimeout())
	defer cancel()
	if err := b.syncer.PollOnce(ctx); err != nil {
		b.reportSyncError(chatID, err)
		return
	}
	b.sendView(chatID, "assign", 0, 0)
}

// commandRefresh triggers a live Canvas refresh from the chat.
func (b *Bot) commandRefresh(chatID int64) {
	if !b.ensureSyncToken(chatID) {
		b.promptSetup(chatID)
		return
	}
	b.tg.SendHTML(chatID, "⏳ Опрашиваю Canvas…")
	ctx, cancel := context.WithTimeout(context.Background(), b.syncTimeout())
	defer cancel()
	if err := b.syncer.PollOnce(ctx); err != nil {
		b.reportSyncError(chatID, err)
		return
	}
	b.sendView(chatID, "assign", 0, 0)
}

// sendCanvasStatus reports the Canvas connection state for this chat.
func (b *Bot) sendCanvasStatus(chatID int64) {
	snap := b.stats.Snapshot()

	var sb strings.Builder
	sb.WriteString("🔑 <b>Canvas API</b>\n\n")
	sb.WriteString("🌐 " + html.EscapeString(b.canvas.BaseURL()) + "\n")
	sb.WriteString("🔑 Токен: " + b.canvasStateLine() + "\n")
	if tok, _, ok := b.chatToken(chatID); ok {
		sb.WriteString("🗝 Сохранённый токен: <code>" + html.EscapeString(maskToken(tok)) + "</code>\n")
	}
	sb.WriteString("🔗 Связь: " + canvasStatusLine(snap.CanvasOK, snap.CanvasErr) + "\n")
	lastPoll := "ещё не было"
	if !snap.LastPoll.IsZero() {
		lastPoll = b.fm.DateTime(&snap.LastPoll)
	}
	sb.WriteString("🕒 Последний опрос: " + lastPoll + "\n\n")
	sb.WriteString("📚 Активных курсов: " + strconv.Itoa(len(b.activeCourses())) + "\n")
	sb.WriteString("📝 Отслеживается заданий: " + strconv.Itoa(len(b.activeAssignments())) + "\n")

	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Обновить сейчас", cbSyncRefresh("assign", 0, 0)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔑 Вставить / заменить токен", cbToken),
		),
	}
	_ = b.tg.SendInline(chatID, strings.TrimRight(sb.String(), "\n"), tgbotapi.NewInlineKeyboardMarkup(rows...))
}

// disconnectCanvas drops the token stored for this chat.
func (b *Bot) disconnectCanvas(chatID int64) {
	if _, _, ok := b.chatToken(chatID); !ok {
		b.tg.SendHTML(chatID, "Для этого чата токен не сохранён — отключать нечего.")
		return
	}
	if err := b.store.DropChatToken(chatID); err != nil {
		b.tg.SendHTML(chatID, "🔴 Не удалось удалить токен: "+html.EscapeString(err.Error()))
		return
	}
	b.setPending(chatID, false)

	// Re-point the background sync at another chat token, or stop it.
	stopped := false
	if b.envToken == "" {
		replaced := false
		if latest, ok := b.store.LatestChatToken(); ok {
			if plain, err := b.box.Open(latest.Token); err == nil {
				b.canvas.SetToken(plain)
				replaced = true
			}
		}
		if !replaced {
			b.canvas.SetToken("")
			stopped = true
		}
	}

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
	case errors.Is(err, canvas.ErrNoToken):
		b.promptSetup(chatID)
	case errors.Is(err, canvas.ErrUnauthorized):
		b.tg.SendHTML(chatID, "🔴 <b>Canvas отклонил токен (401/403).</b>\n\n"+
			"Обновите его: /setup")
	default:
		b.tg.SendHTML(chatID, "🔴 <b>Canvas не ответил.</b>\n\n"+
			"<code>"+html.EscapeString(err.Error())+"</code>\n\n"+
			"Попробуйте ещё раз: 🔄 Обновить")
	}
}

// --- Canvas token helpers ---

// chatToken returns the plaintext Canvas token stored for a chat.
func (b *Bot) chatToken(chatID int64) (string, model.User, bool) {
	row, ok := b.store.ChatToken(chatID)
	if !ok {
		return "", model.User{}, false
	}
	plain, err := b.box.Open(row.Token)
	if err != nil {
		b.log.Error("open stored canvas token failed", "chat", chatID, "err", err)
		return "", model.User{}, false
	}
	return plain, model.User{Name: row.Name, Login: row.Login, Email: row.Email}, true
}

// clientFor returns the Canvas client to use for an on-demand request from
// this chat: the chat's own token when it has one, the shared client otherwise.
func (b *Bot) clientFor(chatID int64) *canvas.Client {
	if tok, _, ok := b.chatToken(chatID); ok {
		return b.canvas.WithToken(tok)
	}
	return b.canvas
}

// ensureSyncToken makes sure the background sync has a token, adopting the
// chat's own token when nothing is configured yet. It reports whether Canvas
// can be queried.
func (b *Bot) ensureSyncToken(chatID int64) bool {
	if b.canvas.HasToken() {
		return true
	}
	if tok, _, ok := b.chatToken(chatID); ok {
		b.canvas.SetToken(tok)
		return true
	}
	return false
}

// canvasConfigured reports whether this chat can show Canvas data: either a
// token came from the environment or the chat connected its own.
func (b *Bot) canvasConfigured(chatID int64) bool {
	return b.canvas.HasToken() || b.chatHasToken(chatID)
}

// chatHasToken reports whether a readable token is stored for the chat.
func (b *Bot) chatHasToken(chatID int64) bool {
	_, _, ok := b.chatToken(chatID)
	return ok
}

// canvasStateLine renders the Canvas token source for menus and /status.
func (b *Bot) canvasStateLine() string {
	switch {
	case b.envToken != "":
		return "из .env (CANVAS_API_TOKEN)"
	case b.canvas.HasToken():
		return "подключён в чате (/setup)"
	default:
		return "не подключён — нажмите /setup"
	}
}

// renderNoToken tells the user to connect Canvas and offers the button.
func (b *Bot) renderNoToken() (string, tgbotapi.InlineKeyboardMarkup) {
	text := "🔑 <b>Canvas API не подключён</b>\n\n" +
		"Чтобы бот видел курсы, задания и оценки, нужен токен доступа Canvas.\n\n" +
		"Нажмите кнопку ниже или отправьте /setup — я попрошу вставить токен, " +
		"проверю его и сразу загружу данные."
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
