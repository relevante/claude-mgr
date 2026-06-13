"use strict";
// claude-mgr mobile client. The session list (with all the rail's actions as
// taps) replaces the tmux keybindings; the terminal is a real xterm.js viewport
// bridged to a tmux client over a WebSocket.

// --- auth: token comes in the page URL once, then persists locally ----------
const url = new URL(location.href);
let token = url.searchParams.get("token");
if (token) {
  localStorage.setItem("cmgr_token", token);
  url.searchParams.delete("token");
  history.replaceState(null, "", url.pathname);
} else {
  token = localStorage.getItem("cmgr_token") || "";
}

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
    `<span class="app"></span>` +
    `<span class="acts">` +
    `<button data-act="pin" title="Pin">📌</button>` +
    `<button data-act="rename" title="Rename">✏️</button>` +
    `<button data-act="archive" title="Archive">🗄️</button>` +
    `<button data-act="kill" title="Kill">⛔️</button>` +
    `</span>`;
  const name = row.querySelector(".name");
  name.textContent = s.name || s.id.slice(0, 8);
  if (s.live && s.status !== "idle") {
    const sub = document.createElement("small");
    sub.textContent = s.status === "waiting" || s.status === "permission" ? "your turn" : s.status;
    name.appendChild(sub);
  }
  row.querySelector(".app").textContent = s.app;
  name.onclick = () => selectSession(s.id);
  row.querySelector(".dot").onclick = () => selectSession(s.id);
  for (const b of row.querySelectorAll(".acts button")) {
    b.onclick = (e) => {
      e.stopPropagation();
      doAction(s, b.dataset.act);
    };
  }
  return row;
}

async function doAction(s, act) {
  if (act === "rename") {
    const name = prompt("Rename session", s.name || "");
    if (name === null) return;
    await api(`/api/sessions/${s.id}/rename`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name }),
    });
  } else if (act === "kill") {
    if (!confirm("Kill this session's process?")) return;
    await api(`/api/sessions/${s.id}/kill`, { method: "POST" });
  } else {
    await api(`/api/sessions/${s.id}/${act}`, { method: "POST" });
  }
  loadSessions();
}

async function loadSessions() {
  try {
    const r = await api("/api/sessions");
    if (r.ok) render(await r.json());
  } catch (e) {}
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
  ws = new WebSocket(
    `${proto}://${location.host}/api/terminal?token=${encodeURIComponent(token)}&session=${id}`
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

window.addEventListener("resize", sendResize);

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

// --- go ---------------------------------------------------------------------
loadSessions();
startStream();
