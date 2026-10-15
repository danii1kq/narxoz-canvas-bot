package webapp

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"

	"narxoz-canvas-bot/internal/canvas"
	"narxoz-canvas-bot/internal/canvasconn"
	"narxoz-canvas-bot/internal/model"
	"narxoz-canvas-bot/internal/notify"
	"narxoz-canvas-bot/internal/runtime"
	"narxoz-canvas-bot/internal/secrets"
	"narxoz-canvas-bot/internal/store"
)

// TestVisualApp serves the Mini App against a real database so the interface can
// be inspected in a browser during development. It is skipped unless
// WEBAPP_VISUAL=1, never runs in a normal `go test ./...` and is not part of the
// deployed binary.
//
//	WEBAPP_VISUAL=1 WEBAPP_VISUAL_DB=./bot.db go test ./internal/webapp -run TestVisualApp -v -timeout 0
func TestVisualApp(t *testing.T) {
	if os.Getenv("WEBAPP_VISUAL") != "1" {
		t.Skip("set WEBAPP_VISUAL=1 to serve the app locally")
	}
	dbPath := os.Getenv("WEBAPP_VISUAL_DB")
	if dbPath == "" {
		dbPath = "./bot.db"
	}
	addr := os.Getenv("WEBAPP_VISUAL_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8090"
	}

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store %q: %v", dbPath, err)
	}
	defer st.Close()

	box := secrets.NewBox(os.Getenv("CANVAS_TOKEN_SECRET"))
	cv := canvas.New(envOr("CANVAS_BASE_URL", "https://canvas.narxoz.kz"), "", 10, 30*time.Second, log)
	// Restore the stored token exactly like cmd/bot does at startup.
	chatID := int64(0)
	if raw := os.Getenv("WEBAPP_VISUAL_CHAT"); raw != "" {
		chatID, _ = strconv.ParseInt(raw, 10, 64)
	}
	if latest, ok := st.LatestChatToken(); ok {
		if chatID == 0 {
			chatID = latest.ChatID
		}
		if plain, oerr := box.Open(latest.Token); oerr == nil {
			cv.SetToken(plain)
		}
	}
	if chatID == 0 {
		chatID = 1
	}

	conn := canvasconn.New(cv, st, box, noopSyncer{}, "", log)
	stats := runtime.New()
	switch os.Getenv("WEBAPP_VISUAL_SEED") {
	case "1", "empty":
		if os.Getenv("WEBAPP_VISUAL_SEED") == "1" {
			seedPreview(t, st)
		}
		// Present the preview as a working, recently polled connection.
		if err := conn.Persist(chatID, "visual-token-for-preview", model.User{Name: "LI DANIIL", Login: "danii1kq"}, true); err != nil {
			t.Fatalf("seed token: %v", err)
		}
		now := time.Now().UTC()
		stats.SetCanvasOK(true, "", now)
		stats.SetLastPoll(now)
		stats.IncPoll()
	}
	srv := New(Config{
		Addr:            addr,
		LeadHours:       24,
		RequestTimeout:  20 * time.Second,
		RefreshCooldown: time.Second,
	}, st, conn, stats, notify.NewFormatter(time.UTC), NewAuth("visual", chatID), log)

	log.Info("visual mini app", "url", "http://"+addr, "chat", chatID, "db", dbPath)
	if err := srv.Run(context.Background()); err != nil {
		t.Fatalf("serve: %v", err)
	}
}

// noopSyncer reports a successful refresh without touching Canvas, so the UI can
// be exercised offline.
type noopSyncer struct{}

func (noopSyncer) PollOnce(ctx context.Context) error { return nil }

// seedPreview fills an empty database with illustrative data shaped like a real
// Canvas export (the course and assignment names come from this bot's own
// account), so every interface state can be inspected. Deadlines are relative
// to the moment the preview starts: overdue, today, soon, later, no deadline.
func seedPreview(t *testing.T, st *store.Store) {
	t.Helper()
	if courses, _ := st.LoadCourses(true); len(courses) > 0 {
		return // already populated
	}
	now := time.Now().UTC()
	soon := now.Add(3*time.Hour + 20*time.Minute)
	in3d := now.Add(70 * time.Hour)
	in9d := now.Add(9 * 24 * time.Hour)
	overdue := now.Add(-4 * 24 * time.Hour)
	month := now.Add(20 * 24 * time.Hour)

	courses := []model.Course{
		{ID: 7910, Name: "Minor: Событийный менеджмент", Code: "202610:HOT3501-RUS-1"},
		{ID: 7402, Name: "Введение в компьютерные сети", Code: "202610:CSS3002-ENG-3"},
		{ID: 9006, Name: "Казахский язык 1", Code: "202610:KZL1001-KAZ-15"},
		{ID: 7403, Name: "Машинное обучение", Code: "202610:CSS3004-ENG-1"},
		{ID: 8490, Name: "Методы исследований", Code: "202610:SCOM3001-ENG-6"},
		{ID: 7410, Name: "Основы UX/UI", Code: "202610:CSS3006-ENG-3"},
	}
	if err := st.SyncCourses(courses); err != nil {
		t.Fatalf("seed courses: %v", err)
	}

	pts := func(v float64) *float64 { return &v }
	sc := func(v float64) *float64 { return &v }
	gr := func(v string) *string { return &v }

	rows := []model.Assignment{
		// Overdue and never submitted.
		{CourseID: 7410, ID: 1, Name: "MID PRACTISE", Points: pts(40), DueAt: &overdue, HTMLURL: "https://canvas.narxoz.kz/courses/7410/assignments/1"},
		// Due today.
		{CourseID: 9006, ID: 2, Name: "Тапсырма-8. Лексика-грамматикалық тест", Points: pts(30), DueAt: &soon, HTMLURL: "https://canvas.narxoz.kz/courses/9006/assignments/2"},
		{CourseID: 7410, ID: 3, Name: "Lab 5 · Usability test", Points: pts(10), DueAt: &soon, HTMLURL: "https://canvas.narxoz.kz/courses/7410/assignments/3"},
		// Later this and next week.
		{CourseID: 8490, ID: 4, Name: "Task-3: Data Collection Plan", Points: pts(20), DueAt: &in3d, HTMLURL: "https://canvas.narxoz.kz/courses/8490/assignments/4"},
		{CourseID: 7403, ID: 5, Name: "Task 3 · Topics 5-6", Points: pts(25), DueAt: &in9d, HTMLURL: "https://canvas.narxoz.kz/courses/7403/assignments/5"},
		{CourseID: 7910, ID: 6, Name: "Задание 1 ВСК 2", Points: pts(30), DueAt: &month, HTMLURL: "https://canvas.narxoz.kz/courses/7910/assignments/6"},
		// No deadline, like the Kazakh language tasks.
		{CourseID: 9006, ID: 7, Name: "Тапсырма-1. Айтылым", Points: pts(20), HTMLURL: "https://canvas.narxoz.kz/courses/9006/assignments/7"},
		// Submitted, submitted late, graded.
		{CourseID: 7410, ID: 8, Name: "Lab 3 · Heuristic review", Points: pts(5), Score: sc(5), Grade: gr("5"), DueAt: &overdue, SubmittedAt: &overdue, HTMLURL: "https://canvas.narxoz.kz/courses/7410/assignments/8"},
		{CourseID: 7910, ID: 9, Name: "Задание 3 ВСК 1", Points: pts(40), Score: sc(36), Grade: gr("36"), DueAt: &overdue, SubmittedAt: &overdue, HTMLURL: "https://canvas.narxoz.kz/courses/7910/assignments/9"},
		{CourseID: 7410, ID: 10, Name: "Kahoot · mid-term quiz", Points: pts(10), Score: sc(9.6), Grade: gr("9.6"), DueAt: &overdue, SubmittedAt: &overdue, HTMLURL: "https://canvas.narxoz.kz/courses/7410/assignments/10"},
		{CourseID: 8490, ID: 11, Name: "Task-2: Mini Literature Review", Points: pts(20), DueAt: &overdue, SubmittedAt: &overdue, HTMLURL: "https://canvas.narxoz.kz/courses/8490/assignments/11"},
	}
	late := overdue.Add(30 * time.Hour)
	rows = append(rows, model.Assignment{
		CourseID: 7403, ID: 12, Name: "Task 2 · Topics 3-4", Points: pts(25),
		DueAt: &overdue, SubmittedAt: &late, HTMLURL: "https://canvas.narxoz.kz/courses/7403/assignments/12",
	})
	if err := st.SyncAssignments(rows); err != nil {
		t.Fatalf("seed assignments: %v", err)
	}
	if err := st.SetLeadHours(24); err != nil {
		t.Fatalf("seed lead: %v", err)
	}
	if err := st.AddSubscriber(869865322); err != nil {
		t.Fatalf("seed subscriber: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
