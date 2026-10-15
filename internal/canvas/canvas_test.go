package canvas_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"narxoz-canvas-bot/internal/canvas"
)

func newTestClient(baseURL string) *canvas.Client {
	return canvas.New(baseURL, "test-token", 1000, 10*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
}

func TestCoursesPaginationAndAuth(t *testing.T) {
	var srv *httptest.Server
	var page int32
	var gotAuth, gotAccept string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/courses" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		n := atomic.AddInt32(&page, 1)
		if n == 1 {
			w.Header().Set("Link", "<"+srv.URL+"/api/v1/courses?page=2>; rel=\"next\"")
			_, _ = w.Write([]byte(`[{"id":1,"name":"Math","course_code":"M1"}]`))
			return
		}
		_, _ = w.Write([]byte(`[{"id":2,"name":"Physics","course_code":"P1"}]`))
	})
	srv = httptest.NewServer(handler)
	defer srv.Close()

	c := newTestClient(srv.URL)
	courses, err := c.Courses(context.Background())
	if err != nil {
		t.Fatalf("Courses: %v", err)
	}
	if len(courses) != 2 {
		t.Fatalf("want 2 courses (both pages), got %d", len(courses))
	}
	if courses[0].ID != 1 || courses[1].ID != 2 {
		t.Fatalf("pagination order wrong: %+v", courses)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	if gotAccept != "application/json" {
		t.Fatalf("accept header = %q", gotAccept)
	}
}

func TestAssignmentsParsingAndTimezone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/courses/42/assignments" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"id":100,"name":"HW1","points_possible":15,"due_at":"2026-01-02T03:04:05Z","html_url":"https://x/1","submission":{"score":12.5,"grade":"A","submitted_at":"2026-01-01T00:00:00Z","graded_at":null,"workflow_state":"graded"}}]`))
	}))
	defer srv.Close()

	asgs, err := newTestClient(srv.URL).Assignments(context.Background(), 42)
	if err != nil {
		t.Fatalf("Assignments: %v", err)
	}
	if len(asgs) != 1 {
		t.Fatalf("want 1 assignment, got %d", len(asgs))
	}
	a := asgs[0]
	if a.CourseID != 42 || a.ID != 100 || a.Name != "HW1" {
		t.Fatalf("basic fields wrong: %+v", a)
	}
	if a.Points == nil || *a.Points != 15 {
		t.Fatalf("points = %v", a.Points)
	}
	if a.DueAt == nil || !a.DueAt.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) || a.DueAt.Location() != time.UTC {
		t.Fatalf("due_at wrong: %v", a.DueAt)
	}
	if a.Score == nil || *a.Score != 12.5 {
		t.Fatalf("score = %v", a.Score)
	}
	if a.Grade == nil || *a.Grade != "A" {
		t.Fatalf("grade = %v", a.Grade)
	}
	if a.SubmittedAt == nil || !a.Submitted() {
		t.Fatalf("submission not recorded")
	}
}

// TestAssignmentsResolveOverrideDates covers assignments whose plain due_at is
// null and whose deadline lives in all_dates (section/group overrides) — the
// case that made Kazakh language tasks look like they had no deadline.
func TestAssignmentsResolveOverrideDates(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = w.Write([]byte(`[
			{"id":1,"name":"override only","due_at":null,"all_dates":[
				{"due_at":"2026-10-25T18:59:59Z","base":false,"title":"Группа 2"},
				{"due_at":null,"base":true,"title":"Everyone"}
			]},
			{"id":2,"name":"base only","due_at":null,"all_dates":[
				{"due_at":"2026-11-01T18:59:59Z","base":true,"title":"Everyone"}
			]},
			{"id":3,"name":"no dates at all","due_at":null,"all_dates":[]},
			{"id":4,"name":"own due_at wins","due_at":"2026-10-02T10:00:00Z","all_dates":[
				{"due_at":"2026-10-09T10:00:00Z","base":false,"title":"Группа 2"}
			]}
		]`))
	}))
	defer srv.Close()

	asgs, err := newTestClient(srv.URL).Assignments(context.Background(), 7)
	if err != nil {
		t.Fatalf("Assignments: %v", err)
	}
	if len(asgs) != 4 {
		t.Fatalf("want 4 assignments, got %d", len(asgs))
	}
	if !strings.Contains(query, "all_dates") {
		t.Fatalf("all_dates must be requested, query = %q", query)
	}
	want := map[int64]string{
		1: "2026-10-25T18:59:59Z", // the user's override, not the empty base
		2: "2026-11-01T18:59:59Z", // base date when nothing else applies
		4: "2026-10-02T10:00:00Z", // plain due_at stays authoritative
	}
	for _, a := range asgs {
		w, ok := want[a.ID]
		if !ok {
			continue
		}
		if a.DueAt == nil {
			t.Fatalf("assignment %d must have a resolved due date", a.ID)
		}
		if got := a.DueAt.UTC().Format(time.RFC3339); got != w {
			t.Fatalf("assignment %d due = %s, want %s", a.ID, got, w)
		}
	}
	for _, a := range asgs {
		if a.ID == 3 && a.DueAt != nil {
			t.Fatalf("an assignment without any dates must stay undated")
		}
	}
}

// TestWithProxyRoutesCanvasThroughProxy runs a minimal proxy and checks that
// Canvas calls go through it — the deployment escape hatch for hosts whose own
// network cannot reach the Canvas instance. The fake Canvas is plain HTTP here,
// so the proxy handles both forms: absolute-URI forwarding and CONNECT tunnels.
func TestWithProxyRoutesCanvasThroughProxy(t *testing.T) {
	var hits int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.Method == http.MethodConnect {
			upstream, err := net.DialTimeout("tcp", r.Host, 5*time.Second)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				upstream.Close()
				http.Error(w, "no hijacking", http.StatusInternalServerError)
				return
			}
			client, _, err := hijacker.Hijack()
			if err != nil {
				upstream.Close()
				return
			}
			_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
			go func() { _, _ = io.Copy(upstream, client); upstream.Close() }()
			_, _ = io.Copy(client, upstream)
			client.Close()
			return
		}
		// Plain HTTP proxying: the request carries an absolute URL.
		outReq, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		outReq.Header = r.Header.Clone()
		resp, err := http.DefaultTransport.RoundTrip(outReq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, values := range resp.Header {
			for _, v := range values {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer proxy.Close()

	canvasSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":7,"name":"Z"}]`))
	}))
	defer canvasSrv.Close()

	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	c := canvas.New(canvasSrv.URL, "tok", 1000, 10*time.Second, log, canvas.WithProxy(proxy.URL))
	courses, err := c.Courses(context.Background())
	if err != nil {
		t.Fatalf("Courses through proxy: %v", err)
	}
	if len(courses) != 1 || courses[0].ID != 7 {
		t.Fatalf("unexpected courses: %+v", courses)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("the request did not use the proxy")
	}

	// An invalid proxy URL must not break the client: it falls back to a direct
	// connection instead of failing every call.
	broken := canvas.New(canvasSrv.URL, "tok", 1000, 10*time.Second, log, canvas.WithProxy("://not a url"))
	if _, err := broken.Courses(context.Background()); err != nil {
		t.Fatalf("invalid proxy must fall back to a direct request: %v", err)
	}
}

func TestUnauthorizedReturnsSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"errors":"unauthorized"}`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv.URL).User(context.Background())
	if !errors.Is(err, canvas.ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestRetryOnServerError(t *testing.T) {
	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&count, 1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`[{"id":7,"name":"Z"}]`))
	}))
	defer srv.Close()

	courses, err := newTestClient(srv.URL).Courses(context.Background())
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if len(courses) != 1 || courses[0].ID != 7 {
		t.Fatalf("unexpected courses: %+v", courses)
	}
	if count != 3 {
		t.Fatalf("expected 3 attempts (2 failures + 1 success), got %d", count)
	}
}

func TestEmptyTokenReturnsErrNoToken(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := canvas.New(srv.URL, "", 1000, 10*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if c.HasToken() {
		t.Fatal("client without a token must report HasToken() == false")
	}
	_, err := c.User(context.Background())
	if !errors.Is(err, canvas.ErrNoToken) {
		t.Fatalf("want ErrNoToken, got %v", err)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatal("no HTTP request may be sent without a token")
	}
}

func TestSetTokenAndWithToken(t *testing.T) {
	var gotAuth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"id":1,"name":"Me"}`))
	}))
	defer srv.Close()

	c := canvas.New(srv.URL, "", 1000, 10*time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	// A probe client must not change the token of the client it came from.
	probe := c.WithToken("probe-token")
	if _, err := probe.User(context.Background()); err != nil {
		t.Fatalf("probe User: %v", err)
	}
	if c.HasToken() {
		t.Fatal("WithToken must not mutate the original client")
	}

	// SetToken flips the shared client over to a new token at runtime.
	c.SetToken("  chat-token  ")
	if !c.HasToken() {
		t.Fatal("client must report a token after SetToken")
	}
	if _, err := c.User(context.Background()); err != nil {
		t.Fatalf("User after SetToken: %v", err)
	}
	if len(gotAuth) != 2 || gotAuth[0] != "Bearer probe-token" || gotAuth[1] != "Bearer chat-token" {
		t.Fatalf("auth headers = %v", gotAuth)
	}

	// Clearing the token disables calls again.
	c.SetToken("")
	if _, err := c.User(context.Background()); !errors.Is(err, canvas.ErrNoToken) {
		t.Fatalf("want ErrNoToken after clearing, got %v", err)
	}
}
