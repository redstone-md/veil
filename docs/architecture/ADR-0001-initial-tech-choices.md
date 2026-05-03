# ADR-0001: Initial technology choices

**Status:** Accepted
**Date:** 2026-05-04
**Deciders:** Initial maintainer

## Context

Veil is a from-scratch project that needs a foundational tech stack
for its core, its installer, and its client applications. The choices
made now will be very expensive to undo later, so we are recording the
reasoning explicitly.

The product requirements are detailed in the [PRD](../../PRD.md);
this ADR addresses the technology selection for delivering them.

## Decision

We adopt the following stack for v1:

| Component                 | Technology                                                |
|---------------------------|-----------------------------------------------------------|
| Core (server, client, CLI, admin) | **Go 1.22+**, single binary                       |
| Cryptography              | **Noise Protocol Framework** + standard library primitives |
| QUIC implementation       | **`quic-go/quic-go`** as a dependency (not a fork)        |
| TLS fingerprint mimicry   | **`refraction-networking/utls`**                          |
| TLS-Reality               | Independent implementation, conforming to public XTLS-Reality semantics |
| User database             | **SQLite (embedded)** by default; optional Postgres        |
| Embedded admin UI         | Vite-built SPA, served from Go via `embed.FS`             |
| Logging                   | Standard-library **`log/slog`**                            |
| Metrics                   | **Prometheus** client library                             |
| ACME client               | **`caddyserver/certmagic`**                               |
| GUI installer             | **Tauri v2** (Rust shell + WebView)                       |
| Desktop client            | Same Tauri stack as installer                              |
| Mobile client             | **React Native** with native modules for VPN APIs          |
| C-API                     | CGO, single shared library `libveil.{so,dll,dylib}`        |
| Build / release           | **GitHub Actions**, **Sigstore (cosign)** for signing      |
| License                   | **Apache License 2.0**                                     |

## Alternatives considered

### Core language

| Option | Why not |
|--------|---------|
| Rust   | Strongest safety story but: smaller pool of contributors with networking + cryptography experience, longer iteration cycle for a small team, weaker mature ecosystem for our specific needs (CGO replacement, embedded SQL, ACME). Reconsider for v2 modules where appropriate. |
| C / C++ | Memory-safety risk in a security-critical project. Out. |
| Zig    | Too young; ecosystem not mature enough for production VPN. |
| Java / Kotlin | Heavy runtime, GC tuning headaches, awkward fit for embedded/router targets later. |

Go offers the best balance for this team and this product right now:
fast iteration, strong networking story, large pool of qualified
contributors, decent mobile / shared-library support via CGO,
and a battle-tested QUIC implementation in the ecosystem.

### QUIC: fork vs. dependency

The original PRD draft suggested forking `quic-go`. We rejected this
because:

- `quic-go` evolves quickly with QUIC spec changes; a fork accumulates
  merge debt rapidly.
- Most of what we need is achievable through dependency injection at
  the TLS-config and connection-acceptor layers.
- Where `quic-go` lacks a hook, we can upstream the change benefiting
  the whole community.

If a feature genuinely cannot be expressed without a fork, we will
revisit and document the divergence.

### Memory: arena allocators

Earlier drafts suggested using Go's experimental `arena` package for
zero-GC packet handling. We are NOT taking this path:

- The `arena` experiment has been frozen since Go 1.20 and shows no
  signs of stabilising.
- `sync.Pool` and careful slice reuse give us 90% of the benefit with
  zero stability risk.
- Real zero-copy on the data path would mean no CGO in the hot path
  anyway, so arenas don't even apply where we'd need them most.

### eBPF datapath

Considered for high-throughput operators. Rejected for v1: out of scope
per PRD §3.2. Will revisit in v2 if operators report bottlenecks at
expected workloads.

### Reality vs. Shadow-TLS

Reality and Shadow-TLS solve overlapping problems: making the server
respond plausibly to active probes by leveraging a real third-party
host. Reality has effectively superseded Shadow-TLS in active use and
has stronger probe-resistance properties. We implement Reality and
omit Shadow-TLS.

### Database

| Option | Why not |
|--------|---------|
| Bolt / Pebble (KV) | Awkward for relational user/quota data. |
| In-memory + flat files | Loses durability guarantees for a system that should survive crashes. |
| Required Postgres | Excessive for the home-VPS user. |

SQLite is zero-config, embedded, durable, and a single file the
operator can back up trivially. Postgres remains an option for
operators running at scale.

### Installer / GUI framework

| Option | Why not |
|--------|---------|
| Electron | 100+ MB binary, large RAM footprint, JS-only main process.        |
| Native (Qt / GTK / WPF / SwiftUI) | Triples the maintenance burden across platforms. |
| Web (only) | Cannot drive SSH, OS networking APIs, certificate stores.  |

Tauri ships a small Rust binary that uses the OS's WebView, gives us
a single language for the privileged side, and produces installers
in the 10–30 MB range.

### Mobile

| Option | Why not |
|--------|---------|
| Native (Swift + Kotlin) | 2x development cost; useful UI is mostly identical anyway. |
| Flutter | Smaller ecosystem for our specific needs (NetworkExtension on iOS, VpnService on Android); Dart adds yet another language to maintain. |

React Native is a known quantity, has good support for the platform
VPN APIs we need to call, and shares logic naturally with the desktop
client UI.

### License

| Option | Why not |
|--------|---------|
| MIT | Permissive enough but no patent grant; trivial for proprietary forks to relicense without contributing back. |
| GPLv3 | Forces forks open, but reduces commercial integration significantly; for a tool meant to be embedded by other apps, this is a deployment hurdle. |
| AGPLv3 | Designed for SaaS; Veil is self-host, so the SaaS-trigger clause does not match our deployment model and creates licensing FUD. |
| BSL / SSPL | Not OSI-approved; misaligned with our community goals. |

Apache 2.0 gives:
- Permissive commercial use (good for adoption).
- Explicit patent grant (protects against trolls).
- NOTICE-file mechanism (a clean way to require attribution).
- Wide ecosystem familiarity.

## Consequences

### Positive

- One language (Go) covers nearly all server/client/CLI surface.
- The single-binary model simplifies operations and packaging.
- Tauri keeps installer/desktop overhead low.
- Apache 2.0 keeps the door open for commercial integrators while
  protecting against patent litigation.

### Negative / accepted trade-offs

- Go's runtime imposes GC pauses; we accept this and mitigate with
  pooling on the hot path.
- CGO is required for the C-API, complicating cross-compilation
  matrices and adding a memory-management seam. We will fence CGO
  to the API boundary only.
- React Native means we depend on the JavaScript ecosystem and its
  churn for the mobile clients.
- Apache 2.0 does not force forks open; a malicious closed-source
  fork is legally permissible. We rely on user education and
  trademarks (project mark guidelines in NOTICE) to mitigate.

### Reversibility

- Replacing `quic-go` with a different implementation: medium cost,
  isolated behind the transport adapter interface.
- Replacing SQLite with another DB: low cost via repository
  interface.
- Replacing the GUI framework: high cost; multiple-month effort.
- Replacing the core language: prohibitively high cost; effectively
  a v2 rewrite.

We accept that the core-language decision is the most consequential
and least reversible.
