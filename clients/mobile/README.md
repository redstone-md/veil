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

The final remaining piece is the tun2socks engine itself: the
`core/internal/mobile` package models the fd + callback ingestion
shapes and the lifetime, but packets currently queue and drop
pending a gVisor or `xjasonlyu/tun2socks` integration. Until that
lands, the SOCKS5 listener inside the session is reachable from
inside the tunnel (handy for tests + apps that opt into a SOCKS
proxy explicitly) but full system-traffic interception is not yet
exercised end-to-end.
