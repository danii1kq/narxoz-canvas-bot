package store

import (
	"database/sql"
	"time"

	"narxoz-canvas-bot/internal/model"
)

// ChatToken is a Canvas API token supplied by a user inside a Telegram chat.
//
// Token is stored sealed (see internal/secrets) and must be opened by the
// caller before use. Name, Login and Email describe the Canvas user the token
// authenticated as; they exist for display and are never used as a credential.
// Verified is false while the token was accepted without a successful probe
// (Canvas was unreachable) and is flipped to true by the first successful call.
type ChatToken struct {
	ChatID   int64
	Token    string // sealed at rest
	Name     string
	Login    string
	Email    string
	Verified bool
	SavedAt  time.Time
}

// SaveChatToken stores (or replaces) the sealed Canvas token for a chat.
func (s *Store) SaveChatToken(chatID int64, sealedToken string, u model.User, verified bool) error {
	_, err := s.db.Exec(`INSERT INTO chat_tokens (chat_id, token, name, login, email, verified, saved_at)
VALUES (?,?,?,?,?,?,?)
ON CONFLICT(chat_id) DO UPDATE SET token=excluded.token, name=excluded.name,
    login=excluded.login, email=excluded.email, verified=excluded.verified,
    saved_at=excluded.saved_at`,
		chatID, sealedToken, u.Name, u.Login, u.Email, boolInt(verified), nowUTC().Format(time.RFC3339))
	return err
}

// SetChatTokenVerified records that a token proved usable (or that it needs
// re-checking) without touching the token itself.
func (s *Store) SetChatTokenVerified(chatID int64, verified bool) error {
	_, err := s.db.Exec("UPDATE chat_tokens SET verified=? WHERE chat_id=?", boolInt(verified), chatID)
	return err
}

// SetLatestChatTokenVerified marks the most recently saved token as verified.
// The background sync uses that token when CANVAS_API_TOKEN is not set, so a
// successful poll proves it works.
func (s *Store) SetLatestChatTokenVerified(verified bool) error {
	_, err := s.db.Exec(`UPDATE chat_tokens SET verified=? WHERE chat_id IN
(SELECT chat_id FROM chat_tokens ORDER BY saved_at DESC, chat_id DESC LIMIT 1)`, boolInt(verified))
	return err
}

// ChatToken returns the token stored for a chat, or ok=false when the chat
// never connected Canvas.
func (s *Store) ChatToken(chatID int64) (ChatToken, bool) {
	return s.scanChatToken(s.db.QueryRow(chatTokenCols+" FROM chat_tokens WHERE chat_id=?", chatID))
}

// LatestChatToken returns the most recently saved chat token. It is the token
// the background sync uses when no token is configured via CANVAS_API_TOKEN.
func (s *Store) LatestChatToken() (ChatToken, bool) {
	return s.scanChatToken(s.db.QueryRow(
		chatTokenCols + " FROM chat_tokens ORDER BY saved_at DESC, chat_id DESC LIMIT 1"))
}

// DropChatToken deletes the stored token for a chat.
func (s *Store) DropChatToken(chatID int64) error {
	_, err := s.db.Exec("DELETE FROM chat_tokens WHERE chat_id=?", chatID)
	return err
}

// ChatTokenCount returns how many chats have connected a Canvas token.
func (s *Store) ChatTokenCount() int {
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM chat_tokens").Scan(&n); err != nil {
		return 0
	}
	return n
}

func (s *Store) scanChatToken(row *sql.Row) (ChatToken, bool) {
	var t ChatToken
	var saved string
	var verified int
	if err := row.Scan(&t.ChatID, &t.Token, &t.Name, &t.Login, &t.Email, &verified, &saved); err != nil {
		return ChatToken{}, false
	}
	t.Verified = verified != 0
	if ts, perr := time.Parse(time.RFC3339, saved); perr == nil {
		t.SavedAt = ts
	}
	return t, true
}

// chatTokenCols lists the chat_tokens columns in scan order.
const chatTokenCols = "SELECT chat_id, token, name, login, email, verified, saved_at"
