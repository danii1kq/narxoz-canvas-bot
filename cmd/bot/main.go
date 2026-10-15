// Command bot is the entrypoint for the narxoz-canvas-bot Telegram bot and its
// Mini App.
//
// It loads configuration, opens the SQLite store, connects to Canvas and
// Telegram, then runs the poller, the bot and (when configured) the Mini App
// HTTP server until it receives SIGINT/SIGTERM, after which it finishes the
// in-flight work and closes the DB.
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
	"narxoz-canvas-bot/internal/canvasconn"
	"narxoz-canvas-bot/internal/config"
	"narxoz-canvas-bot/internal/notify"
	"narxoz-canvas-bot/internal/poller"
	"narxoz-canvas-bot/internal/runtime"
	"narxoz-canvas-bot/internal/secrets"
	"narxoz-canvas-bot/internal/store"
	"narxoz-canvas-bot/internal/webapp"
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
	cv := canvas.New(cfg.CanvasBaseURL, cfg.CanvasToken, 10, cfg.RequestTimeout, log, canvas.WithProxy(cfg.CanvasProxy))

	// CANVAS_API_TOKEN is optional: when it is empty, restore the token a user
	// connected from the chat/app so the background sync keeps working across
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

	p := poller.New(cfg, cv, st, tg, fm, stats, log)
	conn := canvasconn.New(cv, st, box, p, cfg.CanvasToken, log)
	if !cv.HasToken() {
		log.Warn("canvas is not connected yet; connect it with /setup in the chat or in the Mini App")
	}

	b := bot.New(tg, st, conn, fm, stats, log, cfg.TZ, cfg.TelegramChatID, cfg.WebAppURL, cfg.RequestTimeout)

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

	// The Mini App runs in the same process and serves the same data. Its
	// public URL is advertised to Telegram as the chat menu button ("Open").
	var app *webapp.Server
	if cfg.WebAppURL != "" || cfg.WebAppDevChatID != 0 {
		auth := webapp.NewAuth(cfg.TelegramToken, cfg.WebAppDevChatID)
		app = webapp.New(webapp.Config{
			Addr:            cfg.WebAppAddr,
			AppURL:          cfg.WebAppURL,
			OwnerChatID:     cfg.TelegramChatID,
			LeadHours:       cfg.ReminderLead,
			RequestTimeout:  cfg.RequestTimeout,
			RefreshCooldown: 20 * time.Second,
		}, st, conn, stats, fm, auth, log)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := app.Run(ctx); err != nil {
				log.Error("mini app stopped", "err", err)
			}
		}()
		if cfg.WebAppDevChatID != 0 {
			log.Warn("WEBAPP_DEV_CHAT_ID is set: the Mini App accepts unauthenticated local requests",
				"dev_chat_id", cfg.WebAppDevChatID)
		}
		if cfg.WebAppURL != "" {
			setMenuButton(ctx, tg, cfg.WebAppURL, log)
		}
	}

	log.Info("narxoz-canvas-bot started",
		"poll_interval", cfg.PollInterval.String(),
		"reminder_interval", cfg.ReminderInterval.String(),
		"tz", cfg.TZ.String(),
		"db", cfg.DBPath,
		"telegram_username", tg.API().Self.UserName,
		"canvas_token", tokenSource,
		"token_encryption", box.Enabled(),
		"webapp_url", cfg.WebAppURL,
		"webapp_addr", cfg.WebAppAddr,
	)

	<-ctx.Done()
	log.Info("shutdown signal received, finishing in-flight work...")
	wg.Wait()
	log.Info("shutdown complete")
	return nil
}

// setMenuButton installs the "Open" Mini App button next to the message input.
func setMenuButton(ctx context.Context, tg *notify.Client, appURL string, log *slog.Logger) {
	done := make(chan error, 1)
	go func() { done <- tg.SetMenuButton(0, "Открыть", appURL) }()
	select {
	case err := <-done:
		if err != nil {
			log.Error("could not set the chat menu button", "err", err, "url", appURL)
			return
		}
		log.Info("chat menu button points at the mini app", "url", appURL)
	case <-ctx.Done():
	case <-time.After(15 * time.Second):
		log.Error("setting the chat menu button timed out", "url", appURL)
	}
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
