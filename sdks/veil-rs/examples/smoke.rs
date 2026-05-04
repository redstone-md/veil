//! Minimal end-to-end smoke test for the Veil Rust bindings.
//!
//! Build the libveil shared library first:
//!
//!     cd ../../core
//!     CGO_ENABLED=1 go build -buildmode=c-shared \
//!         -o ../sdks/veil-rs/libveil.so ./pkg/cgo
//!
//! Then run:
//!
//!     LD_LIBRARY_PATH=. cargo run --example smoke -- client.yaml
//!
//! On macOS / Windows substitute libveil.dylib / veil.dll and the
//! corresponding loader path variable.

use std::{env, fs, sync::Arc, time::Duration};

fn main() {
    let path = env::args()
        .nth(1)
        .expect("usage: smoke <path/to/client.yaml-or-json>");
    let cfg = fs::read_to_string(&path).expect("read config");

    println!("libveil version: {:?}", veil::Veil::library_version());

    let v = veil::Veil::create(&cfg).expect("create");
    let cb: veil::EventHandler = Arc::new(|e: veil::Event| {
        println!(
            "[event] kind={:?} transport={} remote={} bytes_tx={} bytes_rx={} msg={}",
            e.typed(),
            e.transport,
            e.remote,
            e.bytes_tx,
            e.bytes_rx,
            e.message
        );
    });
    v.start(Some(cb)).expect("start");

    // Hold the instance for a short while so a curl through the
    // SOCKS5 proxy from another shell would observe it.
    std::thread::sleep(Duration::from_secs(15));

    println!("metrics: {:?}", v.metrics());
    v.stop().expect("stop");
    drop(v); // forces destroy + free
    println!("done");
}
