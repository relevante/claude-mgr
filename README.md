# claude-mgr

A single pane to manage every Claude Code session across every project.

You run Claude Code for lots of different things and end up with terminal windows
and tabs scattered everywhere — hard to track, easy to lose on reboot.
`claude-mgr` is a persistent, fullscreen "home base": a session switcher rail on
the left (grouped by project, with live status), and the focused Claude session
running live on the right. Keep it up all the time; jump between threads with the
keyboard; nothing gets lost.

It reads the sessions Claude Code already persists at
`~/.claude/projects/*/<sessionId>.jsonl`, so **no session is ever lost** — every
thread is one keystroke from `claude --resume`, even after a reboot.

## How it works

- A dedicated **tmux** server (socket `claude-mgr`) renders and multiplexes the
  live Claude panes — so terminal emulation, colors, resize, and **mouse-wheel
  scrolling** all Just Work. (tmux is only a multiplexer; it has nothing to do
  with git.)
- A single-binary **Go controller** (Bubble Tea) is the left rail. It reads the
  session index off disk, drives the right pane, and scrapes pane content to show
  what each agent is doing.
- Sessions you switch away from are **parked** in detached tmux windows — their
  processes keep running. Switching back rejoins the same live process.

## Requirements

- macOS (built/tested on Apple Terminal) — `brew install tmux`
- Go 1.24+ to build
- The `claude` CLI on your `PATH`

## Install

```sh
brew install tmux
go build -o ~/.local/bin/claude-mgr .   # ~/.local/bin is already on your PATH
```

## Use

```sh
claude-mgr          # opens the dashboard (creates/attaches the tmux session)
claude-mgr --dump   # headless: print the session index and exit
```

### Keys (rail focused)

| Key | Action |
|-----|--------|
| `↑`/`↓` (or `k`/`j`) | move selection · **mouse wheel** scrolls too |
| `Ctrl-d`/`Ctrl-u` (or `PgDn`/`PgUp`) | jump half a screen |
| `g`/`G` | top / bottom |
| `↵` | open the selected thread on the right (resumes it) |
| `tab` / `→` | jump focus into the Claude pane to type |
| `z` | zoom the Claude pane fullscreen / back (`Option+z` from either pane) |

**Switch sessions from anywhere (even while typing in Claude):** `Option+j` / `Option+k` (also `Option+↓`/`Option+↑`) move one item and load it into the right pane.

**Returning to the rail from the Claude pane:** `Option+h` (← rail) / `Option+l`
(→ session), or **click** the rail (mouse is on), or `Ctrl-b ←`. Meta+letter is
more reliable than Meta+arrow in Apple Terminal.

### Other keys
| `/` | fuzzy-search across all threads |
| `r` | rename the selected thread (stored by the dashboard) |
| `n` | new session in a chosen directory |
| `p` | pin / unpin |
| `a` | archive / unarchive · `A` show archived |
| `e` | show/hide empty (`/clear`-artifact) sessions |
| `q` | detach (dashboard + sessions keep running in the background) |

### Status dots

`▶` shown · `●` working (yellow) · `◐` waiting for you · `⚠` needs permission
(red) · `●` live in another terminal (cyan) · `○` dormant (resumable).

## Persistence

The set of open threads is saved to `~/.config/claude-mgr/workspace.json`. On
launch (e.g. after a reboot) the dashboard relaunches them automatically — one
shown, the rest parked — so your workspace comes back. `q` detaches without
tearing anything down; re-running `claude-mgr` re-attaches.

Config & state live under `~/.config/claude-mgr/`:
`index.json` (session cache), `overlay.json` (names/pins/archives),
`workspace.json` (open threads).

## Layout

```
main.go                 launcher + __controller subcommand + --dump
internal/index/         session discovery, tail-read, (path,mtime,size) cache
internal/tmux/          tmux CLI wrapper: split, park/join, zoom, capture
internal/status/        classify pane content → working/waiting/permission/idle
internal/live/          map running claude PIDs → sessions (live elsewhere)
internal/overlay/       custom names, pins, archives
internal/workspace/     open-thread persistence + restore
internal/ui/            Bubble Tea rail: list, search, rename, new-session
```

See `spike/FINDINGS.md` for the verified terminal-behavior facts the design rests on.
