# Android — Veil VpnService

This directory holds the Android-specific glue between the React
Native UI in `clients/mobile/` and `libveil`.

## Layout

```
android/
├── app/
│   └── src/main/
│       ├── AndroidManifest.xml      ← VpnService + activity declarations
│       ├── java/org/veil/mobile/
│       │   ├── VeilVpnService.kt    ← TUN owner + libveil session
│       │   ├── VeilBridgeModule.kt  ← RN ↔ service bridge
│       │   └── VeilBridgePackage.kt ← ReactPackage registration
│       ├── jniLibs/<abi>/libveil.so ← built from core/pkg/cgo
│       └── res/values/strings.xml
```

## Build prerequisites

1. **libveil for Android.** Cross-compile each ABI you ship:

   ```bash
   cd <repo>/core
   ANDROID_NDK=$HOME/Android/Sdk/ndk/26.1.10909125
   for abi in arm64-v8a armeabi-v7a x86_64; do
     case $abi in
       arm64-v8a)   target=aarch64-linux-android   cc=$ANDROID_NDK/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android24-clang ;;
       armeabi-v7a) target=armv7-linux-androideabi cc=$ANDROID_NDK/toolchains/llvm/prebuilt/linux-x86_64/bin/armv7a-linux-androideabi24-clang ;;
       x86_64)      target=x86_64-linux-android    cc=$ANDROID_NDK/toolchains/llvm/prebuilt/linux-x86_64/bin/x86_64-linux-android24-clang ;;
     esac
     CGO_ENABLED=1 GOOS=android GOARCH=${target%%-*} CC=$cc \
       go build -buildmode=c-shared \
         -o ../clients/mobile/android/app/src/main/jniLibs/$abi/libveil.so \
         ./pkg/cgo
   done
   ```

2. **React Native autolinking** picks up `VeilBridgePackage` if the
   parent project's `react-native.config.js` includes
   `clients/mobile/android` in its module search. Bare Expo
   projects do this automatically.

3. **Permissions.** The user must grant the Android system VPN
   consent dialog the first time `start()` runs. The bridge wires
   that into `VpnService.prepare()` + `startActivityForResult()`.

## Status

Phase 4.6 — VpnService, the React Native bridge, and the JNI symbols
(`nativeStart`, `nativeStop` in `core/pkg/cgo/jni_android.go`) are in
place. The Kotlin `nativeStart` calls into
`Java_org_veil_mobile_VeilVpnService_nativeStart`, which in turn
forwards to `veil_create` + `veil_mobile_start_with_tun` — taking
ownership of the TUN fd handed in by `VpnService.Builder.establish()`.

The TUN ↔ SOCKS5 forwarder runs through
[`xjasonlyu/tun2socks/v2/engine`](https://github.com/xjasonlyu/tun2socks)
inside `core/internal/mobile/tun_pipe.go`. The engine takes the TUN
fd `VpnService.Builder.establish()` returns and dials TCP / UDP
flows through the per-session SOCKS5 listener (`127.0.0.1:1080` by
default). UDP timeout, MTU and the engine log level are at their
sensible defaults; see `tun_pipe.go` for the knobs.

Constraints: tun2socks's engine package keeps process-wide
singleton state, so only one Veil tunnel runs in an Android
process at a time. The mobile clients only ever start one tunnel
per app, so this is not a usability limitation.
