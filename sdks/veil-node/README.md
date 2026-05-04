# @veil/node

Node.js bindings for the Veil VPN client.

This package wraps `libveil` (the Veil C ABI shared library) through
the safe Rust SDK in [`sdks/veil-rs`](../veil-rs/) and exposes a
small, JS-flavoured surface to Node consumers.

## Install

The package is shipped as a native addon (`.node`). At runtime it
expects two binaries to be reachable:

1. The native NAPI addon itself (`veil.<triple>.node`), produced by
   `napi build --release --platform`.
2. The Veil shared library (`libveil.so`, `libveil.dylib`, or
   `veil.dll`), produced by `go build -buildmode=c-shared` from
   [`core/pkg/cgo/`](../../core/pkg/cgo/). The dynamic loader must
   find it via `LD_LIBRARY_PATH`, `DYLD_LIBRARY_PATH`, the system
   library path, or by sitting next to the calling binary.

For local development:

```bash
# 1. Build libveil
cd <repo>/core
go build -buildmode=c-shared -o ../sdks/veil-node/libveil.so ./pkg/cgo

# 2. Build the NAPI addon
cd ../sdks/veil-node
npm install
npm run build:debug
```

## Use

```js
const { Veil, libraryVersion } = require("@veil/node");

console.log(JSON.parse(libraryVersion()));

const cfg = require("node:fs").readFileSync("client.yaml", "utf8");
const v = new Veil(cfg);

v.start((ev) => {
  switch (ev.type) {
    case 1: console.log("connected via", ev.transport, "→", ev.remote); break;
    case 2: console.log("disconnected"); break;
    case 3: console.error("error:", ev.message); break;
    case 4: console.log("traffic:", ev.bytes_tx, ev.bytes_rx); break;
    case 5: console.log("transport switch →", ev.transport); break;
  }
});

// ...later
const m = JSON.parse(v.metricsJson());
console.log("bytes:", m.bytes_tx, m.bytes_rx);

v.stop();
v.destroy();
```

## Threading

The runtime callback is invoked from a libveil-internal goroutine.
napi-rs's `ThreadsafeFunction` queues each event onto the Node event
loop, so your handler runs on the JS main thread. Do not block in
the handler — copy the payload into your application state and
return promptly.

## Status

Pre-alpha. The ABI is v1; minor releases of this package are
append-only within v1. See [`core/pkg/cgo/include/veil.h`](../../core/pkg/cgo/include/veil.h)
for the canonical surface description.
