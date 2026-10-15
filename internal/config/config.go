// Package config loads and validates runtime configuration from environment
// variables. Configuration is validated at startup; the process exits if a
// required value is missing or malformed.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds validated runtime configuration.
type Config struct {
	// Required.
	TelegramToken string

	// Optional. CanvasToken may be left empty: the bot then asks for it inside
	// the Telegram chat (/setup) and keeps it per chat. TokenSecret enables
	// AES-256-GCM encryption of chat-entered tokens at rest.
	CanvasToken   string
	TokenSecret   string
	CanvasBaseURL string

	// Optional / defaulted.
	TelegramChatID int64 // primary notification target; 0 means unset
	// TelegramAPIEndpoint overrides the Bot API root (self-hosted Bot API
	// servers and tests); empty means https://api.telegram.org.
	TelegramAPIEndpoint string
	PollInterval        time.Duration
	ReminderInterval    time.Duration
	ReminderLead        int // early-reminder lead time in hours (6, 12 or 24)
	TZ                  *time.Location
	LogLevel            string
	DBPath              string
	RequestTimeout      time.Duration

	// Telegram Mini App. WebAppURL is the public HTTPS address of the app
	// (e.g. https://narxoz-bot.duckdns.org) used for the chat menu button;
	// when empty the app is not advertised in Telegram. WebAppAddr is the
	// local listen address behind the HTTPS reverse proxy.
	WebAppURL  string
	WebAppAddr string
	// WebAppDevChatID authenticates a browser as this chat without Telegram
	// initData. Development only: never set it on a public deployment.
	WebAppDevChatID int64
}

// Load reads configuration from the environment and validates it.
func Load() (Config, error) {
	var c Config
	var err error
	c.TelegramToken = strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	c.CanvasToken = strings.TrimSpace(os.Getenv("CANVAS_API_TOKEN"))
	c.TokenSecret = strings.TrimSpace(os.Getenv("CANVAS_TOKEN_SECRET"))
	c.TelegramAPIEndpoint = strings.TrimSpace(os.Getenv("TELEGRAM_API_ENDPOINT"))
	if c.TelegramToken == "" {
		return c, fmt.Errorf("TELEGRAM_BOT_TOKEN is required")
	}
	// CANVAS_API_TOKEN is optional: when it is empty the bot asks for a token
	// inside the chat (/setup) and persists it in the database.

	c.CanvasBaseURL = envOr("CANVAS_BASE_URL", "https://canvas.narxoz.kz")
	c.CanvasBaseURL = strings.TrimRight(c.CanvasBaseURL, "/")

	if c.PollInterval, err = envDuration("POLL_INTERVAL", 5*time.Minute); err != nil {
		return c, err
	}
	if c.ReminderInterval, err = envDuration("REMINDER_INTERVAL", 30*time.Minute); err != nil {
		return c, err
	}
	if c.ReminderLead, err = envLead("REMINDER_LEAD", 24); err != nil {
		return c, err
	}
	if c.RequestTimeout, err = envDuration("REQUEST_TIMEOUT", 30*time.Second); err != nil {
		return c, err
	}

	// TELEGRAM_CHAT_ID is optional: when set it becomes the primary
	// notification target; when unset notifications are delivered to every
	// chat that pressed /start.
	if v := strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_ID")); v != "" {
		id, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil {
			return c, fmt.Errorf("TELEGRAM_CHAT_ID must be an integer, got %q", v)
		}
		c.TelegramChatID = id
	}

	c.TZ = loadLocation(envOr("TZ", "Asia/Almaty"))
	c.LogLevel = envOr("LOG_LEVEL", "info")
	c.DBPath = envOr("DB_PATH", "./bot.db")

	c.WebAppURL = strings.TrimRight(strings.TrimSpace(os.Getenv("WEBAPP_URL")), "/")
	c.WebAppAddr = envOr("WEBAPP_ADDR", ":8080")
	if v := strings.TrimSpace(os.Getenv("WEBAPP_DEV_CHAT_ID")); v != "" {
		id, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil {
			return c, fmt.Errorf("WEBAPP_DEV_CHAT_ID must be an integer, got %q", v)
		}
		c.WebAppDevChatID = id
	}
	// Telegram only opens Mini Apps over HTTPS.
	if c.WebAppURL != "" && !strings.HasPrefix(c.WebAppURL, "https://") {
		return c, fmt.Errorf("WEBAPP_URL must start with https:// (got %q)", c.WebAppURL)
	}

	if c.PollInterval < 10*time.Second {
		return c, fmt.Errorf("POLL_INTERVAL must be at least 10s")
	}
	if c.ReminderLead != 6 && c.ReminderLead != 12 && c.ReminderLead != 24 {
		return c, fmt.Errorf("REMINDER_LEAD must be one of 6, 12, 24")
	}
	return c, nil
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	// Allow bare integers to be interpreted as a number of minutes for
	// convenience (POLL_INTERVAL=5 => 5m), like the config examples use.
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Minute, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid duration %q", key, v)
	}
	return d, nil
}

func envLead(key string, def int) (int, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid integer %q", key, v)
	}
	switch n {
	case 6, 12, 24:
		return n, nil
	}
	return 0, fmt.Errorf("%s must be one of 6, 12, 24", key)
}

// loadLocation resolves an IANA location, falling back to a fixed UTC+5 zone
// (Asia/Almaty has had no DST since 1991) so the binary works without tzdata.
func loadLocation(name string) *time.Location {
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	if name == "Asia/Almaty" {
		return time.FixedZone("Asia/Almaty", 5*60*60)
	}
	return time.UTC
}
