package config_test

import (
	"testing"
	"time"

	"narxoz-canvas-bot/internal/config"
)

func clearOptional(t *testing.T) {
	t.Helper()
	for _, k := range []string{"TELEGRAM_CHAT_ID", "CANVAS_BASE_URL", "POLL_INTERVAL", "REMINDER_INTERVAL", "REMINDER_LEAD", "REQUEST_TIMEOUT", "TZ", "LOG_LEVEL", "DB_PATH", "CANVAS_TOKEN_SECRET", "TELEGRAM_API_ENDPOINT"} {
		t.Setenv(k, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "tg-token")
	t.Setenv("CANVAS_API_TOKEN", "cv-token")
	clearOptional(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CanvasBaseURL != "https://canvas.narxoz.kz" {
		t.Errorf("base url = %q", cfg.CanvasBaseURL)
	}
	if cfg.PollInterval != 5*time.Minute {
		t.Errorf("poll interval = %v", cfg.PollInterval)
	}
	if cfg.ReminderInterval != 30*time.Minute {
		t.Errorf("reminder interval = %v", cfg.ReminderInterval)
	}
	if cfg.ReminderLead != 24 {
		t.Errorf("reminder lead = %d", cfg.ReminderLead)
	}
	if cfg.TelegramChatID != 0 {
		t.Errorf("default chat id should be 0, got %d", cfg.TelegramChatID)
	}
	// Timezone must be UTC+5 (Asia/Almaty), whether via tzdata or fixed fallback.
	if cfg.TZ == nil {
		t.Fatal("timezone is nil")
	}
	utc := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, off := utc.In(cfg.TZ).Zone()
	if off != 5*3600 {
		t.Errorf("timezone offset = %d, want +5h (Asia/Almaty)", off)
	}
}

func TestLoadRequired(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("CANVAS_API_TOKEN", "cv-token")
	clearOptional(t)
	if _, err := config.Load(); err == nil {
		t.Fatal("expected error when TELEGRAM_BOT_TOKEN missing")
	}
}

// CanvasToken is optional: the token may be connected from the chat (/setup).
func TestLoadCanvasTokenOptional(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "tg-token")
	t.Setenv("CANVAS_API_TOKEN", "")
	clearOptional(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load without CANVAS_API_TOKEN: %v", err)
	}
	if cfg.CanvasToken != "" {
		t.Fatalf("canvas token = %q, want empty", cfg.CanvasToken)
	}
	if cfg.TokenSecret != "" {
		t.Fatalf("token secret = %q, want empty", cfg.TokenSecret)
	}

	t.Setenv("CANVAS_TOKEN_SECRET", "  s3cret  ")
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TokenSecret != "s3cret" {
		t.Fatalf("token secret = %q, want trimmed value", cfg.TokenSecret)
	}
}

func TestLoadInvalidChatID(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "tg-token")
	t.Setenv("CANVAS_API_TOKEN", "cv-token")
	clearOptional(t)
	t.Setenv("TELEGRAM_CHAT_ID", "not-a-number")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected error for invalid TELEGRAM_CHAT_ID")
	}
}

func TestLoadBareIntervalAsMinutes(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "tg-token")
	t.Setenv("CANVAS_API_TOKEN", "cv-token")
	clearOptional(t)
	t.Setenv("POLL_INTERVAL", "7")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollInterval != 7*time.Minute {
		t.Fatalf("poll interval = %v, want 7m", cfg.PollInterval)
	}
}

func TestLoadValidChatID(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "tg-token")
	t.Setenv("CANVAS_API_TOKEN", "cv-token")
	clearOptional(t)
	t.Setenv("TELEGRAM_CHAT_ID", "123456789")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TelegramChatID != 123456789 {
		t.Fatalf("chat id = %d", cfg.TelegramChatID)
	}
}
