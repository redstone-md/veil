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

The current focus is the Phase 0 / Phase 1 milestones from the [PRD](PRD.md):
protocol foundation, basic QUIC + Noise handshake, and a working end-to-end
prototype. APIs, configuration formats, and the wire protocol are unstable
and will change without notice until the v1.0 release.

See the [roadmap](PRD.md#18-roadmap) for milestones.

---

## Why Veil

Existing tools fall into two categories: **easy but blockable**
(WireGuard, OpenVPN, Outline / Shadowsocks) or **strong but hard to deploy**
(XRay-core, Sing-box, hand-rolled XTLS-Reality setups).
Veil targets the gap: an **easy-to-deploy, end-user-friendly product**
that ships a state-of-the-art anti-censorship core.

| Capability                             | Veil | Amnezia | Outline | XRay-core |
|----------------------------------------|:----:|:-------:|:-------:|:---------:|
| One-click GUI server install           |  ✅  |   ✅    |    ✅   |     ❌    |
| Multi-transport adaptive runtime       |  ✅  |   ❌    |    ❌   |     🟡    |
| Dynamic SNI pool per user              |  ✅  |   ❌    |    ❌   |     ❌    |
| Decoy traffic engine                   |  ✅  |   ❌    |    ❌   |     ❌    |
| Statistical traffic mimicry            |  ✅  |   ❌    |    ❌   |     ❌    |
| Edge-function backend support          |  ✅  |   ❌    |    ❌   |     ❌    |
| Zero phone-home / no central control   |  ✅  |   ✅    |    ❌   |     ✅    |
| Stable C-API for third-party apps      |  ✅  |   ❌    |    ❌   |     🟡    |

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

(Cross-compilation, GUI installer, and Docker images will be added in Phase 1–3.)

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
