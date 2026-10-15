// Package secrets seals long-lived API tokens before they are written to
// disk. The bot receives Canvas tokens from users inside a Telegram chat and
// persists them in SQLite, so a raw token must never land in the database
// file as plain text.
//
// When a secret is configured (CANVAS_TOKEN_SECRET) tokens are encrypted with
// AES-256-GCM. When it is empty, tokens are only base64-encoded (prefix
// "plain:"), which keeps the raw token out of a casual `strings bot.db` while
// remaining fully recoverable — the bot must be able to read a token back to
// call Canvas. Values written by either scheme can be read back, and rows
// written before sealing existed (no prefix) are passed through unchanged.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// Storage prefixes identify how a value was sealed, so the format can evolve
// without breaking existing rows.
const (
	prefixPlain = "plain:"
	prefixBoxed = "boxed:"
)

// ErrWrongSecret is returned when a sealed value cannot be opened with the
// currently configured secret (the secret was changed, or the value is
// corrupted).
var ErrWrongSecret = errors.New("secrets: value was sealed with a different secret")

// Box seals and opens secrets. A nil *Box behaves like a Box without a
// configured secret (obfuscation only), so callers never have to nil-check.
type Box struct {
	aead cipher.AEAD // nil when no secret is configured
}

// NewBox returns a Box. An empty secret disables encryption (obfuscation
// only); any other secret is stretched with SHA-256 into an AES-256 key.
func NewBox(secret string) *Box {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return &Box{}
	}
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		// A 32-byte key is always valid for AES-256; unreachable.
		return &Box{}
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return &Box{}
	}
	return &Box{aead: aead}
}

// Enabled reports whether secrets are encrypted, not just obfuscated.
func (b *Box) Enabled() bool {
	return b != nil && b.aead != nil
}

// Seal prepares plain for storage.
func (b *Box) Seal(plain string) (string, error) {
	if b == nil {
		b = &Box{}
	}
	if !b.Enabled() {
		return prefixPlain + base64.RawURLEncoding.EncodeToString([]byte(plain)), nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secrets: read nonce: %w", err)
	}
	// Seal appends the ciphertext to dst, producing nonce||ciphertext.
	sealed := b.aead.Seal(nonce, nonce, []byte(plain), nil)
	return prefixBoxed + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// Open reverses Seal and returns the original plaintext.
func (b *Box) Open(sealed string) (string, error) {
	if b == nil {
		b = &Box{}
	}
	switch {
	case strings.HasPrefix(sealed, prefixPlain):
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(sealed, prefixPlain))
		if err != nil {
			return "", fmt.Errorf("secrets: decode obfuscated value: %w", err)
		}
		return string(raw), nil

	case strings.HasPrefix(sealed, prefixBoxed):
		if !b.Enabled() {
			return "", ErrWrongSecret
		}
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(sealed, prefixBoxed))
		if err != nil {
			return "", fmt.Errorf("secrets: decode encrypted value: %w", err)
		}
		ns := b.aead.NonceSize()
		if len(raw) < ns {
			return "", ErrWrongSecret
		}
		plain, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
		if err != nil {
			return "", ErrWrongSecret
		}
		return string(plain), nil

	default:
		// Legacy row written before sealing existed: keep it readable.
		return sealed, nil
	}
}
