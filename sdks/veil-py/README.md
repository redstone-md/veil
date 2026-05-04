# veil — Python bindings for libveil

A pure-Python ctypes wrapper around the Veil VPN client C ABI.

## Install (local development)

```bash
pip install -e .
```

## Build the native library first

The package loads `libveil.{so,dylib,dll}` from the directory it
imported `veil` from. Build it from the upstream `core/` tree:

```bash
cd <repo>/core
CGO_ENABLED=1 go build \
  -buildmode=c-shared \
  -o ../sdks/veil-py/libveil.so \
  ./pkg/cgo
```

(Substitute `.dylib` on macOS, `veil.dll` on Windows.)

You can also point Python at an arbitrary path with the
`VEIL_LIBRARY` environment variable.

## Quick start

```python
import time
import veil

cfg = open("client.yaml").read()
with veil.Veil(cfg) as v:
    v.start(on_event=lambda e: print(e))
    time.sleep(60)
    print(v.metrics())
```

`examples/smoke.py` is a runnable version of the snippet above.

## API surface

- `veil.Veil(cfg_text)` — construct a client; raises `VeilError`
  on bad config.
- `Veil.start(on_event=None)` — bring the client up. The optional
  callback receives `Event` instances on a Veil-internal thread
  (don't block in it).
- `Veil.stop()` — request graceful shutdown.
- `Veil.metrics()` — `Metrics(running, bytes_tx, bytes_rx)`.
- `veil.library_version()` — `Version(version, commit, date)`.

`Veil` is also a context manager; `__exit__` calls `close()`,
which destroys the underlying handle.

## License

Apache-2.0, same as the upstream project.
