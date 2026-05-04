// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

// Package mobile bridges an OS-supplied packet tunnel to the Veil
// session's SOCKS5 listener. Two shapes are exposed because the two
// mobile platforms expose tunnels very differently:
//
//   - FDPipe — Android. The OS hands us a TUN file descriptor. We
//     drive xjasonlyu/tun2socks/v2/engine against the fd; the
//     engine internally builds a gVisor netstack pinned to that fd
//     and dials each TCP/UDP flow it lifts off the TUN through the
//     per-session SOCKS5 listener.
//
//   - CallbackPipe — iOS. NEPacketTunnelProvider exposes
//     packetFlow.readPackets / writePackets callbacks rather than a
//     fd. We set up a parallel netstack with a custom
//     channel.Endpoint as the LinkEndpoint: Ingest() injects packets
//     read off packetFlow into the netstack, and an outbound
//     goroutine pulls packets the netstack has produced and forwards
//     them to the registered emit callback (which the Swift side
//     plugs into packetFlow.writePackets).
//
// Engine constraints: tun2socks's engine, tunnel and proxy packages
// keep state in a process-wide singleton (`tunnel.T()` is global,
// `engine._defaultStack` is global). FDPipe goes through the engine
// path; CallbackPipe builds its own stack but still has to share the
// global `tunnel.T()` dialer hookup. We mirror that with a package-
// level mutex so two pipes cannot coexist. The mobile clients only
// ever run one tunnel per process, so the constraint is not a
// usability limitation.

package mobile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/xjasonlyu/tun2socks/v2/core"
	"github.com/xjasonlyu/tun2socks/v2/engine"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"github.com/xjasonlyu/tun2socks/v2/tunnel"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/stack"

	"github.com/redstone-md/veil/core/internal/client"
)

// pipeActive guards the per-process tun2socks state. Only one pipe
// (FDPipe or CallbackPipe) may run at a time.
var pipeActive atomic.Bool

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
// if another pipe (FD or callback) is already running in this
// process.
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
	if !pipeActive.CompareAndSwap(false, true) {
		return nil, errors.New("mobile: another tun2socks pipe is already running in this process")
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
	// known LogLevel) so the failure modes that remain are runtime-
	// only (e.g. EBADF on the supplied fd) — those terminate the
	// VpnService process, which matches Android's expectation that
	// an unusable TUN tears the service down.
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
	pipeActive.Store(false)
	// engine.Stop closes the device which already owns the fd; we
	// must not double-close it here.
}

// CallbackPipe drives an iOS-side tun2socks pipe whose underlying
// transport is a pair of Swift callbacks (packetFlow.readPackets ↔
// packetFlow.writePackets) rather than a fd.
//
// Ingest() injects each IP packet read from packetFlow into the
// gVisor netstack; tun2socks's tunnel processor lifts TCP / UDP
// flows off the netstack and dials them through the SOCKS5 listener.
// Each outbound packet the netstack produces is pulled by an
// internal goroutine and pushed to the emit callback the Swift side
// registered via veil_ne_start.
type CallbackPipe struct {
	emit   func([]byte, int)
	cli    *client.Client
	logger *slog.Logger

	endpoint *channel.Endpoint
	stk      *stack.Stack

	ctx    context.Context
	cancel context.CancelFunc

	closed atomic.Bool
	wg     sync.WaitGroup
}

// AttachCallback prepares a callback-driven pipe. The supplied emit
// function may be nil; if so the pipe drops outbound packets but
// still terminates inbound flows through SOCKS5 (handy for tests
// that drive only the ingress side).
func AttachCallback(emit func([]byte, int), cli *client.Client, logger *slog.Logger) (*CallbackPipe, error) {
	if cli == nil {
		return nil, errors.New("mobile: nil client")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if !pipeActive.CompareAndSwap(false, true) {
		return nil, errors.New("mobile: another tun2socks pipe is already running in this process")
	}

	socks := cli.SOCKSAddr()
	logger.Info("mobile: starting callback-driven tun2socks pipe",
		"socks5", socks)

	socksProxy, err := proxy.NewSocks5(socks, "", "")
	if err != nil {
		pipeActive.Store(false)
		return nil, fmt.Errorf("mobile: socks5 proxy: %w", err)
	}
	tunnel.T().SetDialer(socksProxy)
	tunnel.T().ProcessAsync()

	const queueLen = 512
	const mtu = 1500
	endpoint := channel.New(queueLen, mtu, "")

	stk, err := core.CreateStack(&core.Config{
		LinkEndpoint:     endpoint,
		TransportHandler: tunnel.T(),
	})
	if err != nil {
		endpoint.Close()
		pipeActive.Store(false)
		return nil, fmt.Errorf("mobile: create stack: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	p := &CallbackPipe{
		emit:     emit,
		cli:      cli,
		logger:   logger,
		endpoint: endpoint,
		stk:      stk,
		ctx:      ctx,
		cancel:   cancel,
	}
	p.wg.Add(1)
	go p.outboundLoop()
	return p, nil
}

// Ingest pushes one IP packet into the netstack. family is 4 for
// AF_INET, 6 for AF_INET6. Packets that don't decode as a known IP
// version are silently dropped.
func (p *CallbackPipe) Ingest(packet []byte, family int) {
	if p.closed.Load() || len(packet) == 0 {
		return
	}
	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
		Payload: buffer.MakeWithData(packet),
	})
	defer pkt.DecRef()

	// Trust the Swift side's family hint when the packet's first
	// nibble is ambiguous (zero), otherwise pick from the IP header
	// which is authoritative.
	switch header.IPVersion(packet) {
	case header.IPv4Version:
		p.endpoint.InjectInbound(header.IPv4ProtocolNumber, pkt)
	case header.IPv6Version:
		p.endpoint.InjectInbound(header.IPv6ProtocolNumber, pkt)
	default:
		if family == 6 {
			p.endpoint.InjectInbound(header.IPv6ProtocolNumber, pkt)
		} else {
			p.endpoint.InjectInbound(header.IPv4ProtocolNumber, pkt)
		}
	}
}

// outboundLoop drains packets the netstack has produced (responses
// to ingressed flows, RSTs etc.) and pushes them to the emit
// callback. The loop exits when the pipe is closed.
func (p *CallbackPipe) outboundLoop() {
	defer p.wg.Done()
	for {
		pkt := p.endpoint.ReadContext(p.ctx)
		if pkt == nil {
			return // ctx cancelled
		}
		if p.emit != nil {
			buf := pkt.ToBuffer()
			data := buf.Flatten()
			buf.Release()
			family := 4
			if len(data) > 0 && header.IPVersion(data) == header.IPv6Version {
				family = 6
			}
			p.emit(data, family)
		}
		pkt.DecRef()
	}
}

// Close tears the pipe down: cancel the outbound loop, close the
// stack, release the global pipe slot.
func (p *CallbackPipe) Close() {
	if !p.closed.CompareAndSwap(false, true) {
		return
	}
	p.logger.Info("mobile: stopping callback-driven tun2socks pipe")
	p.cancel()
	p.endpoint.Close()
	p.stk.Close()
	p.stk.Wait()
	p.wg.Wait()
	pipeActive.Store(false)
}
