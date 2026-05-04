// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package realitytr

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"sync"
	"time"

	"github.com/redstone-md/veil/core/internal/transport"
)

// ListenConfig parameterises a Reality listener.
type ListenConfig struct {
	// Addr is the host:port the listener binds (TCP).
	Addr string

	// Secret is the per-deployment auth secret (typically derived
	// from the server's static Noise XK public key via
	// DeriveAuthSecret).
	Secret []byte

	// TargetSNI is the real host name probes will appear to be
	// connecting to. Reality forges a certificate matching this
	// SNI for clients that pass auth and proxies to TargetAddr
	// for everyone else.
	TargetSNI string

	// TargetAddr is the host:port to splice probe / unauthorised
	// connections to. Defaults to "<TargetSNI>:443" when empty.
	TargetAddr string

	// Logger receives operational events (auth misses leading to
	// splice, dial failures, etc). Nil means slog.Default().
	Logger *slog.Logger
}

// Listener accepts inbound Reality connections, returning only those
// that carried a valid auth tag. Probe traffic is silently spliced
// to the real target and never surfaces to Accept callers.
type Listener struct {
	cfg      ListenConfig
	tcpLn    net.Listener
	verifier *Verifier
	logger   *slog.Logger

	tlsCfg *tls.Config

	accepts chan transport.Conn
	closed  chan struct{}
	once    sync.Once
}

// Listen binds the configured address.
func Listen(cfg ListenConfig) (*Listener, error) {
	if cfg.TargetSNI == "" {
		return nil, errors.New("realitytr: TargetSNI is required")
	}
	if cfg.TargetAddr == "" {
		cfg.TargetAddr = cfg.TargetSNI + ":443"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if len(cfg.Secret) < 16 {
		return nil, ErrAuthShortSecret
	}

	tcpLn, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("realitytr: tcp listen: %w", err)
	}
	tlsCfg, err := forgedTLSConfig(cfg.TargetSNI)
	if err != nil {
		_ = tcpLn.Close()
		return nil, err
	}

	l := &Listener{
		cfg:      cfg,
		tcpLn:    tcpLn,
		verifier: NewVerifier(cfg.Secret),
		logger:   cfg.Logger,
		tlsCfg:   tlsCfg,
		accepts:  make(chan transport.Conn, 16),
		closed:   make(chan struct{}),
	}

	go l.acceptLoop()
	return l, nil
}

// Accept blocks until the next authenticated client arrives.
func (l *Listener) Accept(ctx context.Context) (transport.Conn, error) {
	select {
	case c := <-l.accepts:
		return c, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

// Close shuts the listener down. Already-spliced connections continue
// to run to completion in their own goroutines.
func (l *Listener) Close() error {
	var err error
	l.once.Do(func() {
		close(l.closed)
		err = l.tcpLn.Close()
	})
	return err
}

func (l *Listener) acceptLoop() {
	for {
		raw, err := l.tcpLn.Accept()
		if err != nil {
			select {
			case <-l.closed:
				return
			default:
			}
			l.logger.Warn("realitytr accept", "err", err)
			return
		}
		go l.handle(raw)
	}
}

func (l *Listener) handle(raw net.Conn) {
	_ = raw.SetDeadline(time.Now().Add(20 * time.Second))
	br := bufio.NewReaderSize(raw, 16*1024)
	hello, helloRaw, err := ParseClientHello(br)

	switch {
	case err == nil && l.verifier.Verify(hello.SessionID) == nil:
		// Genuine Veil client. Drain anything bufio buffered
		// beyond the ClientHello so the TLS handshake reads it.
		l.upgrade(raw, br, hello, helloRaw)
	default:
		if err != nil {
			l.logger.Debug("reality probe / parse miss",
				"err", err, "remote", raw.RemoteAddr().String())
		} else {
			l.logger.Debug("reality auth miss",
				"sni", hello.SNI, "remote", raw.RemoteAddr().String())
		}
		l.splice(raw, br, helloRaw)
	}
}

// upgrade clears the deadline, hands the connection to crypto/tls
// (with the prepended ClientHello bytes restored), waits for the TLS
// handshake to complete, and forwards the result to Accept.
func (l *Listener) upgrade(raw net.Conn, br *bufio.Reader, hello *HelloInfo, helloRaw []byte) {
	_ = raw.SetDeadline(time.Time{})
	prefixed := &prependConn{
		Conn:   raw,
		prefix: append(append([]byte(nil), helloRaw...), drainBuffered(br)...),
	}
	tlsConn := tls.Server(prefixed, l.tlsCfg)
	if err := tlsConn.HandshakeContext(context.Background()); err != nil {
		l.logger.Warn("reality tls handshake failed",
			"sni", hello.SNI, "remote", raw.RemoteAddr().String(), "err", err)
		_ = raw.Close()
		return
	}
	wrapped := &realityConn{Conn: tlsConn, raw: raw}
	select {
	case l.accepts <- wrapped:
		l.logger.Info("reality client accepted",
			"sni", hello.SNI, "remote", raw.RemoteAddr().String())
	case <-l.closed:
		_ = wrapped.Close()
	}
}

// splice pipes probe traffic to the real target. Failures to dial
// the target collapse the probe with a TCP RST, which is
// indistinguishable from a real backend that is briefly unhealthy.
func (l *Listener) splice(raw net.Conn, br *bufio.Reader, helloRaw []byte) {
	_ = raw.SetDeadline(time.Time{})
	defer raw.Close()

	d := &net.Dialer{Timeout: 10 * time.Second}
	target, err := d.Dial("tcp", l.cfg.TargetAddr)
	if err != nil {
		l.logger.Warn("reality splice dial failed",
			"target", l.cfg.TargetAddr, "err", err)
		return
	}
	defer target.Close()

	// Replay the ClientHello bytes (and anything else bufio happened
	// to read) verbatim so the target completes its real TLS
	// handshake with the probe.
	if len(helloRaw) > 0 {
		if _, err := target.Write(helloRaw); err != nil {
			return
		}
	}
	leftover := drainBuffered(br)
	if len(leftover) > 0 {
		if _, err := target.Write(leftover); err != nil {
			return
		}
	}

	pipeBoth(raw, target)
}

func drainBuffered(br *bufio.Reader) []byte {
	n := br.Buffered()
	if n == 0 {
		return nil
	}
	buf, _ := br.Peek(n)
	out := append([]byte(nil), buf...)
	_, _ = br.Discard(n)
	return out
}

func pipeBoth(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(a, b); halfClose(a) }()
	go func() { defer wg.Done(); _, _ = io.Copy(b, a); halfClose(b) }()
	wg.Wait()
}

func halfClose(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.CloseWrite()
	}
}

// prependConn is a net.Conn whose Reads return prefix bytes before
// delegating to the underlying connection. Used so crypto/tls can
// process the ClientHello we already consumed off the wire.
type prependConn struct {
	net.Conn
	prefix []byte
}

func (p *prependConn) Read(b []byte) (int, error) {
	if len(p.prefix) > 0 {
		n := copy(b, p.prefix)
		p.prefix = p.prefix[n:]
		return n, nil
	}
	return p.Conn.Read(b)
}

// realityConn is the transport.Conn handed out by Accept.
type realityConn struct {
	net.Conn
	raw net.Conn
}

func (c *realityConn) RemoteAddr() net.Addr { return c.raw.RemoteAddr() }
func (c *realityConn) LocalAddr() net.Addr  { return c.raw.LocalAddr() }

// forgedTLSConfig produces a tls.Config bearing a freshly-generated
// self-signed ECDSA P-256 certificate whose SAN/CN names the target
// SNI. Probes never reach this config (they are spliced earlier);
// only authenticated Veil clients see it, and they skip TLS chain
// validation because Noise XK is the real authentication anchor.
func forgedTLSConfig(sni string) (*tls.Config, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("realitytr: ecdsa generate: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: sni},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{sni},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		return nil, fmt.Errorf("realitytr: x509 create: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("realitytr: marshal ec key: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("realitytr: x509 keypair: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}
