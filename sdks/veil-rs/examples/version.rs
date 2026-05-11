//! Minimal smoke that only calls library_version() — no config or
//! network needed. Confirms the dynamic loader can find libveil and
//! the C ABI is wired up.

fn main() {
    let v = veil::Veil::library_version().expect("library_version");
    println!(
        "libveil: version={} commit={} date={}",
        v.version, v.commit, v.date
    );
}
