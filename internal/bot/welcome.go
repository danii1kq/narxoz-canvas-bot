package bot

const helpText = `👋 <b>Привет!</b>

Я слежу за заданиями и оценами в Canvas Нархоз и присылаю уведомления: о новых заданиях, поставленных оценках, отправленных работах и приближающихся дедлайнах.

Доступно через кнопки меню ниже 👇 или команды:

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

// sendWelcome sends the greeting and shows the persistent main menu.
func (b *Bot) sendWelcome(chatID int64) {
	b.tg.SendMenu(chatID, helpText, MainMenuKeyboard())
	if !b.canvasConfigured(chatID) {
		b.promptSetup(chatID)
	}
}

// sendHelp sends the help text.
func (b *Bot) sendHelp(chatID int64) {
	b.tg.SendHTML(chatID, helpText)
}
