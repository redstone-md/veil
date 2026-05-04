# ADR-0002: TLS-Reality as the high-priority anti-probe transport

**Status:** Accepted (skeleton landed; functional implementation deferred)
**Date:** 2026-05-04
**Deciders:** Initial maintainer

## Context

Phase 2's anti-DPI work has to address two distinct censor capabilities:

1. **Passive flow classification** — ML inferences from packet sizes,
   timings, and SNI distribution. Mitigations: uTLS fingerprint mimicry,
   SNI pool, decoy traffic, statistical mimicry profiles.
2. **Active probing** — the censor connects to a suspect IP, presents
   a TLS ClientHello with the SNI it observed in real traffic, and
   compares the response to what the legitimate target would have
   returned. If the response differs, the IP is flagged.

The mitigations for (1) are useful but insufficient against (2). An
endpoint can be flagged in seconds once a probe is targeted at it.

The community-developed XTLS-Reality protocol addresses (2). The
server transparently proxies any TLS handshake whose authentication
extension does not validate to the real target host. From the
censor's perspective, probing the IP returns the genuine target site,
indistinguishable from a benign reverse proxy.

Reality is the de-facto SOTA in 2025/2026 for production
anti-censorship VPN deployments in CN, RU, and IR.

## Decision

We adopt TLS-Reality as a first-class Veil transport.

In this revision we land:

- A spec stub at `internal/transport/realitytr/` describing the
  structures and the packaged behaviour.
- Configuration plumbing: `config.TransportReality` is recognised as a
  type and produces a "not yet implemented" error so users see a clear
  signal rather than a silent absence.
- This ADR documenting the decision and design intent.

We **defer** the functional implementation to a dedicated work
sequence because Reality requires careful cryptographic and TLS
plumbing that is best done with focused review.

## Implementation plan (for the next revision)

1. **Wire format:** mirror the published XTLS-Reality v1.5 wire layout
   so we can interoperate with sing-box / xray-core test corpora as a
   sanity check.
2. **Server**:
   - Listen TCP on the configured port.
   - For each connection: parse the incoming ClientHello (no `crypto/tls`
     state machine, just the byte layout) up to and including the TLS
     extensions.
   - Look for the Veil auth extension (custom extension type); if
     present, pull the X25519 ephemeral and verify a tag computed from
     `HKDF(serverStaticPriv * clientEphemeralPub)`.
   - **Auth tag valid** → terminate TLS ourselves with a freshly
     forged certificate whose chain claims to belong to the SNI; once
     TLS finishes, run the standard Veil session pipeline on the inner
     channel.
   - **Auth tag absent or invalid** → splice the entire TCP connection
     to `targetSNI:443`, producing a faithful reverse-proxy view of
     the real site.
3. **Client**:
   - Open TCP to the Veil server.
   - Construct a ClientHello carrying the configured SNI (drawn from
     the SNI pool) and our auth extension.
   - Drive the TLS handshake to completion; inside the established
     TLS, run the Noise XK + VWP/1 stack.
4. **Cert generation**: ECDSA P-256, SAN matching the SNI, validity
   window mimicking Let's Encrypt 90-day windows.
5. **Tests**:
   - Round-trip handshake (auth valid).
   - Probe handshake (auth invalid) — verify byte-for-byte response is
     what the real target would have produced (recorded fixture).
   - Replay attack — repeated auth tags must be rejected by a
     server-side window.

## Alternatives considered

- **Shadow-TLS** — earlier generation of the same idea; superseded by
  Reality which has stronger probe resistance and an active
  contributor base. Not adopted.
- **Native TLS with ACME-issued certs** — works against passive DPI
  but trivially fails active probing because the certificate identifies
  the server as itself, not as the SNI. Already covered by the WSS
  transport for users who do not need probe resistance.
- **Domain fronting via CDN** — viable in some markets but blocked
  outright in RU/CN (where most CDN families are either filtered or
  non-cooperative). Will be added as an *additional* topology, not as
  a replacement for Reality.

## Consequences

### Positive

- Reality directly addresses the most-feared adversary capability
  (active probing) and is the gating feature for serious deployment
  in RU and CN.
- Fits within the existing `transport.Listener` / `transport.Dialer`
  abstractions; no upper-layer changes required.
- Implementing it ourselves (rather than embedding xray-core) keeps
  the dependency surface small and lets us audit the whole stack.

### Negative / accepted trade-offs

- Reality requires careful TLS-byte-level handling. A bug here
  silently weakens the cover story and is hard to detect from the
  outside.
- The server needs outbound network access to the real target for the
  fall-through case. Operators on heavily egress-filtered networks
  must whitelist their target SNI.
- Cert forgery raises Web PKI eyebrows even though it never reaches a
  trust store; we will document loudly that the forged cert is for
  the local TLS termination only.

### Reversibility

- Adopted as one transport among many; if the implementation cost
  becomes unsupportable we can deprecate without disturbing QUIC and
  WSS users.
