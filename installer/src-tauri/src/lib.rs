// Veil Installer — Tauri command surface.
//
// The lib crate exposes the small Rust commands the JS frontend can
// call via `invoke()`. Today this is just `save_compose`, which pops
// a native file dialog and writes the supplied YAML text to disk.
// Subsequent revisions will add commands for SSH installs and
// edge-function OAuth flows.

use std::fs;

use tauri::Manager;
use tauri_plugin_dialog::DialogExt;

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

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_fs::init())
        .plugin(tauri_plugin_shell::init())
        .invoke_handler(tauri::generate_handler![save_compose])
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
