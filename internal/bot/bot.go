package bot

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"narxoz-canvas-bot/internal/canvasconn"
	"narxoz-canvas-bot/internal/model"
	"narxoz-canvas-bot/internal/notify"
	"narxoz-canvas-bot/internal/runtime"
	"narxoz-canvas-bot/internal/store"
)

// Bot wires Telegram updates to the store and the Canvas connection. All
// functionality is reachable via commands; the interactive lists live in the
// Mini App when one is configured.
type Bot struct {
	tg          *notify.Client
	store       *store.Store
	conn        *canvasconn.Conn
	fm          notify.Formatter
	stats       *runtime.Stats
	log         *slog.Logger
	loc         *time.Location
	defaultChat int64

	// appURL is the public Mini App address; empty when the app is disabled.
	appURL     string
	reqTimeout time.Duration

	pendMu sync.Mutex
	// pending tracks chats that are expected to paste a Canvas token next.
	pending map[int64]bool
}

// New constructs a Bot.
//
// conn owns the Canvas token and sync state shared with the Mini App; appURL
// is the public address of that app (empty when it is not configured).
func New(
	tg *notify.Client,
	st *store.Store,
	conn *canvasconn.Conn,
	fm notify.Formatter,
	stats *runtime.Stats,
	log *slog.Logger,
	loc *time.Location,
	defaultChat int64,
	appURL string,
	reqTimeout time.Duration,
) *Bot {
	return &Bot{
		tg:          tg,
		store:       st,
		conn:        conn,
		fm:          fm,
		stats:       stats,
		log:         log,
		loc:         loc,
		defaultChat: defaultChat,
		appURL:      strings.TrimRight(strings.TrimSpace(appURL), "/"),
		reqTimeout:  reqTimeout,
		pending:     map[int64]bool{},
	}
}

// Run starts the long-polling update loop until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) {
	u := tgbotapi.UpdateConfig{
		Offset:         0,
		Timeout:        30,
		AllowedUpdates: []string{"message", "callback_query"},
	}
	updates := b.tg.API().GetUpdatesChan(u)
	defer b.tg.API().StopReceivingUpdates()
	b.log.Info("telegram bot is listening for updates", "username", b.tg.API().Self.UserName)
	for {
		select {
		case <-ctx.Done():
			return
		case upd, ok := <-updates:
			if !ok {
				return
			}
			b.safeHandle(upd)
		}
	}
}

// safeHandle runs a single update handler with panic recovery so one bad
// update can never crash the process.
func (b *Bot) safeHandle(u tgbotapi.Update) {
	defer func() {
		if r := recover(); r != nil {
			b.log.Error("recovered from panic handling update", "update_id", u.UpdateID, "recover", r)
		}
	}()
	b.handle(u)
}

func (b *Bot) handle(u tgbotapi.Update) {
	if u.CallbackQuery != nil {
		b.onCallback(u.CallbackQuery)
		return
	}
	if u.Message != nil {
		b.onMessage(u.Message)
	}
}

// onMessage routes a text message (reply-keyboard button or command).
// subscribe registers a chat for notifications. Only private chats are
// subscribed: personal assignments and grades must not leak into a group, and
// the subscription is not created by an arbitrary message.
func (b *Bot) subscribe(chatID int64, private bool) {
	if !private {
		return
	}
	if err := b.store.AddSubscriber(chatID); err != nil {
		b.log.Error("could not register the subscriber", "chat", chatID, "err", err)
	}
}

// groupNotice explains what the bot does (and does not do) in a group chat.
const groupNotice = "ℹ️ Уведомления о заданиях и оценках бот присылает только в личный чат. " +
	"Здесь бот отвечает на команды, но сообщения о дедлайнах не публикует."

// sendHTML sends a message and logs a failure instead of dropping it, so a
// delivery problem shows up in the logs.
func (b *Bot) sendHTML(chatID int64, text string) {
	if err := b.tg.SendHTML(chatID, text); err != nil {
		b.log.Error("send message failed", "chat", chatID, "err", err)
	}
}

// sendInline sends a message with an inline keyboard and logs a failure.
func (b *Bot) sendInline(chatID int64, text string, kb tgbotapi.InlineKeyboardMarkup) {
	if err := b.tg.SendInline(chatID, text, kb); err != nil {
		b.log.Error("send message failed", "chat", chatID, "err", err)
	}
}

// editInline edits a message and logs a failure instead of dropping it.
func (b *Bot) editInline(chatID int64, msgID int, text string, kb tgbotapi.InlineKeyboardMarkup) {
	if err := b.tg.EditInline(chatID, msgID, text, kb); err != nil {
		b.log.Error("edit message failed", "chat", chatID, "message", msgID, "err", err)
	}
}

func (b *Bot) onMessage(m *tgbotapi.Message) {
	if m.Text == "" {
		return
	}
	chatID := m.Chat.ID
	text := strings.TrimSpace(m.Text)
	private := m.Chat.Type == "private"

	// While this chat waits for a Canvas token, every non-command message is
	// treated as the token (/setup dialog). Only a private chat qualifies:
	// in a group the bot must not read and delete somebody else's message.
	if private && b.isPending(chatID) && !strings.HasPrefix(text, "/") {
		b.handlePastedToken(chatID, m)
		return
	}

	// Reply-keyboard buttons (also accept the equivalent text).
	switch text {
	case btnAssignments:
		b.sendView(chatID, "assign", 0, 0)
		return
	case btnMissing:
		b.sendView(chatID, "missing", 0, 0)
		return
	case btnOverdue:
		b.sendView(chatID, "overdue", 0, 0)
		return
	case btnGrades:
		b.sendView(chatID, "grades", 0, 0)
		return
	case btnCourses:
		b.sendCourses(chatID)
		return
	case btnSettings:
		b.sendSettings(chatID)
		return
	case btnRefresh:
		b.commandRefresh(chatID)
		return
	case btnSetup:
		b.startSetup(chatID, private)
		return
	}

	if strings.HasPrefix(text, "/") {
		fields := strings.Fields(text)
		cmd := fields[0]
		// Trim any "@BotName" suffix from the command.
		if at := strings.IndexByte(cmd, '@'); at >= 0 {
			cmd = cmd[:at]
		}
		arg := strings.TrimSpace(strings.TrimPrefix(text, fields[0]))
		switch cmd {
		case "/start", "/menu":
			b.subscribe(chatID, private)
			b.sendWelcome(chatID)
			if !private {
				b.tg.SendHTML(chatID, groupNotice)
			}
		case "/help":
			b.sendHelp(chatID)
		case "/assignments":
			b.sendView(chatID, "assign", 0, 0)
		case "/missing":
			b.sendView(chatID, "missing", 0, 0)
		case "/overdue":
			b.sendView(chatID, "overdue", 0, 0)
		case "/grades":
			b.sendView(chatID, "grades", 0, 0)
		case "/course", "/courses":
			if arg == "" {
				b.sendCourses(chatID)
			} else {
				b.searchCourse(chatID, arg)
			}
		case "/settings":
			b.sendSettings(chatID)
		case "/status":
			b.sendStatus(chatID)
		case "/refresh":
			b.commandRefresh(chatID)
		case "/setup":
			b.startSetup(chatID, private)
		case "/canvas":
			b.sendCanvasStatus(chatID)
		case "/cancel":
			b.cancelSetup(chatID)
		case "/disconnect":
			b.disconnectCanvas(chatID)
		case "/mute":
			b.commandMute(chatID, arg, true)
		case "/unmute":
			b.commandMute(chatID, arg, false)
		default:
			b.tg.SendHTML(chatID, "Неизвестная команда. Откройте /help или нажмите кнопку меню.")
		}
		return
	}

	// Unknown free-form text.
	if b.conn.Configured(chatID) {
		b.tg.SendHTML(chatID, "Нажмите кнопку меню ниже 👇 или отправьте /help.")
		return
	}
	b.promptSetup(chatID)
}

// onCallback routes inline button presses. It always answers the callback so
// the user never sees a stuck loading indicator.
func (b *Bot) onCallback(cq *tgbotapi.CallbackQuery) {
	cb, ok := parseCallback(cq.Data)
	if !ok {
		b.tg.AnswerCallback(cq.ID, "Команда не распознана", true)
		return
	}
	if cq.Message == nil {
		b.tg.AnswerCallback(cq.ID, "⏳ Уже обновляю...", false)
		return
	}
	chatID := cq.Message.Chat.ID
	msgID := cq.Message.MessageID

	switch cb.kind {
	case "noop":
		b.tg.AnswerCallback(cq.ID, "", false)
	case "page":
		b.tg.AnswerCallback(cq.ID, "⏳", false)
		b.editView(chatID, msgID, cb.view, cb.param, cb.page)
	case "settings":
		b.tg.AnswerCallback(cq.ID, "⏳", false)
		b.editSettings(chatID, msgID)
	case "interval":
		b.doInterval(chatID, msgID, cq.ID)
	case "mute":
		b.doToggleMute(chatID, msgID, cb.param, cq.ID)
	case "token":
		b.tg.AnswerCallback(cq.ID, "🔑", false)
		b.startSetup(chatID, cq.Message.Chat.Type == "private")
	case "sync":
		b.doSync(chatID, msgID, cq.ID, cb.view, cb.param, cb.page)
	case "ping":
		b.doPing(chatID, msgID, cq.ID)
	}
}

// --- store loaders ---

func (b *Bot) activeAssignments() []model.Assignment {
	rows, err := b.store.LoadAssignments()
	if err != nil {
		b.log.Error("load assignments", "err", err)
		return nil
	}
	out := make([]model.Assignment, 0, len(rows))
	for _, a := range rows {
		if a.Active {
			out = append(out, a)
		}
	}
	return out
}

func (b *Bot) activeCourses() []model.Course {
	rows, err := b.store.LoadCourses(true)
	if err != nil {
		b.log.Error("load courses", "err", err)
		return nil
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name)
	})
	return rows
}
