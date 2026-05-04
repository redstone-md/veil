// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package snipool_test

import (
	"strings"
	"testing"

	"github.com/redstone-md/veil/core/internal/dpi/snipool"
)

func TestDefaultPoolNonEmpty(t *testing.T) {
	t.Parallel()
	p := snipool.New()
	if p.Len() < 50 {
		t.Fatalf("expected >=50 entries in starter pool, got %d", p.Len())
	}
}

func TestFilterByRegion(t *testing.T) {
	t.Parallel()
	p := snipool.New()

	ru := p.Filter(snipool.RegionRU)
	if len(ru) == 0 {
		t.Fatal("expected non-empty RU filter")
	}
	for _, e := range ru {
		if e.Region != snipool.RegionRU && e.Region != snipool.RegionGlobal {
			t.Errorf("RU filter returned unexpected region %q for %q", e.Region, e.Domain)
		}
	}

	all := p.Filter(snipool.RegionGlobal)
	if len(all) < len(ru) {
		t.Fatalf("Global filter (%d) smaller than RU filter (%d)", len(all), len(ru))
	}
}

func TestPickDeterministicWithSeed(t *testing.T) {
	t.Parallel()
	p := snipool.New()
	first := p.Pick(snipool.RegionRU, 12345)
	second := p.Pick(snipool.RegionRU, 12345)
	if first != second {
		t.Fatalf("seeded Pick non-deterministic: %q != %q", first, second)
	}
	if first == "" {
		t.Fatal("Pick returned empty string for non-empty filter")
	}
}

func TestPickRespectsRegion(t *testing.T) {
	t.Parallel()
	p := snipool.New()

	cnEntries := make(map[string]struct{})
	for _, e := range p.Filter(snipool.RegionCN) {
		cnEntries[e.Domain] = struct{}{}
	}
	if len(cnEntries) == 0 {
		t.Fatal("CN region empty")
	}

	// Sample many picks; every result must be in the CN-or-Global
	// subset.
	for i := int64(1); i < 200; i++ {
		got := p.Pick(snipool.RegionCN, i)
		if _, ok := cnEntries[got]; !ok {
			t.Fatalf("seed %d returned out-of-region pick %q", i, got)
		}
	}
}

func TestShardDeterministicAndDistinct(t *testing.T) {
	t.Parallel()
	p := snipool.New()

	a1 := p.Shard(snipool.RegionGlobal, "alice", 5)
	a2 := p.Shard(snipool.RegionGlobal, "alice", 5)
	if !sameSlice(a1, a2) {
		t.Fatal("Shard non-deterministic for same userKey")
	}

	b := p.Shard(snipool.RegionGlobal, "bob", 5)
	if sameSlice(a1, b) {
		t.Fatal("Shard returned identical sets for distinct userKeys (collision suspect)")
	}
	if len(b) != 5 {
		t.Fatalf("Shard size mismatch: want 5 got %d", len(b))
	}
}

func TestReplaceClearsCache(t *testing.T) {
	t.Parallel()
	p := snipool.New()
	original := p.Pick(snipool.RegionGlobal, 1)
	p.Replace([]snipool.Entry{
		{Domain: "example.com", Region: snipool.RegionGlobal, Weight: 1},
	})
	got := p.Pick(snipool.RegionGlobal, 1)
	if got != "example.com" {
		t.Fatalf("after Replace expected example.com, got %q", got)
	}
	if got == original {
		t.Log("note: Replace happened to return same domain by coincidence")
	}
	if !strings.Contains(got, "example.com") {
		t.Fatalf("unexpected: %q", got)
	}
}

func sameSlice(a, b []snipool.Entry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Domain != b[i].Domain {
			return false
		}
	}
	return true
}
