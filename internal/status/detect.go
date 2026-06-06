// Package status classifies what an agent session is doing by scraping the
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

// Codex markers verified during the Codex support spike (2026-06-05).
var Codex = Markers{
	Working: []string{
		"Working (",
		"Running ",
		"Reviewing approval request",
		"esc to interrupt",
	},
	Permission: []string{
		"Would you like to run the following command?",
		"Do you trust the contents of this directory?",
		"Press enter to confirm or esc to cancel",
		"Press enter to continue",
	},
}

// Classify maps captured pane text to a status. Working and Permission take
// priority; absent any marker the pane is treated as Idle.
func Classify(paneText string) index.Status {
	return Default.Classify(paneText)
}

func ClassifyApp(app, paneText string) index.Status {
	if app == index.AppCodex {
		return Codex.Classify(paneText)
	}
	return Classify(paneText)
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

// FromRegistry maps Claude's self-reported pid-registry status — which it keeps
// current in ~/.claude/sessions/<pid>.json — to a Status. This is authoritative
// and real-time (verified: idle/busy/waiting flip within a few hundred ms),
// unlike scraping pane text, so it's the preferred source where available.
// "busy" → working, "waiting" → blocked on you, "shell" → a background shell
// still running while Claude sits at the prompt (verified against claude
// 2.1.162: busy → shell when the turn ends with a bg task alive → idle when it
// exits), anything else → idle.
func FromRegistry(s string) index.Status {
	switch s {
	case "busy":
		return index.StatusWorking
	case "waiting":
		return index.StatusWaiting
	case "shell":
		return index.StatusShell
	default:
		return index.StatusIdle
	}
}

// Resolve picks a session's status, preferring Claude's authoritative registry
// flag (reg, when regKnown) over scraped pane text, and refining a generic
// "waiting" into a specific Permission ⚠ when the pane shows the confirm dialog.
func Resolve(reg index.Status, regKnown bool, paneText string) index.Status {
	st := reg
	if !regKnown {
		st = Classify(paneText)
	}
	if st == index.StatusWaiting && Classify(paneText) == index.StatusPermission {
		st = index.StatusPermission
	}
	return st
}

func ResolveApp(app string, reg index.Status, regKnown bool, paneText string) index.Status {
	if app != index.AppCodex {
		return Resolve(reg, regKnown, paneText)
	}
	return ClassifyApp(app, paneText)
}

// IsResumePrompt reports whether the pane is showing Claude's "resume from
// summary vs full session" choice (offered when resuming a large session).
func IsResumePrompt(paneText string) bool {
	return strings.Contains(paneText, "Resume full session as-is")
}

func anyContains(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
