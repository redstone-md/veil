// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

//go:build cgo && windows

// Wintun-based desktop entry point. The matching Tauri host on the
// Veil desktop client invokes this after elevating to Administrator
// and assigning an IPv4 address + default route to the adapter.

package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef void (*VeilEventCallback)(int type, const char* json_payload, void* user_data);
*/
import "C"

import (
	"unsafe"

	"github.com/redstone-md/veil/core/internal/mobile"
)

// veil_desktop_start_with_wintun — Windows TUN entry point.
//
//export veil_desktop_start_with_wintun
func veil_desktop_start_with_wintun(handle C.uint64_t, adapterName *C.char, mtu C.int, cb C.VeilEventCallback, user unsafe.Pointer) C.int {
	if adapterName == nil {
		return -99
	}
	rc := veil_start(handle, cb, user)
	if rc != 0 {
		return rc
	}
	inst := lookup(uint64(handle))
	if inst == nil {
		return -6
	}
	wp, err := mobile.AttachWintun(C.GoString(adapterName), int(mtu), inst.cli, inst.logger)
	if err != nil {
		_ = veil_stop(handle)
		return -99
	}
	inst.mu.Lock()
	inst.wintun = wp
	inst.mu.Unlock()
	return 0
}
