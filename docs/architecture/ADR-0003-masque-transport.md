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

We have prototyped against `quic-go/masque-go` v0.3 to validate the
shape; the headline observations are recorded below so the next
attempt does not re-walk the discovery.

### `masque-go` API as of v0.3

```go
// Client
type Client struct {
    TLSClientConfig *tls.Config
    QUICConfig      *quic.Config
}
func (c *Client) DialAddr(ctx, proxyTemplate, target) (net.PacketConn, *http.Response, error)

// Server
type Proxy struct { /* unexported */ }
func (s *Proxy) Proxy(w http.ResponseWriter, r *Request) error
func (s *Proxy) ProxyConnectedSocket(w, _ *Request, conn *net.UDPConn) error
```

### Architectural shape that follows from that API

`DialAddr` returns a `net.PacketConn`, not a `net.Conn`. CONNECT-UDP
is fundamentally a datagram tunnel; to give the rest of the Veil
stack a byte-stream `transport.Conn`, the natural approach is to
**run an inner QUIC session over the PacketConn**:

```
+-----------------+   inner Noise-encrypted VWP/1
| Veil session    |   (on top of an inner QUIC stream)
+-----------------+
| inner QUIC      |   quic-go session driven over the PacketConn
+-----------------+
| MASQUE          |   masque-go Client.DialAddr / Proxy.Proxy
+-----------------+
| HTTP/3 + TLS    |   quic-go/http3
+-----------------+
| outer QUIC      |   quic-go session to the proxy
+-----------------+
```

This is **nested QUIC**: outer QUIC carries HTTP/3 carrying CONNECT-
UDP capsules carrying inner QUIC datagrams carrying inner QUIC
streams carrying Noise XK carrying VWP/1. Each outer/inner layer
has its own congestion control, retransmission, and TLS handshake.
Double-encryption and double-CC are real costs to factor in before
recommending MASQUE as anyone's primary transport.

### Server side (when we ship)

- Bind `quic-go/http3.Server` on the configured port.
- Register an HTTP handler at `cfg.Path` (default `/masque`,
  randomised per deployment) that parses CONNECT-UDP via
  `masque.ParseRequest` against an URI template.
- For each accepted request, dial an internal UDP loopback that
  hosts our existing QUIC-Noise listener; hand the loopback's
  `*net.UDPConn` to `Proxy.ProxyConnectedSocket` so the masque
  layer pumps datagrams between the HTTP/3 capsule stream and the
  inner QUIC ingress.
- The QUIC-Noise listener is unchanged; it sees a regular UDP
  flow on a high port.

### Client side (when we ship)

- Build a `masque.Client` with TLS config sourced from the WSS-
  family ACME path (so a Reality-fronted MASQUE deployment can
  reuse one cert).
- `DialAddr` to obtain a `net.PacketConn`.
- Construct a fresh `quic-go` session over that PacketConn,
  ALPN-negotiating the same way the bare-QUIC transport does.
- The resulting QUIC stream becomes the `transport.Conn` the
  rest of the client stack consumes.

### What is gating the ship

- `quic-go/masque-go` is at v0.3, and `quic-go` itself flags the
  HTTP/3 + datagram surface as experimental. Pinning to a single
  pair of revisions is a manageable cost; chasing breaking changes
  every minor release is not.
- We need a credible **uTLS-shaped HTTP/3 ClientHello** for the
  outer connection or the cover story is weakened relative to
  Reality. `refraction-networking/uquic` exists but is also pre-1.0
  and not yet plug-and-play with `masque-go`.
- The nested-QUIC stack needs end-to-end perf benchmarking; if the
  steady-state throughput penalty is large enough to make MASQUE
  unattractive for everyone except the most paranoid users, the
  feature is not worth the maintenance burden.

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
