// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package session

// ringBuf is a fixed-capacity circular byte buffer with no internal
// locking. The owning Stream serialises access via rxMu.
//
// Sizing: starts at the negotiated InitialWindow (typically 256 KiB
// — see DefaultStreamRecvBuffer) and may grow up to MaxStreamRecvBuffer
// when the producer side saturates the ring repeatedly. The grow path
// doubles the capacity, copies live data, and returns the delta so
// the caller can emit a WINDOW_UPDATE telling the peer it now has
// more send credit.
type ringBuf struct {
	buf  []byte
	head int // index of next byte to Read
	tail int // index of next byte to Write
	size int // bytes currently stored
}

// defaultRingCap matches DefaultStreamRecvBuffer. Kept as a separate
// constant so unit tests can reason about the ring without dragging
// in the whole session package.
const defaultRingCap = 1 << 18 // 256 KiB

func newRingBuf(capacity int) *ringBuf {
	if capacity <= 0 {
		capacity = defaultRingCap
	}
	return &ringBuf{buf: make([]byte, capacity)}
}

func (r *ringBuf) Cap() int  { return len(r.buf) }
func (r *ringBuf) Len() int  { return r.size }
func (r *ringBuf) Free() int { return len(r.buf) - r.size }

// Read copies up to len(p) bytes out of the ring into p. Returns the
// number of bytes copied. Returns 0 if the ring is empty.
func (r *ringBuf) Read(p []byte) int {
	if r.size == 0 || len(p) == 0 {
		return 0
	}
	n := len(p)
	if n > r.size {
		n = r.size
	}
	cap := len(r.buf)
	if r.head+n <= cap {
		copy(p, r.buf[r.head:r.head+n])
	} else {
		first := cap - r.head
		copy(p[:first], r.buf[r.head:])
		copy(p[first:n], r.buf[:n-first])
	}
	r.head = (r.head + n) % cap
	r.size -= n
	return n
}

// Write copies up to free space bytes from p into the ring. Returns
// the number of bytes accepted; the caller is expected to retry with
// the remainder once Read frees room.
func (r *ringBuf) Write(p []byte) int {
	free := len(r.buf) - r.size
	if free == 0 || len(p) == 0 {
		return 0
	}
	n := len(p)
	if n > free {
		n = free
	}
	cap := len(r.buf)
	if r.tail+n <= cap {
		copy(r.buf[r.tail:r.tail+n], p)
	} else {
		first := cap - r.tail
		copy(r.buf[r.tail:], p[:first])
		copy(r.buf[:n-first], p[first:n])
	}
	r.tail = (r.tail + n) % cap
	r.size += n
	return n
}

// resize grows (or shrinks) the ring to newCap, preserving the bytes
// currently in flight. Returns the (signed) capacity delta so the
// caller can emit a matching WINDOW_UPDATE. Returns 0 if newCap
// equals the current capacity, or if newCap can't fit existing data.
func (r *ringBuf) resize(newCap int) int {
	oldCap := len(r.buf)
	if newCap == oldCap || newCap < r.size {
		return 0
	}
	nb := make([]byte, newCap)
	if r.size > 0 {
		if r.head+r.size <= oldCap {
			copy(nb, r.buf[r.head:r.head+r.size])
		} else {
			first := oldCap - r.head
			copy(nb[:first], r.buf[r.head:])
			copy(nb[first:r.size], r.buf[:r.size-first])
		}
	}
	r.buf = nb
	r.head = 0
	r.tail = r.size
	return newCap - oldCap
}
