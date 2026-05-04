# Real-world verification

Snapshot of the first end-to-end run of the Veil stack against a
real public VPS, captured during pre-alpha development.

This document is not a substitute for the formal external audit
(see [docs/AUDIT_PREP.md](AUDIT_PREP.md)); it is a developer-facing
sanity record so future maintainers can compare against the same
shape of test when they revisit a transport or change the
data-path.

The exact VPS used for this run has been retired; numbers are
preserved for reference. Reproducing the run requires standing up
your own VPS — the commands at the bottom of the file walk
through that bring-up.

## Test bench

| | |
|---|---|
| Date | 2026-05-04 |
| Veil revision | dev pre-alpha |
| Veil binary build | `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 -trimpath -ldflags "-s -w"` |
| Server | Debian 13 (trixie), kernel 6.12.x, KVM VPS, 1 GiB RAM, 15 GiB disk |
| Server location | Northern Europe, single IPv4 (no IPv6) |
| Server hosting | Mid-tier shared VPS provider (~$5/mo class) |
| Client | Local development machine, Central Europe |

The Veil server ran as a `systemd` unit (`veil.service`) with the
hardening flags from [`docs/INSTALL.md`](INSTALL.md):
`AmbientCapabilities=CAP_NET_BIND_SERVICE`, `NoNewPrivileges=true`,
`ProtectSystem=strict`, `ReadWritePaths=/var/lib/veil`,
`ProtectHome=true`, `PrivateTmp=true`, `LimitNOFILE=65535`.

## Server-side raw bandwidth

Baseline egress of the VPS itself, measured with `curl` against
public Hetzner endpoints. Numbers are end-to-end including TLS
handshake.

| Endpoint | Throughput |
|----------|------------|
| `hel1-speed.hetzner.com/100MB.bin` (Helsinki) | **587 Mbps** (73.4 MiB/s, 1.43 s) |
| `ash-speed.hetzner.com/100MB.bin` (Amsterdam) | **164 Mbps** (20.5 MiB/s, 5.10 s) |

The VPS' egress port is happy to push hundreds of Mbps to nearby
peering points; geographically distant sites (Amsterdam) lose
~70 % of that.

## Client throughput, single stream

Same client, same Hetzner targets, `curl` over a SOCKS5 tunnel
backed by a Veil session over the **Reality** transport.

| Path | Direct | Via Veil | Overhead |
|------|--------|----------|----------|
| Hetzner Amsterdam (100 MiB) | **28.9 Mbps** (3.62 MiB/s) | **21.7 Mbps** (2.71 MiB/s) | ≈25 % |
| Hetzner Helsinki (100 MiB)  | **43.6 Mbps** (5.45 MiB/s) | **19.8 Mbps** (2.48 MiB/s) | ≈55 % |

The Helsinki overhead is dominated by the extra client → server
hop (latency, not encryption). Single-stream throughput around
20 Mbps is consistent with a real VPN of this generation.

## Concurrent multiplex

30 parallel `curl` calls over **one** Reality session, each
downloading a 1 MiB Cloudflare blob. All 30 returned `200 OK`. Per-
stream throughput ranged from 109 KiB/s to 2.7 MiB/s as the tunnel
bandwidth was time-shared across the streams; the slowest finished
in 9.8 s, giving an aggregate throughput of ≈24 Mbps that matches
the single-stream baseline. The session multiplexer absorbed all 30
opens cleanly (server log: streams `1, 3, 5, …, 39` open within 200
ms of each other) and tore them down without leaks.

## Multi-transport

The same server then bound all three implemented adapters
simultaneously:

| Transport | Listen | Test |
|-----------|--------|------|
| Reality   | TCP/443  | `curl https://example.com` via SOCKS5 → **200 OK in 97 ms** |
| WSS       | TCP/8443 (path `/api/sync`, self-signed) | **200 OK in 96 ms** |
| QUIC      | UDP/8444 | **200 OK in 100 ms** |

End-to-end latency is identical across the three; the VPS hosting
provider does not throttle UDP, so QUIC is a viable real-world
fall-back for clients on networks that filter low-numbered TCP
ports.

## Probe behaviour (Reality)

`openssl s_client -connect <vps>:443 -servername www.cloudflare.com`
without a valid auth tag returned the **real Cloudflare leaf
certificate** (Subject `CN=www.cloudflare.com`, Issuer
`Google Trust Services WE1`, identical `notBefore` /
`notAfter` to the genuine origin), and a follow-up
`curl https://www.cloudflare.com/ --resolve …:<vps-ip>` returned
the genuine Cloudflare HTML — including a `country` field in
Cloudflare's geo-detection block matching the **VPS's** location
(not the client's), because the splice path made the request from
the VPS itself.

A Reality probe of this VPS is therefore indistinguishable on the
TLS layer from a TCP-level reverse proxy in front of
`www.cloudflare.com`.

> **Note on `target_sni` choice.** This run used Cloudflare for
> simplicity. In threat models that include the Russian or Chinese
> national DPI, Cloudflare is not a good front because it is
> heavily blocked / DNS-poisoned in those networks. Real
> deployments should pick a `target_sni` that is itself reachable
> from the threat-model client locale — for example `www.microsoft.com`
> works in both RU and CN.

## What this run did NOT cover

- Long-running stability (hours-to-days under load).
- Targeted active-probing emulation (rapid-fire ClientHello replays,
  sequenced TLS extensions, etc).
- Adversarial multipath / CDN-fronted topologies.
- Real censorship vantage points (RU/CN test boxes).

These belong to the formal audit window, not to a developer-driven
sanity run.

## Reproducing this run

The exact configurations used live in this commit; the server-side
flow, paraphrased:

```bash
# Cross-build the binary:
cd core && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags "-s -w" -o ../bin/veil-linux-amd64 ./cmd/veil

# Upload + bootstrap (replace <vps> with your host):
scp ../bin/veil-linux-amd64 root@<vps>:/usr/local/bin/veil
ssh root@<vps> 'chmod +x /usr/local/bin/veil &&
  mkdir -p /etc/veil /var/lib/veil &&
  cat >/etc/veil/server.yaml <<YAML
transports:
  - type: reality
    listen: "0.0.0.0:443"
    target_sni: "www.microsoft.com"
    target_addr: "www.microsoft.com:443"
  - type: wss
    listen: "0.0.0.0:8443"
    path: "/api/sync"
  - type: quic
    listen: "0.0.0.0:8444"
static_key_path: "/var/lib/veil/server.key"
user_db_path:    "/var/lib/veil/users.db"
YAML
  veil admin user-create --db /var/lib/veil/users.db \
    --username admin --password "<choose>"
  veil user add --db /var/lib/veil/users.db --name <username>'
```

Followed by a systemd unit (see `docs/INSTALL.md`) and the client
configuration produced by `veil user show-config --transport reality
--server-pubkey <server-pub> --server-addr <vps>:443 --sni
www.microsoft.com --client-key-b64 <printed-by-add>`.
