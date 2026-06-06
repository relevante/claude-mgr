# Codex support spike findings

Date: 2026-06-05 EDT (Codex rollout timestamps are UTC 2026-06-06).
Environment: codex-cli 0.137.0, tmux 3.6b, Apple Terminal 455.1, macOS
15.6.0. Experiments used scratch directories under
`/tmp/claude-mgr-codex-spike-*`, a separate tmux socket at
`/tmp/claude-mgr-codex-spike.tmux`, and read-only inspection of Codex sqlite
state. The production `tmux -L claude-mgr` dashboard was not touched.

## 1. Resume identity - same uuid, same rollout

`codex resume <uuid>` continues the same thread id and appends to the same
rollout file. It does not fork for normal resume.

Evidence:
- New scratch session `019e9a6e-c43b-7f62-b05d-397ff2d9b4ed` wrote
  `/Users/jnsears/.codex/sessions/2026/06/05/rollout-2026-06-05T20-56-51-019e9a6e-c43b-7f62-b05d-397ff2d9b4ed.jsonl`.
- While resumed, `lsof -p 14010` showed fd `41w` on that same rollout path.
- `history.jsonl` appended both prompts with the same `session_id`.
- The copied/live state DB had one row for the q1 cwd, not a second fork row.
- The rollout contains one `session_meta` line and no `forked_from_id`.

Implementation decision: overlay and workspace keys do not need fork-chain
following for ordinary `resume`.

## 2. Heartbeat purity - clean on keystrokes, not continuous during turns

Typing without submitting did not update the thread row. After q2 id
`019e9a71-44e2-7032-98b3-8efc62bbf3db` went idle, `updated_at_ms` stayed at
`1780707580243` before and after typing a full prompt without Enter.

The handoff's "sub-second heartbeat during active turns" did not reproduce.
This contradicts the handoff. In these experiments `updated_at_ms` advanced on
turn events, not continuously:

- Pure-generation turn: samples at 21:00:38-21:00:45 stayed at
  `1780707633367` while Codex was still working; completion later set
  `1780707647335`.
- Tool turn: samples moved from `1780707699364` (turn start), to
  `1780707704522` (tool output/token event), to `1780707709055` (completion).

Implementation decision: use the state WAL as a wake-up source and
`updated_at_ms` as "recent activity", not as an authoritative busy heartbeat.
For live Codex panes, working/approval status must come from pane markers plus
the lsof pid-to-rollout live map. A 3-5s recency threshold is useful only for
fresh turn-start hints and would misclassify longer quiet turns as idle.

## 3. App-server and approval observability - not a live external source

The generated app-server schema includes `thread/list`, `thread/loaded/list`,
`thread/status/changed`, and token/status notifications, but a separate
app-server process cannot observe externally-owned TUI threads.

Evidence:
- Temporary app-server on `ws://127.0.0.1:17777` returned
  `thread/loaded/list -> {"data":[]}` while the q2 Codex TUI was still alive.
- The same app-server's `thread/list` saw q2 only as `status: {"type":"notLoaded"}`.

Approval state is not exposed as a durable status in `threads`. A q5
`--ask-for-approval untrusted` run stopped at a manual command prompt:

```text
Would you like to run the following command?
$ python3 -c 'print(42)'
› 1. Yes, proceed (y)
  2. Yes, and don't ask again for commands that start with ...
  3. No, and tell Codex what to do differently (esc)
Press enter to confirm or esc to cancel
```

While that prompt was pending, the main `threads` row had
`approval_mode='untrusted'`, `tokens_used=0`, and `updated_at_ms=1780708320447`;
the rollout tail ended at the `function_call`; logs had generic turn/function
call records but no stable "waiting for user approval" field. Pane scraping is
therefore required for the permission marker.

The `on-request` approval experiment created separate guardian subagent rows
with `source={"subagent":{"other":"guardian"}}` and `thread_source='subagent'`.
Those rows must be excluded from the interactive session index.

## 4. `tokens_used` semantics - cumulative, not context

`threads.tokens_used` is cumulative total token usage, not current context
window fill. This contradicts the handoff's suggested context-pie source.

Evidence from q2:
- State row: `tokens_used=86117`.
- Latest rollout `token_count`: `total_token_usage.total_tokens=86117` and
  `last_token_usage.total_tokens=21801`.
- TUI `/status`: `Context window: 96% left (21.8K used / 258K)`.

Implementation decision: the context pie should use the latest rollout
`token_count.info.last_token_usage.total_tokens` against
`model_context_window`. Discovery can still use sqlite for catalog metadata,
but context fill needs a tiny rollout-tail read for Codex rows.

## 5. TUI pane markers - usable, with approval-specific strings

Observed Codex pane strings:

| State | Telltale pane text |
|-------|--------------------|
| Working | `Working (` and `esc to interrupt` |
| Running command | `Running <command>` |
| Manual approval | `Would you like to run the following command?` and `Press enter to confirm or esc to cancel` |
| Trust prompt | `Do you trust the contents of this directory?` and `Press enter to continue` |
| Auto-review in progress | `Reviewing approval request` |
| Idle | prompt placeholder plus footer, e.g. `gpt-5.5 xhigh · /private/tmp/...`, with no working/approval text |

Implementation decision: add Codex-specific status markers. Treat the manual
approval/trust strings as permission/waiting. Treat `Reviewing approval
request` as working unless/until a manual approval prompt is visible.

## 6. Resume prompt / picker - explicit uuid has no picker

`codex resume <uuid> <prompt>` resumed q1 directly and ran the prompt. Explicit
`codex resume <uuid>` without a prompt also opened the transcript directly at
the input box. Neither showed a picker or "resume full session" style prompt.

Implementation decision: no auto-answer path is needed for explicit Codex
resumes. Omitted-id resume still uses a picker per `codex resume --help`, but
claude-mgr will always pass a uuid.

## Additional implementation notes

- Live pid mapping via lsof is confirmed. Running Codex processes held an open
  write fd on their active rollout file, e.g. q1 fd `51w` / `41w`, q2 fd `52w`,
  q5 fd `47w`.
- Read-only sqlite access worked here while Codex was running:
  `sqlite3 -readonly ~/.codex/state_5.sqlite ...` read current WAL-backed rows.
  This contradicts the handoff access caveat. However, `immutable=1` on a copied
  DB read stale main-db data and missed WAL updates; do not use immutable mode
  for Codex state unless the WAL has been checkpointed or intentionally ignored.
- Interactive session filtering should include `source='cli'` with
  `thread_source` empty/null or `user`. The current DB also has `exec`,
  `exec/user`, review subagents, and guardian subagents; those are not
  dashboard sessions.
- Throwaway artifacts: scratch dirs under `/tmp/claude-mgr-codex-spike-*` and
  Codex trust/session rows for those dirs. The isolated tmux server and
  temporary app-server were stopped at the end of the spike.
