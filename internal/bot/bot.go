package bot

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"narxoz-canvas-bot/internal/canvas"
	"narxoz-canvas-bot/internal/model"
	"narxoz-canvas-bot/internal/notify"
	"narxoz-canvas-bot/internal/runtime"
	"narxoz-canvas-bot/internal/secrets"
	"narxoz-canvas-bot/internal/store"
)

// Syncer refreshes the Canvas dataset on demand. It is implemented by the
// poller; the bot only needs it for the "refresh now" button and /setup.
type Syncer interface {
	PollOnce(ctx context.Context) error
}

// Bot wires Telegram updates to the store and Canvas client. All functionality
// is reachable both via reply-keyboard buttons and text commands.
type Bot struct {
	tg          *notify.Client
	store       *store.Store
	canvas      *canvas.Client
	syncer      Syncer
	box         *secrets.Box
	fm          notify.Formatter
	stats       *runtime.Stats
	log         *slog.Logger
	loc         *time.Location
	defaultChat int64

	// envToken is CANVAS_API_TOKEN as read from the environment. When it is
	// non-empty it is authoritative for the background sync, and tokens
	// entered in a chat never replace it.
	envToken   string
	reqTimeout time.Duration

	pendMu sync.Mutex
	// pending tracks chats that are expected to paste a Canvas token next.
	pending map[int64]bool
}

// New constructs a Bot.
//
// box seals Canvas tokens before they are persisted; envToken is the
// CANVAS_API_TOKEN value (empty when the token comes from the chat); syncer
// performs on-demand Canvas refreshes.
func New(
	tg *notify.Client,
	st *store.Store,
	cv *canvas.Client,
	syncer Syncer,
	box *secrets.Box,
	fm notify.Formatter,
	stats *runtime.Stats,
	log *slog.Logger,
	loc *time.Location,
	defaultChat int64,
	envToken string,
	reqTimeout time.Duration,
) *Bot {
	return &Bot{
		tg:          tg,
		store:       st,
		canvas:      cv,
		syncer:      syncer,
		box:         box,
		fm:          fm,
		stats:       stats,
		log:         log,
		loc:         loc,
		defaultChat: defaultChat,
		envToken:    strings.TrimSpace(envToken),
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
func (b *Bot) onMessage(m *tgbotapi.Message) {
	if m.Text == "" {
		return
	}
	chatID := m.Chat.ID
	text := strings.TrimSpace(m.Text)

	// While this chat waits for a Canvas token, every non-command message is
	// treated as the token (/setup dialog).
	if b.isPending(chatID) && !strings.HasPrefix(text, "/") {
		b.handlePastedToken(chatID, m)
		return
	}

	// Register the chat as a subscriber so it receives notifications.
	_ = b.store.AddSubscriber(chatID)

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
		b.startSetup(chatID)
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
			b.sendWelcome(chatID)
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
			b.startSetup(chatID)
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
	if b.canvasConfigured(chatID) {
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
		b.startSetup(chatID)
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
