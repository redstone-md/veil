// Veil iOS — Swift wrapper over the libveil C ABI.
//
// Mirrors the safety story of sdks/veil-rs but in Swift: an opaque
// handle, RAII Stop on deinit, structured Event delivery via a
// Swift closure, errors thrown rather than returned as raw codes.
//
// libveil's event callback fires from an internal goroutine; we
// hop onto a serial DispatchQueue before invoking the user's
// closure so the Swift side never sees re-entrancy.

import Foundation

final class VeilSession {

    typealias EventHandler = (String) -> Void

    private var handle: VeilHandle = 0
    private let queue = DispatchQueue(label: "org.veil.event-queue")
    private var onEvent: EventHandler?
    // Retained box for the C user_data so the trampoline can find
    // its way back to Swift land without us having to maintain a
    // global map.
    private var userDataBox: VeilUserData?

    static func start(configText: String, onEvent: @escaping EventHandler) throws -> VeilSession {
        let s = VeilSession()
        s.onEvent = onEvent

        let h: VeilHandle = configText.withCString { cstr in
            return veil_create(cstr)
        }
        guard h != 0 else { throw VeilError.createFailed }
        s.handle = h

        // Box `s` into a heap-stable container the trampoline can
        // reconstruct via Unmanaged.
        let box = VeilUserData(owner: s)
        s.userDataBox = box
        let userPtr = Unmanaged.passUnretained(box).toOpaque()

        let rc = veil_start(h, veil_event_trampoline, userPtr)
        if rc != 0 {
            veil_destroy(h)
            throw VeilError.startFailed(rc)
        }
        return s
    }

    static func libraryVersion() -> String {
        guard let raw = veil_version_string() else {
            return #"{"version":"unavailable","commit":"","date":""}"#
        }
        defer { veil_free_string(raw) }
        return String(cString: raw)
    }

    func metricsJson() -> String {
        guard handle != 0, let raw = veil_get_metrics(handle) else {
            return #"{"running":false}"#
        }
        defer { veil_free_string(raw) }
        return String(cString: raw)
    }

    func stop() {
        if handle != 0 {
            _ = veil_stop(handle)
            veil_destroy(handle)
            handle = 0
        }
    }

    /// Pump packets read off the NEPacketTunnelProvider.packetFlow
    /// into libveil. The matching cgo-side entry point is to be
    /// added in `core/pkg/cgo/ne_ios.go`; until then this is a
    /// no-op so the skeleton compiles.
    func writePackets(_ packets: [Data], protocols: [NSNumber]) {
        // TODO(P4.6): wire to veil_ne_ingest_packets once the cgo
        // side ships a NetworkExtension-friendly TUN ingestion API.
        _ = packets; _ = protocols
    }

    deinit { stop() }

    // The C callback hops back into Swift through this trampoline.
    // We marshal the JSON string immediately and dispatch onto the
    // serial queue; the rest of Swift never observes the goroutine.
    fileprivate func deliver(jsonCString: UnsafePointer<CChar>?) {
        guard let jsonCString = jsonCString else { return }
        let json = String(cString: jsonCString)
        let handler = onEvent
        queue.async {
            handler?(json)
        }
    }
}

private final class VeilUserData {
    weak var owner: VeilSession?
    init(owner: VeilSession) { self.owner = owner }
}

@_cdecl("veil_event_trampoline")
private func veil_event_trampoline(
    _ kind: Int32,
    _ json: UnsafePointer<CChar>?,
    _ user: UnsafeMutableRawPointer?
) {
    guard let user = user else { return }
    let box = Unmanaged<VeilUserData>.fromOpaque(user).takeUnretainedValue()
    box.owner?.deliver(jsonCString: json)
    _ = kind // kind is duplicated inside the JSON payload; we forward the JSON verbatim.
}
