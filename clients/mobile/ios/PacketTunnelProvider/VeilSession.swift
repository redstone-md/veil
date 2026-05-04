// Veil iOS — Swift wrapper over the libveil C ABI.
//
// Mirrors the safety story of sdks/veil-rs but in Swift: an opaque
// handle, RAII Stop on deinit, structured Event delivery via a
// Swift closure, errors thrown rather than returned as raw codes.
//
// libveil's event callback fires from an internal goroutine; we hop
// onto a serial DispatchQueue before invoking the user's closure so
// the Swift side never sees re-entrancy.
//
// Packet flow: NEPacketTunnelProvider.packetFlow.readPackets gives
// us inbound IP packets in a Swift-array shape; we forward each one
// through veil_ne_ingest_packet. libveil produces outbound packets
// asynchronously and hands them back through an emit callback this
// file installs at start time; the callback shovels them into
// packetFlow.writePackets.

import Foundation
import NetworkExtension

final class VeilSession {

    typealias EventHandler = (String) -> Void
    typealias PacketEmitter = (Data, NSNumber) -> Void

    private var handle: VeilHandle = 0
    private let queue = DispatchQueue(label: "org.veil.event-queue")
    private var onEvent: EventHandler?
    private var onEmit: PacketEmitter?
    // Retained box for the C user_data so the trampolines can find
    // their way back to this Swift instance without us having to
    // keep a global registry.
    private var userDataBox: VeilUserData?

    static func start(
        configText: String,
        onEvent: @escaping EventHandler,
        onEmit: @escaping PacketEmitter
    ) throws -> VeilSession {
        let s = VeilSession()
        s.onEvent = onEvent
        s.onEmit = onEmit

        let h: VeilHandle = configText.withCString { cstr in
            return veil_create(cstr)
        }
        guard h != 0 else { throw VeilError.createFailed }
        s.handle = h

        let box = VeilUserData(owner: s)
        s.userDataBox = box
        let userPtr = Unmanaged.passUnretained(box).toOpaque()

        let rc = veil_ne_start(h, veil_event_trampoline, veil_emit_trampoline, userPtr)
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

    /// Pump one batch of packets read off NEPacketTunnelFlow into
    /// libveil. Each Data is one IP packet; the matching protocol
    /// in `protocols` carries AF_INET (2 on Apple platforms) or
    /// AF_INET6 (30) — we collapse those into the family value
    /// libveil expects (4 for IPv4, 6 for IPv6).
    func writePackets(_ packets: [Data], protocols: [NSNumber]) {
        guard handle != 0 else { return }
        for (idx, packet) in packets.enumerated() {
            let proto = idx < protocols.count ? protocols[idx].int32Value : Int32(2)
            let family: Int32 = (proto == 30) ? 6 : 4
            packet.withUnsafeBytes { (raw: UnsafeRawBufferPointer) in
                guard let base = raw.baseAddress?.assumingMemoryBound(to: UInt8.self),
                      raw.count > 0 else { return }
                _ = veil_ne_ingest_packet(handle, base, Int32(raw.count), family)
            }
        }
    }

    deinit { stop() }

    // The C callbacks hop back into Swift through these methods.
    fileprivate func deliverEvent(jsonCString: UnsafePointer<CChar>?) {
        guard let jsonCString = jsonCString else { return }
        let json = String(cString: jsonCString)
        let handler = onEvent
        queue.async { handler?(json) }
    }

    fileprivate func deliverPacket(data: UnsafePointer<UInt8>?, len: Int32, family: Int32) {
        guard let data = data, len > 0 else { return }
        let buffer = UnsafeBufferPointer(start: data, count: Int(len))
        let copy = Data(buffer: buffer)
        let proto = NSNumber(value: family == 6 ? Int32(30) : Int32(2))
        let emit = onEmit
        queue.async { emit?(copy, proto) }
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
    box.owner?.deliverEvent(jsonCString: json)
    _ = kind // duplicated inside the JSON payload
}

@_cdecl("veil_emit_trampoline")
private func veil_emit_trampoline(
    _ data: UnsafePointer<UInt8>?,
    _ len: Int32,
    _ family: Int32,
    _ user: UnsafeMutableRawPointer?
) {
    guard let user = user else { return }
    let box = Unmanaged<VeilUserData>.fromOpaque(user).takeUnretainedValue()
    box.owner?.deliverPacket(data: data, len: len, family: family)
}

enum VeilError: LocalizedError {
    case missingConfig
    case createFailed
    case startFailed(Int32)

    var errorDescription: String? {
        switch self {
        case .missingConfig:        return "missing veil_config in providerConfiguration"
        case .createFailed:         return "veil_create returned 0; configuration rejected"
        case .startFailed(let rc):  return "veil_start returned \(rc)"
        }
    }
}
