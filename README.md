# Veil

> Self-hosted, censorship-resistant VPN platform with adaptive multi-transport and active DPI evasion.

[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Status: Pre-Alpha](https://img.shields.io/badge/Status-Pre--Alpha-orange.svg)](#project-status)

Veil is an open-source VPN platform built for environments with active deep-packet inspection
(Russia, China, Iran, and similar). It is designed around a single principle:
**make blocking expensive enough to break the legitimate web.**

This repository contains the protocol core, server, command-line client, GUI installer,
desktop and mobile client apps, deployment recipes, and SDK bindings.

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
- [ ] **Phase 3.6** — Tauri installer: SSH remote install + edge
      OAuth flows; cross-platform release packaging.
- [ ] **Phase 4–6** — Clients, edge backends, hardening, audit, GA.

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
