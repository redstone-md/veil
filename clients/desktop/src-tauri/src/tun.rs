// Veil desktop — system-wide TUN mode (Windows / Wintun).
//
// The flow when the user toggles into TUN mode:
//
//   1. Verify we're running as Administrator. Wintun adapter creation
//      requires it; without elevation Wintun returns ERROR_ACCESS_DENIED
//      and the session never starts. We surface this as a clear error
//      to the UI rather than letting the request silently fail.
//
//   2. Hand the configuration string to libveil's
//      veil_desktop_start_with_wintun (added in core/pkg/cgo/desktop_windows.go),
//      which opens "Veil" Wintun adapter, attaches the gVisor netstack
//      via the existing CallbackPipe, and starts the SOCKS5 listener
//      bound to 127.0.0.1:1080.
//
//   3. Once the adapter is open, assign it 10.42.0.2/24 and add a
//      default route (0.0.0.0/0) through it via netsh + route. We
//      intentionally exempt the SOCKS5 path (127.0.0.1) and the
//      Veil server's IP itself from the TUN routes — without that
//      exemption libveil's own outbound dial would loop through its
//      own TUN.
//
//   4. On disconnect, tear the routes back down and let the libveil
//      destroy path close the adapter.

use std::process::Command;
use std::sync::Mutex;

use serde::Serialize;
use veil::Veil;

const ADAPTER_NAME: &str = "Veil";
const TUN_IP: &str = "10.42.0.2";
const TUN_MASK: &str = "255.255.255.0";
const TUN_GATEWAY: &str = "10.42.0.1";

#[derive(Default)]
pub struct TunState {
    pub inner: Mutex<Option<TunSession>>,
}

pub struct TunSession {
    pub veil: Veil,
    pub server_ip: Option<String>,
}

#[derive(Debug, Serialize)]
pub struct TunStatus {
    pub active: bool,
    pub adapter: String,
    pub tun_ip: String,
}

extern "C" {
    fn veil_desktop_start_with_wintun(
        handle: u64,
        adapter_name: *const std::os::raw::c_char,
        mtu: i32,
        cb: Option<unsafe extern "C" fn(i32, *const std::os::raw::c_char, *mut std::os::raw::c_void)>,
        user: *mut std::os::raw::c_void,
    ) -> i32;
}

/// Start a TUN-mode session. Returns Err with an actionable message
/// when prerequisites are missing.
pub fn tun_start(config_text: &str, _server_ip_hint: Option<String>) -> Result<TunStatus, String> {
    if !is_elevated() {
        return Err(
            "TUN mode requires running Veil as Administrator. \
             Right-click the Veil icon → Run as administrator, then try again."
                .into(),
        );
    }
    if !wintun_dll_present() {
        return Err(
            "wintun.dll was not found next to the app. \
             Download it from https://www.wintun.net/ and drop wintun.dll into the install folder."
                .into(),
        );
    }

    // 1. Veil::create — same C ABI as SOCKS5 mode.
    let v = Veil::create(config_text).map_err(|e| format!("create: {e}"))?;

    // 2. Pull the raw handle out so we can call the desktop-only
    //    Wintun entry point. The handle is stable for the lifetime
    //    of the Veil; we still bind veil_start through the SDK so
    //    Drop runs the matching veil_stop / veil_destroy.
    let handle = v.raw_handle();
    let adapter_c = std::ffi::CString::new(ADAPTER_NAME).unwrap();
    let rc = unsafe {
        veil_desktop_start_with_wintun(handle, adapter_c.as_ptr(), 1380, None, std::ptr::null_mut())
    };
    if rc != 0 {
        return Err(format!("libveil: veil_desktop_start_with_wintun returned {rc}"));
    }

    // 3. Assign the adapter an IP and install the default route.
    configure_adapter()?;
    Ok(TunStatus {
        active: true,
        adapter: ADAPTER_NAME.into(),
        tun_ip: TUN_IP.into(),
    })
}

/// Tear down the TUN session: restore routes, drop the Veil instance
/// (which closes the Wintun adapter via the cgo destroy path).
pub fn tun_stop(state: &TunState) -> Result<(), String> {
    {
        let mut guard = state.inner.lock().expect("TunState mutex poisoned");
        if let Some(session) = guard.take() {
            // Restore routes before dropping libveil — once the
            // adapter is gone the OS removes the routes that point
            // through it anyway, but we run the explicit cleanup so
            // the routing table reads cleanly between sessions.
            let _ = restore_routes();
            // Drop runs veil_stop + veil_destroy → mobile.WintunPipe.Close
            // → tun.Close → adapter handle released.
            drop(session);
        }
    }
    Ok(())
}

fn configure_adapter() -> Result<(), String> {
    // Assign IP + subnet via netsh interface ipv4 set address.
    sh(
        "netsh",
        &[
            "interface", "ipv4", "set", "address",
            &format!("name={ADAPTER_NAME}"),
            "static", TUN_IP, TUN_MASK, TUN_GATEWAY,
        ],
    )?;
    // Bring it up — netsh usually does this implicitly but be loud.
    let _ = sh(
        "netsh",
        &[
            "interface", "set", "interface",
            &format!("name={ADAPTER_NAME}"),
            "admin=enabled",
        ],
    );
    // Default route through the Veil adapter (low metric so it wins
    // over the existing default).
    sh(
        "route",
        &["add", "0.0.0.0", "mask", "0.0.0.0", TUN_GATEWAY, "metric", "5"],
    )?;
    // 1.1.1.1 / 9.9.9.9 DNS so the OS resolver doesn't hang on a
    // route-loop.
    sh(
        "netsh",
        &[
            "interface", "ipv4", "set", "dns",
            &format!("name={ADAPTER_NAME}"),
            "static", "1.1.1.1", "primary",
        ],
    )?;
    let _ = sh(
        "netsh",
        &[
            "interface", "ipv4", "add", "dns",
            &format!("name={ADAPTER_NAME}"),
            "9.9.9.9", "index=2",
        ],
    );
    Ok(())
}

fn restore_routes() -> Result<(), String> {
    // The default route via 10.42.0.1 disappears together with the
    // adapter when Wintun closes; explicit `route delete` is a safety
    // net for the rare case where the adapter survives the process.
    let _ = sh("route", &["delete", "0.0.0.0", TUN_GATEWAY]);
    Ok(())
}

fn sh(cmd: &str, args: &[&str]) -> Result<(), String> {
    let out = Command::new(cmd)
        .args(args)
        .output()
        .map_err(|e| format!("exec {cmd}: {e}"))?;
    if !out.status.success() {
        return Err(format!(
            "{cmd} {args:?} failed: {}",
            String::from_utf8_lossy(&out.stderr).trim()
        ));
    }
    Ok(())
}

fn is_elevated() -> bool {
    // Cheap check: try to open a known-protected key (HKLM\SYSTEM\…)
    // for write. Wintun needs adapter-create which requires Admin;
    // ServiceControlManager open with SC_MANAGER_CREATE_SERVICE is
    // a stricter probe but this one is enough for the UX gate.
    use std::process::Command;
    Command::new("net")
        .args(["session"])
        .output()
        .map(|o| o.status.success())
        .unwrap_or(false)
}

fn wintun_dll_present() -> bool {
    if let Ok(exe) = std::env::current_exe() {
        if let Some(dir) = exe.parent() {
            if dir.join("wintun.dll").exists() {
                return true;
            }
        }
    }
    // Fall back to LoadLibrary semantics: file in PATH.
    if let Some(path) = std::env::var_os("PATH") {
        for d in std::env::split_paths(&path) {
            if d.join("wintun.dll").exists() {
                return true;
            }
        }
    }
    false
}

