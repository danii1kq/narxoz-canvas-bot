package webapp

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"narxoz-canvas-bot/internal/canvasconn"
	"narxoz-canvas-bot/internal/model"
)

// State is the payload of GET /api/state.
type State struct {
	User     UserInfo     `json:"user"`
	Canvas   CanvasInfo   `json:"canvas"`
	Counts   Counts       `json:"counts"`
	Sync     SyncInfo     `json:"sync"`
	Settings SettingsInfo `json:"settings"`
}

// UserInfo identifies the Telegram user the app is used by.
type UserInfo struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// CanvasInfo describes the Canvas connection.
type CanvasInfo struct {
	BaseURL   string `json:"base_url"`
	Connected bool   `json:"connected"`
	Source    string `json:"source"`   // .env | chat | another chat | none
	Verified  bool   `json:"verified"` // a chat token confirmed by Canvas
	Masked    string `json:"masked"`   // masked chat token, for display
	User      string `json:"user"`     // Canvas user the stored token belonged to
	OK        bool   `json:"ok"`       // last known connectivity
	Error     string `json:"error"`
}

// Counts summarises the tracked data.
type Counts struct {
	Courses     int `json:"courses"`
	Assignments int `json:"assignments"`
	Missing     int `json:"missing"`
	Overdue     int `json:"overdue"`
	Graded      int `json:"graded"`
}

// SyncInfo describes the background poll.
type SyncInfo struct {
	LastPoll      *time.Time `json:"last_poll"`
	PollCount     int        `json:"poll_count"`
	Notifications int        `json:"notifications"`
}

// SettingsInfo holds user-adjustable settings.
type SettingsInfo struct {
	LeadHours int `json:"lead_hours"`
	Muted     int `json:"muted_courses"`
}

// Assignment is one row of the assignments list.
type Assignment struct {
	ID        int64      `json:"id"`
	CourseID  int64      `json:"course_id"`
	Course    string     `json:"course"`
	Name      string     `json:"name"`
	Points    *float64   `json:"points"`
	Score     *float64   `json:"score"`
	Grade     *string    `json:"grade"`
	DueAt     *time.Time `json:"due_at"`
	Submitted bool       `json:"submitted"`
	Late      bool       `json:"late"`
	Status    string     `json:"status"`
	URL       string     `json:"url"`
}

// AssignmentPage is the response of GET /api/assignments.
type AssignmentPage struct {
	Items []Assignment `json:"items"`
	Total int          `json:"total"`
	Page  int          `json:"page"`
	Pages int          `json:"pages"`
	View  string       `json:"view"`
}

// Course is one row of the courses list.
type Course struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Code    string `json:"code"`
	Muted   bool   `json:"muted"`
	Total   int    `json:"total"`
	Missing int    `json:"missing"`
	Overdue int    `json:"overdue"`
}

// CoursesPage is the response of GET /api/courses.
type CoursesPage struct {
	Items []Course `json:"items"`
}

// --- handlers ---

func (s *Server) handleState(w http.ResponseWriter, r *http.Request, u User) {
	now := time.Now().UTC()
	items := s.assignments()
	courses, _ := s.store.LoadCourses(true)
	snap := s.stats.Snapshot()

	counts := Counts{Courses: len(courses), Assignments: len(items)}
	for _, a := range items {
		switch {
		case isOverdue(a, now):
			counts.Overdue++
		case isMissing(a, now):
			counts.Missing++
		}
		if isGraded(a) {
			counts.Graded++
		}
	}

	var muted int
	all, _ := s.store.LoadCourses(false)
	for _, c := range all {
		if c.Active && c.Muted {
			muted++
		}
	}

	state := State{
		User:   UserInfo{ID: u.ID, Name: u.Name},
		Canvas: s.canvasInfo(u.ID),
		Counts: counts,
		Sync: SyncInfo{
			PollCount:     snap.PollCount,
			Notifications: snap.Notifications,
		},
		Settings: SettingsInfo{LeadHours: s.store.GetLeadHours(s.cfg.LeadHours), Muted: muted},
	}
	if !snap.LastPoll.IsZero() {
		t := snap.LastPoll
		state.Sync.LastPoll = &t
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleAssignments(w http.ResponseWriter, r *http.Request, u User) {
	q := r.URL.Query()
	view := q.Get("view")
	if view == "" {
		view = "all"
	}
	page := atoiDefault(q.Get("page"), 1)
	if page < 1 {
		page = 1
	}
	perPage := atoiDefault(q.Get("per_page"), 40)
	if perPage < 1 || perPage > 200 {
		perPage = 40
	}
	courseID, _ := strconv.ParseInt(q.Get("course"), 10, 64)

	now := time.Now().UTC()
	names := s.store.CourseNameMap()
	items := s.assignments()
	switch view {
	case "missing":
		items = keep(items, func(a model.Assignment) bool { return isMissing(a, now) })
	case "overdue":
		items = keep(items, func(a model.Assignment) bool { return isOverdue(a, now) })
	case "grades":
		items = keep(items, func(a model.Assignment) bool { return isGraded(a) })
		items = sortGrades(items)
	case "course":
		items = keep(items, func(a model.Assignment) bool { return a.CourseID == courseID })
		items = sortUpcoming(items)
	default: // all
		items = sortUpcoming(items)
	}
	if view == "missing" || view == "overdue" {
		items = sortUpcoming(items)
	}
	if view == "grades" && len(items) > 60 {
		items = items[:60]
	}

	total := len(items)
	pages := (total + perPage - 1) / perPage
	if pages == 0 {
		pages = 1
	}
	if page > pages {
		page = pages
	}
	start := (page - 1) * perPage
	end := start + perPage
	if end > total {
		end = total
	}

	out := AssignmentPage{Items: make([]Assignment, 0, end-start), Total: total, Page: page, Pages: pages, View: view}
	for _, a := range items[start:end] {
		out.Items = append(out.Items, Assignment{
			ID:        a.ID,
			CourseID:  a.CourseID,
			Course:    names[a.CourseID],
			Name:      a.Name,
			Points:    a.Points,
			Score:     a.Score,
			Grade:     a.Grade,
			DueAt:     a.DueAt,
			Submitted: a.Submitted(),
			Late:      a.Late(),
			Status:    assignmentStatus(a, now),
			URL:       a.HTMLURL,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCourses(w http.ResponseWriter, r *http.Request, u User) {
	now := time.Now().UTC()
	courses, err := s.store.LoadCourses(true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "не удалось прочитать курсы")
		return
	}
	byCourse := map[int64][]model.Assignment{}
	for _, a := range s.assignments() {
		byCourse[a.CourseID] = append(byCourse[a.CourseID], a)
	}
	items := make([]Course, 0, len(courses))
	for _, c := range courses {
		row := Course{ID: c.ID, Name: c.Name, Code: c.Code, Muted: c.Muted}
		for _, a := range byCourse[c.ID] {
			row.Total++
			switch {
			case isOverdue(a, now):
				row.Overdue++
			case isMissing(a, now):
				row.Missing++
			}
		}
		items = append(items, row)
	}
	writeJSON(w, http.StatusOK, CoursesPage{Items: items})
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request, u User) {
	if !s.conn.EnsureSyncToken(u.ID) {
		writeError(w, http.StatusConflict, "Canvas API не подключён — вставьте токен")
		return
	}
	wait, ok := s.refreshAllowed(u.ID)
	if !ok {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error":       "обновление уже выполнялось только что",
			"retry_after": int(wait.Seconds()) + 1,
		})
		return
	}
	ctx, cancel := s.requestContext(r)
	defer cancel()
	if err := s.conn.Refresh(ctx, u.ID); err != nil {
		s.log.Warn("webapp refresh failed", "chat", u.ID, "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":  canvasconnErrorText(err),
			"detail": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSetLead(w http.ResponseWriter, r *http.Request, u User) {
	var body struct {
		LeadHours int `json:"lead_hours"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	switch body.LeadHours {
	case 6, 12, 24:
	default:
		writeError(w, http.StatusBadRequest, "интервал должен быть 6, 12 или 24 часа")
		return
	}
	if err := s.store.SetLeadHours(body.LeadHours); err != nil {
		writeError(w, http.StatusInternalServerError, "не удалось сохранить интервал")
		return
	}
	writeJSON(w, http.StatusOK, SettingsInfo{LeadHours: body.LeadHours})
}

var coursePathRe = regexp.MustCompile(`^/api/courses/(\d+)/mute$`)

func (s *Server) handleCourseMute(w http.ResponseWriter, r *http.Request, u User) {
	m := coursePathRe.FindStringSubmatch(r.URL.Path)
	if m == nil {
		writeError(w, http.StatusNotFound, "курс не найден")
		return
	}
	id, _ := strconv.ParseInt(m[1], 10, 64)
	var body struct {
		Muted bool `json:"muted"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := s.store.SetCourseMuted(id, body.Muted); err != nil {
		writeError(w, http.StatusInternalServerError, "не удалось сохранить настройку курса")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "muted": body.Muted})
}

func (s *Server) handleCanvasToken(w http.ResponseWriter, r *http.Request, u User) {
	var body struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	token := cleanToken(body.Token)
	if !plausibleToken(token) {
		writeError(w, http.StatusBadRequest, "это не похоже на токен Canvas")
		return
	}
	ctx, cancel := s.requestContext(r)
	defer cancel()

	canvasUser, err := s.conn.Save(ctx, u.ID, token)
	if err == nil {
		name := canvasUser.Name
		if name == "" {
			name = canvasUser.Login
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "verified": true, "user": name, "login": canvasUser.Login,
		})
		return
	}
	if canvasconn.IsUnauthorized(err) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok": false, "verified": false,
			"error": "Canvas отклонил токен (401/403). Проверьте, что он скопирован целиком и создан на " + s.conn.BaseURL(),
		})
		return
	}
	// Canvas is unreachable: keep the token and verify it in the background.
	if perr := s.conn.Persist(u.ID, token, model.User{}, false, false); perr != nil {
		s.log.Error("webapp save token failed", "chat", u.ID, "err", perr)
		writeError(w, http.StatusInternalServerError, "не удалось сохранить токен")
		return
	}
	s.log.Warn("webapp token probe failed, stored for background verification", "chat", u.ID, "err", err)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "verified": false,
		"warning": "Canvas не ответил на проверку: токен сохранён и будет проверяться в фоне",
		"detail":  err.Error(),
	})
}

func (s *Server) handleCanvasTokenDelete(w http.ResponseWriter, r *http.Request, u User) {
	if !s.conn.HasToken(u.ID) {
		writeError(w, http.StatusNotFound, "токен не сохранён")
		return
	}
	stopped, err := s.conn.Drop(u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "не удалось удалить токен")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "polling_stopped": stopped})
}

func (s *Server) handleCanvasCheck(w http.ResponseWriter, r *http.Request, u User) {
	// Only the caller's own token is checked. Falling back to the shared
	// client would answer with the Canvas account of somebody else.
	if !s.conn.HasToken(u.ID) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "Canvas API не подключён"})
		return
	}
	ctx, cancel := s.requestContext(r)
	defer cancel()
	canvasUser, err := s.conn.Client(u.ID).User(ctx)
	if err != nil {
		if canvasconn.IsNoToken(err) {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "Canvas API не подключён"})
			return
		}
		if canvasconn.IsUnauthorized(err) {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "Canvas отклонил токен (401/403)"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": canvasconnErrorText(err), "detail": err.Error()})
		return
	}
	name := canvasUser.Name
	if name == "" {
		name = canvasUser.Login
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": name, "login": canvasUser.Login})
}

// canvasInfo summarises the Canvas connection for the state payload.
func (s *Server) canvasInfo(chatID int64) CanvasInfo {
	info := CanvasInfo{BaseURL: s.conn.BaseURL(), Connected: s.conn.Configured(chatID)}
	snap := s.stats.Snapshot()
	info.OK, info.Error = snap.CanvasOK, snap.CanvasErr

	switch {
	case s.conn.FromEnv():
		info.Source = ".env"
	case s.conn.HasToken(chatID):
		info.Source = "chat"
	case s.conn.LatestChatID() != 0:
		info.Source = "another chat"
	default:
		info.Source = "none"
	}
	if tok, row, ok := s.conn.ChatToken(chatID); ok {
		info.Masked = maskToken(tok)
		info.Verified = row.Verified
		info.User = row.Name
		if info.User == "" {
			info.User = row.Login
		}
	}
	return info
}

// canvasconnErrorText turns a Canvas failure into a short Russian sentence.
func canvasconnErrorText(err error) string {
	switch {
	case canvasconn.IsNoToken(err):
		return "Canvas API не подключён"
	case canvasconn.IsUnauthorized(err):
		return "Canvas отклонил токен (401/403)"
	case canvasconn.IsProxyError(err):
		return "прокси CANVAS_PROXY недоступен: проверьте его адрес и доступность"
	case canvasconn.IsTimeout(err):
		return "Canvas не ответил вовремя"
	default:
		return "Canvas недоступен"
	}
}

// --- view helpers ---

func (s *Server) assignments() []model.Assignment {
	rows, err := s.store.LoadAssignments()
	if err != nil {
		s.log.Error("load assignments failed", "err", err)
		return nil
	}
	return keep(rows, func(a model.Assignment) bool { return a.Active })
}

func keep(items []model.Assignment, f func(model.Assignment) bool) []model.Assignment {
	out := make([]model.Assignment, 0, len(items))
	for _, a := range items {
		if f(a) {
			out = append(out, a)
		}
	}
	return out
}

func isMissing(a model.Assignment, now time.Time) bool {
	return a.Active && !a.Submitted() && a.DueAt != nil && a.DueAt.After(now)
}

func isOverdue(a model.Assignment, now time.Time) bool {
	return a.Active && !a.Submitted() && a.DueAt != nil && a.DueAt.Before(now)
}

func isGraded(a model.Assignment) bool { return a.Active && a.Score != nil }

// assignmentStatus classifies a row for the UI. The colour is never the only
// signal: every status has its own label in the client.
func assignmentStatus(a model.Assignment, now time.Time) string {
	switch {
	case a.Submitted() && a.Late():
		return "submitted_late"
	case a.Submitted():
		return "submitted"
	case a.Score != nil:
		return "graded"
	case a.DueAt == nil:
		return "no_deadline"
	case a.DueAt.Before(now):
		return "overdue"
	case a.DueAt.Sub(now) < 24*time.Hour:
		return "today"
	case a.DueAt.Sub(now) < 72*time.Hour:
		return "soon"
	default:
		return "later"
	}
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		return
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "некорректный запрос")
		return false
	}
	return true
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// maskToken hides the middle of a token so it can be shown safely.
func maskToken(token string) string {
	if len(token) <= 8 {
		return "••••"
	}
	return token[:4] + "…" + token[len(token)-2:]
}

// cleanToken strips copy/paste noise from a pasted token.
func cleanToken(raw string) string {
	s := strings.TrimSpace(raw)
	lower := strings.ToLower(s)
	for _, p := range []string{"authorization: bearer ", "bearer ", "token: ", "canvas_api_token="} {
		if strings.HasPrefix(lower, p) {
			s = strings.TrimSpace(s[len(p):])
			break
		}
	}
	s = strings.TrimSpace(strings.Trim(s, "\"'`"))
	s = strings.TrimRight(s, ";,.")
	return strings.TrimSpace(strings.Trim(s, "\"'`"))
}

// plausibleToken rejects obviously wrong input before calling Canvas.
func plausibleToken(s string) bool {
	if len(s) < 10 {
		return false
	}
	if strings.ContainsAny(s, " \t\r\n<>") || strings.Contains(s, "://") {
		return false
	}
	return true
}
