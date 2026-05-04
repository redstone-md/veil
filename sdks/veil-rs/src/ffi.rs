// Veil VPN — raw FFI declarations for libveil.
//
// Hand-written rather than bindgen-generated to keep the
// dependency surface minimal. Mirrors core/pkg/cgo/include/veil.h
// as of the v1 ABI.

use std::ffi::{c_char, c_int, c_void};

pub type VeilHandle = u64;

pub type VeilEventCallback =
    Option<unsafe extern "C" fn(kind: c_int, json_payload: *const c_char, user_data: *mut c_void)>;

extern "C" {
    pub fn veil_create(config_text: *const c_char) -> VeilHandle;
    pub fn veil_start(handle: VeilHandle, cb: VeilEventCallback, user_data: *mut c_void) -> c_int;
    pub fn veil_stop(handle: VeilHandle) -> c_int;
    pub fn veil_destroy(handle: VeilHandle);

    pub fn veil_get_metrics(handle: VeilHandle) -> *mut c_char;
    pub fn veil_version_string() -> *mut c_char;
    pub fn veil_free_string(s: *mut c_char);
}
