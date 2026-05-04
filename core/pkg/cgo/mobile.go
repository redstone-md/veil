// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

//go:build cgo

// Mobile-specific cgo entry points. The desktop and CLI code paths
// terminate at a SOCKS5 listener; mobile clients have to bridge an
// OS-supplied packet tunnel to that SOCKS5 instead. This file adds
// two integration shapes covering the two mobile platforms:
//
//   * veil_mobile_start_with_tun — Android. The OS hands us a TUN
//     file descriptor (the one returned by VpnService.Builder.establish);
//     libveil owns it for the lifetime of the session and runs an
//     internal tun2socks pipe between it and the SOCKS5 listener.
//
//   * veil_ne_start / veil_ne_ingest_packet — iOS. NEPacketTunnelProvider
//     gives the host read/write callbacks on a NEPacketTunnelFlow rather
//     than a fd; libveil exposes ingest + emit-callback hooks the Swift
//     side wires into packetFlow.readPackets / writePackets.
//
// The actual packet pump is implemented in internal/mobile/tun_pipe.go;
// this file is only the cgo / handle-management layer.

package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef enum {
    VEIL_MOBILE_OK              = 0,
    VEIL_MOBILE_ERR_BAD_HANDLE  = -6,
    VEIL_MOBILE_ERR_INTERNAL    = -99
} VeilMobileError;

typedef void (*VeilEventCallback)(int type, const char* json_payload, void* user_data);
typedef void (*VeilEmitPacketCallback)(const uint8_t* data, int len, int family, void* user_data);

// Bridge functions: cgo forbids calling Go-typed function pointers from
// Go code, so we trampoline through a C inline shim.
static inline void veil_invoke_emit_cb(VeilEmitPacketCallback cb, const uint8_t* d, int n, int family, void* u) {
    if (cb) cb(d, n, family, u);
}
*/
import "C"

import (
	"unsafe"

	"github.com/redstone-md/veil/core/internal/mobile"
)

// veil_mobile_start_with_tun — Android entry point.
//
// Brings the client up exactly like veil_start and additionally
// installs an internal tun2socks pipe owning the supplied TUN file
// descriptor. The fd MUST remain valid for the lifetime of the
// session; libveil closes it on veil_destroy. Negative tun_fd or a
// bad handle returns the same error codes as veil_start.
//
//export veil_mobile_start_with_tun
func veil_mobile_start_with_tun(handle C.uint64_t, tunFd C.int, cb C.VeilEventCallback, user unsafe.Pointer) C.int {
	if tunFd < 0 {
		return C.int(C.VEIL_MOBILE_ERR_INTERNAL)
	}
	rc := veil_start(handle, cb, user)
	if rc != 0 {
		return rc
	}
	inst := lookup(uint64(handle))
	if inst == nil {
		return C.int(C.VEIL_MOBILE_ERR_BAD_HANDLE)
	}
	pipe, err := mobile.AttachFD(int(tunFd), inst.cli, inst.logger)
	if err != nil {
		_ = veil_stop(handle)
		return C.int(C.VEIL_MOBILE_ERR_INTERNAL)
	}
	inst.mu.Lock()
	inst.fdPipe = pipe
	inst.mu.Unlock()
	return C.int(C.VEIL_MOBILE_OK)
}

// veil_ne_start — iOS entry point.
//
// Brings the client up like veil_start and registers an emit-packet
// callback the Swift side reads packetFlow.writePackets out of. The
// callback may be NULL during early bring-up (the session simply
// drops outbound packets); production callers should always supply
// one.
//
//export veil_ne_start
func veil_ne_start(handle C.uint64_t, cb C.VeilEventCallback, emit C.VeilEmitPacketCallback, user unsafe.Pointer) C.int {
	rc := veil_start(handle, cb, user)
	if rc != 0 {
		return rc
	}
	inst := lookup(uint64(handle))
	if inst == nil {
		return C.int(C.VEIL_MOBILE_ERR_BAD_HANDLE)
	}

	// Wrap the C function pointer behind a Go closure so the
	// internal tun pipe can stay platform-agnostic.
	var emitFn func([]byte, int)
	if emit != nil {
		emitFn = func(p []byte, family int) {
			if len(p) == 0 {
				return
			}
			C.veil_invoke_emit_cb(emit, (*C.uint8_t)(unsafe.Pointer(&p[0])), C.int(len(p)), C.int(family), user)
		}
	}
	pipe, err := mobile.AttachCallback(emitFn, inst.cli, inst.logger)
	if err != nil {
		_ = veil_stop(handle)
		return C.int(C.VEIL_MOBILE_ERR_INTERNAL)
	}
	inst.mu.Lock()
	inst.cbPipe = pipe
	inst.mu.Unlock()
	return C.int(C.VEIL_MOBILE_OK)
}

// veil_ne_ingest_packet — iOS entry point. Push one IP packet from
// packetFlow.readPackets into the tun2socks pipe. family is 4 for
// AF_INET, 6 for AF_INET6.
//
//export veil_ne_ingest_packet
func veil_ne_ingest_packet(handle C.uint64_t, data *C.uint8_t, n C.int, family C.int) C.int {
	if data == nil || n <= 0 {
		return 0
	}
	inst := lookup(uint64(handle))
	if inst == nil {
		return C.int(C.VEIL_MOBILE_ERR_BAD_HANDLE)
	}
	inst.mu.Lock()
	pipe := inst.cbPipe
	inst.mu.Unlock()
	if pipe == nil {
		return C.int(C.VEIL_MOBILE_ERR_INTERNAL)
	}
	// Copy because Swift owns the buffer only for the duration of
	// this call; the pipe may queue the slice for asynchronous
	// processing.
	buf := C.GoBytes(unsafe.Pointer(data), n)
	pipe.Ingest(buf, int(family))
	return C.int(C.VEIL_MOBILE_OK)
}
