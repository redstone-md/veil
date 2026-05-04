// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package frame_test

import (
	"bytes"
	"errors"
	"net"
	"testing"

	"github.com/redstone-md/veil/core/internal/frame"
)

func TestFrameRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		f    frame.Frame
	}{
		{
			name: "stream data with payload no padding",
			f: frame.Frame{
				Type:     frame.TypeStreamData,
				Flags:    frame.FlagEndStream,
				StreamID: 1,
				Payload:  []byte("hello world"),
			},
		},
		{
			name: "ping with token",
			f: frame.Frame{
				Type:     frame.TypePing,
				StreamID: 0,
				Payload:  []byte{1, 2, 3, 4, 5, 6, 7, 8},
			},
		},
		{
			name: "padding only frame",
			f: frame.Frame{
				Type:       frame.TypePaddingOnly,
				StreamID:   0,
				PaddingLen: 64,
			},
		},
		{
			name: "stream data with payload and padding",
			f: frame.Frame{
				Type:       frame.TypeStreamData,
				StreamID:   3,
				Payload:    []byte("the quick brown fox"),
				PaddingLen: 11,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			encoded, err := tc.f.Encode()
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if len(encoded) != tc.f.EncodedLen() {
				t.Fatalf("EncodedLen %d != actual %d", tc.f.EncodedLen(), len(encoded))
			}
			decoded, n, err := frame.Decode(encoded)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if n != len(encoded) {
				t.Fatalf("decode consumed %d, expected %d", n, len(encoded))
			}
			if decoded.Type != tc.f.Type {
				t.Errorf("Type: want %v got %v", tc.f.Type, decoded.Type)
			}
			if decoded.Flags != tc.f.Flags {
				t.Errorf("Flags: want %#02x got %#02x", tc.f.Flags, decoded.Flags)
			}
			if decoded.StreamID != tc.f.StreamID {
				t.Errorf("StreamID: want %d got %d", tc.f.StreamID, decoded.StreamID)
			}
			if !bytes.Equal(decoded.Payload, tc.f.Payload) {
				t.Errorf("Payload mismatch")
			}
			if decoded.PaddingLen != tc.f.PaddingLen {
				t.Errorf("PaddingLen: want %d got %d", tc.f.PaddingLen, decoded.PaddingLen)
			}
		})
	}
}

func TestDecodeShortFrame(t *testing.T) {
	t.Parallel()

	// 11 bytes is one short of the header.
	short := make([]byte, 11)
	if _, _, err := frame.Decode(short); !errors.Is(err, frame.ErrShortFrame) {
		t.Fatalf("expected ErrShortFrame, got %v", err)
	}

	// Header complete but payload truncated.
	full, _ := (&frame.Frame{Type: frame.TypeStreamData, StreamID: 7, Payload: []byte("xyz")}).Encode()
	if _, _, err := frame.Decode(full[:len(full)-1]); !errors.Is(err, frame.ErrShortFrame) {
		t.Fatalf("expected ErrShortFrame on truncated payload, got %v", err)
	}
}

func TestDecodeMultipleConcatenated(t *testing.T) {
	t.Parallel()

	a, _ := (&frame.Frame{Type: frame.TypeStreamData, StreamID: 1, Payload: []byte("aaa")}).Encode()
	b, _ := (&frame.Frame{Type: frame.TypePing, Payload: []byte("01234567")}).Encode()
	buf := append(a, b...)

	first, n1, err := frame.Decode(buf)
	if err != nil {
		t.Fatalf("decode1: %v", err)
	}
	if first.Type != frame.TypeStreamData {
		t.Fatalf("first type: %v", first.Type)
	}
	second, n2, err := frame.Decode(buf[n1:])
	if err != nil {
		t.Fatalf("decode2: %v", err)
	}
	if second.Type != frame.TypePing {
		t.Fatalf("second type: %v", second.Type)
	}
	if n1+n2 != len(buf) {
		t.Fatalf("did not consume entire buffer")
	}
}

func TestStreamOpenRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []frame.StreamOpenPayload{
		{
			StreamType:    frame.StreamTypeReliable,
			InitialWindow: 256 * 1024,
			Target: frame.Address{
				IP:   net.ParseIP("1.2.3.4"),
				Port: 443,
			},
		},
		{
			StreamType:    frame.StreamTypeReliable,
			InitialWindow: 64 * 1024,
			Target: frame.Address{
				IP:   net.ParseIP("2606:4700:4700::1111"),
				Port: 443,
			},
		},
		{
			StreamType:    frame.StreamTypeReliable,
			InitialWindow: 64 * 1024,
			Target: frame.Address{
				Host: "example.com",
				Port: 80,
			},
		},
	}

	for _, want := range cases {
		want := want
		t.Run(want.Target.String(), func(t *testing.T) {
			t.Parallel()
			b, err := want.Encode()
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			got, err := frame.DecodeStreamOpen(b)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.StreamType != want.StreamType ||
				got.InitialWindow != want.InitialWindow ||
				got.Target.Port != want.Target.Port {
				t.Fatalf("mismatch: want %+v got %+v", want, got)
			}
			if want.Target.IP != nil {
				if !want.Target.IP.Equal(got.Target.IP) {
					t.Fatalf("IP: want %v got %v", want.Target.IP, got.Target.IP)
				}
			}
			if want.Target.Host != got.Target.Host {
				t.Fatalf("Host: want %q got %q", want.Target.Host, got.Target.Host)
			}
		})
	}
}

func TestStreamOpenRejectsLongHostname(t *testing.T) {
	t.Parallel()

	longHost := make([]byte, 256)
	for i := range longHost {
		longHost[i] = 'a'
	}
	p := frame.StreamOpenPayload{
		StreamType: frame.StreamTypeReliable,
		Target:     frame.Address{Host: string(longHost), Port: 80},
	}
	if _, err := p.Encode(); err == nil {
		t.Fatal("expected encode to reject 256-byte hostname")
	}
}

func TestEncodeRejectsOversizePayload(t *testing.T) {
	t.Parallel()

	over := make([]byte, frame.MaxPayload+1)
	f := &frame.Frame{Type: frame.TypeStreamData, StreamID: 1, Payload: over}
	if _, err := f.Encode(); err == nil {
		t.Fatal("expected encode to reject oversize payload")
	}
}
