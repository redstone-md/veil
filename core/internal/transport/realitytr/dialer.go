// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package realitytr

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	utls "github.com/refraction-networking/utls"

	"github.com/redstone-md/veil/core/internal/transport"
)

// dialTimeout caps the TCP + TLS handshake duration.
const dialTimeout = 20 * time.Second

// DialConfig parameterises a Reality client dial.
type DialConfig struct {
	// Secret is the per-deployment auth secret. Both peers derive
	// it identically from the server's static Noise XK public key
	// via DeriveAuthSecret.
	Secret []byte

	// SNI is the host name placed in the TLS ClientHello. It MUST
	// match TargetSNI on the server (and SHOULD be a real domain
	// the server-side splice path can reach, otherwise probes will
	// see TCP RST instead of a plausible response).
	SNI string

	// Fingerprint selects the uTLS browser preset for the
	// ClientHello. An empty value defaults to HelloChrome_Auto.
	Fingerprint utls.ClientHelloID
}

// Dial opens a Reality session to addr (host:port) on the server.
func Dial(ctx context.Context, addr string, cfg DialConfig) (transport.Conn, error) {
	if cfg.SNI == "" {
		return nil, errors.New("realitytr: SNI is required")
	}
	if len(cfg.Secret) < 16 {
		return nil, ErrAuthShortSecret
	}

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	d := &net.Dialer{Timeout: dialTimeout}
	tcp, err := d.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("realitytr: tcp dial: %w", err)
	}

	sessionID, err := BuildAuthSessionID(cfg.Secret)
	if err != nil {
		_ = tcp.Close()
		return nil, err
	}

	hello := cfg.Fingerprint
	if hello == (utls.ClientHelloID{}) {
		hello = utls.HelloChrome_Auto
	}

	uCfg := &utls.Config{
		ServerName:         cfg.SNI,
		InsecureSkipVerify: true, // Noise XK is the real anchor
		MinVersion:         utls.VersionTLS12,
	}
	uConn := utls.UClient(tcp, uCfg, hello)

	// Build the Chrome-shaped Hello, then overwrite its SessionId
	// with our auth-bearing 32-octet value before we send it on
	// the wire.
	if err := uConn.BuildHandshakeState(); err != nil {
		_ = tcp.Close()
		return nil, fmt.Errorf("realitytr: build hello: %w", err)
	}
	uConn.HandshakeState.Hello.SessionId = sessionID
	if err := uConn.MarshalClientHello(); err != nil {
		_ = tcp.Close()
		return nil, fmt.Errorf("realitytr: marshal hello: %w", err)
	}

	if err := uConn.HandshakeContext(dialCtx); err != nil {
		_ = tcp.Close()
		return nil, fmt.Errorf("realitytr: tls handshake: %w", err)
	}
	return &realityClientConn{UConn: uConn, raw: tcp}, nil
}

// realityClientConn is the transport.Conn returned to callers.
type realityClientConn struct {
	*utls.UConn
	raw net.Conn
}

func (c *realityClientConn) RemoteAddr() net.Addr { return c.raw.RemoteAddr() }
func (c *realityClientConn) LocalAddr() net.Addr  { return c.raw.LocalAddr() }

// Dialer adapts the package-level Dial function into the
// transport.Dialer interface.
type Dialer struct {
	Config DialConfig
}

// NewDialer returns a Dialer that calls Dial with cfg.
func NewDialer(cfg DialConfig) *Dialer { return &Dialer{Config: cfg} }

// Dial satisfies transport.Dialer.
func (d *Dialer) Dial(ctx context.Context, addr string) (transport.Conn, error) {
	return Dial(ctx, addr, d.Config)
}
