// Veil iOS — PacketTunnelProvider.
//
// Runs in its own process (the NetworkExtension target) and owns
// the packet tunnel + the libveil session for the duration of the
// VPN connection. The host app (the React Native UI) talks to this
// provider through NETunnelProviderManager messages.
//
// libveil ships as `libveil.dylib` in the extension bundle. The
// `Veil` Swift wrapper at the bottom of this file calls into the
// C ABI declared by `core/pkg/cgo/include/veil.h`. The bridging
// header `Veil-Bridging-Header.h` re-exports the C symbols so
// Swift can call them directly.

import NetworkExtension
import os.log

class PacketTunnelProvider: NEPacketTunnelProvider {

    private let log = OSLog(subsystem: "org.veil.mobile.PacketTunnel", category: "tunnel")
    private var session: VeilSession?

    override func startTunnel(
        options: [String : NSObject]?,
        completionHandler: @escaping (Error?) -> Void
    ) {
        os_log("startTunnel", log: log, type: .info)

        // The host app passes the YAML/JSON config text inside the
        // protocol's providerConfiguration dictionary under the
        // key "veil_config".
        guard
            let proto = self.protocolConfiguration as? NETunnelProviderProtocol,
            let providerCfg = proto.providerConfiguration,
            let configText = providerCfg["veil_config"] as? String
        else {
            completionHandler(VeilError.missingConfig)
            return
        }

        // Standard packet-tunnel network settings: full-tunnel /0
        // routes, 10.42.0.2/24 intra-tunnel address, 1.1.1.1 DNS.
        // Tweak per deployment if you need split tunnel behaviour.
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "127.0.0.1")
        settings.ipv4Settings = NEIPv4Settings(
            addresses: ["10.42.0.2"],
            subnetMasks: ["255.255.255.0"]
        )
        settings.ipv4Settings?.includedRoutes = [NEIPv4Route.default()]
        settings.dnsSettings = NEDNSSettings(servers: ["1.1.1.1", "9.9.9.9"])
        settings.mtu = 1380

        setTunnelNetworkSettings(settings) { [weak self] error in
            if let error = error {
                os_log("setTunnelNetworkSettings failed: %{public}@",
                       log: self?.log ?? .default, type: .error, error.localizedDescription)
                completionHandler(error)
                return
            }
            do {
                let s = try VeilSession.start(
                    configText: configText,
                    onEvent: { [weak self] ev in
                        self?.forwardEvent(ev)
                    }
                )
                self?.session = s
                self?.startReadingPackets()
                completionHandler(nil)
            } catch {
                completionHandler(error)
            }
        }
    }

    override func stopTunnel(
        with reason: NEProviderStopReason,
        completionHandler: @escaping () -> Void
    ) {
        os_log("stopTunnel reason=%{public}d", log: log, type: .info, reason.rawValue)
        session?.stop()
        session = nil
        completionHandler()
    }

    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)?) {
        // Allows the host app to query metrics + library version
        // through NETunnelProviderSession.sendProviderMessage. The
        // wire shape is a tiny tag-byte + JSON; matches the keys in
        // the React Native bridge.
        guard let cmd = String(data: messageData, encoding: .utf8) else {
            completionHandler?(nil); return
        }
        switch cmd {
        case "metrics":
            let json = session?.metricsJson() ?? #"{"running":false}"#
            completionHandler?(json.data(using: .utf8))
        case "version":
            let json = VeilSession.libraryVersion()
            completionHandler?(json.data(using: .utf8))
        default:
            completionHandler?(nil)
        }
    }

    private func startReadingPackets() {
        // The packet flow is: read IP packets from packetFlow,
        // hand them to libveil's TUN-side ingestion entry point
        // (to be added in core/pkg/cgo/ne_ios.go), and write back
        // any packets libveil produces. Phase 4.6 v0 lands the
        // skeleton; the actual packetFlow ↔ libveil pump connects
        // up alongside the iOS-specific cgo entry point.
        packetFlow.readPackets { [weak self] packets, protocols in
            guard let self = self else { return }
            self.session?.writePackets(packets, protocols: protocols)
            self.startReadingPackets()
        }
    }

    private func forwardEvent(_ json: String) {
        // Surface the libveil event to the host app via
        // NEProvider's logger; the host app subscribes through
        // sendProviderMessage("subscribe") in a follow-up commit.
        os_log("event: %{public}@", log: log, type: .info, json)
    }
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
