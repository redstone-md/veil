// Veil desktop — UI ported pixel-perfect from /design.
//
// State management: Zustand vanilla store + TanStack QueryClient
// (see ./store.js). The shell (titlebar + sidebar) mounts ONCE and
// stays in the DOM across navigation; only the main pane swaps.
// Combined with TanStack's cached invokes, this kills the navigation
// flicker the old `root.innerHTML = ""` pattern introduced.

import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import { getCurrentWindow } from "@tauri-apps/api/window";
import { getVersion } from "@tauri-apps/api/app";
import { Store } from "@tauri-apps/plugin-store";
import { relaunch } from "@tauri-apps/plugin-process";

// Resolved once at module load. The label nodes that render this
// (sidebar foot + onboarding foot) read it lazily, so the empty
// initial value gets replaced before paint.
let _appVersion = "";
getVersion().then((v) => {
  _appVersion = `v${v}`;
  // Patch any already-rendered labels by class.
  document.querySelectorAll("[data-app-version]").forEach((n) => {
    n.textContent = _appVersion;
  });
}).catch(() => { _appVersion = "v?"; });

import { state, set, subscribeAll, throughput, liveStats, queryClient, cachedInvoke, invalidate } from "./store.js";

const appWindow = getCurrentWindow();
const STORE_FILE = "veil.store.json";

const root = document.getElementById("app");
let store = null;

async function bootStore() {
  store = await Store.load(STORE_FILE, { autoSave: true });
  set({
    profiles: (await store.get("profiles")) || [],
    activeId: (await store.get("active_profile")) || null,
  });
  const persisted = await store.get("settings");
  if (persisted) set((s) => ({ settings: { ...s.settings, ...persisted } }));
  if (state.activeId) {
    const p = state.profiles.find((p) => p.id === state.activeId);
    if (p) state.configEditor = p.config;
  }
  try {
    const a = await invoke("get_autostart");
    set((s) => ({ settings: { ...s.settings, autostart: a } }));
  } catch (_) {}

  // Uptime tick — only thing left on a setInterval. Sampling is now
  // event-driven (see the kind=4 case in the veil-event listener
  // below) so the chart reacts the instant fresh metrics arrive
  // from the 250 ms Rust poller.
  setInterval(() => {
    if (state.status !== "connected") return;
    if (state.view === "home") {
      const upt = document.getElementById("uptime");
      if (upt) upt.textContent = fmtUptime(elapsed());
    }
  }, 1000);

  // Subscribe once. Every store change → coalesced render() via RAF.
  // Throughput tick subscribers fire here too but the persistent
  // shell means only the main pane is rebuilt — no flash.
  subscribeAll(() => scheduleRender());

  render();
}

async function persistProfiles() {
  await store.set("profiles", state.profiles);
  await store.set("active_profile", state.activeId);
}
async function persistSettings() { await store.set("settings", state.settings); }

// Patch a single settings field (or several) — wraps Zustand
// immutability + persistence in one call so call sites stay terse.
function patchSettings(patch) {
  set((s) => ({ settings: { ...s.settings, ...patch } }));
  persistSettings().catch((e) => console.warn("settings persist failed", e));
}

// ─── render orchestration (persistent shell, swap-only-main) ────
//
// Mounted DOM nodes are cached so navigation never tears down the
// titlebar / sidebar / overlays. Only the active main pane is
// rebuilt — and even that swaps in via replaceWith so layout doesn't
// reflow from zero. Toast + modal are siblings of the shell, so they
// don't get clobbered on view changes.

const mounted = {
  titlebar: null,
  shell: null,
  sidebar: null,
  main: null,
  toast: null,
  modal: null,
  shellMode: null, // "default" | "onboarding" | "update"
};

let _renderQueued = false;
function scheduleRender() {
  if (_renderQueued) return;
  _renderQueued = true;
  requestAnimationFrame(() => { _renderQueued = false; render(); });
}

function render() {
  // Decide shell mode this frame.
  let mode = "default";
  if (state.profiles.length === 0 && state.view !== "editor") mode = "onboarding";
  else if (state.view === "update") mode = "update";

  // Shell mode change → tear down the whole shell once and remount
  // (rare — happens at most a few times in a session).
  if (mode !== mounted.shellMode) {
    if (mounted.titlebar) mounted.titlebar.remove();
    if (mounted.shell)    mounted.shell.remove();
    mounted.titlebar = renderTitlebar();
    mounted.shell    = el("div", { class: "shell shell-" + mode });
    if (mode === "default") {
      mounted.sidebar = renderSidebar();
      mounted.main    = el("div", { class: "main" });
      mounted.shell.append(mounted.sidebar, mounted.main);
    } else if (mode === "onboarding") {
      mounted.sidebar = null;
      mounted.main    = el("div", { class: "main onboarding-shell" });
      mounted.shell.append(mounted.main);
    } else {
      mounted.sidebar = null;
      mounted.main    = el("div", { class: "main update-shell" });
      mounted.shell.append(mounted.main);
    }
    root.append(mounted.titlebar, mounted.shell);
    mounted.shellMode = mode;
  } else {
    // Same shell mode — swap titlebar (label may differ) + sidebar
    // (active item, profile count) in place. Main pane gets emptied.
    const newTb = renderTitlebar();
    mounted.titlebar.replaceWith(newTb);
    mounted.titlebar = newTb;
    if (mounted.sidebar) {
      const newSb = renderSidebar();
      mounted.sidebar.replaceWith(newSb);
      mounted.sidebar = newSb;
    }
    mounted.main.innerHTML = "";
  }

  // Render the active view into the main pane.
  if (mode === "onboarding") {
    renderOnboarding(mounted.main);
  } else if (mode === "update") {
    renderUpdate(mounted.main);
  } else {
    switch (state.view) {
      case "profiles": renderProfiles(mounted.main); break;
      case "logs":     renderLogs(mounted.main); break;
      case "settings": renderSettings(mounted.main); break;
      case "editor":   renderEditor(mounted.main); break;
      default:         renderHome(mounted.main);
    }
  }

  // Overlays (toast / modal) — keep as siblings of the shell.
  if (mounted.toast) { mounted.toast.remove(); mounted.toast = null; }
  if (mounted.modal) { mounted.modal.remove(); mounted.modal = null; }
  if (state.toast) { mounted.toast = toastEl(state.toast); root.append(mounted.toast); }
  if (state.modal) { mounted.modal = modalEl(state.modal); root.append(mounted.modal); }
}

// ─── titlebar ───────────────────────────────────────────────────

function renderTitlebar() {
  const drag = el("div", { class: "tb-drag", "data-tauri-drag-region": "" },
    el("span", { class: "tb-brand" }, veilMarkSVG(14), el("span", null, titlebarLabel())),
  );
  const ctrls = el("div", { class: "tb-ctrls" },
    el("button", { class: "tb-btn", title: "Minimize", onclick: () => appWindow.minimize() }, tbCtrlIcon("min")),
    el("button", { class: "tb-btn", title: "Maximize", onclick: () => appWindow.toggleMaximize() }, tbCtrlIcon("max")),
    el("button", { class: "tb-btn close", title: "Hide to tray", onclick: () => appWindow.hide() }, tbCtrlIcon("close")),
  );
  return el("div", { class: "titlebar" }, drag, ctrls);
}
function tbCtrlIcon(kind) {
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
function titlebarLabel() {
  switch (state.view) {
    case "profiles": return "Veil — Profiles";
    case "logs":     return "Veil — Logs";
    case "settings": return "Veil — Settings";
    case "editor":   return "Veil — Edit server";
    default:         return "Veil";
  }
}

// ─── sidebar ────────────────────────────────────────────────────

function renderSidebar() {
  const active = activeProfile();
  const sb = el("div", { class: "sidebar" });
  sb.append(el("div", { class: "sb-brand" },
    veilMarkSVG(20),
    el("div", { style: "flex:1; min-width:0" },
      el("div", { class: "sb-brand-name" }, "Veil"),
      el("div", { class: "sb-brand-profile" }, active ? active.name : "no server"),
    ),
  ));
  sb.append(el("div", { class: "sb-divider" }));
  sb.append(navItem("home",     "Connection", glyph("shield", 15)));
  sb.append(navItem("profiles", "Profiles",   glyph("list", 15), state.profiles.length || null));
  sb.append(navItem("logs",     "Logs",       glyph("terminal", 15)));
  sb.append(navItem("settings", "Settings",   glyph("settings", 15)));
  sb.append(el("div", { style: "flex:1" }));
  sb.append(el("div", { class: "sb-foot" },
    el("span", { "data-app-version": "1" }, _appVersion || "…"),
    el("span", { class: "sb-tag" }, "PRE-ALPHA"),
  ));
  return sb;
}
function navItem(view, label, icon, badge) {
  const cls = "sb-item" + (state.view === view ? " active" : "");
  return el("button", { class: cls, onclick: () => { state.view = view; render(); } },
    el("span", { class: "sb-icon" }, icon),
    el("span", { class: "sb-label" }, label),
    badge ? el("span", { class: "sb-badge" }, String(badge)) : null,
  );
}

// ─── home ──────────────────────────────────────────────────────

function renderHome(host) {
  const labels = {
    idle:        { title: "Disconnected", sub: "Tap to connect" },
    stopped:     { title: "Disconnected", sub: "Tap to connect" },
    connecting:  { title: "Connecting…",  sub: "Probing transports" },
    connected:   { title: "Protected",    sub: "All traffic is tunneled" },
    error:       { title: "Connection failed", sub: liveStats.message || "Server unreachable" },
  };
  const cur = labels[state.status] || labels.idle;
  const isUp = state.status === "connected";
  const isConnecting = state.status === "connecting";
  const active = activeProfile();
  const transportLabel = liveStats.transport
    ? liveStats.transport.charAt(0).toUpperCase() + liveStats.transport.slice(1)
    : (state.settings.transport === "auto" ? "Auto" : state.settings.transport.toUpperCase());

  // While connecting, prefer the latest TUN-progress label over the
  // generic "Probing transports". The id'd elements are also patched
  // in place by the tun-progress listener so subsequent stages don't
  // require a re-render.
  const subText = !active
    ? "Add a server first"
    : isConnecting && liveStats.tunProgress
      ? liveStats.tunProgress.label + "…"
      : cur.sub;
  const stepText = isConnecting && liveStats.tunProgress
    ? `${liveStats.tunProgress.step}/${liveStats.tunProgress.total}`
    : "";

  const top = el("div", { class: "home-top" },
    el("div", { class: "status-block" },
      el("div", { class: "status-eyebrow" },
        "STATUS",
        el("span", {
          id: "status-step",
          class: "status-step",
          style: stepText ? "" : "display:none",
        }, stepText),
      ),
      el("div", { class: "status-title" }, cur.title),
      el("div", { class: "status-sub", id: "status-sub" }, subText),
      el("div", { class: "pill-row" },
        transportPill(transportLabel, isUp),
        isUp ? transportPill("multiplex×8", true, true) : null,
        isUp && state.settings.mimicry ? transportPill(`mimic: ${state.settings.mimicry}`, true, true) : null,
      ),
    ),
    statusOrb(state.status, () => onOrbClick()),
  );

  const thru = el("div", { class: "card flush" },
    el("div", { class: "thru-head" },
      el("div", {},
        el("div", { class: "thru-eyebrow" }, "Throughput"),
        el("div", { class: "thru-row" },
          el("div", {},
            el("span", { class: "thru-down", id: "thru-dn" }, isUp ? mbps(0) : "0.0"),
            el("span", { class: "thru-down-unit" }, "MB/s ↓"),
          ),
          el("div", {},
            el("span", { class: "thru-up", id: "thru-up" }, isUp ? mbps(0) : "0.0"),
            el("span", { class: "thru-up-unit" }, "MB/s ↑"),
          ),
        ),
      ),
      el("div", { class: "thru-meta" },
        "60s · uptime ",
        el("strong", { id: "uptime" }, isUp ? fmtUptime(elapsed()) : "—"),
      ),
    ),
    buildChart(),
  );

  const stats = el("div", { class: "stats-grid" },
    statCell("Down",    isUp ? fmtBytes(liveStats.bytesRx) : "—", "stat-down"),
    statCell("Up",      isUp ? fmtBytes(liveStats.bytesTx) : "—", "stat-up"),
    statCell("Streams", isUp ? "—" : "—"),
    statCell("Latency", isUp ? "—" : "—"),
  );

  host.append(el("div", { class: "home-pad" }, top, thru, stats));
}

function statCell(label, value, id) {
  return el("div", { class: "stat-cell" },
    el("div", { class: "label" }, label),
    el("div", { class: "value", id }, value),
  );
}

function onOrbClick() {
  if (state.status === "connecting") return;
  if (state.status === "connected") disconnect();
  else                              connect();
}

// ─── profiles ──────────────────────────────────────────────────

function renderProfiles(host) {
  const head = el("div", { class: "page-head" },
    el("div", {},
      el("div", { class: "page-title" }, "Profiles"),
      el("div", { class: "page-sub" }, `${state.profiles.length} configured · paste a veil:// link to add`),
    ),
    el("button", { class: "primary", onclick: openPasteLink }, glyph("plus", 13), "Add profile"),
  );

  const input = el("input", { class: "paste-input", type: "text", placeholder: "paste a share link or drag a config file" });
  const pasteRow = el("div", { class: "paste-row" },
    el("span", { class: "paste-prefix" }, "veil://"),
    input,
    el("button", { class: "subtle", onclick: () => importPaste(input.value) }, "Import"),
  );

  const list = el("div", { class: "card flush", style: "flex:1; overflow:hidden" });
  if (state.profiles.length === 0) {
    list.append(el("div", { style: "padding: 32px; text-align: center; color: var(--text-dim); font-size: 13px" }, "No profiles yet — paste a veil:// link above."));
  } else {
    for (const p of state.profiles) {
      const isActive = p.id === state.activeId;
      const transports = extractTransportTypes(p.config);
      const row = el("div", { class: "profile-row" + (isActive ? " active" : ""), onclick: () => selectProfile(p.id) },
        el("div", { class: "profile-icon" }, glyph("server", 14)),
        el("div", { class: "profile-meta" },
          el("div", { class: "profile-name-row" },
            el("span", { class: "profile-name" }, p.name),
            isActive ? el("span", { class: "tag-active" }, "ACTIVE") : null,
          ),
          el("div", { class: "profile-host" }, extractHostLine(p.config) || "—"),
        ),
        el("div", { class: "row-pills" },
          ...transports.map((t) => transportPill(t.toUpperCase(), isActive, true)),
        ),
        el("button", { class: "icon-btn", title: "Edit", onclick: (e) => { e.stopPropagation(); openEditor(p); } }, glyph("edit", 13)),
        el("button", { class: "icon-btn", title: "Delete", onclick: (e) => { e.stopPropagation(); openDeleteProfile(p); } }, glyph("trash", 13)),
      );
      list.append(row);
    }
  }

  host.append(el("div", { class: "page-pad" }, head, pasteRow, list));
}

// ─── logs ──────────────────────────────────────────────────────

function renderLogs(host) {
  const head = el("div", { class: "logs-pad" },
    el("div", {},
      el("div", { class: "page-title" }, "Event log"),
      el("div", { class: "page-sub" }, `Tailing in real time · ${state.log.length} events`),
    ),
    el("div", { style: "display:flex; gap:6px" },
      segmented(["all", "info", "warn", "err"], "all", () => {}),
      el("button", { class: "subtle", onclick: () => copyToClipboard(state.log.join("\n")) }, glyph("copy", 12), "Copy"),
    ),
  );

  const body = el("div", { class: "logs-body" });
  if (state.log.length === 0) {
    body.append(el("div", { class: "log-line" },
      el("span", { class: "log-msg", style: "color: var(--text-mute)" }, "(no events yet — connect to start the log)")));
  } else {
    for (const line of state.log) {
      const m = line.match(/^(\d\d:\d\d:\d\d)\s+(.*)$/);
      const t = m ? m[1] : "";
      const msg = m ? m[2] : line;
      const lvl = msg.startsWith("error") || msg.startsWith("connect failed") ? "err"
                : msg.includes("retry") || msg.startsWith("transport switch") ? "warn"
                : "info";
      body.append(el("div", { class: "log-line" },
        el("span", { class: "log-time" }, t),
        el("span", { class: "log-lvl " + lvl }, lvl),
        el("span", { class: "log-msg" }, msg),
      ));
    }
  }
  body.append(el("div", { class: "log-line" },
    el("span", { class: "log-time" }, ""),
    el("span", { class: "log-cursor" }, "▌"),
  ));

  host.append(head, body);
}

// ─── settings ──────────────────────────────────────────────────

function renderSettings(host) {
  const pad = el("div", { class: "settings-pad" });
  pad.append(
    el("div", { class: "page-title" }, "Settings"),
    el("div", { class: "page-sub", style: "margin-bottom:14px" }, "Preferences sync to the local store only — Veil never phones home."),
  );

  pad.append(sectionHeader("GENERAL"));
  pad.append(settingsRow("Launch at login", "Start Veil when you sign in.",
    toggle(state.settings.autostart, async (v) => {
      patchSettings({ autostart: v });
      try { await invoke("set_autostart", { enabled: v }); }
      catch (e) { toast("Autostart toggle failed: " + e, "error"); }
    })));
  pad.append(settingsRow("Show notifications", "Connect, error, transport switch.",
    toggle(state.settings.notifications, (v) => patchSettings({ notifications: v }))));

  pad.append(el("div", { style: "height:18px" }));
  pad.append(sectionHeader("TRANSPORT"));
  pad.append(settingsRow("Adaptive transport", "Race Reality → WSS → QUIC → MASQUE.",
    segmented(["auto", "reality", "wss", "quic", "masque"], state.settings.transport, (v) => patchSettings({ transport: v }))));
  pad.append(settingsRow("Statistical mimicry", "Pad + jitter packets to look like browsing.",
    segmented(["off", "browse", "video", "msg"], state.settings.mimicry || "off",
      (v) => patchSettings({ mimicry: v === "off" ? "" : v }))));
  pad.append(settingsRow("Decoy traffic", "Inject cover requests when idle.",
    toggle(state.settings.decoy, (v) => patchSettings({ decoy: v }))));

  pad.append(el("div", { style: "height:18px" }));
  pad.append(sectionHeader("ROUTING"));
  pad.append(el("div", { class: "modegrid" },
    modeCard("socks5", "SOCKS5 proxy",    "Per-app: 127.0.0.1:1080. No admin.",   state.settings.mode === "socks5"),
    modeCard("tun",    "System-wide TUN", "Wintun adapter. Needs Administrator.", state.settings.mode === "tun"),
  ));
  if (state.settings.mode === "tun") {
    const ta = el("textarea", {
      placeholder: "192.168.0.0/16\n10.0.0.0/8\n# Apex Legends edge:\n52.40.0.0/14",
      oninput: (ev) => patchSettings({ bypassCidrs: ev.target.value }),
    });
    ta.value = state.settings.bypassCidrs || "";
    pad.append(
      el("div", { class: "fieldlabel", style: "margin-top:14px" }, "Always direct (CIDR per line — LAN, gaming)"),
      ta,
      el("div", { class: "page-sub", style: "margin-top:6px" }, "These networks bypass the tunnel. Veil's server IPs are auto-included."),
    );
  }

  pad.append(el("div", { style: "height:18px" }));
  pad.append(sectionHeader("UPDATES"));
  pad.append(settingsRow(
    state.update?.update_available ? `Update available — ${state.update.latest}` : "Check for updates",
    state.update ? `Current ${state.update.current}` : "Press Check to query GitHub Releases.",
    el("div", { style: "display:flex; gap:6px" },
      el("button", { class: "subtle", onclick: doCheckUpdate }, "Check"),
      el("button", { class: "primary", disabled: !state.update?.update_available,
        onclick: () => { state.view = "update"; render(); } }, "View"),
    ),
  ));

  host.append(pad);
}

function settingsRow(label, sub, control) {
  return el("div", { class: "settings-row" },
    el("div", { class: "text" },
      el("div", { class: "label" }, label),
      sub ? el("div", { class: "sub" }, sub) : null,
    ),
    control,
  );
}
function sectionHeader(label) {
  return el("div", { class: "settings-section-h" }, el("div", { class: "settings-eyebrow" }, label));
}
function modeCard(value, title, body, active) {
  const click = () => {
    if (state.status === "connected" || state.status === "connecting") {
      toast("Disconnect before switching tunnel mode.", "error"); return;
    }
    patchSettings({ mode: value });
  };
  return el("button", { class: "modecard" + (active ? " active" : ""), onclick: click },
    el("div", { class: "modecard-title" }, title),
    el("div", { class: "modecard-body" }, body),
  );
}

// ─── editor ────────────────────────────────────────────────────

function renderEditor(host) {
  const a = activeProfile();
  const head = el("div", { class: "page-head" },
    el("div", { class: "page-title" }, a ? `Edit — ${a.name}` : "Edit server"),
    el("button", { class: "subtle", onclick: () => { state.view = "profiles"; render(); } }, "← Back"),
  );

  const nameInput = el("input", { type: "text", placeholder: "Server name" });
  if (a) nameInput.value = a.name;
  nameInput.oninput = (ev) => { if (a) a.name = ev.target.value; };

  const ta = el("textarea", { spellcheck: "false" });
  ta.value = state.configEditor;
  ta.oninput = (ev) => { state.configEditor = ev.target.value; };

  const card = el("div", { class: "card padded" },
    el("label", { class: "fieldlabel" }, "Name"),
    nameInput,
    el("label", { class: "fieldlabel", style: "margin-top:12px" }, "Configuration"),
    ta,
    el("div", { style: "display:flex; gap:8px; justify-content:flex-end; margin-top:8px" },
      el("button", { class: "primary", onclick: async () => {
        if (!a) return;
        a.config = state.configEditor;
        await persistProfiles();
        toast(`Saved "${a.name}"`, "success");
        state.view = "profiles"; render();
      } }, "Save"),
    ),
  );

  host.append(el("div", { class: "page-pad" }, head, card));
}

// ─── profile actions ──────────────────────────────────────────

function activeProfile() { return state.profiles.find((p) => p.id === state.activeId) || null; }

async function selectProfile(id) {
  state.activeId = id;
  const p = state.profiles.find((p) => p.id === id);
  state.configEditor = p?.config || "";
  await persistProfiles();
  render();
}
function openPasteLink() {
  openModal({
    title: "Add server",
    body: "Paste a veil:// share link your operator gave you, or paste a YAML configuration.",
    fields: [
      { key: "config", label: "Configuration", placeholder: "veil://eyJTZXJ2ZXJzIjpb…", multiline: true },
      { key: "name",   label: "Name",          placeholder: `Server ${state.profiles.length + 1}` },
    ],
    submitLabel: "Add",
    onSubmit: async (v) => {
      const cfg = (v.config || "").trim();
      if (!cfg) throw new Error("config is empty");
      const fallbackName = extractHostLine(cfg) || `Server ${state.profiles.length + 1}`;
      const name = (v.name || "").trim() || fallbackName;
      const id = String(Date.now());
      set((s) => ({
        profiles: [...s.profiles, { id, name, config: cfg }],
        activeId: id,
        configEditor: cfg,
      }));
      await persistProfiles();
      toast(`Added "${name}"`, "success");
    },
  });
}
async function importPaste(text) {
  if (!text) return;
  const id = String(Date.now());
  const name = extractHostLine(text) || `Server ${state.profiles.length + 1}`;
  const cfg = text.trim();
  set((s) => ({
    profiles: [...s.profiles, { id, name, config: cfg }],
    activeId: id,
    configEditor: cfg,
  }));
  await persistProfiles();
  toast(`Imported "${name}"`, "success");
}
function openEditor(p) {
  state.activeId = p.id;
  state.configEditor = p.config;
  state.view = "editor";
  render();
}
function openDeleteProfile(p) {
  openModal({
    title: "Delete profile",
    body: `Delete "${p.name}"? This cannot be undone.`,
    submitLabel: "Delete",
    danger: true,
    onSubmit: async () => {
      state.profiles = state.profiles.filter((x) => x.id !== p.id);
      if (state.activeId === p.id) state.activeId = state.profiles[0]?.id ?? null;
      await persistProfiles();
      toast("Deleted.", "info");
      render();
    },
  });
}

// ─── connect / disconnect ─────────────────────────────────────

async function connect() {
  const a = activeProfile();
  if (!a) { toast("Add a server first.", "error"); return; }
  const cfg = (a.config || "").trim();
  if (!cfg) { toast("Active server has empty config.", "error"); return; }
  liveStats.message = "starting client…";
  liveStats.bytesTx = 0; liveStats.bytesRx = 0;
  liveStats.tunProgress = null;
  throughput.prevTx = 0; throughput.prevRx = 0;
  throughput.lastTs = 0; throughput.lastRx = 0; throughput.lastTx = 0;
  throughput.lastRateRx = 0; throughput.lastRateTx = 0;
  throughput.down = new Array(60).fill(0);
  throughput.up   = new Array(60).fill(0);
  set({ status: "connecting" });
  pushLog(`connect requested (mode=${state.settings.mode})`);

  // Pre-flight admin check for TUN mode. Pop a native UAC/PolicyKit
  // dialog ourselves instead of dead-ending with "right-click → Run
  // as administrator" — one click, app restarts elevated, profile
  // state survives via tauri-plugin-store on disk.
  if (state.settings.mode === "tun") {
    try {
      const elev = await invoke("elevation_status");
      if (!elev.elevated) {
        if (elev.can_request) {
          set({ status: "idle" });
          return openElevationPrompt();
        }
        // Fallback platform — surface the manual-run hint.
        set({ status: "error" });
        toast("TUN mode needs admin/root. Run Veil as administrator (Windows) or with sudo (Linux).", "error");
        return;
      }
    } catch (e) {
      pushLog("elevation probe failed: " + e);
      // Fall through and let tun_start surface the real error.
    }
  }

  try {
    if (state.settings.mode === "tun") {
      const serverIPs = extractServerHosts(cfg);
      const bypassCidrs = (state.settings.bypassCidrs || "")
        .split(/\r?\n/).map((l) => l.replace(/#.*$/, "").trim()).filter(Boolean);
      await invoke("tun_start", { args: { config_text: cfg, bypass_cidrs: bypassCidrs, server_ips: serverIPs } });
    } else {
      await invoke("veil_start", { configText: cfg });
    }
  } catch (e) {
    liveStats.message = String(e);
    set({ status: "error" });
    toast(String(e), "error");
    pushLog("connect failed: " + e);
  }
}

function openElevationPrompt() {
  openModal({
    title: "Restart with admin rights?",
    body: "System-wide TUN mode needs administrator privileges to install routes and create the Wintun adapter. " +
          "Click Restart to confirm via the Windows UAC prompt — Veil will close and reopen with the rights it needs. " +
          "All your servers and settings stay put.\n\n" +
          "Don't want to elevate right now? Click 'Use SOCKS5 instead' to fall back to a per-app proxy on 127.0.0.1:1080. " +
          "You can switch back to TUN any time from Settings → Mode.",
    submitLabel: "Restart with admin",
    cancelLabel: "Use SOCKS5 instead",
    onSubmit: async () => {
      try {
        await invoke("request_elevation");
        // App will exit shortly; show a quiet toast in case the OS
        // prompt is hidden behind something.
        toast("Confirm the UAC prompt to continue…", "info");
      } catch (e) {
        toast("Elevation declined: " + e, "error");
        throw e;
      }
    },
    // Cancel doubles as "fall back to SOCKS5" — flip the persisted
    // mode setting and re-attempt connect immediately so the user
    // gets traffic flowing without a second click.
    onCancel: async () => {
      // set() expects a functional patch that RETURNS the slice diff
      // (the store applies it via setState shallow-merge), not a
      // mutator. See store.js:120.
      set((s) => ({ settings: { ...s.settings, mode: "socks5" } }));
      toast("Switched to SOCKS5 (per-app proxy on 127.0.0.1:1080).", "info");
      await connect();
    },
  });
}

async function disconnect() {
  pushLog("disconnect requested");
  try {
    if (state.settings.mode === "tun") await invoke("tun_stop");
    else                                await invoke("veil_stop");
    liveStats.transport = "";
    liveStats.remote = "";
    liveStats.message = "";
    set({ status: "stopped", startedAt: 0 });
    pushLog("stopped");
  } catch (e) {
    toast("Disconnect failed: " + e, "error");
    pushLog("disconnect failed: " + e);
  }
}

async function doCheckUpdate({ silent = false } = {}) {
  if (!silent) toast("Checking for updates…", "info");
  try {
    state.update = await invoke("check_update");
    pushLog(`update check: latest=${state.update.latest} ${state.update.update_available ? "(available)" : "(up to date)"}`);
    if (!silent && !state.update.update_available) {
      toast(`You're on the latest (${state.update.current}).`, "success");
    }
  } catch (e) {
    if (!silent) toast("Update check failed: " + e, "error");
    pushLog("update check failed: " + e);
  }
  render();
  return state.update;
}

// Fired ~8 s after launch, after the user's persisted profile has
// loaded but before they typically click Connect. Silent on the
// no-update path so the "you're on latest" toast doesn't ambush
// users every cold start. If an update IS available, surface the
// changelog modal so the user can choose Install / Later.
async function maybeAutoCheckUpdate() {
  const u = await doCheckUpdate({ silent: true });
  if (u && u.update_available) {
    showUpdatePrompt();
  }
}

function showUpdatePrompt() {
  if (!state.update?.update_available) return;
  // Render the manifest's `notes` (Markdown from the tag annotation)
  // into a richer DOM tree than the default modal body's plain text.
  // Tiny ad-hoc renderer — no external markdown lib is worth pulling
  // for the handful of constructs release notes actually use:
  // headings (# / ##), inline code (`x`), paragraphs (blank line
  // separator), lists (- / *).
  const bodyNode = renderUpdateNotes(
    state.update.notes && state.update.notes.trim().length > 0
      ? state.update.notes
      : `A new version of Veil is available. Install ${state.update.latest}? The app will restart automatically.`
  );
  openModal({
    title: `Update available — ${state.update.latest}`,
    bodyNode,
    submitLabel: "Install now",
    cancelLabel: "Later",
    onSubmit: async () => {
      // Hand off to the install flow. doApplyUpdate opens its own
      // progress UI (toast-based) and handles the relaunch, so we
      // dismiss this prompt immediately.
      doApplyUpdate();
    },
  });
}

// Tiny markdown → DOM renderer scoped to what release notes contain
// in practice. Splits on blank lines for paragraphs, recognises
// `## heading`, `- item`, and inline `code`. Anything more exotic
// renders as a plain paragraph — a markdown lib would be overkill.
function renderUpdateNotes(text) {
  const root = el("div", { class: "update-notes" });
  const paragraphs = text.replace(/\r\n/g, "\n").split(/\n{2,}/);
  for (const p of paragraphs) {
    const trimmed = p.trim();
    if (!trimmed) continue;

    // Heading
    const h = trimmed.match(/^#{1,6}\s+(.+)$/);
    if (h) {
      root.append(el("h3", { class: "update-h" }, h[1]));
      continue;
    }

    // Bullet list — every line in this paragraph starts with - or *
    const lines = trimmed.split("\n");
    if (lines.every((l) => /^\s*[-*]\s+/.test(l))) {
      const ul = el("ul", { class: "update-ul" });
      for (const l of lines) {
        const item = el("li", null);
        appendInline(item, l.replace(/^\s*[-*]\s+/, ""));
        ul.append(item);
      }
      root.append(ul);
      continue;
    }

    // Plain paragraph; collapse single newlines into spaces (typical
    // hard-wrapped commit/tag messages) so the text reflows in the
    // narrower modal width.
    const para = el("p", { class: "update-p" });
    appendInline(para, lines.join(" "));
    root.append(para);
  }
  return root;
}

// Walk a string of inline content, breaking out backtick-quoted
// spans into <code> elements. Everything else lands as a text node.
function appendInline(parent, s) {
  const parts = s.split(/(`[^`]+`)/g);
  for (const part of parts) {
    if (part.startsWith("`") && part.endsWith("`") && part.length >= 2) {
      parent.append(el("code", { class: "update-code" }, part.slice(1, -1)));
    } else if (part) {
      parent.append(document.createTextNode(part));
    }
  }
}

// Render the install progress as a single self-replacing toast so
// the user sees a live MB / % counter without us re-opening the
// modal on every chunk (which would steal focus mid-typing).
let _updateOff = null;
let _updateOffDone = null;
function doApplyUpdate() {
  if (!state.update?.update_available) return;
  if (_updateOff || _updateOffDone) return; // install already in flight
  toast(`Downloading ${state.update.latest}…`, "info");

  const fmtMB = (b) => (b / 1024 / 1024).toFixed(1);
  let lastShown = 0;

  // Subscribe to progress + finished signals BEFORE invoking apply
  // so we can't race a fast first chunk.
  const wireListeners = async () => {
    _updateOff = await listen("update-progress", (msg) => {
      const p = msg.payload || {};
      const downloaded = p.downloaded || 0;
      const total = p.total || 0;
      const now = Date.now();
      // Throttle toast updates to ~5 Hz so we don't thrash the DOM.
      if (now - lastShown < 200) return;
      lastShown = now;
      const text = total > 0
        ? `Downloading ${state.update.latest}… ${fmtMB(downloaded)} / ${fmtMB(total)} MB (${Math.round(downloaded / total * 100)}%)`
        : `Downloading ${state.update.latest}… ${fmtMB(downloaded)} MB`;
      // Replace the toast in place.
      state.toast = { text, kind: "info" };
      render();
    });
    _updateOffDone = await listen("update-event", async (msg) => {
      if (msg.payload?.kind !== "finished") return;
      _updateOff?.(); _updateOffDone?.();
      _updateOff = _updateOffDone = null;
      toast("Update installed. Restarting…", "success");
      // Tiny delay so the toast actually appears before the relaunch
      // wipes the window.
      await new Promise((r) => setTimeout(r, 1200));
      try { await relaunch(); }
      catch (e) { toast("Relaunch failed: " + e + ". Close the app manually.", "error"); }
    });
  };

  (async () => {
    try {
      await wireListeners();
      await invoke("apply_update");
    } catch (e) {
      _updateOff?.(); _updateOffDone?.();
      _updateOff = _updateOffDone = null;
      toast("Update install failed: " + e, "error");
      pushLog("update apply failed: " + e);
    }
  })();
}

// ─── event channel ─────────────────────────────────────────────
//
// Native traffic events fire MANY times per second. They mutate
// `liveStats` directly (NOT the Zustand store) so they never schedule
// a re-render. The 1Hz sampler picks the latest values up and patches
// the relevant DOM nodes in place. Only real status transitions
// (connect / disconnect / error / transport switch) touch the store.

listen("veil-event", (msg) => {
  const e = msg.payload || {};
  if (e.transport) liveStats.transport = e.transport;
  if (e.remote)    liveStats.remote    = e.remote;
  if (typeof e.bytes_tx === "number") liveStats.bytesTx = e.bytes_tx;
  if (typeof e.bytes_rx === "number") liveStats.bytesRx = e.bytes_rx;
  if (e.message)   liveStats.message   = e.message;
  switch (e.type) {
    case 1:
      set({ status: "connected", startedAt: Date.now() });
      pushLog(`connected via ${e.transport} → ${e.remote}`);
      break;
    case 2:
      set((s) => ({ status: s.status === "connecting" ? "error" : "stopped", startedAt: 0 }));
      pushLog("disconnected");
      break;
    case 3:
      set({ status: "error", startedAt: 0 });
      pushLog("error: " + (e.message || "?"));
      // Auto-disconnect on fatal errors. In TUN mode the Rust side
      // already tears routes down, but call disconnect() anyway so
      // the JS state slot doesn't leak a half-dead Veil instance,
      // and SOCKS5 mode (which Rust doesn't auto-clean) gets reset.
      toast(e.message || "Connection failed", "error");
      disconnect();
      break;
    case 4: {
      // Traffic event — drives ALL throughput rendering. Fires every
      // 250 ms from the Rust metrics poller, so latency from byte to
      // pixel is bounded by that period (was up to 1 s when sampling
      // ran on its own interval).
      const now = performance.now();
      const dtMs = now - throughput.lastTs;
      // First event after connect just primes the baseline; we can't
      // compute a meaningful rate without a previous timestamp.
      if (throughput.lastTs > 0 && dtMs >= 50) {
        const dt = dtMs / 1000;
        const dn = Math.max(0, (liveStats.bytesRx - throughput.lastRx) / dt);
        const up = Math.max(0, (liveStats.bytesTx - throughput.lastTx) / dt);
        // Adaptive scale: peak of last buffer becomes the ceiling so
        // a steady 4 MB/s link doesn't render as a tiny smear at the
        // bottom of a 100 MB/s axis.
        const peak = Math.max(0.5 * 1024 * 1024, ...throughput.down, ...throughput.up, dn, up);
        const norm = (n) => Math.min(1, n / peak);
        throughput.down.push(norm(dn));
        throughput.up.push(norm(up));
        if (throughput.down.length > 60) throughput.down.shift();
        if (throughput.up.length   > 60) throughput.up.shift();
        // Also stash the raw rate for the header readout.
        throughput.lastRateRx = dn;
        throughput.lastRateTx = up;
        if (state.view === "home") {
          updateChart();
          const dnEl = document.getElementById("thru-dn");
          const upEl = document.getElementById("thru-up");
          if (dnEl) dnEl.textContent = mbps(dn);
          if (upEl) upEl.textContent = mbps(up);
        }
      }
      throughput.lastTs = now;
      throughput.lastRx = liveStats.bytesRx;
      throughput.lastTx = liveStats.bytesTx;
      if (state.view === "home") {
        const sd = document.getElementById("stat-down");
        const su = document.getElementById("stat-up");
        if (sd) sd.textContent = fmtBytes(liveStats.bytesRx);
        if (su) su.textContent = fmtBytes(liveStats.bytesTx);
      }
      break;
    }
    case 5: pushLog(`transport switch: ${e.transport}`); break;
  }
});

listen("tray-action", (msg) => {
  if (msg.payload === "connect") connect();
  else if (msg.payload === "disconnect") disconnect();
});

// TUN bring-up progress — emitted from Rust as the worker walks
// through gateway probe → adapter create → routes → DNS. Patches
// the connecting sub-line in place; never schedules a re-render.
listen("tun-progress", (msg) => {
  liveStats.tunProgress = msg.payload || null;
  pushLog(`tun: ${msg.payload?.label || "?"}`);
  // If the home view is mounted, patch the sub-line + stage marker
  // directly. We deliberately skip a full render — the orb, chart,
  // and pills don't need to change.
  const sub  = document.getElementById("status-sub");
  const step = document.getElementById("status-step");
  if (sub && state.status === "connecting" && msg.payload) {
    sub.textContent = msg.payload.label + "…";
  }
  if (step && state.status === "connecting" && msg.payload) {
    step.textContent = `${msg.payload.step}/${msg.payload.total}`;
    step.style.display = "inline-block";
  }
});

// ─── modal ─────────────────────────────────────────────────────

function openModal(opts) {
  state.modal = { ...opts, _values: {} };
  for (const f of opts.fields || []) state.modal._values[f.key] = f.initial ?? "";
  render();
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
        if (ev.key === "Escape") closeModal();
        if (ev.key === "Enter" && !f.multiline && !ev.shiftKey) { ev.preventDefault(); submit(); }
      },
    };
    const input = f.multiline ? el("textarea", props) : el("input", { type: "text", ...props });
    input.value = m._values[f.key] || "";
    return el("div", { class: "modal-field" },
      el("label", { class: "fieldlabel" }, f.label),
      input,
    );
  });
  const submit = async () => {
    try {
      const r = m.onSubmit(m._values);
      if (r && typeof r.then === "function") await r;
    } catch (e) { toast(String(e?.message || e), "error"); return; }
    if (state.modal === m) closeModal();
  };
  // Cancel handler — fires onCancel if the caller registered one,
  // then dismisses the modal. Used today by the elevation prompt to
  // surface a "Use SOCKS5 instead" path; defaults to plain dismiss.
  const cancel = async () => {
    if (state.modal === m) closeModal();
    if (typeof m.onCancel === "function") {
      try {
        const r = m.onCancel();
        if (r && typeof r.then === "function") await r;
      } catch (e) { toast(String(e?.message || e), "error"); }
    }
  };
  // `bodyNode` is the rich variant; release-notes modals build a
  // <div> with paragraphs / headings / lists in renderUpdateNotes
  // and pass it here. Plain `body` strings still go through the
  // legacy text path so existing call-sites keep working.
  const bodyEl = m.bodyNode
    ? el("div", { class: "modal-body" }, m.bodyNode)
    : (m.body ? el("div", { class: "modal-body" }, m.body) : null);
  const card = el("div", { class: "modal-card" },
    el("div", { class: "modal-title" }, m.title),
    bodyEl,
    ...fields,
    el("div", { class: "modal-actions" },
      el("button", { class: "subtle", onclick: cancel }, m.cancelLabel || "Cancel"),
      el("button", { class: m.danger ? "danger" : "primary", onclick: submit }, m.submitLabel || "OK"),
    ),
  );
  // Backdrop / Escape dismiss → plain close, NOT onCancel. Background
  // click is usually accidental; we don't want it to silently switch
  // modes. Users have to make the choice via the explicit button.
  return el("div", { class: "modal-backdrop", onclick: (ev) => { if (ev.target === ev.currentTarget) closeModal(); } }, card);
}

function toast(text, kind = "info") {
  state.toast = { text, kind };
  render();
  setTimeout(() => {
    if (state.toast?.text === text) { state.toast = null; render(); }
  }, 4000);
}
function toastEl(t) { return el("div", { class: "toast " + t.kind }, t.text); }

async function copyToClipboard(text) {
  try { await navigator.clipboard.writeText(text); toast("Copied.", "success"); }
  catch (e) { toast("Copy failed", "error"); }
}

// ─── onboarding (first-run paste veil://) ─────────────────────

function renderOnboarding(host) {
  const ta = el("textarea", {
    class: "ob-paste",
    placeholder: "veil://...",
    spellcheck: "false",
  });

  const submit = async () => {
    const text = ta.value.trim();
    if (!text) { toast("Paste a veil:// link first.", "error"); return; }
    const id = String(Date.now());
    const name = extractHostLine(text) || "Server 1";
    set((s) => ({
      profiles: [...s.profiles, { id, name, config: text }],
      activeId: id,
      configEditor: text,
      view: "home",
    }));
    await persistProfiles();
    toast(`Added "${name}". Tap the orb to connect.`, "success");
  };

  const card = el("div", { class: "ob-card" },
    el("div", { class: "ob-mark" }, veilMarkSVG(44)),
    el("div", { class: "ob-title" }, "Welcome to Veil"),
    el("div", { class: "ob-sub" },
      "Veil routes your traffic through a server ",
      el("em", null, "you"), " control. Paste a ",
      el("span", { class: "ob-scheme" }, "veil://"),
      " share link your operator gave you to get started.",
    ),
    el("div", { class: "ob-form" },
      ta,
      el("button", { class: "primary ob-cta", onclick: submit }, "Connect"),
      el("div", { class: "ob-hint" },
        "…or ",
        el("span", { class: "ob-link", onclick: () => toast("QR import coming in alpha.2.", "info") }, "scan a QR code"),
        " · ",
        el("span", { class: "ob-link", onclick: () => toast("Use the Veil Installer app to deploy a server.", "info") }, "set up your own server"),
      ),
    ),
  );
  const foot = el("div", { class: "ob-foot" },
    el("span", null, "Pre-alpha · No external audit yet"),
    el("span", { "data-app-version": "1", style: "font-family: var(--font-mono)" }, _appVersion || "…"),
  );

  host.append(el("div", { class: "ob-pad" }, card), foot);
}

// ─── update screen (full-bleed dedicated) ──────────────────────

function renderUpdate(host) {
  const u = state.update || { current: "?", latest: "?", update_available: false };
  const head = el("div", { class: "upd-head" },
    el("div", { class: "upd-icon" }, glyph("download", 20)),
    el("div", null,
      el("div", { class: "upd-title" }, "Update available"),
      el("div", { class: "upd-versions" }, `${u.current} → ${u.latest}`),
    ),
  );
  const notes = el("div", { class: "upd-notes" },
    el("div", { class: "upd-notes-h" }, "What's new"),
    el("ul", null,
      el("li", null, "Bug fixes and stability improvements"),
      el("li", null, "Updated transport selection logic"),
      el("li", null, "Cosign signature verification on auto-update"),
    ),
  );
  const verified = el("div", { class: "upd-verified" },
    el("span", { style: "color: var(--ok)" }, glyph("check", 13)),
    "Cosign signature verified",
  );
  const actions = el("div", { class: "upd-actions" },
    el("button", { class: "subtle", onclick: () => { state.view = "settings"; render(); } }, "Later"),
    el("button", { class: "primary", onclick: doApplyUpdate, disabled: !u.update_available }, "Restart & install"),
  );
  host.append(el("div", { class: "upd-pad" }, head, notes, verified, el("div", { style: "flex:1" }), actions));
}

// ─── primitives: orb, pill, chart, glyphs, mark ────────────────

function statusOrb(status, onclick) {
  const cls = "orb " + (status || "idle");
  const wrap = el("button", { class: cls, onclick, "aria-label": "Toggle connection",
    style: "width:140px; height:140px;" },
    el("div", { class: "orb-ring r0", style: "width:140px; height:140px;" }),
    el("div", { class: "orb-ring r1", style: "width:140px; height:140px;" }),
    el("div", { class: "orb-ring r2", style: "width:140px; height:140px;" }),
    el("div", { class: "orb-glow" }),
  );
  const disc = document.createElement("span");
  disc.className = "orb-disc";
  disc.style.width = "108px";
  disc.style.height = "108px";
  disc.innerHTML = `
    <svg class="orb-power" width="32" height="32" viewBox="0 0 24 24" fill="none">
      <path d="M12 3 L12 12" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
      <path d="M6.5 7 A 7 7 0 1 0 17.5 7" stroke="currentColor" stroke-width="2" stroke-linecap="round" fill="none"/>
    </svg>`;
  wrap.append(disc);
  return wrap;
}

function transportPill(label, active = false, compact = false) {
  return el("span", { class: "pill" + (active ? " active" : "") + (compact ? " compact" : "") },
    el("span", { class: "pill-dot" }),
    label,
  );
}

// Chart geometry constants — shared between buildChart() and the
// in-place updater. The SVG element is created once per home-view
// mount; subsequent samples patch the four <path d> attributes
// instead of swapping the DOM node, which is what made the chart
// look "torn".
const CHART_W = 600, CHART_H = 88, CHART_N = 60;

function chartPx(i) { return (i / (CHART_N - 1)) * CHART_W; }
function chartPy(v) { return CHART_H - v * CHART_H * 0.92 - 4; }
function chartPathArea(data) {
  let d = `M 0 ${CHART_H}`;
  for (let i = 0; i < data.length; i++) d += ` L ${chartPx(i)} ${chartPy(data[i])}`;
  d += ` L ${CHART_W} ${CHART_H} Z`;
  return d;
}
function chartPathLine(data) {
  let d = "";
  for (let i = 0; i < data.length; i++) d += `${i === 0 ? "M" : "L"} ${chartPx(i)} ${chartPy(data[i])} `;
  return d;
}
function chartSeries() {
  const dn = throughput.down.slice(-CHART_N);
  const up = throughput.up.slice(-CHART_N);
  while (dn.length < CHART_N) dn.unshift(0);
  while (up.length < CHART_N) up.unshift(0);
  return { dn, up };
}

function buildChart() {
  const { dn, up } = chartSeries();
  const wrap = document.createElement("div");
  wrap.id = "thru-chart";
  wrap.style.padding = "0 14px 12px";
  wrap.innerHTML = `
    <svg viewBox="0 0 ${CHART_W} ${CHART_H}" width="100%" height="${CHART_H}" preserveAspectRatio="none" style="display:block">
      <defs>
        <linearGradient id="vg-down" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="#7C5CFF" stop-opacity="0.45"/>
          <stop offset="100%" stop-color="#7C5CFF" stop-opacity="0"/>
        </linearGradient>
        <linearGradient id="vg-up" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="#7C5CFF" stop-opacity="0.18"/>
          <stop offset="100%" stop-color="#7C5CFF" stop-opacity="0"/>
        </linearGradient>
      </defs>
      <line x1="0" x2="${CHART_W}" y1="${CHART_H * 0.25}" y2="${CHART_H * 0.25}" stroke="rgba(255,255,255,0.07)" stroke-width="0.5" stroke-dasharray="2 4"/>
      <line x1="0" x2="${CHART_W}" y1="${CHART_H * 0.5}"  y2="${CHART_H * 0.5}"  stroke="rgba(255,255,255,0.07)" stroke-width="0.5" stroke-dasharray="2 4"/>
      <line x1="0" x2="${CHART_W}" y1="${CHART_H * 0.75}" y2="${CHART_H * 0.75}" stroke="rgba(255,255,255,0.07)" stroke-width="0.5" stroke-dasharray="2 4"/>
      <path id="thru-area-d" d="${chartPathArea(dn)}" fill="url(#vg-down)"/>
      <path id="thru-line-d" d="${chartPathLine(dn)}" fill="none" stroke="#7C5CFF" stroke-width="1.4"/>
      <path id="thru-area-u" d="${chartPathArea(up)}" fill="url(#vg-up)"/>
      <path id="thru-line-u" d="${chartPathLine(up)}" fill="none" stroke="#7C5CFF" stroke-width="1" stroke-opacity="0.5" stroke-dasharray="3 2"/>
    </svg>`;
  return wrap;
}

// Patch the four <path d> attributes in place — no DOM swap.
function updateChart() {
  const ad = document.getElementById("thru-area-d");
  if (!ad) return; // chart isn't mounted (different view)
  const { dn, up } = chartSeries();
  ad.setAttribute("d", chartPathArea(dn));
  document.getElementById("thru-line-d").setAttribute("d", chartPathLine(dn));
  document.getElementById("thru-area-u").setAttribute("d", chartPathArea(up));
  document.getElementById("thru-line-u").setAttribute("d", chartPathLine(up));
}

function veilMarkSVG(size = 22) {
  const wrap = document.createElement("span");
  wrap.style.display = "inline-flex";
  wrap.innerHTML = `
    <svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none">
      <path d="M3 4 L12 21 L21 4" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>
      <path d="M6 4 L12 16.5 L18 4" stroke="#7C5CFF" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" opacity="0.85"/>
      <path d="M9 4 L12 12 L15 4" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" opacity="0.4"/>
    </svg>`;
  return wrap;
}

const GLYPHS = {
  shield:   `<path d="M12 3 L4 6 V12 C4 16.5 7.5 20 12 21 C16.5 20 20 16.5 20 12 V6 Z"/>`,
  list:     `<path d="M8 6 H 21 M8 12 H 21 M8 18 H 21 M3.5 6 H 3.6 M3.5 12 H 3.6 M3.5 18 H 3.6"/>`,
  terminal: `<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 9 L 10 12 L 7 15 M12 15 H 16"/>`,
  settings: `<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 0 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 0 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 0 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 0 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 0 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 0 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 0 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 0 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/>`,
  plus:     `<path d="M12 5 V 19 M5 12 H 19"/>`,
  copy:     `<rect x="8" y="8" width="13" height="13" rx="2"/><path d="M16 8 V 5 A 2 2 0 0 0 14 3 H 5 A 2 2 0 0 0 3 5 V 14 A 2 2 0 0 0 5 16 H 8"/>`,
  edit:     `<path d="M14 4 L 20 10 L 8 22 H 2 V 16 Z M 12 6 L 18 12"/>`,
  trash:    `<path d="M3 6 H 21 M8 6 V 4 A 1 1 0 0 1 9 3 H 15 A 1 1 0 0 1 16 4 V 6 M5 6 L 6 21 H 18 L 19 6"/>`,
  server:   `<rect x="3" y="4" width="18" height="7" rx="1.5"/><rect x="3" y="13" width="18" height="7" rx="1.5"/><circle cx="7" cy="7.5" r="0.6" fill="currentColor"/><circle cx="7" cy="16.5" r="0.6" fill="currentColor"/>`,
  download: `<path d="M12 4 V 16 M5 10 L 12 16 L 19 10 M4 20 H 20"/>`,
  check:    `<path d="M5 12 L 10 17 L 19 7"/>`,
};

function glyph(kind, size = 16) {
  const wrap = document.createElement("span");
  wrap.style.display = "inline-flex";
  wrap.innerHTML = `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">${GLYPHS[kind] || ""}</svg>`;
  return wrap;
}

function toggle(on, onchange) {
  return el("button", { class: "toggle" + (on ? " on" : ""), onclick: () => onchange(!on) });
}
function segmented(options, value, onchange) {
  const wrap = el("div", { class: "seg" });
  for (const o of options) {
    wrap.append(el("button", {
      class: "seg-item" + (o === value ? " on" : ""),
      onclick: () => { onchange(o); render(); },
    }, o));
  }
  return wrap;
}

// ─── extractors ────────────────────────────────────────────────

function extractHostLine(cfg) {
  if (!cfg) return "";
  if (cfg.startsWith("veil://")) {
    try {
      const obj = decodeShareLink(cfg);
      return obj.Servers?.[0]?.Addr || "veil:// share link";
    } catch { return "veil:// share link"; }
  }
  const m = cfg.match(/addr:\s*([^\s\n]+)/);
  return m ? m[1].replace(/['"]/g, "") : "YAML config";
}
function extractTransportTypes(cfg) {
  if (!cfg) return [];
  if (cfg.startsWith("veil://")) {
    try { return (decodeShareLink(cfg).Servers || []).map((s) => s.Type); }
    catch { return ["?"]; }
  }
  return [...cfg.matchAll(/type:\s*(\w+)/g)].map((m) => m[1]);
}
function extractServerHosts(cfg) {
  const out = new Set();
  if (cfg.startsWith("veil://")) {
    try { for (const s of decodeShareLink(cfg).Servers || []) if (s.Addr) out.add(s.Addr.split(":")[0]); }
    catch {}
  } else {
    for (const m of cfg.matchAll(/addr:\s*([^\s\n]+)/g)) out.add(m[1].split(":")[0].replace(/['"]/g, ""));
  }
  return [...out];
}
function decodeShareLink(link) {
  const b64 = link.slice("veil://".length).replace(/-/g, "+").replace(/_/g, "/");
  const padded = b64 + "==".slice((b64.length % 4) ? -(4 - (b64.length % 4)) : 0);
  return JSON.parse(atob(padded));
}

// ─── formatters ────────────────────────────────────────────────

function fmtBytes(n) {
  if (!n) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0, v = n;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(i ? 1 : 0)} ${u[i]}`;
}
function mbps(bytes) { return (bytes / (1024 * 1024)).toFixed(2); }
function fmtUptime(s) {
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), r = s % 60;
  const pad = (n) => String(n).padStart(2, "0");
  return `${pad(h)}:${pad(m)}:${pad(r)}`;
}
function elapsed() { return state.startedAt ? Math.floor((Date.now() - state.startedAt) / 1000) : 0; }

function pushLog(line) {
  const ts = new Date().toLocaleTimeString();
  set((s) => ({ log: [...s.log, `${ts} ${line}`].slice(-200) }));
}

// ─── DOM helper ────────────────────────────────────────────────

function el(tag, props, ...children) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
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

// ─── boot ──────────────────────────────────────────────────────

bootStore();

// Auto-check for updates 8 s after launch — past the moment the user
// typically clicks Connect, but soon enough that the prompt lands
// during their first session rather than several restarts later.
setTimeout(() => { maybeAutoCheckUpdate(); }, 8000);
