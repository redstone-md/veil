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
	"sync"

	"github.com/flynn/noise"

	"github.com/redstone-md/veil/core/internal/transport"
)

// MaxCiphertextSize bounds the size of a single AEAD record on the
// wire. The 32 KiB ceiling comfortably accommodates a maximum-sized
// VWP/1 frame (header + max payload + max padding + AEAD tag).
const MaxCiphertextSize = 32 * 1024

// SecureChannel is a reliable, in-order, AEAD-protected message
// channel built on top of a transport.Conn and a pair of established
// Noise CipherStates.
//
// Each SendFrame call produces one length-prefixed AEAD record on
// the wire; each RecvFrame call consumes one. Both directions are
// independently serialised by their own mutex; full-duplex use is
// supported.
type SecureChannel struct {
	conn transport.Conn

	sendMu sync.Mutex
	send   *noise.CipherState

	recvMu sync.Mutex
	recv   *noise.CipherState
}

// NewSecureChannel pairs a transport with the CipherStates produced
// by a completed Noise handshake.
func NewSecureChannel(conn transport.Conn, est *Established) *SecureChannel {
	return &SecureChannel{
		conn: conn,
		send: est.Send,
		recv: est.Recv,
	}
}

// SendFrame encrypts plaintext as a single AEAD record and writes
// it to the underlying transport with a 4-octet big-endian length
// prefix.
//
// SendFrame is safe to call concurrently with RecvFrame but not
// concurrently with itself.
func (c *SecureChannel) SendFrame(plaintext []byte) error {
	if len(plaintext) > MaxCiphertextSize-16 {
		return fmt.Errorf("secure: plaintext too large: %d", len(plaintext))
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	cipher, err := c.send.Encrypt(nil, nil, plaintext)
	if err != nil {
		return fmt.Errorf("secure: encrypt: %w", err)
	}
	if len(cipher) > MaxCiphertextSize {
		return fmt.Errorf("secure: ciphertext too large: %d", len(cipher))
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(cipher)))
	if _, err := c.conn.Write(hdr[:]); err != nil {
		return fmt.Errorf("secure: write header: %w", err)
	}
	if _, err := c.conn.Write(cipher); err != nil {
		return fmt.Errorf("secure: write body: %w", err)
	}
	return nil
}

// RecvFrame reads one AEAD record from the underlying transport and
// returns the decrypted plaintext. It returns io.EOF when the peer
// has cleanly closed its side and no further data is available.
//
// RecvFrame is safe to call concurrently with SendFrame but not
// concurrently with itself.
func (c *SecureChannel) RecvFrame() ([]byte, error) {
	c.recvMu.Lock()
	defer c.recvMu.Unlock()

	var hdr [4]byte
	if _, err := io.ReadFull(c.conn, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 {
		return nil, errors.New("secure: zero-length record")
	}
	if n > MaxCiphertextSize {
		return nil, fmt.Errorf("secure: ciphertext too large: %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(c.conn, buf); err != nil {
		return nil, fmt.Errorf("secure: read body: %w", err)
	}
	plain, err := c.recv.Decrypt(nil, nil, buf)
	if err != nil {
		return nil, fmt.Errorf("secure: decrypt: %w", err)
	}
	return plain, nil
}

// Close closes the underlying transport.
func (c *SecureChannel) Close() error { return c.conn.Close() }
