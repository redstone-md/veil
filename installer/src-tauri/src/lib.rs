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

use std::collections::BTreeMap;
use std::fs;

use serde::Deserialize;
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

/// Edge worker generation parameters supplied by the GUI.
#[derive(Debug, Deserialize)]
struct EdgeParams {
    /// "deno" or "fly".
    provider: String,
    origin_host: String,
    #[serde(default)]
    origin_port: Option<u16>,
    #[serde(default)]
    path: Option<String>,
    /// Fly-only: app name (becomes `app =` in fly.toml).
    #[serde(default)]
    app_name: Option<String>,
}

/// Returns a map of filename -> file contents the operator should
/// drop into a directory and deploy with their provider's CLI.
///
/// We intentionally do NOT push directly to the provider's API in
/// v0; doing so would require the operator to paste a long-lived
/// PAT and the GUI to act as the deploy frontend, both of which
/// expand the trust surface beyond the threat model. The next
/// revision adds full OAuth flows for both providers.
#[tauri::command]
async fn edge_generate(params: EdgeParams) -> Result<BTreeMap<String, String>, String> {
    let port = params.origin_port.unwrap_or(443);
    let path = params.path.unwrap_or_else(|| "/ws".to_string());
    let path = if path.starts_with('/') {
        path
    } else {
        format!("/{path}")
    };

    let mut out = BTreeMap::new();
    match params.provider.as_str() {
        "deno" => {
            let worker = include_str!("../../../deploy/edge/deno/worker.ts");
            let deno_json = include_str!("../../../deploy/edge/deno/deno.json");
            let env_doc = format!(
                "# Bring-up:\n#   deployctl deploy --project=YOUR_PROJECT --prod \\\n#     --env=VEIL_ORIGIN_HOST={host} \\\n#     --env=VEIL_ORIGIN_PORT={port} \\\n#     --env=VEIL_PATH={path} \\\n#     worker.ts\n",
                host = params.origin_host,
                port = port,
                path = path,
            );
            out.insert("worker.ts".to_string(), worker.to_string());
            out.insert("deno.json".to_string(), deno_json.to_string());
            out.insert("DEPLOY.md".to_string(), env_doc);
        }
        "fly" => {
            let main_go = include_str!("../../../deploy/edge/fly/main.go");
            let go_mod = include_str!("../../../deploy/edge/fly/go.mod");
            let dockerfile = include_str!("../../../deploy/edge/fly/Dockerfile");
            let app = params
                .app_name
                .clone()
                .unwrap_or_else(|| "veil-edge".to_string());
            let fly_toml = format!(
                "app = \"{app}\"\nprimary_region = \"fra\"\n\n[build]\n  dockerfile = \"Dockerfile\"\n\n[env]\n  VEIL_LISTEN = \":8080\"\n  VEIL_ORIGIN_HOST = \"{host}\"\n  VEIL_ORIGIN_PORT = \"{port}\"\n  VEIL_PATH = \"{path}\"\n\n[http_service]\n  internal_port = 8080\n  force_https = true\n  auto_stop_machines = \"stop\"\n  auto_start_machines = true\n  min_machines_running = 0\n\n[[vm]]\n  cpu_kind = \"shared\"\n  cpus = 1\n  memory_mb = 256\n",
                app = app,
                host = params.origin_host,
                port = port,
                path = path,
            );
            let deploy_md = format!(
                "# Bring-up:\n#   fly apps create {app}\n#   fly deploy --app {app}\n",
                app = app
            );
            out.insert("main.go".to_string(), main_go.to_string());
            out.insert("go.mod".to_string(), go_mod.to_string());
            out.insert("Dockerfile".to_string(), dockerfile.to_string());
            out.insert("fly.toml".to_string(), fly_toml);
            out.insert("DEPLOY.md".to_string(), deploy_md);
        }
        other => return Err(format!("unknown provider {other:?}")),
    }
    Ok(out)
}

/// Save a generated edge bundle into a directory chosen by the
/// operator via a native folder dialog.
#[tauri::command]
async fn edge_save(
    app: tauri::AppHandle,
    files: BTreeMap<String, String>,
) -> Result<String, String> {
    let (tx, rx) = std::sync::mpsc::channel::<Option<std::path::PathBuf>>();
    app.dialog()
        .file()
        .set_title("Choose a folder to write the edge worker into")
        .pick_folder(move |path| {
            let _ = tx.send(path.and_then(|p| p.into_path().ok()));
        });
    let dir = match rx.recv().map_err(|e| e.to_string())? {
        Some(p) => p,
        None => return Ok(String::new()),
    };
    fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    for (name, contents) in files {
        let p = dir.join(name);
        fs::write(&p, contents).map_err(|e| e.to_string())?;
    }
    Ok(dir.display().to_string())
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
            ssh_install,
            edge_generate,
            edge_save,
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
