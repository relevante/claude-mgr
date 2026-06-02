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
