// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

//go:build cgo

// Package cgo exposes the Veil client as a C-callable shared library
// (libveil.{so,dll,dylib}). Build it with:
//
//	go build -buildmode=c-shared -o libveil.so ./pkg/cgo
//
// The header file emitted alongside the binary mirrors the public
// surface declared in veil.h (kept hand-written so the documented
// ABI does not drift with cgo's auto-generated declarations).
//
// Memory rules:
//   - Strings returned by veil_get_metrics / veil_version_string MUST
//     be released by the caller via veil_free_string.
//   - The opaque VeilHandle is an integer; it is safe to copy.
//   - Event-callback pointers must remain valid for the lifetime of
//     the started handle.
//
// Thread safety: every exported function is safe for concurrent use
// across handles. Operations on a single handle serialise internally.
package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef enum {
    VEIL_OK = 0,
    VEIL_ERR_INVALID_CONFIG = -1,
    VEIL_ERR_TRANSPORT_FAILED = -2,
    VEIL_ERR_AUTH_FAILED = -3,
    VEIL_ERR_NOT_RUNNING = -4,
    VEIL_ERR_ALREADY_RUNNING = -5,
    VEIL_ERR_BAD_HANDLE = -6,
    VEIL_ERR_INTERNAL = -99
} VeilError;

typedef enum {
    VEIL_EVENT_CONNECTED = 1,
    VEIL_EVENT_DISCONNECTED = 2,
    VEIL_EVENT_ERROR = 3,
    VEIL_EVENT_TRAFFIC = 4,
    VEIL_EVENT_TRANSPORT_SWITCH = 5
} VeilEventType;

typedef void (*VeilEventCallback)(int type, const char* json_payload, void* user_data);

// Bridge that lets the Go side dispatch into a function pointer
// without violating the cgo no-Go-pointers rule.
static inline void veil_invoke_event_cb(VeilEventCallback cb, int t, const char* j, void* u) {
    if (cb) cb(t, j, u);
}
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"unsafe"

	"gopkg.in/yaml.v3"

	"github.com/redstone-md/veil/core/internal/buildinfo"
	"github.com/redstone-md/veil/core/internal/client"
	"github.com/redstone-md/veil/core/internal/config"
	"github.com/redstone-md/veil/core/internal/mobile"
	"github.com/redstone-md/veil/core/internal/sharelink"
)

// instance is the runtime side of one VeilHandle.
type instance struct {
	cfg    *config.ClientConfig
	logger *slog.Logger

	cb   C.VeilEventCallback
	user unsafe.Pointer
	cbMu sync.Mutex

	mu      sync.Mutex
	cli     *client.Client
	cancel  context.CancelFunc
	running atomic.Bool

	// Mobile-only: TUN pipes attached via veil_mobile_start_with_tun
	// (Android, fdPipe) or veil_ne_start (iOS, cbPipe). Both are nil
	// for desktop / CLI sessions.
	fdPipe *mobile.FDPipe
	cbPipe *mobile.CallbackPipe
}

// Registry of live instances keyed by handle.
var (
	regMu     sync.RWMutex
	registry  = make(map[uint64]*instance)
	nextHndle uint64
)

func newHandle(inst *instance) uint64 {
	regMu.Lock()
	defer regMu.Unlock()
	for {
		nextHndle++
		if nextHndle == 0 {
			continue // skip zero so it can act as a sentinel
		}
		if _, taken := registry[nextHndle]; !taken {
			registry[nextHndle] = inst
			return nextHndle
		}
	}
}

func lookup(h uint64) *instance {
	regMu.RLock()
	defer regMu.RUnlock()
	return registry[h]
}

func drop(h uint64) {
	regMu.Lock()
	delete(registry, h)
	regMu.Unlock()
}

// loadConfig accepts a JSON, YAML, or veil:// share-link payload.
// Auto-detect: leading "veil://" → share-link decode; leading '{' →
// JSON; otherwise YAML. The share-link branch matches the CLI's
// `connect --link` behaviour so SDK callers (desktop / mobile / Node /
// Python / Rust) can hand the same one-line string straight to
// veil_create without an extra decode step.
func loadConfig(payload string) (*config.ClientConfig, error) {
	trimmed := []byte(payload)
	if len(trimmed) >= 7 && string(trimmed[:7]) == "veil://" {
		c, err := sharelink.Decode(string(trimmed))
		if err != nil {
			return nil, err
		}
		return c, c.Validate()
	}
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var c config.ClientConfig
		if err := json.Unmarshal(trimmed, &c); err == nil {
			return &c, c.Validate()
		}
	}
	var c config.ClientConfig
	if err := yaml.Unmarshal(trimmed, &c); err != nil {
		return nil, err
	}
	return &c, c.Validate()
}

// veil_create — parse the supplied config (JSON or YAML) and return
// an opaque handle. Returns 0 on failure.
//
//export veil_create
func veil_create(cfgPtr *C.char) C.uint64_t {
	cfg, err := loadConfig(C.GoString(cfgPtr))
	if err != nil {
		return 0
	}
	inst := &instance{
		cfg:    cfg,
		logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	return C.uint64_t(newHandle(inst))
}

// veil_start — bring the client up. The callback (may be NULL)
// receives JSON-encoded VeilEvent payloads. user_data is passed back
// unchanged.
//
//export veil_start
func veil_start(handle C.uint64_t, cb C.VeilEventCallback, user unsafe.Pointer) C.int {
	inst := lookup(uint64(handle))
	if inst == nil {
		return C.VEIL_ERR_BAD_HANDLE
	}
	inst.mu.Lock()
	if inst.running.Load() {
		inst.mu.Unlock()
		return C.VEIL_ERR_ALREADY_RUNNING
	}
	inst.cb = cb
	inst.user = user
	inst.cli = client.New(inst.cfg, inst.logger,
		client.ListenerFunc(inst.onEvent))
	ctx, cancel := context.WithCancel(context.Background())
	inst.cancel = cancel
	inst.running.Store(true)
	inst.mu.Unlock()

	go func() {
		defer inst.running.Store(false)
		_ = inst.cli.Run(ctx)
	}()
	return C.VEIL_OK
}

// veil_stop — request a graceful shutdown of a running client.
//
//export veil_stop
func veil_stop(handle C.uint64_t) C.int {
	inst := lookup(uint64(handle))
	if inst == nil {
		return C.VEIL_ERR_BAD_HANDLE
	}
	inst.mu.Lock()
	cancel := inst.cancel
	cli := inst.cli
	inst.mu.Unlock()
	if !inst.running.Load() {
		return C.VEIL_ERR_NOT_RUNNING
	}
	if cli != nil {
		cli.Stop()
	}
	if cancel != nil {
		cancel()
	}
	return C.VEIL_OK
}

// veil_destroy — free all resources associated with a handle. The
// handle MUST NOT be used after this call. Stops the client first
// if it is still running.
//
//export veil_destroy
func veil_destroy(handle C.uint64_t) {
	inst := lookup(uint64(handle))
	if inst == nil {
		return
	}
	if inst.running.Load() {
		veil_stop(handle)
	}
	inst.mu.Lock()
	fd := inst.fdPipe
	cb := inst.cbPipe
	inst.fdPipe = nil
	inst.cbPipe = nil
	inst.mu.Unlock()
	if fd != nil {
		fd.Close()
	}
	if cb != nil {
		cb.Close()
	}
	drop(uint64(handle))
}

// veil_get_metrics — return a JSON-encoded snapshot of the client's
// runtime metrics. The caller MUST free the returned pointer with
// veil_free_string.
//
//export veil_get_metrics
func veil_get_metrics(handle C.uint64_t) *C.char {
	inst := lookup(uint64(handle))
	if inst == nil {
		return C.CString(`{"error":"bad handle"}`)
	}
	inst.mu.Lock()
	cli := inst.cli
	inst.mu.Unlock()
	if cli == nil {
		return C.CString(`{"running":false}`)
	}
	return C.CString(cli.MetricsJSON())
}

// veil_free_string — release a string previously returned by Veil.
//
//export veil_free_string
func veil_free_string(s *C.char) {
	if s != nil {
		C.free(unsafe.Pointer(s))
	}
}

// veil_version_string — return the Veil version string. Caller
// MUST free with veil_free_string.
//
//export veil_version_string
func veil_version_string() *C.char {
	v := struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Date    string `json:"date"`
	}{
		Version: buildinfo.Version,
		Commit:  buildinfo.Commit,
		Date:    buildinfo.Date,
	}
	b, _ := json.Marshal(v)
	return C.CString(string(b))
}

// onEvent dispatches a Go-side event into the C callback if any.
func (inst *instance) onEvent(e client.Event) {
	inst.cbMu.Lock()
	cb := inst.cb
	user := inst.user
	inst.cbMu.Unlock()
	if cb == nil {
		return
	}
	payload, err := json.Marshal(e)
	if err != nil {
		return
	}
	cstr := C.CString(string(payload))
	C.veil_invoke_event_cb(cb, C.int(e.Type), cstr, user)
	// We free the payload after the callback returns. Callers that
	// need to retain the string MUST copy it before the callback
	// returns; this matches the documented contract.
	C.free(unsafe.Pointer(cstr))
}

// main is required for buildmode=c-shared but is never invoked.
func main() {}

// keep imports referenced even if some paths above shake out.
var _ = errors.New
