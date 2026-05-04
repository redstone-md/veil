# syntax=docker/dockerfile:1.7
#
# Veil Fly.io edge worker image.
#
# Two-stage build: golang:alpine compiles the static binary; the
# final layer is distroless/static so the deployed container has
# nothing in it that the worker does not actually need.

ARG GO_VERSION=1.22

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 \
    go build -trimpath -ldflags "-s -w" -o /out/veil-edge ./

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/veil-edge /usr/local/bin/veil-edge
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/veil-edge"]
