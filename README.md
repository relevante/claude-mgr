# claude-mgr

**A single pane for all your Claude Code sessions, across every project.**

You run Claude Code for lots of different things and end up with terminal windows
and tabs scattered everywhere — hard to track, easy to lose on reboot. `claude-mgr`
is a persistent, fullscreen "home base": a session switcher on the left (grouped by
project, with live status), and the focused Claude session running live on the
right. Keep it up all the time, jump between threads with the keyboard, and nothing
gets lost.

It reads the sessions Claude Code already persists at
`~/.claude/projects/*/<sessionId>.jsonl`, so **no session is ever lost** — every
thread is one keystroke from `claude --resume`, even after a reboot.

## Setup

**Requirements:** macOS (built/tested on Apple Terminal), `tmux`, Go 1.24+, and the
`claude` CLI on your `PATH`.

```sh
brew install tmux
go build -o ~/.local/bin/claude-mgr .   # ~/.local/bin is typically already on PATH
claude-mgr                              # launch the dashboard
```

> Setting this up with Claude Code? Just open this repo in Claude and say
> "build and install claude-mgr per the README." It needs `tmux` (`brew install
> tmux`) and Go; the build is a single `go build`. The first launch creates a tmux
> session and attaches you to it.

`claude-mgr --dump` prints the session index without launching the UI (handy for a
quick look or for debugging).

## How it works

- A dedicated **tmux** server (socket `claude-mgr`) renders and multiplexes the
  live Claude panes, so terminal emulation, colors, resize, and **mouse-wheel
  scrolling** all just work. (tmux is only a terminal multiplexer — nothing to do
  with git.)
- A single-binary **Go controller** (Bubble Tea) is the left rail. It reads the
  session index off disk and drives the right pane. Each agent's live status
  (working / your-turn / idle) comes from Claude's own session registry, watched
  with file-system notifications so the rail updates the instant a session's
  state flips (with a slow poll as a safety net).
- Sessions you switch away from are **parked** in detached tmux windows — their
  processes keep running. Switching back rejoins the same live process.

## Keys

These work **from anywhere**, even while typing in the Claude pane (`Option` =
`⌥`; Meta+letter is more reliable than Meta+arrow in Apple Terminal):

| Key | Action |
|-----|--------|
| `Option+Tab` (or `Option+L`) | toggle focus between the rail and the Claude pane |
| `Option+↑` / `Option+↓` | switch to the previous / next session and load it on the right |
| `Option+'` / `Option+/` | jump to the previous / next session needing attention (working, your-turn, or done) |
| `Option+Z` | zoom the Claude pane fullscreen / back |
| `Option+T` | open a new terminal window in the current session's project directory |

In the **rail**:

| Key | Action |
|-----|--------|
| `↑`/`↓` (or `k`/`j`) · mouse wheel | move selection |
| `Ctrl-d`/`Ctrl-u` (or `PgDn`/`PgUp`) | jump half a screen · `g`/`G` top/bottom |
| `↵` | open the selected thread on the right (resumes it) |
| `Tab` / `→` | jump focus into the Claude pane to type |
| `z` | zoom the Claude pane |
| `/` | fuzzy-search across all threads |
| `s` | toggle flat "recent activity" sort (across all projects) |
| `f` | toggle "active only" filter |
| `b` | toggle the completion chime (a `♪ on/off` indicator shows in the title bar) |
| `r` | rename the selected thread · `n` new session · `p` pin · `a` archive (`A` show) · `e` show/hide empty |
| `q` | detach (background — sessions keep running; `claude-mgr` re-attaches) |
| `Q` | quit (tear down the dashboard; sessions stay resumable on disk) |

`q` and `Q` ask for a `y` to confirm. From the Claude pane you can also click the
rail to focus it, or use `Ctrl-b ←`.

## The status icons

Each row shows **where/what** on the left, and a **context-fill pie** on the right.

| Icon | Meaning |
|------|---------|
| `▌` (left bar) | this session is the one shown on the right |
| `▶` green | working (Claude is busy) |
| `⚠` red | needs permission / your turn (a confirm dialog) |
| `◐` red | blocked waiting on you (another prompt) |
| `●` white | open here, idle |
| `●` green | finished in the background since you last looked — go check it |
| `●` gray | alive in another terminal |
| `○` dim | dormant (nothing running; resumable) |
| `○◔◑◕●` | context-window fill, neutral → amber → red as it gets full |

Color encodes **where** (your dashboard = colored, elsewhere = gray, nowhere =
hollow); the glyph encodes **what**.

## Niceties it handles for you

- **Reboot/quit restore.** Open threads are saved to
  `~/.config/claude-mgr/workspace.json`; on launch they're relaunched (one shown,
  the rest parked) so your workspace comes back.
- **Resume prompts.** When resuming a large session shows Claude's "resume from
  summary vs full session" prompt, it auto-picks *full session as-is*.
- **`/clear`.** If you `/clear` a session (which starts a new session id under the
  same process), the dashboard follows the change instead of showing a duplicate.
- **Completion chime.** Toggle with `b`: a short, subtle sound plays whenever an
  agent stops working (finishes, or stops to ask you something) — but only for a
  session you're *not* currently watching in a focused window, so you're alerted
  about background work without being pinged at what's in front of you. The sound
  is a custom tone embedded in the binary; override it with `CLAUDE_MGR_SOUND=/path/to/sound`.
  The setting persists. (The "even the viewed session, when you've switched apps"
  case relies on terminal focus reporting, which some terminals — including Apple
  Terminal — may not emit; off-screen sessions always chime regardless.)

State lives under `~/.config/claude-mgr/`: `index.json` (session cache),
`overlay.json` (names/pins/archives), `workspace.json` (open threads). Nothing is
written into your Claude session data.

## Project layout

```
main.go                 launcher + __controller subcommand + --dump
internal/index/         session discovery, tail-read, (path,mtime,size) cache
internal/tmux/          tmux CLI wrapper: split, park/join, zoom, capture, keys
internal/status/        classify pane content → working / permission / idle
internal/live/          map running claude pids → sessions (registry-based)
internal/overlay/       custom names, pins, archives
internal/workspace/     open-thread persistence + restore
internal/ui/            Bubble Tea rail: list, search, rename, new-session, status
```

See `spike/FINDINGS.md` for the verified terminal-behavior facts the design rests on.

## Notes & limits

- macOS-focused (uses `open -a Terminal` for `Option+T`; overridable via
  `CLAUDE_MGR_TERMINAL`). The context pie assumes a 1M context window.
- Status (working / your-turn / idle) comes from Claude's own real-time session
  registry (`~/.claude/sessions/<pid>.json`: `busy`/`waiting`/`idle`); pane
  scraping is only a fallback and refines the permission ⚠. A future change to
  the registry shape or the `internal/status/detect.go` markers could require
  re-tuning.
- Keybindings/look changes can be applied to a running dashboard live; behavior
  changes need a restart (`Q`, then `claude-mgr`) to load the new binary.
