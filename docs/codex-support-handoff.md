# Codex support — handoff

Goal: claude-mgr manages **OpenAI Codex CLI sessions alongside Claude Code
sessions** — one mixed list grouped by project, per-app identity marks, app
choice when starting a new session, and otherwise the same feature set (show,
park, search, rename/pin/archive, status dots, chime, workspace restore).

Estimated 3–5 focused days. Spike first (§3) — it's the only part with real
unknowns. Everything in §2 was **verified live on Nick's machine 2026-06-05**
against codex-cli **0.137.0** (Homebrew cask) and claude-mgr HEAD `c25003c`.

---

## 1. Target capability matrix

| Capability            | Claude (today)               | Codex (target)                              |
|-----------------------|------------------------------|---------------------------------------------|
| Discovery + titles    | transcript JSONL tail-parse  | `threads` table in state sqlite (§2.1)      |
| Live pid → session    | `~/.claude/sessions/<pid>.json` | `lsof` open rollout-file handles (§2.3)  |
| Working/idle realtime | registry `status` field      | `updated_at_ms` heartbeat + WAL watch (§2.2)|
| Waiting/permission ⚠  | registry + pane scrape       | pane scrape only (spike Q3 may improve)     |
| Background shell ▷    | registry `"shell"` status    | none known — acceptable gap                 |
| Context-fill pie      | usage from transcript tail   | `tokens_used` column (semantics = spike Q4) |
| Resume                | `claude --resume <id>`       | `codex resume <uuid>` (fork? = spike Q1)    |
| New session           | `exec claude`                | `exec codex`                                |

## 2. Verified Codex data sources

### 2.1 Session catalog: `~/.codex/state_5.sqlite`, table `threads`
Columns (the useful ones): `id` (uuid), `rollout_path`, `cwd`, `title`,
`first_user_message`, `preview`, `tokens_used`, `created_at_ms`,
`updated_at_ms`, `git_branch`, `git_sha`, `model`, `archived`, `source`,
`thread_source`, `cli_version`. 68 rows on Nick's machine; indexed by
`updated_at DESC`.

- **Filter to interactive sessions**: `source = 'cli'`. Subagent/review
  threads have `thread_source = 'subagent'` / `source` like `codex_exec`
  (observed `originator: "codex_exec"` + `"source":{"subagent":"review"}` in
  rollout meta). Decide the exact WHERE during the spike by eyeballing rows.
- Codex has its **own `archived` flag** — decide whether to respect it
  (suggest: treat as archived in our overlay sense, read-only).
- One query replaces the whole JSONL tail-parse layer Claude needs. Titles
  and first-user-message come for free (no aiTitle equivalent needed).

**Access caveat**: a plain read-only sqlite open FAILED while codex held the
db (WAL mode); copying db+wal worked. In Go, try
`file:...?mode=ro&_txlock=deferred` or `immutable=0` with busy_timeout; if
codex's locking is hostile, fall back to copy-to-tmp (cheap at this size) or
`sqlite3` shell-out. Resolve in spike. **Never open it read-write.**

### 2.2 Real-time working/idle: `updated_at_ms` heartbeat
Verified: during active turns the thread row updates **sub-second**
(`updated_at = 1780703459` observed at wall clock `1780703460`, twice in one
sample). So: `now - updated_at_ms < ~3-5s → working`, else idle.
Event-drive it by watching `state_5.sqlite-wal` mtime with the existing
`internal/watch` fsnotify pattern (per-file watch; in-place rewrites).
**Spike must confirm** it ticks *only* during agent work, not on keystrokes
(Q2). Turn-*start* also visible: `~/.codex/history.jsonl` appends
`{"session_id","ts","text"}` per user message (verified) — doubles as
last-prompt source.

### 2.3 Live pid → session: open rollout file handles
A running codex holds an **open write fd on its rollout file**
(`lsof -p <pid>` showed fd 25w → `sessions/.../rollout-...-<uuid>.jsonl`).
So: `pgrep -x codex` → `lsof -p` → regex the uuid out of the rollout path →
authoritative live map, including sessions in other terminals. Mind the cost:
lsof per poll is fine on the 5s safety net, not the 800ms loop; cache by pid
start time. (Claude's equivalent lives in `internal/live`; add a codex
implementation behind a shared interface.)

### 2.4 Rollout files (fallback / detail source)
`~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl`. First line
`session_meta` (id, cwd, originator, cli_version, `forked_from_id`, source).
Then `response_item` (role user/assistant messages) and `event_msg` rows —
`token_count` events carry `last_token_usage.input_tokens` ≈ current context
size (useful if `tokens_used` turns out cumulative). File **grows during a
turn** (streamed events) — mtime is a secondary activity signal.

### 2.5 App-server protocol (stretch, spike Q3)
The binary embeds an event vocabulary: `thread/started`,
`thread/status/changed`, `turn/started`, `turn/completed`, `item/*`,
`thread/tokenUsage/updated`, plus a `remote_control_enrollments` table and
`remoteControl/status/changed`. If `codex app-server` (or remote-control) can
**observe threads owned by other processes**, that beats everything above.
Likely it only sees threads it owns — timebox to ~1h.

### 2.6 Logs (diagnostic only)
`~/.codex/logs_2.sqlite` `logs` table: TRACE-level tracing from all codex
processes, span text embeds `thread_id=<uuid>` and `pid:<pid>:<uuid>`, rows
include `submission_dispatch{codex.op="user_input"}`, `session_task.turn`,
`op.dispatch.interrupt`. Useful for debugging the integration; too fragile to
build on.

## 3. Spike questions (do these first, ~half day)

1. **Resume identity**: does `codex resume <uuid>` continue the same uuid +
   rollout file, or fork a new uuid (`forked_from_id`)? Test: scratch dir,
   `codex` → say something → quit → `codex resume <uuid>` → say something →
   check threads table + sessions dir. If it forks, overlay/workspace keys
   need chain-following — design for it before building.
2. **Heartbeat purity**: confirm `updated_at_ms` only ticks during turns
   (type without submitting; sit idle; run a turn). Pick the busy threshold.
3. **App-server observability** (timeboxed): can an app-server client list +
   subscribe to externally-owned threads? Also check whether an
   approval-pending state is visible anywhere on disk (start a turn that
   needs approval with `approval_mode=on-request`, then inspect threads row /
   rollout tail / logs while the prompt is up).
4. **`tokens_used` semantics**: compare the column against the TUI's own
   context display and the rollout `token_count` events for a long session.
   Decide the context-pie source.
5. **TUI pane markers**: run codex in tmux, capture the pane in each state
   (working / idle / approval prompt) → markers for `internal/status`
   fallback, like the existing `Markers` struct. Note codex's footer strings.
6. **Resume prompt / picker**: does `codex resume <uuid>` ever show an
   interactive picker or prompt that needs auto-answering (like Claude's
   "resume full session" menu)?

Write findings to `spike/CODEX-FINDINGS.md` (same spirit as
`spike/FINDINGS.md` — claims tied to observed evidence, with version
numbers).

## 4. Where it plugs into the code

- `internal/index` — `SessionMeta` gains `App string` ("claude"/"codex").
  Add a codex scanner (sqlite) merged into `Store.Scan()`'s output; sort and
  grouping are already app-agnostic. **Bump `cacheVersion`** in `cache.go`
  (SessionMeta shape changes; codex rows may not need file-cache at all —
  the sqlite IS the cache).
- `internal/live` — extract an interface over `Sessions/Statuses/
  SessionForPID`; codex impl per §2.2/2.3. `SessionForPID` matters: it
  powers shown-pane adoption (`adoptShownID`) and restart adoption.
- `internal/status` — codex pane `Markers` from spike Q5; `Resolve` logic is
  reusable as-is.
- `internal/watch` — second `Registry` watching the codex state WAL (the
  per-file watch pattern is already portable to this).
- `internal/tmux` — `claudeCmd`/`newClaudeCmd` become per-app
  (`codex resume <id>` / `codex`); add a `CLAUDE_MGR_CODEX_CMD` test
  override mirroring `CLAUDE_MGR_CLAUDE_CMD`. Parked-window names (`s_<id8>`)
  work unchanged with codex uuids.
- `internal/ui` — brand mark in `renderRow` (§5), app in the `n` flow,
  `pendingNew` adoption needs the app to know which transcript store to
  match in `findPendingNew`.
- `internal/overlay`, `internal/workspace` — keyed by session id; uuids
  don't collide across apps, so no schema change needed (but store the app in
  workspace entries so restore knows which command resumes each id).

## 5. UI spec (decided with Nick, 2026-06-05)

- **Brand mark**: one char between status mark and title, shape + color:
  - Claude: `✳` muted orange, 256-color `173` (≈ Anthropic #D97757)
  - Codex: `⬡` teal, 256-color `36` (≈ OpenAI #10A37F)
- Status mark keeps its column and its where/what color semantics untouched.
- Brand mark dims to gray on external/dormant rows (consistent with "color
  encodes where"); keeps its color on the selected row via the existing
  per-segment background rendering in `renderRow`.
- `✳`/`⬡` are ambiguous-width — verify rendering in Apple Terminal before
  settling (fallbacks: `✲ ✱ ⏣ ⎔`). The rail already uses `⚠` successfully.
- Same mark in: the `n` new-session prompt (one key toggles app — `tab` is
  taken by dir completion; suggest `ctrl+a` or similar, shown inline like
  `new ✳ in: …`), the shown-pane title, search rows.
- Group headers stay app-agnostic (a project can host both).
- Possible later refinement (Nick is open to it): mark only the minority app.
- README status-legend table needs the new rows.

## 6. Suggested phasing

1. Spike (§3) → findings doc, go/no-go on each signal.
2. Discovery: codex scanner + `App` field + brand marks → mixed list renders.
3. Launch/resume: per-app commands, `n` app toggle, workspace restore.
4. Status: live map (lsof), heartbeat watcher, pane markers, chime parity.
5. Polish: README, edge cases (forked resumes, archived sync, context pie).

Each phase ends green (`go build ./... && go test ./internal/...`) and
committable on its own.

## 7. House rules (read `~/.claude/...claude-mgr/memory/` too)

- **Verify against real behavior** before claiming anything works — this
  project's history is full of "obvious" assumptions that were wrong until
  tested against the live system. Tie claims to observed evidence.
- **Never disturb the production dashboard**: Nick's real rail runs on tmux
  socket `claude-mgr` (`tmux -L claude-mgr`), session `cmgr`. Read-only
  `capture-pane` / `list-windows` is fine; never kill/respawn/send-keys
  there without asking. For integration testing use a **separate socket**
  and the `CLAUDE_MGR_*` env overrides (`PROJECTS`, `CACHE`, `OVERLAY`,
  `CLAUDE_CMD`) — see how tests use them.
- Codex experiments: scratch dirs only; **read-only** toward `~/.codex`
  (the state/logs sqlites especially — copy before querying if locking
  bites).
- Restart procedure for the live rail (only with Nick's go-ahead):
  `go build -o ~/.local/bin/claude-mgr .` then
  `tmux -L claude-mgr respawn-pane -k -t cmgr:main.0 "'$HOME/.local/bin/claude-mgr' __controller"`
  — parked sessions and the shown pane survive (adopt-on-restart, `c25003c`).
- Code style: match the existing comment density and "why, not what" tone;
  table-driven tests next to the behavior they pin; small focused commits on
  `main` with imperative subjects.
- Claude behavior must not regress: every Claude-only feature (registry
  status incl. `"shell"` → `▷`, resume auto-answer, /clear id-following)
  keeps working unchanged.

---

## 8. Kickoff prompt (copy-paste for the implementing agent)

> Read `docs/codex-support-handoff.md` in this repo top to bottom — it
> contains verified findings, decisions already made with Nick, and house
> rules; treat its §7 constraints as hard. Also skim `README.md` and
> `spike/FINDINGS.md` for project conventions.
>
> Your job is to add OpenAI Codex CLI session support to claude-mgr per that
> document. Work in this order:
>
> 1. Run the §3 spike: answer all six questions with real experiments
>    (scratch dirs, separate tmux socket, read-only toward `~/.codex`).
>    Write `spike/CODEX-FINDINGS.md` documenting each answer with the
>    evidence, codex version, and dates. Where a finding contradicts the
>    handoff, the finding wins — note the contradiction.
> 2. Stop and present the findings plus your concrete plan (any deviations
>    from the handoff's §4/§6 called out) before writing feature code.
> 3. Implement in the §6 phases. Every phase: `go build ./...` and
>    `go test ./internal/...` green, table-driven tests for new decision
>    logic, then a focused commit on main. Do not push.
> 4. Do NOT touch the running dashboard (tmux socket `claude-mgr`) except
>    read-only inspection, and do not restart it — Nick deploys with the
>    hot-swap procedure in §7 when he's ready.
>
> Throughout: verify real behavior over assumptions, keep Claude-side
> behavior bit-for-bit unchanged, and match the existing code's comment and
> test style.
