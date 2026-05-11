// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package session_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redstone-md/veil/core/internal/crypto"
	"github.com/redstone-md/veil/core/internal/frame"
	"github.com/redstone-md/veil/core/internal/session"
)

// TestFlowControlBackpressure proves that a slow consumer applies
// real back-pressure to the peer: a producer writing N bytes while
// the consumer reads at a fixed slow rate must NOT race ahead by
// more than the configured window.
//
// Pre-flow-control behaviour: producer writes all N bytes
// immediately and the receiver's rxBuf grows to N (unbounded).
// With flow control: producer's outstanding-but-not-yet-acked bytes
// stay ≤ window.
func TestFlowControlBackpressure(t *testing.T) {
	t.Parallel()
	cli, srv, cleanup := newPairedSessions(t)
	defer cleanup()
	go func() { _ = srv.Run() }()
	go func() { _ = cli.Run() }()

	target := frame.Address{Host: "echo", Port: 1}
	const window = 16 * 1024 // small window so back-pressure engages
	const total = 128 * 1024 // 8x window — must wait at least 7 round-trips
	payload := make([]byte, total)
	rand.Read(payload)

	var producerDone time.Time
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		st, err := srv.AcceptStream(context.Background())
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		// Drain in 4 KB chunks with a 5 ms pause per chunk → about
		// 800 KB/s drain rate. Producer would race far ahead without
		// flow control but with it must stay within the window.
		buf := make([]byte, 4096)
		got := bytes.NewBuffer(nil)
		for {
			n, err := st.Read(buf)
			got.Write(buf[:n])
			if err != nil {
				if !errors.Is(err, io.EOF) {
					t.Errorf("server read: %v", err)
				}
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if !bytes.Equal(got.Bytes(), payload) {
			t.Errorf("payload mismatch: got %d bytes want %d", got.Len(), len(payload))
		}
	}()

	st, err := cli.OpenStreamFull(context.Background(), target, frame.StreamTypeReliable, window)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t0 := time.Now()
	if _, err := st.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	producerDone = time.Now()
	_ = st.Close()
	elapsed := producerDone.Sub(t0)

	// Drain rate is ~800 KB/s (4 KB / 5 ms). Total = 128 KB → ~160 ms
	// minimum even with infinite window for the consumer to drain it.
	// Pre-flow-control behaviour: producer races to fill the unbounded
	// rxBuf in ~1 ms. With a 16 KB window the producer must wait for
	// WINDOW_UPDATE every 8 KB, so wallclock tracks the drain rate.
	if elapsed < 80*time.Millisecond {
		t.Fatalf("producer did not back-pressure: finished in %s, expected ≥80ms", elapsed)
	}

	select {
	case <-srvDone:
	case <-time.After(10 * time.Second):
		t.Fatal("server consumer did not finish")
	}
}

// TestFlowControlWindowUpdates verifies that a stream which fills
// the window, drains it, refills repeatedly across many windows
// transfers the full payload without losing or duplicating bytes.
func TestFlowControlWindowUpdates(t *testing.T) {
	t.Parallel()
	cli, srv, cleanup := newPairedSessions(t)
	defer cleanup()
	go func() { _ = srv.Run() }()
	go func() { _ = cli.Run() }()

	target := frame.Address{Host: "loopback", Port: 0}
	const total = 4 * 1024 * 1024 // 4 MiB → spans many windows
	payload := make([]byte, total)
	rand.Read(payload)

	srvDone := make(chan struct{})
	var got atomic.Int64
	go func() {
		defer close(srvDone)
		st, _ := srv.AcceptStream(context.Background())
		hasher := bytes.NewBuffer(nil)
		buf := make([]byte, 32*1024)
		for {
			n, err := st.Read(buf)
			if n > 0 {
				hasher.Write(buf[:n])
				got.Add(int64(n))
			}
			if err != nil {
				break
			}
		}
		if !bytes.Equal(hasher.Bytes(), payload) {
			t.Errorf("payload mismatch")
		}
	}()

	st, _ := cli.OpenStream(context.Background(), target)
	if _, err := st.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = st.Close()

	select {
	case <-srvDone:
	case <-time.After(15 * time.Second):
		t.Fatalf("4 MiB transfer stalled — got %d / %d", got.Load(), total)
	}
}

// TestFlowControlNoHeadOfLineBlock verifies that a slow consumer on
// stream A does not block stream B. Pre-flow-control, the synchronous
// dispatcher would stall B while A's rxBuf was full; with bounded
// per-stream rings + WINDOW_UPDATE the dispatcher only stalls on A.
func TestFlowControlNoHeadOfLineBlock(t *testing.T) {
	t.Parallel()
	cli, srv, cleanup := newPairedSessions(t)
	defer cleanup()
	go func() { _ = srv.Run() }()
	go func() { _ = cli.Run() }()

	// Server: stream A drains slowly, stream B drains fast and
	// reports its completion time.
	bDone := make(chan time.Duration, 1)
	wg := sync.WaitGroup{}
	wg.Add(2)
	go func() {
		defer wg.Done()
		// First stream = slow drainer.
		st, _ := srv.AcceptStream(context.Background())
		buf := make([]byte, 4096)
		for {
			_, err := st.Read(buf)
			if err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	go func() {
		defer wg.Done()
		// Second stream = fast drainer; report wallclock.
		st, _ := srv.AcceptStream(context.Background())
		t0 := time.Now()
		_, _ = io.Copy(io.Discard, st)
		bDone <- time.Since(t0)
	}()

	// Client: open A, start writing slowly so server blocks reading
	// it; then open B and dump 256 KB through fast.
	stA, _ := cli.OpenStream(context.Background(), frame.Address{Host: "slow", Port: 1})
	go func() {
		// Drip-feed A so its rxRing fills and stays full while B
		// runs. Without flow control we'd buffer it all up front.
		for i := 0; i < 256; i++ {
			_, _ = stA.Write(make([]byte, 1024))
			time.Sleep(1 * time.Millisecond)
		}
		_ = stA.Close()
	}()
	time.Sleep(20 * time.Millisecond) // let A start

	stB, _ := cli.OpenStream(context.Background(), frame.Address{Host: "fast", Port: 2})
	bPayload := make([]byte, 256*1024)
	rand.Read(bPayload)
	_, _ = stB.Write(bPayload)
	_ = stB.Close()

	select {
	case d := <-bDone:
		// B should drain in well under a second; without HOL fix it
		// would inherit A's 5+ s drip latency.
		if d > 2*time.Second {
			t.Fatalf("stream B blocked by stream A: took %s", d)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stream B never finished")
	}
	wg.Wait()
}

// mustPair is the *testing.B-compatible twin of newPairedSessions.
// Inlined rather than shared because the existing helper closes
// over *testing.T directly; refactoring it would noisify unrelated
// tests.
func mustPair(b *testing.B) (cli, srv *session.Session, cleanup func()) {
	b.Helper()
	serverKP, _ := crypto.GenerateKeypair()
	clientKP, _ := crypto.GenerateKeypair()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	srvAccept := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		srvAccept <- c
	}()
	cliConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		b.Fatal(err)
	}
	srvConn := <-srvAccept
	_ = ln.Close()

	type result struct {
		est *session.Established
		err error
	}
	cliCh, srvCh := make(chan result, 1), make(chan result, 1)
	go func() {
		est, err := session.HandshakeAsInitiator(pipeConn{cliConn}, *clientKP, serverKP.Public)
		cliCh <- result{est, err}
	}()
	go func() {
		est, err := session.HandshakeAsResponder(pipeConn{srvConn}, *serverKP)
		srvCh <- result{est, err}
	}()
	c := <-cliCh
	s := <-srvCh
	if c.err != nil || s.err != nil {
		b.Fatalf("handshake: cli=%v srv=%v", c.err, s.err)
	}
	cli = session.New(session.NewSecureChannel(pipeConn{cliConn}, c.est),
		session.Options{Role: session.RoleClient})
	srv = session.New(session.NewSecureChannel(pipeConn{srvConn}, s.est),
		session.Options{Role: session.RoleServer})
	cleanup = func() { _ = cli.Close(); _ = srv.Close() }
	return cli, srv, cleanup
}

// BenchmarkSessionThroughput measures sustained one-way bytes/sec
// across a real handshaken session pair. Compare ns/op + MB/s against
// pre-flow-control baseline:
//
//	go test -bench=BenchmarkSessionThroughput -benchtime=3s ./internal/session/
func BenchmarkSessionThroughput(b *testing.B) {
	cli, srv, cleanup := mustPair(b)
	defer cleanup()
	go func() { _ = srv.Run() }()
	go func() { _ = cli.Run() }()

	const chunkSize = 64 * 1024
	chunk := make([]byte, chunkSize)
	rand.Read(chunk)

	srvReady := make(chan *session.Stream, 1)
	go func() {
		st, err := srv.AcceptStream(context.Background())
		if err != nil {
			b.Errorf("accept: %v", err)
			return
		}
		srvReady <- st
		_, _ = io.Copy(io.Discard, st)
	}()

	stCli, err := cli.OpenStream(context.Background(), frame.Address{Host: "bench", Port: 0})
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	<-srvReady // ensure server is draining

	b.ResetTimer()
	b.SetBytes(int64(chunkSize))
	for i := 0; i < b.N; i++ {
		if _, err := stCli.Write(chunk); err != nil {
			b.Fatalf("write: %v", err)
		}
	}
	b.StopTimer()
	_ = stCli.Close()
}
