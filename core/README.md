# veil/core

The Veil protocol core: a single Go binary that acts as server,
client, admin server, and CLI.

This is the implementation of the [Veil Wire Protocol v1](../docs/PROTOCOL.md).

## Layout

```
cmd/veil/                 main entry point
internal/
  config/                 config file parsing and validation
  crypto/                 Noise XK handshake, AEAD, key rotation
  transport/              transport adapters (QUIC, TLS-Reality, WSS, MASQUE)
  dpi/                    SNI pool, decoy engine, mimicry profiles, uTLS
  proxy/                  SOCKS5 / HTTP local proxies for clients
  admin/                  embedded HTTP admin server
  users/                  user CRUD, quota, persistence
  metrics/                Prometheus exporter, slog setup
  update/                 self-update with signature verification
pkg/cgo/                  C-API exposed as a shared library
```

`internal/` packages are not importable outside the module by Go's
visibility rules; this is intentional. Stable APIs for third-party
consumers live in `pkg/cgo/` and the language SDKs under `../sdks/`.

## Build

```
go build -o ../bin/veil ./cmd/veil
```

## Test

```
go test ./...
```

## Status

Pre-alpha. See the project [README](../README.md) for milestone tracking.
