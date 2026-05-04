// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package session_test

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/redstone-md/veil/core/internal/crypto"
	"github.com/redstone-md/veil/core/internal/session"
)

// TestSecureChannelRoundTrip drives a full handshake over an in-memory
// pipe and exchanges several encrypted frames in both directions.
func TestSecureChannelRoundTrip(t *testing.T) {
	t.Parallel()

	serverKP, _ := crypto.GenerateKeypair()
	clientKP, _ := crypto.GenerateKeypair()

	cliPipe, srvPipe := net.Pipe()
	t.Cleanup(func() {
		_ = cliPipe.Close()
		_ = srvPipe.Close()
	})

	type result struct {
		ch  *session.SecureChannel
		err error
	}
	cliCh := make(chan result, 1)
	srvCh := make(chan result, 1)

	go func() {
		est, err := session.HandshakeAsInitiator(pipeConn{cliPipe}, *clientKP, serverKP.Public)
		if err != nil {
			cliCh <- result{err: err}
			return
		}
		cliCh <- result{ch: session.NewSecureChannel(pipeConn{cliPipe}, est)}
	}()
	go func() {
		est, err := session.HandshakeAsResponder(pipeConn{srvPipe}, *serverKP)
		if err != nil {
			srvCh <- result{err: err}
			return
		}
		srvCh <- result{ch: session.NewSecureChannel(pipeConn{srvPipe}, est)}
	}()

	deadline := time.After(5 * time.Second)
	var cli, srv result
	for i := 0; i < 2; i++ {
		select {
		case r := <-cliCh:
			cli = r
		case r := <-srvCh:
			srv = r
		case <-deadline:
			t.Fatal("handshake timed out")
		}
	}
	if cli.err != nil || srv.err != nil {
		t.Fatalf("handshake errors: cli=%v srv=%v", cli.err, srv.err)
	}

	// Several frames in alternating directions. We use net.Pipe
	// which is fully synchronous, so each Send must run on its own
	// goroutine while the matching Recv consumes on the other end.
	cases := [][]byte{
		[]byte("a"),
		[]byte("hello secure channel"),
		bytes.Repeat([]byte{0xAA}, 1024),
		bytes.Repeat([]byte{0x55}, 4096),
	}
	for _, payload := range cases {
		payload := payload
		errCh := make(chan error, 1)
		go func() { errCh <- cli.ch.SendFrame(payload) }()
		got, err := srv.ch.RecvFrame()
		if err != nil {
			t.Fatalf("server recv: %v", err)
		}
		if err := <-errCh; err != nil {
			t.Fatalf("client send: %v", err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("c->s payload mismatch (len=%d)", len(payload))
		}
	}
	for _, payload := range cases {
		payload := payload
		errCh := make(chan error, 1)
		go func() { errCh <- srv.ch.SendFrame(payload) }()
		got, err := cli.ch.RecvFrame()
		if err != nil {
			t.Fatalf("client recv: %v", err)
		}
		if err := <-errCh; err != nil {
			t.Fatalf("server send: %v", err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("s->c payload mismatch (len=%d)", len(payload))
		}
	}
}
