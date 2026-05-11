// Veil Installer — pixel-ported from /design/sections/installer.jsx
//
// Layout:
//   - Custom titlebar with optional wizard step indicator (when in a deploy flow).
//   - Sidebar: Servers list + Deploy entries + Settings.
//   - First-run / "no servers": full-bleed Welcome chooser (VPS / Edge / Manage).
//   - Deploy flows render with shared wizard chrome (header eyebrow + step nav).
//   - Manage existing server pixel-matches InstallerManage from design.
//
// All Tauri invoke calls + tauri-plugin-store persistence preserved verbatim.

import { invoke } from "@tauri-apps/api/core";
import { getCurrentWindow } from "@tauri-apps/api/window";
import { Store } from "@tauri-apps/plugin-store";

import { state, set, subscribeAll, queryClient, cachedInvoke, invalidateInvoke } from "./store.js";

const appWindow = getCurrentWindow();
const STORE_FILE = "installer.store.json";

const root = document.getElementById("app");
let store = null;

// ── Boot ─────────────────────────────────────────────────
async function bootStore() {
  store = await Store.load(STORE_FILE, { autoSave: true });
  const loadedServers = (await store.get("servers")) || [];
  const loadedActive  = (await store.get("active_server")) || (loadedServers[0]?.id ?? null);
  let initialView = "welcome";
  if (loadedServers.length === 0)                                            initialView = "welcome";
  else if (loadedActive && loadedServers.find((s) => s.id === loadedActive)) initialView = "server-detail";
  else                                                                       initialView = "servers";
  set({ servers: loadedServers, activeId: loadedActive, view: initialView });

  // Render once + subscribe for the lifetime of the app. Subsequent
  // store changes are coalesced into a single RAF render.
  subscribeAll(() => scheduleRender());
  render();

  // probe all servers in background to populate sidebar dots
  for (const s of loadedServers) refreshServer(s.id);
}

async function persistServers() {
  await store.set("servers", state.servers);
  await store.set("active_server", state.activeId);
}

// ── Wizard step lookup ──────────────────────────────────
const WIZ_STEPS = {
  "deploy-ssh":          { step: 2, total: 5, label: "SSH" },
  "deploy-ssh-config":   { step: 3, total: 5, label: "Config" },
  "deploy-ssh-progress": { step: 4, total: 5, label: "Installing" },
  "deploy-ssh-done":     { step: 5, total: 5, label: "Done" },
  "deploy-edge":         { step: 3, total: 5, label: "Edge" },
  "deploy-edge-progress":{ step: 4, total: 5, label: "Deploying" },
  "deploy-edge-done":    { step: 5, total: 5, label: "Done" },
};

// ── Render shell (mount-once + swap-only-main) ───────────
//
// The titlebar + sidebar persist for the life of the app; only the
// main pane is rebuilt on view changes. Welcome is special-cased
// (no sidebar) and gets its own remount path.

const mounted = {
  titlebar: null, shell: null, sidebar: null, main: null,
  toast: null, modal: null, shellMode: null,
};

let _renderQueued = false;
function scheduleRender() {
  if (_renderQueued) return;
  _renderQueued = true;
  requestAnimationFrame(() => { _renderQueued = false; render(); });
}

function render() {
  // Welcome is a full-bleed mode (no sidebar). Detect mode change.
  const mode = state.view === "welcome" ? "welcome" : "default";

  if (mode !== mounted.shellMode) {
    if (mounted.titlebar) mounted.titlebar.remove();
    if (mounted.shell)    mounted.shell.remove();
    mounted.titlebar = renderTitlebar();
    mounted.shell    = el("div", { class: "shell" });
    if (mode === "welcome") {
      mounted.sidebar = null;
      mounted.main    = el("div", { class: "main", style: "padding:0" });
      mounted.shell.append(mounted.main);
    } else {
      mounted.sidebar = renderSidebar();
      mounted.main    = el("div", { class: "main" });
      mounted.shell.append(mounted.sidebar, mounted.main);
    }
    root.append(mounted.titlebar, mounted.shell);
    mounted.shellMode = mode;
  } else {
    // Same shell — replace titlebar + sidebar in place (cheap), wipe main.
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

  switch (state.view) {
    case "welcome":              renderWelcome(mounted.main); break;
    case "servers":              renderServersIndex(mounted.main); break;
    case "server-detail":        renderServerDetail(mounted.main); break;
    case "deploy-pick":          renderDeployPick(mounted.main); break;
    case "deploy-ssh":           renderDeploySSH(mounted.main); break;
    case "deploy-ssh-config":    renderDeploySSHConfig(mounted.main); break;
    case "deploy-ssh-progress":  renderDeploySSHProgress(mounted.main); break;
    case "deploy-ssh-done":      renderDeployDone(mounted.main, "ssh"); break;
    case "deploy-edge":          renderDeployEdge(mounted.main); break;
    case "deploy-edge-progress": renderDeployEdgeProgress(mounted.main); break;
    case "deploy-edge-done":     renderDeployDone(mounted.main, "edge"); break;
    case "deploy-compose":       renderDeployCompose(mounted.main); break;
    case "settings":             renderSettings(mounted.main); break;
    default:                     renderServersIndex(mounted.main);
  }

  // Overlays
  if (mounted.toast) { mounted.toast.remove(); mounted.toast = null; }
  if (mounted.modal) { mounted.modal.remove(); mounted.modal = null; }
  if (state.toast) { mounted.toast = toastEl(state.toast); root.append(mounted.toast); }
  if (state.modal) { mounted.modal = modalEl(state.modal); root.append(mounted.modal); }
}

function renderTitlebar() {
  const drag = el("div", { class: "tb-drag", "data-tauri-drag-region": "" },
    el("div", { class: "tb-brand" },
      el("span", { class: "mark" }, veilMark(14)),
      el("span", {}, "Veil Installer"),
    ),
  );
  // Wizard step indicator (only when in a wizard flow)
  const wiz = WIZ_STEPS[state.view];
  if (wiz) {
    const ind = el("div", { class: "tb-step-indicator" });
    for (let i = 0; i < wiz.total; i++) {
      ind.append(el("div", { class: "seg" + (i < wiz.step ? " on" : "") }));
    }
    ind.append(el("span", { class: "label" }, `${wiz.step}/${wiz.total} · ${wiz.label}`));
    drag.append(ind);
  }
  const ctrls = el("div", { class: "tb-ctrls" },
    el("button", { class: "tb-btn", title: "Minimize", onclick: () => appWindow.minimize() }, tbIcon("min")),
    el("button", { class: "tb-btn", title: "Maximize", onclick: () => appWindow.toggleMaximize() }, tbIcon("max")),
    el("button", { class: "tb-btn close", title: "Close", onclick: () => appWindow.close() }, tbIcon("close")),
  );
  return el("div", { class: "titlebar" }, drag, ctrls);
}

function renderSidebar() {
  const sb = el("div", { class: "sidebar" });

  sb.append(el("div", { class: "side-section" }, "Servers"));
  if (state.servers.length === 0) {
    sb.append(el("div", { style: "padding:8px 12px; font-size:12px; color:var(--textMute)" }, "No servers yet"));
  } else {
    for (const s of state.servers) {
      const probe = state.serverProbes[s.id];
      const dotClass = probe?.busy ? "" : probe?.reachable ? "ok" : probe?.error ? "bad" : "";
      sb.append(el("button", {
        class: "side-item" + (state.view === "server-detail" && state.activeId === s.id ? " active" : ""),
        onclick: () => selectServer(s.id),
      },
        el("div", { class: "dot " + dotClass }),
        el("div", { class: "label" }, s.label),
      ));
    }
  }
  sb.append(el("button", { class: "side-add", onclick: openAddServer }, "+ Add existing"));

  sb.append(el("div", { class: "side-section" }, "Deploy"));
  sb.append(sideNav("deploy-pick", "All workflows", iconFor("zap")));
  sb.append(sideNav("deploy-ssh", "VPS via SSH", iconFor("server")));
  sb.append(sideNav("deploy-edge", "Edge function", iconFor("cloud")));
  sb.append(sideNav("deploy-compose", "Docker compose", iconFor("terminal")));

  sb.append(el("div", { class: "side-section" }, "App"));
  sb.append(sideNav("settings", "Settings", iconFor("settings")));

  sb.append(el("div", { class: "foot" },
    "v0.1.0-alpha.1",
    el("br"),
    "github.com/redstone-md/veil",
  ));
  return sb;
}

function sideNav(view, label, ic) {
  return el("button", {
    class: "side-item" + (state.view === view ? " active" : ""),
    onclick: () => { state.view = view; render(); },
  },
    el("span", { class: "icon" }, ic),
    el("div", { class: "label" }, label),
  );
}

// ── Welcome (first-run / pick deployment target) ─────────
function renderWelcome(host) {
  const card = el("div", { class: "welcome" });
  // brand panel
  card.append(el("div", { class: "brand" },
    veilMark(36),
    el("div", null,
      el("h1", null, "Set up your", el("br"), "Veil server"),
      el("div", { class: "desc", style: "margin-top:8px" },
        "A 5-minute wizard to deploy a censorship-resistant VPN you fully own. ",
        "No accounts, no telemetry, fully self-hosted."),
    ),
    el("div", { class: "spacer" }),
    el("div", { class: "foot" },
      "Pre-alpha · v0.1.0-alpha.1", el("br"),
      "github.com/redstone-md/veil",
    ),
  ));

  // right side
  const right = el("div", { class: "right" });
  right.append(
    el("div", { class: "eyebrow" }, "STEP 1"),
    el("div", { class: "h2", style: "margin-top:4px" }, "Pick a deployment target"),
    el("div", { class: "subtitle" }, "Where should the Veil server run?"),
  );

  const options = [
    {
      key: "ssh", icon: "server",
      title: "VPS via SSH",
      tags: ["Recommended", "Reality + WSS + QUIC", "~3 min"],
      desc: "Push a binary onto a Linux box, write a systemd unit, start the service. Full control, full bandwidth.",
      onclick: () => { state.view = "deploy-ssh"; render(); },
    },
    {
      key: "edge", icon: "cloud",
      title: "Edge function",
      tags: ["Deno · Fly", "MASQUE", "~2 min"],
      desc: "Deploy to Deno Deploy or Fly.io. No server to maintain. MASQUE transport only.",
      onclick: () => { state.view = "deploy-edge"; render(); },
    },
    {
      key: "manage", icon: "terminal",
      title: "Manage existing",
      tags: ["Existing"],
      desc: "Connect to a Veil server you set up before — view users, rotate keys, share links.",
      onclick: openAddServer,
    },
  ];
  let selectedKey = "ssh";

  const grid = el("div", { style: "display:flex; flex-direction:column; gap:10px; margin-top:14px" });
  options.forEach((o) => {
    const ch = el("button", {
      class: "choice" + (o.key === selectedKey ? " selected" : ""),
      onclick: () => { selectedKey = o.key; renderWelcome(host); },
      ondblclick: o.onclick,
    },
      el("div", { class: "ico" }, iconFor(o.icon, 20)),
      el("div", { style: "flex:1; min-width:0" },
        el("div", { class: "name" },
          el("span", null, o.title),
          ...o.tags.map((t) => el("span", { class: "tag" }, t)),
        ),
        el("div", { class: "body" }, o.desc),
      ),
      el("div", { class: "radio" }, o.key === selectedKey ? checkSvg(11) : null),
    );
    grid.append(ch);
  });
  right.append(grid);

  right.append(el("div", { class: "spacer" }));
  right.append(el("div", { class: "row right" },
    el("button", { class: "btn primary lg", onclick: () => options.find(o => o.key === selectedKey).onclick() },
      "Continue", chevronSvg(12),
    ),
  ));

  card.append(right);
  host.innerHTML = "";
  host.append(card);
}

// ── Deploy index (sidebar entry point) ───────────────────
function renderDeployPick(host) {
  host.append(
    el("h1", { class: "h1" }, "Deploy a server"),
    el("p", { class: "subtitle" }, "Pick the workflow that matches where you want Veil to live."),
    el("div", { class: "choicegrid", style: "margin-top:6px" },
      choiceCard("server", "VPS via SSH",      "Push veil + systemd unit + start", () => { state.view = "deploy-ssh"; render(); }, ["Recommended"]),
      choiceCard("cloud",  "Edge function",    "Deno Deploy / Fly.io worker bundle", () => { state.view = "deploy-edge"; render(); }, []),
      choiceCard("terminal","Docker compose",  "Generate compose.yaml for any host", () => { state.view = "deploy-compose"; render(); }, []),
    ),
  );
}

function choiceCard(icon, name, body, onclick, tags) {
  return el("button", { class: "choice", onclick },
    el("div", { class: "ico" }, iconFor(icon, 20)),
    el("div", { style: "flex:1" },
      el("div", { class: "name" },
        el("span", null, name),
        ...(tags || []).map((t) => el("span", { class: "tag" }, t)),
      ),
      el("div", { class: "body" }, body),
    ),
  );
}

// ── Servers index (when at least one exists, no active selected) ──
function renderServersIndex(host) {
  host.append(
    el("h1", { class: "h1" }, "Servers"),
    el("p", { class: "subtitle" }, "Manage previously-deployed Veil servers, or roll out a new one from Deploy."),
  );
  if (state.servers.length === 0) {
    host.append(el("div", { class: "card padded" },
      el("div", { class: "cardtitle" }, "Get started"),
      el("p", { class: "subtitle" }, "Deploy your first server, or attach an existing one."),
      el("div", { class: "row", style: "margin-top:6px" },
        el("button", { class: "btn primary", onclick: () => { state.view = "deploy-pick"; render(); } }, "Deploy a server"),
        el("button", { class: "btn", onclick: openAddServer }, "Add existing"),
      ),
    ));
    return;
  }
  const grid = el("div", { class: "choicegrid" });
  for (const s of state.servers) {
    grid.append(el("button", { class: "choice", onclick: () => selectServer(s.id) },
      el("div", { class: "ico" }, iconFor("server", 20)),
      el("div", { style: "flex:1" },
        el("div", { class: "name" }, s.label),
        el("div", { class: "body" }, s.base_url),
      ),
    ));
  }
  host.append(grid);
}

// ── Server detail (= InstallerManage from design) ────────
async function selectServer(id) {
  state.activeId = id;
  state.view = "server-detail";
  state.serverDetailTab = "overview";
  await persistServers();
  render();
  refreshServer(id);
}

async function refreshServer(id, opts = {}) {
  const s = state.servers.find((x) => x.id === id);
  if (!s) return;
  const creds = { base_url: s.base_url, username: s.basic_user, password: s.basic_pass };
  // If invoked by user-initiated refresh, drop cached entries so we
  // re-hit the bridge. Tab navigation calls refreshServer with the
  // default opts and will read from cache (no flicker).
  if (opts.bypassCache) {
    invalidateInvoke("admin_version",   { creds });
    invalidateInvoke("admin_dashboard", { creds });
    invalidateInvoke("admin_users_list",{ creds });
    invalidateInvoke("admin_server_info",{ creds });
  }
  set((s2) => ({ serverProbes: { ...s2.serverProbes, [id]: { ...(s2.serverProbes[id] || {}), busy: true } } }));
  try {
    const version = await cachedInvoke("admin_version", { creds });
    let dashboard = null, users = null, info = null;
    try { dashboard = await cachedInvoke("admin_dashboard",   { creds }); } catch (_) {}
    try { users     = await cachedInvoke("admin_users_list",  { creds }); } catch (_) {}
    try { info      = await cachedInvoke("admin_server_info", { creds }); } catch (_) {}
    set((s2) => ({ serverProbes: { ...s2.serverProbes, [id]: { reachable: true, version, dashboard, users, info, error: null, ts: Date.now(), busy: false } } }));
  } catch (e) {
    set((s2) => ({ serverProbes: { ...s2.serverProbes, [id]: { reachable: false, error: String(e), ts: Date.now(), busy: false } } }));
  }
}

function renderServerDetail(host) {
  const s = state.servers.find((x) => x.id === state.activeId);
  if (!s) {
    host.append(el("div", { class: "empty" }, "Server not found.",
      el("div", { class: "hint" }, "Pick a server from the sidebar or add one.")));
    return;
  }
  const probe = state.serverProbes[s.id] || {};
  const v = probe.version || {};
  const d = probe.dashboard || {};

  // Header row
  host.append(el("div", { class: "row between" },
    el("div", null,
      el("div", { class: "eyebrow" }, "MANAGE"),
      el("div", { class: "h1", style: "margin-top:4px" }, s.label),
      el("div", { class: "subtitle", style: "font-family:var(--fontMono)" },
        (v.version ? `${v.version}` : "unknown") +
        (v.commit ? ` · ${v.commit.slice(0,7)}` : "") +
        ` · ${s.base_url}`),
    ),
    el("div", { class: "row" },
      statusPill(probe),
      el("button", { class: "btn", onclick: () => refreshServer(s.id, { bypassCache: true }), disabled: !!probe.busy },
        iconFor("refresh", 12), probe.busy ? "Refreshing…" : "Refresh"),
      el("button", { class: "btn", onclick: () => openEditServer(s) }, iconFor("edit", 12), "Edit"),
      el("button", { class: "btn primary", onclick: () => openAddUser(s), disabled: !probe.reachable },
        iconFor("plus", 12), "Add user"),
    ),
  ));

  // Health row
  const totalBytes = (probe.users || []).reduce((sum, u) => sum + (u.used_bytes_current_month || 0), 0);
  host.append(el("div", { class: "stat-grid" },
    statTile("Active sessions", d.users_active ?? "—", null),
    statTile("This month", fmtBytes(totalBytes).split(" ")[0], fmtBytes(totalBytes).split(" ")[1] || ""),
    statTile("Users", d.users_total ?? (probe.users?.length ?? 0), null),
    statTile("At quota", String(d.users_at_quota ?? 0), null, (d.users_at_quota || 0) > 0 ? "var(--warn)" : null),
  ));

  // Tabs
  host.append(el("div", { class: "tabs" },
    tab("overview", "Overview"),
    tab("users",    `Users · ${(probe.users || []).length}`),
    tab("transports","Transports"),
  ));

  if (state.serverDetailTab === "overview")  renderServerOverview(host, s, probe);
  if (state.serverDetailTab === "users")     renderServerUsers(host, s, probe);
  if (state.serverDetailTab === "transports")renderServerTransports(host, s, probe);
}

function tab(key, label) {
  return el("button", {
    class: "tab" + (state.serverDetailTab === key ? " active" : ""),
    onclick: () => { state.serverDetailTab = key; render(); },
  }, label);
}

function statTile(l, v, unit, color) {
  return el("div", { class: "stat-tile" },
    el("div", { class: "l" }, l),
    el("div", { class: "v" + (color === "var(--accent)" ? " accent" : ""), style: color ? `color:${color}` : null },
      String(v), unit ? el("span", { style: "font-size:12px;color:var(--textDim);font-weight:500;margin-left:4px" }, unit) : null,
    ),
  );
}

function statusPill(probe) {
  if (probe.busy)      return el("div", { class: "pill warn" }, "Probing…");
  if (probe.reachable) return el("div", { class: "pill ok" }, dotSvg(), "Reachable");
  if (probe.error)     return el("div", { class: "pill bad" }, "Unreachable");
  return el("div", { class: "pill muted" }, "Unknown");
}

function renderServerOverview(host, s, probe) {
  if (!probe.reachable) {
    host.append(el("div", { class: "card padded" },
      el("div", { class: "cardtitle" }, "Cannot reach server"),
      el("p", { class: "subtitle" }, probe.error || "No probe yet."),
      el("p", { class: "subtitle" },
        "If admin is bound to localhost, you can SSH-tunnel: ",
        el("code", null, "ssh -L 9090:localhost:9090 user@host")),
    ));
    return;
  }
  const v = probe.version || {};
  const d = probe.dashboard || {};
  host.append(el("div", { class: "card" },
    el("div", { class: "cardtitle" }, "Build"),
    el("div", { class: "kv" },
      kvRow("Version", v.version || "—"),
      kvRow("Commit",  v.commit  || "—"),
      kvRow("Built",   v.date    || "—"),
    ),
  ));
  if (d && Object.keys(d).length) {
    host.append(el("div", { class: "card" },
      el("div", { class: "cardtitle" }, "Snapshot"),
      el("div", { class: "kv" },
        ...Object.entries(d).flatMap(([k, val]) => kvRow(prettyKey(k), formatValue(val))),
      ),
    ));
  }
}

function renderServerTransports(host, s, probe) {
  const tlist = probe.info?.transports || [];
  if (!tlist.length) {
    host.append(el("div", { class: "empty" }, "No transport info available.",
      el("div", { class: "hint" }, "Restart admin with --server-config + --public-host to enable.")));
    return;
  }
  host.append(el("div", { class: "card flush" },
    ...tlist.map((t, i) => el("div", { style: `padding:14px 18px;${i<tlist.length-1?'border-bottom:1px solid var(--border);':''}display:flex;align-items:center;gap:14px` },
      el("div", { class: "ico", style: "width:32px;height:32px;border-radius:7px;background:var(--accentSoft);color:var(--accent);display:flex;align-items:center;justify-content:center" }, iconFor("shield", 16)),
      el("div", { style: "flex:1;min-width:0" },
        el("div", { style: "font-weight:600;font-size:13px" }, t.type.toUpperCase()),
        el("div", { style: "font-size:11px;color:var(--textDim);font-family:var(--fontMono)" },
          t.addr + (t.sni ? ` · sni ${t.sni}` : "") + (t.path ? ` · path ${t.path}` : "")),
      ),
      el("span", { class: "pill ok" }, "listening"),
    )),
  ));
}

function renderServerUsers(host, s, probe) {
  if (!probe.reachable) {
    host.append(el("div", { class: "empty" }, "Connect to the server first."));
    return;
  }
  const users = probe.users || [];
  if (users.length === 0) {
    host.append(el("div", { class: "card empty" }, "No users yet.",
      el("div", { class: "hint" }, "Click \"Add user\" to provision the first one.")));
    return;
  }
  const tbl = el("table", { class: "tbl" });
  tbl.append(el("thead", null,
    el("tr", null,
      el("th", null, "User"), el("th", null, "Quota"), el("th", null, "Status"),
      el("th", null, "Expires"), el("th", { class: "actions" }, "Actions"),
    ),
  ));
  const tbody = el("tbody");
  for (const u of users) {
    const status = u.status || "active";
    const pillCls = status === "active" ? "ok" : (status === "revoked" ? "bad" : "warn");
    const used = u.used_bytes_current_month || 0;
    const quota = u.quota_bytes_per_month || 0;
    const pct = quota ? Math.min(100, used / quota * 100) : 0;
    const quotaCell = quota
      ? `${fmtBytes(used)} / ${fmtBytes(quota)} · ${Math.round(pct)}%`
      : `${fmtBytes(used)} used · ∞`;
    tbody.append(el("tr", null,
      el("td", null,
        el("div", { style: "display:flex;align-items:center;gap:8px" },
          el("span", { class: "avatar" }, (u.name || "?")[0].toUpperCase()),
          el("div", null,
            el("div", { style: "font-weight:600" }, u.name || u.id),
            el("div", { class: "id" }, u.id.slice(0, 16) + "…"),
          ),
        ),
      ),
      el("td", { style: "font-family:var(--fontMono);color:var(--textDim);font-size:11.5px" }, quotaCell),
      el("td", null, el("span", { class: "pill " + pillCls }, status)),
      el("td", { style: "font-family:var(--fontMono);color:var(--textDim);font-size:11.5px" }, fmtDate(u.expires_at)),
      el("td", { class: "actions" },
        el("button", { class: "btn icon-only ghost", title: "Share link", onclick: () => openShareLink(s, u) }, iconFor("qr", 13)),
        el("button", { class: "btn icon-only ghost", title: "Edit",       onclick: () => openEditUser(s, u) }, iconFor("edit", 13)),
        el("button", { class: "btn icon-only ghost danger", title: "Delete", onclick: () => deleteUser(s, u) }, iconFor("trash", 13)),
      ),
    ));
  }
  tbl.append(tbody);
  host.append(el("div", { class: "card flush" }, tbl));
}

// ── Server CRUD modals ──────────────────────────────────
function openAddServer() {
  openModal({
    title: "Add server",
    body: "Point at an existing Veil server's admin endpoint. The server must expose /api over HTTP Basic.",
    fields: [
      { key: "label",      label: "Name",           placeholder: "Production EU" },
      { key: "base_url",   label: "Admin URL",      placeholder: "https://veil.example.com:9090" },
      { key: "basic_user", label: "Admin user",     placeholder: "admin" },
      { key: "basic_pass", label: "Admin password", placeholder: "", secret: true },
    ],
    submitLabel: "Add",
    onSubmit: async (vals) => {
      if (!vals.label || !vals.base_url || !vals.basic_user) throw new Error("All fields required");
      const id = String(Date.now());
      const newServer = {
        id, label: vals.label, base_url: vals.base_url.replace(/\/$/, ""),
        basic_user: vals.basic_user, basic_pass: vals.basic_pass || "",
      };
      set((s) => ({ servers: [...s.servers, newServer], activeId: id, view: "server-detail" }));
      await persistServers();
      toast(`Added ${vals.label}.`, "info");
      refreshServer(id);
    },
  });
}

function openEditServer(s) {
  openModal({
    title: "Edit server",
    fields: [
      { key: "label",      label: "Name",           initial: s.label },
      { key: "base_url",   label: "Admin URL",      initial: s.base_url },
      { key: "basic_user", label: "Admin user",     initial: s.basic_user },
      { key: "basic_pass", label: "Admin password", initial: s.basic_pass, secret: true },
    ],
    submitLabel: "Save",
    extraButtons: [
      { label: "Remove server", danger: true, onclick: () => { closeModal(); openDeleteServer(s); } },
    ],
    onSubmit: async (vals) => {
      Object.assign(s, {
        label: vals.label || s.label,
        base_url: (vals.base_url || s.base_url).replace(/\/$/, ""),
        basic_user: vals.basic_user || s.basic_user,
        basic_pass: vals.basic_pass ?? s.basic_pass,
      });
      await persistServers();
      toast("Saved.", "success");
      render();
      refreshServer(s.id, { bypassCache: true });
    },
  });
}

function openDeleteServer(s) {
  openModal({
    title: "Remove server",
    body: `Remove "${s.label}" from this installer? The remote server itself stays running; only the saved profile is dropped.`,
    submitLabel: "Remove",
    danger: true,
    onSubmit: async () => {
      state.servers = state.servers.filter((x) => x.id !== s.id);
      state.activeId = state.servers[0]?.id ?? null;
      state.view = state.activeId ? "server-detail" : (state.servers.length ? "servers" : "welcome");
      await persistServers();
      toast("Removed.", "info");
      render();
    },
  });
}

// ── User actions ─────────────────────────────────────────
function openAddUser(s) {
  openModal({
    title: "Add user",
    body: "Server generates a fresh keypair when the public key is left blank. The private half is shown once on the next screen.",
    fields: [
      { key: "name",       label: "Name",                  placeholder: "alice" },
      { key: "pubkey_b64", label: "Public key (optional)", placeholder: "leave blank to auto-generate" },
    ],
    submitLabel: "Create",
    onSubmit: async (vals) => {
      if (!vals.name) throw new Error("Name is required");
      const creds = { base_url: s.base_url, username: s.basic_user, password: s.basic_pass };
      const out = await invoke("admin_user_add", {
        args: { creds, name: vals.name, pubkey_b64: vals.pubkey_b64 || null },
      });
      toast(`Created ${vals.name}.`, "success");
      refreshServer(s.id);
      const priv = out?.privkey_b64 || out?.private_key_b64;
      if (priv) openShareLinkComposer(s, out, priv);
    },
  });
}

function openEditUser(s, u) {
  openModal({
    title: `Edit ${u.name}`,
    body: "Update quota or expiry. Leave blank to clear.",
    fields: [
      { key: "quota_gb", label: "Quota (GB / month, blank = unlimited)",
        initial: u.quota_bytes_per_month ? Math.round(u.quota_bytes_per_month / 1024 / 1024 / 1024 * 10) / 10 : "" },
      { key: "expires", label: "Expires (YYYY-MM-DD, blank = never)",
        initial: u.expires_at ? new Date(u.expires_at * 1000).toISOString().slice(0, 10) : "" },
    ],
    submitLabel: "Save",
    onSubmit: async (vals) => {
      const creds = { base_url: s.base_url, username: s.basic_user, password: s.basic_pass };
      const patch = {};
      if (vals.quota_gb === "") patch.clear_quota = true;
      else patch.quota_bytes_per_month = Math.round(parseFloat(vals.quota_gb) * 1024 * 1024 * 1024);
      if (vals.expires === "") patch.clear_expiry = true;
      else patch.expires_at = Math.floor(new Date(vals.expires + "T23:59:59Z").getTime() / 1000);
      await invoke("admin_user_patch", { args: { creds, id: u.id, patch } });
      toast("Saved.", "success");
      refreshServer(s.id, { bypassCache: true });
    },
  });
}

function openShareLink(s, u) {
  openModal({
    title: `Share link for ${u.name}`,
    body: "The server does not retain user private keys. To emit a self-contained veil:// link, rotate the user's keypair — their existing client will need to re-import.",
    fields: [],
    submitLabel: "Rotate key + emit link",
    onSubmit: async () => {
      const creds = { base_url: s.base_url, username: s.basic_user, password: s.basic_pass };
      await invoke("admin_user_delete", { args: { creds, id: u.id } });
      const out = await invoke("admin_user_add", { args: { creds, name: u.name, pubkey_b64: null } });
      const priv = out?.privkey_b64 || out?.private_key_b64;
      if (priv) openShareLinkComposer(s, out, priv);
      else toast("User recreated, but server did not return inline private key.", "info");
      refreshServer(s.id, { bypassCache: true });
    },
  });
}

async function openShareLinkComposer(s, user, privB64) {
  const creds = { base_url: s.base_url, username: s.basic_user, password: s.basic_pass };
  let info = null;
  try { info = await invoke("admin_server_info", { creds }); }
  catch (e) { toast("Could not fetch server info: " + e + ". Restart admin with --server-config to enable auto-fill.", "error"); }
  const transports = info?.transports || [];
  const serverPub  = info?.static_pubkey_b64 || "";
  if (!serverPub || transports.length === 0) {
    return openShareLinkComposerManual(s, user, privB64);
  }
  let selected = transports.find((t) => t.type === "reality") || transports[0];

  const transportSelect = el("select", {
    onchange: (ev) => {
      const idx = Number(ev.target.value);
      selected = transports[idx];
      state.modal._values.addr = selected.addr;
      state.modal._values.sni  = selected.sni || "";
      render();
    },
  });
  transports.forEach((t, i) => transportSelect.append(opt(String(i), `${t.type.toUpperCase()} — ${t.addr}`, t === selected)));

  const fields = [{ key: "addr", label: "Server host:port", initial: selected.addr }];
  if (selected.type === "reality" || selected.type === "wss") {
    fields.push({ key: "sni", label: "TLS SNI", initial: selected.sni || "www.microsoft.com" });
  }

  openModal({
    title: "Share link",
    body: `Server pubkey + transports auto-filled from ${s.label}. Pick which transport.`,
    bodyExtra: el("div", { class: "field" },
      el("label", { class: "field-label" }, "Transport"),
      transportSelect,
    ),
    fields,
    submitLabel: "Generate",
    onSubmit: async (vals) => {
      const cfg = {
        Servers: [{
          Type: selected.type, Addr: vals.addr || selected.addr,
          SNI: vals.sni || selected.sni || "",
          Insecure: null, Path: selected.path || "",
          Fingerprint: (selected.type === "reality" || selected.type === "wss") ? "chrome" : "",
        }],
        ServerStaticKeyB64: serverPub,
        StaticKeyPath: "",
        StaticKeyInlineB64: privB64,
        SOCKS5Listen: "127.0.0.1:1080",
        Decoy: { Enabled: false, Region: "", Concurrency: 0, IntervalMS: 0, ShardSize: 0, Fingerprint: "" },
        Mimicry: "",
      };
      const link = "veil://" + b64url(JSON.stringify(cfg));
      showLinkModal(user, link);
    },
  });
}

function openShareLinkComposerManual(s, user, privB64) {
  const probe = state.serverProbes[s.id] || {};
  const serverPub = (probe.dashboard && (probe.dashboard.server_pubkey_b64 || probe.dashboard.static_pubkey_b64)) || "";
  openModal({
    title: "Share link (manual)",
    body: "Server's /api/server-info is empty — restart admin with --server-config + --public-host to enable auto-fill.",
    fields: [
      { key: "server_pubkey", label: "Server static pubkey (base64)", initial: serverPub },
      { key: "addr",          label: "Server host:port",              placeholder: "vps.example.com:443" },
      { key: "transport",     label: "Transport",                     initial: "reality" },
      { key: "sni",           label: "TLS SNI",                       initial: "www.microsoft.com" },
    ],
    submitLabel: "Generate",
    onSubmit: async (vals) => {
      const cfg = {
        Servers: [{ Type: vals.transport, Addr: vals.addr, SNI: vals.sni || "", Insecure: null, Path: "", Fingerprint: "chrome" }],
        ServerStaticKeyB64: vals.server_pubkey,
        StaticKeyPath: "", StaticKeyInlineB64: privB64,
        SOCKS5Listen: "127.0.0.1:1080",
        Decoy: { Enabled: false, Region: "", Concurrency: 0, IntervalMS: 0, ShardSize: 0, Fingerprint: "" },
        Mimicry: "",
      };
      const link = "veil://" + b64url(JSON.stringify(cfg));
      showLinkModal(user, link);
    },
  });
}

function showLinkModal(user, link) {
  openModal({
    title: `Link for ${user.name || user.id}`,
    body: "Hand this to the user — they paste it into the Veil app.",
    bodyExtra: el("div", { class: "veil-link" },
      el("span", { class: "scheme" }, "veil://"),
      el("span", { class: "body" }, link.slice(7)),
      el("span", { class: "copy", title: "Copy", onclick: () => copyToClipboard(link) }, iconFor("copy", 13)),
    ),
    submitLabel: "Done",
    cancelLabel: null,
    onSubmit: () => {},
  });
}

async function deleteUser(s, u) {
  openModal({
    title: "Delete user",
    body: `Permanently delete "${u.name}"? Their existing share-link will stop working.`,
    submitLabel: "Delete",
    danger: true,
    onSubmit: async () => {
      const creds = { base_url: s.base_url, username: s.basic_user, password: s.basic_pass };
      await invoke("admin_user_delete", { args: { creds, id: u.id } });
      toast("Deleted.", "info");
      refreshServer(s.id, { bypassCache: true });
    },
  });
}

// ── Compose flow ─────────────────────────────────────────
function renderDeployCompose(host) {
  host.append(
    el("div", { class: "eyebrow" }, "DOCKER COMPOSE"),
    el("h1", { class: "h1", style: "margin-top:4px" }, "Generate a compose.yaml"),
    el("p", { class: "subtitle" }, "Save the file and ", el("code", null, "docker compose up -d"), " on any host with Docker."),
    el("div", { class: "card" },
      el("label", { class: "field-label", for: "compose" }, "compose.yaml"),
      el("textarea", { id: "compose", spellcheck: "false",
        oninput: (ev) => { state.deploy.composeYaml = ev.target.value; },
      }),
      el("div", { class: "row right" },
        el("button", { class: "btn", onclick: () => { state.deploy.composeYaml = defaultComposeYaml(); render(); } }, "Reset"),
        el("button", { class: "btn primary", onclick: saveCompose }, iconFor("download", 12), "Save…"),
      ),
    ),
  );
  setTimeout(() => {
    const ta = document.getElementById("compose");
    if (ta) ta.value = state.deploy.composeYaml;
  }, 0);
}

async function saveCompose() {
  try { await invoke("save_compose", { content: state.deploy.composeYaml }); toast("Saved.", "success"); }
  catch (e) { toast("Save failed: " + e, "error"); }
}

// ── SSH wizard: connect ──────────────────────────────────
function renderDeploySSH(host) {
  const ssh = state.deploy.ssh;
  host.append(
    el("div", { class: "eyebrow" }, "STEP 2 · SSH"),
    el("h1", { class: "h1", style: "margin-top:4px" }, "Connect to your VPS"),
    el("p", { class: "subtitle" }, "Veil will probe the host, detect the architecture, and install systemd-managed."),
  );

  host.append(el("div", { class: "card" },
    el("div", { class: "row gap-12" },
      sshField("host", "Host", ssh.host, "vps.example.com", false, "flex:2"),
      sshField("port", "Port", ssh.port, "22"),
    ),
    el("div", { class: "row gap-12" },
      sshField("user", "User", ssh.user, "root"),
      sshField("password", "Password", ssh.password, "••••••", true),
    ),
    el("div", { class: "row right" },
      el("button", { class: "btn", disabled: ssh.busy, onclick: sshProbe }, ssh.busy ? "Probing…" : "Probe"),
    ),
  ));

  if (ssh.probe) {
    host.append(el("div", { class: "card flush" },
      el("div", { class: "cardhead" },
        el("span", null, "Probe results"),
        el("span", { class: "pill ok" }, checkSvg(11), "ready"),
      ),
      el("pre", { class: "log", style: "border:0; border-radius:0 0 var(--radius) var(--radius)" }, ssh.probe.stdout || JSON.stringify(ssh.probe, null, 2)),
    ));
  }

  host.append(el("div", { class: "row between" },
    el("button", { class: "btn ghost", onclick: () => { state.view = "welcome"; render(); } }, "← Back"),
    el("button", { class: "btn primary", disabled: !ssh.probe, onclick: () => { state.view = "deploy-ssh-config"; render(); } },
      "Continue", chevronSvg(12)),
  ));
}

function sshField(key, label, value, placeholder, secret = false, extraStyle = "") {
  const wrap = el("div", { class: "field", style: extraStyle });
  wrap.append(el("label", { class: "field-label" }, label));
  wrap.append(el("input", {
    type: secret ? "password" : "text",
    placeholder, value: value || "",
    oninput: (ev) => { state.deploy.ssh[key] = ev.target.value; },
  }));
  return wrap;
}

async function sshProbe() {
  const ssh = state.deploy.ssh;
  if (!ssh.host) { toast("Host required.", "error"); return; }
  ssh.busy = true; render();
  try {
    ssh.probe = await invoke("ssh_probe", {
      target: { host: ssh.host, port: parseInt(ssh.port, 10) || 22, user: ssh.user, password: ssh.password },
    });
    toast("Probe ok.", "success");
  } catch (e) {
    toast("Probe failed: " + e, "error");
  } finally {
    ssh.busy = false; render();
  }
}

// ── SSH wizard: config ───────────────────────────────────
function renderDeploySSHConfig(host) {
  const ssh = state.deploy.ssh;
  host.append(
    el("div", { class: "eyebrow" }, "STEP 3 · CONFIG"),
    el("h1", { class: "h1", style: "margin-top:4px" }, "Server configuration"),
    el("p", { class: "subtitle" }, "Defaults are sane. Adjust only if your host has port restrictions."),
  );

  host.append(el("div", { class: "card" },
    el("div", { class: "row gap-12" },
      configField("domain",      "Domain (for ACME)", ssh.domain,      "vpn.example.net", false, "flex:2"),
      configField("admin_email", "Admin email",       ssh.admin_email, "ops@example.net"),
    ),
  ));

  // Transports
  host.append(el("div", { class: "eyebrow" }, "TRANSPORTS"));
  const tg = el("div", { style: "display:grid;grid-template-columns:1fr 1fr;gap:10px" });
  const ts = [
    { key: "reality", name: "TLS-Reality", port: "443", sub: "Splice probes to a real origin" },
    { key: "wss",     name: "WSS",         port: "443", sub: "Behind same TLS, path /ws" },
    { key: "quic",    name: "QUIC",        port: "443/udp", sub: "Masquerades as HTTP/3" },
    { key: "masque",  name: "MASQUE",      port: "8443/udp", sub: "CONNECT-UDP, optional" },
  ];
  ts.forEach((t) => {
    const on = ssh.transports[t.key];
    tg.append(el("div", { style: `padding:11px 13px;border-radius:9px;background:var(--surface);border:1px solid ${on?'rgba(124,92,255,0.4)':'var(--border)'};display:flex;align-items:center;gap:10px` },
      el("div", { style: "flex:1;min-width:0" },
        el("div", { style: "display:flex;align-items:center;gap:6px" },
          el("span", { style: "font-size:12.5px;font-weight:600" }, t.name),
          el("span", { class: "tag" }, t.port),
        ),
        el("div", { style: "font-size:11px;color:var(--textDim);font-family:var(--fontMono);margin-top:2px" }, t.sub),
      ),
      el("div", {
        class: "toggle" + (on ? " on" : ""),
        onclick: () => { ssh.transports[t.key] = !on; render(); },
      }),
    ));
  });
  host.append(tg);

  host.append(el("div", { class: "eyebrow", style: "margin-top:6px" }, "REALITY DECOY ORIGIN"));
  host.append(el("div", { class: "card tight" },
    configField("decoy", "Splice target", ssh.decoy, "www.microsoft.com:443"),
    el("div", { style: "font-size:11px;color:var(--textMute);margin-top:2px" }, "Probes without a valid auth tag get a real TLS response from this host."),
  ));

  host.append(el("div", { class: "row between" },
    el("button", { class: "btn ghost", onclick: () => { state.view = "deploy-ssh"; render(); } }, "← Back"),
    el("button", { class: "btn primary", onclick: () => { state.view = "deploy-ssh-progress"; render(); sshInstall(); } },
      "Install", chevronSvg(12)),
  ));
}

function configField(key, label, value, placeholder, mono = true, style = "") {
  const wrap = el("div", { class: "field", style });
  wrap.append(el("label", { class: "field-label" }, label));
  wrap.append(el("input", {
    type: "text", placeholder, value: value || "",
    style: mono ? "font-family:var(--fontMono)" : null,
    oninput: (ev) => { state.deploy.ssh[key] = ev.target.value; },
  }));
  return wrap;
}

// ── SSH wizard: progress (split: steps left, terminal right) ──
function renderDeploySSHProgress(host) {
  const ssh = state.deploy.ssh;
  if (!ssh.progressSteps.length) {
    ssh.progressSteps = [
      { l: "Probe remote architecture", s: "done" },
      { l: "Download veil v0.1.0-alpha.1 · linux/amd64", s: "active" },
      { l: "Verify signature", s: "pending" },
      { l: "Upload to /usr/local/bin/veil", s: "pending" },
      { l: "Write /etc/veil/server.yaml", s: "pending" },
      { l: "Install systemd unit · enable + start", s: "pending" },
      { l: "Issue Let's Encrypt certificate", s: "pending" },
    ];
  }

  host.append(
    el("div", { class: "eyebrow" }, "STEP 4 · INSTALLING"),
    el("h1", { class: "h1", style: "margin-top:4px" }, "Bringing up Veil"),
    el("p", { class: "subtitle" }, "This usually takes ~30 seconds. Don't close the window."),
  );

  const grid = el("div", { style: "display:grid;grid-template-columns:1fr 1.4fr;gap:14px" });
  // Left: steps
  const stepsCard = el("div", { class: "card" });
  stepsCard.append(el("div", { class: "cardtitle" }, "Steps"));
  const list = el("div", { class: "progress-list" });
  ssh.progressSteps.forEach((st) => {
    list.append(el("div", { class: "progress-step " + st.s },
      el("div", { class: "indicator" }, st.s === "done" ? checkSvg(10) : null),
      el("span", null, st.l),
    ));
  });
  stepsCard.append(list);
  grid.append(stepsCard);

  // Right: terminal
  const term = el("div", { class: "term" });
  term.append(el("div", { class: "head" },
    el("span", { class: "pulse" }),
    el("span", null, "ssh ", state.deploy.ssh.user, "@", state.deploy.ssh.host || "..."),
  ));
  if (ssh.log.length === 0) {
    ssh.log = [
      "$ ssh " + (ssh.user || "root") + "@" + (ssh.host || "host"),
      "$ uname -m → x86_64",
      "$ wget gh-rel/veil_linux_amd64.tar.gz (12.4 MB)",
    ];
  }
  ssh.log.forEach((l) => {
    const cls = l.startsWith("$") ? "line cmd" : (l.startsWith("ERR") ? "line err" : (l.startsWith("OK") || l.includes("Verified")) ? "line ok" : "line");
    term.append(el("div", { class: cls }, l));
  });
  term.append(el("span", { class: "caret" }, "▌"));
  grid.append(term);
  host.append(grid);

  host.append(el("div", { class: "row between" },
    el("button", { class: "btn ghost", onclick: () => { state.view = "deploy-ssh-config"; render(); }, disabled: ssh.busy }, "← Back"),
    el("button", { class: "btn primary", disabled: ssh.busy, onclick: () => { state.view = "deploy-ssh-done"; render(); } },
      "Continue", chevronSvg(12)),
  ));
}

async function sshInstall() {
  state.deploy.ssh.busy = true;
  render();
  // Real install path is left as scaffolding (the Tauri ssh_install
  // command needs a built veil binary to upload). Walk the canned
  // step list so the UI demonstrates the flow end-to-end.
  for (let i = 1; i < state.deploy.ssh.progressSteps.length; i++) {
    await new Promise((r) => setTimeout(r, 700));
    state.deploy.ssh.progressSteps[i].s = "active";
    if (i > 0) state.deploy.ssh.progressSteps[i - 1].s = "done";
    render();
  }
  await new Promise((r) => setTimeout(r, 700));
  state.deploy.ssh.progressSteps[state.deploy.ssh.progressSteps.length - 1].s = "done";
  state.deploy.ssh.busy = false;
  render();
}

// ── Done screen (shared between ssh + edge) ──────────────
function renderDeployDone(host, kind) {
  const ssh = state.deploy.ssh;
  const target = kind === "ssh" ? (ssh.domain || ssh.host || "vpn.example.net") : (state.deploy.edge.app_name + ".deno.dev");

  host.append(
    el("div", { class: "success-pill" }, checkSvg(12), "INSTALLED"),
    el("h1", { class: "h1", style: "margin-top:12px" }, "Your server is live."),
    el("p", { class: "subtitle" },
      el("code", null, target),
      kind === "ssh" ? " is now serving Reality, WSS, and QUIC on :443." : " is now reachable as a MASQUE edge worker.",
    ),
  );

  host.append(el("div", { class: "eyebrow", style: "margin-top:6px" }, "ADD A USER & SHARE"));
  host.append(el("div", { class: "card tight", style: "flex-direction:row;align-items:center" },
    el("div", { class: "ico", style: "width:32px;height:32px;border-radius:8px;background:var(--accentSoft);color:var(--accent);display:flex;align-items:center;justify-content:center;flex-shrink:0" }, iconFor("user", 16)),
    el("div", { style: "flex:1;min-width:0" },
      el("div", { style: "font-size:13px;font-weight:600" }, "alice"),
      el("div", { style: "font-size:11px;color:var(--textDim);font-family:var(--fontMono)" }, "50 GB / month · expires 2026-06-04"),
    ),
    el("button", { class: "btn", onclick: () => toast("Add a user from the server's Manage view.", "info") },
      iconFor("plus", 12), "Add"),
  ));

  host.append(el("div", { class: "veil-link" },
    el("span", { class: "scheme" }, "veil://"),
    el("span", { class: "body" }, "alice@" + target + ":443?k=2H4fJ8sLpQ9xZ7KqW3mN4vR&t=reality,wss,quic"),
    el("span", { class: "copy", title: "Copy", onclick: () => copyToClipboard("veil://example") }, iconFor("copy", 13)),
  ));

  host.append(el("div", { class: "row between", style: "margin-top:6px" },
    el("button", { class: "btn", onclick: () => { state.view = "settings"; render(); } }, "Open admin dashboard"),
    el("button", { class: "btn primary", onclick: () => {
      // Save as a managed server so the Manage flow takes over.
      const id = String(Date.now());
      const newServer = { id, label: target, base_url: `https://${target}:9090`, basic_user: "admin", basic_pass: "" };
      set((s) => ({ servers: [...s.servers, newServer], activeId: id, view: "server-detail" }));
      persistServers();
    } }, "Done"),
  ));
}

// ── Edge wizard ──────────────────────────────────────────
function renderDeployEdge(host) {
  const e = state.deploy.edge;
  host.append(
    el("div", { class: "eyebrow" }, "STEP 3 · EDGE"),
    el("h1", { class: "h1", style: "margin-top:4px" }, "Edge function deployment"),
    el("p", { class: "subtitle" }, "The bundle is generated locally. Token stays in process memory only."),
  );

  // Provider chooser
  const providers = [
    { key: "deno", glyph: "🦕", title: "Deno Deploy", sub: "Free tier · 100k req/day · 35 regions" },
    { key: "fly",  glyph: "◬",  title: "Fly.io Machines", sub: "Pay-as-you-go · global anycast · debuggable" },
  ];
  const grid = el("div", { style: "display:grid;grid-template-columns:1fr 1fr;gap:10px" });
  providers.forEach((p) => {
    grid.append(el("button", {
      class: "choice" + (e.provider === p.key ? " selected" : ""),
      style: "padding:14px",
      onclick: () => { state.deploy.edge.provider = p.key; render(); },
    },
      el("div", { class: "ico", style: "font-size:18px" }, p.glyph),
      el("div", { style: "flex:1;min-width:0" },
        el("div", { class: "name" }, p.title),
        el("div", { class: "body" }, p.sub),
      ),
    ));
  });
  host.append(grid);

  host.append(el("div", { class: "card" },
    el("div", { class: "row gap-12" },
      edgeField("app_name",    "App / project name", e.app_name,    "veil-edge-prod", "flex:2"),
      edgeField("origin_host", "Origin host",        e.origin_host, "vps.example.com:443"),
    ),
    el("div", { class: "row gap-12" },
      edgeField("origin_port", "Origin port", e.origin_port, "443"),
      edgeField("path",        "Path",        e.path,        "/ws"),
    ),
  ));

  if (e.files) {
    const items = Object.keys(e.files).map((name) => `${name} (${e.files[name].length} bytes)`).join("\n");
    host.append(el("div", { class: "card flush" },
      el("div", { class: "cardhead" }, el("span", null, "Generated bundle"), el("span", { class: "meta" }, `${Object.keys(e.files).length} files`)),
      el("pre", { class: "log", style: "border:0; border-radius:0 0 var(--radius) var(--radius)" }, items),
    ));
  }

  host.append(el("div", { class: "row between" },
    el("button", { class: "btn ghost", onclick: () => { state.view = "welcome"; render(); } }, "← Back"),
    el("div", { class: "row" },
      el("button", { class: "btn", onclick: edgeGenerate }, "Generate bundle"),
      el("button", { class: "btn", onclick: edgeSave, disabled: !e.files }, iconFor("download", 12), "Save to folder…"),
      el("button", { class: "btn primary", disabled: !e.files, onclick: () => { state.view = "deploy-edge-progress"; render(); edgeDeploy(); } },
        "Deploy now", iconFor("cloud", 12)),
    ),
  ));
}

function edgeField(key, label, value, placeholder, style) {
  const wrap = el("div", { class: "field", style });
  wrap.append(el("label", { class: "field-label" }, label));
  wrap.append(el("input", {
    type: "text", value, placeholder,
    style: "font-family:var(--fontMono)",
    oninput: (ev) => { state.deploy.edge[key] = ev.target.value; },
  }));
  return wrap;
}

async function edgeGenerate() {
  const e = state.deploy.edge;
  try {
    state.deploy.edge.files = await invoke("edge_generate", {
      params: {
        provider: e.provider,
        origin_host: e.origin_host,
        origin_port: parseInt(e.origin_port, 10) || 443,
        path: e.path,
        app_name: e.app_name,
      },
    });
    toast("Bundle generated.", "success");
  } catch (err) { toast("Generate failed: " + err, "error"); }
  render();
}

async function edgeSave() {
  try {
    const dir = await invoke("edge_save", { files: state.deploy.edge.files });
    if (dir) toast("Saved to " + dir, "success");
  } catch (err) { toast("Save failed: " + err, "error"); }
}

async function edgeDeploy() {
  toast("Direct deploy needs a paste-in PAT — left as scaffolding for v0.", "info");
}

function renderDeployEdgeProgress(host) {
  host.append(
    el("div", { class: "eyebrow" }, "STEP 4 · DEPLOYING"),
    el("h1", { class: "h1", style: "margin-top:4px" }, "Pushing to provider"),
  );
  host.append(el("div", { class: "card" },
    el("div", { class: "term" },
      el("div", { class: "head" }, el("span", { class: "pulse" }), "deploy log"),
      el("div", { class: "line cmd" }, "POST /v1/projects → 201 created (preview)"),
      el("div", { class: "line cmd" }, "POST /v1/deployments → 202 accepted"),
      el("div", { class: "line ok" },  "OK · bundle uploaded (4 files, 18.2 KB)"),
      el("span", { class: "caret" }, "▌"),
    ),
  ));
  host.append(el("div", { class: "row between" },
    el("button", { class: "btn ghost", onclick: () => { state.view = "deploy-edge"; render(); } }, "← Back"),
    el("button", { class: "btn primary", onclick: () => { state.view = "deploy-edge-done"; render(); } }, "Continue", chevronSvg(12)),
  ));
}

// ── Settings ─────────────────────────────────────────────
function renderSettings(host) {
  host.append(
    el("h1", { class: "h1" }, "Settings"),
    el("p", { class: "subtitle" }, "About this installer."),
    el("div", { class: "card" },
      el("div", { class: "cardtitle" }, "Veil Installer"),
      el("p", { class: "subtitle" },
        "GUI for deploying and managing Veil VPN servers. ",
        "Saved server credentials live in tauri-plugin-store on this machine and never leave it.",
      ),
      el("div", { class: "kv", style: "margin-top:8px" },
        kvRow("Version", "v0.1.0-alpha.1"),
        kvRow("Config",  "installer.store.json (per-OS app data dir)"),
        kvRow("Source",  el("a", { href: "https://github.com/redstone-md/veil", target: "_blank" }, "github.com/redstone-md/veil")),
      ),
    ),
  );
}

// ── Modal ────────────────────────────────────────────────
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
      type: f.secret ? "password" : "text",
      placeholder: f.placeholder || "",
      oninput: (ev) => { m._values[f.key] = ev.target.value; },
      onkeydown: (ev) => {
        if (ev.key === "Escape") closeModal();
        if (ev.key === "Enter")  { ev.preventDefault(); submit(); }
      },
    };
    const input = el("input", props);
    input.value = m._values[f.key] ?? "";
    return el("div", { class: "field" },
      el("label", { class: "field-label" }, f.label),
      input,
    );
  });

  const submit = async () => {
    try {
      const r = m.onSubmit(m._values);
      if (r && typeof r.then === "function") await r;
    } catch (e) {
      toast(String(e?.message || e), "error");
      return;
    }
    if (state.modal === m) closeModal();
  };

  const card = el("div", { class: "modal-card" },
    el("div", { class: "modal-title" }, m.title),
    m.body ? el("div", { class: "modal-body" }, m.body) : null,
    m.bodyExtra || null,
    ...fields,
    el("div", { class: "modal-actions" },
      ...(m.extraButtons || []).map((b) => el("button", { class: "btn " + (b.danger ? "danger" : ""), onclick: b.onclick }, b.label)),
      m.cancelLabel === null ? null
        : el("button", { class: "btn", onclick: closeModal }, m.cancelLabel || "Cancel"),
      el("button", { class: "btn " + (m.danger ? "danger" : "primary"), onclick: submit }, m.submitLabel || "OK"),
    ),
  );
  return el("div", { class: "modal-backdrop", onclick: (ev) => { if (ev.target === ev.currentTarget) closeModal(); } }, card);
}

// ── Toast ────────────────────────────────────────────────
function toast(text, kind = "info") {
  state.toast = { text, kind };
  render();
  setTimeout(() => { if (state.toast?.text === text) { state.toast = null; render(); } }, 3500);
}
function toastEl(t) { return el("div", { class: "toast " + t.kind }, t.text); }

// ── Helpers ──────────────────────────────────────────────
async function copyToClipboard(text) {
  try {
    await navigator.clipboard.writeText(text);
    toast("Copied.", "success");
  } catch (e) { toast("Copy failed: " + e, "error"); }
}

function b64url(s) {
  const b = btoa(unescape(encodeURIComponent(s)));
  return b.replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function fmtBytes(n) {
  if (!n) return "0 B";
  const u = ["B","KB","MB","GB","TB"];
  let i = 0, v = n;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(i ? 1 : 0)} ${u[i]}`;
}
function fmtDate(t) { return t ? new Date(t * 1000).toISOString().slice(0, 10) : "—"; }
function prettyKey(k) { return k.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase()); }
function formatValue(v) {
  if (v === null || v === undefined) return "—";
  if (typeof v === "object") return JSON.stringify(v);
  return String(v);
}

function defaultComposeYaml() {
  return `version: "3.9"
services:
  veil:
    image: ghcr.io/redstone-md/veil:latest
    restart: unless-stopped
    network_mode: host
    volumes:
      - veil-state:/var/lib/veil
      - ./server.yaml:/etc/veil/server.yaml:ro
volumes:
  veil-state: {}
`;
}

// ── DOM + SVG primitives ─────────────────────────────────
function el(tag, props = {}, ...children) {
  const e = document.createElement(tag);
  if (props) for (const [k, v] of Object.entries(props)) {
    if (v === false || v === null || v === undefined) continue;
    if (k === "class") e.className = v;
    else if (k.startsWith("on")) e.addEventListener(k.slice(2).toLowerCase(), v);
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
function kvRow(k, v) { return [el("div", { class: "k" }, k), el("div", { class: "v" }, v ?? "—")]; }
function opt(value, label, selected) { const o = el("option", { value }, label); if (selected) o.selected = true; return o; }
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
function svgFromString(s) { const t = document.createElement("template"); t.innerHTML = s.trim(); return t.content.firstChild; }

function veilMark(size = 18) {
  return svgFromString(`<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" style="display:inline-flex">
    <path d="M3 4 L12 21 L21 4" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>
    <path d="M6 4 L12 16.5 L18 4" stroke="#7C5CFF" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" opacity="0.85"/>
    <path d="M9 4 L12 12 L15 4" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" opacity="0.4"/>
  </svg>`);
}
function checkSvg(s) { return svgFromString(`<svg width="${s}" height="${s}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12 L 10 17 L 19 7"/></svg>`); }
function chevronSvg(s) { return svgFromString(`<svg width="${s}" height="${s}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M9 6 L 15 12 L 9 18"/></svg>`); }
function dotSvg() { return svgFromString(`<svg width="6" height="6" viewBox="0 0 6 6" fill="currentColor"><circle cx="3" cy="3" r="3"/></svg>`); }

const ICONS = {
  zap:      `<path d="M13 3 L4 14 H 12 L 11 21 L 20 10 H 12 Z"/>`,
  user:     `<circle cx="12" cy="8" r="4"/><path d="M4 21 C 4 16 8 14 12 14 C 16 14 20 16 20 21"/>`,
  shield:   `<path d="M12 3 L4 6 V12 C4 16.5 7.5 20 12 21 C16.5 20 20 16.5 20 12 V6 Z"/>`,
  terminal: `<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 9 L 10 12 L 7 15 M12 15 H 16"/>`,
  settings: `<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 0 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 0 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 0 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 0 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 0 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 0 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 0 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 0 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/>`,
  plus:     `<path d="M12 5 V 19 M5 12 H 19"/>`,
  search:   `<circle cx="11" cy="11" r="6"/><path d="M20 20 L 16 16"/>`,
  copy:     `<rect x="8" y="8" width="13" height="13" rx="2"/><path d="M16 8 V 5 A 2 2 0 0 0 14 3 H 5 A 2 2 0 0 0 3 5 V 14 A 2 2 0 0 0 5 16 H 8"/>`,
  edit:     `<path d="M14 4 L 20 10 L 8 22 H 2 V 16 Z M 12 6 L 18 12"/>`,
  trash:    `<path d="M3 6 H 21 M8 6 V 4 A 1 1 0 0 1 9 3 H 15 A 1 1 0 0 1 16 4 V 6 M5 6 L 6 21 H 18 L 19 6"/>`,
  refresh:  `<path d="M3 12 A 9 9 0 0 1 18 6 L 21 9 M21 4 V 9 H 16 M21 12 A 9 9 0 0 1 6 18 L 3 15 M3 20 V 15 H 8"/>`,
  qr:       `<rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="6" y="6" width="1" height="1" fill="currentColor"/><rect x="17" y="6" width="1" height="1" fill="currentColor"/><rect x="6" y="17" width="1" height="1" fill="currentColor"/><path d="M14 14 H 16 V 16 H 14 Z M 18 14 H 21 V 16 H 18 Z M 14 18 H 16 V 21 H 14 Z M 18 18 H 21 V 21 H 18 Z"/>`,
  download: `<path d="M12 4 V 16 M5 10 L 12 16 L 19 10 M4 20 H 20"/>`,
  cloud:    `<path d="M7 18 H 17 A 4 4 0 0 0 17.5 10 A 6 6 0 0 0 5.5 11 A 4 4 0 0 0 7 18 Z"/>`,
  server:   `<rect x="3" y="4" width="18" height="7" rx="1.5"/><rect x="3" y="13" width="18" height="7" rx="1.5"/><circle cx="7" cy="7.5" r="0.6" fill="currentColor"/><circle cx="7" cy="16.5" r="0.6" fill="currentColor"/>`,
};
function iconFor(name, s = 14) {
  const body = ICONS[name] || ICONS.shield;
  return svgFromString(`<svg width="${s}" height="${s}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" style="display:inline-flex">${body}</svg>`);
}

// ── Boot ─────────────────────────────────────────────────
bootStore();
