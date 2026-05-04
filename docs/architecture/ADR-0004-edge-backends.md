# ADR-0004: Edge backends as a deployment topology

**Status:** Accepted (Deno Deploy reference worker landed; Fly.io variant deferred)
**Date:** 2026-05-04
**Deciders:** Initial maintainer

## Context

A bare VPS deployment of Veil — the Phase 0/1 default — has one
weakness Phase 2/2.5 cannot fully patch: a *single IP* identified
to the censor as a Veil server is one short jump from a permanent
block, regardless of how stealthily its TLS handshakes are shaped.

The architecture's intended escape hatch (PRD §7.4 Topology 3) is
**edge backends**: small workers running on a public CDN-or-edge
host (Deno Deploy, Fly.io, …) that accept inbound WSS traffic and
forward it to an origin VPS. From the censor's vantage point the
inbound IP is a CDN edge IP shared with thousands of other tenants;
blocking it costs them collateral damage to legitimate traffic.

Edge backends also enable a "no VPS at all" starter mode: a single
worker that exits to the open internet directly, suitable for
low-bandwidth use within the platform's free tier.

## Decision

We adopt edge backends as a first-class deployment topology and
ship reference worker implementations for the platforms users are
most likely to reach for.

This revision lands:

- A reference Deno Deploy worker
  (`deploy/edge/deno/`) that accepts WSS upgrades on a configured
  path and proxies the binary websocket frames to an origin host
  over a raw TCP connection. The origin runs an unmodified
  `veil serve` with a WSS transport bound to the inside.
- This ADR documenting the deployment shape, the trust model, and
  the upcoming Fly.io variant.

We **defer** the Fly.io variant (which buys us a full HTTP/3 path
once MASQUE — see ADR-0003 — lands) to the same revision that
makes MASQUE functional. The two pair naturally and deserve
shipping together.

## Trust model implications

The edge backend is a third-party tenant with the operator's
deployment scope; it sees:

- Every inbound TLS handshake, including SNI.
- Every byte of the inner WebSocket binary frames (which, in
  Veil's stack, are AEAD ciphertext from the Noise XK session
  above).

It does **not** see:

- The server's static Noise XK private key — that lives on the
  origin host.
- The plaintext client traffic — that is encrypted before the WSS
  layer forwards it through.
- The user database — that lives on the origin host.

The threat model already names "Edge-function provider" as
adversary A5; the edge worker therefore inherits the existing
mitigations (E2E ciphertext, no key material on edge, opt-in
bandwidth-shaping). No new cryptographic primitive is introduced.

## Configuration shape

A typical client config that uses an edge backend:

```yaml
servers:
  - type: wss
    addr: "veil-edge.deno.dev:443"     # CDN-fronted endpoint
    sni:  "veil-edge.deno.dev"
    path: "/ws"
    fingerprint: chrome
  - type: wss                          # fall-back: direct origin
    addr: "vps.example.com:443"
    sni:  "vps.example.com"
    path: "/ws"
server_static_key_b64: "..."
static_key_path: "client.key"
socks5_listen: "127.0.0.1:1080"
```

Server-side configuration is unchanged: the origin runs a normal
`veil serve` that listens on a non-public port; the worker is the
only thing that talks to it.

## Implementation plan (Fly.io variant)

The Deno Deploy worker handles the simple case (one Veil tenant
per Deno project). Fly.io is the variant for operators who want:

- HTTP/3 / MASQUE pairs naturally with Fly's edge proxy story.
- More bandwidth than the Deno free tier supports.
- Closer geographic proximity in regions Deno does not cover.

The Fly.io worker is essentially the same code in a small
deployable container. The `fly.toml` and Dockerfile land in
`deploy/edge/fly/` together with the MASQUE work.

## Alternatives considered

- **Cloudflare Workers** — historically the cleanest fit, but
  CF is blocked or hostile in the target markets that benefit
  most from this topology (RU since 2024, intermittently in CN
  and IR). The reference worker in this commit does not target
  CF; we will revisit if the regulatory picture changes.
- **Self-hosted Caddy / nginx in front** — works but requires the
  operator to already run real production infrastructure, which
  defeats the "no-ops starter" angle that motivates the Deno
  variant.
- **Tor pluggable transport** — overlapping community but
  fundamentally different threat model (anonymity vs. unblocking);
  out of scope.

## Consequences

### Positive

- Adds a topology that scales the IP-rotation and
  collateral-damage story without operator effort beyond OAuth
  + a `deno deploy` push.
- Compatible with every existing transport adapter; no protocol
  changes required.
- Reduces operator setup cost from "rent a VPS" to "click through
  a free-tier sign-up".

### Negative / accepted trade-offs

- Adds a third party to the trust chain (the edge provider). The
  existing threat-model mitigations cover the worst cases but do
  not eliminate them.
- Free tiers carry generous-but-finite bandwidth; users who lean
  heavily on the edge path will eventually need to graduate to a
  paid tier or to a self-run origin.
- Edge providers can ban accounts for TOS violation (high
  bandwidth, "tunnelling"). The reference worker is small enough
  to deploy under a fresh account if banned, but recurrent bans
  on the same operator email could become friction.
