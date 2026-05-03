// Package session orchestrates a Noise XK handshake over a transport
// connection, returning the established AEAD CipherStates that the
// caller uses to send and receive VWP/1 frames.
//
// At v0 the handshake is performed with length-prefixed messages on
// a single bidirectional stream. The framing is internal to the
// handshake itself and is replaced by the VWP/1 frame format once
// the session is established.
package session

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/flynn/noise"

	"github.com/redstone-md/veil/core/internal/crypto"
	"github.com/redstone-md/veil/core/internal/transport"
)

// Established is the result of a completed Noise XK handshake.
type Established struct {
	// Send is the CipherState used to encrypt outgoing application
	// frames.
	Send *noise.CipherState
	// Recv is the CipherState used to decrypt incoming application
	// frames.
	Recv *noise.CipherState
	// PeerStatic is the long-term public key of the authenticated
	// peer. For an initiator this equals the pinned RemoteStatic
	// it provided. For a responder this is the freshly-learned
	// initiator key.
	PeerStatic []byte
}

// HandshakeAsInitiator performs the Noise XK initiator role over
// conn and returns the established CipherStates.
func HandshakeAsInitiator(conn transport.Conn, local crypto.Keypair, remoteStatic []byte) (*Established, error) {
	hs, err := crypto.NewHandshake(crypto.HandshakeConfig{
		Role:         crypto.RoleInitiator,
		LocalStatic:  local,
		RemoteStatic: remoteStatic,
	})
	if err != nil {
		return nil, err
	}
	return runInitiator(conn, hs, remoteStatic)
}

// HandshakeAsResponder performs the Noise XK responder role over
// conn and returns the established CipherStates plus the learned
// peer static key.
func HandshakeAsResponder(conn transport.Conn, local crypto.Keypair) (*Established, error) {
	hs, err := crypto.NewHandshake(crypto.HandshakeConfig{
		Role:        crypto.RoleResponder,
		LocalStatic: local,
	})
	if err != nil {
		return nil, err
	}
	return runResponder(conn, hs)
}

func runInitiator(conn transport.Conn, hs *noise.HandshakeState, remoteStatic []byte) (*Established, error) {
	// XK pattern: -> e
	msg, _, _, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("xk msg1 write: %w", err)
	}
	if err := writeFramed(conn, msg); err != nil {
		return nil, err
	}

	// <- e, ee, s, es
	in, err := readFramed(conn)
	if err != nil {
		return nil, err
	}
	if _, _, _, err := hs.ReadMessage(nil, in); err != nil {
		return nil, fmt.Errorf("xk msg2 read: %w", err)
	}

	// -> s, se
	msg, sendCS, recvCS, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("xk msg3 write: %w", err)
	}
	if err := writeFramed(conn, msg); err != nil {
		return nil, err
	}
	if sendCS == nil || recvCS == nil {
		return nil, errors.New("handshake produced no cipher states")
	}
	return &Established{
		Send:       sendCS,
		Recv:       recvCS,
		PeerStatic: remoteStatic,
	}, nil
}

func runResponder(conn transport.Conn, hs *noise.HandshakeState) (*Established, error) {
	// -> e
	in, err := readFramed(conn)
	if err != nil {
		return nil, err
	}
	if _, _, _, err := hs.ReadMessage(nil, in); err != nil {
		return nil, fmt.Errorf("xk msg1 read: %w", err)
	}

	// <- e, ee, s, es
	msg, _, _, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("xk msg2 write: %w", err)
	}
	if err := writeFramed(conn, msg); err != nil {
		return nil, err
	}

	// -> s, se
	in, err = readFramed(conn)
	if err != nil {
		return nil, err
	}
	_, recvCS, sendCS, err := hs.ReadMessage(nil, in)
	if err != nil {
		return nil, fmt.Errorf("xk msg3 read: %w", err)
	}
	if sendCS == nil || recvCS == nil {
		return nil, errors.New("handshake produced no cipher states")
	}
	return &Established{
		Send:       sendCS,
		Recv:       recvCS,
		PeerStatic: hs.PeerStatic(),
	}, nil
}

// maxHandshakeMessage caps the size of any single Noise XK handshake
// frame to a generous value that comfortably exceeds the largest
// expected (~96-byte) handshake message but rejects garbage early.
const maxHandshakeMessage = 4096

func writeFramed(w io.Writer, b []byte) error {
	if len(b) > maxHandshakeMessage {
		return fmt.Errorf("handshake message too large: %d", len(b))
	}
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(b)))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("write handshake header: %w", err)
	}
	if _, err := w.Write(b); err != nil {
		return fmt.Errorf("write handshake body: %w", err)
	}
	return nil
}

func readFramed(r io.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, fmt.Errorf("read handshake header: %w", err)
	}
	n := binary.BigEndian.Uint16(hdr[:])
	if n > maxHandshakeMessage {
		return nil, fmt.Errorf("handshake message too large: %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("read handshake body: %w", err)
	}
	return buf, nil
}
