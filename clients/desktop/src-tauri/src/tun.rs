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

use serde::{Deserialize, Serialize};
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
    /// CIDRs we explicitly routed through the original gateway. Kept
    /// so the stop path knows what to clean up.
    pub bypass_routes: Vec<String>,
    pub original_gateway: Option<String>,
}

#[derive(Debug, Serialize)]
pub struct TunStatus {
    pub active: bool,
    pub adapter: String,
    pub tun_ip: String,
    pub bypass_count: usize,
    pub original_gateway: Option<String>,
}

#[derive(Debug, Deserialize, Default)]
pub struct TunStartArgs {
    /// JSON or YAML or veil:// share link.
    pub config_text: String,
    /// CIDRs (or single IPs) the user wants kept off the tunnel —
    /// LAN, gaming services, etc.
    #[serde(default)]
    pub bypass_cidrs: Vec<String>,
    /// Server IPs (or hostnames the host already resolved). We
    /// auto-bypass these so libveil's own outbound dial doesn't
    /// loop through its own TUN. Pass at least the IPs of every
    /// transport in the active config.
    #[serde(default)]
    pub server_ips: Vec<String>,
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
pub fn tun_start(args: TunStartArgs) -> Result<(TunStatus, TunSession), String> {
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

    // 0. Capture the OS's existing default gateway BEFORE we install
    //    the Wintun-pointing default; we need it for every bypass
    //    route we install in step 2.
    let original_gw = original_default_gateway();
    let original_gw_str = original_gw
        .clone()
        .ok_or_else(|| "Could not determine the existing default gateway. Are you online?".to_string())?;

    // 1. Veil::create — same C ABI as SOCKS5 mode.
    let v = Veil::create(&args.config_text).map_err(|e| format!("create: {e}"))?;

    // 2. Compute the bypass route set: server IPs (mandatory — without
    //    them libveil's outbound dial loops through its own TUN) plus
    //    user-supplied "always direct" CIDRs.
    let mut bypass: Vec<String> = Vec::new();
    for ip in &args.server_ips {
        let cidr = if ip.contains('/') { ip.clone() } else { format!("{ip}/32") };
        if !bypass.contains(&cidr) {
            bypass.push(cidr);
        }
    }
    for c in &args.bypass_cidrs {
        let c = c.trim();
        if c.is_empty() { continue; }
        let cidr = if c.contains('/') { c.into() } else { format!("{c}/32") };
        if !bypass.contains(&cidr) {
            bypass.push(cidr);
        }
    }

    // Install bypass routes BEFORE the Wintun default — they have
    // metric=1 so they always win over the metric=5 default.
    for cidr in &bypass {
        if let Err(e) = add_bypass_route(cidr, &original_gw_str) {
            // Log but don't abort; one bad CIDR shouldn't kill the
            // whole connect.
            eprintln!("tun: failed to add bypass route {cidr}: {e}");
        }
    }

    // 3. Pull the raw handle out so we can call the desktop-only
    //    Wintun entry point. The handle is stable for the lifetime
    //    of the Veil; we still bind veil_start through the SDK so
    //    Drop runs the matching veil_stop / veil_destroy.
    let handle = v.raw_handle();
    let adapter_c = std::ffi::CString::new(ADAPTER_NAME).unwrap();
    let rc = unsafe {
        veil_desktop_start_with_wintun(handle, adapter_c.as_ptr(), 1380, None, std::ptr::null_mut())
    };
    if rc != 0 {
        // Clean up the bypass routes we already installed before
        // bubbling the error up.
        for cidr in &bypass {
            let _ = del_bypass_route(cidr, &original_gw_str);
        }
        return Err(format!("libveil: veil_desktop_start_with_wintun returned {rc}"));
    }

    // 4. Assign the adapter an IP and install the default route.
    configure_adapter()?;

    let status = TunStatus {
        active: true,
        adapter: ADAPTER_NAME.into(),
        tun_ip: TUN_IP.into(),
        bypass_count: bypass.len(),
        original_gateway: Some(original_gw_str.clone()),
    };
    let session = TunSession {
        veil: v,
        bypass_routes: bypass,
        original_gateway: Some(original_gw_str),
    };
    Ok((status, session))
}

/// Tear down a previously-started TUN session. Removes every bypass
/// route we installed, lets the Wintun adapter close via the libveil
/// destroy path, and finally cleans the explicit default route.
pub fn tun_stop(session: TunSession) -> Result<(), String> {
    // 1. Drop user-installed bypass routes first so the routing
    //    table doesn't leak entries between sessions.
    if let Some(gw) = session.original_gateway.as_deref() {
        for cidr in &session.bypass_routes {
            let _ = del_bypass_route(cidr, gw);
        }
    }
    // 2. Restore the default route. The adapter teardown removes
    //    routes through it implicitly, but we run the explicit
    //    cleanup so a torn-down session leaves the table clean.
    let _ = restore_routes();
    // 3. Drop the Veil — runs veil_stop + veil_destroy → WintunPipe
    //    → adapter handle released.
    drop(session.veil);
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

fn add_bypass_route(cidr: &str, gateway: &str) -> Result<(), String> {
    let (dest, mask) = parse_cidr(cidr)?;
    sh("route", &["add", &dest, "mask", &mask, gateway, "metric", "1"])
}

fn del_bypass_route(cidr: &str, gateway: &str) -> Result<(), String> {
    let (dest, mask) = parse_cidr(cidr)?;
    sh("route", &["delete", &dest, "mask", &mask, gateway])
}

fn parse_cidr(cidr: &str) -> Result<(String, String), String> {
    if let Some((ip, prefix)) = cidr.split_once('/') {
        let bits: u8 = prefix.parse().map_err(|_| format!("bad CIDR prefix in {cidr}"))?;
        if bits > 32 {
            return Err(format!("CIDR prefix > 32 in {cidr}"));
        }
        Ok((ip.into(), prefix_to_mask(bits)))
    } else {
        Ok((cidr.into(), "255.255.255.255".into()))
    }
}

fn prefix_to_mask(prefix: u8) -> String {
    let mask: u32 = if prefix == 0 { 0 } else { (!0u32) << (32 - prefix) };
    let octets = mask.to_be_bytes();
    format!("{}.{}.{}.{}", octets[0], octets[1], octets[2], octets[3])
}

/// Best-effort current default gateway. Parses `route print 0.0.0.0`.
fn original_default_gateway() -> Option<String> {
    let out = Command::new("route")
        .args(["print", "0.0.0.0"])
        .output()
        .ok()?;
    let text = String::from_utf8_lossy(&out.stdout);
    for line in text.lines() {
        let parts: Vec<&str> = line.split_whitespace().collect();
        // Active Routes section rows: dest mask gateway interface metric
        if parts.len() >= 4 && parts[0] == "0.0.0.0" && parts[1] == "0.0.0.0" {
            // Skip rows that point AT our own Wintun (TUN_GATEWAY) — we
            // want the gateway that existed BEFORE we added the TUN
            // default.
            if parts[2] == TUN_GATEWAY {
                continue;
            }
            return Some(parts[2].into());
        }
    }
    None
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

