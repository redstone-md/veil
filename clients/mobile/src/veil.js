// Native bridge to the platform-specific Veil tunnel implementation.
//
// On iOS the tunnel runs in a NetworkExtension PacketTunnelProvider
// process; on Android in a long-lived VpnService. Both load libveil
// out-of-process. The JS side talks to them through a thin
// NativeModules bridge defined in:
//
//   ios/PacketTunnelProvider/VeilBridge.swift
//   android/app/src/main/java/org/veil/mobile/VeilBridge.kt
//
// The shape exposed here intentionally mirrors @veil/node so the
// React Native UI and any future Node-side tooling can share JS
// helpers without forking on platform.

import { NativeEventEmitter, NativeModules, Platform } from "react-native";

const { VeilBridge } = NativeModules;

if (!VeilBridge) {
  console.warn(
    "Veil: native bridge not linked. Mobile builds need the iOS NetworkExtension target or the Android VpnService module installed."
  );
}

const emitter = VeilBridge ? new NativeEventEmitter(VeilBridge) : null;

export const EVENT_TYPES = {
  CONNECTED: 1,
  DISCONNECTED: 2,
  ERROR: 3,
  TRAFFIC: 4,
  TRANSPORT_SWITCH: 5,
};

/**
 * Start a Veil session with the supplied JSON or YAML config text.
 * Returns a Promise that resolves once the native side has accepted
 * the request; runtime state surfaces through onEvent().
 */
export async function start(configText) {
  if (!VeilBridge) throw new Error("Veil native bridge not available");
  return VeilBridge.start(configText);
}

export async function stop() {
  if (!VeilBridge) throw new Error("Veil native bridge not available");
  return VeilBridge.stop();
}

export async function metricsJson() {
  if (!VeilBridge) return JSON.stringify({ running: false });
  return VeilBridge.metricsJson();
}

export async function libraryVersion() {
  if (!VeilBridge) return JSON.stringify({ version: "unavailable" });
  return VeilBridge.libraryVersion();
}

/**
 * Subscribe to runtime events. Returns an unsubscribe function.
 */
export function onEvent(handler) {
  if (!emitter) return () => {};
  const sub = emitter.addListener("veil-event", handler);
  return () => sub.remove();
}

export const PLATFORM_TUNNEL_KIND =
  Platform.OS === "ios" ? "NetworkExtension" : "VpnService";
