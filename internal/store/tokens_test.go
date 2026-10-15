package store_test

import (
	"testing"

	"narxoz-canvas-bot/internal/model"
)

func TestChatTokenLifecycle(t *testing.T) {
	st := openTemp(t)

	if _, ok := st.ChatToken(111); ok {
		t.Fatal("no token must be stored initially")
	}
	if _, ok := st.LatestChatToken(); ok {
		t.Fatal("latest token must be empty initially")
	}
	if n := st.ChatTokenCount(); n != 0 {
		t.Fatalf("count = %d, want 0", n)
	}

	if err := st.SaveChatToken(111, "sealed-1", model.User{Name: "Ann", Login: "ann", Email: "ann@x"}, true); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok := st.ChatToken(111)
	if !ok {
		t.Fatal("token must be found after save")
	}
	if got.Token != "sealed-1" || got.Name != "Ann" || got.Login != "ann" || got.Email != "ann@x" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if !got.Verified {
		t.Fatal("token saved as verified must read back as verified")
	}
	if got.SavedAt.IsZero() {
		t.Fatal("saved_at must be recorded")
	}

	// Replacing keeps a single row per chat.
	if err := st.SaveChatToken(111, "sealed-2", model.User{Name: "Ann2"}, true); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, _ = st.ChatToken(111)
	if got.Token != "sealed-2" || got.Name != "Ann2" {
		t.Fatalf("replace mismatch: %+v", got)
	}
	if n := st.ChatTokenCount(); n != 1 {
		t.Fatalf("count = %d, want 1", n)
	}

	// LatestChatToken drives the background sync: last saved wins.
	if err := st.SaveChatToken(222, "sealed-3", model.User{Name: "Bob"}, false); err != nil {
		t.Fatalf("save 222: %v", err)
	}
	latest, ok := st.LatestChatToken()
	if !ok || latest.ChatID != 222 || latest.Token != "sealed-3" {
		t.Fatalf("latest = %+v (ok=%v), want chat 222", latest, ok)
	}
	if latest.Verified {
		t.Fatal("token stored without a successful probe must start unverified")
	}

	// A successful poll verifies the token that drives the sync.
	if err := st.SetLatestChatTokenVerified(true); err != nil {
		t.Fatalf("mark verified: %v", err)
	}
	if latest, _ := st.LatestChatToken(); !latest.Verified {
		t.Fatal("latest token must be verified after a successful poll")
	}
	if other, _ := st.ChatToken(111); !other.Verified {
		t.Fatal("verifying the latest token must not affect other chats' flags")
	}

	// Per-chat verification is independent.
	if err := st.SaveChatToken(333, "sealed-4", model.User{}, false); err != nil {
		t.Fatalf("save 333: %v", err)
	}
	if err := st.SetChatTokenVerified(333, true); err != nil {
		t.Fatalf("verify 333: %v", err)
	}
	if row, _ := st.ChatToken(333); !row.Verified || row.Token != "sealed-4" {
		t.Fatalf("chat 333 = %+v, want verified sealed-4", row)
	}

	if err := st.DropChatToken(222); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, ok := st.ChatToken(222); ok {
		t.Fatal("token must be gone after drop")
	}
	latest, ok = st.LatestChatToken()
	if !ok || latest.ChatID != 333 {
		t.Fatalf("latest after drop = %+v (ok=%v), want chat 333", latest, ok)
	}
}
