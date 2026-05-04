// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package update_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/redstone-md/veil/core/internal/update"
)

func TestChecksumVerifierSuccess(t *testing.T) {
	t.Parallel()
	blob := []byte("hello veil")
	sum := sha256.Sum256(blob)
	v := update.ChecksumVerifier{ExpectedHex: hex.EncodeToString(sum[:])}
	if err := v.Verify(blob); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestChecksumVerifierMismatch(t *testing.T) {
	t.Parallel()
	v := update.ChecksumVerifier{ExpectedHex: "deadbeef" + "00" + "ff"}
	if err := v.Verify([]byte("anything")); err == nil {
		t.Fatal("expected mismatch error")
	}
}

func TestReplaceRunsVerifiersBeforeWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "veil-fake")
	if err := os.WriteFile(target, []byte("ORIGINAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	failing := failingVerifier{}
	err := update.Replace(target, []byte("NEW"), failing)
	if err == nil {
		t.Fatal("expected verifier to reject")
	}
	got, _ := os.ReadFile(target)
	if string(got) != "ORIGINAL" {
		t.Fatalf("target overwritten despite verifier failure: %q", got)
	}
}

func TestReplaceWritesNewContent(t *testing.T) {
	if runtime.GOOS == "windows" {
		// On Windows Replace cannot overwrite a running .exe and
		// emits ErrPendingRestart even on success; the file is
		// renamed via .old. Walk through the rename outcome.
		dir := t.TempDir()
		target := filepath.Join(dir, "veil-fake.exe")
		if err := os.WriteFile(target, []byte("ORIGINAL"), 0o644); err != nil {
			t.Fatal(err)
		}
		err := update.Replace(target, []byte("NEW"))
		if !errors.Is(err, update.ErrPendingRestart) {
			t.Fatalf("expected ErrPendingRestart, got %v", err)
		}
		got, _ := os.ReadFile(target)
		if string(got) != "NEW" {
			t.Fatalf("target not updated: %q", got)
		}
		aside, _ := os.ReadFile(target + ".old")
		if string(aside) != "ORIGINAL" {
			t.Fatalf("aside not preserved: %q", aside)
		}
		return
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "veil-fake")
	if err := os.WriteFile(target, []byte("ORIGINAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := update.Replace(target, []byte("NEW"))
	if !errors.Is(err, update.ErrPendingRestart) {
		t.Fatalf("expected ErrPendingRestart, got %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "NEW" {
		t.Fatalf("target not updated: %q", got)
	}
}

type failingVerifier struct{}

func (failingVerifier) Verify(_ []byte) error {
	return errors.New("nope")
}
