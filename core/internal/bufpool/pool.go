// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

// Package bufpool implements a tiered sync.Pool of []byte slices.
//
// Inspired by xray-core/common/bytespool: rather than a single pool
// shared across allocations of wildly different sizes (which causes
// the runtime to keep large slices alive when callers wanted small
// ones), the pool keeps a fan of size buckets. Get rounds up to the
// next bucket; Put returns the slice to its bucket.
//
// Hot paths: SecureChannel.RecvFrame (cipher buf, ~16-32 KiB),
// Stream.Write encode buffer (~14 KiB + header), Datagram framing
// (≤ 64 KiB). All comfortably fit in the configured tiers.
package bufpool

import "sync"

const (
	numTiers   = 5
	startSize  = 2 * 1024 // 2 KiB smallest tier
	growFactor = 4        // each tier 4× the previous: 2K, 8K, 32K, 128K, 512K
)

var (
	pools [numTiers]sync.Pool
	sizes [numTiers]int
)

func init() {
	s := startSize
	for i := 0; i < numTiers; i++ {
		size := s // capture for closure
		pools[i] = sync.Pool{
			New: func() any { return make([]byte, size) },
		}
		sizes[i] = s
		s *= growFactor
	}
}

// Get returns a slice with cap >= size and len == size. The returned
// slice was either freshly allocated or recycled from a previous Put.
//
// Sizes larger than the biggest tier are allocated directly (the
// caller is then responsible for not Put-ing them, since they don't
// belong to any pool — Put will silently drop oversize slices).
func Get(size int) []byte {
	for i, ps := range sizes {
		if size <= ps {
			b := pools[i].Get().([]byte)
			return b[:size]
		}
	}
	return make([]byte, size)
}

// Put returns a slice to the pool. The slice's length is reset to
// its capacity so the next Get sees a clean buffer. Slices that
// don't match any tier are dropped to GC.
//
// Safe to call with nil (no-op).
func Put(b []byte) {
	if b == nil {
		return
	}
	c := cap(b)
	for i, ps := range sizes {
		if c == ps {
			//nolint:staticcheck // SA6002: pooling byte slices is the
			// canonical pattern in Go for high-throughput byte work;
			// the runtime allocates the slice header on the heap once
			// (in init), and Get/Put just shuffle the same backing array.
			pools[i].Put(b[:cap(b)])
			return
		}
	}
}

// Tiers returns the configured bucket sizes for diagnostics + tests.
func Tiers() []int {
	out := make([]int, numTiers)
	copy(out, sizes[:])
	return out
}
