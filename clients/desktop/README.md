# Veil — desktop client

A Tauri 2 desktop GUI for the Veil VPN client. Wraps the safe Rust
SDK ([`sdks/veil-rs`](../../sdks/veil-rs)) so the JS frontend can
start / stop a Veil session and observe runtime events without
re-implementing the C ABI.

## Status

Pre-alpha skeleton. Connect / disconnect, status indicator,
traffic counters, paste-in config (veil:// link or YAML) work.

The Tauri host links libveil at runtime via the SDK's dynamic-load
path; the desktop bundle MUST ship `libveil.{so,dylib,dll}`
alongside the executable for the dynamic loader to find it.

## Develop

```bash
cd clients/desktop
npm install
npm run tauri:dev
```

The Vite dev server runs on port 1421; the Tauri window
auto-points there.

For the Veil session to start successfully, libveil has to be
discoverable. The simplest dev setup:

```bash
# Build libveil for your host
cd ../../core
CGO_ENABLED=1 go build -buildmode=c-shared \
  -o ../clients/desktop/src-tauri/libveil.so \
  ./pkg/cgo
# (substitute libveil.dylib on macOS, veil.dll on Windows)

# Now `npm run tauri:dev` from clients/desktop will find it.
```

## Build

```bash
npm run tauri:build
# Artefacts land in src-tauri/target/release/bundle/
```

## Layout

```
clients/desktop/
├── package.json            JS deps + npm scripts
├── vite.config.js          Vite config (port 1421)
├── index.html              SPA entry point
├── src/
│   ├── main.js             SPA logic (status / config / connect / log)
│   └── style.css           styles
└── src-tauri/
    ├── Cargo.toml          links the veil-rs SDK
    ├── tauri.conf.json     window + bundle config
    ├── build.rs
    ├── capabilities/
    └── src/
        ├── main.rs         entry point
        └── lib.rs          #[tauri::command] surface (start/stop/metrics)
```

## What lands in this revision

- Project scaffold (file layout above).
- `veil_start` / `veil_stop` / `veil_metrics_json` Tauri commands
  that drive an in-process `veil::Veil` instance.
- Frontend status panel: status dot, transport label, byte
  counters, last event line.
- Configuration text-area persisting through `localStorage` so a
  return visit does not lose the paste.
- Event-stream wiring: every Veil runtime event is emitted as a
  `"veil-event"` Tauri app-event with the SDK's payload shape.

## What lands next (Phase 4.5+)

- Auto-import for `veil://` share links scanned from a QR code.
- System-tray integration so the client can stay resident without
  an open window.
- "Set this as my system proxy" toggle (per-OS proxy APIs).
- Mobile clients (React Native + NetworkExtension on iOS,
  VpnService on Android).
