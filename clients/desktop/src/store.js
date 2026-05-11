// Veil desktop — Zustand vanilla store + TanStack QueryClient.
//
// All UI state lives in a single Zustand store. A Proxy facade lets
// existing code use `state.x = y` syntax while routing through
// zustand's setState (so subscribers fire and the renderer can stay
// reactive). The `set()` export is the canonical mutation API for
// nested updates.
//
// QueryClient wraps Tauri `invoke()` calls that are safe to cache —
// repeated nav between tabs reads from the cache instead of hitting
// the bridge each time, killing the navigation flicker.

import { createStore } from "zustand/vanilla";
import { QueryClient } from "@tanstack/query-core";

// ── Initial state (extracted verbatim from main.js) ─────────────
//
// IMPORTANT: only put fields here that should trigger a UI re-render
// when they change. High-frequency / volatile fields (byte counters,
// transport name, remote, message) live in `liveStats` below — they
// update on every traffic event and would otherwise flood the
// renderer.
const initial = {
  status: "idle",
  startedAt: 0,

  profiles: [],
  activeId: null,
  configEditor: "",

  view: "home",

  settings: {
    autostart: false,
    mimicry: "browse",
    decoy: false,
    notifications: true,
    mode: "socks5",
    transport: "auto",
    bypassCidrs: "",
  },

  log: [],
  update: null,
  toast: null,
  modal: null,
};

// Throughput buffers live outside the reactive store — they update
// every second while connected and would otherwise trigger a render
// each tick. The home view reads these directly and patches DOM
// in place, so no subscriber needs to know.
//
// Sampling is event-driven (one sample per Traffic event from the
// 250 ms Rust poller). `lastTs` / `lastRx` / `lastTx` track the prior
// snapshot so each event computes a true bytes-per-second rate.
export const throughput = {
  down: new Array(60).fill(0),
  up:   new Array(60).fill(0),
  prevTx: 0,
  prevRx: 0,
  lastTs: 0,
  lastRx: 0,
  lastTx: 0,
  lastRateRx: 0,
  lastRateTx: 0,
};

// Volatile per-event stats from the native bridge. The Tauri event
// listener fires many times per second when traffic is flowing —
// putting these in the Zustand store would schedule a render each
// tick. Instead the home view reads them on its first paint and the
// 1Hz sampler updates the relevant DOM nodes in place.
export const liveStats = {
  remote: "",
  transport: "",
  bytesTx: 0,
  bytesRx: 0,
  message: "",
  // Latest TUN bring-up progress event ({ step, total, label }) — the
  // home view's status sub-line reads it while status === "connecting".
  tunProgress: null,
};

export const store = createStore(() => ({ ...initial }));

// Proxy facade: `state.x = y` routes through zustand setState so
// subscribers fire. Reads are always live snapshots — no stale refs.
// Equal-value writes are short-circuited so e.g. setting status to
// the value it already has doesn't schedule a useless re-render.
export const state = new Proxy({}, {
  get(_, key) {
    return store.getState()[key];
  },
  set(_, key, value) {
    if (Object.is(store.getState()[key], value)) return true;
    store.setState({ [key]: value });
    return true;
  },
  has(_, key) {
    return key in store.getState();
  },
  ownKeys() {
    return Reflect.ownKeys(store.getState());
  },
  getOwnPropertyDescriptor(_, key) {
    return { enumerable: true, configurable: true, value: store.getState()[key] };
  },
});

// `set(patch)` — explicit functional/object setter for nested updates
// (e.g. `set(s => ({ settings: { ...s.settings, mimicry: "off" } }))`).
// Bails early when the patch wouldn't change anything, so callers
// can write idempotently without scheduling a useless render.
export function set(patch) {
  const cur = store.getState();
  const p = typeof patch === "function" ? patch(cur) : patch;
  let changed = false;
  for (const k in p) {
    if (!Object.is(cur[k], p[k])) { changed = true; break; }
  }
  if (!changed) return; // no-op: don't notify subscribers
  store.setState({ ...cur, ...p });
}

// Subscribe to slice changes. Returns unsubscribe.
export function subscribe(selector, listener) {
  let prev = selector(store.getState());
  return store.subscribe((s) => {
    const next = selector(s);
    if (!Object.is(next, prev)) {
      const old = prev;
      prev = next;
      listener(next, old);
    }
  });
}

// Top-level subscribe (any change). Useful for the renderer.
export function subscribeAll(listener) {
  return store.subscribe(listener);
}

// ── QueryClient ─────────────────────────────────────────────────
// Cached calls live for 30s; navigating between tabs within that
// window is instant (no spinner, no flicker).
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      gcTime: 5 * 60_000,
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
});

// Helper: cache-aware Tauri invoke. Skip caching by passing
// `{ cache: false }`.
import { invoke } from "@tauri-apps/api/core";
export async function cachedInvoke(cmd, args, opts = {}) {
  if (opts.cache === false) return invoke(cmd, args);
  const key = ["invoke", cmd, args];
  return queryClient.fetchQuery({
    queryKey: key,
    queryFn: () => invoke(cmd, args),
    staleTime: opts.staleTime,
  });
}

// Invalidate one or many cached invoke results.
export function invalidate(cmd, args) {
  return queryClient.invalidateQueries({ queryKey: args ? ["invoke", cmd, args] : ["invoke", cmd] });
}
