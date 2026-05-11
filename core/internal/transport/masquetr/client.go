// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package masquetr

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/quic-go/masque-go"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/yosida95/uritemplate/v3"

	"github.com/redstone-md/veil/core/internal/transport"
)

// DialConfig parameterises a MASQUE dialer.
type DialConfig struct {
	// ProxyAddr is the host:port of the HTTP/3 MASQUE proxy.
	ProxyAddr string

	// Path is the URL path the proxy accepts CONNECT-UDP requests on.
	// Defaults to "/masque" when empty. MUST match the server.
	Path string

	// SNI overrides the TLS Server Name Indication for the outer
	// QUIC handshake against the proxy. When empty, the host part of
	// ProxyAddr is used.
	SNI string

	// InsecureSkipVerify disables outer TLS verification. The inner
	// Noise XK handshake is the actual authentication anchor; the
	// outer TLS is cover only. Pre-alpha defaults to true at the
	// caller layer.
	InsecureSkipVerify bool

	// InnerTarget is the host:port placeholder embedded in the
	// CONNECT-UDP URI template's {target_host}/{target_port}. The
	// Veil server ignores this and routes every flow to its
	// configured loopback inner-QUIC listener; the value still has
	// to parse as a valid host:port. Defaults to "127.0.0.1:1".
	InnerTarget string
}

// Dialer initiates outbound connections through a MASQUE proxy.
//
// Each Dial brings up:
//
//  1. an outer QUIC + HTTP/3 connection to the proxy,
//  2. an Extended-CONNECT (CONNECT-UDP) request to open a UDP flow,
//  3. an *inner* QUIC handshake over the proxied UDP datagrams,
//  4. an inner bidirectional stream that the rest of the Veil stack
//     treats as a transport.Conn.
type Dialer struct {
	cfg DialConfig
}

// NewDialer returns a configured MASQUE dialer.
func NewDialer(cfg DialConfig) *Dialer {
	if cfg.Path == "" {
		cfg.Path = "/masque"
	}
	if cfg.InnerTarget == "" {
		cfg.InnerTarget = "127.0.0.1:1"
	}
	return &Dialer{cfg: cfg}
}

// Dial opens a tunneled QUIC stream through the MASQUE proxy.
//
// The addr argument is currently informational — the inner target is
// fixed to DialConfig.InnerTarget, since the server-side proxy routes
// every accepted flow to its own configured loopback listener
// regardless of what the client requested.
func (d *Dialer) Dial(ctx context.Context, addr string) (transport.Conn, error) {
	sni := d.cfg.SNI
	if sni == "" {
		host, _, err := net.SplitHostPort(d.cfg.ProxyAddr)
		if err != nil {
			return nil, fmt.Errorf("masquetr: parse proxy addr: %w", err)
		}
		sni = host
	}

	tplStr := fmt.Sprintf("https://%s%s/{target_host}/{target_port}/", d.cfg.ProxyAddr, trimSlash(d.cfg.Path))
	tpl, err := uritemplate.New(tplStr)
	if err != nil {
		return nil, fmt.Errorf("masquetr: build URI template: %w", err)
	}

	outerTLS := &tls.Config{
		ServerName:         sni,
		InsecureSkipVerify: d.cfg.InsecureSkipVerify, //nolint:gosec // Noise XK above
		NextProtos:         []string{http3.NextProtoH3},
		MinVersion:         tls.VersionTLS13,
	}

	mc := &masque.Client{TLSClientConfig: outerTLS}
	target := d.cfg.InnerTarget
	_ = addr // accepted for the transport.Dialer signature; see doc above
	// bodyclose false-positive: mc.DialAddr returns a net.PacketConn
	// (closed via pc.Close() in every error / done path below), not
	// an http.Response.
	pc, _, err := mc.DialAddr(ctx, tpl, target) //nolint:bodyclose
	if err != nil {
		_ = mc.Close()
		return nil, fmt.Errorf("masquetr: dial proxy: %w", err)
	}

	// Inner QUIC handshake over the proxied UDP flow. The remote
	// address is what the proxy will see; the proxiedConn ignores
	// WriteTo's addr argument and always forwards to the server's
	// configured loopback target. We still need a valid net.Addr
	// for quic-go's bookkeeping.
	innerAddr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		_ = pc.Close()
		_ = mc.Close()
		return nil, fmt.Errorf("masquetr: resolve inner addr: %w", err)
	}

	innerTLS := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // Noise XK above
		NextProtos:         []string{"h3"},
		MinVersion:         tls.VersionTLS13,
	}
	innerQuicCfg := &quic.Config{
		MaxIdleTimeout:        90 * time.Second,
		KeepAlivePeriod:       15 * time.Second,
		MaxIncomingStreams:    1024,
		MaxIncomingUniStreams: 0,
		Allow0RTT:             false,
	}

	conn, err := quic.Dial(ctx, pc, innerAddr, innerTLS, innerQuicCfg)
	if err != nil {
		_ = pc.Close()
		_ = mc.Close()
		return nil, fmt.Errorf("masquetr: inner quic dial: %w", err)
	}
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		_ = conn.CloseWithError(0, "no stream")
		_ = pc.Close()
		_ = mc.Close()
		return nil, fmt.Errorf("masquetr: open inner stream: %w", err)
	}

	return &dialedConn{
		stream:  stream,
		conn:    conn,
		packets: pc,
		client:  mc,
	}, nil
}

// dialedConn adapts the nested QUIC stream into transport.Conn while
// keeping references to every layer below it so Close tears them down
// in order.
type dialedConn struct {
	stream  *quic.Stream
	conn    *quic.Conn
	packets net.PacketConn
	client  *masque.Client
}

func (c *dialedConn) Read(p []byte) (int, error)  { return c.stream.Read(p) }
func (c *dialedConn) Write(p []byte) (int, error) { return c.stream.Write(p) }

// Close tears the four layers down inside-out: stream, inner QUIC
// connection, MASQUE-proxied PacketConn, then the outer QUIC client.
// The brief sleep mirrors quictr.quicConn.Close — gives the peer a
// chance to drain in-flight bytes before CONNECTION_CLOSE arrives.
func (c *dialedConn) Close() error {
	_ = c.stream.Close()
	time.Sleep(100 * time.Millisecond)
	_ = c.conn.CloseWithError(0, "")
	_ = c.packets.Close()
	return c.client.Close()
}

func (c *dialedConn) LocalAddr() net.Addr  { return c.conn.LocalAddr() }
func (c *dialedConn) RemoteAddr() net.Addr { return c.conn.RemoteAddr() }
