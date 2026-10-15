package secrets_test

import (
	"errors"
	"strings"
	"testing"

	"narxoz-canvas-bot/internal/secrets"
)

func TestBoxRoundTripEncrypted(t *testing.T) {
	box := secrets.NewBox("super-secret")
	if !box.Enabled() {
		t.Fatal("box with a secret must encrypt")
	}
	const token = "1234~aVeryLongCanvasTokenValue"

	sealed, err := box.Seal(token)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if strings.Contains(sealed, token) {
		t.Fatalf("sealed value leaks the token: %q", sealed)
	}
	got, err := box.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got != token {
		t.Fatalf("Open = %q, want %q", got, token)
	}

	// A different secret must not be able to read the value.
	if _, err := secrets.NewBox("other-secret").Open(sealed); !errors.Is(err, secrets.ErrWrongSecret) {
		t.Fatalf("want ErrWrongSecret, got %v", err)
	}
}

func TestBoxRoundTripWithoutSecret(t *testing.T) {
	box := secrets.NewBox("")
	if box.Enabled() {
		t.Fatal("box without a secret must not encrypt")
	}
	const token = "1234~token"

	sealed, err := box.Seal(token)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if strings.Contains(sealed, token) {
		t.Fatalf("sealed value hides the token, got %q", sealed)
	}
	got, err := box.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got != token {
		t.Fatalf("Open = %q, want %q", got, token)
	}

	// An obfuscated (unencrypted) value stays readable when a secret appears.
	if _, err := secrets.NewBox("secret").Open(sealed); err != nil {
		t.Fatalf("plain: values must open regardless of the secret: %v", err)
	}
	// ...but an encrypted value needs the secret back.
	enc, err := secrets.NewBox("secret").Seal(token)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := secrets.NewBox("").Open(enc); !errors.Is(err, secrets.ErrWrongSecret) {
		t.Fatalf("want ErrWrongSecret for encrypted value without secret, got %v", err)
	}
}

func TestBoxNilAndLegacyValues(t *testing.T) {
	var nilBox *secrets.Box
	sealed, err := nilBox.Seal("token")
	if err != nil {
		t.Fatalf("nil box Seal: %v", err)
	}
	if got, err := nilBox.Open(sealed); err != nil || got != "token" {
		t.Fatalf("nil box Open = %q, %v", got, err)
	}

	// Unprefixed values (written before sealing existed) pass through.
	if got, err := nilBox.Open("legacy-raw-token"); err != nil || got != "legacy-raw-token" {
		t.Fatalf("legacy Open = %q, %v", got, err)
	}
}
