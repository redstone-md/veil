// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package masquetr_test

import (
	"context"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/redstone-md/veil/core/internal/transport/masquetr"
	"github.com/redstone-md/veil/core/internal/transport/quictr"
)

// TestRoundtripNestedQUIC brings up a real four-layer MASQUE stack
// on loopback (outer QUIC + HTTP/3 + CONNECT-UDP + inner QUIC) and
// pushes a few payloads through. Guards against silent regressions
// in the nested-QUIC plumbing that the per-layer unit tests cannot
// catch on their own.
func TestRoundtripNestedQUIC(t *testing.T) {
	innerAddr := freeUDPAddr(t)
	masqueAddr := freeUDPAddr(t)

	innerLn, err := quictr.Listen(innerAddr)
	if err != nil {
		t.Fatalf("inner quic listen: %v", err)
	}
	defer innerLn.Close()

	masqueLn, err := masquetr.Listen(masquetr.ListenConfig{
		Addr:       masqueAddr,
		Path:       "/m",
		TargetAddr: innerAddr,
	})
	if err != nil {
		t.Fatalf("masque listen: %v", err)
	}
	defer masqueLn.Close()

	echoErr := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		conn, err := innerLn.Accept(ctx)
		if err != nil {
			echoErr <- err
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				if _, werr := conn.Write(buf[:n]); werr != nil {
					echoErr <- werr
					return
				}
			}
			if err != nil {
				echoErr <- err
				return
			}
		}
	}()

	dialer := masquetr.NewDialer(masquetr.DialConfig{
		ProxyAddr:          masqueAddr,
		Path:               "/m",
		SNI:                "localhost",
		InsecureSkipVerify: true,
	})

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dialCancel()
	conn, err := dialer.Dial(dialCtx, "ignored:1")
	if err != nil {
		t.Fatalf("masque dial: %v", err)
	}

	for i, payload := range [][]byte{
		[]byte("hello over masque"),
		[]byte("second payload"),
		[]byte("third"),
	} {
		if _, err := conn.Write(payload); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		buf := make([]byte, len(payload))
		if _, err := io.ReadFull(conn, buf); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if string(buf) != string(payload) {
			t.Fatalf("payload %d mismatch: got %q want %q", i, buf, payload)
		}
	}

	_ = conn.Close()
	select {
	case <-echoErr:
	case <-time.After(2 * time.Second):
	}
}

// freeUDPAddr binds a UDP socket on 127.0.0.1:0, closes it, and
// returns the address. Suitable for letting downstream listeners pick
// a free port without race-prone scanning.
func freeUDPAddr(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free udp: %v", err)
	}
	addr := pc.LocalAddr().(*net.UDPAddr)
	_ = pc.Close()
	return "127.0.0.1:" + strconv.Itoa(addr.Port)
}
