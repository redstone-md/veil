// Profile + settings persistence backed by AsyncStorage.
//
// Mirrors the schema the desktop client persists through
// tauri-plugin-store so a future profile-export feature can move
// blobs between desktop and mobile without reshape.

import AsyncStorage from "@react-native-async-storage/async-storage";

const KEY_PROFILES = "veil:profiles";
const KEY_ACTIVE = "veil:active_profile";
const KEY_SETTINGS = "veil:settings";

export async function loadProfiles() {
  const raw = await AsyncStorage.getItem(KEY_PROFILES);
  return raw ? JSON.parse(raw) : [];
}

export async function saveProfiles(profiles) {
  await AsyncStorage.setItem(KEY_PROFILES, JSON.stringify(profiles));
}

export async function loadActiveId() {
  return AsyncStorage.getItem(KEY_ACTIVE);
}

export async function saveActiveId(id) {
  if (id == null) await AsyncStorage.removeItem(KEY_ACTIVE);
  else await AsyncStorage.setItem(KEY_ACTIVE, id);
}

export async function loadSettings() {
  const raw = await AsyncStorage.getItem(KEY_SETTINGS);
  return raw
    ? JSON.parse(raw)
    : { mimicry: "", decoy: false, notifications: true };
}

export async function saveSettings(settings) {
  await AsyncStorage.setItem(KEY_SETTINGS, JSON.stringify(settings));
}
