// Veil mobile — Zustand store + TanStack QueryClient.
//
// One global store for UI state; tab/screen components subscribe to
// the slices they need so navigating between tabs doesn't trigger
// re-renders of unrelated state. Persistence is funneled through
// the AsyncStorage helper in ./store.js so its KV schema stays
// authoritative.

import { create } from "zustand";
import { QueryClient } from "@tanstack/react-query";

import * as nativeStore from "./store";

export const useStore = create((set, get) => ({
  // tunnel state (driven by native bridge events)
  status: "idle",
  transport: "",
  remote: "",
  bytesTx: 0,
  bytesRx: 0,
  message: "",
  startedAt: 0,

  // throughput rolling buffer (30 samples, ~1s each)
  thru: new Array(30).fill(0),
  prevTx: 0,
  prevRx: 0,
  uptime: "—",

  // navigation
  tab: "connect",
  setTab: (tab) => set({ tab }),

  // local profile state (mirrors AsyncStorage; loaded once on boot)
  profiles: [],
  activeId: null,
  editor: "",
  pasteLink: "",

  setEditor:    (v) => set({ editor: v }),
  setPasteLink: (v) => set({ pasteLink: v }),

  // settings (mirrors AsyncStorage)
  settings: { mimicry: "browse", decoy: false, notifications: true, autoConnect: false },

  // event handler — called by native bridge listener
  applyEvent(e) {
    const patch = {};
    if (e.transport) patch.transport = e.transport;
    if (e.remote) patch.remote = e.remote;
    if (typeof e.bytes_tx === "number") patch.bytesTx = e.bytes_tx;
    if (typeof e.bytes_rx === "number") patch.bytesRx = e.bytes_rx;
    if (e.message) patch.message = e.message;
    set(patch);
  },
  setStatus: (status) => set({ status }),
  setStartedAt: (startedAt) => set({ startedAt }),
  resetCounters: () =>
    set({ bytesTx: 0, bytesRx: 0, prevTx: 0, prevRx: 0, thru: new Array(30).fill(0) }),
  pushThruSample: (delta) =>
    set((s) => ({ thru: [...s.thru.slice(1), delta], prevRx: s.bytesRx, prevTx: s.bytesTx })),
  setUptime: (uptime) => set({ uptime }),
}));

// ── QueryClient ─────────────────────────────────────────────────
// Used wherever asynchronous reads benefit from caching across tab
// switches. Generous defaults: tab nav reads from cache instantly.
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 60_000,
      gcTime: 5 * 60_000,
      retry: 1,
      refetchOnWindowFocus: false,
      refetchOnReconnect: false,
    },
  },
});

// ── Persistence facade ──────────────────────────────────────────
export async function bootPersistedState() {
  const [profiles, activeId, settings] = await Promise.all([
    nativeStore.loadProfiles(),
    nativeStore.loadActiveId(),
    nativeStore.loadSettings(),
  ]);
  useStore.setState({
    profiles,
    activeId,
    settings: settings || useStore.getState().settings,
    editor: profiles.find((p) => p.id === activeId)?.config || "",
  });
}

export async function commitProfile(profile) {
  const next = [...useStore.getState().profiles, profile];
  useStore.setState({ profiles: next, activeId: profile.id, editor: profile.config });
  await nativeStore.saveProfiles(next);
  await nativeStore.saveActiveId(profile.id);
}

export async function dropProfile(id) {
  const cur = useStore.getState();
  const next = cur.profiles.filter((p) => p.id !== id);
  const newActive = cur.activeId === id ? (next[0]?.id ?? null) : cur.activeId;
  useStore.setState({
    profiles: next,
    activeId: newActive,
    editor: next.find((p) => p.id === newActive)?.config || "",
  });
  await nativeStore.saveProfiles(next);
  await nativeStore.saveActiveId(newActive);
}

export async function selectProfileId(id) {
  const cur = useStore.getState();
  useStore.setState({
    activeId: id,
    editor: cur.profiles.find((p) => p.id === id)?.config || "",
  });
  await nativeStore.saveActiveId(id);
}

export async function patchSettings(patch) {
  const next = { ...useStore.getState().settings, ...patch };
  useStore.setState({ settings: next });
  await nativeStore.saveSettings(next);
}
