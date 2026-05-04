// Veil Installer — Tauri command surface.
//
// The lib crate exposes the Rust commands the JS frontend can call
// via `invoke()`. v0 ships:
//
//   save_compose   — pop a native file dialog and write a YAML blob.
//   ssh_probe      — connect, run `uname -a`, return capture.
//   ssh_install    — full bring-up: upload binary + write config +
//                    write systemd unit + enable + tail logs.
//
// All async commands return Result<_, String> because Tauri requires
// the error type to be Serialize; anyhow::Error isn't, so we convert
// at the boundary.

use std::fs;

use tauri::Manager;
use tauri_plugin_dialog::DialogExt;

mod ssh;

#[tauri::command]
async fn save_compose(app: tauri::AppHandle, content: String) -> Result<(), String> {
    let (tx, rx) = std::sync::mpsc::channel::<Option<std::path::PathBuf>>();
    let dialog = app
        .dialog()
        .file()
        .set_title("Save Veil compose.yaml")
        .add_filter("YAML", &["yaml", "yml"])
        .set_file_name("compose.yaml");
    dialog.save_file(move |path| {
        let _ = tx.send(path.and_then(|p| p.into_path().ok()));
    });
    match rx.recv().map_err(|e| e.to_string())? {
        Some(path) => fs::write(&path, content).map_err(|e| e.to_string()),
        None => Ok(()),
    }
}

#[tauri::command]
async fn ssh_probe(target: ssh::SshTarget) -> Result<ssh::ExecResult, String> {
    ssh::run_one(
        &target,
        "uname -m && cat /etc/os-release 2>/dev/null | head -3 && df -h / | tail -1",
    )
    .await
    .map_err(|e| format!("{e:#}"))
}

#[tauri::command]
async fn ssh_install(plan: ssh::InstallPlan) -> Result<Vec<ssh::InstallStep>, String> {
    ssh::install(plan).await.map_err(|e| format!("{e:#}"))
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_fs::init())
        .plugin(tauri_plugin_shell::init())
        .invoke_handler(tauri::generate_handler![
            save_compose,
            ssh_probe,
            ssh_install
        ])
        .setup(|app| {
            #[cfg(debug_assertions)]
            {
                let window = app.get_webview_window("main").unwrap();
                window.open_devtools();
            }
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
