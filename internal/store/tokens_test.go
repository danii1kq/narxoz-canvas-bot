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

	if err := st.SaveChatToken(111, "sealed-1", model.User{Name: "Ann", Login: "ann", Email: "ann@x"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok := st.ChatToken(111)
	if !ok {
		t.Fatal("token must be found after save")
	}
	if got.Token != "sealed-1" || got.Name != "Ann" || got.Login != "ann" || got.Email != "ann@x" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if got.SavedAt.IsZero() {
		t.Fatal("saved_at must be recorded")
	}

	// Replacing keeps a single row per chat.
	if err := st.SaveChatToken(111, "sealed-2", model.User{Name: "Ann2"}); err != nil {
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
	if err := st.SaveChatToken(222, "sealed-3", model.User{Name: "Bob"}); err != nil {
		t.Fatalf("save 222: %v", err)
	}
	latest, ok := st.LatestChatToken()
	if !ok || latest.ChatID != 222 || latest.Token != "sealed-3" {
		t.Fatalf("latest = %+v (ok=%v), want chat 222", latest, ok)
	}

	if err := st.DropChatToken(222); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, ok := st.ChatToken(222); ok {
		t.Fatal("token must be gone after drop")
	}
	latest, ok = st.LatestChatToken()
	if !ok || latest.ChatID != 111 {
		t.Fatalf("latest after drop = %+v (ok=%v), want chat 111", latest, ok)
	}
}
