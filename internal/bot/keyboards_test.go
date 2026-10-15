package bot

import "testing"

func TestParseCallback(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want callback
		ok   bool
	}{
		{"noop", "noop", callback{kind: "noop"}, true},
		{"settings", cbSettings, callback{kind: "settings"}, true},
		{"interval", cbInterval, callback{kind: "interval"}, true},
		{"ping", cbPing, callback{kind: "ping"}, true},
		{"token", cbToken, callback{kind: "token"}, true},
		{"sync refresh", cbSyncRefresh("assign", 0, 2), callback{kind: "sync", view: "assign", page: 2}, true},
		{"sync settings", cbSyncRefresh("settings", 0, 0), callback{kind: "sync", view: "settings", page: 0}, true},
		{"sync course", cbSyncRefresh("course", 42, 1), callback{kind: "sync", view: "course", param: 42, page: 1}, true},
		{"bad sync no page", "s:sync:assign:0", callback{}, false},
		{"bad sync nan", "s:sync:assign:0:x", callback{}, false},
		{"mute", "s:mute:42", callback{kind: "mute", param: 42}, true},
		{"page assign", "p:assign:0:0", callback{kind: "page", view: "assign", page: 0}, true},
		{"page course", cbPage("course", 12345, 2), callback{kind: "page", view: "course", param: 12345, page: 2}, true},
		{"page courses", "p:courses:0:1", callback{kind: "page", view: "courses", page: 1}, true},
		{"bad page missing page", "p:assign:0", callback{}, false},
		{"bad page nan", "p:assign:0:x", callback{}, false},
		{"bad page neg", "p:assign:0:-1", callback{}, false},
		{"bad mute nan", "s:mute:abc", callback{}, false},
		{"random", "rand", callback{}, false},
		{"empty", "", callback{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseCallback(tt.in)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCallbackDataLength(t *testing.T) {
	cases := []string{
		cbPage("course", 9999999999, 999),
		cbPage("courses", 0, 999),
		cbMute(9999999999),
		cbSyncRefresh("assign", 0, 999),
		cbSyncRefresh("settings", 0, 0),
		cbSyncRefresh("course", 9999999999, 999),
		cbToken,
	}
	for _, c := range cases {
		if len(c) > 64 {
			t.Fatalf("callback_data %q exceeds 64 bytes (%d)", c, len(c))
		}
	}
}

func TestMainMenuKeyboardHasAllButtons(t *testing.T) {
	kb := MainMenuKeyboard()
	var labels []string
	for _, row := range kb.Keyboard {
		for _, btn := range row {
			labels = append(labels, btn.Text)
		}
	}
	want := []string{btnAssignments, btnMissing, btnGrades, btnOverdue, btnCourses, btnSettings, btnRefresh, btnSetup}
	for _, w := range want {
		found := false
		for _, got := range labels {
			if got == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("button %q missing from the main menu: %v", w, labels)
		}
	}
}

func TestTotalPagesOf(t *testing.T) {
	tests := []struct {
		total, size, want int
	}{
		{0, 7, 1},
		{1, 7, 1},
		{7, 7, 1},
		{8, 7, 2},
		{14, 7, 2},
		{15, 7, 3},
		{-5, 7, 1},
	}
	for _, tt := range tests {
		if got := totalPagesOf(tt.total, tt.size); got != tt.want {
			t.Errorf("totalPagesOf(%d,%d)=%d, want %d", tt.total, tt.size, got, tt.want)
		}
	}
}

func TestClampPage(t *testing.T) {
	tests := []struct {
		page, size, total, want int
	}{
		{0, 7, 0, 0},
		{-1, 7, 10, 0},
		{0, 7, 10, 0},
		{1, 7, 10, 1},
		{2, 7, 10, 1},
		{10, 7, 10, 1},
		{0, 7, 7, 0},
		{3, 7, 1, 0},
	}
	for _, tt := range tests {
		if got := clampPage(tt.page, tt.size, tt.total); got != tt.want {
			t.Errorf("clampPage(%d,%d,%d)=%d, want %d", tt.page, tt.size, tt.total, got, tt.want)
		}
	}
}

func TestSliceRange(t *testing.T) {
	tests := []struct {
		n, page, size      int
		wantStart, wantEnd int
	}{
		{10, 0, 7, 0, 7},
		{10, 1, 7, 7, 10},
		{10, 2, 7, 10, 10},
		{3, 0, 7, 0, 3},
		{0, 0, 7, 0, 0},
	}
	for _, tt := range tests {
		s, e := sliceRange(tt.n, tt.page, tt.size)
		if s != tt.wantStart || e != tt.wantEnd {
			t.Errorf("sliceRange(%d,%d,%d)=%d,%d want %d,%d", tt.n, tt.page, tt.size, s, e, tt.wantStart, tt.wantEnd)
		}
	}
}

func TestPaginationRowCallbackData(t *testing.T) {
	mk := func(p int) string { return cbPage("assign", 0, p) }

	first := paginationRow(mk, 0, 15, 7) // totalPages=3
	if first[0].CallbackData == nil || *first[0].CallbackData != cbNoop {
		t.Errorf("first page prev must be noop")
	}
	if first[2].CallbackData == nil || *first[2].CallbackData != cbPage("assign", 0, 1) {
		t.Errorf("next must point to page 1")
	}

	last := paginationRow(mk, 2, 15, 7)
	if last[2].CallbackData == nil || *last[2].CallbackData != cbNoop {
		t.Errorf("last page next must be noop")
	}
	if last[0].CallbackData == nil || *last[0].CallbackData != cbPage("assign", 0, 1) {
		t.Errorf("prev on last page must point to page 1")
	}
}
