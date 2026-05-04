// Veil mobile — root component.
//
// Single-screen Connect / Disconnect / Status / Profile / Settings
// layout, matching the desktop client's information density. Every
// state transition is driven by the native bridge in src/veil.js;
// the UI just reflects what the tunnel reports.

import { useEffect, useRef, useState } from "react";
import {
  Alert,
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

import * as veil from "./src/veil";
import * as store from "./src/store";

const STATUS_LABELS = {
  idle: "Disconnected",
  connecting: "Connecting…",
  connected: "Connected",
  error: "Error",
  stopped: "Stopped",
};

export default function App() {
  const [status, setStatus] = useState("idle");
  const [transport, setTransport] = useState("");
  const [remote, setRemote] = useState("");
  const [bytesTx, setBytesTx] = useState(0);
  const [bytesRx, setBytesRx] = useState(0);
  const [message, setMessage] = useState("");
  const [profiles, setProfiles] = useState([]);
  const [activeId, setActiveId] = useState(null);
  const [editor, setEditor] = useState("");
  const [settings, setSettings] = useState({
    mimicry: "",
    decoy: false,
    notifications: true,
  });
  const [showSettings, setShowSettings] = useState(false);
  const [log, setLog] = useState([]);
  const logRef = useRef(log);
  logRef.current = log;

  function pushLog(line) {
    const ts = new Date().toLocaleTimeString();
    const next = [...logRef.current, `${ts} ${line}`].slice(-200);
    setLog(next);
  }

  // Boot: load persisted state and subscribe to native events.
  useEffect(() => {
    (async () => {
      const profs = await store.loadProfiles();
      setProfiles(profs);
      const id = await store.loadActiveId();
      setActiveId(id);
      const active = profs.find((p) => p.id === id);
      if (active) setEditor(active.config);
      setSettings(await store.loadSettings());
    })();

    const off = veil.onEvent((e) => {
      if (e.transport) setTransport(e.transport);
      if (e.remote) setRemote(e.remote);
      if (typeof e.bytes_tx === "number") setBytesTx(e.bytes_tx);
      if (typeof e.bytes_rx === "number") setBytesRx(e.bytes_rx);
      if (e.message) setMessage(e.message);
      switch (e.type) {
        case veil.EVENT_TYPES.CONNECTED:
          setStatus("connected");
          pushLog(`connected via ${e.transport} → ${e.remote}`);
          break;
        case veil.EVENT_TYPES.DISCONNECTED:
          setStatus((s) => (s === "connecting" ? "error" : "stopped"));
          pushLog("disconnected");
          break;
        case veil.EVENT_TYPES.ERROR:
          setStatus("error");
          pushLog("error: " + (e.message || "?"));
          break;
        case veil.EVENT_TYPES.TRAFFIC:
          pushLog(`traffic tx=${fmtBytes(e.bytes_tx)} rx=${fmtBytes(e.bytes_rx)}`);
          break;
        case veil.EVENT_TYPES.TRANSPORT_SWITCH:
          pushLog(`transport switch: ${e.transport}`);
          break;
      }
    });
    return off;
  }, []);

  async function connect() {
    const cfg = (editor || "").trim();
    if (!cfg) {
      setMessage("config is empty");
      return;
    }
    setStatus("connecting");
    setBytesTx(0);
    setBytesRx(0);
    pushLog("connect requested");
    try {
      await veil.start(cfg);
      pushLog("started");
    } catch (e) {
      setStatus("error");
      setMessage(String(e));
      pushLog("connect failed: " + e);
    }
  }

  async function disconnect() {
    pushLog("disconnect requested");
    try {
      await veil.stop();
      pushLog("stopped");
    } catch (e) {
      pushLog("disconnect failed: " + e);
    }
  }

  async function addProfile() {
    Alert.prompt?.("New profile", "Profile name", async (name) => {
      if (!name) return;
      const id = String(Date.now());
      const next = [...profiles, { id, name, config: editor }];
      setProfiles(next);
      setActiveId(id);
      await store.saveProfiles(next);
      await store.saveActiveId(id);
    });
  }

  async function deleteProfile() {
    if (!activeId) return;
    const next = profiles.filter((p) => p.id !== activeId);
    const newActive = next[0]?.id ?? null;
    setProfiles(next);
    setActiveId(newActive);
    setEditor(next[0]?.config ?? "");
    await store.saveProfiles(next);
    await store.saveActiveId(newActive);
  }

  async function selectProfile(id) {
    setActiveId(id);
    const p = profiles.find((p) => p.id === id);
    setEditor(p?.config ?? "");
    await store.saveActiveId(id);
  }

  async function saveProfileConfig() {
    const idx = profiles.findIndex((p) => p.id === activeId);
    if (idx === -1) return;
    const next = [...profiles];
    next[idx] = { ...next[idx], config: editor };
    setProfiles(next);
    await store.saveProfiles(next);
    pushLog(`saved profile "${next[idx].name}"`);
  }

  async function updateSettings(patch) {
    const next = { ...settings, ...patch };
    setSettings(next);
    await store.saveSettings(next);
  }

  return (
    <View style={styles.root}>
      <StatusBar barStyle="light-content" />
      <KeyboardAvoidingView
        style={{ flex: 1 }}
        behavior={Platform.OS === "ios" ? "padding" : undefined}
      >
        <ScrollView contentContainerStyle={styles.container}>
          <Text style={styles.h1}>Veil</Text>
          <Text style={styles.subtitle}>
            Self-hosted, censorship-resistant VPN ({veil.PLATFORM_TUNNEL_KIND}).
          </Text>

          {!showSettings ? (
            <>
              <View style={styles.card}>
                <View style={styles.statusRow}>
                  <View style={[styles.dot, dotStyle(status)]} />
                  <Text style={styles.statusText}>{STATUS_LABELS[status] || status}</Text>
                  <Text style={styles.statusHint}>
                    {transport ? `via ${transport}` : ""}
                  </Text>
                </View>
                <KV k="Remote" v={remote || "—"} />
                <KV k="Bytes ↑" v={fmtBytes(bytesTx)} />
                <KV k="Bytes ↓" v={fmtBytes(bytesRx)} />
                <KV k="Last event" v={message || "—"} />
              </View>

              <View style={styles.card}>
                <Text style={styles.label}>Profile</Text>
                <ScrollView horizontal showsHorizontalScrollIndicator={false}>
                  {profiles.length === 0 ? (
                    <Text style={styles.muted}>(no profiles)</Text>
                  ) : (
                    profiles.map((p) => (
                      <Pressable
                        key={p.id}
                        style={[
                          styles.chip,
                          p.id === activeId && styles.chipActive,
                        ]}
                        onPress={() => selectProfile(p.id)}
                      >
                        <Text style={styles.chipText}>{p.name}</Text>
                      </Pressable>
                    ))
                  )}
                </ScrollView>
                <View style={styles.row}>
                  <Btn label="+ Add" onPress={addProfile} />
                  <Btn label="🗑 Delete" onPress={deleteProfile} disabled={!activeId} />
                </View>
              </View>

              <View style={styles.card}>
                <Text style={styles.label}>
                  Configuration (paste a veil:// link or YAML)
                </Text>
                <TextInput
                  multiline
                  value={editor}
                  onChangeText={setEditor}
                  placeholder="veil://eyJTZXJ2ZXJzIjpbey4uLn1dfQ"
                  placeholderTextColor="#586069"
                  style={styles.editor}
                  autoCorrect={false}
                  autoCapitalize="none"
                />
                <Btn label="Save to profile" onPress={saveProfileConfig} disabled={!activeId} />
              </View>

              <View style={styles.row}>
                <BigBtn
                  label={status === "connecting" ? "Connecting…" : "Connect"}
                  primary
                  onPress={connect}
                  disabled={status === "connecting" || status === "connected"}
                />
                <BigBtn
                  label="Disconnect"
                  danger
                  onPress={disconnect}
                  disabled={status !== "connected" && status !== "connecting"}
                />
              </View>

              <Btn label="⚙ Settings" onPress={() => setShowSettings(true)} />

              <View style={styles.log}>
                {log.map((line, i) => (
                  <Text key={i} style={styles.logLine}>
                    {line}
                  </Text>
                ))}
              </View>
            </>
          ) : (
            <SettingsPanel
              settings={settings}
              onChange={updateSettings}
              onBack={() => setShowSettings(false)}
            />
          )}
        </ScrollView>
      </KeyboardAvoidingView>
    </View>
  );
}

function SettingsPanel({ settings, onChange, onBack }) {
  return (
    <>
      <Btn label="← Back" onPress={onBack} />
      <View style={styles.card}>
        <SettingRow
          label="OS notifications"
          value={settings.notifications}
          onChange={(v) => onChange({ notifications: v })}
        />
        <SettingRow
          label="Decoy cover traffic"
          value={settings.decoy}
          onChange={(v) => onChange({ decoy: v })}
        />
        <Text style={[styles.label, { marginTop: 16 }]}>Mimicry profile</Text>
        <View style={styles.row}>
          {["", "browse", "video", "messaging", "search"].map((m) => (
            <Pressable
              key={m || "off"}
              onPress={() => onChange({ mimicry: m })}
              style={[styles.chip, settings.mimicry === m && styles.chipActive]}
            >
              <Text style={styles.chipText}>{m || "off"}</Text>
            </Pressable>
          ))}
        </View>
      </View>
      <Text style={styles.subtitle}>Settings persist via AsyncStorage.</Text>
    </>
  );
}

function SettingRow({ label, value, onChange }) {
  return (
    <View style={[styles.row, { justifyContent: "space-between" }]}>
      <Text style={styles.text}>{label}</Text>
      <Switch value={value} onValueChange={onChange} />
    </View>
  );
}

function KV({ k, v }) {
  return (
    <View style={styles.kvRow}>
      <Text style={styles.kvK}>{k}</Text>
      <Text style={styles.kvV}>{v}</Text>
    </View>
  );
}

function Btn({ label, onPress, disabled }) {
  return (
    <Pressable
      onPress={onPress}
      disabled={disabled}
      style={({ pressed }) => [
        styles.btn,
        pressed && !disabled && styles.btnPressed,
        disabled && styles.btnDisabled,
      ]}
    >
      <Text style={styles.btnText}>{label}</Text>
    </Pressable>
  );
}

function BigBtn({ label, onPress, disabled, primary, danger }) {
  return (
    <Pressable
      onPress={onPress}
      disabled={disabled}
      style={({ pressed }) => [
        styles.bigBtn,
        primary && styles.bigBtnPrimary,
        danger && styles.bigBtnDanger,
        pressed && !disabled && { opacity: 0.85 },
        disabled && styles.btnDisabled,
      ]}
    >
      <Text style={styles.bigBtnText}>{label}</Text>
    </Pressable>
  );
}

function dotStyle(s) {
  switch (s) {
    case "connected": return { backgroundColor: "#3fb950" };
    case "connecting": return { backgroundColor: "#d29922" };
    case "error": return { backgroundColor: "#f85149" };
    default: return { backgroundColor: "#8b949e" };
  }
}

function fmtBytes(n) {
  if (!n) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0, v = n;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(i ? 1 : 0)} ${u[i]}`;
}

const styles = StyleSheet.create({
  root: { flex: 1, backgroundColor: "#0d1117" },
  container: { padding: 16, gap: 12 },
  h1: { color: "#c9d1d9", fontSize: 24, fontWeight: "600" },
  subtitle: { color: "#8b949e", fontSize: 13 },
  text: { color: "#c9d1d9", fontSize: 14 },
  muted: { color: "#8b949e", fontSize: 13, padding: 8 },
  label: { color: "#8b949e", fontSize: 12, marginBottom: 6 },
  card: {
    backgroundColor: "#161b22",
    borderColor: "#30363d",
    borderWidth: 1,
    borderRadius: 8,
    padding: 12,
    gap: 6,
  },
  statusRow: { flexDirection: "row", alignItems: "center", gap: 8, marginBottom: 8 },
  dot: { width: 12, height: 12, borderRadius: 6 },
  statusText: { color: "#c9d1d9", fontSize: 14 },
  statusHint: { color: "#8b949e", fontSize: 12, marginLeft: "auto" },
  kvRow: { flexDirection: "row", justifyContent: "space-between", paddingVertical: 2 },
  kvK: { color: "#8b949e", fontSize: 12 },
  kvV: { color: "#c9d1d9", fontSize: 12, fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace" },
  row: { flexDirection: "row", flexWrap: "wrap", alignItems: "center", gap: 8 },
  editor: {
    color: "#c9d1d9",
    backgroundColor: "#0d1117",
    borderColor: "#30363d",
    borderWidth: 1,
    borderRadius: 6,
    padding: 8,
    fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace",
    fontSize: 12,
    minHeight: 110,
    textAlignVertical: "top",
  },
  chip: {
    backgroundColor: "#21262d",
    borderColor: "#30363d",
    borderWidth: 1,
    borderRadius: 16,
    paddingHorizontal: 12,
    paddingVertical: 6,
    marginRight: 6,
  },
  chipActive: { backgroundColor: "#2f81f7", borderColor: "#2f81f7" },
  chipText: { color: "#c9d1d9", fontSize: 13 },
  btn: {
    backgroundColor: "#21262d",
    borderColor: "#30363d",
    borderWidth: 1,
    borderRadius: 6,
    paddingHorizontal: 12,
    paddingVertical: 8,
  },
  btnPressed: { backgroundColor: "#30363d" },
  btnDisabled: { opacity: 0.4 },
  btnText: { color: "#c9d1d9", fontSize: 13 },
  bigBtn: {
    flex: 1,
    backgroundColor: "#21262d",
    borderRadius: 6,
    paddingVertical: 12,
    alignItems: "center",
  },
  bigBtnPrimary: { backgroundColor: "#2f81f7" },
  bigBtnDanger: { backgroundColor: "#5a1d1d", borderColor: "#f85149" },
  bigBtnText: { color: "#fff", fontSize: 15, fontWeight: "600" },
  log: {
    backgroundColor: "#0d1117",
    borderColor: "#30363d",
    borderWidth: 1,
    borderRadius: 6,
    padding: 8,
    minHeight: 120,
  },
  logLine: { color: "#8b949e", fontSize: 11, fontFamily: Platform.OS === "ios" ? "Menlo" : "monospace" },
});
