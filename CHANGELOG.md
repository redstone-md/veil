# Changelog

All notable changes to Veil are recorded in this file.

The format follows [Keep a Changelog 1.1](https://keepachangelog.com/en/1.1.0/),
and the project's published versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Unreleased work lives at the top under `## [Unreleased]`. Tagged
releases are appended below it as `## [vMAJOR.MINOR.PATCH] – YYYY-MM-DD`.

> **Pre-1.0.** Every entry below describes pre-alpha development
> work. The first tagged release will be `v0.1.0-alpha.1` once the
> Phase 6 release pipeline is exercised end-to-end against the
> repository tags.

---

## [Unreleased]

### Added — protocol and transports

- **VWP/1 wire protocol** with binary frame codec (`internal/frame`),
  AEAD-secured channel over Noise XK (`internal/session/secure`),
  multiplexed streams with per-stream flow control
  (`internal/session/{session,stream}`).
- **QUIC transport adapter** (`internal/transport/quictr`) — the
  Phase 0/1 default; uses `quic-go/quic-go` as a dependency.
- **WebSocket-over-TLS transport** (`internal/transport/wsstr`) —
  TCP+TLS+websocket carrying VWP/1; the natural pair for uTLS
  fingerprint mimicry and for CDN-fronted deployments.
- **TLS-Reality transport** (`internal/transport/realitytr`) —
  client embeds an HMAC-derived auth tag in the TLS SessionID;
  server proxies all unauthenticated traffic transparently to a
  configured target SNI so probes see the real third-party site;
  authenticated clients short-circuit into a Noise XK session
  inside a forged-cert TLS termination.
- **HTTP/3 MASQUE transport** — skeleton + design (ADR-0003); the
  functional implementation is gated on `quic-go/masque-go`
  reaching a stable release (Phase 5.7).
- **Multi-listen per transport** — one `transports[*]` entry can
  bind several IPs/ports via `listens: []string`.

### Added — anti-DPI layer

- **Dynamic regional SNI pool** (`internal/dpi/snipool`) of
  hand-curated top-rank domains across six regions, with
  Zipf-weighted selection and per-user FNV shards so distinct
  Veil clients on the same network draw distinct subsets.
- **uTLS browser ClientHello mimicry** (`internal/dpi/utlsdial`)
  for TCP-based transports, covering Chrome, Firefox, Safari,
  iOS, Android (OkHttp), Edge, plus randomized ALPN.
- **Cover-traffic decoy engine** (`internal/dpi/decoy`) — periodic
  real HTTPS GETs to the SNI pool from a uTLS-shaped client.
- **Statistical mimicry** (`internal/dpi/mimicry`) — outbound
  STREAM_DATA frames padded to a chosen profile's
  packet-size distribution and delayed by the profile's
  inter-arrival cadence (browse / video / messaging / search).

### Added — operator surface

- **SQLite-backed user store** (`internal/users`) with quotas,
  status flags, and admin-login table; pure-Go via
  `modernc.org/sqlite` (no CGO).
- **`veil user` CRUD subcommand** with add / list / revoke /
  restore / regen / delete / set-quota / set-expiry / show-config.
- **Embedded admin HTTP API + Web UI** (`internal/admin`) — REST
  endpoints over the user store, HTTP Basic auth backed by the
  admin_users table, vanilla-JS dashboard with sortable table and
  inline quota / expiry editors.
- **`veil admin serve` + `admin user-create` / `user-passwd`**.
- **`veil:// share-link URI`** (`internal/sharelink`) — one-line
  base64-of-JSON client config; printed by `veil user
  show-config`, accepted by `veil connect --link`.
- **Pluggable Authenticator** (`internal/auth`) — file or SQLite
  backend, selected by which config field the operator sets.
- **Per-user byte accounting and quota enforcement**
  (`internal/users/accountant` + `internal/forward`) — in-flight
  cutoff once a user crosses their monthly cap; status flips to
  `quota_exceeded`.
- **caddyserver/certmagic ACME** integration (`internal/acme`):
  declare a domain on a TLS-terminating transport plus a server
  `acme:` block, get auto-renewing Let's Encrypt certs.
- **`veil update` self-installer** (`internal/update` +
  `internal/cli/update`): GitHub Releases lookup, platform asset
  download, SHA-256 checksum verify, atomic binary replace
  (Unix rename / Windows aside-stage). `--cosign` flag adds
  Sigstore keyless signature verification on top of the
  checksum.

### Added — SDKs and libraries

- **C ABI** (`core/pkg/cgo` + hand-written
  `core/pkg/cgo/include/veil.h`): `veil_create / start / stop /
  destroy / get_metrics / version_string / free_string` with an
  event callback hook.
- **Rust SDK** (`sdks/veil-rs`) — RAII handle, typed enums,
  closure callbacks, hand-written FFI bindings (libc + serde +
  thiserror only).
- **Python SDK** (`sdks/veil-py`) — pure ctypes wrapper, no third-
  party deps, `with`-syntax context manager.

### Added — deployment

- **Docker Compose recipe** (`deploy/docker/`) — multi-stage
  distroless build, non-root user, persistent state volume, health
  check.
- **Deno Deploy edge worker** (`deploy/edge/deno/`) — TypeScript
  WSS-to-origin proxy in ~80 lines.
- **Fly.io edge worker** (`deploy/edge/fly/`) — Go in a distroless
  container; functionally equivalent to the Deno variant for
  operators who need the regions / bandwidth / debuggability Fly
  offers.

### Added — installer and clients

- **Tauri v2 installer scaffold** (`installer/`) — Vite + vanilla
  JS frontend on a Rust host; the Docker compose generator
  workflow is fully implemented; SSH and Edge OAuth workflows
  are placeholders for Phase 3.6.

### Added — security and verification

- **Hand-written threat model** (`docs/THREAT_MODEL.md`) covering
  six adversaries, nine assets, and per-asset / per-adversary
  mitigations.
- **Architecture Decision Records** (`docs/architecture/`) for
  the initial tech choices, Reality, MASQUE, and edge backends.
- **Audit-prep document** (`docs/AUDIT_PREP.md`) — scope,
  primitives, code map, posture, prior-reviews ledger.
- **Go-native fuzz tests** for the binary parsers (frame codec,
  Reality ClientHello, share-link) plus a nightly CI fuzz job.
- **govulncheck** runs in CI on every PR.

### Added — documentation

- **Product Requirements Document** (`PRD.md`) describing scope,
  phases, success metrics, and risks.
- **Protocol specification** (`docs/PROTOCOL.md`) — VWP/1 wire
  format, frame layout, transport adapters, mimicry profiles.
- **Install guide** (`docs/INSTALL.md`) — end-user walkthrough
  from build to first SOCKS5 tunnel.

### Changed

- Server config schema is multi-transport (`transports: [...]`)
  rather than single-transport. Each transport entry can bind
  several addresses via `listens: []`.
- Client config is multi-server (`servers: [...]`) with declarative
  fall-back order; the dialer probes entries in declaration order.
- The `connect` CLI is a thin wrapper over the embeddable
  `internal/client.Client`, which is also the substrate of the
  C ABI and the SDK bindings.
- Server transport choice extended to recognise `reality` and
  `masque` (the latter returns `ErrNotImplemented` until the
  Phase 5.7 functional ship).

### Security

- Default WSS / Reality TLS termination uses self-signed certs
  in development; production deployments are expected to set
  cert/key files or enable ACME. The startup logs warn loudly
  when self-signed mode is in use.
- Admin HTTP endpoint defaults to `127.0.0.1`; binding `0.0.0.0`
  is allowed but logs a loud warning recommending SSH local-
  forward instead.
- Cosign signature verification on auto-updates lands behind an
  opt-in `--cosign` flag; the default integrity check is SHA-256
  against the release's `checksums.txt`.

### Tests

- Unit tests in every implementation package that warrants one.
- `-race` enabled in CI on Linux/macOS/Windows.
- Fuzz harness in CI (nightly), failing inputs uploaded as
  artefacts.

---

[Unreleased]: https://github.com/redstone-md/veil/compare/HEAD
