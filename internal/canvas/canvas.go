// Package canvas implements a resilient HTTP client for the Canvas LMS API.
//
// It honours the Canvas rate limit (~10 req/sec) with golang.org/x/time/rate,
// retries on 429/5xx with exponential backoff, enforces a per-request timeout
// via context, and follows all pagination pages via the Link header.
package canvas

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"narxoz-canvas-bot/internal/model"
)

// Sentinel errors returned by the client.
var (
	// ErrUnauthorized is returned on HTTP 401/403 (invalid or expired token).
	ErrUnauthorized = errors.New("canvas: unauthorized (invalid or expired token)")
	// ErrNotFound is returned on HTTP 404.
	ErrNotFound = errors.New("canvas: resource not found")
	// ErrNoToken is returned when no API token is configured, i.e. the user
	// has not connected Canvas yet (run /setup in the bot).
	ErrNoToken = errors.New("canvas: no API token configured")
)

// Client is a rate-limited Canvas API client.
type Client struct {
	baseURL    string
	token      atomic.Pointer[string]
	http       *http.Client
	limiter    *rate.Limiter
	log        *slog.Logger
	maxRetries int
}

// New creates a Canvas client. requestsPerSecond throttles outbound calls and
// requestTimeout bounds each HTTP request. The token may be empty; see
// SetToken and ErrNoToken.
func New(baseURL, token string, requestsPerSecond float64, requestTimeout time.Duration, log *slog.Logger) *Client {
	if requestsPerSecond <= 0 {
		requestsPerSecond = 10
	}
	if requestTimeout <= 0 {
		requestTimeout = 30 * time.Second
	}
	burst := int(requestsPerSecond)
	if burst < 1 {
		burst = 1
	}
	limiter := rate.NewLimiter(rate.Every(time.Duration(float64(time.Second)/requestsPerSecond)), burst)
	return newClient(baseURL, token, limiter, &http.Client{Timeout: requestTimeout}, log)
}

// newClient assembles a client from already-built parts.
func newClient(baseURL, token string, limiter *rate.Limiter, httpClient *http.Client, log *slog.Logger) *Client {
	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		http:       httpClient,
		limiter:    limiter,
		log:        log,
		maxRetries: 5,
	}
	c.SetToken(token)
	return c
}

// WithToken returns a client that authenticates with token while sharing this
// client's base URL, connection pool, rate limiter and logging. It is used to
// probe a token supplied by a user before it becomes the active one.
func (c *Client) WithToken(token string) *Client {
	return newClient(c.baseURL, token, c.limiter, c.http, c.log)
}

// SetToken replaces the API token used for subsequent requests. An empty
// token disables all calls (they fail with ErrNoToken).
func (c *Client) SetToken(token string) {
	t := strings.TrimSpace(token)
	c.token.Store(&t)
}

// Token returns the API token currently in use. Never log it.
func (c *Client) Token() string {
	if p := c.token.Load(); p != nil {
		return *p
	}
	return ""
}

// HasToken reports whether an API token is configured.
func (c *Client) HasToken() bool { return c.Token() != "" }

// BaseURL returns the configured Canvas instance URL.
func (c *Client) BaseURL() string { return c.baseURL }

// User validates the token and returns the authenticated user.
func (c *Client) User(ctx context.Context) (model.User, error) {
	body, _, err := c.do(ctx, c.url("/api/v1/users/self", nil))
	if err != nil {
		return model.User{}, err
	}
	var d userDTO
	if err := json.Unmarshal(body, &d); err != nil {
		return model.User{}, fmt.Errorf("canvas: decode user: %w", err)
	}
	return d.toModel(), nil
}

// Courses returns all active courses, following pagination.
func (c *Client) Courses(ctx context.Context) ([]model.Course, error) {
	q := url.Values{}
	q.Set("enrollment_state", "active")
	q.Set("per_page", "100")
	var out []model.Course
	err := c.getAll(ctx, c.url("/api/v1/courses", q), func(b []byte) error {
		var page []courseDTO
		if err := json.Unmarshal(b, &page); err != nil {
			return fmt.Errorf("canvas: decode courses: %w", err)
		}
		for _, d := range page {
			out = append(out, d.toModel())
		}
		return nil
	})
	return out, err
}

// Assignments returns all assignments for a course with the embedded
// submission, following pagination.
func (c *Client) Assignments(ctx context.Context, courseID int64) ([]model.Assignment, error) {
	q := url.Values{}
	q.Set("include[]", "submission")
	q.Set("per_page", "100")
	q.Set("order_by", "due_at")
	var out []model.Assignment
	path := "/api/v1/courses/" + strconv.FormatInt(courseID, 10) + "/assignments"
	err := c.getAll(ctx, c.url(path, q), func(b []byte) error {
		var page []assignmentDTO
		if err := json.Unmarshal(b, &page); err != nil {
			return fmt.Errorf("canvas: decode assignments: %w", err)
		}
		for _, d := range page {
			a := d.toModel()
			if a.CourseID == 0 {
				a.CourseID = courseID
			}
			out = append(out, a)
		}
		return nil
	})
	return out, err
}

// url builds an absolute Canvas API URL.
func (c *Client) url(path string, q url.Values) string {
	u := c.baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// getAll walks every pagination page, invoking decode on each page body.
func (c *Client) getAll(ctx context.Context, firstURL string, decode func([]byte) error) error {
	next := firstURL
	const maxPages = 500 // safety valve against a misbehaving Link header
	for i := 0; i < maxPages && next != ""; i++ {
		body, nxt, err := c.do(ctx, next)
		if err != nil {
			return err
		}
		if err := decode(body); err != nil {
			return err
		}
		next = nxt
	}
	return nil
}

// do performs a single rate-limited GET with retries and returns the body
// plus the next pagination URL (empty when there is no next page).
func (c *Client) do(ctx context.Context, fullURL string) ([]byte, string, error) {
	token := c.Token()
	if token == "" {
		return nil, "", ErrNoToken
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, "", err
	}

	var lastErr error
	backoff := 400 * time.Millisecond
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, "", ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > 15*time.Second {
				backoff = 15 * time.Second
			}
			if err := c.limiter.Wait(ctx); err != nil {
				return nil, "", err
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
		if err != nil {
			return nil, "", err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")

		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, "", fmt.Errorf("canvas: GET %s: %w", fullURL, ctx.Err())
			}
			// Keep the URL and the transport cause in the error: it is shown
			// to the user when a chat setup probe fails.
			lastErr = fmt.Errorf("canvas: GET %s: %w", fullURL, err)
			c.log.Warn("canvas request failed, retrying", "url", fullURL, "err", err, "attempt", attempt+1)
			continue
		}

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			resp.Body.Close()
			return nil, "", ErrUnauthorized
		}

		next := parseNextLink(resp.Header.Get("Link"))

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			lastErr = fmt.Errorf("canvas: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			c.log.Warn("canvas retryable status", "status", resp.StatusCode, "url", fullURL, "attempt", attempt+1)
			continue
		}

		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			resp.Body.Close()
			if resp.StatusCode == http.StatusNotFound {
				return nil, "", ErrNotFound
			}
			return nil, "", fmt.Errorf("canvas: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		return body, next, nil
	}
	return nil, "", lastErr
}

// parseNextLink extracts the rel="next" URL from a Link header.
func parseNextLink(header string) string {
	if header == "" {
		return ""
	}
	for _, part := range strings.Split(header, ",") {
		segment := strings.TrimSpace(part)
		if !strings.Contains(segment, `rel="next"`) && !strings.Contains(segment, "rel=next") {
			continue
		}
		start := strings.IndexByte(segment, '<')
		end := strings.IndexByte(segment, '>')
		if start >= 0 && end > start {
			return segment[start+1 : end]
		}
	}
	return ""
}

// --- JSON DTOs ---

type courseDTO struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	CourseCode string `json:"course_code"`
}

func (d courseDTO) toModel() model.Course {
	return model.Course{ID: d.ID, Name: d.Name, Code: d.CourseCode}
}

type userDTO struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Login string `json:"login"`
	Email string `json:"email"`
}

func (d userDTO) toModel() model.User {
	return model.User{ID: d.ID, Name: d.Name, Login: d.Login, Email: d.Email}
}

type assignmentDTO struct {
	ID              int64          `json:"id"`
	CourseID        int64          `json:"course_id"`
	Name            string         `json:"name"`
	PointsPossible  *float64       `json:"points_possible"`
	DueAt           *time.Time     `json:"due_at"`
	SubmissionTypes []string       `json:"submission_types"`
	HTMLURL         string         `json:"html_url"`
	Submission      *submissionDTO `json:"submission"`
}

type submissionDTO struct {
	Score         *float64   `json:"score"`
	Grade         *string    `json:"grade"`
	SubmittedAt   *time.Time `json:"submitted_at"`
	GradedAt      *time.Time `json:"graded_at"`
	WorkflowState string     `json:"workflow_state"`
}

func (d assignmentDTO) toModel() model.Assignment {
	a := model.Assignment{
		CourseID: d.CourseID,
		ID:       d.ID,
		Name:     d.Name,
		Points:   d.PointsPossible,
		DueAt:    utc(d.DueAt),
		HTMLURL:  d.HTMLURL,
		Active:   true,
	}
	if d.Submission != nil {
		a.SubmittedAt = utc(d.Submission.SubmittedAt)
		a.GradedAt = utc(d.Submission.GradedAt)
		a.Score = d.Submission.Score
		a.Grade = d.Submission.Grade
		a.WorkflowState = d.Submission.WorkflowState
	}
	return a
}

// utc normalises a timestamp to UTC (or returns nil).
func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
