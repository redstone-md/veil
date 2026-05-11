// veil-rs build script.
//
// Tells cargo where to find the libveil shared library at link time.
// We assume the consumer (or `cargo run --example smoke` in this
// crate) drops `libveil.so` / `libveil.dylib` / `veil.dll` next to
// `Cargo.toml`. CI / release builds may want to set
// `VEIL_LIBRARY_DIR` to override.
//
// On Windows, MSVC needs an import library (`veil.lib`); generate it
// once with:
//
//     dlltool --input-def veil.def --dllname veil.dll \
//             --output-lib veil.lib
//
// using the .def file shipped alongside this build.rs.

use std::env;
use std::fs;
use std::path::{Path, PathBuf};

fn main() {
    let crate_dir = env::var("CARGO_MANIFEST_DIR").expect("CARGO_MANIFEST_DIR");
    let lib_dir = env::var("VEIL_LIBRARY_DIR").unwrap_or_else(|_| crate_dir.clone());

    println!("cargo:rerun-if-changed=build.rs");
    println!("cargo:rerun-if-env-changed=VEIL_LIBRARY_DIR");
    println!("cargo:rustc-link-search=native={lib_dir}");
    println!("cargo:rustc-link-lib=dylib=veil");

    // Stage the runtime shared library next to the consumer's binary
    // so `target/{debug,release}/foo.exe` finds it without the user
    // having to copy the dll by hand. OUT_DIR is per-crate
    // (`target/{profile}/build/<crate>-<hash>/out`); walk three levels
    // up to land in `target/{profile}/`.
    if let Ok(out_dir) = env::var("OUT_DIR") {
        let target_dir: PathBuf = Path::new(&out_dir)
            .ancestors()
            .nth(3)
            .map(|p| p.to_path_buf())
            .unwrap_or_default();

        let target_os = env::var("CARGO_CFG_TARGET_OS").unwrap_or_default();
        let lib_name = match target_os.as_str() {
            "windows" => "veil.dll",
            "macos" => "libveil.dylib",
            _ => "libveil.so",
        };

        let src = Path::new(&lib_dir).join(lib_name);
        let dst = target_dir.join(lib_name);
        if src.exists() && !target_dir.as_os_str().is_empty() {
            // Best-effort copy: if it fails (file in use, perms), the
            // user will see the same "missing dll" error at runtime
            // and can drop the file by hand.
            let _ = fs::copy(&src, &dst);
            println!("cargo:rerun-if-changed={}", src.display());
        }
    }
}
