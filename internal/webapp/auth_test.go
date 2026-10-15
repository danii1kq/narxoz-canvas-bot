package webapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"
)

// signInitData builds a valid Telegram Mini App initData payload the same way
// Telegram does, so the verifier can be tested without Telegram.
func signInitData(t *testing.T, botToken string, userID int64, authDate time.Time) string {
	t.Helper()
	values := url.Values{}
	values.Set("auth_date", fmt.Sprintf("%d", authDate.Unix()))
	values.Set("query_id", "AAF-test")
	values.Set("user", fmt.Sprintf(`{"id":%d,"first_name":"Даниил","last_name":"","username":"danii1kq","language_code":"ru"}`, userID))

	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(k + "=" + values.Get(k))
	}

	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(botToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(sb.String()))
	values.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return values.Encode()
}

func TestVerifyAcceptsValidInitData(t *testing.T) {
	const token = "1234:TEST-TOKEN"
	a := NewAuth(token, 0)
	initData := signInitData(t, token, 777, time.Now().Add(-time.Minute))

	u, err := a.Verify(initData)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if u.ID != 777 || u.Name != "Даниил" || u.Username != "danii1kq" {
		t.Fatalf("user = %+v", u)
	}
}

func TestVerifyRejectsTamperedData(t *testing.T) {
	const token = "1234:TEST-TOKEN"
	a := NewAuth(token, 0)
	initData := signInitData(t, token, 777, time.Now())
	tampered := strings.Replace(initData, "%7B%22id%22%3A777", "%7B%22id%22%3A778", 1)
	if tampered == initData {
		// url encoding differs between Go versions; fall back to a hash edit
		tampered = strings.Replace(initData, "hash=", "hash=00", 1)
	}

	if _, err := a.Verify(tampered); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}

	// A payload signed with another bot token must not verify either.
	other := signInitData(t, "9999:OTHER", 777, time.Now())
	if _, err := a.Verify(other); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature for a foreign token, got %v", err)
	}
}

func TestVerifyRejectsStaleAndMissingData(t *testing.T) {
	const token = "1234:TEST-TOKEN"
	a := NewAuth(token, 0)

	old := signInitData(t, token, 777, time.Now().Add(-25*time.Hour))
	if _, err := a.Verify(old); !errors.Is(err, ErrStaleInitData) {
		t.Fatalf("want ErrStaleInitData, got %v", err)
	}

	if _, err := a.Verify(""); !errors.Is(err, ErrNoInitData) {
		t.Fatalf("want ErrNoInitData for an empty payload, got %v", err)
	}
	if _, err := a.Verify("user=%7B%22id%22%3A1%7D"); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature without a hash, got %v", err)
	}
}

func TestAuthorizeDevFallback(t *testing.T) {
	a := NewAuth("1234:TEST-TOKEN", 4242)
	u, err := a.Authorize("")
	if err != nil {
		t.Fatalf("dev authorize: %v", err)
	}
	if u.ID != 4242 {
		t.Fatalf("dev user id = %d, want 4242", u.ID)
	}

	// Without the dev chat, an unsigned request is rejected.
	strict := NewAuth("1234:TEST-TOKEN", 0)
	if _, err := strict.Authorize(""); !errors.Is(err, ErrNoInitData) {
		t.Fatalf("want ErrNoInitData, got %v", err)
	}
}
