// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package session

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// MaxDatagramSize bounds one UDP datagram carried over a Datagram
// stream. The 64 KiB ceiling matches the IPv4/IPv6 MTU upper limit.
const MaxDatagramSize = 64 * 1024

// WriteDatagram emits a single UDP datagram on a Datagram stream as
// a length-prefixed (2-byte big-endian) record. Returns the number
// of payload bytes written (excluding the 2-byte header) on success.
//
// Safe to call concurrently across distinct streams; serial within
// a single stream (writes acquire the underlying secure channel
// mutex internally).
func WriteDatagram(w io.Writer, payload []byte) error {
	if len(payload) > MaxDatagramSize {
		return fmt.Errorf("session: datagram %d > max %d", len(payload), MaxDatagramSize)
	}
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(payload)))
	// Coalesce header + payload into one Write so a Stream-level
	// Send fires one secure frame, not two. (Two writes would also
	// fragment across SecureChannel records, breaking framing on
	// the peer side.)
	buf := make([]byte, 2+len(payload))
	copy(buf[:2], hdr[:])
	copy(buf[2:], payload)
	_, err := w.Write(buf)
	return err
}

// ReadDatagram reads one length-prefixed UDP datagram from r into
// the supplied buffer. Returns the number of bytes read on success
// or io.EOF when the peer closed cleanly.
func ReadDatagram(r io.Reader, buf []byte) (int, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, err
	}
	n := int(binary.BigEndian.Uint16(hdr[:]))
	if n == 0 {
		return 0, errors.New("session: zero-length datagram")
	}
	if n > MaxDatagramSize {
		return 0, fmt.Errorf("session: oversized datagram %d", n)
	}
	if n > len(buf) {
		return 0, fmt.Errorf("session: datagram %d > buffer %d", n, len(buf))
	}
	if _, err := io.ReadFull(r, buf[:n]); err != nil {
		return 0, err
	}
	return n, nil
}
