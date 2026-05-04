// Veil desktop client — consumer-grade VPN UI.
//
// Hero toggle button, server picker chip, stat strip, quick paste
// for veil:// links, settings as a gear. The technical YAML editor
// is folded into the active server's menu (Edit), and the diagnostic
// log lives in a collapsed drawer at the bottom — out of the way for
// the 95% of operations that are just connect/disconnect.
//
// State persists via tauri-plugin-store; tray menu, OS notifications,
// autostart, and update flow live in the Rust host (lib.rs).

import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import { getCurrentWindow } from "@tauri-apps/api/window";
import { Store } from "@tauri-apps/plugin-store";

const appWindow = getCurrentWindow();

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
  startedAt: 0,
  uptimeTick: 0,
  profiles: [],          // [{id, name, config}]
  activeId: null,
  serverMenuOpen: false,
  view: "main",          // main | settings | editor
  editorBuffer: "",      // when view==='editor'
  settings: {
    autostart: false,
    mimicry: "",
    decoy: false,
    notifications: true,
    mode: "socks5", // socks5 (default) | tun (system-wide, coming soon)
  },
  log: [],
  update: null,
  toast: null,           // { kind: 'info'|'error', text }
  modal: null,           // { title, body, fields, onSubmit, submitLabel }
};

let store = null;

async function bootStore() {
  store = await Store.load(STORE_FILE, { autoSave: true });
  state.profiles = (await store.get(KEY_PROFILES)) || [];
  state.activeId = (await store.get(KEY_ACTIVE)) || null;
  const persistedSettings = await store.get(KEY_SETTINGS);
  if (persistedSettings) state.settings = { ...state.settings, ...persistedSettings };
  try {
    state.settings.autostart = await invoke("get_autostart");
  } catch (_) {}
  setInterval(() => {
    if (state.status === "connected") {
      state.uptimeTick++;
      const u = document.getElementById("uptime");
      if (u) u.textContent = fmtUptime(elapsed());
    }
  }, 1000);
  render();
}

async function persistProfiles() {
  await store.set(KEY_PROFILES, state.profiles);
  await store.set(KEY_ACTIVE, state.activeId);
}
async function persistSettings() {
  await store.set(KEY_SETTINGS, state.settings);
}

// --- main render ---

function render() {
  root.innerHTML = "";
  root.append(renderTitlebar());
  const body = el("div", { class: "appbody" });
  root.append(body);
  switch (state.view) {
    case "settings": renderSettings(body); break;
    case "editor":   renderEditor(body); break;
    default:         renderHome(body);
  }
  if (state.toast) {
    root.append(toastEl(state.toast));
  }
  if (state.modal) {
    root.append(modalEl(state.modal));
  }
}

function renderHome(host) {
  for (const node of renderHero()) host.append(node);
  host.append(renderDrawer());
}

function renderTitlebar() {
  const drag = el("div", { class: "tb-drag", "data-tauri-drag-region": "" },
    el("div", { class: "tb-brand" },
      el("div", { class: "tb-mark" }),
      el("span", {}, titlebarLabel()),
    ),
  );
  const appActions = el("div", { class: "tb-appactions" });
  if (state.view === "main") {
    appActions.append(
      tbActionBtn("paste", "Paste veil:// link", quickPasteLink),
      tbActionBtn("gear",  "Settings",           () => { state.view = "settings"; render(); }),
    );
  } else {
    appActions.append(
      tbActionBtn("back", "Back", () => { state.view = "main"; render(); }),
    );
  }
  const ctrls = el("div", { class: "tb-ctrls" },
    el("button", { class: "tb-btn", title: "Minimize", onclick: () => appWindow.minimize() }, tbIcon("min")),
    el("button", { class: "tb-btn", title: "Maximize", onclick: () => appWindow.toggleMaximize() }, tbIcon("max")),
    el("button", { class: "tb-btn close", title: "Hide to tray", onclick: () => appWindow.hide() }, tbIcon("close")),
  );
  return el("div", { class: "titlebar" }, drag, appActions, ctrls);
}

function titlebarLabel() {
  switch (state.view) {
    case "settings": return "Settings";
    case "editor":   return "Edit server";
    default:         return "Veil";
  }
}

function tbActionBtn(kind, title, onclick) {
  return el("button", { class: "tb-actbtn", title, onclick }, iconSVG(kind));
}

function tbIcon(kind) {
  const wrap = document.createElement("span");
  wrap.style.display = "inline-flex";
  const svgs = {
    min:   `<svg viewBox="0 0 12 12" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.2"><line x1="2.5" y1="6" x2="9.5" y2="6"/></svg>`,
    max:   `<svg viewBox="0 0 12 12" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.2"><rect x="2.5" y="2.5" width="7" height="7" rx="0.5"/></svg>`,
    close: `<svg viewBox="0 0 12 12" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.2"><line x1="3" y1="3" x2="9" y2="9"/><line x1="9" y1="3" x2="3" y2="9"/></svg>`,
  };
  wrap.innerHTML = svgs[kind] || "";
  return wrap;
}

function renderTopbar() {
  return el("div", { class: "topbar" },
    el("div", { class: "topactions" },
      iconBtn("paste", "Paste veil:// link", quickPasteLink),
      iconBtn("gear",  "Settings",           () => { state.view = "settings"; render(); }),
    ),
  );
}

function renderHero() {
  const active = activeProfile();
  const server = renderServerChip(active);
  const button = renderHeroButton();
  const status = el("div", { class: "hero-status" }, statusLabel(state.status));
  const detail = el("div", { class: "hero-detail" }, heroDetailText(active));
  const stats = renderStats();
  return [
    server,
    el("div", { class: "hero" }, button, status, detail),
    stats,
  ];
}

function renderServerChip(active) {
  const label = active ? active.name : "No server";
  const sub   = active ? (extractServerLine(active.config) || "tap to edit") : "Tap to paste a veil:// link";
  const click = () => {
    if (!state.profiles.length) {
      quickPasteLink();
      return;
    }
    state.serverMenuOpen = !state.serverMenuOpen;
    render();
  };
  const chip = el("button", { class: "serverchip", onclick: click },
    el("div", { class: "serverdot " + (active ? "ok" : "empty") }),
    el("div", { class: "servertext" },
      el("div", { class: "servername" }, label),
      el("div", { class: "serversub"  }, sub),
    ),
    el("div", { class: "chevron" }, state.serverMenuOpen ? "▲" : "▼"),
  );
  if (!state.serverMenuOpen) return chip;

  const items = state.profiles.map((p) =>
    el("button", {
      class: "menuitem" + (p.id === state.activeId ? " active" : ""),
      onclick: async () => {
        await selectProfile(p.id);
        state.serverMenuOpen = false;
        render();
      },
    },
      el("div", { class: "menuitem-name" }, p.name),
      el("div", { class: "menuitem-sub" }, extractServerLine(p.config) || ""),
    ),
  );
  const actions = el("div", { class: "menuactions" },
    smallBtn("+ Paste link", quickPasteLink),
    smallBtn("✎ Edit",       () => {
      const a = activeProfile();
      state.editorBuffer = a ? a.config : "";
      state.view = "editor";
      state.serverMenuOpen = false;
      render();
    }, !state.activeId),
    smallBtn("🗑 Delete",    deleteActiveProfile, !state.activeId),
  );
  return el("div", { class: "serverwrap" }, chip, el("div", { class: "menu" }, ...items, actions));
}

function renderHeroButton() {
  const ringClass = "hero-btn " + state.status;
  const onclick = () => {
    if (state.status === "connecting") return;
    if (state.status === "connected") disconnect();
    else                              connect();
  };
  const inner = state.status === "connecting"
    ? el("div", { class: "spinner" })
    : el("div", { class: "power" }, powerIconSVG());
  return el("button", {
    class: ringClass,
    onclick,
    disabled: state.status === "connecting" || (!activeProfile() && state.status !== "connected"),
    title: !activeProfile() ? "Add a server first" : (state.status === "connected" ? "Click to disconnect" : "Click to connect"),
  }, inner);
}

function renderStats() {
  return el("div", { class: "stats" },
    statPill(iconSVG("up"),    fmtBytes(state.bytesTx)),
    statPill(iconSVG("down"),  fmtBytes(state.bytesRx)),
    el("div", { class: "stat", id: "uptime-pill" },
      iconSVG("clock"),
      el("span", { class: "statv", id: "uptime" }, state.status === "connected" ? fmtUptime(elapsed()) : "00:00:00"),
    ),
  );
}

function renderDrawer() {
  const wrap = el("details", { class: "drawer" },
    el("summary", {}, "▾ Diagnostics"),
    el("div", { class: "drawer-body" },
      el("div", { class: "kv compact" },
        kvRow("Transport", state.transport || "—"),
        kvRow("Remote",    state.remote    || "—"),
        kvRow("Last event", state.message  || "—"),
      ),
      el("pre", { class: "log" }, state.log.join("\n") || "(no events yet)"),
    ),
  );
  return wrap;
}

// --- settings page ---

function renderSettings(host) {
  const mode = el("div", { class: "card" },
    cardTitle("Tunnel mode"),
    el("div", { class: "modegrid" },
      modeCard("socks5", "SOCKS5 proxy", "Per-app: configure your browser / app to use 127.0.0.1:1080. Works without admin rights.", state.settings.mode === "socks5"),
      modeCard("tun",    "System-wide TUN", "All traffic transparently via a Wintun adapter. Needs Administrator + wintun.dll next to the app.", state.settings.mode === "tun"),
    ),
  );

  const general = el("div", { class: "card" },
    cardTitle("General"),
    switchRow("Launch at login", state.settings.autostart, async (v) => {
      state.settings.autostart = v;
      await persistSettings();
      try { await invoke("set_autostart", { enabled: v }); }
      catch (e) { toast("Autostart toggle failed: " + e, "error"); }
    }),
    switchRow("OS notifications on connect / error", state.settings.notifications, async (v) => {
      state.settings.notifications = v;
      await persistSettings();
    }),
  );

  const dpi = el("div", { class: "card" },
    cardTitle("Anti-DPI"),
    el("label", { class: "fieldlabel" }, "Mimicry profile"),
    selectField(state.settings.mimicry, [
      ["", "Off — fastest, most fingerprintable"],
      ["browse", "Browsing"],
      ["video", "Video streaming"],
      ["messaging", "Messaging"],
      ["search", "Search"],
    ], async (v) => { state.settings.mimicry = v; await persistSettings(); }),
    switchRow("Decoy cover traffic (when enabled in config)", state.settings.decoy, async (v) => {
      state.settings.decoy = v;
      await persistSettings();
    }),
  );

  const updateBox = el("div", { class: "card" },
    cardTitle("Updates"),
    el("div", { class: "kv compact" },
      kvRow("Current", state.update?.current || "—"),
      kvRow("Latest",  state.update?.latest  || "—"),
      kvRow("Status",
        state.update == null ? "—" :
        state.update.update_available ? "update available" : "up to date"),
    ),
    el("div", { class: "row" },
      smallBtn("Check now", doCheckUpdate),
      primaryBtn("Apply update", doApplyUpdate, !state.update?.update_available),
    ),
  );

  host.append(mode, general, dpi, updateBox);
}

function modeCard(value, title, body, active) {
  const click = async () => {
    if (state.status === "connected" || state.status === "connecting") {
      toast("Disconnect before switching tunnel mode.", "error");
      return;
    }
    state.settings.mode = value;
    await persistSettings();
    render();
  };
  return el("button", {
    class: "modecard" + (active ? " active" : ""),
    onclick: click,
  },
    el("div", { class: "modecard-title" }, title),
    el("div", { class: "modecard-body" }, body),
  );
}

function renderEditor(host) {
  const a = activeProfile();
  const nameInput = el("input", {
    type: "text",
    placeholder: "Server name",
    value: a?.name || "",
    oninput: (ev) => { if (a) a.name = ev.target.value; },
  });
  const ta = el("textarea", {
    spellcheck: "false",
    placeholder: "veil://eyJTZXJ2ZXJzIjpbey4uLn1dfQ\n\n— or —\n\nservers:\n  - type: reality\n    addr: vps.example.com:443\n    sni: www.cloudflare.com\nserver_static_key_b64: ...\nstatic_key_path: /tmp/veil-client.key\nsocks5_listen: 127.0.0.1:1080",
    oninput: (ev) => { state.editorBuffer = ev.target.value; },
  });
  ta.value = state.editorBuffer;
  const save = primaryBtn("Save", async () => {
    if (!a) return;
    a.config = state.editorBuffer;
    await persistProfiles();
    toast("Saved profile \"" + a.name + "\".", "info");
    state.view = "main";
    render();
  });

  const card = el("div", { class: "card" },
    el("label", { class: "fieldlabel" }, "Name"),
    nameInput,
    el("label", { class: "fieldlabel", style: "margin-top: 12px" }, "Configuration (paste a veil:// link or YAML)"),
    ta,
    el("div", { class: "row" }, save),
  );
  host.append(card);
}

// --- helpers ---

function activeProfile() {
  return state.profiles.find((p) => p.id === state.activeId) || null;
}

function elapsed() {
  if (!state.startedAt) return 0;
  return Math.floor((Date.now() - state.startedAt) / 1000);
}

function statusLabel(s) {
  return ({
    idle:        "Disconnected",
    connecting:  "Connecting…",
    connected:   "Connected",
    error:       "Error",
    stopped:     "Disconnected",
  })[s] || s;
}

function heroDetailText(active) {
  if (state.status === "connected") {
    const where = state.transport ? `via ${state.transport}` : "";
    return state.remote ? `${where} → ${state.remote}` : where;
  }
  if (state.status === "error") return state.message || "Connection failed";
  if (!active) return "Add a server to get started";
  return active.name;
}

function extractServerLine(cfg) {
  if (!cfg) return "";
  if (cfg.startsWith("veil://")) return "veil:// share link";
  // Try to grab the first addr from YAML.
  const m = cfg.match(/addr:\s*([^\s\n]+)/);
  if (m) return m[1];
  return "YAML config";
}

function fmtBytes(n) {
  if (!n) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0, v = n;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(i ? 1 : 0)} ${u[i]}`;
}

function fmtUptime(s) {
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), r = s % 60;
  const pad = (n) => String(n).padStart(2, "0");
  return `${pad(h)}:${pad(m)}:${pad(r)}`;
}

function pushLog(line) {
  const ts = new Date().toLocaleTimeString();
  state.log.push(`${ts} ${line}`);
  if (state.log.length > 200) state.log.shift();
  // Avoid full re-render for log-only updates if drawer is closed; keep it simple here.
  render();
}

function toast(text, kind = "info") {
  state.toast = { text, kind };
  render();
  setTimeout(() => {
    if (state.toast && state.toast.text === text) {
      state.toast = null;
      render();
    }
  }, 4000);
}

// --- profile actions ---

function quickPasteLink() {
  openModal({
    title: "Add server",
    body: "Paste a veil:// share link, or paste a YAML configuration.",
    fields: [
      { key: "config", label: "Configuration", placeholder: "veil://eyJTZXJ2ZXJzIjpb...", multiline: true },
      { key: "name",   label: "Name",          placeholder: `Server ${state.profiles.length + 1}` },
    ],
    submitLabel: "Add",
    onSubmit: async (vals) => {
      const cleaned = (vals.config || "").trim();
      if (!cleaned) throw new Error("config is empty");
      const fallbackName = (extractServerLine(cleaned) || `Server ${state.profiles.length + 1}`);
      const name = (vals.name || "").trim() || fallbackName;
      const id = String(Date.now());
      state.profiles.push({ id, name, config: cleaned });
      state.activeId = id;
      await persistProfiles();
      toast(`Added "${name}".`, "info");
      render();
    },
  });
}

function deleteActiveProfile() {
  if (!state.activeId) return;
  const a = activeProfile();
  openModal({
    title: "Delete server",
    body: `Delete "${a?.name}"? This cannot be undone.`,
    submitLabel: "Delete",
    danger: true,
    onSubmit: async () => {
      state.profiles = state.profiles.filter((p) => p.id !== state.activeId);
      state.activeId = state.profiles[0]?.id ?? null;
      await persistProfiles();
      toast("Deleted.", "info");
      render();
    },
  });
}

async function selectProfile(id) {
  state.activeId = id;
  await persistProfiles();
}

// --- connect / disconnect ---

async function connect() {
  const a = activeProfile();
  if (!a) { toast("Add a server first.", "error"); return; }
  const cfg = (a.config || "").trim();
  if (!cfg) { toast("Active server has empty config.", "error"); return; }
  state.status = "connecting";
  state.message = "starting client…";
  state.bytesTx = 0; state.bytesRx = 0;
  pushLog(`connect requested (mode=${state.settings.mode})`);
  try {
    if (state.settings.mode === "tun") {
      await invoke("tun_start", { configText: cfg });
      // tun_start completes once Wintun is up + routes are installed.
      // libveil's EventConnected will arrive on the event channel.
    } else {
      await invoke("veil_start", { configText: cfg });
    }
  } catch (e) {
    state.status = "error";
    state.message = String(e);
    toast(String(e), "error");
    pushLog("connect failed: " + e);
    render();
  }
}

async function disconnect() {
  pushLog("disconnect requested");
  try {
    if (state.settings.mode === "tun") {
      await invoke("tun_stop");
    } else {
      await invoke("veil_stop");
    }
    // libveil's EventDisconnected races against the SDK Drop that
    // clears the C-side callback slot, so the event often never
    // reaches the JS layer. Once stop returns OK the session is
    // gone — flip the UI ourselves.
    state.status = "stopped";
    state.startedAt = 0;
    state.transport = "";
    state.remote = "";
    state.message = "";
    pushLog("stopped");
    render();
  } catch (e) {
    toast("Disconnect failed: " + e, "error");
    pushLog("disconnect failed: " + e);
  }
}

async function doCheckUpdate() {
  toast("Checking for updates…", "info");
  try {
    state.update = await invoke("check_update");
    pushLog(`update check: latest=${state.update.latest} ${state.update.update_available ? "(available)" : "(up to date)"}`);
  } catch (e) {
    toast("Update check failed: " + e, "error");
    state.update = null;
  }
  render();
}

function doApplyUpdate() {
  if (!state.update?.update_available) return;
  openModal({
    title: "Install update",
    body: `Download and install ${state.update.latest}? The app will need to restart afterwards.`,
    submitLabel: "Install",
    onSubmit: async () => {
      toast("Applying update…", "info");
      try {
        await invoke("apply_update");
        toast("Update installed; restart the app.", "info");
      } catch (e) {
        throw new Error("Update apply failed: " + e);
      }
    },
  });
}

// --- event wiring ---

listen("veil-event", (msg) => {
  const e = msg.payload || {};
  if (e.transport) state.transport = e.transport;
  if (e.remote) state.remote = e.remote;
  const txBumped = typeof e.bytes_tx === "number" && e.bytes_tx !== state.bytesTx;
  const rxBumped = typeof e.bytes_rx === "number" && e.bytes_rx !== state.bytesRx;
  if (typeof e.bytes_tx === "number") state.bytesTx = e.bytes_tx;
  if (typeof e.bytes_rx === "number") state.bytesRx = e.bytes_rx;
  if (e.message) state.message = e.message;
  // Brief flash on the stat values when bytes change, so users see
  // throughput "breathing" at a glance.
  if (txBumped || rxBumped) {
    requestAnimationFrame(() => {
      document.querySelectorAll(".statv").forEach((n) => {
        n.classList.add("bump");
        setTimeout(() => n.classList.remove("bump"), 220);
      });
    });
  }
  switch (e.type) {
    case 1:
      state.status = "connected";
      state.startedAt = Date.now();
      pushLog(`connected via ${e.transport} → ${e.remote}`);
      break;
    case 2:
      state.status = state.status === "connecting" ? "error" : "stopped";
      state.startedAt = 0;
      pushLog("disconnected");
      break;
    case 3:
      state.status = "error";
      state.startedAt = 0;
      pushLog("error: " + (e.message || "?"));
      break;
    case 4:
      // skip the log spam — bytes already update in the stats row
      break;
    case 5: pushLog(`transport switch: ${e.transport}`); break;
  }
  render();
});

listen("tray-action", (msg) => {
  const action = msg.payload;
  if (action === "connect") connect();
  else if (action === "disconnect") disconnect();
});

// --- DOM helpers ---

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

function statPill(label, value) {
  const k = typeof label === "string"
    ? el("span", { class: "statk" }, label)
    : label;
  return el("div", { class: "stat" },
    k,
    el("span", { class: "statv" }, value),
  );
}

function iconBtn(kind, title, onclick) {
  return el("button", { class: "iconbtn", title, onclick }, iconSVG(kind));
}

function smallBtn(label, onclick, disabled = false) {
  return el("button", { class: "smallbtn", onclick, disabled }, label);
}

function primaryBtn(label, onclick, disabled = false) {
  return el("button", { class: "primary", onclick, disabled }, label);
}

function cardTitle(t) { return el("div", { class: "cardtitle" }, t); }

function switchRow(label, value, onchange) {
  const cb = el("input", { type: "checkbox", onchange: (ev) => onchange(ev.target.checked) });
  if (value) cb.checked = true;
  return el("label", { class: "switchrow" }, cb, el("span", {}, label));
}

function selectField(value, options, onchange) {
  const sel = el("select", { onchange: (ev) => onchange(ev.target.value) });
  for (const [v, txt] of options) {
    const o = el("option", { value: v }, txt);
    if (v === value) o.selected = true;
    sel.append(o);
  }
  return sel;
}

function toastEl(t) {
  return el("div", { class: "toast " + t.kind }, t.text);
}

// Modal: { title, body?, fields?: [{key,label,placeholder,multiline,initial}], submitLabel?, danger?, onSubmit(values), cancelLabel? }
function openModal(opts) {
  state.modal = { ...opts, _values: {}, _refs: {} };
  // seed initial values
  for (const f of opts.fields || []) {
    state.modal._values[f.key] = f.initial ?? "";
  }
  render();
  // focus first input
  setTimeout(() => {
    const first = document.querySelector(".modal-card input, .modal-card textarea");
    if (first) first.focus();
  }, 0);
}
function closeModal() { state.modal = null; render(); }

function modalEl(m) {
  const fields = (m.fields || []).map((f) => {
    const props = {
      placeholder: f.placeholder || "",
      oninput: (ev) => { m._values[f.key] = ev.target.value; },
      onkeydown: (ev) => {
        if (ev.key === "Escape") { closeModal(); }
        if (ev.key === "Enter" && !f.multiline && !ev.shiftKey) {
          ev.preventDefault();
          submit();
        }
      },
    };
    const input = f.multiline ? el("textarea", props) : el("input", { type: "text", ...props });
    input.value = m._values[f.key] || "";
    const wrap = el("div", { class: "modal-field" });
    if (f.label) wrap.append(el("label", { class: "fieldlabel" }, f.label));
    wrap.append(input);
    return wrap;
  });

  const submit = async () => {
    try {
      const r = m.onSubmit(m._values);
      if (r && typeof r.then === "function") await r;
    } catch (e) {
      toast(String(e), "error");
      return;
    }
    closeModal();
  };

  const card = el("div", { class: "modal-card" },
    el("div", { class: "modal-title" }, m.title),
    m.body ? el("div", { class: "modal-body" }, m.body) : null,
    ...fields,
    el("div", { class: "modal-actions" },
      el("button", { class: "smallbtn", onclick: closeModal }, m.cancelLabel || "Cancel"),
      el("button", { class: m.danger ? "danger" : "primary", onclick: submit }, m.submitLabel || "OK"),
    ),
  );
  return el("div", { class: "modal-backdrop", onclick: (ev) => { if (ev.target === ev.currentTarget) closeModal(); } }, card);
}

function powerIconSVG() {
  const wrap = document.createElement("span");
  wrap.innerHTML = `<svg viewBox="0 0 24 24" width="64" height="64" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M18.36 6.64a9 9 0 1 1-12.73 0"/><line x1="12" y1="2" x2="12" y2="12"/></svg>`;
  return wrap.firstElementChild;
}

function iconSVG(kind) {
  const wrap = document.createElement("span");
  wrap.style.display = "inline-flex";
  const sizes = { up: 13, down: 13, clock: 13 };
  const sz = sizes[kind] || 18;
  const svgs = {
    gear:  `<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09a1.65 1.65 0 0 0 1.51-1 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33h0a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51h0a1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82v0a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>`,
    paste: `<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>`,
    back:  `<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="19" y1="12" x2="5" y2="12"/><polyline points="12 19 5 12 12 5"/></svg>`,
    up:    `<svg viewBox="0 0 24 24" width="${sz}" height="${sz}" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="19" x2="12" y2="5"/><polyline points="5 12 12 5 19 12"/></svg>`,
    down:  `<svg viewBox="0 0 24 24" width="${sz}" height="${sz}" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="5" x2="12" y2="19"/><polyline points="19 12 12 19 5 12"/></svg>`,
    clock: `<svg viewBox="0 0 24 24" width="${sz}" height="${sz}" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>`,
  };
  wrap.innerHTML = svgs[kind] || "";
  return wrap;
}

bootStore();
