// Package canvasconn owns the Canvas connection state that the Telegram bot
// and the Mini App both work with: the per-chat API tokens stored in SQLite,
// which token drives the background sync, and the operations that change them.
//
// The Telegram-specific wording stays in the bot; everything here is transport
// agnostic so both front-ends share one implementation.
package canvasconn

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"narxoz-canvas-bot/internal/canvas"
	"narxoz-canvas-bot/internal/model"
	"narxoz-canvas-bot/internal/secrets"
	"narxoz-canvas-bot/internal/store"
)

// Syncer refreshes the Canvas dataset on demand (implemented by the poller).
type Syncer interface {
	PollOnce(ctx context.Context) error
}

// Conn wires the Canvas client, the token store and the sync together.
type Conn struct {
	client   *canvas.Client
	store    *store.Store
	box      *secrets.Box
	syncer   Syncer
	envToken string
	log      *slog.Logger

	// activeChat is the chat whose token the shared client currently uses
	// for the background sync (0 when the sync runs on the token from the
	// environment or on no token at all).
	activeChat atomic.Int64
}

// New constructs a Conn. envToken is CANVAS_API_TOKEN as read from the
// environment: when it is non-empty it pins the background sync and tokens
// entered by users never replace it.
func New(client *canvas.Client, st *store.Store, box *secrets.Box, syncer Syncer, envToken string, log *slog.Logger) *Conn {
	return &Conn{
		client:   client,
		store:    st,
		box:      box,
		syncer:   syncer,
		envToken: strings.TrimSpace(envToken),
		log:      log,
	}
}

// FromEnv reports whether CANVAS_API_TOKEN pins the background sync.
func (c *Conn) FromEnv() bool { return c.envToken != "" }

// BaseURL returns the configured Canvas instance URL.
func (c *Conn) BaseURL() string { return c.client.BaseURL() }

// Client returns the Canvas client to use for an on-demand request from a
// chat: the chat's own token when it has one, the shared client otherwise.
func (c *Conn) Client(chatID int64) *canvas.Client {
	if tok, _, ok := c.ChatToken(chatID); ok {
		return c.client.WithToken(tok)
	}
	return c.client
}

// ChatToken returns the plaintext Canvas token stored for a chat together with
// its stored metadata (who it belongs to, whether it was verified).
func (c *Conn) ChatToken(chatID int64) (string, store.ChatToken, bool) {
	row, ok := c.store.ChatToken(chatID)
	if !ok {
		return "", store.ChatToken{}, false
	}
	plain, err := c.box.Open(row.Token)
	if err != nil {
		c.log.Error("open stored canvas token failed", "chat", chatID, "err", err)
		return "", store.ChatToken{}, false
	}
	return plain, row, true
}

// HasToken reports whether a readable token is stored for the chat.
func (c *Conn) HasToken(chatID int64) bool {
	_, _, ok := c.ChatToken(chatID)
	return ok
}

// Configured reports whether a chat can show Canvas data: either a token came
// from the environment or the chat connected its own.
func (c *Conn) Configured(chatID int64) bool {
	return c.client.HasToken() || c.HasToken(chatID)
}

// StateLine renders the Canvas token source, used by /status and the app.
func (c *Conn) StateLine(chatID int64) string {
	if c.FromEnv() {
		return "из .env (CANVAS_API_TOKEN)"
	}
	if _, row, ok := c.ChatToken(chatID); ok {
		if row.Verified {
			return "подключён в чате, подтверждён ✅"
		}
		return "подключён в чате, ждёт проверки ⏳"
	}
	if c.client.HasToken() {
		return "подключён в другом чате"
	}
	return "не подключён — нажмите /setup"
}

// Probe checks a token against Canvas and returns the authenticated user.
func (c *Conn) Probe(ctx context.Context, token string) (model.User, error) {
	return c.client.WithToken(token).User(ctx)
}

// Save probes the token and, when Canvas accepts it, stores it for the chat
// (marking it verified).
//
// The token is not adopted for the background sync: only the chat flow does
// that. A token entered in the Mini App must not silently take over the sync
// of the whole bot.
func (c *Conn) Save(ctx context.Context, chatID int64, token string) (model.User, error) {
	u, err := c.Probe(ctx, token)
	if err != nil {
		return model.User{}, err
	}
	if err := c.Persist(chatID, token, u, true, false); err != nil {
		return model.User{}, err
	}
	return u, nil
}

// Persist stores a token for a chat. verified=false means Canvas could not be
// reached at the time (the token is kept and checked in the background);
// verified=true means a probe already succeeded.
//
// adopt tells whether the token may become the token of the shared background
// sync. Without a token from .env the first chat token drives that sync (the
// store holds one shared dataset), but only the chat flow may claim it.
func (c *Conn) Persist(chatID int64, token string, u model.User, verified, adopt bool) error {
	sealed, err := c.box.Seal(token)
	if err != nil {
		return err
	}
	if err := c.store.SaveChatToken(chatID, sealed, u, verified); err != nil {
		return err
	}
	if adopt {
		// Without a token from .env the first chat token drives the background
		// sync (the store holds one shared dataset).
		c.AdoptTokenIfOwner(chatID, token)
	}
	return nil
}

// ActiveChatID returns the chat whose token currently drives the background
// sync, 0 when the sync uses the token from the environment or no token.
func (c *Conn) ActiveChatID() int64 { return c.activeChat.Load() }

// SetToken puts a token into the shared client without a chat owner. It is
// used for a token that does not belong to a chat (the environment or a
// restore from the database).
func (c *Conn) SetToken(token string) {
	c.client.SetToken(token)
	c.activeChat.Store(0)
}

// AdoptTokenIfOwner hands the shared client the chat token when nothing else
// is configured for the background sync.
func (c *Conn) AdoptTokenIfOwner(chatID int64, token string) {
	if c.FromEnv() || c.client.HasToken() {
		return
	}
	c.client.SetToken(token)
	c.activeChat.Store(chatID)
}

// Drop removes the chat's token and re-points the background sync at another
// chat token, or stops it. stopped reports whether Canvas polling was stopped
// (the caller may tell the user).
func (c *Conn) Drop(chatID int64) (stopped bool, err error) {
	if err := c.store.DropChatToken(chatID); err != nil {
		return false, err
	}
	if c.FromEnv() {
		return false, nil
	}
	if latest, ok := c.store.LatestChatToken(); ok {
		if plain, oerr := c.box.Open(latest.Token); oerr == nil {
			c.client.SetToken(plain)
			c.activeChat.Store(latest.ChatID)
			return false, nil
		}
	}
	c.client.SetToken("")
	c.activeChat.Store(0)
	return true, nil
}

// LatestChatID returns the chat whose token currently drives the background
// sync (0 when the sync runs on an environment token or none at all).
func (c *Conn) LatestChatID() int64 {
	if c.FromEnv() {
		return 0
	}
	if latest, ok := c.store.LatestChatToken(); ok {
		return latest.ChatID
	}
	return 0
}

// EnsureSyncToken makes sure the background sync has a token, adopting the
// chat's own token when nothing is configured yet. It reports whether Canvas
// can be queried at all.
func (c *Conn) EnsureSyncToken(chatID int64) bool {
	if c.client.HasToken() {
		return true
	}
	if tok, _, ok := c.ChatToken(chatID); ok {
		c.client.SetToken(tok)
		c.activeChat.Store(chatID)
		return true
	}
	return false
}

// Refresh pulls fresh data from Canvas and marks the chat's token as verified
// once Canvas answered.
func (c *Conn) Refresh(ctx context.Context, chatID int64) error {
	if err := c.syncer.PollOnce(ctx); err != nil {
		return err
	}
	// Only the token that the sync actually used may be marked as verified.
	if c.HasToken(chatID) && c.ActiveChatID() == chatID {
		if err := c.store.SetChatTokenVerified(chatID, true); err != nil {
			c.log.Warn("could not mark chat token verified", "chat", chatID, "err", err)
		}
	}
	return nil
}

// Encrypted reports whether stored tokens are encrypted (not just obfuscated).
func (c *Conn) Encrypted() bool { return c.box.Enabled() }

// IsUnauthorized reports whether err means the token was rejected.
func IsUnauthorized(err error) bool { return errors.Is(err, canvas.ErrUnauthorized) }

// IsNoToken reports whether err means no token is configured.
func IsNoToken(err error) bool { return errors.Is(err, canvas.ErrNoToken) }

// IsTimeout reports whether err is a timeout or deadline error.
func IsTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded")
}

// IsProxyError reports whether the request failed while dialling or resolving
// the CANVAS_PROXY proxy (wrong address, placeholder left in .env, DNS down).
func IsProxyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "proxyconnect") || strings.Contains(msg, "proxy url") {
		return true
	}
	return strings.Contains(msg, "proxy") && (strings.Contains(msg, "lookup") || strings.Contains(msg, "dial tcp"))
}

// TimeoutText renders "за 30s" for a request timeout, for error hints.
func TimeoutText(d time.Duration) string {
	if d <= 0 {
		d = 30 * time.Second
	}
	return d.Round(time.Second).String()
}
