package notify

import (
	"errors"
	"net/http"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// A permanent rejection must not be retried: retrying a "message is too long"
// only blocks the bot for several seconds and fails again every time.
func TestIsPermanent(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"bad request", &tgbotapi.Error{Code: http.StatusBadRequest, Message: "message is too long"}, true},
		{"not found", &tgbotapi.Error{Code: http.StatusNotFound, Message: "chat not found"}, true},
		{"forbidden", &tgbotapi.Error{Code: http.StatusForbidden, Message: "bot was blocked"}, true},
		{"too many requests", &tgbotapi.Error{Code: http.StatusTooManyRequests, Message: "retry after"}, false},
		{"server error", &tgbotapi.Error{Code: http.StatusBadGateway, Message: "bad gateway"}, false},
		{"network error", errors.New("dial tcp: connection refused"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPermanent(tc.err); got != tc.want {
				t.Fatalf("isPermanent(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
