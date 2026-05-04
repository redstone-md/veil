# veil — Rust bindings for libveil

Safe, ergonomic Rust wrappers over the Veil VPN client C ABI.

[`libveil`](../../core/pkg/cgo/) is a C-shared library produced from
the Go reference implementation of Veil. This crate adds the Rust
ergonomics on top: RAII handles, typed error enums, structured event
payloads, closure-based callbacks.

## Status

Pre-alpha. The ABI is v1; minor crate releases are append-only.

## Build the native library first

The crate links dynamically against `libveil.{so,dylib,dll}`. Build
that one binary out of the upstream `core/` tree:

```bash
cd <repo>/core
CGO_ENABLED=1 go build \
  -buildmode=c-shared \
  -o ../sdks/veil-rs/libveil.so \
  ./pkg/cgo
```

(Substitute `.dylib` on macOS, `.dll` on Windows.)

## Use the crate

```toml
[dependencies]
veil = { path = "../sdks/veil-rs" }
```

```rust
use std::{fs, sync::Arc};

fn main() -> Result<(), veil::Error> {
    let cfg = fs::read_to_string("client.yaml")?;
    let v = veil::Veil::create(&cfg)?;

    let cb: veil::EventHandler = Arc::new(|e| {
        println!("event: {:?} bytes_tx={} bytes_rx={}",
                 e.typed(), e.bytes_tx, e.bytes_rx);
    });
    v.start(Some(cb))?;

    std::thread::sleep(std::time::Duration::from_secs(60));

    println!("{:?}", v.metrics()?);
    v.stop()?;
    Ok(())
}
```

The `examples/smoke.rs` example is a complete runnable version of
the snippet above.

## ABI promises

* The numeric values in `LibCode` and `EventType` are part of the
  ABI; never renumber, only append.
* Strings emitted by libveil are copied into Rust-owned `String`s
  before the FFI buffer is freed. Consumers do not need to think
  about lifetimes.
* The event callback runs on a Veil-internal thread. Don't block in
  it; marshal into your own runtime.

## License

Apache-2.0, same as the upstream project.
