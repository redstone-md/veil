// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

// Package mobile bridges an OS-supplied packet tunnel to the Veil
// session's SOCKS5 listener. Two shapes are exposed because the two
// mobile platforms expose tunnels very differently:
//
//   - FDPipe — Android. The OS hands us a TUN file descriptor. We
//     drive xjasonlyu/tun2socks/v2/engine against the fd, which
//     converts ingressed IP packets into TCP/UDP connections it
//     dials through the per-session SOCKS5 listener.
//   - CallbackPipe — iOS. NEPacketTunnelProvider exposes
//     packetFlow.readPackets / writePackets callbacks rather than a
//     fd. tun2socks's engine cannot consume that shape directly;
//     wiring this case up needs a custom gVisor LinkEndpoint and is
//     deferred to a follow-up commit. For now CallbackPipe is a
//     bounded queue that drops packets — the SOCKS5 listener is
//     still reachable from inside the tunnel for apps that opt in
//     explicitly.
//
// Engine constraints: xjasonlyu/tun2socks's engine package keeps
// state in a process-wide singleton. We mirror that with a package-
// level mutex so two FDPipes can't fight over it. The mobile clients
// only ever run one tunnel per process, so this is not a usability
// limitation.

package mobile

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/xjasonlyu/tun2socks/v2/engine"

	"github.com/redstone-md/veil/core/internal/client"
)

// engineActive guards the tun2socks engine singleton. Only one
// FDPipe at a time may hold it.
var engineActive atomic.Bool

// FDPipe owns a TUN file descriptor and drives the tun2socks engine
// between it and the Veil session's SOCKS5 listener.
type FDPipe struct {
	fd     int
	cli    *client.Client
	logger *slog.Logger

	closed atomic.Bool
}

// AttachFD takes ownership of fd and starts the tun2socks engine.
// The fd is closed when the pipe is closed.
//
// The pipe dials the SOCKS5 listener at cli.SOCKSAddr() for every
// new TCP / UDP flow tun2socks lifts off the TUN. Returns an error
// if another FDPipe is already running in this process.
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
	if !engineActive.CompareAndSwap(false, true) {
		return nil, errors.New("mobile: tun2socks engine already running in this process")
	}

	socks := cli.SOCKSAddr()
	logger.Info("mobile: starting tun2socks engine",
		"tun_fd", fd, "socks5", socks)

	engine.Insert(&engine.Key{
		Device:   fmt.Sprintf("fd://%d", fd),
		Proxy:    "socks5://" + socks,
		LogLevel: "warn",
		MTU:      1500,
	})
	// engine.Start swallows errors via log.Fatalf; the Key fields
	// above are well-formed (constant scheme strings, integer fd,
	// known LogLevel) so the failure modes we'd hit here are
	// runtime-only (e.g. EBADF on the supplied fd) — those surface
	// as the engine logging through log.Fatalf, which terminates
	// the process. That matches Android's expectation that the
	// VpnService is killed if the TUN is unusable.
	engine.Start()

	return &FDPipe{fd: fd, cli: cli, logger: logger}, nil
}

// Close stops the tun2socks engine and releases the TUN fd. Safe to
// call more than once.
func (p *FDPipe) Close() {
	if !p.closed.CompareAndSwap(false, true) {
		return
	}
	p.logger.Info("mobile: stopping tun2socks engine")
	engine.Stop()
	engineActive.Store(false)
	// engine.Stop closes the device which already owns the fd; we
	// must not double-close it here.
}

// CallbackPipe carries the iOS-side callback shape: an Ingest method
// the host calls per inbound packet, plus an emit-callback the pipe
// invokes per outbound packet.
//
// At Phase 4.6 v0 the callback model is a bounded queue with no
// netstack underneath; see the package comment for the deferred
// gVisor LinkEndpoint integration.
type CallbackPipe struct {
	emit   func([]byte, int)
	cli    *client.Client
	logger *slog.Logger

	closed atomic.Bool

	mu      sync.Mutex
	pending [][]byte
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
	logger.Warn("mobile: CallbackPipe is a queue-only stub; iOS NetworkExtension end-to-end forwarding is pending a custom gVisor LinkEndpoint")
	return &CallbackPipe{emit: emit, cli: cli, logger: logger}, nil
}

// Ingest pushes one IP packet from the OS into the pipe. family is 4
// for AF_INET, 6 for AF_INET6. Packets are queued for processing by
// the (still-pending) gVisor LinkEndpoint integration; until that
// lands they are dropped after a small bound to avoid unbounded
// memory growth.
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
	_ = family // forwarded once the LinkEndpoint lands
}

// Close stops the pipe.
func (p *CallbackPipe) Close() {
	if !p.closed.CompareAndSwap(false, true) {
		return
	}
	p.mu.Lock()
	p.pending = nil
	p.mu.Unlock()
}
