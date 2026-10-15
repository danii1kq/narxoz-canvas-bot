package webapp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

const (
	testToken = "1234~AbCdEfGhIjKlMnOpQrStUvWxYz0123456789"
	testChat  = int64(777)
)

// stubSyncer records refreshes and can be told to fail.
type stubSyncer struct {
	calls int
	err   error
}

func (s *stubSyncer) PollOnce(ctx context.Context) error {
	s.calls++
	if s.err != nil {
		return s.err
	}
	return nil
}

type fixture struct {
	srv    *Server
	http   *httptest.Server
	store  *store.Store
	conn   *canvasconn.Conn
	syncer *stubSyncer
	canvas *httptest.Server
}

// newFixture wires a Server against a fake Canvas and a temp database. The
// Canvas instance accepts only token "testToken".
func newFixture(t *testing.T, opts ...func(*Config)) *fixture {
	t.Helper()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))

	canvasSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"errors":[{"message":"Invalid access token"}]}`))
			return
		}
		switch r.URL.Path {
		case "/api/v1/users/self":
			_, _ = w.Write([]byte(`{"id":7,"name":"Иван Студент","login":"ivan","email":"ivan@narxoz.kz"}`))
		case "/api/v1/courses":
			_, _ = w.Write([]byte(`[{"id":1,"name":"Математика","course_code":"M1"}]`))
		case "/api/v1/courses/1/assignments":
			_, _ = w.Write([]byte(`[{"id":100,"name":"Домашка 1","points_possible":10,"due_at":"2030-12-01T10:00:00Z","html_url":"https://canvas/1"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(canvasSrv.Close)

	st, err := store.Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	box := secrets.NewBox("test-secret")
	cv := canvas.New(canvasSrv.URL, "", 1000, 5*time.Second, log)
	syncer := &stubSyncer{}
	conn := canvasconn.New(cv, st, box, syncer, "", log)

	cfg := Config{
		Addr:            ":0",
		OwnerChatID:     0,
		LeadHours:       24,
		RequestTimeout:  5 * time.Second,
		RefreshCooldown: 20 * time.Second,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	// The dev chat authenticates local requests without Telegram initData.
	srv := New(cfg, st, conn, runtime.New(), notify.NewFormatter(time.UTC), NewAuth("test-token", testChat), log)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return &fixture{srv: srv, http: ts, store: st, conn: conn, syncer: syncer, canvas: canvasSrv}
}

func (f *fixture) do(t *testing.T, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = strings.NewReader(string(raw))
	}
	req, err := http.NewRequest(method, f.http.URL+path, reader)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := f.http.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	var payload map[string]any
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &payload)
	}
	return resp, payload
}

func TestStateRequiresAuthAndReportsCounts(t *testing.T) {
	f := newFixture(t)
	seedData(t, f.store)

	// Unauthenticated: rejected (no dev chat here).
	strict := newFixture(t, func(c *Config) { c.OwnerChatID = 0 })
	strict.srv.auth = NewAuth("other-token", 0)
	if resp, _ := strict.do(t, http.MethodGet, "/api/state", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}

	resp, payload := f.do(t, http.MethodGet, "/api/state", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%v)", resp.StatusCode, payload)
	}
	if name := payload["user"].(map[string]any)["name"]; name != "Локальный тест" {
		t.Fatalf("user name = %v", name)
	}
	canvasInfo := payload["canvas"].(map[string]any)
	if canvasInfo["connected"] != false {
		t.Fatalf("canvas must start disconnected: %v", canvasInfo)
	}
	counts := payload["counts"].(map[string]any)
	if counts["assignments"].(float64) != 3 || counts["overdue"].(float64) != 1 || counts["missing"].(float64) != 1 {
		t.Fatalf("counts = %v", counts)
	}
	if payload["settings"].(map[string]any)["lead_hours"].(float64) != 24 {
		t.Fatalf("lead hours = %v", payload["settings"])
	}
}

func TestAssignmentsViewsAndFiltering(t *testing.T) {
	f := newFixture(t)
	seedData(t, f.store)

	cases := []struct {
		query string
		want  int
	}{
		{"view=all", 3},
		{"view=missing", 1},
		{"view=overdue", 1},
		{"view=grades", 1},
		{"view=course&course=1", 2},
	}
	for _, tc := range cases {
		resp, payload := f.do(t, http.MethodGet, "/api/assignments?"+tc.query, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", tc.query, resp.StatusCode)
		}
		items := payload["items"].([]any)
		if len(items) != tc.want {
			t.Fatalf("%s: %d items, want %d (%v)", tc.query, len(items), tc.want, payload)
		}
	}

	// Row shape: course name resolved, status classified, no colour-only data.
	_, payload := f.do(t, http.MethodGet, "/api/assignments?view=all", nil)
	items := payload["items"].([]any)
	first := items[0].(map[string]any)
	if first["course"] != "Математика" {
		t.Fatalf("course name not resolved: %v", first)
	}
	if first["status"] == "" {
		t.Fatalf("status must be set: %v", first)
	}
}

func TestCoursesAndMuteToggle(t *testing.T) {
	f := newFixture(t)
	seedData(t, f.store)

	resp, payload := f.do(t, http.MethodGet, "/api/courses", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	items := payload["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("courses = %v", items)
	}
	first := items[0].(map[string]any)
	if first["total"].(float64) != 2 || first["overdue"].(float64) != 1 || first["missing"].(float64) != 1 {
		t.Fatalf("course counters = %v", first)
	}

	if resp, _ := f.do(t, http.MethodPut, "/api/courses/1/mute", map[string]any{"muted": true}); resp.StatusCode != http.StatusOK {
		t.Fatalf("mute status = %d", resp.StatusCode)
	}
	courses, _ := f.store.LoadCourses(false)
	for _, c := range courses {
		if c.ID == 1 && !c.Muted {
			t.Fatal("course 1 must be muted")
		}
	}
}

func TestLeadHoursValidation(t *testing.T) {
	f := newFixture(t)
	if resp, _ := f.do(t, http.MethodPut, "/api/settings/lead", map[string]any{"lead_hours": 12}); resp.StatusCode != http.StatusOK {
		t.Fatalf("valid lead rejected")
	}
	if got := f.store.GetLeadHours(24); got != 12 {
		t.Fatalf("lead = %d, want 12", got)
	}
	if resp, _ := f.do(t, http.MethodPut, "/api/settings/lead", map[string]any{"lead_hours": 7}); resp.StatusCode != http.StatusBadRequest {
		t.Fatal("invalid lead must be rejected")
	}
}

func TestCanvasTokenFlow(t *testing.T) {
	f := newFixture(t)

	// Rejected token: Canvas answers 401, nothing is stored.
	resp, payload := f.do(t, http.MethodPut, "/api/canvas/token", map[string]any{"token": "wrong~token~value123456"})
	if resp.StatusCode != http.StatusBadRequest || payload["ok"] != false {
		t.Fatalf("rejected token: %d %v", resp.StatusCode, payload)
	}
	if f.conn.HasToken(testChat) {
		t.Fatal("a rejected token must not be stored")
	}

	// Garbage input is refused before any Canvas call.
	if resp, _ := f.do(t, http.MethodPut, "/api/canvas/token", map[string]any{"token": "not a token"}); resp.StatusCode != http.StatusBadRequest {
		t.Fatal("implausible token must be refused")
	}

	// Valid token: verified and stored sealed.
	resp, payload = f.do(t, http.MethodPut, "/api/canvas/token", map[string]any{"token": testToken})
	if resp.StatusCode != http.StatusOK || payload["verified"] != true {
		t.Fatalf("valid token: %d %v", resp.StatusCode, payload)
	}
	if payload["user"] != "Иван Студент" {
		t.Fatalf("canvas user = %v", payload["user"])
	}
	row, ok := f.store.ChatToken(testChat)
	if !ok || !row.Verified {
		t.Fatalf("token must be stored and verified: %+v", row)
	}
	if strings.Contains(row.Token, testToken) {
		t.Fatal("token must not be stored in plain text")
	}
	if !f.conn.EnsureSyncToken(testChat) {
		t.Fatal("the app token must be adopted for the background sync")
	}

	// State now reports a connected, verified Canvas.
	_, state := f.do(t, http.MethodGet, "/api/state", nil)
	info := state["canvas"].(map[string]any)
	if info["connected"] != true || info["verified"] != true || info["source"] != "chat" {
		t.Fatalf("canvas info = %v", info)
	}
	if strings.Contains(info["masked"].(string), testToken) {
		t.Fatalf("masked token leaks the secret: %v", info["masked"])
	}

	// Connectivity check uses the stored token.
	resp, payload = f.do(t, http.MethodPost, "/api/canvas/check", nil)
	if resp.StatusCode != http.StatusOK || payload["ok"] != true {
		t.Fatalf("check: %d %v", resp.StatusCode, payload)
	}

	// Refresh runs the syncer and honours the cooldown.
	if resp, _ := f.do(t, http.MethodPost, "/api/refresh", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh must succeed")
	}
	if f.syncer.calls != 1 {
		t.Fatalf("syncer calls = %d, want 1", f.syncer.calls)
	}
	if resp, _ := f.do(t, http.MethodPost, "/api/refresh", nil); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second refresh must be rate limited")
	}

	// Disconnect removes the token again.
	if resp, _ := f.do(t, http.MethodDelete, "/api/canvas/token", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("delete must succeed")
	}
	if f.conn.HasToken(testChat) {
		t.Fatal("token must be gone after DELETE")
	}
}

func TestUnreachableCanvasKeepsToken(t *testing.T) {
	f := newFixture(t)
	// Point the client at a dead address: the probe fails without an HTTP 401.
	dead := canvas.New("http://127.0.0.1:1", "", 1000, 300*time.Millisecond, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	f.srv.conn = canvasconn.New(dead, f.store, secrets.NewBox("test-secret"), f.syncer, "", slog.New(slog.NewJSONHandler(io.Discard, nil)))

	resp, payload := f.do(t, http.MethodPut, "/api/canvas/token", map[string]any{"token": testToken})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%v)", resp.StatusCode, payload)
	}
	if payload["verified"] != false || payload["warning"] == nil {
		t.Fatalf("unreachable Canvas must store the token unverified with a warning: %v", payload)
	}
	row, ok := f.store.ChatToken(testChat)
	if !ok || row.Verified {
		t.Fatalf("stored row must exist and be unverified: %+v", row)
	}
}

func TestOwnerRestriction(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.OwnerChatID = 999 })
	if resp, _ := f.do(t, http.MethodGet, "/api/state", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a non-owner", resp.StatusCode)
	}
}

func TestSPAFallback(t *testing.T) {
	f := newFixture(t)
	resp, err := f.http.Client().Get(f.http.URL + "/some/client/route")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Canvas") {
		t.Fatalf("index.html not served: %s", string(body[:min(len(body), 80)]))
	}
}

// seedData stores two courses and three assignments: one overdue and
// unsubmitted, one graded and submitted, one with a future deadline.
func seedData(t *testing.T, st *store.Store) {
	t.Helper()
	if err := st.SyncCourses([]model.Course{
		{ID: 1, Name: "Математика", Code: "M1"},
		{ID: 2, Name: "Физика", Code: "P1"},
	}); err != nil {
		t.Fatalf("sync courses: %v", err)
	}
	past := time.Now().UTC().Add(-48 * time.Hour)
	future := time.Now().UTC().Add(72 * time.Hour)
	submitted := past.Add(time.Hour)
	score := 8.0
	points := 10.0
	grade := "8"
	if err := st.SyncAssignments([]model.Assignment{
		{CourseID: 1, ID: 100, Name: "Домашка 1", Points: &points, DueAt: &past, HTMLURL: "https://canvas/1"},
		{CourseID: 1, ID: 300, Name: "Домашка 2", Points: &points, DueAt: &future, HTMLURL: "https://canvas/3"},
		{CourseID: 2, ID: 200, Name: "Лаба 2", Points: &points, Score: &score, Grade: &grade, DueAt: &past, SubmittedAt: &submitted, HTMLURL: "https://canvas/2"},
	}); err != nil {
		t.Fatalf("sync assignments: %v", err)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
