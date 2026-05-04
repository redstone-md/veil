# Veil mobile

React Native (Expo bare) client for Veil VPN — iOS + Android, sharing
a single JS UI on top of platform-specific tunnel implementations.

## Layout

```
clients/mobile/
├── App.js                     ← single-screen UI (status / profile / settings)
├── src/
│   ├── veil.js                ← native bridge
│   └── store.js               ← profile + settings persistence
├── android/                   ← VpnService + JNI bridge
└── ios/PacketTunnelProvider/  ← NetworkExtension + Swift bridge
```

## Build

The native code paths are platform-specific; see
[`android/README.md`](android/README.md) and
[`ios/README.md`](ios/README.md) for the per-platform prerequisites.

The JS layer is plain Expo bare:

```bash
cd clients/mobile
npm install
npx expo run:ios       # device / simulator
npx expo run:android   # device / emulator
```

## Architecture

The JS UI talks to a thin `src/veil.js` bridge that mirrors the
shape of `@veil/node`:

```js
import * as veil from "./src/veil";

await veil.start(configText);
const off = veil.onEvent((e) => { ... });
const m = JSON.parse(await veil.metricsJson());
await veil.stop();
```

On Android this dispatches into `VeilBridgeModule` →
`VeilVpnService` → `libveil.so` via JNI. On iOS it dispatches into
the React Native module → `NETunnelProviderManager` →
`PacketTunnelProvider` → `libveil.dylib` via the bridging header.

In both cases the tunnel runs in a separate OS-managed process so
the UI can be killed without dropping the connection.

## Status

Phase 4.6 — the JS surface, profile + settings persistence, and the
platform-side tunnel paths are wired end-to-end:

* `core/pkg/cgo/mobile.go` exposes `veil_mobile_start_with_tun`
  (Android, fd model) and `veil_ne_start` /
  `veil_ne_ingest_packet` / emit-callback (iOS, callback model).
* `core/pkg/cgo/jni_android.go` provides the JNI symbols the Kotlin
  `VeilVpnService.nativeStart` / `nativeStop` external functions
  bind to.
* `clients/mobile/ios/PacketTunnelProvider/VeilSession.swift` runs
  the packetFlow ↔ libveil pump using the new ingest + emit-callback
  API.
* `clients/mobile/ios/VeilBridge/` is the host-app React Native
  module wrapping `NETunnelProviderManager` (install / start / stop
  / sendProviderMessage for metrics + version) and surfacing
  NEVPNStatusDidChange notifications onto the JS event channel.

**Android** and **iOS** are both end-to-end functional through
`core/internal/mobile/tun_pipe.go`:

  * Android (FDPipe) wires `xjasonlyu/tun2socks/v2/engine` against
    the TUN fd `VpnService.Builder.establish()` returns. The engine
    builds an internal gVisor netstack pinned to that fd and dials
    each TCP / UDP flow through the per-session SOCKS5 listener.

  * iOS (CallbackPipe) builds its own gVisor netstack via
    `xjasonlyu/tun2socks/v2/core.CreateStack` with a
    `channel.Endpoint` as the LinkEndpoint, since
    NEPacketTunnelFlow exposes read/write callbacks rather than a
    fd. Ingest() injects packets into the netstack; an outbound
    goroutine pulls produced packets and emits them through the
    Swift-registered callback into `packetFlow.writePackets`.

tun2socks's internal state (`tunnel.T()`, `engine._defaultStack`)
is process-wide, so a package-level mutex prevents the two pipes
from coexisting. Mobile clients only ever run one tunnel per
process, so the constraint is not a usability limitation.
