# iOS — Veil PacketTunnelProvider

This directory holds the Apple-platforms glue between the React Native
UI in `clients/mobile/` and `libveil`.

## Layout

```
ios/
├── PacketTunnelProvider/            ← extension target
│   ├── PacketTunnelProvider.swift   ← NEPacketTunnelProvider subclass
│   ├── VeilSession.swift            ← Swift wrapper over the C ABI (ingest + emit)
│   ├── Veil-Bridging-Header.h       ← re-exports core/pkg/cgo/include/veil.h
│   ├── Info.plist                   ← NetworkExtension target plist
│   └── PacketTunnel.entitlements    ← App Group + packet-tunnel-provider
└── VeilBridge/                      ← host-app React Native module
    ├── VeilBridge.swift             ← NETunnelProviderManager wrapper
    └── VeilBridge.m                 ← RCT_EXTERN_MODULE glue
```

## Build prerequisites

1. **libveil for iOS.** Cross-compile a fat dylib that contains the
   device + simulator slices (or build them separately and merge
   with `lipo`):

   ```bash
   cd <repo>/core
   for sdk in iphoneos iphonesimulator; do
     case $sdk in
       iphoneos)         GOOS=ios GOARCH=arm64 SDK=iphoneos      ;;
       iphonesimulator)  GOOS=ios GOARCH=arm64 SDK=iphonesimulator ;;
     esac
     CGO_ENABLED=1 GOOS=ios GOARCH=arm64 \
       SDKROOT=$(xcrun --sdk $SDK --show-sdk-path) \
       CC="xcrun --sdk $SDK clang -arch arm64" \
       go build -buildmode=c-shared \
         -o ../clients/mobile/ios/PacketTunnelProvider/libveil-$sdk.dylib \
         ./pkg/cgo
   done
   ```

2. **Xcode target setup.** The bundle identifiers, signing
   capabilities, and App Group come from your Apple Developer
   account; this skeleton declares the bundle structure but the
   project must be wired by hand the first time:

   * Add a NetworkExtension target named `PacketTunnelProvider` to
     the host React Native project.
   * Set its bundle ID to `org.veil.mobile.PacketTunnel`.
   * Enable the **Network Extensions** capability and check
     **Packet Tunnel**.
   * Enable **App Groups** with `group.org.veil.mobile`.
   * Add `libveil-iphoneos.dylib` (and `libveil-iphonesimulator.dylib`
     under a separate configuration) to **Link Binary With Libraries**.
   * Add a **Copy Files** build phase, destination = Frameworks,
     containing the dylib.
   * Set `Objective-C Bridging Header` to
     `PacketTunnelProvider/Veil-Bridging-Header.h`.

3. **Host-app side.** The React Native bridge (to be added) uses
   `NETunnelProviderManager` to install / start / stop the tunnel
   and `sendProviderMessage` for metrics / version queries.

## Status

Phase 4.6 — end-to-end functional. PacketTunnelProvider, VeilSession,
the host-app VeilBridge module, and the full tun2socks pipe are all
wired:

  * Inbound (OS → libveil): `packetFlow.readPackets` →
    `veil_ne_ingest_packet` → `CallbackPipe.Ingest` →
    `channel.Endpoint.InjectInbound` → gVisor netstack →
    `tunnel.T().HandleTCP/UDP` → SOCKS5 dial against the per-session
    listener.

  * Outbound (libveil → OS): SOCKS5 reply → tunnel processor →
    netstack → `channel.Endpoint.ReadContext` →
    `@_cdecl("veil_emit_trampoline")` → `packetFlow.writePackets`.

The CallbackPipe builds its own gVisor stack via
`xjasonlyu/tun2socks/v2/core.CreateStack` with our channel.Endpoint
as the LinkEndpoint, then plumbs `tunnel.T()` and the SOCKS5 dialer
in for transport-layer forwarding. tun2socks's internal state is
process-wide, so a `pipeActive` mutex prevents the iOS path and the
Android FDPipe from coexisting; mobile clients only ever start one
tunnel per process so this is not a usability limitation.
