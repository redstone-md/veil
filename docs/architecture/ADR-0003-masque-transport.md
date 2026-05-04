# ADR-0003: HTTP/3 MASQUE as the highest-stealth transport

**Status:** Accepted (skeleton landed; functional implementation deferred to Phase 6)
**Date:** 2026-05-04
**Deciders:** Initial maintainer

## Context

Phase 2 / Phase 2.5 give us QUIC, WSS, and Reality. They cover the
common censor adversary capabilities except one in particular:
**HTTP/3 endpoints that consume CONNECT-UDP requests via the MASQUE
extension look indistinguishable from any other modern web frontend
that proxies WebRTC or low-latency telemetry**. There is no
nested-TLS shape to fingerprint, no SessionID manipulation to spot,
no atypical handshake timing — only ordinary HTTP/3 stream traffic
to a domain that already serves browser users.

For deployments behind a real CDN or behind a third-party HTTP/3
front-end, MASQUE is therefore the single highest-stealth option
available without building bespoke infrastructure.

The IETF specification is mature: RFC 9298 (CONNECT-UDP) and the
underlying RFC 9297 (HTTP datagrams).

## Decision

We adopt MASQUE as a fourth first-class transport.

This revision lands:

- This ADR, capturing the design intent.
- A stub package at `internal/transport/masquetr/` that reserves
  the import path, surfaces a clean `ErrNotImplemented`, and
  documents the configuration shape the upcoming functional
  implementation will accept.
- Recognition of `config.TransportType` value `masque` so a
  misconfigured deployment fails fast with a clear pointer to this
  ADR rather than a confusing "unknown transport" error.

We **defer** the functional implementation because the upstream
Go MASQUE story is still consolidating: the most viable library is
`quic-go/masque-go`, which is itself pre-1.0 and depends on
`quic-go` features that are advertised as experimental. Landing a
serious implementation now would lock us into either a fork or
significant private patches.

## Implementation plan (for the next revision)

1. **Server**:
   - HTTP/3 listener built on `quic-go/http3` (already a transitive
     dependency via the QUIC adapter).
   - Handler binds the configured path (default `/masque`) and
     parses CONNECT-UDP capsules per RFC 9298.
   - Each accepted CONNECT-UDP session yields a UDP-flow handle
     that is wired into the existing transport.Conn machinery.
   - Authentication remains the Noise XK responder layer above; no
     transport-level token is required (the URL path can be
     randomised per deployment, which is sufficient against
     untargeted scanners).

2. **Client**:
   - HTTP/3 client (uTLS-shaped Hello via `refraction-networking/uquic`
     once that lands in stable, otherwise stdlib + a clear
     fingerprint warning) issues an extended-CONNECT for `:protocol = connect-udp`.
   - The capsule pipe becomes the transport.Conn the rest of the
     client stack consumes.

3. **Edge mode**:
   - MASQUE pairs naturally with the Edge backend story (ADR-0004):
     a Deno Deploy / Fly.io worker hosts the HTTP/3 frontend and
     forwards the inner traffic to an origin VPS over WSS or
     Reality. This is the deployment shape we expect most
     production users to choose once both pieces ship.

4. **Tests**:
   - End-to-end smoke against `quic-go/masque-go`'s reference
     server.
   - Negative test: requests without the Veil-defined path are
     served a generic 404 so the host does not self-fingerprint.

## Alternatives considered

- **Plain HTTP/3 streams + manual framing**. Cheaper to implement
  than MASQUE proper but produces a wire shape that does not match
  any real browser behaviour, which defeats the point.
- **Wireguard-over-UDP wrappers** (e.g. AmneziaWG handshake-shape
  obfuscation). Not in our family of choices: we already use
  Noise XK and a Noise-shaped handshake is happily disguised by
  Reality / WSS today.
- **gRPC tunnels**. Plausible for some adversaries but heavier
  dependency surface and a smaller deployment ecosystem than
  HTTP/3.

## Consequences

### Positive

- Highest-stealth transport in the family. Pairs with edge
  deployment to put Veil traffic behind a real CDN's HTTP/3
  frontend with no special server configuration on our side.
- Introduces no new authentication primitives; the Noise XK
  layer above is unchanged.
- Adds a transport that does not consume our own port allocations
  (the HTTP/3 endpoint can squat on the same `:443` as WSS thanks
  to ALPN).

### Negative / accepted trade-offs

- Upstream library volatility: shipping MASQUE before
  `quic-go/masque-go` reaches a stable surface means we either
  pin to a working revision and accept manual updates, or wait.
  This ADR locks the decision and accepts the wait.
- Implementing the CONNECT-UDP capsule machinery ourselves is
  ~500-1000 LoC of TLS-byte-level work, similar in complexity to
  the Reality listener we landed in Phase 2.5.

### Reversibility

- The transport sits behind the existing `transport.Listener` /
  `transport.Dialer` interfaces; if the implementation cost
  becomes unsupportable we can deprecate without disturbing the
  other adapters.
