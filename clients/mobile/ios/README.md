# iOS — Veil PacketTunnelProvider

This directory holds the Apple-platforms glue between the React Native
UI in `clients/mobile/` and `libveil`.

## Layout

```
ios/
└── PacketTunnelProvider/
    ├── PacketTunnelProvider.swift   ← NEPacketTunnelProvider subclass
    ├── VeilSession.swift            ← Swift wrapper over the C ABI
    ├── Veil-Bridging-Header.h       ← Re-exports core/pkg/cgo/include/veil.h
    ├── Info.plist                   ← NetworkExtension target plist
    └── PacketTunnel.entitlements    ← App Group + packet-tunnel-provider
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

Phase 4.6 v0 — file layout and the PacketTunnelProvider/VeilSession
skeleton are in place. The packetFlow ↔ libveil pump connects up
alongside `core/pkg/cgo/ne_ios.go`, the iOS-specific cgo entry
point that maps NetworkExtension's `[Data]` packet shape onto a
form libveil's TUN ingestion API can consume.
