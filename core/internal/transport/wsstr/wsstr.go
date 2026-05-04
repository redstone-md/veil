// Package wsstr implements the WebSocket-over-TLS transport adapter
// for VWP/1.
//
// On the wire each WSS session is a single TLS connection carrying
// a single websocket session whose binary frames carry the AEAD
// records produced by the session.SecureChannel layer.
//
// On the client side this transport is the natural pair for uTLS
// fingerprint mimicry (which only works with TCP-based TLS) and for
// CDN-fronted deployments where 443/TCP is the only durably-open
// outbound port.
package wsstr

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
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/redstone-md/veil/core/internal/transport"
)

// DefaultPath is the URL path the server accepts WS upgrades on
// when no path is configured.
const DefaultPath = "/"

// dialTimeout caps the TCP + TLS + WS upgrade handshake duration.
const dialTimeout = 20 * time.Second

// idleTimeout is applied to the underlying TLS connection between
// frames so a wedged peer does not pin a goroutine forever.
const idleTimeout = 90 * time.Second

// wssConn adapts a websocket-derived net.Conn into transport.Conn.
type wssConn struct {
	netConn net.Conn
	wsConn  *websocket.Conn
	closer  func() error

	closeOnce sync.Once
	closeErr  error
}

func (c *wssConn) Read(p []byte) (int, error)  { return c.netConn.Read(p) }
func (c *wssConn) Write(p []byte) (int, error) { return c.netConn.Write(p) }

func (c *wssConn) Close() error {
	c.closeOnce.Do(func() {
		if c.closer != nil {
			c.closeErr = c.closer()
			return
		}
		c.closeErr = c.netConn.Close()
	})
	return c.closeErr
}

func (c *wssConn) LocalAddr() net.Addr  { return c.netConn.LocalAddr() }
func (c *wssConn) RemoteAddr() net.Addr { return c.netConn.RemoteAddr() }

// Listener accepts inbound WSS connections.
type Listener struct {
	httpServer *http.Server
	tcpLn      net.Listener
	path       string
	accepts    chan transport.Conn
	serveErr   chan error

	closeOnce sync.Once
	closed    chan struct{}
}

// ListenConfig parameterises a WSS listener.
type ListenConfig struct {
	// Addr is the host:port to bind.
	Addr string
	// Path is the URL path that accepts WS upgrades. Defaults to "/".
	Path string
	// TLS supplies the server certificate. Required.
	TLS *tls.Config
}

// Listen binds the given configuration.
func Listen(cfg ListenConfig) (*Listener, error) {
	if cfg.TLS == nil {
		return nil, errors.New("wsstr: TLS config required")
	}
	path := cfg.Path
	if path == "" {
		path = DefaultPath
	}
	tcpLn, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("wsstr: tcp listen: %w", err)
	}
	tlsLn := tls.NewListener(tcpLn, cfg.TLS)

	l := &Listener{
		tcpLn:    tcpLn,
		path:     path,
		accepts:  make(chan transport.Conn, 16),
		serveErr: make(chan error, 1),
		closed:   make(chan struct{}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc(path, l.upgrade)
	mux.HandleFunc("/", l.notFound)
	l.httpServer = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: dialTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          nil, // routed through accept-loop error reporting below
	}

	go func() {
		err := l.httpServer.Serve(tlsLn)
		select {
		case l.serveErr <- err:
		default:
		}
	}()
	return l, nil
}

// Accept blocks until a new WSS session is available, the listener
// is closed, or the underlying http.Server returns a fatal error.
func (l *Listener) Accept(ctx context.Context) (transport.Conn, error) {
	select {
	case c := <-l.accepts:
		return c, nil
	case err := <-l.serveErr:
		if err == nil {
			return nil, net.ErrClosed
		}
		return nil, fmt.Errorf("wsstr serve: %w", err)
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

// Close stops the listener.
func (l *Listener) Close() error {
	var err error
	l.closeOnce.Do(func() {
		close(l.closed)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = l.httpServer.Shutdown(shutdownCtx)
	})
	return err
}

func (l *Listener) upgrade(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Veil does not embed in browsers; same-origin checks are
		// not meaningful here. Authentication happens at the Noise
		// layer above.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	netConn := websocket.NetConn(context.Background(), c, websocket.MessageBinary)

	// Synthesise a remote address from the HTTP request because
	// websocket.NetConn().RemoteAddr returns a generic websocket
	// address that is not useful for logging.
	remote, _ := net.ResolveTCPAddr("tcp", r.RemoteAddr)
	wrapped := &wssConn{
		netConn: &addrOverrideConn{Conn: netConn, remote: remote},
		wsConn:  c,
		closer: func() error {
			_ = c.Close(websocket.StatusNormalClosure, "")
			return nil
		},
	}
	select {
	case l.accepts <- wrapped:
	case <-l.closed:
		_ = wrapped.Close()
	}
}

func (l *Listener) notFound(w http.ResponseWriter, _ *http.Request) {
	// Generic 404 to avoid leaking that this host runs Veil. A
	// production deployment fronts this with a real static site
	// (Phase 3 / Reality).
	http.Error(w, "Not Found", http.StatusNotFound)
}

// addrOverrideConn wraps a net.Conn and substitutes its RemoteAddr.
// Used because websocket.NetConn returns a fake address by default.
type addrOverrideConn struct {
	net.Conn
	remote net.Addr
}

func (c *addrOverrideConn) RemoteAddr() net.Addr {
	if c.remote != nil {
		return c.remote
	}
	return c.Conn.RemoteAddr()
}

// Dialer adapts the package-level Dial function into the
// transport.Dialer interface so a WSS endpoint can sit alongside
// other transports in a transport.FallbackDialer.
type Dialer struct {
	Config DialConfig
}

// NewDialer returns a Dialer that calls Dial with cfg.
func NewDialer(cfg DialConfig) *Dialer { return &Dialer{Config: cfg} }

// Dial satisfies transport.Dialer.
func (d *Dialer) Dial(ctx context.Context, addr string) (transport.Conn, error) {
	return Dial(ctx, addr, d.Config)
}

// DialConfig parameterises a WSS dial.
type DialConfig struct {
	// SNI is the TLS Server Name Indication to send. When empty,
	// the dialer derives it from the URL host.
	SNI string

	// Path is the URL path to upgrade against. Defaults to "/".
	Path string

	// InsecureSkipVerify disables certificate validation. The
	// authentication anchor is the Noise XK static key pinned in
	// configuration; TLS validation is therefore best-effort.
	InsecureSkipVerify bool

	// TLSDial is an optional dialer that supplies the underlying
	// TLS connection. When set, the standard crypto/tls Dial is
	// bypassed; this is the integration point for uTLS.
	TLSDial func(ctx context.Context, network, addr string, sni string) (net.Conn, error)
}

// Dial opens a WSS session to addr (host:port).
func Dial(ctx context.Context, addr string, cfg DialConfig) (transport.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("wsstr: split host:port: %w", err)
	}
	sni := cfg.SNI
	if sni == "" {
		sni = host
	}
	path := cfg.Path
	if path == "" {
		path = DefaultPath
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			ForceAttemptHTTP2: false,
			DialTLSContext: func(ctx context.Context, network, dialAddr string) (net.Conn, error) {
				if cfg.TLSDial != nil {
					return cfg.TLSDial(ctx, network, dialAddr, sni)
				}
				return tlsDial(ctx, network, dialAddr, sni, cfg.InsecureSkipVerify)
			},
		},
	}

	u := &url.URL{
		Scheme: "wss",
		Host:   addr,
		Path:   path,
	}

	c, resp, err := websocket.Dial(dialCtx, u.String(), &websocket.DialOptions{
		HTTPClient: httpClient,
		// Mimic a browser-ish Origin to look like a normal upgrade.
		HTTPHeader: http.Header{
			"Origin":     []string{"https://" + sni},
			"User-Agent": []string{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"},
		},
	})
	// coder/websocket guarantees resp.Body is http.NoBody after a
	// successful Dial; closing it is a no-op but keeps bodyclose
	// happy and removes a real footgun on error paths.
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("wsstr: ws dial: %w", err)
	}
	netConn := websocket.NetConn(context.Background(), c, websocket.MessageBinary)
	tcpRemote, _ := net.ResolveTCPAddr("tcp", addr)
	return &wssConn{
		netConn: &addrOverrideConn{Conn: netConn, remote: tcpRemote},
		wsConn:  c,
		closer: func() error {
			_ = c.Close(websocket.StatusNormalClosure, "")
			return nil
		},
	}, nil
}

// tlsDial is the default DialTLS used by Dial when the caller did
// not supply a custom TLSDial (uTLS). It uses the standard
// crypto/tls library.
func tlsDial(ctx context.Context, network, addr, sni string, insecure bool) (net.Conn, error) {
	d := &net.Dialer{Timeout: dialTimeout}
	tcp, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return nil, fmt.Errorf("wsstr: tcp dial: %w", err)
	}
	tlsConn := tls.Client(tcp, &tls.Config{
		ServerName:         sni,
		InsecureSkipVerify: insecure,
		MinVersion:         tls.VersionTLS12,
		NextProtos:         []string{"http/1.1"},
	})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = tcp.Close()
		return nil, fmt.Errorf("wsstr: tls handshake: %w", err)
	}
	return tlsConn, nil
}

// SelfSignedTLSConfig returns a server-side TLS config bearing a
// freshly-generated self-signed ECDSA P-256 certificate. Suitable
// for development; production deployments should use ACME-issued
// certificates and supply them via ListenConfig.TLS.
func SelfSignedTLSConfig(host string) (*tls.Config, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("wsstr: ecdsa generate: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		DNSNames:     []string{host, "localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		return nil, fmt.Errorf("wsstr: x509 create: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("wsstr: marshal ec key: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("wsstr: x509 keypair: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"http/1.1"},
	}, nil
}

// LoadTLSConfig builds a TLS config from disk-resident certificate
// and key files. The combination is intended for production: the
// certificate must match the SNI clients send (or be a wildcard),
// and the key file must be readable only by the running user.
func LoadTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("wsstr: load keypair: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"http/1.1"},
	}, nil
}

// ensure interface satisfaction.
var _ io.ReadWriteCloser = (*wssConn)(nil)
