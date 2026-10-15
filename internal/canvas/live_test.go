package canvas_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"narxoz-canvas-bot/internal/canvas"
)

// TestLiveCanvasProbe talks to the real Canvas instance with a deliberately
// invalid token. It is skipped unless LIVE_CANVAS=1, so the default test run
// stays hermetic. Use it to confirm that the instance is reachable and that a
// bad token is classified as ErrUnauthorized (which is what the chat setup
// dialog shows to the user):
//
//	LIVE_CANVAS=1 go test ./internal/canvas -run TestLiveCanvasProbe -v
func TestLiveCanvasProbe(t *testing.T) {
	if os.Getenv("LIVE_CANVAS") != "1" {
		t.Skip("set LIVE_CANVAS=1 to probe the real Canvas instance")
	}
	base := os.Getenv("LIVE_CANVAS_URL")
	if base == "" {
		base = "https://canvas.narxoz.kz"
	}
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	c := canvas.New(base, "invalid-token-for-probe", 10, 20*time.Second, log)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := c.User(ctx)
	t.Logf("%s probe returned: %v", base, err)
	if !errors.Is(err, canvas.ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized from the real instance, got %v", err)
	}
}
