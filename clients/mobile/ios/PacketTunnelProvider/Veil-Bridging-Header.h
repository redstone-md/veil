// Veil iOS — Swift ↔ C bridging header.
//
// Re-exports the libveil C ABI so the Swift side of the
// PacketTunnelProvider extension can call into it without an
// extra clang module map.
//
// libveil ships as `libveil.dylib` inside the extension bundle.
// The Xcode target needs:
//
//   * SWIFT_OBJC_BRIDGING_HEADER set to this file
//   * libveil.dylib added to "Link Binary With Libraries"
//   * libveil.dylib copied into Frameworks/ via a "Copy Files"
//     build phase with destination = Frameworks
//   * a Run Script phase that strips arch slices not in
//     `<active arch>` from the dylib so App Store validation does
//     not trip on the simulator slice.

#ifndef Veil_Bridging_Header_h
#define Veil_Bridging_Header_h

#include "veil.h"

#endif /* Veil_Bridging_Header_h */
