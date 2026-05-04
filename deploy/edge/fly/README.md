# Veil — Fly.io edge worker

A reference Veil edge backend that runs as a small Go container on
Fly.io's edge regions. Functionally equivalent to the
[Deno Deploy variant](../deno/) but intended for operators who:

- want geographic regions Deno Deploy does not cover,
- need more bandwidth than Deno's free tier permits,
- prefer the operational shape of a real container they can `ssh`
  into for debugging.

See [ADR-0004](../../../docs/architecture/ADR-0004-edge-backends.md)
for the rationale and the trust model that applies to every edge
deployment.

## Prerequisites

- A Fly.io account with `flyctl` installed and authenticated.
- An origin Veil server running with at least one WSS listener
  bound to a port reachable from Fly's network.

## Deploy

```bash
# 1. Create the app (one-time)
fly apps create veil-edge-yourname

# 2. Configure the origin via Fly secrets so the values are not
#    committed to source.
fly secrets set \
  VEIL_ORIGIN_HOST=vps.example.com \
  VEIL_ORIGIN_PORT=18444 \
  VEIL_PATH=/api/sync \
  --app veil-edge-yourname

# 3. Push the container
fly deploy --app veil-edge-yourname

# 4. Confirm the listener is up
fly logs --app veil-edge-yourname
```

The worker is then reachable at
`wss://veil-edge-yourname.fly.dev/<VEIL_PATH>`.

## Client config that uses both edge and origin

Put the edge first so the client prefers it; fall back to the
direct VPS if Fly is unreachable.

```yaml
servers:
  - type: wss
    addr: "veil-edge-yourname.fly.dev:443"
    sni:  "veil-edge-yourname.fly.dev"
    path: "/api/sync"
    fingerprint: chrome
  - type: wss
    addr: "vps.example.com:443"
    sni:  "vps.example.com"
    path: "/api/sync"
server_static_key_b64: "..."
static_key_path: "client.key"
socks5_listen: "127.0.0.1:1080"
```

## Trust

The Fly worker has the same trust posture as the Deno worker: it
handles AEAD ciphertext from the Noise XK session above, never
sees plaintext, never holds a key. The image is distroless / non-
root by default; no shell is included.

## Cost

Fly's free tier covers small experimental deployments. A single
shared-CPU 256 MiB machine and ~3 GB of bandwidth per month sits
inside the free allotment as of 2025; monitor your billing
dashboard if you expect higher load.
