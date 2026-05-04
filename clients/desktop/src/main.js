// Veil desktop client — Phase 4.7.
//
// Adds a profile manager, settings panel, and tray integration on top
// of the Phase 4.5 connect/disconnect scaffold. State is persisted via
// tauri-plugin-store so profiles + settings survive across launches.

import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import { Store } from "@tauri-apps/plugin-store";

const STORE_FILE = "veil.store.json";
const KEY_PROFILES = "profiles";
const KEY_ACTIVE = "active_profile";
const KEY_SETTINGS = "settings";

const root = document.getElementById("app");

const state = {
  status: "idle", // idle | connecting | connected | error | stopped
  remote: "",
  transport: "",
  bytesTx: 0,
  bytesRx: 0,
  message: "",
  profiles: [],            // [{id, name, config}]
  activeId: null,
  configEditor: "",        // current editor text (may be unsaved)
  configCollapsed: true,
  settings: {
    autostart: false,
    mimicry: "",
    decoy: false,
    notifications: true,
  },
  view: "main",            // main | settings
  log: [],
  update: null,            // {current, latest, update_available, url}
};

let store = null;

async function bootStore() {
  store = await Store.load(STORE_FILE, { autoSave: true });
  state.profiles = (await store.get(KEY_PROFILES)) || [];
  state.activeId = (await store.get(KEY_ACTIVE)) || null;
  const persistedSettings = await store.get(KEY_SETTINGS);
  if (persistedSettings) state.settings = { ...state.settings, ...persistedSettings };
  if (state.activeId) {
    const p = state.profiles.find((p) => p.id === state.activeId);
    if (p) state.configEditor = p.config;
  }
  try {
    state.settings.autostart = await invoke("get_autostart");
  } catch (_) {
    // backend can fail to query autostart in dev; keep persisted value
  }
  render();
}

async function persistProfiles() {
  await store.set(KEY_PROFILES, state.profiles);
  await store.set(KEY_ACTIVE, state.activeId);
}
async function persistSettings() {
  await store.set(KEY_SETTINGS, state.settings);
}

function render() {
  root.innerHTML = "";
  if (state.view === "settings") {
    renderSettings();
    return;
  }
  renderMain();
}

function renderMain() {
  const status = el("div", { class: "statusrow" },
    el("div", { class: "dot " + state.status }),
    el("div", { class: "statustext" }, statusLabel(state.status)),
    el("div", { class: "statushint" }, state.transport ? `via ${state.transport}` : ""),
  );

  const kv = el("div", { class: "card" },
    status,
    el("div", { class: "kv" },
      kvRow("Remote",     state.remote || "—"),
      kvRow("Bytes ↑",    fmtBytes(state.bytesTx)),
      kvRow("Bytes ↓",    fmtBytes(state.bytesRx)),
      kvRow("Last event", state.message || "—"),
    ),
  );

  const profileBar = renderProfileBar();
  const cfgBox = renderConfigCard();

  const connectBtn = el("button", {
    class: "primary",
    disabled: state.status === "connecting" || state.status === "connected",
    onclick: connect,
  }, state.status === "connecting" ? "Connecting…" : "Connect");

  const disconnectBtn = el("button", {
    class: "danger",
    disabled: state.status !== "connected" && state.status !== "connecting",
    onclick: disconnect,
  }, "Disconnect");

  const settingsBtn = el("button", { onclick: () => { state.view = "settings"; render(); } }, "⚙ Settings");

  const log = el("div", { class: "log" }, state.log.join("\n"));

  root.append(
    el("h1", {}, "Veil"),
    el("p", { class: "subtitle" }, "Self-hosted, censorship-resistant VPN."),
    kv,
    profileBar,
    cfgBox,
    el("div", { class: "actions" }, connectBtn, disconnectBtn, settingsBtn),
    log,
  );
}

function renderProfileBar() {
  const select = el("select", {
    onchange: (ev) => switchProfile(ev.target.value),
  });
  if (state.profiles.length === 0) {
    select.append(el("option", { value: "" }, "(no profiles yet)"));
  }
  for (const p of state.profiles) {
    const opt = el("option", { value: p.id }, p.name);
    if (p.id === state.activeId) opt.selected = true;
    select.append(opt);
  }
  return el("div", { class: "card profilebar" },
    el("label", { for: "profile" }, "Profile"),
    el("div", { class: "row" },
      select,
      el("button", { onclick: addProfile }, "+ Add"),
      el("button", {
        disabled: !state.activeId,
        onclick: deleteProfile,
      }, "🗑 Delete"),
    ),
  );
}

function renderConfigCard() {
  const header = el("div", { class: "row collapse-header" },
    el("label", { for: "cfg" }, "Configuration (paste a veil:// link or YAML)"),
    el("button", {
      class: "ghost",
      onclick: () => { state.configCollapsed = !state.configCollapsed; render(); },
    }, state.configCollapsed ? "▼ Show" : "▲ Hide"),
  );
  if (state.configCollapsed) {
    return el("div", { class: "card" }, header);
  }
  const ta = el("textarea", {
    id: "cfg",
    spellcheck: "false",
    placeholder: "veil://eyJTZXJ2ZXJzIjpbey4uLn1dfQ\n\n— or —\n\nservers:\n  - type: reality\n    addr: vps.example.com:443\n    sni: www.cloudflare.com\nserver_static_key_b64: ...\nstatic_key_path: /tmp/veil-client.key\nsocks5_listen: 127.0.0.1:1080",
    oninput: (ev) => { state.configEditor = ev.target.value; },
  });
  ta.value = state.configEditor;
  const saveBtn = el("button", {
    onclick: saveActiveProfileConfig,
    disabled: !state.activeId,
  }, "Save to profile");
  return el("div", { class: "card" },
    header,
    ta,
    el("div", { class: "row" }, saveBtn),
  );
}

function renderSettings() {
  const back = el("button", { onclick: () => { state.view = "main"; render(); } }, "← Back");

  const autostart = renderSwitch("Launch at login", state.settings.autostart, async (v) => {
    state.settings.autostart = v;
    await persistSettings();
    try {
      await invoke("set_autostart", { enabled: v });
    } catch (e) {
      pushLog("autostart toggle failed: " + e);
      state.settings.autostart = !v; // revert
      await persistSettings();
      render();
    }
  });

  const notif = renderSwitch("OS notifications on connect/error", state.settings.notifications, async (v) => {
    state.settings.notifications = v;
    await persistSettings();
  });

  const mimicry = el("div", { class: "row" },
    el("label", { for: "mim" }, "Mimicry profile"),
    selectField("mim", state.settings.mimicry, [
      ["", "(off — fastest, most fingerprintable)"],
      ["browse", "browse"],
      ["video", "video"],
      ["messaging", "messaging"],
      ["search", "search"],
    ], async (v) => { state.settings.mimicry = v; await persistSettings(); }),
  );

  const decoy = renderSwitch("Decoy cover traffic (when supported by config)", state.settings.decoy, async (v) => {
    state.settings.decoy = v;
    await persistSettings();
  });

  const updateBox = el("div", { class: "card" },
    el("h3", { class: "h3" }, "Updates"),
    el("div", { class: "kv" },
      kvRow("Current", state.update?.current || "—"),
      kvRow("Latest",  state.update?.latest  || "—"),
      kvRow("Status",
        state.update == null ? "—" :
        state.update.update_available ? "update available" : "up to date"),
    ),
    el("div", { class: "row" },
      el("button", { onclick: doCheckUpdate }, "Check for updates"),
      el("button", {
        class: "primary",
        disabled: !state.update?.update_available,
        onclick: doApplyUpdate,
      }, "Apply update"),
    ),
  );

  root.append(
    el("h1", {}, "Settings"),
    back,
    el("div", { class: "card settingscard" }, autostart, notif, mimicry, decoy),
    updateBox,
    el("p", { class: "subtitle" }, "Settings persist via tauri-plugin-store (veil.store.json)."),
  );
}

function renderSwitch(label, value, onchange) {
  const checkbox = el("input", {
    type: "checkbox",
    onchange: (ev) => onchange(ev.target.checked),
  });
  if (value) checkbox.checked = true;
  return el("label", { class: "switchrow" },
    checkbox,
    el("span", {}, label),
  );
}

function selectField(id, value, options, onchange) {
  const sel = el("select", { id, onchange: (ev) => onchange(ev.target.value) });
  for (const [v, txt] of options) {
    const o = el("option", { value: v }, txt);
    if (v === value) o.selected = true;
    sel.append(o);
  }
  return sel;
}

function el(tag, props = {}, ...children) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k === "class") e.className = v;
    else if (k.startsWith("on")) e.addEventListener(k.slice(2).toLowerCase(), v);
    else if (v === false || v === null || v === undefined) continue;
    else if (v === true) e.setAttribute(k, "");
    else e.setAttribute(k, v);
  }
  for (const c of children) {
    if (c == null || c === false) continue;
    if (Array.isArray(c)) for (const cc of c) e.append(typeof cc === "string" ? document.createTextNode(cc) : cc);
    else e.append(typeof c === "string" ? document.createTextNode(c) : c);
  }
  return e;
}

function kvRow(k, v) {
  return [el("div", { class: "k" }, k), el("div", { class: "v" }, v)];
}

function statusLabel(s) {
  return ({
    idle: "Disconnected",
    connecting: "Connecting…",
    connected: "Connected",
    error: "Error",
    stopped: "Stopped",
  })[s] || s;
}

function fmtBytes(n) {
  if (!n) return "0 B";
  const u = ["B","KB","MB","GB","TB"];
  let i = 0, v = n;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(i ? 1 : 0)} ${u[i]}`;
}

function pushLog(line) {
  const ts = new Date().toLocaleTimeString();
  state.log.push(`${ts} ${line}`);
  if (state.log.length > 200) state.log.shift();
  render();
}

// --- profile actions ---

async function addProfile() {
  const name = window.prompt("Profile name?");
  if (!name) return;
  const id = String(Date.now());
  state.profiles.push({ id, name, config: state.configEditor || "" });
  state.activeId = id;
  await persistProfiles();
  render();
}

async function deleteProfile() {
  if (!state.activeId) return;
  if (!window.confirm("Delete the active profile?")) return;
  state.profiles = state.profiles.filter((p) => p.id !== state.activeId);
  state.activeId = state.profiles[0]?.id ?? null;
  state.configEditor = state.profiles[0]?.config ?? "";
  await persistProfiles();
  render();
}

async function switchProfile(id) {
  state.activeId = id;
  const p = state.profiles.find((p) => p.id === id);
  state.configEditor = p?.config ?? "";
  await persistProfiles();
  render();
}

async function saveActiveProfileConfig() {
  const p = state.profiles.find((p) => p.id === state.activeId);
  if (!p) return;
  p.config = state.configEditor;
  await persistProfiles();
  pushLog(`saved profile "${p.name}"`);
}

// --- connect / disconnect ---

async function connect() {
  const cfg = (state.configEditor || "").trim();
  if (!cfg) { state.message = "config is empty"; render(); return; }
  state.status = "connecting";
  state.message = "starting client…";
  state.bytesTx = 0; state.bytesRx = 0;
  pushLog("connect requested");
  render();
  try {
    await invoke("veil_start", { configText: cfg });
    pushLog("started");
  } catch (e) {
    state.status = "error";
    state.message = String(e);
    pushLog("connect failed: " + e);
    render();
  }
}

async function disconnect() {
  pushLog("disconnect requested");
  try {
    await invoke("veil_stop");
    pushLog("stopped");
  } catch (e) {
    pushLog("disconnect failed: " + e);
  }
}

// --- update ---

async function doCheckUpdate() {
  pushLog("checking for updates…");
  try {
    state.update = await invoke("check_update");
    pushLog(`update check: latest=${state.update.latest} ${state.update.update_available ? "(available)" : "(up to date)"}`);
  } catch (e) {
    pushLog("update check failed: " + e);
    state.update = null;
  }
  render();
}

async function doApplyUpdate() {
  if (!state.update?.update_available) return;
  if (!window.confirm("Download and install the latest release? The app will need to restart.")) return;
  pushLog("applying update…");
  try {
    await invoke("apply_update");
    pushLog("update installed; restart the app to use the new binary.");
  } catch (e) {
    pushLog("update apply failed: " + e);
  }
}

// --- event wiring ---

listen("veil-event", (msg) => {
  const e = msg.payload || {};
  if (e.transport) state.transport = e.transport;
  if (e.remote) state.remote = e.remote;
  if (e.bytes_tx) state.bytesTx = e.bytes_tx;
  if (e.bytes_rx) state.bytesRx = e.bytes_rx;
  if (e.message) state.message = e.message;
  switch (e.type) {
    case 1: state.status = "connected"; pushLog(`connected via ${e.transport} → ${e.remote}`); break;
    case 2: state.status = state.status === "connecting" ? "error" : "stopped"; pushLog("disconnected"); break;
    case 3: state.status = "error"; pushLog("error: " + (e.message || "?")); break;
    case 4: pushLog(`traffic tx=${fmtBytes(e.bytes_tx)} rx=${fmtBytes(e.bytes_rx)}`); break;
    case 5: pushLog(`transport switch: ${e.transport}`); break;
  }
  render();
});

// Tray menu emits "tray-action" with one of "connect"|"disconnect".
// The tray and the in-window buttons drive the same code paths so
// state stays consistent however the user triggered the action.
listen("tray-action", (msg) => {
  const action = msg.payload;
  if (action === "connect") connect();
  else if (action === "disconnect") disconnect();
});

bootStore();
