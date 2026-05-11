fn main() {
    tauri_build::build();

    // On Windows, stage vendored wintun.dll next to the consumer's
    // binary so the System TUN mode loads it without the user having
    // to download wintun.net themselves. Tauri's bundle.resources
    // takes care of installer payloads; this copy keeps the
    // standalone target/{profile}/veil-desktop.exe runnable too.
    #[cfg(target_os = "windows")]
    {
        use std::path::{Path, PathBuf};
        use std::{env, fs};

        let crate_dir = env::var("CARGO_MANIFEST_DIR").unwrap();
        let src = Path::new(&crate_dir).join("vendor").join("wintun.dll");
        if src.exists() {
            if let Ok(out_dir) = env::var("OUT_DIR") {
                let target_dir: PathBuf = Path::new(&out_dir)
                    .ancestors()
                    .nth(3)
                    .map(|p| p.to_path_buf())
                    .unwrap_or_default();
                if !target_dir.as_os_str().is_empty() {
                    let _ = fs::copy(&src, target_dir.join("wintun.dll"));
                }
            }
            println!("cargo:rerun-if-changed={}", src.display());
        }
    }
}
