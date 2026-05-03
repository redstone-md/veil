// Package quictr implements the QUIC transport adapter for VWP/1.
//
// At the v0 stage this is a thin wrapper around quic-go that gives
// us a single bidirectional stream per session over QUIC, suitable
// for prototyping the Noise XK handshake and basic session traffic.
//
// Production-grade behaviour (uTLS-injected fingerprints, ALPN
// pinning, real certificate management) is layered on in subsequent
// phases.
package quictr

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/redstone-md/veil/core/internal/transport"
)

// ALPN is the application-layer protocol identifier used during the
// QUIC handshake. It is set to a generic HTTP/3 token to blend in
// with normal web traffic on the wire; the inner application layer
// is VWP/1, not HTTP.
const ALPN = "h3"

// quicConn adapts a quic-go stream into a transport.Conn.
type quicConn struct {
	stream *quic.Stream
	conn   *quic.Conn
}

func (q *quicConn) Read(p []byte) (int, error)  { return q.stream.Read(p) }
func (q *quicConn) Write(p []byte) (int, error) { return q.stream.Write(p) }

// Close sends a stream FIN and then tears down the underlying QUIC
// connection. We give the peer a brief grace period to drain any
// in-flight stream data before issuing CONNECTION_CLOSE; without
// this, a peer that is still reading our final write tends to
// surface a confusing "Application error 0x0 (remote)" instead of
// a clean io.EOF.
//
// This grace is a v0 expedient and will go away in Phase 1 once
// the session layer owns its own framed FIN signalling.
func (q *quicConn) Close() error {
	_ = q.stream.Close()
	time.Sleep(100 * time.Millisecond)
	return q.conn.CloseWithError(0, "")
}

func (q *quicConn) LocalAddr() net.Addr  { return q.conn.LocalAddr() }
func (q *quicConn) RemoteAddr() net.Addr { return q.conn.RemoteAddr() }

// Listener accepts inbound QUIC connections.
type Listener struct {
	ln *quic.Listener
}

// Listen starts a QUIC listener on addr. The TLS configuration is
// generated with a self-signed certificate suitable only for the
// v0 prototype; later phases replace this with an ACME-provisioned
// certificate or a Reality-style stolen-SNI handshake.
func Listen(addr string) (*Listener, error) {
	tlsCfg, err := selfSignedTLSConfig()
	if err != nil {
		return nil, err
	}
	ln, err := quic.ListenAddr(addr, tlsCfg, defaultQUICConfig())
	if err != nil {
		return nil, fmt.Errorf("quic listen: %w", err)
	}
	return &Listener{ln: ln}, nil
}

// Accept blocks until a QUIC connection arrives, opens its first
// bidirectional stream, and returns it.
func (l *Listener) Accept(ctx context.Context) (transport.Conn, error) {
	conn, err := l.ln.Accept(ctx)
	if err != nil {
		return nil, fmt.Errorf("quic accept: %w", err)
	}
	stream, err := conn.AcceptStream(ctx)
	if err != nil {
		_ = conn.CloseWithError(0, "no stream")
		return nil, fmt.Errorf("quic accept stream: %w", err)
	}
	return &quicConn{stream: stream, conn: conn}, nil
}

// Close stops accepting new connections.
func (l *Listener) Close() error { return l.ln.Close() }

// Dialer initiates outbound QUIC connections.
//
// In the v0 prototype, server certificates are not verified; this
// is appropriate because authentication is performed at the
// Noise XK layer above. Reality / proper-cert modes are added in
// later phases.
type Dialer struct{}

// NewDialer returns a configured QUIC dialer.
func NewDialer() *Dialer { return &Dialer{} }

// Dial opens a QUIC connection to addr and starts the first
// bidirectional stream.
func (d *Dialer) Dial(ctx context.Context, addr string) (transport.Conn, error) {
	tlsCfg := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{ALPN},
		MinVersion:         tls.VersionTLS13,
	}
	conn, err := quic.DialAddr(ctx, addr, tlsCfg, defaultQUICConfig())
	if err != nil {
		return nil, fmt.Errorf("quic dial: %w", err)
	}
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		_ = conn.CloseWithError(0, "no stream")
		return nil, fmt.Errorf("quic open stream: %w", err)
	}
	return &quicConn{stream: stream, conn: conn}, nil
}

func defaultQUICConfig() *quic.Config {
	return &quic.Config{
		MaxIdleTimeout:        90 * time.Second,
		MaxIncomingStreams:    1024,
		MaxIncomingUniStreams: 0,
		KeepAlivePeriod:       15 * time.Second,
		Allow0RTT:             false,
	}
}

// selfSignedTLSConfig produces a one-shot, in-memory self-signed
// certificate and a TLS config suitable only for prototype use.
//
// The certificate is regenerated on every Listen call; clients in
// the v0 prototype skip verification because the authentication
// anchor is the Noise XK layer, not the TLS PKI.
func selfSignedTLSConfig() (*tls.Config, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("ecdsa generate: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "veil-prototype"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		return nil, fmt.Errorf("x509 create: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("marshal ec key: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("x509 keypair: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{ALPN},
		MinVersion:   tls.VersionTLS13,
	}, nil
}
