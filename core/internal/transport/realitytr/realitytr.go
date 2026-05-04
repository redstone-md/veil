// Package realitytr is the (currently stub) home of the TLS-Reality
// transport adapter for VWP/1.
//
// Reality lets a server transparently impersonate a chosen "decoy"
// origin (e.g. www.microsoft.com) to any TLS prober that does not
// hold the Veil authentication secret, while clients that DO hold
// it complete a Veil handshake instead.
//
// This package currently exists to:
//
//   - reserve the import path so wiring code in cli/serve and
//     cli/connect can refer to it,
//   - surface a clear "not implemented" error if an operator
//     configures `type: reality` in YAML,
//   - hold the design notes that the upcoming functional
//     implementation will follow.
//
// See docs/architecture/ADR-0002-reality-transport.md for the
// rationale and the implementation plan.
package realitytr

import "errors"

// ErrNotImplemented is returned by every public symbol in this
// package until the functional implementation lands.
var ErrNotImplemented = errors.New("realitytr: transport not yet implemented; see docs/architecture/ADR-0002")

// PlannedConfig sketches the configuration shape the functional
// implementation will accept. Documenting it here lets reviewers
// argue with the design before the code is written.
type PlannedConfig struct {
	// Listen is the host:port the server binds (TCP). Unused on
	// the client side.
	Listen string

	// TargetSNI is the host name whose TLS persona this server
	// impersonates and to which probe traffic is forwarded. Both
	// sides MUST agree on this value.
	TargetSNI string

	// TargetAddr is the host:port the server connects to when
	// proxying probe traffic. Defaults to "<TargetSNI>:443" when
	// empty.
	TargetAddr string

	// ServerStaticPub is the server's long-term X25519 public key
	// used to derive the per-handshake authentication tag. This
	// is the same Noise XK static key the rest of Veil uses; on
	// the wire it is *not* the same bytes — Reality mixes it via
	// HKDF before exposing it.
	ServerStaticPub []byte

	// ShortIDs (server only) is a small set of 8-byte tags that
	// the server accepts in the auth extension. Operators rotate
	// these to revoke individual clients without redeploying.
	ShortIDs [][]byte
}
