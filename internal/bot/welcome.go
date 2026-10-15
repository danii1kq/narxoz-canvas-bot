package bot

// helpText is the full command reference. Without the Mini App it is also the
// welcome message (shown with the reply keyboard); with the app it is sent by
// /help only, because the chat stays a notification channel.
const helpText = `👋 <b>Привет!</b>

Я слежу за заданиями и оценами в Canvas Нархоз и присылаю уведомления: о новых заданиях, поставленных оценках, отправленных работах и приближающихся дедлайнах.

Команды:

📋 /assignments — все задания по ближайшему дедлайну
⚠️ /missing — не сданные задания
⛔ /overdue — просроченные задания
📝 /grades — последние оценки
📚 /course &lt;название&gt; — задания конкретного курса
⚙️ /settings — настройки (уведомления, интервал)
📊 /status — краткая сводка
🔄 /refresh — опросить Canvas прямо сейчас
🔑 /setup — подключить Canvas API (вставить токен)
🔍 /canvas — состояние подключения к Canvas
❌ /disconnect — удалить сохранённый токен
❌ /cancel — отменить ввод токена
/mute &lt;курс&gt; — выключить уведомления курса
/unmute &lt;курс&gt; — включить обратно
/help — эта справка`

// appWelcomeText is the greeting used when the Mini App is configured: the
// chat is only a notification channel, everything interactive lives in the app.
const appWelcomeText = `👋 <b>Привет!</b>

Я присылаю сюда уведомления Canvas Нархоз: новые задания, оценки, отправленные работы и дедлайны. Больше в этом чате ничего не будет — ни кнопок, ни меню.

📱 Списки, оценки и настройки — в приложении: кнопка <b>Open</b> у поля ввода.
🔑 Canvas можно подключить там же или командой /setup.
📊 /status — краткая сводка · /help — все команды`

// sendWelcome prepares the chat: with the Mini App it sends a short greeting,
// without it the command reference and the persistent reply keyboard.
func (b *Bot) sendWelcome(chatID int64) {
	if b.appEnabled() {
		b.tg.SendHTML(chatID, appWelcomeText)
	} else {
		b.tg.SendMenu(chatID, helpText, MainMenuKeyboard())
	}
	if !b.conn.Configured(chatID) {
		b.promptSetup(chatID)
	}
}

// sendHelp sends the help text.
func (b *Bot) sendHelp(chatID int64) {
	b.tg.SendHTML(chatID, helpText)
}
