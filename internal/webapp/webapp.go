// Package webapp serves the Telegram Mini App: a small single-page interface
// over the same SQLite data the bot uses, plus the JSON API behind it.
//
// The app is opened from the chat menu button ("Open"). Every API request must
// carry Telegram initData signed with the bot token; the Telegram user id is
// used as the chat id, so the app and the bot act on the same Canvas token and
// the same settings.
package webapp

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"narxoz-canvas-bot/internal/canvasconn"
	"narxoz-canvas-bot/internal/model"
	"narxoz-canvas-bot/internal/notify"
	"narxoz-canvas-bot/internal/runtime"
	"narxoz-canvas-bot/internal/store"
)

// Config configures the Mini App server.
type Config struct {
	// Addr is the local listen address (behind the HTTPS reverse proxy).
	Addr string
	// AppURL is the public HTTPS address, shown in the UI for reference.
	AppURL string
	// OwnerChatID, when non-zero, restricts the app to this Telegram user.
	OwnerChatID int64
	// LeadHours is the default reminder lead time (matches the bot).
	LeadHours int
	// RequestTimeout bounds a Canvas call made from the app.
	RequestTimeout time.Duration
	// RefreshCooldown is the minimum interval between manual refreshes.
	RefreshCooldown time.Duration
}

// Server is the Mini App HTTP server.
type Server struct {
	cfg    Config
	store  *store.Store
	conn   *canvasconn.Conn
	stats  *runtime.Stats
	fm     notify.Formatter
	auth   *Auth
	log    *slog.Logger
	static fs.FS

	mu          sync.Mutex
	lastRefresh map[int64]time.Time
}

// New constructs a Server.
func New(cfg Config, st *store.Store, conn *canvasconn.Conn, stats *runtime.Stats, fm notify.Formatter, auth *Auth, log *slog.Logger) *Server {
	if cfg.LeadHours == 0 {
		cfg.LeadHours = 24
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 30 * time.Second
	}
	if cfg.RefreshCooldown <= 0 {
		cfg.RefreshCooldown = 20 * time.Second
	}
	return &Server{
		cfg:         cfg,
		store:       st,
		conn:        conn,
		stats:       stats,
		fm:          fm,
		auth:        auth,
		log:         log,
		lastRefresh: map[int64]time.Time{},
	}
}

// Handler returns the HTTP routes of the app.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/api/state", s.guarded(s.handleState))
	mux.HandleFunc("/api/assignments", s.guarded(s.handleAssignments))
	mux.HandleFunc("/api/courses", s.guarded(s.handleCourses))
	mux.HandleFunc("/api/refresh", s.guarded(s.handleRefresh))
	mux.HandleFunc("/api/settings/lead", s.guarded(s.handleSetLead))
	mux.HandleFunc("/api/courses/", s.guarded(s.handleCourseMute))
	mux.HandleFunc("/api/canvas/token", s.guardedCanvasToken)
	mux.HandleFunc("/api/canvas/check", s.guarded(s.handleCanvasCheck))

	// The single-page app itself: index.html for "/" and any unknown path.
	static, err := fs.Sub(assets, "assets")
	if err != nil {
		// Unreachable: the embed pattern is fixed at build time.
		panic(err)
	}
	s.static = static
	mux.Handle("/", s.spaHandler(http.FileServer(http.FS(static))))

	return mux
}

// Run serves the app until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute, // a manual refresh walks all courses
		IdleTimeout:       2 * time.Minute,
	}
	errCh := make(chan error, 1)
	go func() {
		s.log.Info("mini app listening", "addr", s.cfg.Addr, "url", s.cfg.AppURL,
			"dev_mode", s.auth.DevMode())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-errCh
	}
}

// guarded wraps a handler with Telegram authentication.
func (s *Server) guarded(fn func(http.ResponseWriter, *http.Request, User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := s.authenticate(w, r)
		if !ok {
			return
		}
		fn(w, r, u)
	}
}

// guardedCanvasToken authenticates and dispatches by method, because the
// token endpoint is both created (PUT) and removed (DELETE).
func (s *Server) guardedCanvasToken(w http.ResponseWriter, r *http.Request) {
	u, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodPut, http.MethodPost:
		s.handleCanvasToken(w, r, u)
	case http.MethodDelete:
		s.handleCanvasTokenDelete(w, r, u)
	default:
		w.Header().Set("Allow", "PUT, DELETE")
		writeError(w, http.StatusMethodNotAllowed, "метод не поддерживается")
	}
}

// authenticate resolves the Telegram user of a request, enforcing the owner
// restriction when one is configured.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (User, bool) {
	initData := initDataOf(r)
	u, err := s.auth.Authorize(initData)
	if err != nil {
		status := http.StatusUnauthorized
		if errors.Is(err, ErrStaleInitData) {
			status = http.StatusUnauthorized
		}
		s.log.Warn("mini app auth failed", "path", r.URL.Path, "err", err)
		writeError(w, status, "откройте приложение из Telegram")
		return User{}, false
	}
	if s.cfg.OwnerChatID != 0 && u.ID != s.cfg.OwnerChatID {
		s.log.Warn("mini app access denied", "user", u.ID, "owner", s.cfg.OwnerChatID)
		writeError(w, http.StatusForbidden, "приложение доступно только владельцу бота")
		return User{}, false
	}
	return u, true
}

// initDataOf extracts Telegram initData from the request. The Mini App sends
// "Authorization: tma <initData>"; a custom header is accepted as well.
func initDataOf(r *http.Request) string {
	if h := strings.TrimSpace(r.Header.Get("Authorization")); h != "" {
		if rest, ok := strings.CutPrefix(h, "tma "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return strings.TrimSpace(r.Header.Get("X-Telegram-Init-Data"))
}

// spaHandler serves static files and falls back to index.html for client-side
// routes. index.html is written directly (delegating to FileServer would issue
// a redirect from /index.html to /).
func (s *Server) spaHandler(files http.Handler) http.Handler {
	index, indexErr := fs.ReadFile(s.static, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			if indexErr != nil {
				http.Error(w, "app is not built", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write(index)
			return
		}
		if strings.Contains(path, ".") {
			if strings.HasSuffix(path, ".html") || strings.HasSuffix(path, ".css") || strings.HasSuffix(path, ".js") {
				w.Header().Set("Cache-Control", "no-cache")
			}
			files.ServeHTTP(w, r)
			return
		}
		if indexErr != nil {
			http.Error(w, "app is not built", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}

// requestContext bounds a Canvas call with the configured timeout.
func (s *Server) requestContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), s.cfg.RequestTimeout)
}

// refreshAllowed applies a per-user cooldown to manual refreshes.
func (s *Server) refreshAllowed(chatID int64) (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if last, ok := s.lastRefresh[chatID]; ok {
		if wait := s.cfg.RefreshCooldown - now.Sub(last); wait > 0 {
			return wait, false
		}
	}
	s.lastRefresh[chatID] = now
	return 0, true
}

// --- sorting (mirrors the bot's list order) ---

func sortUpcoming(items []model.Assignment) []model.Assignment {
	out := append([]model.Assignment(nil), items...)
	sort.SliceStable(out, func(i, j int) bool {
		di, dj := out[i].DueAt, out[j].DueAt
		switch {
		case di == nil || dj == nil:
			if di == nil && dj == nil {
				return lessByKey(out[i], out[j])
			}
			return di != nil
		case !di.Equal(*dj):
			return di.Before(*dj)
		default:
			return lessByKey(out[i], out[j])
		}
	})
	return out
}

func sortGrades(items []model.Assignment) []model.Assignment {
	out := append([]model.Assignment(nil), items...)
	sort.SliceStable(out, func(i, j int) bool {
		gi, gj := out[i].GradedAt, out[j].GradedAt
		switch {
		case gi == nil || gj == nil:
			if gi == nil && gj == nil {
				return lessByKey(out[i], out[j])
			}
			return gi != nil
		case !gi.Equal(*gj):
			return gi.After(*gj)
		default:
			return lessByKey(out[i], out[j])
		}
	})
	return out
}

func lessByKey(a, b model.Assignment) bool {
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	return a.ID < b.ID
}

//go:embed assets
var assets embed.FS
