// Veil desktop client — Tauri command surface.
//
// Wraps the safe Rust SDK (`veil` crate from sdks/veil-rs) so the
// JS frontend can drive a Veil session through #[tauri::command]
// handlers and observe runtime via the "veil-event" app-event.
//
// libveil itself is NOT linked into this binary directly: the Rust
// SDK does the FFI call into the shared library that ships next to
// the desktop app. That keeps codesigning + auto-update concerns
// scoped to the libveil bundle and prevents the Tauri host from
// having to re-implement the C ABI.
//
// Phase 4.7 polish: system tray, OS notifications, autostart toggle,
// in-app update check (delegated to the bundled `veil` CLI binary
// via shell exec — keeps update logic single-sourced in core/).

use std::sync::{Arc, Mutex};

use serde::Serialize;
use tauri::{
    menu::{Menu, MenuItem, PredefinedMenuItem},
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
    AppHandle, Emitter, Manager, State,
};
use tauri_plugin_autostart::{ManagerExt as AutostartManagerExt, MacosLauncher};
use tauri_plugin_notification::NotificationExt;
use veil::{Event, EventHandler, Veil};

#[cfg(windows)]
mod tun;

/// Per-process Veil instance. We hold one at a time; starting a new
/// session while another is live returns an error rather than
/// silently leaking the previous one.
#[derive(Default)]
struct VeilState {
    inner: Mutex<Option<Veil>>,
}

/// JSON shape we forward to the frontend. Mirrors the SDK's Event
/// struct field-for-field so the JS side already knows how to
/// decode it.
#[derive(Debug, Serialize, Clone)]
struct UiEvent {
    #[serde(rename = "type")]
    kind: i32,
    message: String,
    transport: String,
    remote: String,
    bytes_tx: i64,
    bytes_rx: i64,
}

impl From<Event> for UiEvent {
    fn from(e: Event) -> Self {
        UiEvent {
            kind: e.kind,
            message: e.message,
            transport: e.transport,
            remote: e.remote,
            bytes_tx: e.bytes_tx,
            bytes_rx: e.bytes_rx,
        }
    }
}

#[tauri::command]
async fn veil_start(
    app: AppHandle,
    state: State<'_, VeilState>,
    config_text: String,
) -> Result<(), String> {
    // If a previous session is still parked in the state slot, tear
    // it down before we install the new one. Treating connect as
    // "reconnect" matches what users expect from the Connect button
    // and prevents the UI from getting stuck on stale state when an
    // earlier session ended without a clean disconnect event.
    {
        let mut guard = state.inner.lock().expect("VeilState mutex poisoned");
        if let Some(prev) = guard.take() {
            let _ = prev.stop();
            drop(prev);
        }
    }

    // Build the Veil and IMMEDIATELY install it into the state slot
    // before calling start. The veil-rs SDK records `&self as *const
    // Veil` as the C-side user_data; if we call start while v lives
    // on the async-fn stack and only move v into state afterwards,
    // the user_data pointer is left dangling at the moved-from
    // location and the first event callback corrupts memory. Putting
    // v into the Mutex first means start() borrows from a stable
    // location.
    let v = Veil::create(&config_text).map_err(|e| format!("create: {e}"))?;
    let mut guard = state.inner.lock().expect("VeilState mutex poisoned");
    *guard = Some(v);
    let v_ref = guard.as_ref().expect("just inserted");

    let app_for_cb = app.clone();
    // The callback fires from a libveil-internal goroutine. Anything
    // that touches Tauri (emit) or the OS (notification toast) MUST
    // be hopped onto an async task — calling them synchronously from
    // the goroutine wedges Go's scheduler if the OS-side blocks even
    // briefly (Windows Toast init is the worst offender).
    let cb: EventHandler = Arc::new(move |e: Event| {
        let app = app_for_cb.clone();
        tauri::async_runtime::spawn(async move {
            let _ = app.emit("veil-event", UiEvent::from(e.clone()));
            notify_event(&app, &e);
        });
    });
    v_ref.start(Some(cb)).map_err(|e| format!("start: {e}"))?;
    drop(guard);
    spawn_metrics_poller(app.clone());
    Ok(())
}

#[tauri::command]
async fn veil_stop(state: State<'_, VeilState>) -> Result<(), String> {
    let v = {
        let mut guard = state.inner.lock().expect("VeilState mutex poisoned");
        guard.take()
    };
    if let Some(v) = v {
        let _ = v.stop();
        // dropping the Veil triggers veil_destroy in the SDK;
        // explicit drop here for clarity.
        drop(v);
    }
    Ok(())
}

/// Spawn a background poller that pulls Veil::metrics() every 250ms
/// and emits a synthetic Traffic event whenever the byte counters
/// change. libveil's own trafficTicker only fires at 1 Hz, which
/// makes the throughput chart laggy under bursty load. Pulling the
/// metrics directly from the SDK (which reads atomic counters) gives
/// the UI sub-second resolution.
///
/// Exits when no Veil instance is parked in either VeilState or the
/// (Windows-only) TunState slot — i.e. after a clean disconnect.
fn spawn_metrics_poller(app: AppHandle) {
    use std::time::Duration;
    tauri::async_runtime::spawn(async move {
        let mut last: (i64, i64) = (0, 0);
        let mut idle_ticks: u32 = 0;
        loop {
            tokio::time::sleep(Duration::from_millis(250)).await;
            let snapshot = read_metrics(&app);
            let m = match snapshot {
                Some(m) => m,
                None => return, // no active session — caller must respawn on next start
            };
            if (m.bytes_tx, m.bytes_rx) != last {
                last = (m.bytes_tx, m.bytes_rx);
                idle_ticks = 0;
                let _ = app.emit("veil-event", UiEvent {
                    kind: 4, // Traffic
                    message: String::new(),
                    transport: String::new(),
                    remote: String::new(),
                    bytes_tx: m.bytes_tx,
                    bytes_rx: m.bytes_rx,
                });
            } else {
                // No change — back off a bit so an idle tunnel doesn't
                // burn CPU on lock-acquire spam, but still emit a
                // periodic Traffic event so the chart's "0 B/s"
                // baseline is fresh.
                idle_ticks = idle_ticks.saturating_add(1);
                if idle_ticks % 4 == 0 {
                    let _ = app.emit("veil-event", UiEvent {
                        kind: 4,
                        message: String::new(),
                        transport: String::new(),
                        remote: String::new(),
                        bytes_tx: m.bytes_tx,
                        bytes_rx: m.bytes_rx,
                    });
                }
            }
        }
    });
}

fn read_metrics(app: &AppHandle) -> Option<veil::Metrics> {
    {
        let vs = app.state::<VeilState>();
        let g = vs.inner.lock().expect("VeilState mutex poisoned");
        if let Some(v) = g.as_ref() {
            return v.metrics().ok();
        }
    }
    #[cfg(windows)]
    {
        let ts = app.state::<tun::TunState>();
        let g = ts.inner.lock().expect("TunState mutex poisoned");
        if let Some(s) = g.as_ref() {
            return s.veil.metrics().ok();
        }
    }
    None
}

#[tauri::command]
async fn veil_metrics_json(state: State<'_, VeilState>) -> Result<String, String> {
    let guard = state.inner.lock().expect("VeilState mutex poisoned");
    match guard.as_ref() {
        Some(v) => v
            .metrics()
            .map(|m| serde_json::to_string(&m).unwrap_or_default())
            .map_err(|e| format!("metrics: {e}")),
        None => Ok(r#"{"running":false}"#.into()),
    }
}

/// Toggle launch-at-login through tauri-plugin-autostart. The plugin
/// abstracts over the OS-specific mechanism: LSSharedFileList on
/// macOS, ~/.config/autostart on Linux, registry Run key on Windows.
#[tauri::command]
async fn set_autostart(app: AppHandle, enabled: bool) -> Result<(), String> {
    let manager = app.autolaunch();
    if enabled {
        manager.enable().map_err(|e| format!("autostart enable: {e}"))?;
    } else {
        manager.disable().map_err(|e| format!("autostart disable: {e}"))?;
    }
    Ok(())
}

#[tauri::command]
async fn get_autostart(app: AppHandle) -> Result<bool, String> {
    app.autolaunch()
        .is_enabled()
        .map_err(|e| format!("autostart query: {e}"))
}

/// Check for updates against the configured updater endpoint.
///
/// Routed through tauri-plugin-updater so signature verification and
/// platform-target selection live in the well-audited plugin rather
/// than in our shelling-to-CLI legacy. The GitHub release manifest is
/// served from the latest release's `latest.json` asset (see
/// release.yml's `manifest` job).
///
/// Returns `update_available=false` when we're already on the latest
/// version. Returns an Err when the network probe fails or the
/// signature can't be verified — those are user-actionable and should
/// surface as a toast.
#[tauri::command]
async fn check_update(app: AppHandle) -> Result<UpdateInfo, String> {
    use tauri_plugin_updater::UpdaterExt;
    let current = app.package_info().version.to_string();
    match app.updater().map_err(|e| e.to_string())?.check().await {
        Ok(Some(u)) => Ok(UpdateInfo {
            current,
            latest: u.version.clone(),
            update_available: true,
            notes: u.body.clone().unwrap_or_default(),
        }),
        Ok(None) => Ok(UpdateInfo {
            current: current.clone(),
            latest: current,
            update_available: false,
            notes: String::new(),
        }),
        Err(e) => Err(format!("update check: {e}")),
    }
}

/// Apply the available update.
///
/// Downloads the platform-matching binary from the manifest-listed
/// URL, verifies the Ed25519 signature against the embedded pubkey
/// (see tauri.conf.json:plugins.updater.pubkey), and replaces the
/// running binary on relaunch. Emits "update-progress" events at
/// each chunk and "update-event" {kind:"finished"} once the install
/// step succeeds — the frontend uses both to drive a progress bar
/// and the post-install relaunch prompt.
#[tauri::command]
async fn apply_update(app: AppHandle) -> Result<(), String> {
    use tauri_plugin_updater::UpdaterExt;
    let updater = app.updater().map_err(|e| e.to_string())?;
    let upd = match updater.check().await.map_err(|e| e.to_string())? {
        Some(u) => u,
        None => return Err("No update available.".into()),
    };
    let app_for_progress = app.clone();
    let mut downloaded: u64 = 0;
    upd.download_and_install(
        move |chunk_len, total| {
            downloaded = downloaded.saturating_add(chunk_len as u64);
            let _ = app_for_progress.emit(
                "update-progress",
                UpdateProgress {
                    downloaded,
                    total: total.unwrap_or(0),
                },
            );
        },
        || {
            // Tauri fires this once the bytes are on disk and the
            // installer step has handed control back. Frontend uses
            // it to flip the modal into "relaunch" state.
            let _ = app.emit("update-event", serde_json::json!({"kind": "finished"}));
        },
    )
    .await
    .map_err(|e| format!("update install: {e}"))?;
    Ok(())
}

#[derive(Debug, Serialize, Clone)]
struct UpdateProgress {
    /// Bytes downloaded so far.
    downloaded: u64,
    /// Total bytes the manifest advertised; 0 when the server didn't
    /// send Content-Length (frontend should fall back to indeterminate
    /// progress in that case).
    total: u64,
}

/// Build a `Command` that won't pop a console window on Windows. On
/// other platforms this is a plain `Command::new`.
fn silent_command<P: AsRef<std::ffi::OsStr>>(program: P) -> std::process::Command {
    let cmd = std::process::Command::new(program);
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        const CREATE_NO_WINDOW: u32 = 0x0800_0000;
        let mut cmd = cmd;
        cmd.creation_flags(CREATE_NO_WINDOW);
        return cmd;
    }
    #[cfg(not(windows))]
    cmd
}

#[derive(Debug, Serialize, serde::Deserialize)]
struct UpdateInfo {
    current: String,
    latest: String,
    update_available: bool,
    /// Release notes from the updater manifest. Markdown — frontend
    /// renders it inside the update prompt modal so the user knows
    /// what they're agreeing to install.
    #[serde(default)]
    notes: String,
}

/// Surface select runtime events as OS notifications. We deliberately
/// skip the high-frequency Traffic events (kind=4) so the user is not
/// drowned in popups; only Connected / Disconnected / Error / Switch
/// produce a notification.
fn notify_event(app: &AppHandle, e: &Event) {
    let (title, body) = match e.kind {
        1 => ("Veil connected", format!("via {} → {}", e.transport, e.remote)),
        2 => ("Veil disconnected", e.message.clone()),
        3 => ("Veil error", e.message.clone()),
        5 => ("Veil transport switch", format!("now using {}", e.transport)),
        _ => return,
    };
    let _ = app
        .notification()
        .builder()
        .title(title)
        .body(body)
        .show();
}

/// Build the system tray. Icon, click-to-show, right-click menu with
/// Connect / Disconnect / Show / Quit. The Connect / Disconnect items
/// emit frontend events the UI handles like a click on the in-window
/// buttons; this keeps the tray and the window agreeing on state.
fn build_tray(app: &AppHandle) -> tauri::Result<()> {
    let connect = MenuItem::with_id(app, "tray_connect", "Connect", true, None::<&str>)?;
    let disconnect = MenuItem::with_id(app, "tray_disconnect", "Disconnect", true, None::<&str>)?;
    let show = MenuItem::with_id(app, "tray_show", "Show window", true, None::<&str>)?;
    let sep = PredefinedMenuItem::separator(app)?;
    let quit = MenuItem::with_id(app, "tray_quit", "Quit", true, None::<&str>)?;
    let menu = Menu::with_items(app, &[&connect, &disconnect, &show, &sep, &quit])?;

    let _tray = TrayIconBuilder::with_id("main-tray")
        .icon(app.default_window_icon().cloned().unwrap_or_else(|| {
            // Fallback to a transparent 1x1 if the bundle lost its
            // icon — we'd rather render an invisible tray than crash.
            tauri::image::Image::new_owned(vec![0; 4], 1, 1)
        }))
        .tooltip("Veil VPN")
        .menu(&menu)
        .show_menu_on_left_click(false)
        .on_menu_event(|app, event| match event.id.as_ref() {
            "tray_connect" => {
                let _ = app.emit("tray-action", "connect");
                show_main_window(app);
            }
            "tray_disconnect" => {
                let _ = app.emit("tray-action", "disconnect");
            }
            "tray_show" => show_main_window(app),
            "tray_quit" => {
                app.exit(0);
            }
            _ => {}
        })
        .on_tray_icon_event(|tray, event| {
            if let TrayIconEvent::Click {
                button: MouseButton::Left,
                button_state: MouseButtonState::Up,
                ..
            } = event
            {
                show_main_window(tray.app_handle());
            }
        })
        .build(app)?;
    Ok(())
}

fn show_main_window(app: &AppHandle) {
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.show();
        let _ = w.unminimize();
        let _ = w.set_focus();
    }
}

// --- Self-elevation -------------------------------------------------
//
// TUN mode needs admin/root. Rather than tell the user "right-click
// → Run as administrator", we pop the platform's native consent
// dialog (UAC on Windows, PolicyKit/sudo on Linux) when they click
// Connect with TUN selected. On confirm, a fresh elevated instance
// of veil-desktop launches and the unprivileged one exits cleanly —
// profile + settings state survive via tauri-plugin-store on disk.

#[derive(Debug, Serialize)]
struct ElevationStatus {
    elevated: bool,
    /// True if we can pop a native dialog to elevate ourselves.
    /// False on platforms where the user has to do it manually.
    can_request: bool,
}

#[tauri::command]
async fn elevation_status() -> Result<ElevationStatus, String> {
    Ok(ElevationStatus {
        elevated: is_currently_elevated(),
        can_request: cfg!(any(target_os = "windows", target_os = "linux")),
    })
}

#[tauri::command]
async fn request_elevation(app: AppHandle) -> Result<(), String> {
    let exe = std::env::current_exe()
        .map_err(|e| format!("locate self: {e}"))?;

    #[cfg(windows)]
    {
        // PowerShell's Start-Process -Verb RunAs triggers the UAC
        // consent dialog. The new process inherits no environment
        // from us; it reads the same on-disk profile store and
        // resumes exactly where the user left off.
        let exe_str = exe.display().to_string().replace('\'', "''");
        let script = format!("Start-Process -FilePath '{exe_str}' -Verb RunAs");
        let status = silent_command("powershell")
            .args(["-NoProfile", "-NonInteractive", "-Command", &script])
            .status()
            .map_err(|e| format!("spawn UAC: {e}"))?;
        if !status.success() {
            return Err("user declined the UAC prompt".into());
        }
    }
    #[cfg(target_os = "linux")]
    {
        // Try pkexec first — it pops a graphical PolicyKit prompt
        // that integrates with whichever DE the user runs (GNOME,
        // KDE, etc). Fall back to the SUDO_ASKPASS pattern only if
        // pkexec is missing; on a headless VM neither exists and we
        // bubble the error so the UI can show the manual-run hint.
        let exe_str = exe.display().to_string();
        let pkexec_ok = std::process::Command::new("pkexec")
            .arg(&exe_str)
            .spawn()
            .is_ok();
        if !pkexec_ok {
            // Try sudo with a graphical askpass if available.
            let askpass = std::env::var("SUDO_ASKPASS").unwrap_or_default();
            if askpass.is_empty() {
                return Err(
                    "Install policykit-1 (pkexec) or run Veil from a terminal with sudo. \
                     Without a graphical privilege helper we can't elevate ourselves."
                        .into(),
                );
            }
            std::process::Command::new("sudo")
                .args(["-A", &exe_str])
                .spawn()
                .map_err(|e| format!("sudo askpass: {e}"))?;
        }
    }
    #[cfg(not(any(target_os = "windows", target_os = "linux")))]
    {
        return Err("self-elevation not implemented on this platform yet".into());
    }

    // Hand control to the freshly-spawned elevated copy. Brief delay
    // so the new process's window has a chance to materialise before
    // ours disappears; keeps the user from seeing nothing for a beat.
    let app_clone = app.clone();
    tauri::async_runtime::spawn(async move {
        tokio::time::sleep(std::time::Duration::from_millis(400)).await;
        app_clone.exit(0);
    });
    Ok(())
}

// is_currently_elevated probes whether *this* process has admin/root.
// Win: re-uses the tun module's `net session` probe. Linux: parses
// `id -u` so we don't need a libc dep just for one syscall. macOS:
// stubbed — TUN mode is Windows-only today, the macOS port lives in
// a follow-up PR.
#[cfg(windows)]
fn is_currently_elevated() -> bool { tun::is_elevated_pub() }
#[cfg(target_os = "linux")]
fn is_currently_elevated() -> bool {
    let out = match std::process::Command::new("id").arg("-u").output() {
        Ok(o) => o,
        Err(_) => return false,
    };
    String::from_utf8_lossy(&out.stdout).trim() == "0"
}
#[cfg(not(any(windows, target_os = "linux")))]
fn is_currently_elevated() -> bool { false }

// --- TUN (Windows / Wintun) commands -------------------------------

#[cfg(windows)]
#[tauri::command]
async fn tun_start(
    app: AppHandle,
    tun_state: State<'_, tun::TunState>,
    state: State<'_, VeilState>,
    args: tun::TunStartArgs,
) -> Result<tun::TunStatus, String> {
    // Stash any prior SOCKS5-mode Veil first.
    {
        let mut guard = state.inner.lock().expect("VeilState mutex poisoned");
        if let Some(prev) = guard.take() {
            let _ = prev.stop();
            drop(prev);
        }
    }
    // Stash any prior TUN session.
    {
        let mut guard = tun_state.inner.lock().expect("TunState mutex poisoned");
        if let Some(prev) = guard.take() {
            let _ = tun::tun_stop(prev);
        }
    }
    // Bring-up does multiple seconds of synchronous work (netsh,
    // route, Wintun adapter create). Run it on the blocking pool so
    // the async runtime worker stays free and "tun-progress" events
    // we emit from the worker reach the UI in real time.
    let app_for_blocking = app.clone();
    let (status, session) = tauri::async_runtime::spawn_blocking(move || {
        tun::tun_start(&app_for_blocking, args)
    })
    .await
    .map_err(|e| format!("tun_start join: {e}"))??;
    *tun_state.inner.lock().expect("TunState mutex poisoned") = Some(session);
    spawn_metrics_poller(app.clone());
    Ok(status)
}

#[cfg(windows)]
#[tauri::command]
async fn tun_stop(tun_state: State<'_, tun::TunState>) -> Result<(), String> {
    let session = {
        let mut guard = tun_state.inner.lock().expect("TunState mutex poisoned");
        guard.take()
    };
    if let Some(session) = session {
        tun::tun_stop(session)?;
    }
    Ok(())
}

#[cfg(not(windows))]
#[tauri::command]
async fn tun_start(_state: State<'_, VeilState>, _args: serde_json::Value) -> Result<serde_json::Value, String> {
    Err("TUN mode is currently Windows-only (macOS / Linux land in a follow-up).".into())
}

#[cfg(not(windows))]
#[tauri::command]
async fn tun_stop(_state: State<'_, VeilState>) -> Result<(), String> {
    Ok(())
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let builder = tauri::Builder::default()
        .manage(VeilState::default());

    #[cfg(windows)]
    let builder = builder.manage(tun::TunState::default());

    builder
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_notification::init())
        .plugin(tauri_plugin_store::Builder::default().build())
        .plugin(tauri_plugin_autostart::init(
            MacosLauncher::LaunchAgent,
            None,
        ))
        // Auto-update plumbing. Endpoint + pubkey come from
        // tauri.conf.json; `process` exposes app::relaunch() so the
        // frontend can reload the new binary after install completes.
        .plugin(tauri_plugin_updater::Builder::new().build())
        .plugin(tauri_plugin_process::init())
        .invoke_handler(tauri::generate_handler![
            veil_start,
            veil_stop,
            veil_metrics_json,
            set_autostart,
            get_autostart,
            check_update,
            apply_update,
            tun_start,
            tun_stop,
            elevation_status,
            request_elevation,
        ])
        .on_window_event(|window, event| {
            // Closing the main window minimises to tray instead of
            // quitting; the user has to choose Quit from the tray
            // menu (or kill the process) to exit. This matches the
            // ergonomics every other VPN client ships.
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                if window.label() == "main" {
                    let _ = window.hide();
                    api.prevent_close();
                }
            }
        })
        .setup(|app| {
            build_tray(app.handle())?;
            #[cfg(debug_assertions)]
            {
                if let Some(window) = app.get_webview_window("main") {
                    window.open_devtools();
                }
            }
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
