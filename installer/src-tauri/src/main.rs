// Veil Installer — Tauri host process.
//
// This file is a thin entry point. The interesting bits live in
// the lib crate so the Tauri host can be exercised from tests.

#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    veil_installer_lib::run();
}
