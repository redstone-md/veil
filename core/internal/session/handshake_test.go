// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package session_test

import (
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/redstone-md/veil/core/internal/crypto"
	"github.com/redstone-md/veil/core/internal/session"
)

// pipeConn adapts a net.Conn into transport.Conn for the test.
type pipeConn struct{ net.Conn }

func (p pipeConn) LocalAddr() net.Addr  { return p.Conn.LocalAddr() }
func (p pipeConn) RemoteAddr() net.Addr { return p.Conn.RemoteAddr() }

// TestNoiseXKHandshakeOverPipe verifies that the Noise XK orchestration
// in the session package completes successfully when run between two
// halves of an in-memory pipe and that subsequent AEAD round-trips
// decrypt correctly.
func TestNoiseXKHandshakeOverPipe(t *testing.T) {
	t.Parallel()

	serverKP, err := crypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("server keypair: %v", err)
	}
	clientKP, err := crypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("client keypair: %v", err)
	}

	clientSide, serverSide := net.Pipe()
	t.Cleanup(func() {
		_ = clientSide.Close()
		_ = serverSide.Close()
	})

	type result struct {
		est *session.Established
		err error
	}
	clientCh := make(chan result, 1)
	serverCh := make(chan result, 1)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		est, err := session.HandshakeAsInitiator(pipeConn{clientSide}, *clientKP, serverKP.Public)
		clientCh <- result{est, err}
	}()
	go func() {
		defer wg.Done()
		est, err := session.HandshakeAsResponder(pipeConn{serverSide}, *serverKP)
		serverCh <- result{est, err}
	}()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handshake timed out")
	}

	cli := <-clientCh
	srv := <-serverCh
	if cli.err != nil {
		t.Fatalf("client handshake: %v", cli.err)
	}
	if srv.err != nil {
		t.Fatalf("server handshake: %v", srv.err)
	}

	// The responder must learn the initiator's static public key.
	if !bytesEqual(srv.est.PeerStatic, clientKP.Public) {
		t.Fatalf("responder learned wrong peer key:\nwant %x\ngot  %x",
			clientKP.Public, srv.est.PeerStatic)
	}
	// The initiator already had the responder's static key pinned.
	if !bytesEqual(cli.est.PeerStatic, serverKP.Public) {
		t.Fatalf("initiator records wrong peer key:\nwant %x\ngot  %x",
			serverKP.Public, cli.est.PeerStatic)
	}

	// Round-trip encrypted message client -> server.
	plain := []byte("hello veil")
	ciphertext, err := cli.est.Send.Encrypt(nil, nil, plain)
	if err != nil {
		t.Fatalf("client encrypt: %v", err)
	}
	got, err := srv.est.Recv.Decrypt(nil, nil, ciphertext)
	if err != nil {
		t.Fatalf("server decrypt: %v", err)
	}
	if string(got) != string(plain) {
		t.Fatalf("round-trip mismatch: want %q, got %q", plain, got)
	}

	// Round-trip the other direction too.
	reply := []byte("hello back")
	ciphertext, err = srv.est.Send.Encrypt(nil, nil, reply)
	if err != nil {
		t.Fatalf("server encrypt: %v", err)
	}
	got, err = cli.est.Recv.Decrypt(nil, nil, ciphertext)
	if err != nil {
		t.Fatalf("client decrypt: %v", err)
	}
	if string(got) != string(reply) {
		t.Fatalf("reverse round-trip mismatch: want %q, got %q", reply, got)
	}
}

// TestNoiseXKWrongServerKey confirms that an initiator handshake
// fails when the pinned remote-static key does not match the
// responder's actual key.
//
// In Noise XK with the wrong responder static, the responder's
// own ReadMessage of the first message rejects the initiator's
// MAC; the responder then closes its pipe end, the initiator's
// next read returns io.EOF, and HandshakeAsInitiator surfaces an
// error. We accept either a noise-level error or an EOF as a
// successful failure.
func TestNoiseXKWrongServerKey(t *testing.T) {
	t.Parallel()

	serverKP, _ := crypto.GenerateKeypair()
	clientKP, _ := crypto.GenerateKeypair()
	wrongPub, _ := crypto.GenerateKeypair()

	clientSide, serverSide := net.Pipe()
	t.Cleanup(func() {
		_ = clientSide.Close()
		_ = serverSide.Close()
	})

	clientErr := make(chan error, 1)
	go func() {
		_, err := session.HandshakeAsInitiator(pipeConn{clientSide}, *clientKP, wrongPub.Public)
		clientErr <- err
	}()
	go func() {
		_, err := session.HandshakeAsResponder(pipeConn{serverSide}, *serverKP)
		if err == nil {
			t.Errorf("responder accepted handshake with mismatched initiator es")
		}
		// Closing our side is what surfaces the failure to the
		// initiator (which would otherwise block reading msg2).
		_ = serverSide.Close()
	}()

	select {
	case err := <-clientErr:
		if err == nil {
			t.Fatal("expected initiator handshake to fail with wrong server key")
		}
		if !errors.Is(err, io.EOF) && err.Error() == "" {
			t.Fatalf("expected meaningful error, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handshake timed out (initiator did not detect wrong key)")
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
