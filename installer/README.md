# Veil Installer

A Tauri v2 desktop application that walks an operator through
deploying a Veil VPN server. The frontend is a small vanilla
JavaScript SPA built with Vite; the host is a Rust binary using
Tauri 2.

## Status

Pre-alpha skeleton. Only the **Docker compose generator** workflow
is fully implemented; SSH and Edge-function paths surface a
"coming soon" placeholder.

The skeleton is laid out so contributors can extend the SSH and
Edge workflows without rearranging the project shell.

## Prerequisites

- Rust 1.77+ with the `cargo` toolchain.
- Node.js 20+ with `npm`.
- Tauri 2 platform prerequisites: see
  https://tauri.app/start/prerequisites/.
  - Windows: Microsoft Edge WebView2 runtime.
  - macOS: Xcode CLT.
  - Linux: webkit2gtk + assorted -dev packages.

## Develop

```bash
cd installer
npm install
npm run tauri:dev      # opens the desktop window
```

The Vite dev server runs on port 1420; the Tauri window
points at it via `tauri.conf.json`.

If you want to iterate on the JS only (no Tauri host), run
`npm run dev` and open http://localhost:1420 in a browser. The
"Save…" button falls back to a browser download when the Tauri
host is unavailable; everything else works identically.

## Build

```bash
npm run tauri:build
# Artefacts land in src-tauri/target/release/bundle/
```

## Layout

```
installer/
├── package.json            JS deps + npm scripts
├── vite.config.js          Vite config
├── index.html              HTML entry point
├── src/
│   ├── main.js             SPA logic
│   └── style.css           styles
└── src-tauri/
    ├── Cargo.toml          Rust deps
    ├── tauri.conf.json     window + bundle config
    ├── build.rs
    ├── capabilities/       Tauri capability scopes
    └── src/
        ├── main.rs         entry point
        └── lib.rs          #[tauri::command] surface
```

## What lands in this revision

- Project scaffold (the file layout above).
- Home screen with three deploy choices.
- Working **Docker compose generator** with a copy/save action and
  a Rust-backed `save_compose` command (native file dialog).
- "Coming soon" placeholders for the SSH and Edge paths.

## What lands next

- SSH workflow: ask host + key, sync the `veil` binary, write the
  systemd unit, generate the user database, start the server.
- Edge workflow: OAuth Deno Deploy / Fly.io, deploy a thin worker
  pointing back at an origin (or hosting the full stack via WASM).
- Embed the `veil` binary as a Tauri resource so the SSH workflow
  has something to upload.
