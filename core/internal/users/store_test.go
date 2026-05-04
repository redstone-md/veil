// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package users_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/redstone-md/veil/core/internal/users"
)

func newTestStore(t *testing.T) *users.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "users.db")
	s, err := users.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCreateAndLookup(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	u, err := s.CreateUser(ctx, "alice", "PUBKEY-ALICE-32-bytes-base64xxxx")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if u.ID == "" || u.Status != users.StatusActive {
		t.Fatalf("unexpected user: %+v", u)
	}

	got, err := s.GetUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "alice" {
		t.Fatalf("name: got %q", got.Name)
	}

	byKey, err := s.GetUserByPubkey(ctx, u.PubkeyB64)
	if err != nil {
		t.Fatalf("get by pubkey: %v", err)
	}
	if byKey.ID != u.ID {
		t.Fatalf("pubkey lookup id mismatch")
	}

	byName, err := s.GetUserByName(ctx, "alice")
	if err != nil {
		t.Fatalf("get by name: %v", err)
	}
	if byName.ID != u.ID {
		t.Fatalf("name lookup id mismatch")
	}
}

func TestDuplicates(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.CreateUser(ctx, "bob", "K1"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := s.CreateUser(ctx, "bob", "K2")
	if !errors.Is(err, users.ErrDuplicateName) {
		t.Fatalf("dup name: got %v", err)
	}
	_, err = s.CreateUser(ctx, "bob2", "K1")
	if !errors.Is(err, users.ErrDuplicateKey) {
		t.Fatalf("dup key: got %v", err)
	}
}

func TestStatusUpdates(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	u, _ := s.CreateUser(ctx, "carol", "K3")
	if err := s.SetStatus(ctx, u.ID, users.StatusRevoked); err != nil {
		t.Fatalf("set status: %v", err)
	}
	got, _ := s.GetUser(ctx, u.ID)
	if got.Status != users.StatusRevoked {
		t.Fatalf("status not updated: %v", got.Status)
	}

	err := s.SetStatus(ctx, "no-such-id", users.StatusActive)
	if !errors.Is(err, users.ErrNotFound) {
		t.Fatalf("missing id: got %v", err)
	}
}

func TestQuotaAndAccumulator(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	u, _ := s.CreateUser(ctx, "dave", "K4")
	q := int64(1024 * 1024)
	if err := s.SetQuota(ctx, u.ID, &q); err != nil {
		t.Fatalf("set quota: %v", err)
	}

	s.AccumulateBytes(u.ID, 100)
	s.AccumulateBytes(u.ID, 200)
	flushed, err := s.FlushAccumulator(ctx)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if flushed != 300 {
		t.Fatalf("flushed = %d", flushed)
	}
	got, _ := s.GetUser(ctx, u.ID)
	if got.UsedBytesCurrentMonth != 300 {
		t.Fatalf("used = %d", got.UsedBytesCurrentMonth)
	}
	if got.LastSeen == nil {
		t.Fatalf("last_seen not updated")
	}
	if got.QuotaBytesPerMonth == nil || *got.QuotaBytesPerMonth != q {
		t.Fatalf("quota readback: %+v", got.QuotaBytesPerMonth)
	}
}

func TestAdminAuth(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.CreateAdminUser(ctx, "root", "correct-horse-battery-staple"); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := s.VerifyAdminPassword(ctx, "root", "correct-horse-battery-staple"); err != nil {
		t.Fatalf("verify good: %v", err)
	}
	if err := s.VerifyAdminPassword(ctx, "root", "wrong"); err == nil {
		t.Fatalf("verify bad: expected error")
	}
	if err := s.VerifyAdminPassword(ctx, "missing", "x"); !errors.Is(err, users.ErrNotFound) {
		t.Fatalf("missing user: got %v", err)
	}

	n, _ := s.CountAdmins(ctx)
	if n != 1 {
		t.Fatalf("admins count = %d", n)
	}
}

func TestRegenAndDelete(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	u, _ := s.CreateUser(ctx, "eve", "OLD-KEY")
	if err := s.RegenKey(ctx, u.ID, "NEW-KEY"); err != nil {
		t.Fatalf("regen: %v", err)
	}
	got, _ := s.GetUser(ctx, u.ID)
	if got.PubkeyB64 != "NEW-KEY" {
		t.Fatalf("regen not applied")
	}

	if err := s.DeleteUser(ctx, u.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err := s.GetUser(ctx, u.ID)
	if !errors.Is(err, users.ErrNotFound) {
		t.Fatalf("after delete: got %v", err)
	}
}

func TestListOrder(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	ctx := context.Background()

	for _, name := range []string{"a", "b", "c"} {
		if _, err := s.CreateUser(ctx, name, "K-"+name); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	all, err := s.ListUsers(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("count: %d", len(all))
	}
}
