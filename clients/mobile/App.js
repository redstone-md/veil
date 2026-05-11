// Veil mobile — RN port of /design/sections/mobile.jsx.
//
// State is centralized in ./src/appStore.js (Zustand) and wrapped in
// a TanStack QueryClientProvider. Each tab subscribes only to the
// slices it actually reads, so navigation never re-renders unrelated
// state and never re-fetches data.

import { useEffect, useRef } from "react";
import {
  Animated,
  Easing,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  ScrollView,
  StatusBar,
  StyleSheet,
  Switch,
  Text,
  TextInput,
  View,
} from "react-native";
import { QueryClientProvider } from "@tanstack/react-query";

import * as veil from "./src/veil";
import {
  useStore, queryClient, bootPersistedState, commitProfile, dropProfile,
  selectProfileId, patchSettings,
} from "./src/appStore";

// ─── Design tokens ──────────────────────────────────────
const T = {
  bg:          "#0B0B0F",
  bg2:         "#101016",
  surface:     "#16161E",
  surface2:    "#1C1C26",
  border:      "rgba(255,255,255,0.07)",
  borderS:     "rgba(255,255,255,0.12)",
  text:        "#ECECF1",
  textDim:     "rgba(236,236,241,0.62)",
  textMute:    "rgba(236,236,241,0.40)",
  accent:      "#7C5CFF",
  accentDim:   "#5E45CC",
  accentSoft:  "rgba(124,92,255,0.12)",
  accentGlow:  "rgba(124,92,255,0.35)",
  ok:          "#3DD68C",
  okSoft:      "rgba(61,214,140,0.14)",
  warn:        "#F5C26B",
  err:         "#FF6B6B",
  errSoft:     "rgba(255,107,107,0.14)",
};
const FONT_MONO = Platform.OS === "ios" ? "Menlo" : "monospace";

// ─── Root ────────────────────────────────────────────────
export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <Root />
    </QueryClientProvider>
  );
}

function Root() {
  // Boot persisted state ONCE on app mount. Subsequent tab nav
  // doesn't trigger this — Zustand keeps the data alive.
  const tab = useStore((s) => s.tab);

  useEffect(() => {
    bootPersistedState();

    // Native event subscription — runs for the lifetime of the app.
    const off = veil.onEvent((e) => {
      const st = useStore.getState();
      st.applyEvent(e);
      switch (e.type) {
        case veil.EVENT_TYPES.CONNECTED:
          st.setStatus("connected");
          st.setStartedAt(Date.now());
          break;
        case veil.EVENT_TYPES.DISCONNECTED:
          st.setStatus(useStore.getState().status === "connecting" ? "error" : "stopped");
          st.setStartedAt(0);
          break;
        case veil.EVENT_TYPES.ERROR:
          st.setStatus("error");
          st.setStartedAt(0);
          break;
      }
    });
    return off;
  }, []);

  // 1Hz throughput sampler — lives at the root so it doesn't restart
  // when the user switches tabs.
  useEffect(() => {
    const t = setInterval(() => {
      const s = useStore.getState();
      if (s.status !== "connected") return;
      const dn = Math.max(0, s.bytesRx - s.prevRx);
      s.pushThruSample(Math.min(1, dn / (10 * 1024 * 1024)));
      if (s.startedAt) {
        const e = Math.floor((Date.now() - s.startedAt) / 1000);
        const h = Math.floor(e / 3600), m = Math.floor((e % 3600) / 60), sec = e % 60;
        s.setUptime(`${pad2(h)}:${pad2(m)}:${pad2(sec)}`);
      }
    }, 1000);
    return () => clearInterval(t);
  }, []);

  return (
    <View style={styles.root}>
      <StatusBar barStyle="light-content" backgroundColor={T.bg} />
      <KeyboardAvoidingView style={{ flex: 1 }} behavior={Platform.OS === "ios" ? "padding" : undefined}>
        {tab === "connect"  && <ConnectScreen  />}
        {tab === "profiles" && <ProfilesScreen />}
        {tab === "settings" && <SettingsScreen />}
      </KeyboardAvoidingView>
      <TabBar />
    </View>
  );
}

// ─── Connect screen ──────────────────────────────────────
function ConnectScreen() {
  // Each select is a separate hook — only the slice that changes
  // re-renders this component. No flash, no over-render.
  const status     = useStore((s) => s.status);
  const transport  = useStore((s) => s.transport);
  const editor     = useStore((s) => s.editor);
  const activeId   = useStore((s) => s.activeId);
  const profiles   = useStore((s) => s.profiles);
  const uptime     = useStore((s) => s.uptime);
  const bytesTx    = useStore((s) => s.bytesTx);
  const bytesRx    = useStore((s) => s.bytesRx);
  const thru       = useStore((s) => s.thru);
  const settings   = useStore((s) => s.settings);
  const setTab     = useStore((s) => s.setTab);
  const active     = profiles.find((p) => p.id === activeId);

  const labels = {
    idle:        ["Disconnected",     "Tap to connect"],
    stopped:     ["Disconnected",     "Tap to connect"],
    connecting:  ["Connecting…",      "Probing transports"],
    connected:   ["Protected",        active ? `${active.name} · ${uptime}` : "All traffic is tunneled"],
    error:       ["Connection failed","Server unreachable"],
  };
  const [title, sub] = labels[status] || labels.idle;
  const isUp = status === "connected";
  const tLabel = transport ? capitalize(transport) : "Auto";

  const onPress = async () => {
    if (status === "connecting") return;
    const st = useStore.getState();
    if (isUp) {
      try { await veil.stop(); } catch {}
      return;
    }
    const cfg = (editor || "").trim();
    if (!cfg) return;
    st.setStatus("connecting");
    st.resetCounters();
    try { await veil.start(cfg); }
    catch (e) { st.setStatus("error"); st.applyEvent({ message: String(e) }); }
  };

  return (
    <ScrollView contentContainerStyle={styles.screen}>
      <View style={styles.header}>
        <VeilMark size={20} />
        <Pressable style={styles.headBtn} onPress={() => setTab("settings")}>
          <Text style={styles.headBtnIcon}>⚙</Text>
        </Pressable>
      </View>

      <View style={{ marginTop: 18 }}>
        <Text style={styles.eyebrow}>STATUS</Text>
        <Text style={styles.bigTitle}>{title}</Text>
        <Text style={styles.sub}>{sub}</Text>
      </View>

      <Orb status={status} onPress={onPress} />

      <View style={styles.pillRow}>
        <TransportPill label={tLabel} active={isUp} />
        {isUp && <TransportPill label="multiplex×8" active compact />}
        {isUp && settings.mimicry ? <TransportPill label={`mimic: ${settings.mimicry}`} active compact /> : null}
      </View>

      <View style={[styles.card, { marginTop: 16 }]}>
        <View style={styles.thruRow}>
          <Text style={styles.thruDown}>
            {(thru[thru.length - 1] * 10).toFixed(1)}
            <Text style={styles.thruUnit}>  MB/s ↓</Text>
          </Text>
          <Text style={styles.thruMeta}>60s</Text>
        </View>
        <ThruChart data={thru} />
      </View>

      <View style={styles.statsGrid}>
        <Stat label="Down"    value={isUp ? fmtBytes(bytesRx) : "—"} />
        <Stat label="Up"      value={isUp ? fmtBytes(bytesTx) : "—"} />
        <Stat label="Streams" value={isUp ? "—" : "—"} />
        <Stat label="Latency" value={isUp ? "—" : "—"} />
      </View>
    </ScrollView>
  );
}

// ─── Profiles screen ─────────────────────────────────────
function ProfilesScreen() {
  const profiles  = useStore((s) => s.profiles);
  const activeId  = useStore((s) => s.activeId);
  const pasteLink = useStore((s) => s.pasteLink);
  const setPasteLink = useStore((s) => s.setPasteLink);

  const onImport = async () => {
    const cfg = pasteLink.trim();
    if (!cfg) return;
    await commitProfile({
      id: String(Date.now()),
      name: `Server ${profiles.length + 1}`,
      config: cfg,
    });
    setPasteLink("");
  };

  return (
    <ScrollView contentContainerStyle={styles.screen}>
      <View style={[styles.headerRow, { marginTop: 6 }]}>
        <View>
          <Text style={styles.bigTitle}>Profiles</Text>
          <Text style={styles.sub}>{profiles.length} configured</Text>
        </View>
        <Pressable style={styles.addBtn} onPress={onImport}>
          <Text style={styles.addBtnText}>+</Text>
        </Pressable>
      </View>

      <View style={{ marginTop: 14, gap: 10 }}>
        {profiles.length === 0 ? (
          <Text style={styles.empty}>No servers yet. Paste a veil:// link below to add one.</Text>
        ) : profiles.map((p) => {
          const isActive = p.id === activeId;
          return (
            <Pressable
              key={p.id}
              onPress={() => selectProfileId(p.id)}
              onLongPress={() => dropProfile(p.id)}
              style={[styles.profileRow, isActive && styles.profileRowActive]}
            >
              <View style={[styles.profileIcon, isActive && styles.profileIconActive]}>
                <Text style={[styles.profileIconText, isActive && { color: "#fff" }]}>▣</Text>
              </View>
              <View style={{ flex: 1, minWidth: 0 }}>
                <Text style={styles.profileName} numberOfLines={1}>{p.name}</Text>
                <Text style={styles.profileHost} numberOfLines={1}>{(p.config || "").slice(0, 36)}…</Text>
              </View>
              <TransportPill label={extractKind(p.config)} compact active={isActive} />
            </Pressable>
          );
        })}
      </View>

      <Text style={[styles.sectionH, { marginTop: 22 }]}>ADD</Text>
      <View style={[styles.card, { gap: 10 }]}>
        <Text style={styles.fieldLabel}>Paste a veil:// share link</Text>
        <TextInput
          value={pasteLink}
          onChangeText={setPasteLink}
          placeholder="veil://..."
          placeholderTextColor={T.textMute}
          style={styles.input}
          autoCorrect={false}
          autoCapitalize="none"
          multiline
        />
        <Pressable style={styles.primaryBtn} onPress={onImport}>
          <Text style={styles.primaryBtnText}>Import</Text>
        </Pressable>
      </View>
    </ScrollView>
  );
}

// ─── Settings screen ─────────────────────────────────────
function SettingsScreen() {
  const settings = useStore((s) => s.settings);

  return (
    <ScrollView contentContainerStyle={styles.screen}>
      <Text style={[styles.bigTitle, { marginTop: 6 }]}>Settings</Text>
      <Text style={styles.sub}>Preferences sync to local storage only — Veil never phones home.</Text>

      <View style={styles.settingsGroup}>
        <SettingRow
          label="Auto-connect" sub="On launch and untrusted Wi-Fi"
          right={<Switch value={!!settings.autoConnect} onValueChange={(v) => patchSettings({ autoConnect: v })}
                         trackColor={{ false: T.border, true: T.accent }} thumbColor="#fff" />}
        />
        <SettingRow
          label="Decoy traffic" sub="Inject cover requests when idle"
          right={<Switch value={!!settings.decoy} onValueChange={(v) => patchSettings({ decoy: v })}
                         trackColor={{ false: T.border, true: T.accent }} thumbColor="#fff" />}
        />
        <SettingRow
          label="Notifications" sub="Connect, error, transport switch"
          right={<Switch value={!!settings.notifications} onValueChange={(v) => patchSettings({ notifications: v })}
                         trackColor={{ false: T.border, true: T.accent }} thumbColor="#fff" />}
        />
      </View>

      <Text style={styles.fieldLabel}>Mimicry profile</Text>
      <View style={styles.segRow}>
        {["off", "browse", "video", "msg"].map((m) => {
          const cur = settings.mimicry || "off";
          const sel = cur === m;
          return (
            <Pressable key={m} onPress={() => patchSettings({ mimicry: m === "off" ? "" : m })}
              style={[styles.segItem, sel && styles.segItemActive]}>
              <Text style={[styles.segText, sel && styles.segTextActive]}>{m}</Text>
            </Pressable>
          );
        })}
      </View>

      <View style={[styles.settingsGroup, { marginTop: 18 }]}>
        <SettingRow label="Source on GitHub" right={<Text style={styles.chev}>›</Text>} />
        <SettingRow label="Privacy policy"   right={<Text style={styles.chev}>›</Text>} />
      </View>

      <Text style={styles.foot}>Veil v0.1.0-alpha.1 · Pre-alpha</Text>
    </ScrollView>
  );
}

function SettingRow({ label, sub, right }) {
  return (
    <View style={styles.settingRow}>
      <View style={{ flex: 1 }}>
        <Text style={styles.settingLabel}>{label}</Text>
        {sub ? <Text style={styles.settingSub}>{sub}</Text> : null}
      </View>
      {right}
    </View>
  );
}

// ─── Bottom tab bar ──────────────────────────────────────
function TabBar() {
  const tab = useStore((s) => s.tab);
  const setTab = useStore((s) => s.setTab);
  const items = [
    { id: "connect",  label: "Connect",  icon: "▣" },
    { id: "profiles", label: "Profiles", icon: "≡" },
    { id: "settings", label: "Settings", icon: "⚙" },
  ];
  return (
    <View style={styles.tabbar}>
      {items.map((it) => {
        const active = tab === it.id;
        return (
          <Pressable key={it.id} onPress={() => setTab(it.id)} style={styles.tabItem}>
            <Text style={[styles.tabIcon, active && { color: T.accent }]}>{it.icon}</Text>
            <Text style={[styles.tabLabel, active && { color: T.accent }]}>{it.label}</Text>
          </Pressable>
        );
      })}
    </View>
  );
}

// ─── Status orb (pure RN, no SVG) ────────────────────────
function Orb({ status, onPress }) {
  const ring = useRef(new Animated.Value(0)).current;
  const isLive = status === "connecting" || status === "connected";
  useEffect(() => {
    if (status !== "connecting") { ring.setValue(0); return; }
    const loop = Animated.loop(Animated.timing(ring, {
      toValue: 1, duration: 1600, easing: Easing.inOut(Easing.ease), useNativeDriver: true,
    }));
    loop.start();
    return () => loop.stop();
  }, [status]);

  const ringScale = ring.interpolate({ inputRange: [0, 0.5, 1], outputRange: [1, 1.18, 1] });
  const ringOpacity = ring.interpolate({ inputRange: [0, 0.5, 1], outputRange: [0, 0.35, 0] });

  const accent = status === "error" ? T.err : T.accent;

  return (
    <View style={styles.orbWrap}>
      {isLive && (
        <Animated.View style={[styles.orbRing, {
          borderColor: accent,
          opacity: status === "connecting" ? ringOpacity : 0.18,
          transform: [{ scale: ringScale }],
        }]} />
      )}
      {isLive && <View style={[styles.orbGlow, { shadowColor: accent }]} />}
      <Pressable
        onPress={onPress}
        style={[styles.orb, {
          borderColor: status === "idle" || status === "stopped" ? T.borderS : accent,
          backgroundColor: T.surface,
          shadowColor: isLive ? accent : "transparent",
          shadowOpacity: isLive ? 0.6 : 0,
          shadowRadius: isLive ? 24 : 0,
        }]}
      >
        <Text style={[styles.orbIcon, { color: status === "idle" || status === "stopped" ? T.textDim : accent }]}>⏻</Text>
      </Pressable>
    </View>
  );
}

// ─── Throughput chart (bar histogram) ────────────────────
function ThruChart({ data }) {
  const max = Math.max(0.05, ...data);
  return (
    <View style={styles.chart}>
      {data.map((v, i) => (
        <View key={i} style={[styles.chartBar, {
          height: `${Math.max(2, (v / max) * 100)}%`,
          opacity: 0.4 + (i / data.length) * 0.6,
        }]} />
      ))}
    </View>
  );
}

// ─── Transport pill ──────────────────────────────────────
function TransportPill({ label, active = true, compact = false }) {
  return (
    <View style={[
      styles.pill,
      compact && { height: 22, paddingHorizontal: 9 },
      { backgroundColor: active ? T.accentSoft : "rgba(255,255,255,0.04)",
        borderColor: active ? "rgba(124,92,255,0.4)" : T.border },
    ]}>
      <View style={[styles.pillDot, { backgroundColor: active ? T.accent : T.textMute }]} />
      <Text style={[styles.pillText, compact && { fontSize: 10 }, !active && { color: T.textDim }]}>{label}</Text>
    </View>
  );
}

// ─── Veil mark ───────────────────────────────────────────
function VeilMark({ size = 22 }) {
  return (
    <View style={{ width: size, height: size, alignItems: "center", justifyContent: "center" }}>
      <Text style={{ fontSize: size, fontWeight: "700", color: T.accent, lineHeight: size }}>V</Text>
    </View>
  );
}

function Stat({ label, value }) {
  return (
    <View style={styles.statTile}>
      <Text style={styles.statLabel}>{label}</Text>
      <Text style={styles.statValue}>{value}</Text>
    </View>
  );
}

// ─── Helpers ─────────────────────────────────────────────
function pad2(n) { return n < 10 ? "0" + n : "" + n; }
function capitalize(s) { return s ? s.charAt(0).toUpperCase() + s.slice(1) : s; }
function fmtBytes(n) {
  if (!n) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0, v = n;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(i ? 1 : 0)} ${u[i]}`;
}
function extractKind(cfg) {
  if (!cfg) return "?";
  if (cfg.startsWith("veil://")) return "Reality";
  const m = cfg.match(/Type:\s*"?(\w+)"?/);
  return m ? m[1].toUpperCase() : "Reality";
}

// ─── Styles ──────────────────────────────────────────────
const styles = StyleSheet.create({
  root: { flex: 1, backgroundColor: T.bg },
  screen: { padding: 20, paddingTop: 16, paddingBottom: 100 },

  header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between" },
  headerRow: { flexDirection: "row", alignItems: "center", justifyContent: "space-between" },
  headBtn: {
    width: 36, height: 36, borderRadius: 18,
    backgroundColor: T.surface, borderWidth: 1, borderColor: T.border,
    alignItems: "center", justifyContent: "center",
  },
  headBtnIcon: { color: T.textDim, fontSize: 16 },

  eyebrow: { color: T.textMute, fontSize: 11, fontFamily: FONT_MONO, letterSpacing: 1.4 },
  bigTitle: { color: T.text, fontSize: 28, fontWeight: "700", letterSpacing: -0.4, marginTop: 2 },
  sub: { color: T.textDim, fontSize: 13, marginTop: 2 },
  fieldLabel: { color: T.textMute, fontSize: 11, fontFamily: FONT_MONO, letterSpacing: 1.2, textTransform: "uppercase", marginTop: 14, marginBottom: 6 },
  sectionH: { color: T.textMute, fontSize: 11, fontFamily: FONT_MONO, letterSpacing: 1.4, textTransform: "uppercase", marginBottom: 8 },

  orbWrap: {
    width: 200, height: 200, alignSelf: "center", marginTop: 20, marginBottom: 18,
    alignItems: "center", justifyContent: "center", position: "relative",
  },
  orbRing: { position: "absolute", width: 200, height: 200, borderRadius: 100, borderWidth: 1 },
  orbGlow: {
    position: "absolute", width: 184, height: 184, borderRadius: 92,
    shadowOffset: { width: 0, height: 0 }, shadowOpacity: 0.6, shadowRadius: 30,
  },
  orb: {
    width: 168, height: 168, borderRadius: 84,
    borderWidth: 1, alignItems: "center", justifyContent: "center", elevation: 6,
  },
  orbIcon: { fontSize: 44, fontWeight: "200" },

  pillRow: { flexDirection: "row", justifyContent: "center", gap: 6, flexWrap: "wrap" },
  pill: {
    flexDirection: "row", alignItems: "center", gap: 6,
    height: 26, paddingHorizontal: 11, borderRadius: 999, borderWidth: 1,
  },
  pillDot: { width: 6, height: 6, borderRadius: 3 },
  pillText: { color: T.text, fontSize: 11, fontFamily: FONT_MONO, fontWeight: "500" },

  card: {
    backgroundColor: T.surface, borderColor: T.border, borderWidth: 1,
    borderRadius: 14, padding: 14,
  },

  thruRow: { flexDirection: "row", justifyContent: "space-between", alignItems: "baseline", marginBottom: 6 },
  thruDown: { color: T.text, fontSize: 18, fontWeight: "600" },
  thruUnit: { color: T.textDim, fontSize: 11, fontWeight: "400" },
  thruMeta: { color: T.textMute, fontSize: 10, fontFamily: FONT_MONO },
  chart: {
    flexDirection: "row", alignItems: "flex-end",
    height: 56, gap: 2, marginTop: 4,
  },
  chartBar: { flex: 1, backgroundColor: T.accent, borderRadius: 1 },

  statsGrid: { flexDirection: "row", flexWrap: "wrap", gap: 8, marginTop: 12 },
  statTile: {
    width: "47%", flexGrow: 1,
    backgroundColor: T.surface, borderColor: T.border, borderWidth: 1,
    borderRadius: 10, padding: 10,
  },
  statLabel: { color: T.textMute, fontSize: 10, fontFamily: FONT_MONO, letterSpacing: 1.2, textTransform: "uppercase" },
  statValue: { color: T.text, fontSize: 15, fontWeight: "600", marginTop: 2 },

  empty: { color: T.textDim, fontSize: 13, padding: 16, textAlign: "center" },
  profileRow: {
    flexDirection: "row", alignItems: "center", gap: 12,
    padding: 14, borderRadius: 14,
    backgroundColor: T.surface, borderColor: T.border, borderWidth: 1,
  },
  profileRowActive: { borderColor: "rgba(124,92,255,0.6)" },
  profileIcon: {
    width: 36, height: 36, borderRadius: 10,
    backgroundColor: "rgba(255,255,255,0.06)",
    alignItems: "center", justifyContent: "center",
  },
  profileIconActive: { backgroundColor: T.accent },
  profileIconText: { color: T.textDim, fontSize: 16 },
  profileName: { color: T.text, fontSize: 14, fontWeight: "600" },
  profileHost: { color: T.textDim, fontSize: 11, fontFamily: FONT_MONO, marginTop: 1 },

  addBtn: {
    width: 36, height: 36, borderRadius: 18,
    backgroundColor: T.accent, alignItems: "center", justifyContent: "center",
  },
  addBtnText: { color: "#fff", fontSize: 18, fontWeight: "600" },

  primaryBtn: {
    backgroundColor: T.accent, borderRadius: 9,
    paddingVertical: 11, alignItems: "center",
  },
  primaryBtnText: { color: "#fff", fontSize: 13, fontWeight: "600" },

  input: {
    color: T.text, backgroundColor: T.bg2,
    borderColor: T.border, borderWidth: 1,
    borderRadius: 9, padding: 11, minHeight: 70,
    fontFamily: FONT_MONO, fontSize: 12,
    textAlignVertical: "top",
  },

  settingsGroup: {
    marginTop: 16, borderRadius: 14,
    backgroundColor: T.surface, borderColor: T.border, borderWidth: 1,
    overflow: "hidden",
  },
  settingRow: {
    flexDirection: "row", alignItems: "center", gap: 12,
    paddingVertical: 12, paddingHorizontal: 14,
    borderTopColor: T.border, borderTopWidth: 1,
  },
  settingLabel: { color: T.text, fontSize: 13, fontWeight: "500" },
  settingSub: { color: T.textDim, fontSize: 11, marginTop: 1 },
  chev: { color: T.textDim, fontSize: 18 },
  segRow: {
    flexDirection: "row",
    backgroundColor: T.bg2, borderColor: T.border, borderWidth: 1,
    padding: 2, borderRadius: 9,
  },
  segItem: { flex: 1, paddingVertical: 6, alignItems: "center", borderRadius: 7 },
  segItemActive: { backgroundColor: T.surface },
  segText: { color: T.textDim, fontSize: 12, fontFamily: FONT_MONO },
  segTextActive: { color: T.text, fontWeight: "600" },

  foot: {
    color: T.textMute, fontSize: 11, fontFamily: FONT_MONO,
    textAlign: "center", marginTop: 22,
  },

  tabbar: {
    flexDirection: "row",
    borderTopColor: T.border, borderTopWidth: 1,
    backgroundColor: T.bg2,
    paddingBottom: Platform.OS === "ios" ? 24 : 8,
    paddingTop: 8,
  },
  tabItem: { flex: 1, alignItems: "center", gap: 3 },
  tabIcon: { color: T.textMute, fontSize: 20 },
  tabLabel: { color: T.textMute, fontSize: 10, fontWeight: "500" },
});
