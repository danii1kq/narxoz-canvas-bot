package poller

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"narxoz-canvas-bot/internal/canvas"
	"narxoz-canvas-bot/internal/config"
	"narxoz-canvas-bot/internal/model"
	"narxoz-canvas-bot/internal/notify"
	"narxoz-canvas-bot/internal/runtime"
	"narxoz-canvas-bot/internal/store"
)

// Poller runs two independent jobs: the main data poll (every PollInterval)
// and the deadline-reminder job (every ReminderInterval).
type Poller struct {
	canvas *canvas.Client
	store  *store.Store
	tg     *notify.Client
	fm     notify.Formatter
	stats  *runtime.Stats
	log    *slog.Logger
	cfg    config.Config

	mu        sync.Mutex // serialises the load->diff->save critical sections
	seeded    bool
	alertSent bool // "no token / bad token" notice already sent
}

// New constructs a Poller.
func New(cfg config.Config, cv *canvas.Client, st *store.Store, tg *notify.Client, fm notify.Formatter, stats *runtime.Stats, log *slog.Logger) *Poller {
	return &Poller{
		canvas: cv,
		store:  st,
		tg:     tg,
		fm:     fm,
		stats:  stats,
		log:    log,
		cfg:    cfg,
		seeded: st.IsSeeded(),
	}
}

// Run starts both jobs and blocks until ctx is cancelled, waiting for any
// in-flight iteration to finish first.
func (p *Poller) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(2)
	go p.loop(ctx, p.cfg.PollInterval, p.PollOnce, &wg)
	go p.loop(ctx, p.cfg.ReminderInterval, p.ReminderTick, &wg)
	wg.Wait()
}

func (p *Poller) loop(ctx context.Context, interval time.Duration, job func(context.Context) error, wg *sync.WaitGroup) {
	defer wg.Done()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		p.guarded(ctx, job)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// guarded runs a single job iteration, recovering from panics so one failing
// job can never crash the process.
func (p *Poller) guarded(ctx context.Context, job func(context.Context) error) {
	defer func() {
		if r := recover(); r != nil {
			p.log.Error("recovered from panic in poller job", "recover", r)
		}
	}()
	if err := job(ctx); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil {
			return
		}
		p.log.Error("poller job error", "err", err)
	}
}

// targets returns the set of chat ids notifications are delivered to: the
// configured TELEGRAM_CHAT_ID (if set) plus every registered subscriber.
func (p *Poller) targets() []int64 {
	seen := make(map[int64]bool)
	var out []int64
	add := func(id int64) {
		if id != 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	add(p.cfg.TelegramChatID)
	for _, id := range p.store.Subscribers() {
		add(id)
	}
	return out
}

// PollOnce fetches courses and their assignments, diffs against the store,
// persists the new state and dispatches realtime notifications. On the very
// first successful poll (seeding), state is stored but nothing is sent, to
// avoid a flood of "new assignment" notifications for pre-existing work.
func (p *Poller) PollOnce(ctx context.Context) error {
	courses, err := p.canvas.Courses(ctx)
	if err != nil {
		return p.handleCanvasError(err)
	}

	var current []model.Assignment
	for _, c := range courses {
		asgs, aerr := p.canvas.Assignments(ctx, c.ID)
		if aerr != nil {
			p.log.Error("fetch assignments failed", "course", c.ID, "err", aerr)
			if errors.Is(aerr, canvas.ErrNoToken) {
				p.sendCanvasAlert(formatCanvasAlert("no token"))
			} else if errors.Is(aerr, canvas.ErrUnauthorized) {
				p.sendCanvasAlert(formatCanvasAlert("unauthorized"))
			}
			continue
		}
		for i := range asgs {
			asgs[i].CourseID = c.ID
		}
		current = append(current, asgs...)
	}

	now := time.Now().UTC()
	p.stats.SetCanvasOK(true, "", now)
	p.mu.Lock()
	p.alertSent = false
	p.mu.Unlock()
	if p.cfg.CanvasToken == "" {
		// The most recently connected chat token drives this sync, so a
		// successful poll proves it works (it may have been stored while
		// Canvas was unreachable).
		if err := p.store.SetLatestChatTokenVerified(true); err != nil {
			p.log.Warn("could not mark chat token verified", "err", err)
		}
	}

	events, next, muted, isSeed, perr := p.computeAndPersist(current, courses, now)
	if perr != nil {
		return perr
	}

	p.stats.SetLastPoll(now)
	p.stats.IncPoll()

	if isSeed {
		p.log.Info("seeded initial state; no notifications sent", "courses", len(courses), "assignments", len(next))
		return nil
	}
	p.dispatch(events, courses, muted)
	return nil
}

// computeAndPersist runs the diff and persists inside the write lock.
func (p *Poller) computeAndPersist(current []model.Assignment, courses []model.Course, now time.Time) ([]model.Event, []model.Assignment, map[int64]bool, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	prev, err := p.store.LoadAssignments()
	if err != nil {
		return nil, nil, nil, false, err
	}
	isSeed := !p.seeded
	events, next := DetectRealtime(prev, current, now)

	if err := p.store.SyncAssignments(next); err != nil {
		return nil, nil, nil, false, err
	}
	if err := p.store.SyncCourses(courses); err != nil {
		return nil, nil, nil, false, err
	}
	if isSeed {
		p.seeded = true
		_ = p.store.SetSeeded()
	}
	muted := p.store.LoadCourseMutes()
	return events, next, muted, isSeed, nil
}

// dispatch sends ready events to every target, skipping muted courses.
func (p *Poller) dispatch(events []model.Event, courses []model.Course, muted map[int64]bool) {
	if len(events) == 0 {
		return
	}
	names := make(map[int64]string, len(courses))
	for _, c := range courses {
		names[c.ID] = c.Name
	}
	targets := p.targets()
	sent := 0
	for _, ev := range events {
		if muted[ev.A.CourseID] {
			continue
		}
		ev.Course = names[ev.A.CourseID]
		text := p.fm.FormatEvent(ev)
		for _, chat := range targets {
			if err := p.tg.SendHTML(chat, text); err != nil {
				p.log.Error("send notification failed", "chat", chat, "kind", ev.Kind, "err", err)
			} else {
				sent++
			}
		}
	}
	p.stats.AddNotifications(sent)
}

// ReminderTick fires deadline and overdue reminders for unsubmitted
// assignments, using state persisted in the store.
func (p *Poller) ReminderTick(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	lead := time.Duration(p.store.GetLeadHours(p.cfg.ReminderLead)) * time.Hour
	now := time.Now().UTC()

	events, muted, names, err := p.computeReminders(now, lead)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	targets := p.targets()
	sent := 0
	for _, ev := range events {
		if muted[ev.A.CourseID] {
			continue
		}
		ev.Course = names[ev.A.CourseID]
		text := p.fm.FormatEvent(ev)
		for _, chat := range targets {
			if err := p.tg.SendHTML(chat, text); err != nil {
				p.log.Error("send reminder failed", "chat", chat, "kind", ev.Kind, "err", err)
			} else {
				sent++
			}
		}
	}
	p.stats.AddNotifications(sent)
	return nil
}

func (p *Poller) computeReminders(now time.Time, lead time.Duration) ([]model.Event, map[int64]bool, map[int64]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	state, err := p.store.LoadAssignments()
	if err != nil {
		return nil, nil, nil, err
	}
	events, updated := DetectReminders(state, now, lead)
	if len(events) > 0 {
		if err := p.store.UpsertAssignments(updated); err != nil {
			return nil, nil, nil, err
		}
	}
	return events, p.store.LoadCourseMutes(), p.store.CourseNameMap(), nil
}

// handleCanvasError classifies a Canvas error, updates stats and triggers the
// one-off notices for "no token configured" and "token rejected".
func (p *Poller) handleCanvasError(err error) error {
	switch {
	case errors.Is(err, canvas.ErrNoToken):
		p.sendCanvasAlert(formatCanvasAlert("no token"))
	case errors.Is(err, canvas.ErrUnauthorized):
		p.sendCanvasAlert(formatCanvasAlert("unauthorized"))
	default:
		p.stats.SetCanvasOK(false, err.Error(), time.Now().UTC())
	}
	return err
}

// notify sends the "Canvas is not usable" message at most once until a poll
// succeeds again (no spam).
func (p *Poller) sendCanvasAlert(alert canvasAlert) {
	p.mu.Lock()
	already := p.alertSent
	p.alertSent = true
	p.mu.Unlock()

	p.stats.SetCanvasOK(false, alert.reason, time.Now().UTC())
	if already {
		return
	}
	for _, chat := range p.targets() {
		if err := p.tg.SendHTML(chat, alert.text); err != nil {
			p.log.Error("send canvas alert failed", "chat", chat, "reason", alert.reason, "err", err)
		}
	}
}

// canvasAlert is a one-off message explaining why Canvas is not usable and
// what the user should do about it.
type canvasAlert struct {
	reason string // stored in runtime stats
	text   string // sent to the chats
}

// formatCanvasAlert builds the alert for a Canvas failure reason.
func formatCanvasAlert(reason string) canvasAlert {
	switch reason {
	case "no token":
		return canvasAlert{
			reason: "no token",
			text: "🔑 <b>Canvas API не подключён.</b>\n\n" +
				"Бот не может получать задания и оценки. Отправьте <b>/setup</b> и вставьте токен доступа Canvas — " +
				"бот проверит его и сразу подтянет данные.\n\n" +
				"Автоматическая проверка продолжается.",
		}
	case "unauthorized":
		return canvasAlert{
			reason: "unauthorized",
			text: "🔴 <b>Canvas: токен истёк или недействителен.</b>\n\n" +
				"Бот не может получать данные. Отправьте <b>/setup</b> и вставьте новый токен.\n" +
				"Повторные попытки продолжаются автоматически.",
		}
	default:
		return canvasAlert{reason: reason, text: "🔴 <b>Canvas недоступен.</b>\n\n" + reason}
	}
}
