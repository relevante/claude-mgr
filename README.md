# claude-mgr

**A single pane for all your Claude Code and OpenAI Codex CLI sessions, across every project.**

You run agent CLIs for lots of different things and end up with terminal windows
and tabs scattered everywhere — hard to track, easy to lose on reboot. `claude-mgr`
is a persistent, fullscreen "home base": a session switcher on the left (grouped by
project, with live status), and the focused Claude or Codex session running live on
the right. Keep it up all the time, jump between threads with the keyboard, and
nothing gets lost.

It reads the sessions Claude Code persists at
`~/.claude/projects/*/<sessionId>.jsonl` and Codex persists in
`~/.codex/state_5.sqlite`, so **no session is ever lost** — every thread is one
keystroke from `claude --resume` or `codex resume`, even after a reboot.

## Setup

**Requirements:** macOS (built/tested on Apple Terminal), `tmux`, Go 1.24+,
`sqlite3`, and the CLIs you want to manage (`claude` and/or `codex`) on your
`PATH`.

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
  live agent panes, so terminal emulation, colors, resize, and **mouse-wheel
  scrolling** all just work. (tmux is only a terminal multiplexer — nothing to do
  with git.)
- A single-binary **Go controller** (Bubble Tea) is the left rail. It reads the
  session index off disk and drives the right pane. Each agent's live status
  (working / your-turn / idle) comes from Claude's session registry and Codex pane
  markers/lsof presence, watched with file-system notifications where available
  (with a slow poll as a safety net).
- Sessions you switch away from are **parked** in detached tmux windows — their
  processes keep running. Switching back rejoins the same live process.

## Keys

These work **from anywhere**, even while typing in the agent pane (`Option` =
`⌥`; Meta+letter is more reliable than Meta+arrow in Apple Terminal):

| Key | Action |
|-----|--------|
| `Option+Tab` (or `Option+L`) | toggle focus between the rail and the agent pane |
| `Option+↑` / `Option+↓` | switch to the previous / next session and load it on the right (wraps around) |
| `Option+'` / `Option+/` | jump to the previous / next session needing attention — working, your-turn, or done (wraps around) |
| `Option+Z` | zoom the agent pane fullscreen / back |
| `Option+T` | open a new terminal window in the current session's project directory |

In the **rail**:

| Key | Action |
|-----|--------|
| `↑`/`↓` (or `k`/`j`) · mouse wheel | move selection |
| `Ctrl-d`/`Ctrl-u` (or `PgDn`/`PgUp`) | jump half a screen · `g`/`G` top/bottom |
| `↵` | open the selected thread on the right (resumes it) |
| `Tab` / `→` | jump focus into the agent pane to type |
| `z` | zoom the agent pane |
| `/` | fuzzy-search across all threads |
| `s` | toggle flat "recent activity" sort (across all projects) |
| `f` | toggle "active only" filter |
| `c` | cycle the completion chime: off → each sound → off (the current `♪ <sound>` shows in the title bar) |
| `r` | rename the selected thread · `n` new session (`Ctrl+A` toggles Claude/Codex in the prompt) · `p` pin · `a` archive (`A` show) · `e` show/hide empty |
| `q` | detach (background — sessions keep running; `claude-mgr` re-attaches) |
| `Q` | quit (tear down the dashboard; sessions stay resumable on disk) |

`q` and `Q` ask for a `y` to confirm. From the agent pane you can also click the
rail to focus it, or use `Ctrl-b ←`.

## The status icons

Each row shows **where/what** on the left, and a **context-fill pie** on the right.

| Icon | Meaning |
|------|---------|
| `▌` (left bar) | this session is the one shown on the right |
| `✳` orange | Claude session |
| `⬡` teal | Codex session |
| `▶` green | working (agent is busy) |
| `▷` green | a background shell is still running (Claude is at the prompt) |
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
- **Completion chime.** A short, subtle sound plays whenever an agent stops
  working (finishes, or stops to ask you something) — but only for a session
  you're *not* currently watching in a focused window, so you're alerted about
  background work without being pinged at what's in front of you. Press `c` to
  cycle through the sounds (a handful of subtle macOS system sounds plus a custom
  tone) and off; the current one shows in the title bar and the choice persists.
  (The "even the viewed session, when you've switched apps" case relies on
  terminal focus reporting, which some terminals — including Apple Terminal — may
  not emit; off-screen sessions always chime regardless.)

State lives under `~/.config/claude-mgr/`: `index.json` (session cache),
`overlay.json` (names/pins/archives), `workspace.json` (open threads). Nothing is
written directly into your Claude or Codex session data.

## Project layout

```
main.go                 launcher + __controller subcommand + --dump
internal/index/         session discovery, tail-read/sqlite, (path,mtime,size) cache
internal/tmux/          tmux CLI wrapper: split, park/join, zoom, capture, keys
internal/status/        classify pane content → working / permission / idle
internal/live/          map running CLI pids → sessions (Claude registry, Codex lsof)
internal/overlay/       custom names, pins, archives
internal/workspace/     open-thread persistence + restore
internal/ui/            Bubble Tea rail: list, search, rename, new-session, status
```

See `spike/FINDINGS.md` and `spike/CODEX-FINDINGS.md` for the verified
terminal-behavior facts the design rests on.

## Notes & limits

- macOS-focused (uses `open -a Terminal` for `Option+T`; overridable via
  `CLAUDE_MGR_TERMINAL`). The context pie uses each app's reported context window
  when available, otherwise a 1M default.
- Claude status comes from Claude's real-time session registry
  (`~/.claude/sessions/<pid>.json`: `busy`/`waiting`/`idle`), with pane scraping
  as a fallback/refinement for permission ⚠. Codex sessions managed inside the
  dashboard use pane markers; Codex sessions running elsewhere are shown as live
  via lsof, but external working vs idle is not reliably exposed by Codex.
  A future change to these registry shapes or `internal/status/detect.go` markers
  could require re-tuning.
- Keybindings/look changes can be applied to a running dashboard live; behavior
  changes need a restart (`Q`, then `claude-mgr`) to load the new binary.
