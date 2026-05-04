// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

// Package mobile bridges an OS-supplied packet tunnel to the Veil
// session's SOCKS5 listener. Two shapes are exposed because the two
// mobile platforms expose tunnels very differently:
//
//   - FDPipe — Android. The OS hands us a TUN file descriptor. We
//     own it for the lifetime of the pipe.
//   - CallbackPipe — iOS. NEPacketTunnelProvider exposes
//     packetFlow.readPackets / writePackets callbacks rather than a
//     fd. The Swift host pushes ingressed IP packets into the pipe;
//     the pipe pushes egress packets back through a callback.
//
// At Phase 4.6 the two types are deliberately thin queues over a
// stub forwarder rather than a full tun2socks engine. That keeps the
// API surface stable while the deep gVisor-based pipe is wired up
// in a follow-up commit (see TODO_TUN2SOCKS in this file). Without
// the pipe present:
//
//   - Outbound packets the OS writes into the TUN are silently
//     dropped (logged at debug).
//   - Inbound packets are never produced.
//
// In other words: the SOCKS5 listener works (apps that opt in to a
// SOCKS proxy still tunnel) but full system traffic interception is
// gated on the tun2socks integration.

package mobile

import (
	"errors"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"

	"github.com/redstone-md/veil/core/internal/client"
)

// TODO_TUN2SOCKS:
//   Replace the stub forwarder below with a real tun2socks pipe.
//   Two viable choices:
//
//     a) gvisor.dev/gvisor/pkg/tcpip + a SOCKS5 dialer. Most
//        flexible; ~3kLOC of integration work.
//     b) github.com/xjasonlyu/tun2socks/v2/engine. Off-the-shelf;
//        large dep tree. Phase 4.6 is willing to pay this cost
//        once we settle on the dial-side hook into client.Client.
//
//   Whichever choice lands, the API surface in this file does not
//   need to change — the FDPipe and CallbackPipe types already
//   model the two ingestion shapes and own the tear-down flow.

// FDPipe owns a TUN file descriptor and shuttles packets between it
// and the Veil session's SOCKS5 layer.
type FDPipe struct {
	fd     *os.File
	cli    *client.Client
	logger *slog.Logger

	closed atomic.Bool
	wg     sync.WaitGroup
}

// AttachFD takes ownership of fd and starts the forwarder. The fd
// must outlive the returned pipe; calling Close releases the fd.
func AttachFD(fd int, cli *client.Client, logger *slog.Logger) (*FDPipe, error) {
	if fd < 0 {
		return nil, errors.New("mobile: invalid tun fd")
	}
	if cli == nil {
		return nil, errors.New("mobile: nil client")
	}
	if logger == nil {
		logger = slog.Default()
	}
	// os.NewFile does not duplicate; closing the *os.File closes
	// the underlying fd, matching the lifetime contract above.
	f := os.NewFile(uintptr(fd), "veil-tun")
	if f == nil {
		return nil, errors.New("mobile: os.NewFile rejected tun fd")
	}
	p := &FDPipe{fd: f, cli: cli, logger: logger}
	p.wg.Add(1)
	go p.run()
	return p, nil
}

func (p *FDPipe) run() {
	defer p.wg.Done()
	buf := make([]byte, 2048)
	for {
		if p.closed.Load() {
			return
		}
		n, err := p.fd.Read(buf)
		if err != nil {
			if !p.closed.Load() {
				p.logger.Debug("mobile: tun read ended", "err", err)
			}
			return
		}
		if n == 0 {
			continue
		}
		// TODO_TUN2SOCKS: hand buf[:n] to the tun2socks engine
		// instead of dropping. See package comment.
		_ = buf[:n]
	}
}

// Close stops the forwarder and releases the TUN fd.
func (p *FDPipe) Close() {
	if !p.closed.CompareAndSwap(false, true) {
		return
	}
	_ = p.fd.Close()
	p.wg.Wait()
}

// CallbackPipe carries the iOS-side callback shape: an Ingest method
// the host calls per inbound packet, plus an emit-callback the pipe
// invokes per outbound packet.
type CallbackPipe struct {
	emit   func([]byte, int)
	cli    *client.Client
	logger *slog.Logger

	closed atomic.Bool

	mu       sync.Mutex
	pending  [][]byte // queued for processing once tun2socks lands
}

// AttachCallback prepares a callback-driven pipe. The supplied emit
// function may be nil; if so the pipe drops outbound packets.
func AttachCallback(emit func([]byte, int), cli *client.Client, logger *slog.Logger) (*CallbackPipe, error) {
	if cli == nil {
		return nil, errors.New("mobile: nil client")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &CallbackPipe{emit: emit, cli: cli, logger: logger}, nil
}

// Ingest pushes one IP packet from the OS into the pipe. family is 4
// for AF_INET, 6 for AF_INET6. Packets are queued for processing by
// the (still-pending) tun2socks engine; until that lands they are
// dropped after a small bound to avoid unbounded memory growth.
func (p *CallbackPipe) Ingest(packet []byte, family int) {
	if p.closed.Load() {
		return
	}
	const queueBound = 256
	p.mu.Lock()
	if len(p.pending) >= queueBound {
		p.pending = p.pending[1:] // drop oldest
	}
	p.pending = append(p.pending, packet)
	p.mu.Unlock()
	_ = family // forwarded once tun2socks lands
}

// Close drains the queue and stops emitting outbound packets.
func (p *CallbackPipe) Close() {
	if !p.closed.CompareAndSwap(false, true) {
		return
	}
	p.mu.Lock()
	p.pending = nil
	p.mu.Unlock()
}
