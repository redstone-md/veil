// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package masquetr

import (
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
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/quic-go/masque-go"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/yosida95/uritemplate/v3"

	"github.com/redstone-md/veil/core/internal/transport"
)

// ListenConfig parameterises a MASQUE listener.
type ListenConfig struct {
	// Addr is the host:port the HTTP/3 endpoint binds (UDP).
	Addr string

	// Path is the URL path that accepts CONNECT-UDP requests.
	// Defaults to "/masque" when empty. Operators SHOULD pick a
	// non-default path to deflect untargeted scanners.
	Path string

	// TargetAddr is where this proxy forwards every accepted
	// CONNECT-UDP datagram. In the standard Veil deployment this
	// is the loopback UDP port hosting the inner QUIC-Noise
	// listener. Required.
	TargetAddr string

	// CertFile / KeyFile supply the TLS cert. When empty an
	// in-memory self-signed cert is generated; suitable for
	// development only.
	CertFile string
	KeyFile  string

	Logger *slog.Logger
}

// Listener satisfies the transport.Listener interface but never
// surfaces any connections through Accept directly. Connections
// flow through the inner QUIC listener configured separately on
// `TargetAddr`; the MASQUE listener exists only to accept HTTP/3
// CONNECT-UDP traffic and forward UDP datagrams to that loopback.
//
// Accept blocks until Close to keep the server's fan-in loop alive
// without producing spurious accept events.
type Listener struct {
	cfg     ListenConfig
	srv     *http3.Server
	proxy   *masque.Proxy
	tpl     *uritemplate.Template
	logger  *slog.Logger
	udpConn *net.UDPConn

	closeOnce sync.Once
	closed    chan struct{}
	serveErr  chan error
}

// Listen brings up the HTTP/3 + MASQUE proxy.
func Listen(cfg ListenConfig) (*Listener, error) {
	if cfg.TargetAddr == "" {
		return nil, errors.New("masquetr: TargetAddr is required")
	}
	if cfg.Path == "" {
		cfg.Path = "/masque"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	tlsCfg, err := buildTLSConfig(cfg)
	if err != nil {
		return nil, err
	}
	tlsCfg.NextProtos = []string{http3.NextProtoH3}

	udpAddr, err := net.ResolveUDPAddr("udp", cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("masquetr: resolve listen: %w", err)
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, fmt.Errorf("masquetr: udp listen: %w", err)
	}

	// CONNECT-UDP URI template; the {target_host} and {target_port}
	// placeholders are required by RFC 9298 §3.1 even though we
	// override the routing on the server side.
	tplStr := fmt.Sprintf("https://%s%s/{target_host}/{target_port}/", cfg.Addr, trimSlash(cfg.Path))
	tpl, err := uritemplate.New(tplStr)
	if err != nil {
		_ = udpConn.Close()
		return nil, fmt.Errorf("masquetr: build URI template: %w", err)
	}

	proxy := &masque.Proxy{}

	mux := http.NewServeMux()
	l := &Listener{
		cfg:      cfg,
		proxy:    proxy,
		tpl:      tpl,
		logger:   cfg.Logger,
		udpConn:  udpConn,
		closed:   make(chan struct{}),
		serveErr: make(chan error, 1),
	}
	mux.HandleFunc(trimSlash(cfg.Path)+"/", l.handle)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, &http.Request{})
	})

	srv := &http3.Server{
		Addr:            cfg.Addr,
		TLSConfig:       tlsCfg,
		Handler:         mux,
		EnableDatagrams: true,
		// 1350 outer InitialPacketSize matches the masque-go client's
		// default and leaves headroom for the inner QUIC's 1200 MTU
		// once HTTP/3 + CONNECT-UDP framing overhead is accounted for.
		// Without this the server caps datagram payloads below the
		// inner QUIC's minimum and every inner handshake fails with
		// "DATAGRAM frame too large".
		QUICConfig: &quic.Config{
			EnableDatagrams:   true,
			InitialPacketSize: 1350,
		},
	}
	l.srv = srv

	go func() {
		err := srv.Serve(udpConn)
		select {
		case l.serveErr <- err:
		default:
		}
	}()
	return l, nil
}

func (l *Listener) handle(w http.ResponseWriter, r *http.Request) {
	req, err := masque.ParseRequest(r, l.tpl)
	if err != nil {
		l.logger.Warn("masquetr: bad CONNECT-UDP", "err", err)
		var pe *masque.RequestParseError
		if errors.As(err, &pe) {
			http.Error(w, pe.Error(), pe.HTTPStatus)
			return
		}
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Dial a fresh per-flow UDP socket pointed at the inner QUIC
	// listener; ignore the client's `req.Target` field. Doing so
	// turns the proxy into a Veil-specific forwarder rather than
	// an open relay.
	target, err := net.ResolveUDPAddr("udp", l.cfg.TargetAddr)
	if err != nil {
		l.logger.Warn("masquetr: resolve target", "err", err)
		http.Error(w, "bad target", http.StatusInternalServerError)
		return
	}
	flow, err := net.DialUDP("udp", nil, target)
	if err != nil {
		l.logger.Warn("masquetr: dial target", "err", err)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}

	if err := l.proxy.ProxyConnectedSocket(w, req, flow); err != nil {
		l.logger.Debug("masquetr: proxy ended", "err", err)
	}
}

// Accept blocks until Close. See type comment for why.
func (l *Listener) Accept(ctx context.Context) (transport.Conn, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-l.serveErr:
		if err == nil {
			return nil, net.ErrClosed
		}
		return nil, fmt.Errorf("masquetr serve: %w", err)
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

// Close stops the HTTP/3 server.
func (l *Listener) Close() error {
	var err error
	l.closeOnce.Do(func() {
		close(l.closed)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = l.srv.Shutdown(shutdownCtx)
		_ = l.udpConn.Close()
	})
	return err
}

func trimSlash(p string) string {
	for len(p) > 0 && p[len(p)-1] == '/' {
		p = p[:len(p)-1]
	}
	if len(p) == 0 || p[0] != '/' {
		p = "/" + p
	}
	return p
}

func buildTLSConfig(cfg ListenConfig) (*tls.Config, error) {
	if cfg.CertFile != "" && cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("masquetr: load keypair: %w", err)
		}
		return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}, nil
	}
	host, _, _ := net.SplitHostPort(cfg.Addr)
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return selfSignedTLS(host)
}

func selfSignedTLS(cn string) (*tls.Config, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		DNSNames:     []string{cn, "localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}, nil
}
