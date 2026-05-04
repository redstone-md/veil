// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

// Package realitytr implements the TLS-Reality transport adapter
// for VWP/1.
//
// Reality lets a Veil server impersonate a chosen "decoy" origin
// (e.g. www.microsoft.com) to any TLS prober that does not hold the
// Veil authentication secret. Probes are transparently spliced to
// the real origin so they observe its actual response, including
// its real certificate chain. Authenticated Veil clients short-
// circuit the splice and complete a TLS handshake with the Reality
// server's forged certificate, then run a Noise XK + VWP/1 session
// inside the TLS connection.
//
// See docs/architecture/ADR-0002-reality-transport.md for the
// rationale, and docs/PROTOCOL.md §9.2 for the wire-level role
// Reality plays in VWP/1.
//
// Files in this package:
//
//	doc.go      — this overview
//	hello.go    — minimal TLS ClientHello byte parser
//	auth.go     — PSK derivation, auth tag construction and
//	              verification, replay window
//	listener.go — server-side TCP listener with route decision and
//	              transparent splice on auth miss
//	dialer.go   — client-side dialer: uTLS Hello with embedded
//	              auth tag, completed TLS handshake
package realitytr
