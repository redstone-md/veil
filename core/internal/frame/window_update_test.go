// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package frame_test

import (
	"testing"

	"github.com/redstone-md/veil/core/internal/frame"
)

func TestWindowUpdateRoundTrip(t *testing.T) {
	for _, inc := range []uint32{1, 1024, 1 << 20, 0xFFFFFFFF} {
		p := &frame.WindowUpdatePayload{Increment: inc}
		got, err := frame.DecodeWindowUpdate(p.Encode())
		if err != nil {
			t.Fatalf("decode inc=%d: %v", inc, err)
		}
		if got.Increment != inc {
			t.Fatalf("roundtrip inc: want %d got %d", inc, got.Increment)
		}
	}
}

func TestWindowUpdateRejectsZero(t *testing.T) {
	p := &frame.WindowUpdatePayload{Increment: 0}
	if _, err := frame.DecodeWindowUpdate(p.Encode()); err == nil {
		t.Fatal("expected zero-increment rejection")
	}
}

func TestWindowUpdateRejectsShort(t *testing.T) {
	if _, err := frame.DecodeWindowUpdate([]byte{0x00, 0x01}); err == nil {
		t.Fatal("expected short-payload rejection")
	}
}
