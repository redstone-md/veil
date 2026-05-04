// Veil VPN — safe Rust bindings for libveil.
//
// libveil is a C-shared library produced from the upstream Go core
// (`go build -buildmode=c-shared -o libveil.* ./pkg/cgo`). The
// public surface mirrors `core/pkg/cgo/include/veil.h`; this crate
// adds the Rust ergonomics (RAII handles, typed events, Result-
// returning operations, callbacks as closures).
//
// Linking: at build time the consumer must point cargo at libveil.
// The simplest setup is to drop libveil.so / libveil.dylib /
// veil.dll next to the final binary and let the dynamic loader find
// it; for static linking, build libveil with -buildmode=c-archive
// instead and configure cargo with -lveil + the path.

use std::{
    ffi::{c_char, c_int, c_void, CStr, CString},
    sync::{Arc, Mutex},
};

use serde::Deserialize;
use thiserror::Error;

mod ffi;

/// All errors surfaced by the Rust bindings.
#[derive(Debug, Error)]
pub enum Error {
    /// libveil reported a non-zero error code.
    #[error("libveil error: {0:?}")]
    Lib(LibCode),
    /// The supplied configuration is invalid (could not be parsed
    /// or rejected by libveil at create time).
    #[error("invalid config")]
    InvalidConfig,
    /// The Veil instance has already been destroyed.
    #[error("instance destroyed")]
    Destroyed,
    /// A returned string was not valid UTF-8.
    #[error("invalid utf-8 from libveil: {0}")]
    Utf8(#[from] std::str::Utf8Error),
    /// A returned JSON payload could not be parsed.
    #[error("invalid json from libveil: {0}")]
    Json(#[from] serde_json::Error),
}

/// libveil error codes, mirroring the values in veil.h.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(i32)]
pub enum LibCode {
    InvalidConfig = -1,
    TransportFailed = -2,
    AuthFailed = -3,
    NotRunning = -4,
    AlreadyRunning = -5,
    BadHandle = -6,
    Internal = -99,
    Other(i32),
}

impl LibCode {
    fn from_raw(code: i32) -> Self {
        match code {
            -1 => Self::InvalidConfig,
            -2 => Self::TransportFailed,
            -3 => Self::AuthFailed,
            -4 => Self::NotRunning,
            -5 => Self::AlreadyRunning,
            -6 => Self::BadHandle,
            -99 => Self::Internal,
            _ => Self::Other(code),
        }
    }
}

/// Categories of runtime events emitted by a running Veil instance.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(i32)]
pub enum EventType {
    Connected = 1,
    Disconnected = 2,
    Error = 3,
    Traffic = 4,
    TransportSwitch = 5,
}

impl EventType {
    fn from_raw(code: i32) -> Option<Self> {
        Some(match code {
            1 => Self::Connected,
            2 => Self::Disconnected,
            3 => Self::Error,
            4 => Self::Traffic,
            5 => Self::TransportSwitch,
            _ => return None,
        })
    }
}

/// Parsed event payload delivered to user callbacks.
#[derive(Debug, Clone, Default, Deserialize)]
pub struct Event {
    #[serde(rename = "type", default)]
    pub kind: i32,
    #[serde(default)]
    pub message: String,
    #[serde(default)]
    pub transport: String,
    #[serde(default)]
    pub remote: String,
    #[serde(default)]
    pub bytes_tx: i64,
    #[serde(default)]
    pub bytes_rx: i64,
}

impl Event {
    /// Returns the event kind as a typed enum, when recognised.
    pub fn typed(&self) -> Option<EventType> {
        EventType::from_raw(self.kind)
    }
}

/// Snapshot of a running Veil instance's metrics.
#[derive(Debug, Clone, Deserialize)]
pub struct Metrics {
    pub running: bool,
    #[serde(default)]
    pub bytes_tx: i64,
    #[serde(default)]
    pub bytes_rx: i64,
}

/// libveil version metadata.
#[derive(Debug, Clone, Deserialize)]
pub struct Version {
    pub version: String,
    pub commit: String,
    pub date: String,
}

/// Type-erased event callback invoked on libveil's reporter goroutine.
///
/// The callback runs on a Veil-internal thread; consumers MUST NOT
/// block. Marshall the event into your own runtime (channel, async
/// task, UI thread) and return promptly.
pub type EventHandler = Arc<dyn Fn(Event) + Send + Sync + 'static>;

/// A live Veil client instance. Drop stops the client (if running)
/// and releases all native resources.
pub struct Veil {
    handle: ffi::VeilHandle,
    callback: Mutex<Option<EventHandler>>,
}

unsafe impl Send for Veil {}
unsafe impl Sync for Veil {}

impl Veil {
    /// Construct a Veil client from a JSON or YAML configuration
    /// string. The format is auto-detected.
    pub fn create(config_text: &str) -> Result<Self, Error> {
        let cstr = CString::new(config_text).map_err(|_| Error::InvalidConfig)?;
        // SAFETY: cstr stays alive for the duration of the call.
        let handle = unsafe { ffi::veil_create(cstr.as_ptr()) };
        if handle == 0 {
            return Err(Error::InvalidConfig);
        }
        Ok(Veil {
            handle,
            callback: Mutex::new(None),
        })
    }

    /// Start the client. The supplied callback (if any) receives
    /// every runtime event.
    pub fn start(&self, callback: Option<EventHandler>) -> Result<(), Error> {
        // Stash the callback so the C trampoline can find it.
        {
            let mut slot = self.callback.lock().expect("callback poisoned");
            *slot = callback;
        }
        // Pass `self` as the user_data so the trampoline can locate
        // the callback. The handle's lifetime is bounded by Drop on
        // Veil, which calls veil_stop before veil_destroy.
        let user = self as *const Veil as *mut c_void;
        // SAFETY: trampoline reads through user only while the Veil
        // instance is alive; Drop tears the callback down before the
        // instance disappears.
        let rc = unsafe { ffi::veil_start(self.handle, Some(event_trampoline), user) };
        if rc == 0 {
            Ok(())
        } else {
            Err(Error::Lib(LibCode::from_raw(rc)))
        }
    }

    /// Request a graceful stop. Returns immediately; shutdown
    /// completes on a background thread.
    pub fn stop(&self) -> Result<(), Error> {
        let rc = unsafe { ffi::veil_stop(self.handle) };
        if rc == 0 {
            Ok(())
        } else {
            Err(Error::Lib(LibCode::from_raw(rc)))
        }
    }

    /// Snapshot of the client's runtime metrics.
    pub fn metrics(&self) -> Result<Metrics, Error> {
        let raw = unsafe { ffi::veil_get_metrics(self.handle) };
        if raw.is_null() {
            return Err(Error::Lib(LibCode::Internal));
        }
        let json = unsafe { take_string(raw)? };
        Ok(serde_json::from_str(&json)?)
    }

    /// Library version metadata (does not require a started instance).
    pub fn library_version() -> Result<Version, Error> {
        let raw = unsafe { ffi::veil_version_string() };
        if raw.is_null() {
            return Err(Error::Lib(LibCode::Internal));
        }
        let json = unsafe { take_string(raw)? };
        Ok(serde_json::from_str(&json)?)
    }
}

impl Drop for Veil {
    fn drop(&mut self) {
        // Stop and destroy in that order; veil_destroy is a no-op
        // for an already-zeroed handle so a second drop is harmless.
        unsafe {
            let _ = ffi::veil_stop(self.handle);
            ffi::veil_destroy(self.handle);
        }
        // Drop the callback last so the trampoline can never observe
        // a destroyed Veil.
        let mut slot = self.callback.lock().expect("callback poisoned");
        *slot = None;
    }
}

/// Trampoline reachable from C. user_data is the *const Veil we
/// supplied at start time.
unsafe extern "C" fn event_trampoline(kind: c_int, json: *const c_char, user: *mut c_void) {
    if user.is_null() || json.is_null() {
        return;
    }
    // SAFETY: caller (the Veil core) guarantees user_data is the
    // pointer we registered in veil.start; lifetime is bounded by
    // Drop on Veil which clears the callback before the instance
    // disappears.
    let veil = unsafe { &*(user as *const Veil) };
    let cb = match veil.callback.lock() {
        Ok(g) => g.clone(),
        Err(_) => return,
    };
    let Some(cb) = cb else { return };
    let json_str = match unsafe { CStr::from_ptr(json) }.to_str() {
        Ok(s) => s,
        Err(_) => return,
    };
    let mut ev: Event = serde_json::from_str(json_str).unwrap_or_default();
    if ev.kind == 0 {
        ev.kind = kind as i32;
    }
    cb(ev);
}

/// Take ownership of a libveil-allocated string and free the
/// original after copying the bytes into Rust-owned memory.
unsafe fn take_string(raw: *mut c_char) -> Result<String, std::str::Utf8Error> {
    let s = unsafe { CStr::from_ptr(raw) }.to_str()?.to_owned();
    unsafe { ffi::veil_free_string(raw) };
    Ok(s)
}
