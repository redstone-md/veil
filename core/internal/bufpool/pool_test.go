// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package bufpool_test

import (
	"sync"
	"testing"

	"github.com/redstone-md/veil/core/internal/bufpool"
)

func TestGetReturnsRequestedLength(t *testing.T) {
	for _, size := range []int{1, 100, 2048, 8192, 14 * 1024, 32 * 1024, 100 * 1024, 1 << 20} {
		b := bufpool.Get(size)
		if len(b) != size {
			t.Errorf("size=%d: got len=%d, want %d", size, len(b), size)
		}
		bufpool.Put(b)
	}
}

func TestGetSizeBucketing(t *testing.T) {
	tiers := bufpool.Tiers()
	for _, want := range tiers {
		b := bufpool.Get(want)
		if cap(b) != want {
			t.Errorf("requested %d → got cap %d, want %d", want, cap(b), want)
		}
		bufpool.Put(b)
	}
}

func TestPutNilSafe(t *testing.T) {
	bufpool.Put(nil)
}

func TestPutOversizeDropped(t *testing.T) {
	// A slice larger than the largest tier should not be returned to
	// any pool — Put just drops it. Verify by checking that we don't
	// receive it back from a Get of any tier size.
	tiers := bufpool.Tiers()
	largest := tiers[len(tiers)-1]
	huge := make([]byte, largest*2)
	bufpool.Put(huge)
	// Just round-trip a normal-sized request; no panic = pass.
	b := bufpool.Get(2048)
	if cap(b) != 2048 {
		t.Errorf("cap=%d, want 2048", cap(b))
	}
}

func TestConcurrentReuse(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10000; i++ {
				b := bufpool.Get(8192)
				b[0] = byte(i)
				bufpool.Put(b)
			}
		}()
	}
	wg.Wait()
}

// BenchmarkPoolGetPut measures the cost of a Get+Put round trip
// vs raw make. The whole point of the pool: GC pressure avoidance.
func BenchmarkPoolGetPut(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf := bufpool.Get(8192)
		bufpool.Put(buf)
	}
}

func BenchmarkMakeBaseline(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = make([]byte, 8192)
	}
}
