// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package session_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/redstone-md/veil/core/internal/crypto"
	"github.com/redstone-md/veil/core/internal/frame"
	"github.com/redstone-md/veil/core/internal/session"
)

func newPairedSessions(t *testing.T) (cli, srv *session.Session, cleanup func()) {
	t.Helper()
	serverKP, _ := crypto.GenerateKeypair()
	clientKP, _ := crypto.GenerateKeypair()

	// Use real loopback TCP so concurrent send/recv on a single
	// session don't deadlock the way net.Pipe does.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srvAccept := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		srvAccept <- c
	}()
	cliConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	srvConn := <-srvAccept
	_ = ln.Close()

	type result struct {
		est *session.Established
		err error
	}
	cliCh := make(chan result, 1)
	srvCh := make(chan result, 1)
	go func() {
		est, err := session.HandshakeAsInitiator(pipeConn{cliConn}, *clientKP, serverKP.Public)
		cliCh <- result{est, err}
	}()
	go func() {
		est, err := session.HandshakeAsResponder(pipeConn{srvConn}, *serverKP)
		srvCh <- result{est, err}
	}()
	deadline := time.After(5 * time.Second)
	var c, s result
	for i := 0; i < 2; i++ {
		select {
		case r := <-cliCh:
			c = r
		case r := <-srvCh:
			s = r
		case <-deadline:
			t.Fatal("handshake timed out")
		}
	}
	if c.err != nil {
		t.Fatalf("client handshake: %v", c.err)
	}
	if s.err != nil {
		t.Fatalf("server handshake: %v", s.err)
	}

	cli = session.New(session.NewSecureChannel(pipeConn{cliConn}, c.est),
		session.Options{Role: session.RoleClient})
	srv = session.New(session.NewSecureChannel(pipeConn{srvConn}, s.est),
		session.Options{Role: session.RoleServer})

	cleanup = func() {
		_ = cli.Close()
		_ = srv.Close()
	}
	return cli, srv, cleanup
}

func TestSessionStreamRoundTrip(t *testing.T) {
	t.Parallel()
	cli, srv, cleanup := newPairedSessions(t)
	defer cleanup()

	go func() { _ = srv.Run() }()
	go func() { _ = cli.Run() }()

	target := frame.Address{Host: "example.com", Port: 80}

	// Server-side echo handler.
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		st, err := srv.AcceptStream(context.Background())
		if err != nil {
			t.Errorf("server accept: %v", err)
			return
		}
		if st.Target().Host != "example.com" || st.Target().Port != 80 {
			t.Errorf("server got unexpected target: %v", st.Target())
		}
		buf := make([]byte, 64)
		total := []byte{}
		for {
			n, err := st.Read(buf)
			total = append(total, buf[:n]...)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					t.Errorf("server read: %v", err)
				}
				break
			}
		}
		if _, err := st.Write(total); err != nil {
			t.Errorf("server write: %v", err)
		}
		if err := st.Close(); err != nil {
			t.Errorf("server close: %v", err)
		}
	}()

	st, err := cli.OpenStream(context.Background(), target)
	if err != nil {
		t.Fatalf("client open: %v", err)
	}
	payload := bytes.Repeat([]byte("Veil"), 1000)
	if _, err := st.Write(payload); err != nil {
		t.Fatalf("client write: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("client close: %v", err)
	}

	got, err := io.ReadAll(st)
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("echo mismatch: got %dB expected %dB", len(got), len(payload))
	}

	select {
	case <-serverDone:
	case <-time.After(5 * time.Second):
		t.Fatal("server handler did not finish")
	}
}

func TestSessionMultipleConcurrentStreams(t *testing.T) {
	t.Parallel()
	cli, srv, cleanup := newPairedSessions(t)
	defer cleanup()

	go func() { _ = srv.Run() }()
	go func() { _ = cli.Run() }()

	const n = 20

	srvWG := sync.WaitGroup{}
	srvWG.Add(n)
	go func() {
		for i := 0; i < n; i++ {
			st, err := srv.AcceptStream(context.Background())
			if err != nil {
				t.Errorf("accept %d: %v", i, err)
				return
			}
			go func(s *session.Stream) {
				defer srvWG.Done()
				data, _ := io.ReadAll(s)
				_, _ = s.Write(data)
				_ = s.Close()
			}(st)
		}
	}()

	cliWG := sync.WaitGroup{}
	cliWG.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer cliWG.Done()
			st, err := cli.OpenStream(context.Background(),
				frame.Address{Host: "example.com", Port: uint16(80 + i)})
			if err != nil {
				t.Errorf("open %d: %v", i, err)
				return
			}
			payload := bytes.Repeat([]byte{byte(i)}, 500+i)
			if _, err := st.Write(payload); err != nil {
				t.Errorf("write %d: %v", i, err)
				return
			}
			_ = st.Close()
			got, err := io.ReadAll(st)
			if err != nil {
				t.Errorf("read %d: %v", i, err)
				return
			}
			if !bytes.Equal(got, payload) {
				t.Errorf("mismatch on stream %d: got %dB expected %dB", i, len(got), len(payload))
			}
		}(i)
	}

	done := make(chan struct{})
	go func() {
		cliWG.Wait()
		srvWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("concurrent streams did not finish")
	}
}
