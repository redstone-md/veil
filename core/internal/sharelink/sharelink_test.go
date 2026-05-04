// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package sharelink_test

import (
	"strings"
	"testing"

	"github.com/redstone-md/veil/core/internal/config"
	"github.com/redstone-md/veil/core/internal/sharelink"
)

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	want := &config.ClientConfig{
		Servers: []config.ClientServer{
			{Type: config.TransportReality, Addr: "vps.example.com:443", SNI: "www.cloudflare.com"},
			{Type: config.TransportQUIC, Addr: "vps.example.com:18443"},
		},
		ServerStaticKeyB64: "AAAA",
		StaticKeyPath:      "client.key",
		SOCKS5Listen:       "127.0.0.1:1080",
	}
	link, err := sharelink.Encode(want)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.HasPrefix(link, sharelink.Scheme) {
		t.Fatalf("missing scheme: %q", link)
	}

	got, err := sharelink.Decode(link)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ServerStaticKeyB64 != want.ServerStaticKeyB64 {
		t.Errorf("ServerStaticKeyB64 mismatch")
	}
	if len(got.Servers) != 2 {
		t.Fatalf("servers len: %d", len(got.Servers))
	}
	if got.Servers[0].Type != config.TransportReality ||
		got.Servers[0].SNI != "www.cloudflare.com" {
		t.Errorf("server[0] mismatch: %+v", got.Servers[0])
	}
	if got.Servers[1].Type != config.TransportQUIC {
		t.Errorf("server[1] mismatch: %+v", got.Servers[1])
	}
}

func TestDecodeRejectsBadPrefix(t *testing.T) {
	t.Parallel()
	if _, err := sharelink.Decode("https://wrong"); err == nil {
		t.Fatal("expected error on bad prefix")
	}
}

func TestDecodeRejectsBadBase64(t *testing.T) {
	t.Parallel()
	if _, err := sharelink.Decode("veil://!!!notbase64!!!"); err == nil {
		t.Fatal("expected error on bad base64")
	}
}

func TestEncodeRejectsNil(t *testing.T) {
	t.Parallel()
	if _, err := sharelink.Encode(nil); err == nil {
		t.Fatal("expected error on nil config")
	}
}
