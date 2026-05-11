// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package session

// ringBuf is a fixed-capacity circular byte buffer with no internal
// locking. The owning Stream serialises access via rxMu.
//
// Lifetimes are scoped to one Stream so this lives alongside it
// instead of in a public utilities package. Sized once at Stream
// creation from the negotiated InitialWindow; never grows or
// reallocates, which is the whole point — replacing the previous
// append-style rxBuf killed O(N²) memcpy on long downloads.
type ringBuf struct {
	buf  []byte
	head int // index of next byte to Read
	tail int // index of next byte to Write
	size int // bytes currently stored
}

func newRingBuf(capacity int) *ringBuf {
	if capacity <= 0 {
		capacity = 1 << 20 // 1 MiB default
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
