// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package frame

import (
	"encoding/binary"
	"errors"
)

// WindowUpdatePayload conveys an additive credit increment for the
// stream identified by Frame.StreamID. Receiver-side flow control
// emits one of these whenever the application has drained enough of
// the receive buffer to be worth telling the peer about (typically
// half the initial window).
type WindowUpdatePayload struct {
	Increment uint32
}

// Encode serialises a WINDOW_UPDATE payload (4-octet big-endian
// increment).
func (p *WindowUpdatePayload) Encode() []byte {
	out := make([]byte, 4)
	binary.BigEndian.PutUint32(out, p.Increment)
	return out
}

// DecodeWindowUpdate parses a WINDOW_UPDATE payload.
func DecodeWindowUpdate(b []byte) (*WindowUpdatePayload, error) {
	if len(b) < 4 {
		return nil, errors.New("window update: short payload")
	}
	inc := binary.BigEndian.Uint32(b[:4])
	if inc == 0 {
		return nil, errors.New("window update: zero increment")
	}
	return &WindowUpdatePayload{Increment: inc}, nil
}
