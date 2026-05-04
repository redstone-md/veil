/*
 * Veil VPN — public C ABI.
 *
 * Copyright 2026 Veil VPN Project Contributors
 * Licensed under the Apache License, Version 2.0
 *
 * This header documents the stable C surface of libveil. The shared
 * library (libveil.so / libveil.dylib / veil.dll) is built from
 * pkg/cgo/ in this repository.
 *
 * Memory ownership rules:
 *   - Strings returned by veil_get_metrics() and veil_version_string()
 *     are heap-allocated by Veil; the caller MUST free them via
 *     veil_free_string.
 *   - Configuration strings handed to veil_create() are copied
 *     internally; the caller may free them immediately after the
 *     call returns.
 *   - The void* user_data carried through callbacks is opaque to
 *     Veil; lifetime is the caller's responsibility.
 *
 * Threading rules:
 *   - All functions are safe to call from any thread.
 *   - The event callback is invoked from a Veil-owned goroutine. Do
 *     NOT block in the callback; copy the payload and dispatch into
 *     the consumer's UI thread for any non-trivial work.
 *
 * ABI versioning: this header reflects the v1 ABI. Breaking changes
 * are signalled by a major version bump of the library and a new
 * header. Within v1, additions are backwards-compatible (new types
 * appended, new functions appended).
 */

#ifndef VEIL_H
#define VEIL_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Opaque handle. Compare to 0 to detect failure. */
typedef uint64_t VeilHandle;

/* Error codes. Negative for errors, 0 for success. */
typedef enum {
    VEIL_OK                 = 0,
    VEIL_ERR_INVALID_CONFIG = -1,
    VEIL_ERR_TRANSPORT_FAILED = -2,
    VEIL_ERR_AUTH_FAILED    = -3,
    VEIL_ERR_NOT_RUNNING    = -4,
    VEIL_ERR_ALREADY_RUNNING = -5,
    VEIL_ERR_BAD_HANDLE     = -6,
    VEIL_ERR_INTERNAL       = -99
} VeilError;

/* Event taxonomy. The numeric values are part of the ABI; do not
 * renumber. */
typedef enum {
    VEIL_EVENT_CONNECTED        = 1,
    VEIL_EVENT_DISCONNECTED     = 2,
    VEIL_EVENT_ERROR            = 3,
    VEIL_EVENT_TRAFFIC          = 4,
    VEIL_EVENT_TRANSPORT_SWITCH = 5
} VeilEventType;

/*
 * Callback signature for runtime events.
 *
 *   type         — one of VeilEventType
 *   json_payload — JSON-encoded event body. Valid only for the
 *                  duration of the callback.
 *   user_data    — opaque pointer passed to veil_start.
 */
typedef void (*VeilEventCallback)(
    int type,
    const char* json_payload,
    void* user_data);

/*
 * Lifecycle.
 *
 * veil_create — parse the supplied configuration (JSON or YAML
 *               text; auto-detected by leading character) and return
 *               a non-zero handle on success, 0 on failure.
 *
 * veil_start  — bring the client up. Spawns internal goroutines
 *               that handle the transport, session, SOCKS5 listener
 *               and decoy engine. cb may be NULL to opt out of
 *               events; user_data is opaque.
 *
 * veil_stop   — request graceful shutdown. Returns when the stop
 *               request is queued, not when shutdown is complete.
 *
 * veil_destroy — release every resource associated with the handle.
 *                Calls veil_stop first if the handle is still
 *                running. The handle MUST NOT be used after this.
 */
VeilHandle veil_create(const char* config_text);
int        veil_start(VeilHandle handle, VeilEventCallback cb, void* user_data);
int        veil_stop(VeilHandle handle);
void       veil_destroy(VeilHandle handle);

/*
 * Observation.
 *
 * veil_get_metrics — JSON snapshot { running, bytes_tx, bytes_rx }.
 *                    Caller MUST free the returned string with
 *                    veil_free_string.
 *
 * veil_version_string — JSON { version, commit, date } describing
 *                       the loaded library. Free with
 *                       veil_free_string.
 */
char*       veil_get_metrics(VeilHandle handle);
char*       veil_version_string(void);

/*
 * Memory.
 */
void        veil_free_string(char* s);

/*
 * Mobile / TUN integration.
 *
 * The mobile clients (clients/mobile/) have to bridge an OS-supplied
 * packet tunnel to the Veil session. Two integration shapes are
 * provided since the two mobile platforms expose tunnels very
 * differently.
 *
 * --- Android (file-descriptor model) ---
 *
 * veil_mobile_start_with_tun — like veil_start, plus a TUN file
 *   descriptor that libveil owns for the lifetime of the session.
 *   libveil drives a tun2socks pipe between the fd and its internal
 *   SOCKS5 listener so packets the OS writes to the TUN flow through
 *   the Veil session and back. Returns the same error codes as
 *   veil_start; on success the handle behaves identically to a
 *   handle returned by veil_start.
 *
 * --- iOS (callback model) ---
 *
 * NEPacketTunnelProvider does not give the host a TUN fd; it gives
 * read/write callbacks on a NEPacketTunnelFlow. Three calls bridge
 * that into libveil:
 *
 *   veil_ne_start         — start the session and register a callback
 *                           libveil calls when it has a packet to
 *                           deliver to the OS.
 *   veil_ne_ingest_packet — push one IP packet from the OS into the
 *                           libveil tun2socks pipe.
 *   veil_ne_emit_callback — function-pointer signature libveil invokes
 *                           when it produces a packet for the OS.
 *
 * Both shapes are no-ops for the SOCKS5-only desktop / CLI flows;
 * they exist only for the mobile bring-up.
 */
typedef void (*VeilEmitPacketCallback)(const uint8_t* data, int len, int family, void* user_data);

int  veil_mobile_start_with_tun(VeilHandle handle, int tun_fd, VeilEventCallback cb, void* user_data);

int  veil_ne_start(VeilHandle handle, VeilEventCallback cb, VeilEmitPacketCallback emit, void* user_data);
int  veil_ne_ingest_packet(VeilHandle handle, const uint8_t* data, int len, int family);

#ifdef __cplusplus
} /* extern "C" */
#endif

#endif /* VEIL_H */
