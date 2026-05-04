// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package frame_test

import (
	"bytes"
	"testing"

	"github.com/redstone-md/veil/core/internal/frame"
)

// FuzzDecodeRoundTrip pushes arbitrary bytes through Decode and asserts
// the decoder never panics, never reports impossible sizes, and (when
// it accepts the input) round-trips the canonical re-encode.
//
// Run locally with:
//
//	cd core && go test -fuzz=FuzzDecodeRoundTrip -fuzztime=30s ./internal/frame
func FuzzDecodeRoundTrip(f *testing.F) {
	// Seed corpus with valid encodings so the fuzzer has a starting
	// point and short-circuits early on the happy path.
	for _, fr := range []frame.Frame{
		{Type: frame.TypeStreamData, StreamID: 1, Payload: []byte("hello")},
		{Type: frame.TypePing, Payload: []byte{1, 2, 3, 4, 5, 6, 7, 8}},
		{Type: frame.TypeStreamClose, StreamID: 7},
		{Type: frame.TypePaddingOnly, PaddingLen: 64},
		{Type: frame.TypeStreamOpen, StreamID: 3, Payload: []byte{1, 0, 0, 0, 0, 0, 0}},
	} {
		b, _ := fr.Encode()
		f.Add(b)
	}

	f.Fuzz(func(t *testing.T, in []byte) {
		fr, n, err := frame.Decode(in)
		if err != nil {
			return
		}
		if n <= 0 || n > len(in) {
			t.Fatalf("decoded %d bytes from %d-byte input", n, len(in))
		}
		if int(fr.PaddingLen) > frame.MaxPadding {
			t.Fatalf("padding length %d exceeds max", fr.PaddingLen)
		}
		if len(fr.Payload) > frame.MaxPayload {
			t.Fatalf("payload length %d exceeds max", len(fr.Payload))
		}

		// Re-encode and confirm the canonical form decodes back to
		// the same logical frame. We compare logical fields, not
		// byte equality, because Decode may have aliased Payload
		// into the input slice and tail bytes might differ.
		canonical, err := fr.Encode()
		if err != nil {
			t.Fatalf("re-encode: %v", err)
		}
		fr2, _, err := frame.Decode(canonical)
		if err != nil {
			t.Fatalf("re-decode of canonical bytes: %v", err)
		}
		if fr.Type != fr2.Type ||
			fr.Flags != fr2.Flags ||
			fr.StreamID != fr2.StreamID ||
			fr.PaddingLen != fr2.PaddingLen ||
			!bytes.Equal(fr.Payload, fr2.Payload) {
			t.Fatalf("round-trip mismatch: %+v vs %+v", fr, fr2)
		}
	})
}

// FuzzDecodeStreamOpen targets the STREAM_OPEN payload parser, which
// hand-rolls a small TLV format and is therefore the most likely
// place to hit an out-of-bounds slice.
func FuzzDecodeStreamOpen(f *testing.F) {
	for _, payload := range [][]byte{
		// reliable, window 0x100, ipv4 1.2.3.4:443
		{0x01, 0, 0, 1, 0, 0, 0x07, 0x01, 1, 2, 3, 4, 0x01, 0xBB},
		// domain example.com:80
		append(
			[]byte{0x01, 0, 0, 1, 0, 0, 0x0E, 0x03, 0x0B},
			append([]byte("example.com"), 0x00, 0x50)...,
		),
	} {
		f.Add(payload)
	}

	f.Fuzz(func(t *testing.T, in []byte) {
		_, _ = frame.DecodeStreamOpen(in)
		// Property: never panics. No further assertions; rejecting
		// invalid input is acceptable.
	})
}
