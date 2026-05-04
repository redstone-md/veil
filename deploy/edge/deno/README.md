# Veil — Deno Deploy edge worker

A reference edge backend for Veil deployments that want their
public TLS endpoint to live behind a CDN-fronted proxy rather than
on a single VPS IP. The worker accepts WSS upgrades and forwards
the binary websocket frames to an origin Veil server over a raw
TCP connection.

## Why

A bare VPS deployment exposes one IP that can be permanently
blocked once the censor identifies it. Hosting the public endpoint
on Deno Deploy means the inbound IP is shared with thousands of
unrelated tenants; blocking it costs the censor measurable
collateral damage to legitimate traffic.

See [ADR-0004](../../../docs/architecture/ADR-0004-edge-backends.md)
for the full rationale and the trust model.

## Trust

The edge worker handles only AEAD-encrypted ciphertext (the Veil
session above the WSS layer is end-to-end encrypted with Noise XK).
The worker:

- never holds the server's static private key,
- never sees plaintext client traffic,
- never reads the user database,
- can however log the inbound IP, the SNI, and the timing of every
  connection — the existing Veil threat model already names the
  edge provider as adversary "A5" with these capabilities.

## Deploy

1. Sign in to https://dash.deno.com/ and create an empty project.
2. Install the Deno CLI and `deployctl`.
3. From this directory, set the project slug in `deno.json` (the
   `deploy` task's `--project` flag) and run:

   ```bash
   VEIL_ORIGIN_HOST=vps.example.com \
   VEIL_ORIGIN_PORT=18444 \
   VEIL_PATH=/api/sync \
   deno task deploy
   ```

4. Set the same env vars in the Deno Deploy dashboard so the
   worker keeps them across runs.
5. Update your client's YAML to point at the project's
   `*.deno.dev` host name; keep the original VPS as a fall-back
   server entry in case the edge tier is unavailable.

## Limits

Deno Deploy's free tier is generous but finite. As of this
writing: 100 K req/day, 100 GB outbound/month per project. Heavy
users should plan to scale to a paid tier or to the Fly.io variant
(landing in Phase 5.5 alongside MASQUE).
