# Veil Threat Model

**Document version:** 0.1.0 (Draft)
**Last updated:** 2026-05-04
**Status:** Living document — open to public review and revision.

This document describes the adversaries Veil aims to defend against,
the assets it protects, the assumptions it relies on, and the
attack vectors it explicitly addresses. It is the source of truth
for "is this a vulnerability?" decisions and for evaluating proposed
features.

If you believe this model is incomplete or wrong in any specific way,
please open an issue. Threat-model evolution is a first-class
contribution.

---

## 1. Scope

Veil is a **self-hosted VPN platform**. The threat model covers:

- The Veil core protocol (Veil Wire Protocol, VWP).
- The Veil server (run on user-controlled infrastructure: VPS,
  Docker, edge function).
- The Veil clients (desktop, mobile, CLI, SDK consumers).
- The deployment tooling (installer, Docker recipes, Ansible).
- Edge-function backends used as transport (when configured).

It does **not** cover:

- The hardware or operating system the Veil server runs on.
- The hardware or operating system the Veil client runs on.
- Out-of-band channels used to distribute user configurations
  (the security of, e.g., emailing a `veil://` link to a friend
  is the user's responsibility).
- The behaviour of the upstream internet.

---

## 2. Adversaries

### A1. National-scale DPI operator (primary adversary)

Examples: Russian ТСПУ, Chinese GFW, Iranian SmartFilter.

**Capabilities (assumed in 2026):**
- Passive deep packet inspection at line rate on backbone links.
- Active probing of suspect endpoints (TLS handshake replay,
  HTTP probes, SSH banner grabs, etc.).
- Statistical / ML-based traffic classification using flow-level
  features (packet sizes, timings, burstiness, flow durations).
- ASN-wide and IP-range-wide blocklists; can block by hosting
  provider with little political cost.
- Known-protocol blacklisting (OpenVPN, IKEv2, plain WireGuard,
  Shadowsocks without obfuscation).
- Throttling and shaping of suspect flows.
- Periodic full-list updates (hours to days from new technique
  appearing to deployed counter-block).
- Subpoena power over domestic infrastructure (CDNs, hosting,
  registrars).

**Capabilities NOT assumed:**
- Real-time interactive analysis of every flow (cost-prohibitive).
- Cryptographic break of modern primitives (X25519, ChaCha20-Poly1305).
- Subpoena power over arbitrary foreign infrastructure (varies
  by jurisdiction).
- 0-day exploitation of Veil clients (treated separately as A6).

**Motivation:** Information control. The adversary wants to
maximise blocking efficacy while minimising collateral damage to
domestic legitimate traffic and economic activity.

### A2. Local network operator

Examples: corporate networks, hotel/cafe Wi-Fi, school networks.

**Capabilities:**
- DPI at the gateway.
- Port whitelisting (often only 80/443 outbound).
- DNS hijacking and SNI-based blocking.
- TLS interception in some corporate environments (rare for guest
  networks).

**Motivation:** Policy enforcement, bandwidth management.

### A3. VPS hosting provider

The provider hosting the Veil server.

**Capabilities:**
- Sees all traffic to and from the server.
- Can log, throttle, or terminate the service per their TOS.
- Can be subpoenaed by law enforcement in their jurisdiction.

**Motivation:** TOS compliance, legal compliance, abuse mitigation.

### A4. CDN provider (when used as edge)

The CDN whose edge network forwards Veil traffic.

**Capabilities:**
- Sees TLS metadata at the edge.
- Sees the inner HTTP requests if Veil is using HTTP-layer
  transport.
- Can ban accounts for TOS violation (high bandwidth, "tunnelling").

### A5. Edge-function provider (when used as backend)

Examples: Deno Deploy, Fly.io.

**Capabilities:**
- Sees all traffic through deployed functions.
- Can revoke API tokens, terminate the deployment.
- Code is uploaded by the user, so the provider can in principle
  inspect it.

### A6. Targeted attacker

A motivated adversary with the intent and resources to identify
or compromise a specific user.

**Capabilities:**
- Can develop or purchase 0-day exploits against client OSes,
  browsers, or Veil itself.
- May have physical access to the target's device.
- May coerce or compromise the user's social contacts who
  share Veil server access.

**This is partially out of scope.** Veil aims to provide reasonable
defence in depth, but cannot meaningfully resist a sufficiently
well-resourced targeted attacker. Users in this threat scenario
should layer Veil with additional tools (Tails, Qubes, hardware
security keys, compartmentalisation) and seek dedicated guidance
from organisations such as Access Now or EFF.

---

## 3. Assets

| ID  | Asset                                | Confidentiality | Integrity | Availability |
|-----|--------------------------------------|:---------------:|:---------:|:------------:|
| AS1 | User's tunneled traffic              |       ✅        |    ✅     |      ✅      |
| AS2 | User's real IP / identity from third parties beyond the server |  ✅  |     ✅    |     N/A      |
| AS3 | Server's IP / identity to the censor |       ✅        |    N/A    |     N/A      |
| AS4 | Veil presence / fact of use to censor (steganographic property) |   ✅  | N/A   | N/A      |
| AS5 | Server admin user database           |       ✅        |    ✅     |      ✅      |
| AS6 | Server admin UI (privileged plane)   |       ✅        |    ✅     |      ✅      |
| AS7 | Server static keypair                |       ✅        |    ✅     |      ✅      |
| AS8 | Per-user client keypairs             |       ✅        |    ✅     |      ✅      |
| AS9 | Veil release artifacts (supply chain) |      N/A       |    ✅     |      ✅      |

---

## 4. Trust boundaries and assumptions

**Trusted by Veil:**
- The hardware and operating system on which Veil runs (client and
  server).
- The user-managed credentials (server admin password, SSH keys
  used to deploy).
- Standard cryptographic primitives (X25519, ChaCha20-Poly1305,
  BLAKE2s, Noise framework).
- The Go runtime and the audited dependencies pinned in `go.mod`.

**Trusted only as far as their position requires:**
- The VPS provider — sees traffic, operates the infrastructure;
  Veil cannot prevent them from logging, only minimise what is
  meaningful to log.
- The CDN provider (when used) — same as VPS.
- The edge-function provider (when used) — same.

**Explicitly NOT trusted:**
- The network path between client and server (assumed adversarial).
- DNS responses on the client side (Veil resolves through its own
  configuration where possible).
- TLS certificates presented by intermediate proxies (Veil pins
  server identity through Noise static keys, not certificate trust).
- Any centralised Veil-project infrastructure for runtime operation
  (there is none; only release artifacts and update channel signing,
  both verified at the client).

---

## 5. Assets × Adversaries: defended scenarios

The mitigation column references sections of this document
and of the [Veil Wire Protocol spec](PROTOCOL.md).

### AS1 — User's tunneled traffic

| Threat | Adversary | Mitigation |
|--------|-----------|------------|
| Plaintext capture | A1, A2, A3, A4 | E2E Noise XK encryption with ephemeral session keys (PROTOCOL §3) |
| Modification in flight | A1, A2 | AEAD authentication; replay window |
| Long-term decryption (record-now-decrypt-later) | A1 | Forward secrecy via ephemeral DH; PQC roadmap (v2) |

### AS2 — User identity beyond the server

| Threat | Adversary | Mitigation |
|--------|-----------|------------|
| DNS leak | client-OS misconfiguration | Client documents required system configuration; future TUN integration enforces |
| WebRTC IP leak | application-layer | Out of Veil's scope — documented limitation |
| Server operator de-anonymisation | A3 (server hoster) | **Not defended.** Self-host model: server operator inherently sees user metadata. Documented in PRD §5.3 |

### AS3 — Server IP to the censor

| Threat | Adversary | Mitigation |
|--------|-----------|------------|
| Active probing reveals server | A1 | Reality-style SNI stealing: probes without valid auth tag receive real target site response (PROTOCOL §6.2) |
| Statistical fingerprint exposes server | A1 | Decoy traffic engine; statistical mimicry profiles; multi-transport rotation |
| ASN/IP-range scan flags server | A1 | Multi-IP server support; CDN fronting; edge-function topology |
| Bandwidth-pattern detection | A1 | Multi-server failover; client-side throttle option |

### AS4 — Fact of Veil use

| Threat | Adversary | Mitigation |
|--------|-----------|------------|
| TLS-in-TLS detection | A1 | HTTP/3 MASQUE primary; chunked WSS; uTLS fingerprint mimicry — all avoid raw nested TLS |
| ML flow classification | A1 | Statistical mimicry profiles; decoy traffic; per-user weighted SNI shards |
| Handshake timing fingerprint | A1 | Split handshake; jitter injection; matched to browser distributions |
| Static SNI enumeration | A1 | Dynamic SNI pool from Tranco snapshot; per-user weighted shards |

### AS5 — Server user database

| Threat | Adversary | Mitigation |
|--------|-----------|------------|
| Dump via exposed admin UI | external attacker, A1 | Default bind 127.0.0.1; in-product warning if 0.0.0.0; auth required |
| Dump via SQL injection | external attacker | Parameterised queries; no dynamic SQL construction; fuzz-tested |
| Dump via filesystem access | A3 (compromised hoster) | At-rest encryption optional; documented as residual risk |

### AS6 — Admin UI

| Threat | Adversary | Mitigation |
|--------|-----------|------------|
| Brute-force admin password | external | Rate limiting; bcrypt with high cost; optional WebAuthn |
| Session hijack | network MITM | TLS 1.3 only; HSTS; secure-cookie defaults; CSRF tokens |
| Default-exposed | misconfig | 127.0.0.1 default; installer-shown SSH-tunnel command; loud warning if user binds publicly |

### AS7 — Server static keypair

| Threat | Adversary | Mitigation |
|--------|-----------|------------|
| Disk read by hoster | A3 | At-rest encryption optional with passphrase prompt at server start; documented residual risk |
| Backup leak | external | Backup tooling encrypts by default; key never echoed in logs |

### AS8 — Per-user keypairs

| Threat | Adversary | Mitigation |
|--------|-----------|------------|
| Lost client device | A6, opportunistic | Per-user key revocation via admin UI; immediate effect on next handshake |
| Server-side dump | A3 | Only public keys stored server-side; private keys live only on client |

### AS9 — Release artifacts

| Threat | Adversary | Mitigation |
|--------|-----------|------------|
| Tampered binary in release | supply-chain | Reproducible builds; Sigstore signatures; SLSA Level 3 target |
| Tampered update | supply-chain | Update binary verifies signature before applying; rollback if new version crashes within 60s |
| Compromised release key | maintainer compromise | Sigstore keyless signing where possible; key custody plan documented |

---

## 6. Explicit non-goals

The following are deliberately not protected against. If you believe
one of these should be in scope, that is a request to expand the
threat model — open an issue.

1. **Anonymity from the Veil server operator.** This is fundamental to
   the self-host model. Use Tor for anonymity needs.
2. **Resistance to physical compromise of the client device.** Use
   full-disk encryption.
3. **Resistance to a malicious VPS operator who actively MITMs and
   coordinates with the censor.** Veil's E2E crypto limits damage,
   but if your hoster is the adversary, change hosters.
4. **Protection against zero-day exploits in Go runtime, quic-go,
   uTLS, or other dependencies.** Mitigated by timely updates and
   minimal attack surface, but not eliminated.
5. **Non-repudiation of network activity.** Veil does not authenticate
   the user to third parties.
6. **Hiding the existence of a server from a network observer who
   already knows the IP.** Reality-style stealth makes the IP look
   like a different service, but a determined adversary with full
   correlation power may still infer.

---

## 7. Residual risks

Even with all mitigations applied, the following residual risks
remain and should be communicated to users:

- **Single-server install with one IP** can be blocked by ASN/IP
  enumeration over time. Mitigation: rotate to a new VPS or use
  CDN fronting.
- **Decoy traffic still consumes bandwidth.** Users on metered
  connections may notice.
- **Mimicry profiles age.** A profile recorded from 2025 YouTube
  will eventually drift from current YouTube. Profiles must be
  refreshed periodically.
- **The client app itself is identifiable on a compromised device.**
  Veil does not provide plausible deniability of the app's presence
  on disk.
- **First-time deployment leaks operator identity to the VPS provider.**
  Pay anonymously where possible (Mullvad-style cash, Monero) if
  this matters.

---

## 8. Review cadence

- This document is reviewed before every minor version release.
- Adversary capabilities (especially A1) are reassessed quarterly
  based on observed censorship behaviour.
- Major changes to the model require a public RFC and at least
  14 days of community comment before merging.
