// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

// Package masquetr is the (currently stub) home of the HTTP/3
// MASQUE transport adapter for VWP/1.
//
// MASQUE wraps Veil's traffic in CONNECT-UDP capsules carried over
// HTTP/3 (RFC 9298 + RFC 9297), making the wire shape
// indistinguishable from the kind of low-latency proxy traffic that
// modern browsers and WebRTC stacks already emit through CDN
// frontends. It is the highest-stealth transport in the Veil
// family, especially in combination with the edge backend story
// (see ADR-0004).
//
// This package currently exists to:
//
//   - reserve the import path for cli/serve and cli/connect wiring,
//   - surface a clear "not implemented" error if an operator
//     configures `type: masque`,
//   - hold the documented design that the upcoming functional
//     implementation will follow.
//
// See docs/architecture/ADR-0003-masque-transport.md for the
// rationale and implementation plan.
package masquetr

import "errors"

// ErrNotImplemented is returned by every public symbol in this
// package until the functional implementation lands.
var ErrNotImplemented = errors.New(
	"masquetr: transport not yet implemented; see docs/architecture/ADR-0003",
)

// PlannedConfig sketches the configuration shape the functional
// implementation will accept. Documenting it here lets reviewers
// argue with the design before the code is written.
type PlannedConfig struct {
	// Listen is the host:port the HTTP/3 endpoint binds (UDP).
	Listen string

	// Path is the URL path that accepts CONNECT-UDP requests.
	// Defaults to "/masque" and SHOULD be randomised per
	// deployment to defeat untargeted scanners.
	Path string

	// CertFile / KeyFile supply the TLS certificate. When omitted
	// and a server-level ACME config is present, the manager
	// provisions a Let's Encrypt cert for Domain instead.
	CertFile string
	KeyFile  string

	// Domain is the public host name; required when leaning on
	// the server-level ACME block.
	Domain string

	// MaxStreamsPerSession caps per-connection multiplexing to
	// keep memory bounded against long-running clients. Defaults
	// to 256 when zero.
	MaxStreamsPerSession int
}
