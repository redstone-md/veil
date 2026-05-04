// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

// Package masquetr implements the HTTP/3 MASQUE transport adapter
// for VWP/1 (RFC 9298 + RFC 9297).
//
// The wire shape is the same kind of CONNECT-UDP traffic that browsers
// and WebRTC stacks emit through low-latency proxy frontends. In the
// Veil deployment this is the highest-stealth transport, especially
// when paired with the edge backend (see ADR-0004).
//
// Architecture (nested QUIC, see ADR-0003):
//
//	outer QUIC ── HTTP/3 ── CONNECT-UDP ── proxied UDP ── inner QUIC
//	                                                       └── stream ── VWP/1
//
// Server side: an operator declares two transports in tandem — a
// `masque` listener on the public port plus a `quic` listener on a
// loopback address. The MASQUE listener accepts every CONNECT-UDP
// flow and forwards every datagram to the loopback target, where the
// inner QUIC listener picks them up and runs the standard Veil
// Noise XK + VWP/1 stack. The MASQUE listener never surfaces
// connections through Accept directly; see the comment on Listener.
//
// Client side: Dialer composes the four layers in one call (outer
// QUIC, HTTP/3, CONNECT-UDP, inner QUIC) and returns the first
// inner stream as a transport.Conn.
package masquetr
