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

Phase 4.6 v0 — the JS surface, profile + settings persistence, and
the platform-side tunnel skeletons are in place. Final-mile work:

* `core/pkg/cgo/jni_android.go` and `core/pkg/cgo/ne_ios.go` to
  expose libveil's TUN ingestion API to the platform shims.
* an Android `VeilBridgeModule.metricsJson` / `libraryVersion`
  implementation that calls into libveil rather than returning the
  current placeholder strings.
* an iOS host-app side bridge that wraps `NETunnelProviderManager`
  the same way the Android bridge wraps `VpnService.prepare()`.
