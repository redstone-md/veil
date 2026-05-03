// Package crypto wraps the Noise Protocol Framework with the
// parameters chosen by VWP/1: Noise_XK_25519_ChaChaPoly_BLAKE2s.
//
// This package owns key generation, on-disk key persistence, and
// the handshake state machine. It exposes a small surface that the
// transport adapters use to authenticate and establish AEAD sessions.
package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/flynn/noise"
)

// Prologue is the value mixed into every Noise handshake to bind it
// to this protocol version. The format is "VWP/1" || protocol_id.
//
// See docs/PROTOCOL.md §3.2.
var Prologue = []byte{'V', 'W', 'P', '/', '1', 0x01}

// CipherSuite is the fixed Noise cipher suite for VWP/1.
var CipherSuite = noise.NewCipherSuite(
	noise.DH25519,
	noise.CipherChaChaPoly,
	noise.HashBLAKE2s,
)

// Keypair is a Noise X25519 long-term static keypair.
type Keypair struct {
	Private []byte
	Public  []byte
}

// GenerateKeypair returns a fresh X25519 keypair sourced from
// crypto/rand.
func GenerateKeypair() (*Keypair, error) {
	dh := CipherSuite.GenerateKeypair
	kp, err := dh(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate keypair: %w", err)
	}
	return &Keypair{Private: kp.Private, Public: kp.Public}, nil
}

// LoadOrCreateKeypair reads a keypair from path. If the file does
// not exist, a new keypair is generated and written. The file format
// is two base64 lines: private then public.
func LoadOrCreateKeypair(path string) (*Keypair, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		kp, gerr := GenerateKeypair()
		if gerr != nil {
			return nil, gerr
		}
		if werr := writeKeypair(path, kp); werr != nil {
			return nil, werr
		}
		return kp, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read keypair: %w", err)
	}
	return parseKeypair(data)
}

func writeKeypair(path string, kp *Keypair) error {
	body := base64.StdEncoding.EncodeToString(kp.Private) + "\n" +
		base64.StdEncoding.EncodeToString(kp.Public) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return fmt.Errorf("write keypair: %w", err)
	}
	return nil
}

func parseKeypair(data []byte) (*Keypair, error) {
	var priv64, pub64 string
	if _, err := fmt.Sscanf(string(data), "%s\n%s\n", &priv64, &pub64); err != nil {
		return nil, fmt.Errorf("parse keypair: %w", err)
	}
	priv, err := base64.StdEncoding.DecodeString(priv64)
	if err != nil {
		return nil, fmt.Errorf("decode private key: %w", err)
	}
	pub, err := base64.StdEncoding.DecodeString(pub64)
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}
	return &Keypair{Private: priv, Public: pub}, nil
}

// HandshakeRole identifies which side of a Noise XK handshake the
// caller is performing.
type HandshakeRole int

const (
	// RoleInitiator is the client side. It knows the responder's
	// static public key in advance and authenticates itself in the
	// third handshake message.
	RoleInitiator HandshakeRole = iota
	// RoleResponder is the server side. It learns the initiator's
	// static key from the third handshake message.
	RoleResponder
)

// HandshakeConfig parameterises a single Noise XK handshake.
type HandshakeConfig struct {
	Role HandshakeRole

	// LocalStatic is this side's long-term keypair.
	LocalStatic Keypair

	// RemoteStatic is required for the initiator (it is the
	// pinned server identity); MUST be empty for the responder
	// (which learns it during the handshake).
	RemoteStatic []byte
}

// NewHandshake constructs a Noise XK HandshakeState configured per
// VWP/1. The caller drives it via WriteMessage / ReadMessage.
func NewHandshake(cfg HandshakeConfig) (*noise.HandshakeState, error) {
	if cfg.Role == RoleInitiator && len(cfg.RemoteStatic) == 0 {
		return nil, errors.New("initiator requires RemoteStatic")
	}
	if cfg.Role == RoleResponder && len(cfg.RemoteStatic) != 0 {
		return nil, errors.New("responder must not be given RemoteStatic")
	}

	state, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:   CipherSuite,
		Pattern:       noise.HandshakeXK,
		Initiator:     cfg.Role == RoleInitiator,
		Prologue:      Prologue,
		StaticKeypair: noise.DHKey{Private: cfg.LocalStatic.Private, Public: cfg.LocalStatic.Public},
		PeerStatic:    cfg.RemoteStatic,
		Random:        rand.Reader,
	})
	if err != nil {
		return nil, fmt.Errorf("noise handshake init: %w", err)
	}
	return state, nil
}

// EncodePublicKey returns the base64 encoding of a public key suitable
// for inclusion in user-facing configuration.
func EncodePublicKey(pub []byte) string {
	return base64.StdEncoding.EncodeToString(pub)
}

// DecodePublicKey parses a base64-encoded public key.
func DecodePublicKey(s string) ([]byte, error) {
	pub, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}
	return pub, nil
}

// FillRandom fills b with cryptographically secure random bytes.
// Provided here to keep all entropy use in one auditable place.
func FillRandom(b []byte) error {
	_, err := io.ReadFull(rand.Reader, b)
	return err
}
