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
    {
        let guard = state.inner.lock().expect("VeilState mutex poisoned");
        if guard.is_some() {
            return Err("a session is already running; stop it first".into());
        }
    }

    let v = Veil::create(&config_text).map_err(|e| format!("create: {e}"))?;
    let app_for_cb = app.clone();
    let cb: EventHandler = Arc::new(move |e: Event| {
        // Drop the result; if the frontend isn't listening yet
        // there's nothing meaningful to do.
        let _ = app_for_cb.emit("veil-event", UiEvent::from(e.clone()));
        notify_event(&app_for_cb, &e);
    });
    v.start(Some(cb)).map_err(|e| format!("start: {e}"))?;

    let mut guard = state.inner.lock().expect("VeilState mutex poisoned");
    *guard = Some(v);
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

/// Check for updates by shelling out to the bundled `veil` CLI.
/// Single-sources the GitHub release query + signature verification
/// in core/internal/update; the desktop UI just renders the result.
#[tauri::command]
async fn check_update() -> Result<UpdateInfo, String> {
    let exe = match veil_cli_path() {
        Some(p) => p,
        None => return Err(
            "Updates unavailable: bundled `veil` CLI was not found next to the app. \
             This is normal for dev builds; release installers ship the CLI alongside the GUI.".into(),
        ),
    };
    let output = std::process::Command::new(&exe)
        .args(["update", "check", "--json"])
        .output()
        .map_err(|e| format!("exec {}: {e}", exe.display()))?;
    if !output.status.success() {
        let stderr = String::from_utf8_lossy(&output.stderr);
        return Err(format!("veil update check failed: {stderr}"));
    }
    let stdout = String::from_utf8_lossy(&output.stdout);
    serde_json::from_str(stdout.trim()).map_err(|e| format!("parse update json: {e}: {stdout}"))
}

#[tauri::command]
async fn apply_update() -> Result<(), String> {
    let exe = veil_cli_path().ok_or_else(|| {
        "Updates unavailable: bundled `veil` CLI was not found next to the app.".to_string()
    })?;
    let output = std::process::Command::new(&exe)
        .args(["update", "apply", "--cosign"])
        .output()
        .map_err(|e| format!("exec {}: {e}", exe.display()))?;
    if !output.status.success() {
        let stderr = String::from_utf8_lossy(&output.stderr);
        return Err(format!("veil update apply failed: {stderr}"));
    }
    Ok(())
}

#[derive(Debug, Serialize, serde::Deserialize)]
struct UpdateInfo {
    current: String,
    latest: String,
    update_available: bool,
}

/// Resolve the bundled `veil` CLI binary. The desktop installer ships
/// it next to the GUI executable; in dev builds neither file is
/// present, in which case we return None so the caller can show the
/// user a friendlier 'Updates unavailable' message instead of the raw
/// 'program not found' error from the OS.
fn veil_cli_path() -> Option<std::path::PathBuf> {
    let exe_dir = std::env::current_exe().ok()?.parent()?.to_path_buf();
    let bundled = exe_dir.join(if cfg!(windows) { "veil.exe" } else { "veil" });
    if bundled.exists() {
        return Some(bundled);
    }
    // Fall back to PATH lookup so a developer with `veil` on $PATH can
    // still exercise the in-app update flow without copying the CLI
    // next to the dev binary.
    let on_path = if cfg!(windows) { "veil.exe" } else { "veil" };
    let path_env = std::env::var_os("PATH")?;
    for dir in std::env::split_paths(&path_env) {
        let candidate = dir.join(on_path);
        if candidate.exists() {
            return Some(candidate);
        }
    }
    None
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

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .manage(VeilState::default())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_notification::init())
        .plugin(tauri_plugin_store::Builder::default().build())
        .plugin(tauri_plugin_autostart::init(
            MacosLauncher::LaunchAgent,
            None,
        ))
        .invoke_handler(tauri::generate_handler![
            veil_start,
            veil_stop,
            veil_metrics_json,
            set_autostart,
            get_autostart,
            check_update,
            apply_update,
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
