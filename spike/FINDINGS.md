# Phase 0 spike — findings (all risks retired)

Date: 2026-05-30. tmux 3.6b, claude 2.1.158, Apple Terminal, macOS.

## 1. Mouse-wheel scroll (the #1 risk) — WORKS
- Requirement: Claude must be in **fullscreen renderer** mode. Then `set -g mouse on` in tmux is
  enough for a natural trackpad wheel gesture to scroll the *conversation* in the pane.
- User-confirmed "works perfectly."
- Classic renderer fights the wheel (known issue) — so the dashboard must ensure fullscreen.

## 2. Ensuring fullscreen per pane — deterministic & idempotent
- `/tui fullscreen` is a valid direct slash command (not just a menu). Sending it when already in
  fullscreen prints a harmless `⎿ Already using the fullscreen renderer.` line.
- DECISION: after launching any session pane (new or `--resume`), send-keys `/tui fullscreen` + Enter.
  No dependence on whether the preference persists. (No CLI flag / settings.json key for this in 2.1.158.)

## 3. Status detection via `tmux capture-pane -p` — validated headlessly
Capture the bottom region and match the **hint line** (stable; the spinner glyph flickers, don't rely on it):

| State              | Telltale substring in captured pane                                   |
|--------------------|-----------------------------------------------------------------------|
| ● working          | `esc to interrupt`                                                    |
| ◐ waiting for input| `? for shortcuts · ← for agents`  (input box `❯` present, no spinner) |
| ⚠ needs permission | `Enter to confirm · Esc to cancel`  AND/OR  `❯ 1. ` (numbered menu)   |
| ○ idle             | waiting box + no transcript change for N seconds (use index recency)  |

Notes:
- Spinner verbs vary ("Osmosing…", "Churned for 44s", "Worked for 1s") and glyph cycles
  (✢ ✻ ✽ …) — DO NOT match these; match `esc to interrupt` for "working".
- First launch in an untrusted dir shows a trust dialog (same `❯ 1. Yes` / `Enter to confirm`
  family as tool-permission prompts) → treat as ⚠ needs-permission.
- `capture-pane -p -t <pane> -S -8` (last ~8 lines) is enough and cheap (<5ms).
- Matchers should live in config.toml so they can be tuned as the TUI evolves.

## Throwaway artifacts created by the spike (safe to delete)
- tmux server on socket `cmspike`  → killed at end of Phase 0.
- /tmp/cmspike-proj  → removed.
- ~/.claude/projects/-private-tmp-cmspike-proj/  (a real but throwaway claude session) → removed.
