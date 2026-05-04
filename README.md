# Veil

> Self-hosted, censorship-resistant VPN platform with adaptive multi-transport and active DPI evasion.

[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Status: Pre-Alpha](https://img.shields.io/badge/Status-Pre--Alpha-orange.svg)](#project-status)
[![Audit: Pending](https://img.shields.io/badge/External_audit-Pending-yellow.svg)](docs/AUDIT_PREP.md)

> ⚠️ **Pre-alpha. No external security audit yet. Do not use this for anything you can't afford to leak.**
>
> The protocol design is documented and the code is reviewable, but it has not been validated by
> an independent reviewer. Treat it as a research / development artefact until the v0.1.0-alpha.1
> tag and the first audit report ship together.

Veil is an open-source VPN platform built for environments with active deep-packet inspection
(Russia, China, Iran, and similar). It is designed around a single principle:
**make blocking expensive enough to break the legitimate web.**

This repository contains the protocol core, server, command-line client, GUI installer,
desktop and mobile client apps, deployment recipes, and SDK bindings.

## Known limitations (pre-alpha)

The roadmap below tracks what's wired and what isn't. As of this revision, the rough shape:

- ✅ **Server + CLI client + SDKs** are exercised end-to-end against a real VPS — three
  transports (Reality / WSS / QUIC) plus the MASQUE roundtrip, multi-stream multiplex,
  cosign-signed releases.
- ⚠️ **Desktop GUI client** (Tauri 2) functions in SOCKS5 mode end-to-end. The system-wide
  TUN mode is wired through Wintun but has not been exercised on a wide hardware matrix yet.
- ⚠️ **Mobile clients** (React Native + iOS NEPacketTunnelProvider + Android VpnService)
  are scaffolded with the platform-specific TUN code wired into libveil; bring-up against
  Xcode / Android NDK builds is the operator's job until we ship signed binaries.
- ⚠️ **Per-app split tunneling on Windows** is not a thing yet (would require a signed
  kernel driver). The desktop instead supports CIDR-level split routing — game IPs / LAN
  ranges that bypass the tunnel without kernel-mode work.
- ❌ **External security audit** — pending. Funding-gated. Until then every cryptographic
  claim is "the design says so", not "an independent auditor confirmed it".

---

## Project status

**Pre-alpha. Under active development. Not ready for production use.**

Where we are on the [roadmap](PRD.md#18-roadmap):

- [x] **Phase 0** — Foundation: spec, threat model, ADRs, QUIC + Noise XK skeleton.
- [x] **Phase 1** — Core MVP: VWP/1 frames, multiplexed sessions, SOCKS5 client,
      end-to-end TCP forwarding, Docker Compose deploy.
- [x] **Phase 2** — Anti-DPI layer: WebSocket-over-TLS transport, uTLS browser
      fingerprint mimicry, multi-transport listener / fall-back dialer, dynamic
      SNI pool, cover-traffic decoy engine.
- [x] **Phase 2.5** — TLS-Reality transport: client embeds an HMAC-derived
      auth tag in the TLS SessionID; server verifies against the per-deployment
      secret derived from its static Noise XK key. Probe traffic (anything
      without a valid tag) is transparently spliced to the configured target
      origin so probes see a real, third-party TLS response (real cert, real
      content). Authenticated clients get a forged TLS cert whose SAN names
      the target SNI, then run a Noise XK + VWP/1 session inside.
- [x] **Phase 3** — Self-host UX (CLI half): SQLite-backed user store with
      quotas / expiry / status flags, `veil user` CRUD subcommands, an
      embedded admin HTTP API protected by HTTP Basic auth backed by an
      `admin_users` table, a minimal HTML dashboard served from the same
      binary, and an [INSTALL guide](docs/INSTALL.md) walking through
      end-to-end bring-up.
- [x] **Phase 3.5** — Operator polish: caddyserver/certmagic-backed
      ACME (Let's Encrypt) for WSS/Reality, per-user quota enforcement
      in the data path (in-flight cutoff, monthly reset), polished
      Web admin UI (sortable / filterable table, inline quota and
      expiry editors), and a Tauri installer **scaffold** with the
      Docker compose generator workflow wired up. SSH and edge
      installer paths land in the next milestone.
- [x] **Phase 3.6 (installer SSH workflow + real-world bring-up)** —
      The Tauri installer's SSH workflow is now functional: connect,
      upload the bundled `veil` binary, write `/etc/veil/server.yaml`
      and a systemd unit, enable + start the service, tail the
      logs back to the operator. Backed by `russh` (ring crypto
      backend, no NASM dependency on Windows) wrapped in two
      `#[tauri::command]` handlers (`ssh_probe`, `ssh_install`).
      [docs/REAL_WORLD_VERIFICATION.md](docs/REAL_WORLD_VERIFICATION.md)
      captures the first end-to-end run against a real Lithuania
      VPS — three transports (Reality, WSS, QUIC) bound at once,
      30-stream multiplex, throughput numbers vs direct, Reality
      probe behaviour.
- [x] **Phase 3.7 (installer release + edge bundle generator)** —
      a tag-triggered cross-platform installer build matrix
      (`.github/workflows/installer.yml`) producing
      `.AppImage` / `.deb` / `.dmg` / `.app` / `.msi` artefacts
      and attaching them to the matching GitHub Release.
      Plus the installer's Edge workflow is now functional:
      pick Deno Deploy or Fly.io, fill in origin host/port/path,
      hit Generate, and the GUI emits a complete folder of
      worker source + provider config + DEPLOY recipe that the
      operator runs through `deployctl deploy` / `fly deploy`.
- [x] **Phase 3.8 (auto-fetch + codesigning hooks)** — the
      installer's SSH workflow now auto-detects the remote
      architecture, downloads the matching `veil` binary from
      the latest GitHub Release, and uploads it without an
      operator file-pick step. The installer CI workflow gains
      opt-in macOS / Windows codesigning hooks (`APPLE_*`,
      `WINDOWS_*`, `TAURI_SIGNING_*` secrets) — missing secrets
      fall back to an unsigned build, present secrets sign +
      notarise. `docs/RELEASING.md` walks operators through
      the secret setup.
- [x] **Phase 3.9 (direct edge deploy)** — installer pushes the
      generated edge bundle straight to the provider's API via a
      paste-in personal-access token. `installer/src-tauri/src/edge_deploy.rs`
      drives Deno Deploy (`POST /v1/projects` + `/deployments`) and
      Fly.io Machines (`POST /v1/apps` + `/machines`); two Tauri
      commands (`edge_deploy_deno`, `edge_deploy_fly`) surface the
      flow to the GUI, which adds a token + app input + deploy
      button to the existing edge form. Token stays in process
      memory for the duration of one API call (never written to
      disk) — operator revokes from the provider dashboard after.
- [x] **Phase 6.5 (distribution channels)** — release.yml now
      builds + pushes a multi-arch (`linux/amd64,linux/arm64`)
      Veil server image to `ghcr.io/redstone-md/veil:vX.Y.Z` and
      `:latest` on every tag, with cosign-keyless signing of the
      image manifest. Homebrew formula skeleton at
      `deploy/homebrew/veil.rb` and Scoop manifest at
      `deploy/scoop/veil.json` (with checkver / autoupdate blocks)
      land alongside docs walking maintainers through the per-
      release update of the tap and bucket repos.
- [x] **Phase 4 (CLI/SDK half)** — Refactor of the connect path into
      a reusable `internal/client.Client`, a CGO-built libveil shared
      library exposing a stable C ABI (`core/pkg/cgo` +
      `core/pkg/cgo/include/veil.h`), a safe Rust crate over that ABI
      at `sdks/veil-rs`, a ctypes Python package at `sdks/veil-py`,
      and a `veil://` share-link URI scheme so client configs can be
      distributed as a single one-line string (printed by
      `veil user show-config`, accepted by `veil connect --link`).
- [x] **Phase 4.5 (desktop client scaffold)** — `clients/desktop/`
      Tauri 2 app linking the safe `veil-rs` SDK; `veil_start` /
      `veil_stop` / `veil_metrics_json` Tauri commands drive an
      in-process Veil session and forward every runtime event to
      the JS frontend via the `"veil-event"` channel. Status panel
      with status dot, transport label, byte counters, last-event
      line, and a paste-in config text-area persisting through
      `localStorage`. The libveil shared library ships next to the
      binary (built from `core/pkg/cgo`).
- [x] **Phase 4.7 (desktop polish)** — system tray with
      Connect/Disconnect/Show/Quit menu and click-to-restore;
      close-to-tray (window hide instead of process exit); OS
      notifications on connect/error/transport-switch via
      `tauri-plugin-notification`; launch-at-login toggle via
      `tauri-plugin-autostart` (LSSharedFileList / xdg autostart /
      Windows Run key); profile manager (multi-config dropdown with
      add/delete/save) plus a settings panel (autostart, mimicry,
      decoy, notifications), persisted through `tauri-plugin-store`;
      in-app update check + apply that delegates to the bundled
      `veil update` CLI (new `--json` flag on `veil update check`).
- [x] **Phase 4.6 (mobile + Node bindings, scaffold)** —
      `sdks/veil-node/` napi-rs crate that wraps `veil-rs` to ship
      a `@veil/node` package (Veil class with start/stop/metrics +
      ThreadsafeFunction-bridged event callback);
      `clients/mobile/` Expo bare React Native app sharing the
      desktop UX (status, profiles, settings, log) over a thin
      `src/veil.js` native-bridge module;
      `clients/mobile/android/` Kotlin `VeilVpnService` + RN
      `VeilBridgeModule` handling the `VpnService.prepare()`
      consent dance and forwarding events to JS;
      `clients/mobile/ios/PacketTunnelProvider/` Swift
      `NEPacketTunnelProvider` + `VeilSession` wrapper over the C
      ABI through a bridging header. The platform-side `cgo` entry
      points (`jni_android.go`, `ne_ios.go`) for TUN ingestion are
      pending; tracked in the per-platform READMEs.
- [x] **Phase 5 (hardening half)** — statistical mimicry profiles
      (browse / video / messaging / search) wired into the data path
      via packet padding + inter-arrival jitter; multi-listen per
      transport so a single config entry can bind several IPs/ports;
      Go-native fuzz tests for the binary parsers (frame codec,
      Reality ClientHello, share-link) plus a nightly `fuzz.yml`
      workflow; an `AUDIT_PREP.md` checklist that scopes the
      upcoming external review.
- [x] **Phase 5.5 (auto-update + skeletons)** — `veil update`
      subcommand: GitHub releases query, platform asset download,
      SHA-256 checksum verification, atomic binary replace
      (Unix rename / Windows aside-stage). MASQUE transport
      skeleton + ADR-0003 documenting the design. Edge-backend
      reference Deno Deploy worker (`deploy/edge/deno/`) +
      ADR-0004 documenting the trust model and Fly.io variant
      plan.
- [x] **Phase 5.6 (auto-update + edge polish)** — Sigstore
      cosign-keyless signature verification on auto-updates
      (`veil update apply --cosign`), a Fly.io edge worker
      (`deploy/edge/fly/`) functionally equivalent to the Deno
      Deploy reference but suitable for operators who need the
      regions / bandwidth / container debuggability Fly offers,
      and a deeper ADR-0003 update with the concrete `masque-go`
      v0.3 API findings + nested-QUIC architecture sketch.
- [x] **Phase 5.7 (functional MASQUE)** — wired
      `github.com/quic-go/masque-go` v0.3 into `transport/masquetr`
      as a real listener + dialer pair. Server is an HTTP/3 endpoint
      whose CONNECT-UDP handler forwards every datagram to a
      configured loopback inner-QUIC listener; client composes
      outer-QUIC + HTTP/3 + CONNECT-UDP + inner-QUIC in one Dial and
      returns the inner stream. Outer `EnableDatagrams` plus a
      1350-byte `InitialPacketSize` are required so the inner QUIC's
      1200 MTU survives the proxy hop. End-to-end roundtrip test in
      `roundtrip_test.go`.
- [x] **Phase 6 (release machinery)** — tag-triggered cross-platform
      `release.yml` workflow that builds the full asset matrix,
      signs every artefact with cosign keyless via the workflow's
      GitHub OIDC identity, generates per-binary SBOMs via syft,
      and uploads everything to a GitHub Release. CHANGELOG in the
      Keep a Changelog 1.1 format covering every shipped phase.
      `docs/RELEASING.md` operator process, `docs/LAUNCH_CHECKLIST.md`
      pre-GA verification checklist.
- [ ] **Phase 6.6** — external security audit kickoff (depends on
      funding); first tagged release end-to-end through the full
      release pipeline.

APIs, configuration formats, and the wire protocol are unstable and will
change without notice until the v1.0 release.

---

## Why Veil

Existing tools fall into two categories: **easy but blockable**
(WireGuard, OpenVPN, Outline / Shadowsocks) or **strong but hard to deploy**
(XRay-core, Sing-box, hand-rolled XTLS-Reality setups).
Veil targets the gap: an **easy-to-deploy, end-user-friendly product**
that ships a state-of-the-art anti-censorship core.

| Capability                             | Veil | Amnezia | Outline | XRay-core |
|----------------------------------------|:----:|:-------:|:-------:|:---------:|
| One-click GUI server install           |  🟡  |   ✅    |    ✅   |     ❌    |
| Multi-transport adaptive runtime       |  ✅  |   ❌    |    ❌   |     🟡    |
| Dynamic SNI pool per user              |  ✅  |   ❌    |    ❌   |     ❌    |
| Decoy traffic engine                   |  ✅  |   ❌    |    ❌   |     ❌    |
| uTLS browser fingerprint mimicry       |  ✅  |   ❌    |    ❌   |     ✅    |
| Statistical traffic mimicry            |  🟡  |   ❌    |    ❌   |     ❌    |
| Edge-function backend support          |  🟡  |   ❌    |    ❌   |     ❌    |
| Zero phone-home / no central control   |  ✅  |   ✅    |    ❌   |     ✅    |
| Stable C-API for third-party apps      |  🟡  |   ❌    |    ❌   |     🟡    |

(✅ = supported, 🟡 = partial / via add-ons, ❌ = not supported.)

---

## Architecture overview

```
┌─ End-user devices ──────────────────────────┐
│   Desktop (Tauri)   Mobile (RN)   CLI / SDK  │
└──────────────────────┬───────────────────────┘
                       │ Veil Wire Protocol (VWP/1)
                       │ adaptive: QUIC | TLS-Reality | WSS | MASQUE
                       ▼
┌─ User-owned infrastructure ──────────────────┐
│   VPS (systemd or Docker)                     │
│        OR                                     │
│   Edge function (Deno Deploy / Fly.io)        │
└──────────────────────┬───────────────────────┘
                       │
                       ▼
                  The Internet
```

For the full design, see the [PRD](PRD.md), the [protocol spec](docs/PROTOCOL.md),
and the [threat model](docs/THREAT_MODEL.md).

---

## Repository layout

```
core/         Single-binary Go core: server, client, admin, CLI, C-API
installer/    Tauri GUI for one-click server deployment
clients/      Desktop (Tauri) and mobile (React Native) end-user apps
deploy/       Docker, Ansible, Terraform, edge-function recipes
sdks/         Language bindings (Rust, Python, Node, Swift, Kotlin)
docs/         Specifications, threat model, ADRs, install guides
scripts/      Build, release, and helper scripts
```

---

## Getting started

> Installation flows for non-technical users (GUI installer, mobile clients)
> are not yet available. The instructions below are for developers building
> from source during the pre-alpha phase.

### Prerequisites

- Go 1.22 or newer
- Git

### Build the core

```bash
git clone https://github.com/redstone-md/veil.git
cd veil/core
go build -o ../bin/veil ./cmd/veil
../bin/veil --help
```

### Run a server and tunnel through it (local smoke test)

```bash
# 1. Generate the server's keypair and configuration
mkdir -p state
cat > server.yaml <<'EOF'
listen: "127.0.0.1:18443"
static_key_path: "state/server.key"
authorized_keys_path: "state/authorized_keys"
EOF
touch state/authorized_keys

# 2. Start the server (it will create state/server.key on first run)
./bin/veil serve --config server.yaml &

# 3. Read the server's public key and create a client config
SERVER_PUB=$(sed -n '2p' state/server.key)
cat > client.yaml <<EOF
server_addr: "127.0.0.1:18443"
server_static_key_b64: "$SERVER_PUB"
static_key_path: "state/client.key"
socks5_listen: "127.0.0.1:1080"
EOF

# 4. Start the client once to generate its key, then add that key to
#    the server's authorized_keys
./bin/veil connect --config client.yaml &
sleep 1
sed -n '2p' state/client.key >> state/authorized_keys
# (restart server so it picks up the new authorized client)
kill %1 && ./bin/veil serve --config server.yaml &
sleep 1

# 5. Use it
curl --proxy socks5h://127.0.0.1:1080 https://example.com
```

### Run a server in Docker

See [`deploy/docker/README.md`](deploy/docker/README.md).

(GUI installer, mobile clients, and signed releases land in Phases 3–5.)

---

## Documentation

- [PRD](PRD.md) — product requirements, scope, roadmap
- [docs/PROTOCOL.md](docs/PROTOCOL.md) — Veil Wire Protocol specification
- [docs/THREAT_MODEL.md](docs/THREAT_MODEL.md) — adversaries, assets, mitigations
- [docs/architecture/](docs/architecture/) — Architecture Decision Records
- [SECURITY.md](SECURITY.md) — vulnerability reporting
- [CONTRIBUTING.md](CONTRIBUTING.md) — how to contribute
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) — community standards

---

## Funding

Veil is sustained by donations. There is no commercial offering, no enterprise
edition, and no paid features. If the project is useful to you, consider supporting
ongoing development:

- GitHub Sponsors: *(link will be added when GA)*
- OpenCollective: *(link will be added when GA)*

All financial flows will be public via OpenCollective.

---

## License

Veil is licensed under the [Apache License 2.0](LICENSE).

You may use, modify, and redistribute Veil — including for commercial purposes —
as long as you preserve the [NOTICE](NOTICE) file and credit the project per
Apache 2.0 Section 4. We particularly encourage forks that improve the protocol
or extend client coverage; please contribute changes back when possible.

The name "Veil" and the project's logo are project marks; if you ship a derivative
that materially diverges from upstream, please use a different product name to
avoid user confusion.

---

## Disclaimer

Veil is a tool. Like any tool, it can be used responsibly or irresponsibly.
Users are solely responsible for compliance with the laws of their jurisdiction.
The Veil project provides no warranty of fitness for any purpose
and no guarantee of uninterrupted service.
