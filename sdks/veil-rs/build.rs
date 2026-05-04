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

fn main() {
    let crate_dir = env::var("CARGO_MANIFEST_DIR").expect("CARGO_MANIFEST_DIR");
    let lib_dir = env::var("VEIL_LIBRARY_DIR").unwrap_or(crate_dir);

    println!("cargo:rerun-if-changed=build.rs");
    println!("cargo:rerun-if-env-changed=VEIL_LIBRARY_DIR");
    println!("cargo:rustc-link-search=native={lib_dir}");
    println!("cargo:rustc-link-lib=dylib=veil");
}
