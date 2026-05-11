# syntax=docker/dockerfile:1.7
#
# Veil server image.
#
# Multi-stage build:
#   1. Build the static `veil` binary inside the official Go image.
#   2. Copy it into a distroless base for a minimal final image.
#
# The image runs as a non-root user. Persistent state lives under
# /var/lib/veil and the configuration under /etc/veil.

ARG GO_VERSION=1.25

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src

# Cache module downloads in a separate layer.
COPY core/go.mod core/go.sum ./core/
RUN --mount=type=cache,target=/go/pkg/mod \
    cd core && go mod download

# Build.
COPY core/ ./core/
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd core && \
    CGO_ENABLED=0 \
    go build -trimpath \
      -ldflags "-s -w \
        -X github.com/redstone-md/veil/core/internal/buildinfo.Version=${VERSION} \
        -X github.com/redstone-md/veil/core/internal/buildinfo.Commit=${COMMIT} \
        -X github.com/redstone-md/veil/core/internal/buildinfo.Date=${DATE}" \
      -o /out/veil ./cmd/veil

# ---

FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.title="Veil VPN server"
LABEL org.opencontainers.image.source="https://github.com/redstone-md/veil"
LABEL org.opencontainers.image.licenses="Apache-2.0"

COPY --from=build /out/veil /usr/local/bin/veil

# /var/lib/veil holds long-term state (server static keypair, future
# user database). /etc/veil holds the YAML config and authorized_keys.
USER nonroot:nonroot
WORKDIR /var/lib/veil

EXPOSE 18443/udp
ENTRYPOINT ["/usr/local/bin/veil"]
CMD ["serve", "--config", "/etc/veil/server.yaml"]
