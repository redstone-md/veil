// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package client

import (
	"context"
	"testing"
	"time"
)

func TestNextBackoffDoubles(t *testing.T) {
	const max = 30 * time.Second
	cases := []struct {
		in, want time.Duration
	}{
		{500 * time.Millisecond, 1 * time.Second},
		{1 * time.Second, 2 * time.Second},
		{8 * time.Second, 16 * time.Second},
		{16 * time.Second, 30 * time.Second}, // capped
		{30 * time.Second, 30 * time.Second}, // stays at cap
	}
	for _, tc := range cases {
		got := nextBackoff(tc.in, max)
		if got != tc.want {
			t.Errorf("nextBackoff(%s, %s) = %s, want %s", tc.in, max, got, tc.want)
		}
	}
}

func TestSleepBackoffRespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t0 := time.Now()
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	if ok := sleepBackoff(ctx, 5*time.Second); ok {
		t.Fatal("expected sleepBackoff to return false on ctx cancel")
	}
	if elapsed := time.Since(t0); elapsed > 200*time.Millisecond {
		t.Errorf("sleepBackoff didn't honour cancel quickly: took %s", elapsed)
	}
}

func TestSleepBackoffCompletes(t *testing.T) {
	t0 := time.Now()
	if ok := sleepBackoff(context.Background(), 50*time.Millisecond); !ok {
		t.Fatal("expected sleepBackoff to return true on full sleep")
	}
	if elapsed := time.Since(t0); elapsed < 40*time.Millisecond {
		t.Errorf("sleepBackoff returned too early: %s", elapsed)
	}
}
