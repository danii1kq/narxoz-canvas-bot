package bot

import (
	"testing"

	"narxoz-canvas-bot/internal/model"
)

func TestCleanToken(t *testing.T) {
	const want = "1234~AbCdEfGhIjKlMnOpQrStUvWxYz"
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", want, want},
		{"spaces around", "  " + want + "  ", want},
		{"double quotes", `"` + want + `"`, want},
		{"single quotes", "'" + want + "'", want},
		{"bearer prefix", "Bearer " + want, want},
		{"authorization header", "Authorization: Bearer " + want, want},
		{"token label", "token: " + want, want},
		{"env assignment", "CANVAS_API_TOKEN=" + want, want},
		{"trailing semicolon", want + ";", want},
		{"trailing dot and quote", want + `".`, want},
		{"empty", "   ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanToken(tt.in); got != tt.want {
				t.Fatalf("cleanToken(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestPlausibleToken(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"canvas-like", "1234~AbCdEfGhIjKlMnOpQrStUvWxYz0123456789", true},
		{"bare minimum", "abcdefghij", true},
		{"too short", "abc123", false},
		{"empty", "", false},
		{"with spaces", "1234~abc def ghijkl mnop", false},
		{"html", "<b>token</b>", false},
		{"url", "https://canvas.narxoz.kz/profile/settings", false},
		{"multi line", "1234~abcdefghij\nsecond", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := plausibleToken(tt.in); got != tt.want {
				t.Fatalf("plausibleToken(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestMaskToken(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"1234~AbCdEfGhIjKlMnOpQrStUvWxYz", "1234…Yz"},
		{"short", "••••"},
		{"", "••••"},
	}
	for _, tt := range tests {
		if got := maskToken(tt.in); got != tt.want {
			t.Errorf("maskToken(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCanvasUserLabel(t *testing.T) {
	if got := canvasUserLabel(model.User{Name: "Ann", Login: "ann"}); got != "Ann" {
		t.Errorf("label = %q, want Ann", got)
	}
	if got := canvasUserLabel(model.User{Login: "ann"}); got != "ann" {
		t.Errorf("label = %q, want ann", got)
	}
	if got := canvasUserLabel(model.User{}); got != "пользователь Canvas" {
		t.Errorf("label = %q", got)
	}
}

func TestPendingState(t *testing.T) {
	b := &Bot{}
	if b.isPending(10) {
		t.Fatal("chat must not be pending initially")
	}
	b.setPending(10, true)
	if !b.isPending(10) {
		t.Fatal("chat must be pending after setPending(true)")
	}
	if b.isPending(11) {
		t.Fatal("other chats must not be affected")
	}
	b.setPending(10, false)
	if b.isPending(10) {
		t.Fatal("chat must not be pending after setPending(false)")
	}
}

func TestCanvasStatusLine(t *testing.T) {
	tests := []struct {
		ok     bool
		errMsg string
		want   string
	}{
		{true, "", "✅ доступен"},
		{false, "no token", "🔴 не подключён"},
		{false, "unauthorized", "🔴 токен отклонён"},
		{false, "", "⚪ ещё не проверяли"},
		{false, "dial tcp: timeout", "🔴 ошибка (dial tcp: timeout)"},
	}
	for _, tt := range tests {
		if got := canvasStatusLine(tt.ok, tt.errMsg); got != tt.want {
			t.Errorf("canvasStatusLine(%v,%q) = %q, want %q", tt.ok, tt.errMsg, got, tt.want)
		}
	}
}
