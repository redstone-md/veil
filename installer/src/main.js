// Veil Installer — manager-first GUI.
//
// Sidebar shell with two top-level sections:
//
//   Servers — saved deployments. Per-server view shows reachability
//             (admin /api/version), dashboard snapshot, and a users
//             tab driven by /api/users CRUD. Each user row offers a
//             one-click share link emit.
//
//   Deploy  — the original three workflows (Docker compose generator,
//             SSH bring-up, Edge worker bundle). On successful SSH
//             install the resulting server's admin URL + credentials
//             are saved automatically into Servers.
//
// Storage: tauri-plugin-store at installer.store.json. Holds the
// servers list, the active server id, and any user-edited form
// state we want to remember.

import { invoke } from "@tauri-apps/api/core";
import { getCurrentWindow } from "@tauri-apps/api/window";
import { Store } from "@tauri-apps/plugin-store";

const appWindow = getCurrentWindow();
const STORE_FILE = "installer.store.json";

const root = document.getElementById("app");
const state = {
  view: "servers", // servers | server-detail | deploy | deploy-compose | deploy-ssh | deploy-edge | settings
  servers: [],     // [{ id, label, base_url, basic_user, basic_pass, ssh? }]
  activeId: null,
  serverDetailTab: "overview", // overview | users
  serverProbes: {}, // id → { reachable, version, dashboard, users, error, ts }
  modal: null,
  toast: null,
  deploy: {
    composeYaml: defaultComposeYaml(),
    ssh: { host: "", port: "22", user: "root", password: "", probe: null, log: [], busy: false },
    edge: { provider: "deno", origin_host: "", origin_port: "443", path: "/ws", app_name: "veil-edge", files: null },
  },
};

let store = null;

async function bootStore() {
  store = await Store.load(STORE_FILE, { autoSave: true });
  state.servers = (await store.get("servers")) || [];
  state.activeId = (await store.get("active_server")) || (state.servers[0]?.id ?? null);
  if (state.activeId && state.servers.find((s) => s.id === state.activeId)) {
    state.view = "server-detail";
  }
  render();
}

async function persistServers() {
  await store.set("servers", state.servers);
  await store.set("active_server", state.activeId);
}

// --- render ---

function render() {
  root.innerHTML = "";
  root.append(renderTitlebar());
  const shell = el("div", { class: "shell" });
  shell.append(renderSidebar());
  const main = el("div", { class: "main" });
  shell.append(main);
  root.append(shell);
  switch (state.view) {
    case "server-detail":  renderServerDetail(main); break;
    case "deploy":         renderDeployIndex(main); break;
    case "deploy-compose": renderDeployCompose(main); break;
    case "deploy-ssh":     renderDeploySSH(main); break;
    case "deploy-edge":    renderDeployEdge(main); break;
    case "settings":       renderSettings(main); break;
    case "servers":
    default:               renderServersIndex(main);
  }
  if (state.toast) root.append(toastEl(state.toast));
  if (state.modal) root.append(modalEl(state.modal));
}

function renderTitlebar() {
  const drag = el("div", { class: "tb-drag", "data-tauri-drag-region": "" },
    el("div", { class: "tb-brand" },
      el("div", { class: "tb-mark" }),
      el("span", {}, "Veil Installer"),
    ),
  );
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
    sb.append(el("div", { class: "empty", style: "padding: 8px 12px; text-align:left" }, "No servers yet."));
  } else {
    for (const s of state.servers) {
      const probe = state.serverProbes[s.id];
      const dotClass = probe ? (probe.reachable ? "ok" : "bad") : "";
      const item = el("button", {
        class: "side-item" + (state.view === "server-detail" && state.activeId === s.id ? " active" : ""),
        onclick: () => selectServer(s.id),
      },
        el("div", { class: "dot " + dotClass }),
        el("div", { class: "label" }, s.label),
      );
      sb.append(item);
    }
  }
  sb.append(el("button", { class: "side-add", onclick: openAddServer }, "+ Add server"));

  sb.append(el("div", { class: "side-section" }, "Deploy"));
  sb.append(sideNav("deploy", "All workflows"));
  sb.append(sideNav("deploy-compose", "Docker compose"));
  sb.append(sideNav("deploy-ssh", "Install via SSH"));
  sb.append(sideNav("deploy-edge", "Edge worker"));

  sb.append(el("div", { class: "side-section" }, "App"));
  sb.append(sideNav("settings", "About / Settings"));
  return sb;
}

function sideNav(view, label) {
  return el("button", {
    class: "side-item" + (state.view === view ? " active" : ""),
    onclick: () => { state.view = view; render(); },
  },
    el("div", { class: "label" }, label),
  );
}

// --- Servers index (empty state) ---

function renderServersIndex(host) {
  host.append(
    el("h1", { class: "h1" }, "Servers"),
    el("p", { class: "subtitle" }, "Manage previously-deployed Veil servers, or roll out a new one from the Deploy section."),
  );
  if (state.servers.length === 0) {
    host.append(el("div", { class: "card" },
      el("div", { class: "cardtitle" }, "Get started"),
      el("p", { class: "subtitle" }, "Deploy your first server, or add an existing one by host + admin credentials."),
      el("div", { class: "row" },
        el("button", { class: "primary", onclick: () => { state.view = "deploy"; render(); } }, "Deploy a server"),
        el("button", { class: "smallbtn", onclick: openAddServer }, "Add existing"),
      ),
    ));
  } else {
    const grid = el("div", { class: "choicegrid" });
    for (const s of state.servers) {
      grid.append(el("button", { class: "choice", onclick: () => selectServer(s.id) },
        el("div", { class: "name" }, s.label),
        el("div", { class: "body" }, s.base_url),
      ));
    }
    host.append(grid);
  }
}

// --- Server detail view ---

async function selectServer(id) {
  state.activeId = id;
  state.view = "server-detail";
  state.serverDetailTab = "overview";
  await persistServers();
  render();
  refreshServer(id);
}

async function refreshServer(id) {
  const s = state.servers.find((x) => x.id === id);
  if (!s) return;
  const creds = { base_url: s.base_url, username: s.basic_user, password: s.basic_pass };
  state.serverProbes[id] = { ...(state.serverProbes[id] || {}), busy: true };
  render();
  try {
    const version = await invoke("admin_version", { creds });
    let dashboard = null;
    let users = null;
    try { dashboard = await invoke("admin_dashboard", { creds }); }
    catch (_) {}
    try { users = await invoke("admin_users_list", { creds }); }
    catch (_) {}
    state.serverProbes[id] = {
      reachable: true, version, dashboard, users, error: null, ts: Date.now(), busy: false,
    };
  } catch (e) {
    state.serverProbes[id] = { reachable: false, error: String(e), ts: Date.now(), busy: false };
  }
  render();
}

function renderServerDetail(host) {
  const s = state.servers.find((x) => x.id === state.activeId);
  if (!s) {
    host.append(el("div", { class: "empty" }, "Server not found."));
    return;
  }
  const probe = state.serverProbes[s.id] || {};

  host.append(
    el("div", { class: "row", style: "justify-content: space-between; align-items: flex-start" },
      el("div", {},
        el("h1", { class: "h1" }, s.label),
        el("p", { class: "subtitle" }, s.base_url),
      ),
      el("div", { class: "row" },
        statusPill(probe),
        el("button", { class: "smallbtn", onclick: () => refreshServer(s.id), disabled: !!probe.busy }, probe.busy ? "Refreshing…" : "Refresh"),
        el("button", { class: "ghost", onclick: () => openEditServer(s) }, "Edit"),
        el("button", { class: "ghost", onclick: () => openDeleteServer(s) }, "Remove"),
      ),
    ),
  );

  // Tabs
  host.append(el("div", { class: "row" },
    tab("overview", "Overview"),
    tab("users", "Users"),
  ));

  if (state.serverDetailTab === "overview") {
    renderServerOverview(host, s, probe);
  } else if (state.serverDetailTab === "users") {
    renderServerUsers(host, s, probe);
  }
}

function tab(key, label) {
  const active = state.serverDetailTab === key;
  return el("button", {
    class: active ? "primary" : "smallbtn",
    onclick: () => { state.serverDetailTab = key; render(); },
  }, label);
}

function statusPill(probe) {
  if (probe.busy) return el("div", { class: "pill warn" }, "Probing…");
  if (probe.reachable) return el("div", { class: "pill ok" }, "Reachable");
  if (probe.error)     return el("div", { class: "pill bad" }, "Unreachable");
  return el("div", { class: "pill" }, "Unknown");
}

function renderServerOverview(host, s, probe) {
  if (!probe.reachable) {
    host.append(el("div", { class: "card" },
      el("div", { class: "cardtitle" }, "Cannot reach server"),
      el("p", { class: "subtitle" }, probe.error || "No probe yet."),
      el("p", { class: "subtitle" },
        "Verify the admin endpoint is exposed (default localhost only). ",
        "If admin is bound to localhost, you can SSH-tunnel: ",
        el("code", {}, `ssh -L 9090:localhost:9090 user@host`),
      ),
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

function renderServerUsers(host, s, probe) {
  if (!probe.reachable) {
    host.append(el("div", { class: "empty" }, "Connect to the server before managing users."));
    return;
  }
  const users = probe.users || [];
  host.append(el("div", { class: "row", style: "justify-content: space-between" },
    el("div", { class: "subtitle" }, `${users.length} user${users.length === 1 ? "" : "s"}`),
    el("button", { class: "primary", onclick: () => openAddUser(s) }, "+ Add user"),
  ));
  if (users.length === 0) {
    host.append(el("div", { class: "card empty" }, "No users yet. Click +Add user to provision the first one."));
    return;
  }
  const tbl = el("table", { class: "tbl" });
  tbl.append(el("thead", {},
    el("tr", {},
      el("th", {}, "Name"),
      el("th", {}, "Status"),
      el("th", {}, "Quota"),
      el("th", {}, "Used"),
      el("th", {}, "Last seen"),
      el("th", { class: "actions" }, ""),
    ),
  ));
  const tbody = el("tbody");
  for (const u of users) {
    const status = u.status || "active";
    const pillClass = status === "active" ? "ok" : (status === "revoked" ? "bad" : "warn");
    tbody.append(el("tr", {},
      el("td", {}, u.name || u.id, el("div", { class: "id" }, u.id)),
      el("td", {}, el("span", { class: "pill " + pillClass }, status)),
      el("td", {}, fmtQuota(u.quota_bytes_per_month)),
      el("td", {}, fmtBytes(u.used_bytes_current_month || 0)),
      el("td", {}, u.last_seen || "—"),
      el("td", { class: "actions" },
        el("button", { class: "ghost", onclick: () => openShareLink(s, u) }, "Share link"),
        el("button", { class: "ghost", onclick: () => deleteUser(s, u) }, "Delete"),
      ),
    ));
  }
  tbl.append(tbody);
  host.append(el("div", { class: "card", style: "padding: 0; overflow: hidden" }, tbl));
}

// --- Server CRUD modals ---

function openAddServer() {
  openModal({
    title: "Add server",
    body: "Point at an existing Veil server's admin endpoint. The server must expose /api over HTTP Basic.",
    fields: [
      { key: "label",     label: "Name",          placeholder: "Production EU" },
      { key: "base_url",  label: "Admin URL",     placeholder: "https://veil.example.com:9090" },
      { key: "basic_user",label: "Admin user",    placeholder: "admin" },
      { key: "basic_pass",label: "Admin password",placeholder: "", secret: true },
    ],
    submitLabel: "Add",
    onSubmit: async (vals) => {
      if (!vals.label || !vals.base_url || !vals.basic_user) throw new Error("All fields required");
      const id = String(Date.now());
      state.servers.push({
        id, label: vals.label, base_url: vals.base_url.replace(/\/$/, ""),
        basic_user: vals.basic_user, basic_pass: vals.basic_pass || "",
      });
      state.activeId = id;
      state.view = "server-detail";
      await persistServers();
      toast(`Added ${vals.label}.`, "info");
      render();
      refreshServer(id);
    },
  });
}

function openEditServer(s) {
  openModal({
    title: "Edit server",
    fields: [
      { key: "label",      label: "Name",          initial: s.label },
      { key: "base_url",   label: "Admin URL",     initial: s.base_url },
      { key: "basic_user", label: "Admin user",    initial: s.basic_user },
      { key: "basic_pass", label: "Admin password",initial: s.basic_pass, secret: true },
    ],
    submitLabel: "Save",
    onSubmit: async (vals) => {
      Object.assign(s, {
        label: vals.label || s.label,
        base_url: (vals.base_url || s.base_url).replace(/\/$/, ""),
        basic_user: vals.basic_user || s.basic_user,
        basic_pass: vals.basic_pass ?? s.basic_pass,
      });
      await persistServers();
      toast("Saved.", "info");
      render();
      refreshServer(s.id);
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
      state.view = state.activeId ? "server-detail" : "servers";
      await persistServers();
      toast("Removed.", "info");
      render();
    },
  });
}

// --- User actions ---

function openAddUser(s) {
  openModal({
    title: "Add user",
    body: "Server generates a fresh keypair when the public key is left blank. The private half is shown once on the next screen so you can hand it to the user.",
    fields: [
      { key: "name",       label: "Name",                   placeholder: "alice" },
      { key: "pubkey_b64", label: "Public key (optional)",  placeholder: "leave blank to auto-generate" },
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
      // If server returned the inline private key, show it.
      const priv = out?.privkey_b64 || out?.private_key_b64;
      if (priv) {
        openShareLinkComposer(s, out, priv);
      }
    },
  });
}

function openShareLink(s, u) {
  // We can't fully assemble a share-link without the user's private
  // key (server doesn't keep one). Offer a partial preview + a
  // 'rotate key' button that re-creates the user with a fresh
  // keypair and emits the full link.
  openModal({
    title: `Share link for ${u.name}`,
    body: "The server does not retain user private keys. To emit a fully self-contained veil:// link you need to rotate the key — the user's existing client will need to re-import the new link.",
    fields: [],
    submitLabel: "Rotate key + emit link",
    onSubmit: async () => {
      const creds = { base_url: s.base_url, username: s.basic_user, password: s.basic_pass };
      // Server-side rotate isn't directly exposed via REST; fallback:
      // delete + re-add with same name.
      await invoke("admin_user_delete", { args: { creds, id: u.id } });
      const out = await invoke("admin_user_add", {
        args: { creds, name: u.name, pubkey_b64: null },
      });
      const priv = out?.privkey_b64 || out?.private_key_b64;
      if (priv) {
        openShareLinkComposer(s, out, priv);
      } else {
        toast("User recreated, but server did not return inline private key.", "info");
      }
      refreshServer(s.id);
    },
  });
}

async function openShareLinkComposer(s, user, privB64) {
  // Fetch the server's full topology so we can pre-fill every
  // technical field — the operator should not need to know what a
  // base64 pubkey is, let alone type one.
  const creds = { base_url: s.base_url, username: s.basic_user, password: s.basic_pass };
  let info = null;
  try {
    info = await invoke("admin_server_info", { creds });
  } catch (e) {
    toast("Could not fetch server info: " + e + ". Re-run admin with --server-config to enable auto-fill.", "error");
  }
  const transports = (info && info.transports) || [];
  const serverPub = (info && info.static_pubkey_b64) || "";

  if (!serverPub || transports.length === 0) {
    // Fall back to the manual composer when the admin process was
    // started without --server-config.
    openShareLinkComposerManual(s, user, privB64);
    return;
  }

  // Pre-build options. Default to reality if available, else first.
  let selected = transports.find((t) => t.type === "reality") || transports[0];

  const renderForm = () => {
    const transportSelect = el("select", {
      onchange: (ev) => {
        const idx = Number(ev.target.value);
        selected = transports[idx];
        // re-render to reveal/hide SNI field as needed
        state.modal._values.transport_idx = String(idx);
        state.modal._values.addr = selected.addr;
        state.modal._values.sni  = selected.sni || "";
        render();
      },
    });
    transports.forEach((t, i) => {
      transportSelect.append(opt(String(i), `${t.type.toUpperCase()} — ${t.addr}`, t === selected));
    });

    const fields = [
      { key: "addr", label: "Server host:port", initial: selected.addr },
    ];
    if (selected.type === "reality" || selected.type === "wss") {
      fields.push({ key: "sni", label: "TLS SNI", initial: selected.sni || "www.microsoft.com" });
    }

    return { transportSelect, fields };
  };

  const { transportSelect, fields } = renderForm();
  openModal({
    title: "Share link",
    body: `Server pubkey, transports and ports auto-filled from ${s.label}. Just pick which transport.`,
    bodyExtra: el("div", { class: "modal-field" },
      el("label", { class: "fieldlabel" }, "Transport"),
      transportSelect,
    ),
    fields,
    submitLabel: "Generate",
    onSubmit: async (vals) => {
      const cfg = {
        Servers: [{
          Type: selected.type,
          Addr: vals.addr || selected.addr,
          SNI: vals.sni || selected.sni || "",
          Insecure: null,
          Path: selected.path || "",
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

// Manual composer kept as a fallback for older servers that don't
// expose /api/server-info.
function openShareLinkComposerManual(s, user, privB64) {
  const probe = state.serverProbes[s.id] || {};
  const serverPub = (probe.dashboard && (probe.dashboard.server_pubkey_b64 || probe.dashboard.static_pubkey_b64)) || "";
  openModal({
    title: "Share link",
    body: "Server's /api/server-info is empty — restart admin with `--server-config /etc/veil/server.yaml --public-host <fqdn>` to enable auto-fill, or fill the fields manually.",
    fields: [
      { key: "server_pubkey", label: "Server static pubkey (base64)", initial: serverPub },
      { key: "addr",          label: "Server host:port",              placeholder: "vps.example.com:443" },
      { key: "transport",     label: "Transport (reality / wss / quic / masque)", initial: "reality" },
      { key: "sni",           label: "TLS SNI (Reality / WSS)",       initial: "www.microsoft.com" },
    ],
    submitLabel: "Generate",
    onSubmit: async (vals) => {
      const cfg = {
        Servers: [{
          Type: vals.transport, Addr: vals.addr,
          SNI: vals.sni || "", Insecure: null, Path: "", Fingerprint: "chrome",
        }],
        ServerStaticKeyB64: vals.server_pubkey,
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

function showLinkModal(user, link) {
  const input = el("input", {
    type: "text",
    readonly: "",
    onclick: (ev) => ev.target.select(),
  });
  input.value = link;
  const copyBtn = el("button", { class: "smallbtn", onclick: () => {
    input.select();
    try { document.execCommand("copy"); toast("Copied.", "success"); }
    catch (e) { toast("Copy failed: select the text and Ctrl-C.", "error"); }
  } }, "Copy");
  openModal({
    title: `Link for ${user.name || user.id}`,
    body: "Click the field to select all, then Ctrl-C — or use the Copy button.",
    bodyExtra: el("div", { style: "display:flex; gap:8px; align-items:center" }, input, copyBtn),
    submitLabel: "Done",
    cancelLabel: null,
    onSubmit: () => {},
  });
  setTimeout(() => input.select(), 50);
}

async function deleteUser(s, u) {
  openModal({
    title: "Delete user",
    body: `Permanently delete "${u.name}"? The user's existing share-link will stop working.`,
    submitLabel: "Delete",
    danger: true,
    onSubmit: async () => {
      const creds = { base_url: s.base_url, username: s.basic_user, password: s.basic_pass };
      await invoke("admin_user_delete", { args: { creds, id: u.id } });
      toast("Deleted.", "info");
      refreshServer(s.id);
    },
  });
}

// --- Deploy index ---

function renderDeployIndex(host) {
  host.append(
    el("h1", { class: "h1" }, "Deploy a server"),
    el("p", { class: "subtitle" }, "Pick the workflow that matches where you want Veil to live."),
    el("div", { class: "choicegrid" },
      choiceCard("Docker", "compose.yaml + bring-up", "deploy-compose"),
      choiceCard("VPS via SSH", "Push veil + systemd unit + start", "deploy-ssh"),
      choiceCard("Edge function", "Deno Deploy / Fly.io worker bundle", "deploy-edge"),
    ),
  );
}

function choiceCard(name, body, view) {
  return el("button", { class: "choice", onclick: () => { state.view = view; render(); } },
    el("div", { class: "name" }, name),
    el("div", { class: "body" }, body),
  );
}

// --- Compose deploy ---

function renderDeployCompose(host) {
  host.append(
    el("h1", { class: "h1" }, "Docker compose"),
    el("p", { class: "subtitle" }, "Generate a docker-compose.yaml ready to scp + ", el("code", {}, "docker compose up"), "."),
    el("div", { class: "card" },
      el("label", { class: "fieldlabel", for: "compose" }, "compose.yaml"),
      el("textarea", {
        id: "compose",
        spellcheck: "false",
        oninput: (ev) => { state.deploy.composeYaml = ev.target.value; },
      }, ""),
      el("div", { class: "row right" },
        el("button", { class: "smallbtn", onclick: () => { state.deploy.composeYaml = defaultComposeYaml(); render(); } }, "Reset"),
        el("button", { class: "primary", onclick: saveCompose }, "Save…"),
      ),
    ),
  );
  // After append the textarea exists; set its value (for first paint
  // since DOM creation doesn't honour textarea innerText for value).
  setTimeout(() => {
    const ta = document.getElementById("compose");
    if (ta) ta.value = state.deploy.composeYaml;
  }, 0);
}

async function saveCompose() {
  try {
    await invoke("save_compose", { content: state.deploy.composeYaml });
    toast("Saved.", "success");
  } catch (e) {
    toast("Save failed: " + e, "error");
  }
}

// --- SSH deploy ---

function renderDeploySSH(host) {
  const ssh = state.deploy.ssh;
  host.append(
    el("h1", { class: "h1" }, "Install via SSH"),
    el("p", { class: "subtitle" }, "Connect to a fresh VPS, push the Veil binary, install systemd unit and bring the service up."),
  );

  host.append(el("div", { class: "card" },
    el("div", { class: "cardtitle" }, "Connection"),
    el("div", { class: "row" },
      sshField("host", "Host or IP", ssh.host, "vps.example.com"),
      sshField("port", "Port",       ssh.port, "22"),
    ),
    el("div", { class: "row" },
      sshField("user", "User",       ssh.user, "root"),
      sshField("password", "Password", ssh.password, "••••••", true),
    ),
    el("div", { class: "row right" },
      el("button", { class: "smallbtn", disabled: ssh.busy, onclick: sshProbe }, "Probe"),
      el("button", { class: "primary",  disabled: ssh.busy, onclick: sshInstall }, "Install"),
    ),
    ssh.probe ? el("pre", { class: "log" }, JSON.stringify(ssh.probe, null, 2)) : null,
  ));

  if (ssh.log.length) {
    host.append(el("div", { class: "card" },
      el("div", { class: "cardtitle" }, "Install log"),
      el("pre", { class: "log" }, ssh.log.join("\n")),
    ));
  }
}

function sshField(key, label, value, placeholder, secret = false) {
  const wrap = el("div", { style: "flex: 1 1 200px" });
  wrap.append(el("label", { class: "fieldlabel" }, label));
  wrap.append(el("input", {
    type: secret ? "password" : "text",
    placeholder, value: value || "",
    oninput: (ev) => { state.deploy.ssh[key] = ev.target.value; },
  }));
  return wrap;
}

async function sshProbe() {
  state.deploy.ssh.busy = true; render();
  try {
    state.deploy.ssh.probe = await invoke("ssh_probe", {
      target: { host: state.deploy.ssh.host, port: parseInt(state.deploy.ssh.port, 10) || 22, user: state.deploy.ssh.user, password: state.deploy.ssh.password },
    });
    toast("Probe ok.", "success");
  } catch (e) {
    toast("Probe failed: " + e, "error");
  } finally {
    state.deploy.ssh.busy = false; render();
  }
}

async function sshInstall() {
  toast("SSH install requires a release-built veil binary path; left as scaffolding for the v0 GUI. Use `veil` CLI from your shell or trigger the SSH workflow from the original installer release.", "info");
}

// --- Edge deploy ---

function renderDeployEdge(host) {
  const e = state.deploy.edge;
  host.append(
    el("h1", { class: "h1" }, "Edge worker"),
    el("p", { class: "subtitle" }, "Generate a deploy bundle for Deno Deploy or Fly.io. Optionally push it directly via a paste-in PAT."),
  );
  host.append(el("div", { class: "card" },
    el("div", { class: "row" },
      el("div", { style: "flex:1" },
        el("label", { class: "fieldlabel" }, "Provider"),
        el("select", { onchange: (ev) => { state.deploy.edge.provider = ev.target.value; render(); } },
          opt("deno", "Deno Deploy", e.provider === "deno"),
          opt("fly",  "Fly.io",       e.provider === "fly"),
        ),
      ),
      el("div", { style: "flex:1" },
        el("label", { class: "fieldlabel" }, "Origin host"),
        el("input", { type: "text", value: e.origin_host, placeholder: "vps.example.com",
          oninput: (ev) => { state.deploy.edge.origin_host = ev.target.value; } }),
      ),
      el("div", { style: "flex:1" },
        el("label", { class: "fieldlabel" }, "Origin port"),
        el("input", { type: "text", value: e.origin_port, placeholder: "443",
          oninput: (ev) => { state.deploy.edge.origin_port = ev.target.value; } }),
      ),
      el("div", { style: "flex:1" },
        el("label", { class: "fieldlabel" }, "Path"),
        el("input", { type: "text", value: e.path, placeholder: "/ws",
          oninput: (ev) => { state.deploy.edge.path = ev.target.value; } }),
      ),
      e.provider === "fly" ? el("div", { style: "flex:1" },
        el("label", { class: "fieldlabel" }, "App name"),
        el("input", { type: "text", value: e.app_name, placeholder: "veil-edge",
          oninput: (ev) => { state.deploy.edge.app_name = ev.target.value; } }),
      ) : null,
    ),
    el("div", { class: "row right" },
      el("button", { class: "smallbtn", onclick: edgeGenerate }, "Generate bundle"),
      el("button", { class: "primary",  onclick: edgeSave, disabled: !e.files }, "Save to folder…"),
    ),
  ));
  if (e.files) {
    const items = Object.keys(e.files).map((name) =>
      el("li", {}, el("code", {}, name), " (", e.files[name].length.toString(), " bytes)"),
    );
    host.append(el("div", { class: "card" },
      el("div", { class: "cardtitle" }, "Files"),
      el("ul", { style: "margin:0; padding-left:18px" }, ...items),
    ));
  }
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
  } catch (err) {
    toast("Generate failed: " + err, "error");
  }
  render();
}

async function edgeSave() {
  try {
    const dir = await invoke("edge_save", { files: state.deploy.edge.files });
    if (dir) toast("Saved to " + dir, "success");
  } catch (err) {
    toast("Save failed: " + err, "error");
  }
}

// --- Settings ---

function renderSettings(host) {
  host.append(
    el("h1", { class: "h1" }, "About"),
    el("div", { class: "card" },
      el("div", { class: "cardtitle" }, "Veil Installer"),
      el("p", { class: "subtitle" },
        "GUI for deploying and managing Veil VPN servers. ",
        "All saved server credentials live in tauri-plugin-store on this machine and never leave it.",
      ),
      el("div", { class: "kv" },
        kvRow("Config", "installer.store.json (per-OS app data dir)"),
        kvRow("Source", el("a", { href: "https://github.com/redstone-md/veil", target: "_blank" }, "github.com/redstone-md/veil")),
      ),
    ),
  );
}

// --- Modal ---

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
        if (ev.key === "Enter") { ev.preventDefault(); submit(); }
      },
    };
    const input = el("input", props);
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
    } catch (e) {
      toast(String(e?.message || e), "error");
      return;
    }
    // If onSubmit opened ANOTHER modal (chained flow — e.g. rotate
    // -> composer -> link), state.modal now points to the new one.
    // Auto-closing here would wipe it; only close when our modal is
    // still on top.
    if (state.modal === m) closeModal();
  };

  const card = el("div", { class: "modal-card" },
    el("div", { class: "modal-title" }, m.title),
    m.body ? el("div", { class: "modal-body" }, m.body) : null,
    m.bodyExtra || null,
    ...fields,
    el("div", { class: "modal-actions" },
      m.cancelLabel === null ? null
        : el("button", { class: "smallbtn", onclick: closeModal }, m.cancelLabel || "Cancel"),
      el("button", { class: m.danger ? "danger" : "primary", onclick: submit }, m.submitLabel || "OK"),
    ),
  );
  return el("div", { class: "modal-backdrop", onclick: (ev) => { if (ev.target === ev.currentTarget) closeModal(); } }, card);
}

// --- toast ---

function toast(text, kind = "info") {
  state.toast = { text, kind };
  render();
  setTimeout(() => {
    if (state.toast?.text === text) { state.toast = null; render(); }
  }, 4500);
}
function toastEl(t) { return el("div", { class: "toast " + t.kind }, t.text); }

// --- helpers ---

async function copyToClipboard(text) {
  try {
    if (navigator.clipboard?.writeText) await navigator.clipboard.writeText(text);
    else throw new Error("clipboard unavailable");
    toast("Copied.", "success");
  } catch (e) {
    toast("Copy failed: " + e, "error");
  }
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
function fmtQuota(n) {
  if (!n) return "unlimited";
  return fmtBytes(n) + "/mo";
}
function prettyKey(k) {
  return k.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
}
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

// --- DOM ---

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
  return [el("div", { class: "k" }, k), el("div", { class: "v" }, v ?? "—")];
}

function opt(value, label, selected) {
  const o = el("option", { value }, label);
  if (selected) o.selected = true;
  return o;
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

bootStore();
