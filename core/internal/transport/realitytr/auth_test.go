// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package realitytr_test

import (
	"errors"
	"testing"

	"github.com/redstone-md/veil/core/internal/transport/realitytr"
)

func TestAuthRoundTrip(t *testing.T) {
	t.Parallel()

	pub := bytesPattern(0x01, 32)
	secret, err := realitytr.DeriveAuthSecret(pub)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if len(secret) != realitytr.AuthSecretSize {
		t.Fatalf("secret size: want %d got %d", realitytr.AuthSecretSize, len(secret))
	}

	id, err := realitytr.BuildAuthSessionID(secret)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(id) != realitytr.SessionIDSize {
		t.Fatalf("id size: want %d got %d", realitytr.SessionIDSize, len(id))
	}

	v := realitytr.NewVerifier(secret)
	if err := v.Verify(id); err != nil {
		t.Fatalf("verify fresh: %v", err)
	}
}

func TestAuthRejectsReplay(t *testing.T) {
	t.Parallel()
	pub := bytesPattern(0x02, 32)
	secret, _ := realitytr.DeriveAuthSecret(pub)

	id, _ := realitytr.BuildAuthSessionID(secret)
	v := realitytr.NewVerifier(secret)
	if err := v.Verify(id); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	if err := v.Verify(id); !errors.Is(err, realitytr.ErrAuthReplayed) {
		t.Fatalf("replay verify: want ErrAuthReplayed got %v", err)
	}
}

func TestAuthRejectsWrongSecret(t *testing.T) {
	t.Parallel()
	secretA, _ := realitytr.DeriveAuthSecret(bytesPattern(0x03, 32))
	secretB, _ := realitytr.DeriveAuthSecret(bytesPattern(0x04, 32))

	id, _ := realitytr.BuildAuthSessionID(secretA)
	v := realitytr.NewVerifier(secretB)
	if err := v.Verify(id); !errors.Is(err, realitytr.ErrAuthBadTag) {
		t.Fatalf("verify with wrong secret: want ErrAuthBadTag got %v", err)
	}
}

func TestAuthRejectsBadSize(t *testing.T) {
	t.Parallel()
	secret, _ := realitytr.DeriveAuthSecret(bytesPattern(0x05, 32))
	v := realitytr.NewVerifier(secret)
	if err := v.Verify(make([]byte, 16)); !errors.Is(err, realitytr.ErrAuthBadSize) {
		t.Fatalf("short id: want ErrAuthBadSize got %v", err)
	}
	if err := v.Verify(nil); !errors.Is(err, realitytr.ErrAuthMissing) {
		t.Fatalf("nil id: want ErrAuthMissing got %v", err)
	}
}

func TestDistinctNoncesEachCall(t *testing.T) {
	t.Parallel()
	secret, _ := realitytr.DeriveAuthSecret(bytesPattern(0x06, 32))
	a, _ := realitytr.BuildAuthSessionID(secret)
	b, _ := realitytr.BuildAuthSessionID(secret)
	if string(a[:16]) == string(b[:16]) {
		t.Fatal("expected unique nonces per call")
	}
}

func bytesPattern(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}
