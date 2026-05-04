// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package transport_test

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redstone-md/veil/core/internal/transport"
)

// stubDialer.Dial sleeps `delay` then either returns an error or a
// dummy conn. It records when its dial was cancelled by ctx so the
// race tests can assert losing dials get torn down.
type stubDialer struct {
	delay     time.Duration
	err       error
	cancelled atomic.Bool
}

func (s *stubDialer) Dial(ctx context.Context, _ string) (transport.Conn, error) {
	t := time.NewTimer(s.delay)
	defer t.Stop()
	select {
	case <-t.C:
		if s.err != nil {
			return nil, s.err
		}
		return &stubConn{}, nil
	case <-ctx.Done():
		s.cancelled.Store(true)
		return nil, ctx.Err()
	}
}

type stubConn struct{}

func (c *stubConn) Read(p []byte) (int, error)  { return 0, io.EOF }
func (c *stubConn) Write(p []byte) (int, error) { return len(p), nil }
func (c *stubConn) Close() error                { return nil }
func (c *stubConn) LocalAddr() net.Addr         { return nil }
func (c *stubConn) RemoteAddr() net.Addr        { return nil }

func TestFallbackDialer_RacesAndPicksFastest(t *testing.T) {
	fast := &stubDialer{delay: 10 * time.Millisecond}
	slow := &stubDialer{delay: 250 * time.Millisecond}

	d := transport.NewFallback(nil)
	d.Add("slow", "10.0.0.1:443", slow)
	d.Add("fast", "10.0.0.2:443", fast)

	start := time.Now()
	conn, label, err := d.Dial(context.Background())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if label != "fast" {
		t.Errorf("expected fast to win, got %q", label)
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("expected <100ms (fast wins), got %v", elapsed)
	}
	// drain goroutine cancels the slow dialer; give it a moment
	time.Sleep(50 * time.Millisecond)
	if !slow.cancelled.Load() {
		t.Errorf("slow dialer should have been cancelled after fast won")
	}
}

func TestFallbackDialer_AllFailReturnsError(t *testing.T) {
	a := &stubDialer{delay: 10 * time.Millisecond, err: errors.New("a-fail")}
	b := &stubDialer{delay: 10 * time.Millisecond, err: errors.New("b-fail")}
	d := transport.NewFallback(nil)
	d.Add("a", "x", a)
	d.Add("b", "y", b)

	_, _, err := d.Dial(context.Background())
	if err == nil {
		t.Fatal("expected error when all dials fail")
	}
}

func TestFallbackDialer_OneSuccessAmongFailures(t *testing.T) {
	bad := &stubDialer{delay: 10 * time.Millisecond, err: errors.New("nope")}
	good := &stubDialer{delay: 30 * time.Millisecond}
	d := transport.NewFallback(nil)
	d.Add("bad", "x", bad)
	d.Add("good", "y", good)

	conn, label, err := d.Dial(context.Background())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if label != "good" {
		t.Errorf("expected good, got %q", label)
	}
}
