// Veil Installer — Zustand vanilla store + TanStack QueryClient.
//
// One central store for all UI state, accessed through a Proxy facade
// so existing `state.x = y` mutations route through Zustand setState
// (subscribers fire, the renderer can stay reactive). Caching wraps
// every Tauri admin_* invoke — sidebar nav between server detail
// tabs reads from cache instead of re-hitting the bridge each time.

import { createStore } from "zustand/vanilla";
import { QueryClient } from "@tanstack/query-core";
import { invoke } from "@tauri-apps/api/core";

function defaultComposeYaml() {
  return `version: "3.9"
services:
  veil:
    image: ghcr.io/redstone-md/veil:latest
    restart: unless-stopped
    network_mode: host
    volumes:
      - veil-state:/var/lib/veil
      - ./server.yaml:/etc/veil/server.yaml:ro
volumes:
  veil-state: {}
`;
}

const initial = {
  view: "welcome",
  servers: [],
  activeId: null,
  serverDetailTab: "overview",
  serverProbes: {},
  modal: null,
  toast: null,
  deploy: {
    composeYaml: defaultComposeYaml(),
    ssh: {
      host: "", port: "22", user: "root", password: "",
      probe: null, log: [], busy: false,
      domain: "", admin_email: "",
      transports: { reality: true, wss: true, quic: true, masque: false },
      decoy: "www.microsoft.com:443",
      progressSteps: [],
    },
    edge: {
      provider: "deno",
      origin_host: "", origin_port: "443",
      path: "/ws", app_name: "veil-edge",
      files: null,
    },
  },
};

export const store = createStore(() => ({ ...initial }));

export const state = new Proxy({}, {
  get(_, key)        { return store.getState()[key]; },
  set(_, key, value) { store.setState({ [key]: value }); return true; },
  has(_, key)        { return key in store.getState(); },
});

export function set(patch) {
  if (typeof patch === "function") store.setState((s) => ({ ...s, ...patch(s) }));
  else                              store.setState((s) => ({ ...s, ...patch }));
}

export function subscribeAll(listener) {
  return store.subscribe(listener);
}

// ── QueryClient ─────────────────────────────────────────────────
// 30s default stale time → tab switches in the server detail view
// (overview/users/transports) hit the cache, not the bridge.
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

export async function cachedInvoke(cmd, args, opts = {}) {
  if (opts.cache === false) return invoke(cmd, args);
  return queryClient.fetchQuery({
    queryKey: ["invoke", cmd, args],
    queryFn: () => invoke(cmd, args),
    staleTime: opts.staleTime,
  });
}

export function invalidateInvoke(cmd, args) {
  return queryClient.invalidateQueries({
    queryKey: args ? ["invoke", cmd, args] : ["invoke", cmd],
  });
}
