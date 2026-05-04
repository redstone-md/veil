// Veil iOS — host-app RN bridge.
//
// Sits in the React Native host app (NOT the NetworkExtension
// process) and talks to the PacketTunnelProvider through
// NETunnelProviderManager. The JS API exposed to React Native
// matches src/veil.js exactly:
//
//   start(configText) → install (if first run) and start the tunnel
//   stop()            → stop the tunnel
//   metricsJson()     → ask the extension for metrics via
//                        sendProviderMessage
//   libraryVersion()  → ditto for version
//   addListener / removeListeners → required by NativeEventEmitter
//
// Tunnel events are surfaced through NEVPNStatusDidChange
// notifications rather than a custom event channel — that gives us
// connect / disconnect transitions for free without having to wire
// a separate IPC.

import Foundation
import NetworkExtension
import React

@objc(VeilBridge)
final class VeilBridge: RCTEventEmitter {

    private static let tunnelDescription = "Veil"
    private static let providerBundleID = "org.veil.mobile.PacketTunnel"
    private static let appGroup = "group.org.veil.mobile"
    private static let configKey = "veil_config"

    private var hasObservers = false

    override static func requiresMainQueueSetup() -> Bool { false }
    override func supportedEvents() -> [String]! { ["veil-event"] }
    override func startObserving() { hasObservers = true; subscribeStatus() }
    override func stopObserving()  { hasObservers = false; unsubscribeStatus() }

    @objc(start:resolver:rejecter:)
    func start(
        configText: String,
        resolver resolve: @escaping RCTPromiseResolveBlock,
        rejecter reject: @escaping RCTPromiseRejectBlock
    ) {
        loadOrCreateManager { result in
            switch result {
            case .failure(let err):
                reject("manager_load", "loading tunnel manager failed: \(err.localizedDescription)", err)
            case .success(let manager):
                guard let proto = manager.protocolConfiguration as? NETunnelProviderProtocol else {
                    reject("no_proto", "manager has no NETunnelProviderProtocol", nil)
                    return
                }
                var providerCfg = proto.providerConfiguration ?? [:]
                providerCfg[Self.configKey] = configText
                proto.providerConfiguration = providerCfg
                manager.protocolConfiguration = proto
                manager.isEnabled = true
                manager.saveToPreferences { saveErr in
                    if let saveErr = saveErr {
                        reject("save_pref", "saving tunnel preferences failed: \(saveErr.localizedDescription)", saveErr)
                        return
                    }
                    // saveToPreferences invalidates cached state; load
                    // again before starting so the freshly-written
                    // configuration takes effect.
                    manager.loadFromPreferences { loadErr in
                        if let loadErr = loadErr {
                            reject("reload_pref", "reload after save failed: \(loadErr.localizedDescription)", loadErr)
                            return
                        }
                        do {
                            try manager.connection.startVPNTunnel()
                            resolve(nil)
                        } catch {
                            reject("start", "startVPNTunnel failed: \(error.localizedDescription)", error)
                        }
                    }
                }
            }
        }
    }

    @objc(stop:rejecter:)
    func stop(_ resolve: @escaping RCTPromiseResolveBlock, rejecter reject: @escaping RCTPromiseRejectBlock) {
        loadOrCreateManager { result in
            switch result {
            case .failure(let err):
                reject("manager_load", err.localizedDescription, err)
            case .success(let manager):
                manager.connection.stopVPNTunnel()
                resolve(nil)
            }
        }
    }

    @objc(metricsJson:rejecter:)
    func metricsJson(_ resolve: @escaping RCTPromiseResolveBlock, rejecter reject: @escaping RCTPromiseRejectBlock) {
        sendProviderMessage("metrics".data(using: .utf8) ?? Data(), resolve: resolve, reject: reject)
    }

    @objc(libraryVersion:rejecter:)
    func libraryVersion(_ resolve: @escaping RCTPromiseResolveBlock, rejecter reject: @escaping RCTPromiseRejectBlock) {
        sendProviderMessage("version".data(using: .utf8) ?? Data(), resolve: resolve, reject: reject)
    }

    // --- internals ---

    private func sendProviderMessage(
        _ data: Data,
        resolve: @escaping RCTPromiseResolveBlock,
        reject: @escaping RCTPromiseRejectBlock
    ) {
        loadOrCreateManager { result in
            switch result {
            case .failure(let err):
                reject("manager_load", err.localizedDescription, err)
            case .success(let manager):
                guard let session = manager.connection as? NETunnelProviderSession else {
                    reject("no_session", "manager.connection is not NETunnelProviderSession", nil)
                    return
                }
                do {
                    try session.sendProviderMessage(data) { reply in
                        let s = reply.flatMap { String(data: $0, encoding: .utf8) }
                        resolve(s ?? #"{"running":false}"#)
                    }
                } catch {
                    reject("provider_msg", error.localizedDescription, error)
                }
            }
        }
    }

    private func loadOrCreateManager(_ completion: @escaping (Result<NETunnelProviderManager, Error>) -> Void) {
        NETunnelProviderManager.loadAllFromPreferences { managers, err in
            if let err = err {
                completion(.failure(err))
                return
            }
            // Pick the first manager pointing at our extension; if
            // none exists yet, create one. The host app installs at
            // most one Veil tunnel manager.
            let existing = (managers ?? []).first { mgr in
                guard let proto = mgr.protocolConfiguration as? NETunnelProviderProtocol else { return false }
                return proto.providerBundleIdentifier == Self.providerBundleID
            }
            if let existing = existing {
                completion(.success(existing))
                return
            }
            let mgr = NETunnelProviderManager()
            let proto = NETunnelProviderProtocol()
            proto.providerBundleIdentifier = Self.providerBundleID
            proto.serverAddress = "veil"
            proto.providerConfiguration = [:]
            mgr.protocolConfiguration = proto
            mgr.localizedDescription = Self.tunnelDescription
            mgr.isEnabled = true
            mgr.saveToPreferences { saveErr in
                if let saveErr = saveErr {
                    completion(.failure(saveErr))
                    return
                }
                mgr.loadFromPreferences { reloadErr in
                    if let reloadErr = reloadErr {
                        completion(.failure(reloadErr))
                    } else {
                        completion(.success(mgr))
                    }
                }
            }
        }
    }

    private func subscribeStatus() {
        NotificationCenter.default.addObserver(
            self,
            selector: #selector(statusChanged(_:)),
            name: .NEVPNStatusDidChange,
            object: nil
        )
    }

    private func unsubscribeStatus() {
        NotificationCenter.default.removeObserver(self, name: .NEVPNStatusDidChange, object: nil)
    }

    @objc private func statusChanged(_ note: Notification) {
        guard hasObservers, let conn = note.object as? NEVPNConnection else { return }
        // Map NEVPNStatus → libveil VeilEventType so the JS layer
        // can drive the same switch statement it does for the
        // libveil event channel.
        let kind: Int
        let message: String
        switch conn.status {
        case .invalid:       kind = 3; message = "tunnel manager invalid"
        case .disconnected:  kind = 2; message = "disconnected"
        case .connecting:    kind = 0; message = "connecting"
        case .connected:     kind = 1; message = "connected"
        case .reasserting:   kind = 0; message = "reasserting"
        case .disconnecting: kind = 2; message = "disconnecting"
        @unknown default:    kind = 3; message = "unknown VPN status"
        }
        if kind == 0 { return } // intermediate; skip
        sendEvent(withName: "veil-event", body: [
            "type":      kind,
            "message":   message,
            "transport": "NetworkExtension",
            "remote":    "",
            "bytes_tx":  0,
            "bytes_rx":  0,
        ])
    }
}
