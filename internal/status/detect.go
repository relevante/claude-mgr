// Package status classifies what a Claude session is doing by scraping the
// bottom of its tmux pane. The hint line is the stable discriminator — the
// spinner glyph/verb flickers and must not be matched (see spike/FINDINGS.md).
package status

import (
	"strings"

	"claude-mgr/internal/index"
)

// Markers are the substrings that identify each state. They live in one place
// so they're easy to tune as Claude's TUI evolves (a known fragility).
//
// NOTE: a plain prompt ("? for shortcuts …") is NOT a distinct state — Claude
// sits there both when idle and when awaiting a reply, and the two are
// indistinguishable from the pane. So we only flag the two states we can detect
// reliably (working, and a blocking permission/confirm dialog); everything else
// is Idle. Permission markers are specific to the confirm UI to avoid matching
// ordinary conversation text (numbered lists, the word "allow", etc.).
type Markers struct {
	Working    []string
	Permission []string
}

// Default markers verified against claude 2.1.158.
var Default = Markers{
	Working:    []string{"esc to interrupt"},
	Permission: []string{"Enter to confirm", "Do you want to proceed", "❯ 1. Yes", "❯ 1. Allow"},
}

// Classify maps captured pane text to a status. Working and Permission take
// priority; absent any marker the pane is treated as Idle.
func Classify(paneText string) index.Status {
	return Default.Classify(paneText)
}

func (m Markers) Classify(paneText string) index.Status {
	if anyContains(paneText, m.Working) {
		return index.StatusWorking
	}
	if anyContains(paneText, m.Permission) {
		return index.StatusPermission
	}
	return index.StatusIdle
}

func anyContains(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
