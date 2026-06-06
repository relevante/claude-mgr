package ui

import (
	"testing"

	"claude-mgr/internal/index"
)

func sessRow(id string) row { return row{kind: rowSession, sess: index.SessionMeta{SessionID: id}} }

func TestMoveWrap(t *testing.T) {
	// header, S(a), S(b), header, S(c)  → session rows at 1,2,4
	m := &Model{rows: []row{
		{kind: rowHeader},
		sessRow("a"), sessRow("b"),
		{kind: rowHeader},
		sessRow("c"),
	}}

	m.cursor = 4 // last session: down wraps to first session (1)
	if ok := m.moveWrap(1); !ok || m.cursor != 1 {
		t.Fatalf("wrap down: ok=%v cursor=%d, want true/1", ok, m.cursor)
	}
	m.cursor = 1 // first session: up wraps to last session (4)
	if ok := m.moveWrap(-1); !ok || m.cursor != 4 {
		t.Fatalf("wrap up: ok=%v cursor=%d, want true/4", ok, m.cursor)
	}
	m.cursor = 1 // normal step skips the header at 3
	if ok := m.moveWrap(1); !ok || m.cursor != 2 {
		t.Fatalf("step down: ok=%v cursor=%d, want true/2", ok, m.cursor)
	}

	// a single session row has nowhere to wrap → no move, ok=false
	one := &Model{rows: []row{{kind: rowHeader}, sessRow("only")}}
	one.cursor = 1
	if ok := one.moveWrap(1); ok || one.cursor != 1 {
		t.Fatalf("single session: ok=%v cursor=%d, want false/1", ok, one.cursor)
	}
}

func TestClampScrollShowsGroupHeader(t *testing.T) {
	// header(0), S(1), S(2), header(3), S(4), S(5); height 5 → viewport of 3
	rows := []row{
		{kind: rowHeader},
		sessRow("a"), sessRow("b"),
		{kind: rowHeader},
		sessRow("c"), sessRow("d"),
	}

	// Cursor lands on a group's first session → the header scrolls in with it.
	m := &Model{rows: rows, height: 5, scroll: 5, cursor: 4}
	m.clampScroll()
	if m.scroll != 3 {
		t.Fatalf("scroll=%d, want 3 (header row visible above first session)", m.scroll)
	}

	// But not when that would push the cursor off the bottom edge:
	// header(0), S(1), header(2), S(3), S(4), S(5) — scrolling down to cursor 5
	// puts the viewport top on S(3), a group-first session, but pulling in its
	// header at 2 would hide the cursor.
	edge := []row{
		{kind: rowHeader},
		sessRow("a"),
		{kind: rowHeader},
		sessRow("b"), sessRow("c"), sessRow("d"),
	}
	m = &Model{rows: edge, height: 5, scroll: 0, cursor: 5}
	m.clampScroll()
	if m.scroll != 3 {
		t.Fatalf("scroll=%d, want 3 (cursor at bottom edge wins over header)", m.scroll)
	}

	// Mid-group top row (prev row is a session) is left alone.
	m = &Model{rows: rows, height: 5, scroll: 5, cursor: 2}
	m.clampScroll()
	if m.scroll != 2 {
		t.Fatalf("scroll=%d, want 2 (no header pull mid-group)", m.scroll)
	}
}

func TestJumpAttentionWraps(t *testing.T) {
	// attention at rows 0 and 2; row 1 is quiet (8-char ids so tmux.Short is identity)
	m := &Model{
		rows: []row{sessRow("aaaaaaaa"), sessRow("bbbbbbbb"), sessRow("cccccccc")},
		statusByID8: map[string]index.Status{
			"aaaaaaaa": index.StatusWorking,
			"cccccccc": index.StatusPermission,
		},
	}
	m.cursor = 2 // last attention row: next wraps to row 0
	if ok := m.jumpAttention(1); !ok || m.cursor != 0 {
		t.Fatalf("jump wrap down: ok=%v cursor=%d, want true/0", ok, m.cursor)
	}
	m.cursor = 0 // first attention row: prev wraps to row 2
	if ok := m.jumpAttention(-1); !ok || m.cursor != 2 {
		t.Fatalf("jump wrap up: ok=%v cursor=%d, want true/2", ok, m.cursor)
	}

	// only one attention row → no move
	solo := &Model{
		rows:        []row{sessRow("aaaaaaaa"), sessRow("bbbbbbbb")},
		statusByID8: map[string]index.Status{"aaaaaaaa": index.StatusWorking},
	}
	solo.cursor = 0
	if ok := solo.jumpAttention(1); ok || solo.cursor != 0 {
		t.Fatalf("single attention: ok=%v cursor=%d, want false/0", ok, solo.cursor)
	}
}

func TestBrandGlyph(t *testing.T) {
	cases := []struct {
		name string
		app  string
		want string
	}{
		{"default claude", "", "✳"},
		{"claude", index.AppClaude, "✳"},
		{"codex", index.AppCodex, "⬡"},
	}
	for _, c := range cases {
		got := brandGlyph(index.SessionMeta{App: c.app})
		if got != c.want {
			t.Errorf("%s: brandGlyph=%q, want %q", c.name, got, c.want)
		}
	}
}

func TestContextPieUsesSessionLimit(t *testing.T) {
	got, _ := contextPie(index.SessionMeta{ContextTokens: 250_000})
	if got != "◔" {
		t.Fatalf("default limit pie=%q, want ◔", got)
	}
	got, _ = contextPie(index.SessionMeta{ContextTokens: 250_000, ContextLimit: 500_000})
	if got != "◑" {
		t.Fatalf("session limit pie=%q, want ◑", got)
	}
}
