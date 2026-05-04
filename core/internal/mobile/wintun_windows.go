// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

//go:build windows

// Wintun bridge for the Windows desktop client.
//
// Opens (or creates) a Wintun adapter, then attaches the existing
// CallbackPipe machinery to it so that:
//
//   - packets the OS writes into the Wintun adapter are read in a
//     batch loop and forwarded to CallbackPipe.Ingest, which feeds
//     the in-process gVisor netstack and ultimately dials the
//     SOCKS5 listener for each TCP / UDP flow,
//
//   - packets the netstack produces are emitted via the same
//     callback shape we already use for iOS — the callback here
//     turns around and writes them back to the Wintun adapter.
//
// Bundling: wintun.dll must sit next to the loading binary (the
// desktop client's veil-desktop.exe) or on PATH. The wireguard-go
// bindings call `windows.LoadLibrary("wintun.dll")` lazily at
// CreateTUN time, so the file is only required when the operator
// flips into TUN mode — SOCKS5-only sessions never touch it.

package mobile

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"golang.zx2c4.com/wireguard/tun"

	"github.com/redstone-md/veil/core/internal/client"
)

// WintunPipe owns a Wintun adapter for the duration of a desktop
// TUN session. Construction creates the adapter, installs the gVisor
// netstack on top of a CallbackPipe, and starts the read pump.
type WintunPipe struct {
	dev    tun.Device
	pipe   *CallbackPipe
	logger *slog.Logger

	closed atomic.Bool
	wg     sync.WaitGroup
}

// AttachWintun opens (or creates) a Wintun adapter named adapterName
// with the given MTU, and wires it through CallbackPipe → SOCKS5.
//
// The caller MUST already have arranged the IP address and default
// route on the adapter before any traffic flows; the wireguard-go
// bindings do not assign addresses themselves. The desktop Tauri
// host does that step via netsh / Win32 routing API.
func AttachWintun(adapterName string, mtu int, cli *client.Client, logger *slog.Logger) (*WintunPipe, error) {
	if cli == nil {
		return nil, errors.New("mobile: nil client")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if mtu <= 0 {
		mtu = 1380
	}
	if !pipeActive.CompareAndSwap(false, true) {
		return nil, errors.New("mobile: another tun2socks pipe is already running in this process")
	}
	dev, err := tun.CreateTUN(adapterName, mtu)
	if err != nil {
		pipeActive.Store(false)
		return nil, fmt.Errorf("mobile: open wintun adapter %q: %w", adapterName, err)
	}

	// Bypass the package-level pipeActive guard inside AttachCallback
	// — we already grabbed it above. Build the CallbackPipe by hand
	// so we can hold the slot for the WintunPipe instead.
	pipeActive.Store(false)
	cb, err := AttachCallback(func(packet []byte, family int) {
		_ = family
		// wireguard-go's NativeTun.Write expects a slice of buffer
		// references with an offset; emit one packet at a time.
		if _, werr := dev.Write([][]byte{packet}, 0); werr != nil {
			logger.Debug("mobile: wintun write", "err", werr)
		}
	}, cli, logger)
	if err != nil {
		_ = dev.Close()
		return nil, fmt.Errorf("mobile: attach callback: %w", err)
	}
	// pipeActive was restored to true by AttachCallback. Good.

	wp := &WintunPipe{dev: dev, pipe: cb, logger: logger}
	wp.wg.Add(1)
	go wp.readLoop()
	return wp, nil
}

func (w *WintunPipe) readLoop() {
	defer w.wg.Done()
	const batch = 16
	bufs := make([][]byte, batch)
	for i := range bufs {
		bufs[i] = make([]byte, 1<<16)
	}
	sizes := make([]int, batch)
	for {
		if w.closed.Load() {
			return
		}
		n, err := w.dev.Read(bufs, sizes, 0)
		if err != nil {
			if !w.closed.Load() {
				w.logger.Debug("mobile: wintun read", "err", err)
			}
			return
		}
		for i := 0; i < n; i++ {
			if sizes[i] <= 0 {
				continue
			}
			pkt := make([]byte, sizes[i])
			copy(pkt, bufs[i][:sizes[i]])
			// family is decided inside Ingest from the IP header
			w.pipe.Ingest(pkt, 0)
		}
	}
}

// Close tears down the wintun adapter and stops the netstack.
func (w *WintunPipe) Close() {
	if !w.closed.CompareAndSwap(false, true) {
		return
	}
	_ = w.dev.Close()
	w.wg.Wait()
	if w.pipe != nil {
		w.pipe.Close()
	}
}
