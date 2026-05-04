# Veil server — Docker Compose

This directory contains a minimal recipe for running a Veil server
in a container, suitable for a single-host VPS deployment.

## Quick start

```bash
# 1. Prepare configuration files
cp server.example.yaml server.yaml
cp authorized_keys.example authorized_keys

# 2. Start the server (builds the image on first run)
docker compose up -d
docker compose logs -f veil

# 3. Read the server's public key (give this to clients along with
#    the host:port the server is reachable at)
docker compose exec veil cat /var/lib/veil/server.key | sed -n '2p'
```

## Adding clients

1. On a client machine, run `veil connect --config client.yaml` once;
   it generates a fresh keypair and prints the client public key on
   the first log line.
2. Append that public key as a single line to `authorized_keys` in
   this directory.
3. Reload the server: `docker compose restart veil`.

(Phase 2 will replace the manual restart with a runtime reload signal.)

## Image source

The image is built from the repository root using
`deploy/docker/Dockerfile`. To target a specific version, build
manually:

```bash
docker build \
  --build-arg VERSION=v0.2.0-rc1 \
  --build-arg COMMIT=$(git rev-parse --short HEAD) \
  --build-arg DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
  -t veil:local \
  -f deploy/docker/Dockerfile \
  ../..
```

## Storage

The `veil-state` Docker volume holds the server's long-term Noise
static keypair. **Back this up** before you destroy the volume; if
the server keypair changes, every client must update its
`server_static_key_b64` entry.

## Ports

By default the container exposes UDP/18443. Override on the host with:

```bash
VEIL_PORT=443 docker compose up -d
```

(Binding to ports below 1024 may require additional capabilities
depending on your Docker runtime.)
