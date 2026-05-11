// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package session

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestRingBufBasic(t *testing.T) {
	r := newRingBuf(8)
	if r.Cap() != 8 || r.Len() != 0 || r.Free() != 8 {
		t.Fatalf("initial: cap=%d len=%d free=%d", r.Cap(), r.Len(), r.Free())
	}

	n := r.Write([]byte("hello"))
	if n != 5 || r.Len() != 5 || r.Free() != 3 {
		t.Fatalf("after write 5: n=%d len=%d free=%d", n, r.Len(), r.Free())
	}

	out := make([]byte, 4)
	if got := r.Read(out); got != 4 || string(out) != "hell" {
		t.Fatalf("read 4: n=%d out=%q", got, out)
	}
	if r.Len() != 1 || r.Free() != 7 {
		t.Fatalf("after read 4: len=%d free=%d", r.Len(), r.Free())
	}
}

func TestRingBufWrap(t *testing.T) {
	r := newRingBuf(4)
	// Fill, drain partially, write more — forces tail to wrap.
	r.Write([]byte("AB"))
	out := make([]byte, 2)
	r.Read(out) // head=2, tail=2, size=0
	if got := r.Write([]byte("CDEF")); got != 4 {
		t.Fatalf("wrap write: %d", got)
	}
	got := make([]byte, 4)
	if r.Read(got); string(got) != "CDEF" {
		t.Fatalf("wrap read: %q", got)
	}
}

func TestRingBufOverflow(t *testing.T) {
	r := newRingBuf(4)
	if n := r.Write([]byte("ABCDE")); n != 4 {
		t.Fatalf("expected partial 4, got %d", n)
	}
	if r.Len() != 4 || r.Free() != 0 {
		t.Fatalf("expected full: len=%d free=%d", r.Len(), r.Free())
	}
	if n := r.Write([]byte("X")); n != 0 {
		t.Fatalf("expected 0 on full ring, got %d", n)
	}
}

func TestRingBufReadEmpty(t *testing.T) {
	r := newRingBuf(4)
	out := make([]byte, 4)
	if n := r.Read(out); n != 0 {
		t.Fatalf("read on empty: %d", n)
	}
}

func TestRingBufFuzzRoundTrip(t *testing.T) {
	r := newRingBuf(1024)
	src := make([]byte, 4096)
	rand.Read(src)
	dst := bytes.NewBuffer(nil)

	written := 0
	read := 0
	for read < len(src) {
		// Write what fits.
		n := r.Write(src[written:])
		written += n
		// Drain at most half the ring per iteration to force wrap.
		buf := make([]byte, 200)
		got := r.Read(buf)
		dst.Write(buf[:got])
		read += got
	}
	if !bytes.Equal(dst.Bytes(), src) {
		t.Fatalf("roundtrip mismatch: got %d bytes, want %d", dst.Len(), len(src))
	}
}

// BenchmarkRingBufThroughput measures the cost of write+read cycles
// through the ring vs. the old append-and-slice approach. Run with
//   go test -bench=BenchmarkRing -benchmem ./internal/session/
func BenchmarkRingBufThroughput(b *testing.B) {
	const cap = 1 << 20 // 1 MiB
	r := newRingBuf(cap)
	chunk := make([]byte, 14*1024) // streamDataChunk
	rand.Read(chunk)
	out := make([]byte, len(chunk))

	b.ResetTimer()
	b.SetBytes(int64(len(chunk)))
	for i := 0; i < b.N; i++ {
		r.Write(chunk)
		r.Read(out)
	}
}

// BenchmarkAppendBaseline is the pre-flow-control behaviour kept
// as a regression baseline. Compare ns/op + B/op vs the ring.
func BenchmarkAppendBaseline(b *testing.B) {
	chunk := make([]byte, 14*1024)
	rand.Read(chunk)
	out := make([]byte, len(chunk))

	b.ResetTimer()
	b.SetBytes(int64(len(chunk)))
	for i := 0; i < b.N; i++ {
		buf := append([]byte(nil), chunk...)
		copy(out, buf)
		buf = buf[len(out):]
		_ = buf
	}
}
