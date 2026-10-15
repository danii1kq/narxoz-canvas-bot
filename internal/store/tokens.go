package store

import (
	"time"

	"narxoz-canvas-bot/internal/model"
)

// ChatToken is a Canvas API token supplied by a user inside a Telegram chat.
//
// Token is stored sealed (see internal/secrets) and must be opened by the
// caller before use. Name, Login and Email describe the Canvas user the token
// authenticated as; they exist for display and are never used as a credential.
type ChatToken struct {
	ChatID  int64
	Token   string // sealed at rest
	Name    string
	Login   string
	Email   string
	SavedAt time.Time
}

// SaveChatToken stores (or replaces) the sealed Canvas token for a chat.
func (s *Store) SaveChatToken(chatID int64, sealedToken string, u model.User) error {
	_, err := s.db.Exec(`INSERT INTO chat_tokens (chat_id, token, name, login, email, saved_at)
VALUES (?,?,?,?,?,?)
ON CONFLICT(chat_id) DO UPDATE SET token=excluded.token, name=excluded.name,
    login=excluded.login, email=excluded.email, saved_at=excluded.saved_at`,
		chatID, sealedToken, u.Name, u.Login, u.Email, nowUTC().Format(time.RFC3339))
	return err
}

// ChatToken returns the token stored for a chat, or ok=false when the chat
// never connected Canvas.
func (s *Store) ChatToken(chatID int64) (ChatToken, bool) {
	var t ChatToken
	var saved string
	err := s.db.QueryRow(chatTokenCols+" FROM chat_tokens WHERE chat_id=?", chatID).
		Scan(&t.ChatID, &t.Token, &t.Name, &t.Login, &t.Email, &saved)
	if err != nil {
		return ChatToken{}, false
	}
	if ts, perr := time.Parse(time.RFC3339, saved); perr == nil {
		t.SavedAt = ts
	}
	return t, true
}

// LatestChatToken returns the most recently saved chat token. It is the token
// the background sync uses when no token is configured via CANVAS_API_TOKEN.
func (s *Store) LatestChatToken() (ChatToken, bool) {
	var t ChatToken
	var saved string
	err := s.db.QueryRow(chatTokenCols+" FROM chat_tokens ORDER BY saved_at DESC, chat_id DESC LIMIT 1").
		Scan(&t.ChatID, &t.Token, &t.Name, &t.Login, &t.Email, &saved)
	if err != nil {
		return ChatToken{}, false
	}
	if ts, perr := time.Parse(time.RFC3339, saved); perr == nil {
		t.SavedAt = ts
	}
	return t, true
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

// chatTokenCols lists the chat_tokens columns in scan order.
const chatTokenCols = "SELECT chat_id, token, name, login, email, saved_at"
