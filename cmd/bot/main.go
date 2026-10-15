// Command bot is the entrypoint for the narxoz-canvas-bot Telegram bot.
//
// It loads configuration, opens the SQLite store, connects to Canvas and
// Telegram, then runs the poller and the interactive bot until it receives
// SIGINT/SIGTERM, after which it finishes the in-flight poll and closes the DB.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"narxoz-canvas-bot/internal/bot"
	"narxoz-canvas-bot/internal/canvas"
	"narxoz-canvas-bot/internal/config"
	"narxoz-canvas-bot/internal/notify"
	"narxoz-canvas-bot/internal/poller"
	"narxoz-canvas-bot/internal/runtime"
	"narxoz-canvas-bot/internal/secrets"
	"narxoz-canvas-bot/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal error", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Error("failed to open store", "err", err)
		return err
	}
	defer st.Close()

	tg, err := notify.NewTelegram(cfg.TelegramToken, cfg.TelegramAPIEndpoint, log)
	if err != nil {
		log.Error("telegram authentication failed (check TELEGRAM_BOT_TOKEN)", "err", err)
		return err
	}

	fm := notify.NewFormatter(cfg.TZ)
	stats := runtime.New()
	box := secrets.NewBox(cfg.TokenSecret)
	cv := canvas.New(cfg.CanvasBaseURL, cfg.CanvasToken, 10, cfg.RequestTimeout, log)

	// CANVAS_API_TOKEN is optional: when it is empty, restore the token a user
	// connected from the chat so the background sync keeps working across
	// restarts.
	tokenSource := "none"
	switch {
	case cfg.CanvasToken != "":
		tokenSource = "env"
	default:
		if tok, ok := st.LatestChatToken(); ok {
			plain, oerr := box.Open(tok.Token)
			if oerr != nil {
				log.Error("stored canvas token could not be opened", "chat_id", tok.ChatID, "err", oerr)
				break
			}
			cv.SetToken(plain)
			tokenSource = "chat"
			log.Info("canvas token restored from chat setup",
				"chat_id", tok.ChatID, "saved_at", tok.SavedAt.Format(time.RFC3339))
		}
	}
	if !cv.HasToken() {
		log.Warn("canvas is not connected yet; users can connect it with /setup in the chat")
	}

	p := poller.New(cfg, cv, st, tg, fm, stats, log)
	b := bot.New(tg, st, cv, p, box, fm, stats, log, cfg.TZ, cfg.TelegramChatID, cfg.CanvasToken, cfg.RequestTimeout)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		p.Run(ctx)
	}()
	go func() {
		defer wg.Done()
		b.Run(ctx)
	}()

	log.Info("narxoz-canvas-bot started",
		"poll_interval", cfg.PollInterval.String(),
		"reminder_interval", cfg.ReminderInterval.String(),
		"tz", cfg.TZ.String(),
		"db", cfg.DBPath,
		"telegram_username", tg.API().Self.UserName,
		"canvas_token", tokenSource,
		"token_encryption", box.Enabled(),
	)

	<-ctx.Done()
	log.Info("shutdown signal received, finishing in-flight work...")
	wg.Wait()
	log.Info("shutdown complete")
	return nil
}

// newLogger builds a JSON slog logger with the requested level.
func newLogger(level string) *slog.Logger {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn", "warning":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv})
	return slog.New(h)
}
