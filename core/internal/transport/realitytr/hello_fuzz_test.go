// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package realitytr_test

import (
	"bytes"
	"testing"

	"github.com/redstone-md/veil/core/internal/transport/realitytr"
)

// FuzzParseClientHello hammers the TLS ClientHello byte-parser the
// Reality listener uses to make routing decisions. The parser sees
// arbitrary bytes from the network, so a panic here is a remote-
// crash bug.
//
// Run locally with:
//
//	cd core && go test -fuzz=FuzzParseClientHello -fuzztime=30s \
//	  ./internal/transport/realitytr
func FuzzParseClientHello(f *testing.F) {
	// Seed corpus: one valid-looking ClientHello plus a few
	// truncated and malformed variants.
	for _, blob := range [][]byte{
		minimalValidHello(),
		{},
		{0x16, 0x03, 0x03, 0x00, 0x00}, // record header only
		{0x16, 0x03, 0x03, 0xFF, 0xFF}, // claims 65535-byte body
		{0x17, 0x03, 0x03, 0x00, 0x05, 0, 0, 0, 0, 0}, // wrong record type
		{0x16, 0x03, 0x03, 0x00, 0x04, 0x01, 0, 0, 0}, // body says ClientHello, length 0
	} {
		f.Add(blob)
	}

	f.Fuzz(func(t *testing.T, in []byte) {
		// Property: never panic, regardless of input shape.
		_, _, _ = realitytr.ParseClientHello(bytes.NewReader(in))
	})
}

// minimalValidHello returns a hand-crafted, structurally valid TLS
// ClientHello with an SNI extension naming "example.com" and a
// 32-byte SessionID. Used as a fuzz seed; not exposed.
func minimalValidHello() []byte {
	// Body fields:
	//   client_version (2)  random (32)
	//   session_id_length (1) session_id (32)
	//   cipher_suites_length (2) cipher_suites (2)
	//   compression_methods_length (1) compression_methods (1)
	//   extensions_length (2) extensions (...)

	random := make([]byte, 32)
	for i := range random {
		random[i] = 0xAA
	}
	sessionID := make([]byte, 32)
	for i := range sessionID {
		sessionID[i] = 0xBB
	}
	const sni = "example.com"

	// SNI extension body: list_length(2) + entry(name_type 1 +
	//                     name_length 2 + name).
	sniExt := []byte{
		0x00, byte(3 + len(sni)),
		0x00,
		0x00, byte(len(sni)),
	}
	sniExt = append(sniExt, []byte(sni)...)
	// extension framing: type(2) length(2) body
	ext := []byte{0x00, 0x00, 0x00, byte(len(sniExt))}
	ext = append(ext, sniExt...)

	body := []byte{0x03, 0x03}
	body = append(body, random...)
	body = append(body, byte(len(sessionID)))
	body = append(body, sessionID...)
	body = append(body, 0x00, 0x02, 0x13, 0x01) // 2-byte ciphers, TLS_AES_128_GCM_SHA256
	body = append(body, 0x01, 0x00)             // 1 compression method
	body = append(body, 0x00, byte(len(ext)))   // extensions length
	body = append(body, ext...)

	hs := []byte{0x01, 0x00, 0x00, byte(len(body))}
	hs = append(hs, body...)

	rec := []byte{0x16, 0x03, 0x03, 0x00, byte(len(hs))}
	rec = append(rec, hs...)
	return rec
}
