package webapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Errors returned by Auth.Verify.
var (
	// ErrNoInitData means the request carried no Telegram initData at all.
	ErrNoInitData = errors.New("webapp: no Telegram initData")
	// ErrBadSignature means initData was tampered with or signed for another bot.
	ErrBadSignature = errors.New("webapp: initData signature mismatch")
	// ErrStaleInitData means initData is older than maxInitAge.
	ErrStaleInitData = errors.New("webapp: initData is too old")
)

// maxInitAge bounds how old a signed Mini App payload may be.
const maxInitAge = 24 * time.Hour

// User is the Telegram user a request belongs to. For the private chats this
// bot serves, the chat id equals the user id.
type User struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username"`
}

// Auth validates Telegram Mini App initData with the HMAC scheme from the
// Telegram docs: the signing key is HMAC_SHA256("WebAppData", bot_token) and
// the payload is the sorted, newline-joined field list without "hash".
//
// The newer Ed25519 "signature" field is ignored on purpose: the HMAC check
// already binds the payload to this bot.
type Auth struct {
	token   string
	devChat int64
}

// NewAuth returns an Auth for the given bot token. A non-zero devChatID enables
// the development fallback: local requests without initData are treated as that
// chat. It must stay zero on a public deployment.
func NewAuth(botToken string, devChatID int64) *Auth {
	return &Auth{token: strings.TrimSpace(botToken), devChat: devChatID}
}

// DevMode reports whether the unauthenticated development fallback is enabled.
func (a *Auth) DevMode() bool { return a.devChat != 0 }

// Verify checks the signature and freshness of initData.
func (a *Auth) Verify(initData string) (User, error) {
	if strings.TrimSpace(initData) == "" {
		return User{}, ErrNoInitData
	}
	values, err := url.ParseQuery(initData)
	if err != nil {
		return User{}, fmt.Errorf("%w: %v", ErrBadSignature, err)
	}
	gotHash := values.Get("hash")
	if gotHash == "" {
		return User{}, ErrBadSignature
	}
	values.Del("hash")

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
	secret.Write([]byte(a.token))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(sb.String()))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(strings.ToLower(gotHash))) {
		return User{}, ErrBadSignature
	}

	if v := values.Get("auth_date"); v != "" {
		ts, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil {
			return User{}, fmt.Errorf("%w: bad auth_date", ErrBadSignature)
		}
		// auth_date is in the past; a small skew into the future is tolerated.
		if age := time.Since(time.Unix(ts, 0)); age > maxInitAge || age < -5*time.Minute {
			return User{}, ErrStaleInitData
		}
	} else {
		return User{}, fmt.Errorf("%w: missing auth_date", ErrBadSignature)
	}

	// initData carries the user object itself (not wrapped in a "user" key).
	var payload struct {
		ID        int64  `json:"id"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Username  string `json:"username"`
	}
	if err := json.Unmarshal([]byte(values.Get("user")), &payload); err != nil {
		return User{}, fmt.Errorf("%w: bad user payload", ErrBadSignature)
	}
	if payload.ID == 0 {
		return User{}, fmt.Errorf("%w: missing user id", ErrBadSignature)
	}
	name := strings.TrimSpace(payload.FirstName + " " + payload.LastName)
	if name == "" {
		name = payload.Username
	}
	return User{ID: payload.ID, Name: name, Username: payload.Username}, nil
}

// Authorize verifies initData and falls back to the development chat when the
// app is opened locally without Telegram.
func (a *Auth) Authorize(initData string) (User, error) {
	u, err := a.Verify(initData)
	if err == nil {
		return u, nil
	}
	if a.DevMode() && errors.Is(err, ErrNoInitData) {
		return User{ID: a.devChat, Name: "Локальный тест", Username: "dev"}, nil
	}
	return User{}, err
}
