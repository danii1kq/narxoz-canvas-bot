package bot

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"narxoz-canvas-bot/internal/canvas"
	"narxoz-canvas-bot/internal/canvasconn"
	"narxoz-canvas-bot/internal/config"
	"narxoz-canvas-bot/internal/notify"
	"narxoz-canvas-bot/internal/poller"
	"narxoz-canvas-bot/internal/runtime"
	"narxoz-canvas-bot/internal/secrets"
	"narxoz-canvas-bot/internal/store"
)

// TestSetupFlowConnectsCanvasFromChat walks the whole "connect Canvas inside
// the chat" dialog against a fake Canvas and a fake Telegram Bot API: the user
// sends /setup, pastes a wrong token (rejected), pastes the right one, and the
// bot verifies it, stores it sealed and pulls live data into the chat.
func TestSetupFlowConnectsCanvasFromChat(t *testing.T) {
	const (
		goodToken = "1234~AbCdEfGhIjKlMnOpQrStUvWxYz0123456789"
		badToken  = "9999~WrongWrongWrongWrongWrongWrongWrong"
		chatID    = int64(5001)
	)

	// --- fake Canvas (accepts only goodToken) ---
	var canvasMu sync.Mutex
	var canvasAuth []string
	canvasSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		canvasMu.Lock()
		canvasAuth = append(canvasAuth, auth)
		canvasMu.Unlock()

		if auth != "Bearer "+goodToken {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"errors":[{"message":"Invalid access token"}]}`))
			return
		}
		switch r.URL.Path {
		case "/api/v1/users/self":
			writeJSON(w, map[string]any{"id": 7, "name": "Иван Студент", "login": "ivan", "email": "ivan@narxoz.kz"})
		case "/api/v1/courses":
			writeJSON(w, []map[string]any{{"id": 1, "name": "Математика", "course_code": "M1"}})
		case "/api/v1/courses/1/assignments":
			writeJSON(w, []map[string]any{{
				"id": 100, "name": "Домашка 1", "points_possible": 10,
				"due_at": "2026-12-01T10:00:00Z", "html_url": "https://canvas.narxoz.kz/courses/1/assignments/100",
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer canvasSrv.Close()

	// --- fake Telegram Bot API ---
	ft := &fakeTelegram{}
	tgSrv := httptest.NewServer(http.HandlerFunc(ft.serveHTTP))
	defer tgSrv.Close()

	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	tg, err := notify.NewTelegram("test-token", tgSrv.URL+"/bot%s/%s", log)
	if err != nil {
		t.Fatalf("fake telegram: %v", err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	box := secrets.NewBox("test-secret")
	cv := canvas.New(canvasSrv.URL, "", 1000, 10*time.Second, log)
	if cv.HasToken() {
		t.Fatal("client must start without a token when CANVAS_API_TOKEN is empty")
	}

	cfg := config.Config{
		PollInterval:     time.Minute,
		ReminderInterval: time.Minute,
		ReminderLead:     24,
		TZ:               time.UTC,
		RequestTimeout:   10 * time.Second,
	}
	fm := notify.NewFormatter(time.UTC)
	stats := runtime.New()
	p := poller.New(cfg, cv, st, tg, fm, stats, log)
	conn := canvasconn.New(cv, st, box, p, "", log)
	b := New(tg, st, conn, fm, stats, log, time.UTC, 0, "", 10*time.Second)

	// 1) /setup explains how to obtain a token and waits for it.
	b.handle(msgUpdate(chatID, 11, "/setup"))
	if !ft.anyContains("Подключение Canvas API") {
		t.Fatalf("setup prompt not sent; sent=%v", ft.sentTexts())
	}
	if !b.isPending(chatID) {
		t.Fatal("chat must be waiting for the token after /setup")
	}

	// 2) A wrong token is rejected, the secret message is deleted, and the
	//    chat keeps waiting so the user can paste again.
	b.handle(msgUpdate(chatID, 12, badToken))
	if !ft.anyContains("Canvas отклонил токен") {
		t.Fatalf("rejection notice not sent; sent=%v", ft.sentTexts())
	}
	if !ft.deletedMessage(12) {
		t.Fatal("message with the bad token must be deleted")
	}
	if !b.isPending(chatID) {
		t.Fatal("chat must still wait for a token after a rejected paste")
	}

	// 3) A valid token is verified, stored sealed and applied to the client.
	b.handle(msgUpdate(chatID, 13, goodToken))
	sent := strings.Join(ft.sentTexts(), "\n---\n")
	for _, want := range []string{"Canvas API подключён", "Иван Студент", "ivan", "Домашка 1", "Математика"} {
		if !strings.Contains(sent, want) {
			t.Fatalf("chat is missing %q; sent=<<<%s>>>", want, sent)
		}
	}
	if !ft.deletedMessage(13) {
		t.Fatal("message with the token must be deleted")
	}
	if b.isPending(chatID) {
		t.Fatal("chat must leave the waiting state after a successful setup")
	}
	if !cv.HasToken() {
		t.Fatal("the verified token must be applied to the Canvas client")
	}

	row, ok := st.ChatToken(chatID)
	if !ok {
		t.Fatal("token must be persisted for the chat")
	}
	if row.Token == goodToken || strings.Contains(row.Token, goodToken) {
		t.Fatal("token must not be stored in plain text")
	}
	plain, err := box.Open(row.Token)
	if err != nil || plain != goodToken {
		t.Fatalf("stored token did not round-trip: %q, %v", plain, err)
	}
	if !row.Verified {
		t.Fatal("a token confirmed by the live probe must be stored as verified")
	}

	// 4) Live Canvas data landed in the store, so the views are real.
	rows, err := st.LoadAssignments()
	if err != nil || len(rows) != 1 {
		t.Fatalf("assignments = %d (%v), want 1", len(rows), err)
	}
	if rows[0].Name != "Домашка 1" || rows[0].CourseID != 1 {
		t.Fatalf("unexpected assignment: %+v", rows[0])
	}
	if courses, _ := st.LoadCourses(true); len(courses) != 1 || courses[0].Name != "Математика" {
		t.Fatalf("unexpected courses: %+v", courses)
	}

	// 5) The "🔄 Обновить" button asks Canvas again and re-renders the list.
	before := len(ft.sentTexts())
	b.handle(msgUpdate(chatID, 14, btnRefresh))
	if len(ft.sentTexts()) <= before {
		t.Fatal("refresh must send an updated view")
	}
	if !strings.Contains(ft.sentTexts()[len(ft.sentTexts())-1], "Домашка 1") {
		t.Fatalf("refreshed view does not contain live data: %v", ft.sentTexts())
	}

	// 6) Canvas saw the probe with the rejected token first, then every call
	//    carried the verified chat token.
	canvasMu.Lock()
	auth := append([]string(nil), canvasAuth...)
	canvasMu.Unlock()
	if len(auth) < 2 {
		t.Fatalf("expected a probe plus poller calls, got %v", auth)
	}
	if auth[0] != "Bearer "+badToken {
		t.Fatalf("first Canvas call must probe the rejected token, got %q", auth[0])
	}
	for i, h := range auth[1:] {
		if h != "Bearer "+goodToken {
			t.Fatalf("Canvas call %d used %q", i+1, h)
		}
	}

	// 7) The token survives a restart: a fresh client picks it up from the DB.
	restarted := canvas.New(canvasSrv.URL, "", 1000, 10*time.Second, log)
	if latest, ok := st.LatestChatToken(); ok {
		plain, err := box.Open(latest.Token)
		if err != nil || plain != goodToken {
			t.Fatalf("restart restore failed: %q, %v", plain, err)
		}
		restarted.SetToken(plain)
	} else {
		t.Fatal("stored token must be restorable at startup")
	}
	if _, err := restarted.User(t.Context()); err != nil {
		t.Fatalf("restored client cannot reach Canvas: %v", err)
	}
}

// TestRefreshWithoutTokenPromptsSetup checks that asking for data before any
// token exists tells the user to connect Canvas instead of showing nothing.
func TestRefreshWithoutTokenPromptsSetup(t *testing.T) {
	ft := &fakeTelegram{}
	tgSrv := httptest.NewServer(http.HandlerFunc(ft.serveHTTP))
	defer tgSrv.Close()

	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	tg, err := notify.NewTelegram("test-token", tgSrv.URL+"/bot%s/%s", log)
	if err != nil {
		t.Fatalf("fake telegram: %v", err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cv := canvas.New("https://canvas.invalid", "", 1000, time.Second, log)
	fm := notify.NewFormatter(time.UTC)
	stats := runtime.New()
	b := New(tg, st, canvasconn.New(cv, st, secrets.NewBox(""), stubSyncer{}, "", log), fm, stats, log, time.UTC, 0, "", time.Second)

	b.handle(msgUpdate(42, 1, btnAssignments))
	if !ft.anyContains("Canvas API не подключён") {
		t.Fatalf("prompt not sent; sent=%v", ft.sentTexts())
	}
	if ft.anyContains("Домашка") {
		t.Fatal("no data may be rendered without a token")
	}
}

// stubSyncer is a Syncer that reports "no token" like the real poller would.
type stubSyncer struct{}

func (stubSyncer) PollOnce(ctx context.Context) error { return canvas.ErrNoToken }

// TestUnreachableCanvasKeepsToken checks that a probe which fails because
// Canvas is unreachable (not because the token is bad) still stores the token
// — marked as unverified — instead of asking the user to paste it again.
func TestUnreachableCanvasKeepsToken(t *testing.T) {
	const token = "1234~AbCdEfGhIjKlMnOpQrStUvWxYz0123456789"
	const chatID = int64(7001)

	ft := &fakeTelegram{}
	tgSrv := httptest.NewServer(http.HandlerFunc(ft.serveHTTP))
	defer tgSrv.Close()

	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	tg, err := notify.NewTelegram("test-token", tgSrv.URL+"/bot%s/%s", log)
	if err != nil {
		t.Fatalf("fake telegram: %v", err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// Nothing listens on port 1: the dial fails and the 1s context expires.
	cv := canvas.New("http://127.0.0.1:1", "", 1000, time.Second, log)
	box := secrets.NewBox("test-secret")
	fm := notify.NewFormatter(time.UTC)
	stats := runtime.New()
	b := New(tg, st, canvasconn.New(cv, st, box, stubSyncer{}, "", log), fm, stats, log, time.UTC, 0, "", time.Second)

	b.handle(msgUpdate(chatID, 21, "/setup"))
	b.handle(msgUpdate(chatID, 22, token))

	sent := strings.Join(ft.sentTexts(), "\n---\n")
	if !strings.Contains(sent, "Canvas не ответил на проверку") {
		t.Fatalf("expected an unreachable-Canvas notice, got <<<%s>>>", sent)
	}
	if !strings.Contains(sent, "Токен <b>сохранён</b>") {
		t.Fatalf("the notice must say the token was stored, got <<<%s>>>", sent)
	}
	if ft.anyContains("Canvas отклонил токен") {
		t.Fatal("a network failure must not be reported as a rejected token")
	}
	if !ft.deletedMessage(22) {
		t.Fatal("message with the token must be deleted")
	}
	if b.isPending(chatID) {
		t.Fatal("the dialog must be closed once the token is stored")
	}
	if !cv.HasToken() {
		t.Fatal("an unverified token must still be applied so the background sync can use it")
	}

	row, ok := st.ChatToken(chatID)
	if !ok {
		t.Fatal("token must be persisted even when the probe could not reach Canvas")
	}
	if row.Verified {
		t.Fatal("a token that was never confirmed must be stored unverified")
	}
	if plain, err := box.Open(row.Token); err != nil || plain != token {
		t.Fatalf("stored token did not round-trip: %q, %v", plain, err)
	}
	if line := b.conn.StateLine(chatID); !strings.Contains(line, "ждёт проверки") {
		t.Fatalf("state line should mention a pending check, got %q", line)
	}
}

// --- fakes ---

// fakeTelegram is a minimal Bot API server implementing getMe, sendMessage,
// deleteMessage and answerCallbackQuery; enough for a full run of the bot's
// update handler.
type fakeTelegram struct {
	mu    sync.Mutex
	sent  []string
	del   []int
	next  int
	cbAns []string
}

func (f *fakeTelegram) serveHTTP(w http.ResponseWriter, r *http.Request) {
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	// tgbotapi POSTs urlencoded bodies; r.Form merges query and body.
	_ = r.ParseForm()
	text := r.Form.Get("text")
	chatID, _ := strconv.ParseInt(r.Form.Get("chat_id"), 10, 64)
	msgID, _ := strconv.Atoi(r.Form.Get("message_id"))
	if text == "" && chatID == 0 && msgID == 0 {
		body, _ := io.ReadAll(r.Body)
		var p struct {
			ChatID    int64  `json:"chat_id"`
			Text      string `json:"text"`
			MessageID int    `json:"message_id"`
		}
		if err := json.Unmarshal(body, &p); err == nil {
			text, chatID, msgID = p.Text, p.ChatID, p.MessageID
		}
	}

	switch method {
	case "getMe":
		writeJSON(w, map[string]any{"ok": true, "result": map[string]any{
			"id": 1, "is_bot": true, "first_name": "Test", "username": "testbot",
		}})
	case "sendMessage":
		f.mu.Lock()
		f.next++
		id := f.next
		f.sent = append(f.sent, text)
		f.mu.Unlock()
		writeJSON(w, map[string]any{"ok": true, "result": map[string]any{
			"message_id": id, "date": time.Now().Unix(), "text": text,
			"chat": map[string]any{"id": chatID, "type": "private"},
		}})
	case "deleteMessage":
		f.mu.Lock()
		f.del = append(f.del, msgID)
		f.mu.Unlock()
		writeJSON(w, map[string]any{"ok": true, "result": true})
	case "answerCallbackQuery":
		f.mu.Lock()
		f.cbAns = append(f.cbAns, r.Form.Get("callback_query_id"))
		f.mu.Unlock()
		writeJSON(w, map[string]any{"ok": true, "result": true})
	default:
		writeJSON(w, map[string]any{"ok": true, "result": true})
	}
}

func (f *fakeTelegram) sentTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

func (f *fakeTelegram) anyContains(sub string) bool {
	for _, s := range f.sentTexts() {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func (f *fakeTelegram) deletedMessage(id int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, got := range f.del {
		if got == id {
			return true
		}
	}
	return false
}

// msgUpdate builds a private-chat text update.
func msgUpdate(chatID int64, msgID int, text string) tgbotapi.Update {
	return tgbotapi.Update{
		UpdateID: 1,
		Message: &tgbotapi.Message{
			MessageID: msgID,
			Chat:      &tgbotapi.Chat{ID: chatID},
			Text:      text,
		},
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
