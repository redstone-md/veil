// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package mimicry_test

import (
	"testing"
	"time"

	"github.com/redstone-md/veil/core/internal/dpi/mimicry"
)

func TestNoneReturnsNil(t *testing.T) {
	t.Parallel()
	if s := mimicry.New(mimicry.ProfileNone, 1); s != nil {
		t.Fatalf("ProfileNone should yield nil shaper, got %v", s)
	}
}

func TestPadTargetCoversCurrentLen(t *testing.T) {
	t.Parallel()
	for _, p := range []mimicry.Profile{
		mimicry.ProfileBrowse, mimicry.ProfileVideo,
		mimicry.ProfileMessaging, mimicry.ProfileSearch,
	} {
		p := p
		t.Run(string(p), func(t *testing.T) {
			t.Parallel()
			s := mimicry.New(p, 42)
			for i := 0; i < 200; i++ {
				cur := i * 50
				got := s.PadTarget(cur)
				if got < cur {
					t.Fatalf("%s: PadTarget(%d) = %d < cur",
						p, cur, got)
				}
			}
		})
	}
}

func TestNextDelayInsideRange(t *testing.T) {
	t.Parallel()
	s := mimicry.New(mimicry.ProfileBrowse, 7)
	for i := 0; i < 500; i++ {
		d := s.NextDelay()
		if d < 0 || d > 8*time.Millisecond {
			t.Fatalf("delay out of range: %v", d)
		}
	}
}

func TestSeededDeterministic(t *testing.T) {
	t.Parallel()
	a := mimicry.New(mimicry.ProfileVideo, 999)
	b := mimicry.New(mimicry.ProfileVideo, 999)
	for i := 0; i < 50; i++ {
		if a.PadTarget(0) != b.PadTarget(0) {
			t.Fatalf("seeded shapers diverged at iteration %d", i)
		}
	}
}

func TestNilShaperSafe(t *testing.T) {
	t.Parallel()
	var s *mimicry.Shaper
	if s.PadTarget(123) != 123 {
		t.Fatal("nil PadTarget should pass through")
	}
	if s.NextDelay() != 0 {
		t.Fatal("nil NextDelay should be 0")
	}
	if s.Profile() != mimicry.ProfileNone {
		t.Fatal("nil Profile() should be ProfileNone")
	}
}
