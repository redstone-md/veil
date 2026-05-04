// TypeScript declarations for @veil/node.
//
// Mirrors the napi-derive surface in src/lib.rs. Hand-maintained
// rather than emitted by `napi build` so the published .d.ts stays
// readable and reviewable in PRs.

/**
 * One Veil VPN client session. Construct with a JSON or YAML
 * configuration string, call `start()` to bring the session up,
 * and `stop()` (or `destroy()`) to tear it down.
 */
export class Veil {
  /** Throws if the configuration text cannot be parsed. */
  constructor(configText: string);

  /**
   * Bring the session up. The optional callback fires once per
   * runtime event on the Node event loop. Do not block inside it.
   */
  start(callback?: (event: VeilEvent) => void): void;

  /** Request a graceful stop; returns immediately. */
  stop(): void;

  /** JSON-encoded `{ running, bytes_tx, bytes_rx }` snapshot. */
  metricsJson(): string;

  /** Tear the instance down. Subsequent calls throw. */
  destroy(): void;
}

/** Categories of runtime event delivered to the start() callback. */
export type VeilEventType =
  | 1 // Connected
  | 2 // Disconnected
  | 3 // Error
  | 4 // Traffic
  | 5; // TransportSwitch

export interface VeilEvent {
  type: VeilEventType;
  message: string;
  transport: string;
  remote: string;
  bytes_tx: number;
  bytes_rx: number;
}

/** Library version metadata as a JSON-encoded string. */
export function libraryVersion(): string;
