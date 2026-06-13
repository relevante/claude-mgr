"use strict";
// claude-mgr mobile client. The session list (with all the rail's actions as
// taps) replaces the tmux keybindings; the terminal is a real xterm.js viewport
// bridged to a tmux client over a WebSocket.

// --- auth ------------------------------------------------------------------
// The token may arrive in the page URL (?token=…) — used once, then stripped
// for a clean URL and persisted to localStorage. iOS home-screen web apps run
// in a SEPARATE storage container with no token in either place on first launch,
// so when none is found we prompt for it once; it then persists in that app's
// own storage. (We can't bake it into a manifest — that file is unauthenticated
// and would leak the token to anyone on the network.)
let token = "";
(function initToken() {
  const u = new URL(location.href);
  const t = u.searchParams.get("token");
  if (t) {
    token = t;
    localStorage.setItem("cmgr_token", t);
    u.searchParams.delete("token");
    history.replaceState(null, "", u.pathname + u.hash); // keep the clean URL
  } else {
    token = localStorage.getItem("cmgr_token") || "";
  }
})();

const api = (path, opts = {}) =>
  fetch(path, {
    ...opts,
    headers: { ...(opts.headers || {}), Authorization: "Bearer " + token },
  });

// --- DOM --------------------------------------------------------------------
const $ = (id) => document.getElementById(id);
const groupsEl = $("groups");
const listEl = $("list");
const titleEl = $("title");
$("listToggle").onclick = () => listEl.classList.toggle("open");

// Filter: "open" (live in claude-mgr, the default, matching the desktop's
// active-only view) / "waiting" (needs you) / "all".
let filter = "open";
for (const b of document.querySelectorAll("#filter button")) {
  b.onclick = () => {
    filter = b.dataset.f;
    for (const o of document.querySelectorAll("#filter button")) o.classList.toggle("on", o === b);
    render(lastGroups);
  };
}
function passesFilter(s) {
  if (s.archived) return false;
  if (filter === "open") return s.live;
  if (filter === "waiting") return s.status === "waiting" || s.status === "permission";
  return true; // all (minus archived)
}

// --- session list -----------------------------------------------------------
let lastGroups = [];
let activeId = null;

function render(groups) {
  lastGroups = groups || [];
  groupsEl.innerHTML = "";
  let shown = 0;
  for (const g of lastGroups) {
    const sessions = g.sessions.filter(passesFilter);
    if (!sessions.length) continue;
    const label = document.createElement("div");
    label.className = "group-label";
    label.textContent = g.label;
    groupsEl.appendChild(label);
    for (const s of sessions) {
      groupsEl.appendChild(sessionRow(s));
      shown++;
    }
  }
  if (!shown) {
    const e = document.createElement("div");
    e.className = "empty";
    e.textContent =
      filter === "waiting" ? "Nothing waiting on you." :
      filter === "open" ? "No sessions open in claude-mgr." : "No sessions.";
    groupsEl.appendChild(e);
  }
}

function sessionRow(s) {
  const row = document.createElement("div");
  row.className = "sess " + s.status + (s.pinned ? " pinned" : "") + (s.id === activeId ? " active" : "");
  row.innerHTML =
    `<span class="dot"></span>` +
    `<span class="name"></span>` +
    `<span class="age"></span>` +
    `<button class="rename" title="Rename">✏️</button>`;
  const name = row.querySelector(".name");
  name.textContent = s.name || s.id.slice(0, 8);
  // Only the "needs you" case gets a sub-line; the dot color carries the rest.
  if (s.status === "waiting" || s.status === "permission") {
    const sub = document.createElement("small");
    sub.textContent = "your turn";
    name.appendChild(sub);
  }
  row.querySelector(".age").textContent = relTime(s.lastActive);
  name.onclick = () => selectSession(s.id);
  row.querySelector(".dot").onclick = () => selectSession(s.id);
  row.querySelector(".rename").onclick = (e) => {
    e.stopPropagation();
    renameSession(s);
  };
  return row;
}

// relTime mirrors the desktop's index.RelTime so ages read the same on both.
function relTime(unixSec) {
  if (!unixSec) return "—";
  const d = Date.now() / 1000 - unixSec;
  if (d < 0) return "now";
  if (d < 60) return "just now";
  if (d < 3600) return Math.floor(d / 60) + "m ago";
  if (d < 86400) return Math.floor(d / 3600) + "h ago";
  if (d < 172800) return "yesterday";
  if (d < 604800) return Math.floor(d / 86400) + "d ago";
  return new Date(unixSec * 1000).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

async function renameSession(s) {
  const name = prompt("Rename session", s.name || "");
  if (name === null) return;
  await api(`/api/sessions/${s.id}/rename`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name }),
  });
  loadSessions();
}

async function loadSessions() {
  try {
    const r = await api("/api/sessions");
    if (r.status === 401) {
      showTokenPrompt();
      return;
    }
    if (r.ok) render(await r.json());
  } catch (e) {}
}

// showTokenPrompt asks for the token when none is stored (iOS standalone first
// launch) or a stored one is rejected.
function showTokenPrompt() {
  const gate = $("tokenGate");
  if (!gate.hidden) return;
  gate.hidden = false;
  const input = $("tokenInput");
  input.value = "";
  input.focus();
  const submit = () => {
    const v = input.value.trim();
    if (!v) return;
    token = v;
    localStorage.setItem("cmgr_token", v);
    gate.hidden = true;
    start();
  };
  $("tokenSave").onclick = submit;
  input.onkeydown = (e) => {
    if (e.key === "Enter") submit();
  };
}

// Live updates over SSE (EventSource can't set headers → token in the query).
function startStream() {
  const es = new EventSource(`/api/sessions/stream?token=${encodeURIComponent(token)}`);
  es.onmessage = (e) => {
    try {
      render(JSON.parse(e.data));
    } catch (_) {}
  };
  es.onerror = () => {}; // EventSource auto-reconnects
}

// --- terminal ---------------------------------------------------------------
const term = new Terminal({
  fontSize: 13,
  cursorBlink: true,
  scrollback: 8000,
  theme: { background: "#0b0e14", foreground: "#c8d3e0" },
});
const fit = new FitAddon.FitAddon();
term.loadAddon(fit);
term.open($("term"));
fit.fit();

let ws = null;
let ctrlArmed = false;

function wsSend(obj) {
  if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(obj));
}

function selectSession(id) {
  activeId = id;
  listEl.classList.remove("open");
  const sess = lastGroups.flatMap((g) => g.sessions).find((s) => s.id === id);
  titleEl.textContent = (sess && sess.name) || id.slice(0, 8);
  render(lastGroups);

  if (ws && ws.readyState === WebSocket.OPEN) {
    term.clear();
    wsSend({ type: "select", session: id });
    sendResize();
    return;
  }
  openTerminal(id);
}

function openTerminal(id) {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const q = id ? `&session=${id}` : ""; // no id → show whatever the server selected
  ws = new WebSocket(
    `${proto}://${location.host}/api/terminal?token=${encodeURIComponent(token)}${q}`
  );
  ws.binaryType = "arraybuffer";
  ws.onopen = () => sendResize();
  ws.onmessage = (e) => {
    if (e.data instanceof ArrayBuffer) term.write(new Uint8Array(e.data));
  };
  ws.onclose = () => {
    ws = null;
  };
}

function sendResize() {
  fit.fit();
  wsSend({ type: "resize", cols: term.cols, rows: term.rows });
}

term.onData((d) => {
  if (ctrlArmed && d.length === 1) {
    const c = d.toUpperCase().charCodeAt(0);
    if (c >= 64 && c <= 95) d = String.fromCharCode(c & 0x1f);
    ctrlArmed = false;
    $("ctrlBtn").classList.remove("on");
  }
  wsSend({ type: "input", data: d });
});

// Refit whenever the terminal's box actually changes (layout settle, rotation,
// keyboard) so the tmux pane stays exactly matched to the visible area — no
// stale row count, no blank strip below the content.
const ro = new ResizeObserver(() => sendResize());
ro.observe($("term"));

// Touch scrolling. Claude's TUI uses the alternate screen, so xterm has no local
// scrollback to scroll — native touch does nothing. Forward one-finger drags to
// tmux/Claude as SGR mouse-wheel events (mouse mode is on), exactly like the
// desktop wheel. We capture EVERY drag (always preventDefault) and accumulate
// movement, so behavior is identical whether you drag slow or fast — the earlier
// inconsistency was from only intercepting large drags and letting slow ones
// fall through to xterm.
(function enableTouchScroll() {
  const el = $("term");
  let lastY = null;
  let accum = 0;
  const STEP = 16; // px of drag per wheel notch
  el.addEventListener("touchstart", (e) => {
    if (e.touches.length === 1) { lastY = e.touches[0].clientY; accum = 0; }
    else lastY = null;
  }, { passive: true });
  el.addEventListener(
    "touchmove",
    (e) => {
      if (lastY === null || e.touches.length !== 1) return;
      e.preventDefault(); // always own the drag so xterm/native never competes
      const y = e.touches[0].clientY;
      accum += y - lastY;
      lastY = y;
      let notches = 0;
      while (accum >= STEP) { accum -= STEP; notches++; }   // drag down → older content
      while (accum <= -STEP) { accum += STEP; notches--; }
      if (!notches) return;
      // Fixed position (pane center) so behavior doesn't vary by where you touch.
      const col = Math.max(1, Math.floor(term.cols / 2));
      const row = Math.max(1, Math.floor(term.rows / 2));
      const seq = `\x1b[<${notches > 0 ? 64 : 65};${col};${row}M`;
      wsSend({ type: "input", data: seq.repeat(Math.abs(notches)) });
    },
    { passive: false, capture: true }
  );
  el.addEventListener("touchend", () => { lastY = null; }, { passive: true });
})();

// Only shrink the app for the keyboard when one is actually up; otherwise leave
// the CSS height (100dvh) so the layout fills the real screen and the key bar
// sits directly under the terminal.
if (window.visualViewport) {
  const onViewport = () => {
    const vv = window.visualViewport;
    const keyboardUp = window.innerHeight - vv.height > 120;
    $("app").style.height = keyboardUp ? vv.height + "px" : "";
  };
  window.visualViewport.addEventListener("resize", onViewport);
  window.visualViewport.addEventListener("scroll", onViewport);
}

// --- on-screen key bar ------------------------------------------------------
const decodeSeq = (s) =>
  s.replace(/\\x1b/g, "\x1b").replace(/\\t/g, "\t").replace(/\\r/g, "\r");
for (const b of document.querySelectorAll("#keybar button[data-seq]")) {
  b.onclick = () => wsSend({ type: "input", data: decodeSeq(b.dataset.seq) });
}
$("ctrlBtn").onclick = () => {
  ctrlArmed = !ctrlArmed;
  $("ctrlBtn").classList.toggle("on", ctrlArmed);
};

// --- compose bar ------------------------------------------------------------
// A real input field so iOS dictation/autocorrect work normally (they replace
// partials in place); on submit we send the finished line + Enter to the agent.
// Feeding xterm's hidden textarea directly makes dictation accumulate partials.
$("compose").addEventListener("submit", (e) => {
  e.preventDefault();
  const input = $("composeInput");
  const v = input.value;
  if (v) wsSend({ type: "input", data: v + "\r" });
  input.value = "";
  input.focus();
});

// --- new session ------------------------------------------------------------
let newApp = "claude";
const modal = $("modal");
$("newBtn").onclick = openNewModal;
$("modalCancel").onclick = () => (modal.hidden = true);
modal.onclick = (e) => {
  if (e.target === modal) modal.hidden = true; // tap backdrop to dismiss
};
for (const b of document.querySelectorAll("#appToggle button")) {
  b.onclick = () => {
    newApp = b.dataset.app;
    for (const o of document.querySelectorAll("#appToggle button")) o.classList.toggle("on", o === b);
  };
}

function openNewModal() {
  const seen = new Set();
  const items = [];
  for (const g of lastGroups) {
    if (!g.cwd || seen.has(g.cwd)) continue;
    seen.add(g.cwd);
    items.push({ label: g.label, cwd: g.cwd });
  }
  items.sort((a, b) => a.label.localeCompare(b.label));
  const list = $("projList");
  list.innerHTML = "";
  for (const it of items) {
    const el = document.createElement("div");
    el.className = "proj";
    el.textContent = it.label;
    el.onclick = () => createSession(it.cwd, it.label);
    list.appendChild(el);
  }
  modal.hidden = false;
}

async function createSession(cwd, label) {
  modal.hidden = true;
  activeId = null;
  titleEl.textContent = "new · " + label;
  listEl.classList.remove("open");
  const r = await api("/api/new", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ cwd, app: newApp }),
  });
  if (!r.ok) {
    alert(await r.text());
    return;
  }
  // The server has pointed the remote session at the new window.
  if (ws && ws.readyState === WebSocket.OPEN) {
    term.clear();
    sendResize();
  } else {
    openTerminal(); // no id → shows the server-selected new window
  }
  loadSessions();
}

// --- go ---------------------------------------------------------------------
function start() {
  loadSessions();
  startStream();
}
if (token) start();
else showTokenPrompt();
