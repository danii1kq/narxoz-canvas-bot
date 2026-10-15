// Package notify handles Telegram delivery and Russian-language message
// formatting. All user-facing text (notifications, menus, lists, errors) is
// written in Russian.
package notify

import (
	"log/slog"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Client is a retrying wrapper around the Telegram Bot API.
type Client struct {
	api     *tgbotapi.BotAPI
	log     *slog.Logger
	retries int
}

// NewTelegram authenticates with the Bot API (calls getMe) and returns a Client.
// An empty endpoint uses https://api.telegram.org; a non-empty one is used for
// self-hosted Bot API servers and for tests.
func NewTelegram(token, endpoint string, log *slog.Logger) (*Client, error) {
	var api *tgbotapi.BotAPI
	var err error
	if endpoint == "" {
		api, err = tgbotapi.NewBotAPI(token)
	} else {
		api, err = tgbotapi.NewBotAPIWithAPIEndpoint(token, endpoint)
	}
	if err != nil {
		return nil, err
	}
	return &Client{api: api, log: log, retries: 5}, nil
}

// API exposes the underlying BotAPI instance (used to poll for updates).
func (c *Client) API() *tgbotapi.BotAPI { return c.api }

// send executes a chattable request with exponential backoff. ErrTolerated
// results are treated as success by callers of EditInline.
func (c *Client) send(cfg tgbotapi.Chattable) error {
	backoff := 400 * time.Millisecond
	var err error
	for i := 1; i <= c.retries; i++ {
		_, err = c.api.Send(cfg)
		if err == nil {
			return nil
		}
		if isTolerated(err) {
			return errTolerated
		}
		if i == c.retries {
			break
		}
		c.log.Warn("telegram send failed, retrying", "attempt", i, "err", err)
		time.Sleep(backoff)
		backoff *= 2
		if backoff > 8*time.Second {
			backoff = 8 * time.Second
		}
	}
	return err
}

// SendHTML sends an HTML-formatted text message (no keyboard).
func (c *Client) SendHTML(chatID int64, text string) error {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.DisableWebPagePreview = true
	return c.send(msg)
}

// SendMenu sends a text message with the persistent reply keyboard.
func (c *Client) SendMenu(chatID int64, text string, kb tgbotapi.ReplyKeyboardMarkup) error {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	markup := kb
	msg.ReplyMarkup = markup
	return c.send(msg)
}

// SendInline sends a text message with an inline keyboard.
func (c *Client) SendInline(chatID int64, text string, kb tgbotapi.InlineKeyboardMarkup) error {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.DisableWebPagePreview = true
	markup := kb
	msg.ReplyMarkup = markup
	return c.send(msg)
}

// EditInline edits an existing message (text + inline keyboard). Stale taps
// that produce "message is not modified" / "message to edit not found" are
// swallowed so the bot never crashes on a double-tap.
func (c *Client) EditInline(chatID int64, messageID int, text string, kb tgbotapi.InlineKeyboardMarkup) error {
	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	edit.ParseMode = tgbotapi.ModeHTML
	edit.DisableWebPagePreview = true
	markup := kb
	edit.ReplyMarkup = &markup
	err := c.send(edit)
	if err == errTolerated {
		return nil
	}
	return err
}

// DeleteMessage removes a message from a chat. It is used to erase the chat
// message that carried a pasted Canvas API token as soon as it has been read.
//
// The Bot API answers deleteMessage with "result": true, which Send cannot
// decode into a Message, so the request is issued directly and best-effort: a
// message that is already gone is not an error.
func (c *Client) DeleteMessage(chatID int64, messageID int) error {
	if messageID == 0 {
		return nil
	}
	_, err := c.api.Request(tgbotapi.NewDeleteMessage(chatID, messageID))
	if err != nil && isTolerated(err) {
		return nil
	}
	return err
}

// AnswerCallback dismisses the loading indicator on a pressed inline button.
func (c *Client) AnswerCallback(id, text string, alert bool) {
	cb := tgbotapi.NewCallback(id, text)
	cb.ShowAlert = alert
	var err error
	for i := 0; i < 2; i++ {
		if _, err = c.api.Request(cb); err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	c.log.Warn("answerCallbackQuery failed", "err", err)
}

// errTolerated is a sentinel returned by send for benign, ignorable errors.
var errTolerated = &toleratedError{}

type toleratedError struct{}

func (toleratedError) Error() string { return "tolerated" }

// isTolerated reports whether err is a benign "not modified / too old" error.
func isTolerated(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "message is not modified"),
		strings.Contains(msg, "message to edit not found"),
		strings.Contains(msg, "query is too old"),
		strings.Contains(msg, "message can't be edited"),
		strings.Contains(msg, "message to delete not found"):
		return true
	}
	return false
}
