# Veil — Audit Preparation

This document is the entry point for an external security review of
the Veil VPN project. It scopes the review, points at the artifacts
the auditor needs, and tracks what we have already done internally.

> **Status:** Pre-alpha. This file is iterated alongside the code;
> by the time we engage an external auditor formally (Phase 6 RC),
> every section below should be complete and dated.

---

## 1. Scope

### In scope

- The Veil core (`core/`):
  - protocol implementation (Noise XK + VWP/1 + transports
    quictr / wsstr / realitytr / masquetr)
  - server and client lifecycles
  - SQLite user store + admin HTTP API
  - C-API (`pkg/cgo`) and the libveil binary it produces
  - mobile cgo entry points (`pkg/cgo/{mobile,jni_android}.go`)
    and the tun2socks pipe in `internal/mobile`
- The protocol specification (`docs/PROTOCOL.md`).
- The threat model (`docs/THREAT_MODEL.md`).
- The deployment recipes operators are expected to use
  (`deploy/docker/`, `deploy/edge/{deno,fly}/`).
- The release pipeline (`.github/workflows/{ci,release,installer,fuzz}.yml`).

### Adjacent, lower priority

- Mobile clients (`clients/mobile/`) — Kotlin VpnService + JNI,
  Swift NEPacketTunnelProvider + bridging header. The bridge code
  is in scope to the extent it touches libveil's C ABI; the React
  Native UI layer is out of scope for the cryptographic review.
- Desktop client (`clients/desktop/`) — Tauri 2 host that links
  the safe Rust SDK in-process. In scope to the extent it touches
  libveil; the JS UI layer is out of scope.

### Out of scope

- Third-party GUI installer (`installer/`) and language SDKs
  (`sdks/{veil-rs,veil-py,veil-node}`) are pre-alpha; the C ABI
  they wrap is in scope, the wrappers themselves enter audit
  scope for v2.
- Edge worker source code (`deploy/edge/`) is reviewed at the
  protocol-bridging layer (it terminates WSS and forwards to the
  origin); the operator is expected to apply their own provider's
  hardening guidance to the deployment itself.
- The user's hardware and operating system (see threat model
  section "Trust boundaries and assumptions").

---

## 2. Threat model

The authoritative threat model is at `docs/THREAT_MODEL.md`. It
documents:

- six adversaries (national DPI operator, local network operator,
  VPS provider, CDN provider, edge-function provider, targeted
  attacker), with their assumed capabilities;
- nine assets and the C/I/A properties Veil claims to protect;
- the trust boundaries Veil relies on;
- per-asset/per-adversary mitigations;
- explicit non-goals;
- residual risks that survive the design.

Auditors should read it before touching the code; "is this a
vulnerability?" decisions hang on what the model claims to defend.

---

## 3. Cryptographic primitives and their use

| Primitive | Use site | Source |
|-----------|----------|--------|
| Noise XK (`Noise_XK_25519_ChaChaPoly_BLAKE2s`) | Server↔client mutual auth + AEAD session keys | `internal/crypto`, driven by `flynn/noise` |
| ChaCha20-Poly1305 (RFC 8439) | Per-frame AEAD on top of Noise CipherStates | via `flynn/noise`'s CipherSuite |
| HMAC-SHA-256 | Reality auth tag construction | `internal/transport/realitytr/auth.go` (stdlib `crypto/hmac`) |
| HKDF-SHA-256 | Reality PSK derivation from server static pubkey | `internal/transport/realitytr/auth.go` (stdlib `crypto/sha256`) |
| ECDSA-P256 | Self-signed certs (Reality forge, WSS dev fallback) | stdlib `crypto/ecdsa` |
| TLS 1.2/1.3 | WSS transport (server-side terminator); Reality post-auth termination | stdlib `crypto/tls`; client side via `refraction-networking/utls` |
| bcrypt | Admin login passwords in SQLite store | `golang.org/x/crypto/bcrypt` |

All primitives are stdlib or audited libraries. No primitive is
implemented in-tree.

Areas an auditor should pay particular attention to:

- The Reality auth scheme reuses the server's long-term Noise
  static public key (via HKDF) as the PSK for the auth tag. ADR-0002
  explains the rationale and the trade-off (no per-handshake
  forward secrecy on the auth layer; data plane keeps FS via Noise).
- The session-layer SecureChannel relies on Noise CipherStates'
  internal nonce counters. Misuse here would be silent.
- The Reality listener writes a freshly-forged certificate whose
  CN matches the impersonated SNI. Clients must skip TLS chain
  validation; this is by design (Noise is the auth anchor) but is
  worth a second pair of eyes.

---

## 4. Code organisation an auditor will care about

```
core/
├── cmd/veil/                  binary entry point
├── pkg/cgo/                   C ABI for SDK consumers (//go:build cgo)
└── internal/
    ├── acme/                  Let's Encrypt cert manager wrapper
    ├── admin/                 embedded admin HTTP + Web UI
    ├── auth/                  Authenticator (file vs SQLite)
    ├── client/                embeddable client lifecycle
    ├── config/                YAML schema
    ├── crypto/                Noise XK keys + handshake helpers
    ├── dpi/
    │   ├── decoy/             cover-traffic generator
    │   ├── mimicry/           outbound traffic shaping
    │   ├── snipool/           regional weighted SNI pool
    │   └── utlsdial/          uTLS browser fingerprint dialer
    ├── forward/               server-side stream → upstream TCP
    ├── frame/                 binary VWP/1 frame codec
    ├── proxy/                 SOCKS5 listener (RFC 1928)
    ├── session/               SecureChannel + multiplex
    ├── sharelink/             veil:// URI scheme
    ├── transport/             FanIn / Fallback + adapters:
    │   ├── quictr/            QUIC adapter (quic-go)
    │   ├── wsstr/             WSS adapter (coder/websocket)
    │   └── realitytr/         Reality (TLS-Reality-style)
    └── users/                 SQLite store + accountant
```

Highest density of subtle bugs:
1. `internal/transport/realitytr/` — handles raw bytes from the
   network before any cryptographic check.
2. `internal/session/` — frame parsing + cipher state management.
3. `internal/forward/` — the data path; quota accounting and
   half-close semantics live here.
4. `pkg/cgo/` — every memory rule documented at the top of `veil.go`
   is a load-bearing constraint.

---

## 5. Build reproducibility

The CI matrix (`/.github/workflows/ci.yml`) currently builds:

- `veil` static binary (`CGO_ENABLED=0`) for
  `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`,
  `windows/amd64`.
- `libveil.{so,dylib,dll}` (CGO; built on a real OS runner per
  target).

`-trimpath` is set on every release-targeted build. Pre-alpha
builds do not pin Go toolchain hashes; v1.0 release pipeline will
do so explicitly.

A reproducible-build attestation is on the Phase 6 checklist; it is
not yet enabled.

---

## 6. Supply-chain posture

- Dependencies are pinned via `go.sum` and Cargo's lockfile policy
  for the SDKs. The Rust SDK does not ship a `Cargo.lock` (library
  convention).
- `govulncheck` runs in CI on every PR (job: `vulncheck`).
- Dependabot is not yet configured; renovate equivalent is on the
  Phase 6 checklist.
- Releases are signed with Sigstore (cosign keyless) via the
  workflow's GitHub OIDC identity (`.github/workflows/release.yml`).
  Per-binary SBOMs are produced by syft and uploaded alongside.
  The `veil update apply --cosign` flag verifies signatures on
  auto-updates against the configured `--cosign-subject` /
  `--cosign-issuer`.
- Multi-arch container images at
  `ghcr.io/redstone-md/veil:vX.Y.Z` are signed at the manifest
  level by the same workflow.

Top dependencies an auditor should inspect:

| Dep | Version | Use |
|-----|---------|-----|
| `flynn/noise` | latest | Noise XK handshake & AEAD |
| `quic-go/quic-go` | 0.59.x | QUIC transport |
| `refraction-networking/utls` | 1.8.x | uTLS Chrome ClientHello |
| `coder/websocket` | 1.8.x | WSS adapter |
| `caddyserver/certmagic` | 0.25.x | ACME |
| `modernc.org/sqlite` | 1.50.x | embedded user store (pure Go) |
| `golang.org/x/crypto/bcrypt` | latest | admin password hashing |

---

## 7. Testing posture

- **Unit tests** in every package that warrants them: `frame`,
  `session`, `sharelink`, `transport/realitytr`, `dpi/snipool`,
  `dpi/mimicry`, `users`. CI runs them on Linux/macOS/Windows.
- **Race detector**: enabled in the CI test job (`go test -race`).
- **Fuzz tests** (Go native): `frame.FuzzDecodeRoundTrip`,
  `frame.FuzzDecodeStreamOpen`, `realitytr.FuzzParseClientHello`,
  `sharelink.FuzzDecode`. A nightly CI job
  (`.github/workflows/fuzz.yml`) runs each for ~5 minutes; OSS-Fuzz
  integration is on the Phase 6 checklist.
- **End-to-end smoke tests** are documented in the protocol README
  and used during phase-bring-up; CI does not yet automate them
  (a Docker-compose-driven harness lands in Phase 6).

---

## 8. Prior reviews

| Date | Reviewer | Scope | Findings | Result |
|------|----------|-------|----------|--------|
| (pending) | (pending) | — | — | — |

This table is empty by design; we will populate it as reviews
happen and link the public reports back here.

---

## 9. Reporting findings

Per `SECURITY.md`, vulnerabilities are reported privately via the
GitHub Security Advisory channel on `redstone-md/veil`. The
maintainer responds within 72 h. A coordinated disclosure timeline
is then agreed with the reporter; the default window is 90 days,
adjustable by mutual consent.

Auditor-found findings, agreed CVSS, and remediation status are
tracked in the GitHub Security tab; we publish post-disclosure
write-ups as advisories on the repository.

---

## 10. Funding posture

Veil is donation-funded with no commercial backing. Audits are
contracted on best-effort budget; the maintainer raises funding
specifically for each audit cycle via OpenCollective with a
transparent ledger.

Auditors who would prefer to donate hours rather than be paid are
invited to do so under the same coordinated-disclosure terms; the
reciprocal bug-bounty programme will be set up post-v1.0.
