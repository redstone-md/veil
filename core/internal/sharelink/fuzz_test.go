// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package sharelink_test

import (
	"strings"
	"testing"

	"github.com/redstone-md/veil/core/internal/sharelink"
)

// FuzzDecode confirms the share-link parser never panics on
// arbitrary input. The link surface is deliberately small (scheme +
// base64 of JSON) but it sees user input directly, so an unhardened
// parser is a remote-crash vector.
//
// Run locally with:
//
//	cd core && go test -fuzz=FuzzDecode -fuzztime=30s \
//	  ./internal/sharelink
func FuzzDecode(f *testing.F) {
	f.Add("veil://eyJTZXJ2ZXJzIjpbXX0") // valid empty config
	f.Add("veil://")
	f.Add("veil://!!!")
	f.Add("not-a-veil-link")
	f.Add(strings.Repeat("veil://A", 100))
	f.Add("")

	f.Fuzz(func(t *testing.T, in string) {
		_, _ = sharelink.Decode(in)
	})
}
