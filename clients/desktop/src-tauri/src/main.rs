// Veil desktop client — Tauri host entry point.

#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    veil_desktop_lib::run();
}
