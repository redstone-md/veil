// Package client implements the embeddable Veil client: dial a
// configured server, run the Noise XK initiator handshake, expose a
// local SOCKS5 proxy, and (optionally) generate cover traffic.
//
// Both the `veil connect` CLI and the C-API (libveil) live on top
// of this package. Keeping the orchestration here means the two
// front ends cannot drift in behaviour.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redstone-md/veil/core/internal/config"
	"github.com/redstone-md/veil/core/internal/crypto"
	"github.com/redstone-md/veil/core/internal/dpi/decoy"
	"github.com/redstone-md/veil/core/internal/dpi/snipool"
	"github.com/redstone-md/veil/core/internal/dpi/utlsdial"
	"github.com/redstone-md/veil/core/internal/proxy"
	"github.com/redstone-md/veil/core/internal/session"
	"github.com/redstone-md/veil/core/internal/transport"
	"github.com/redstone-md/veil/core/internal/transport/quictr"
	"github.com/redstone-md/veil/core/internal/transport/realitytr"
	"github.com/redstone-md/veil/core/internal/transport/wsstr"
)

// EventType identifies a runtime event reported through the Listener
// callback.
type EventType int

// Event types match the values surfaced by the C-API; do not
// renumber.
const (
	EventConnected       EventType = 1
	EventDisconnected    EventType = 2
	EventError           EventType = 3
	EventTraffic         EventType = 4
	EventTransportSwitch EventType = 5
)

// Event is the structured payload delivered to a Listener. It is
// JSON-marshallable so the C-API and SDKs can pass it across FFI as
// a single string.
type Event struct {
	Type      EventType `json:"type"`
	Message   string    `json:"message,omitempty"`
	Transport string    `json:"transport,omitempty"`
	Remote    string    `json:"remote,omitempty"`
	BytesTx   int64     `json:"bytes_tx,omitempty"`
	BytesRx   int64     `json:"bytes_rx,omitempty"`
}

// Listener receives runtime events.
type Listener interface {
	OnEvent(Event)
}

// ListenerFunc adapts a function into a Listener.
type ListenerFunc func(Event)

// OnEvent satisfies Listener.
func (f ListenerFunc) OnEvent(e Event) { f(e) }

// Client is the runnable end of the embeddable client.
type Client struct {
	cfg      *config.ClientConfig
	logger   *slog.Logger
	listener Listener

	mu      sync.Mutex
	cancel  context.CancelFunc
	running atomic.Bool

	bytesTx atomic.Int64
	bytesRx atomic.Int64
}

// New constructs a Client. cfg MUST already have been validated.
// listener is optional; pass nil for "no events".
func New(cfg *config.ClientConfig, logger *slog.Logger, listener Listener) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{cfg: cfg, logger: logger, listener: listener}
}

// Run brings the client up and blocks until ctx is cancelled or any
// subsystem terminates.
func (c *Client) Run(ctx context.Context) error {
	if !c.running.CompareAndSwap(false, true) {
		return errors.New("client: already running")
	}
	defer c.running.Store(false)

	ctx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()
	defer cancel()

	socksAddr := c.cfg.SOCKS5Listen
	if socksAddr == "" {
		socksAddr = "127.0.0.1:1080"
	}

	staticKP, err := crypto.LoadOrCreateKeypair(c.cfg.StaticKeyPath)
	if err != nil {
		return c.fail("load static key", err)
	}
	c.logger.Info("client static key ready",
		"public_key_b64", crypto.EncodePublicKey(staticKP.Public),
	)

	serverPub, err := crypto.DecodePublicKey(c.cfg.ServerStaticKeyB64)
	if err != nil {
		return c.fail("server static key", err)
	}

	fb := transport.NewFallback(c.logger)
	for i, s := range c.cfg.Servers {
		d, err := buildDialer(s, serverPub)
		if err != nil {
			return c.fail(fmt.Sprintf("client.servers[%d]", i), err)
		}
		fb.Add(string(s.Type), s.Addr, d)
	}

	conn, label, err := fb.Dial(ctx)
	if err != nil {
		return c.fail("transport dial", err)
	}
	defer conn.Close()
	c.emit(Event{
		Type: EventTransportSwitch, Transport: label,
		Remote: conn.RemoteAddr().String(),
	})
	c.logger.Info("transport connected",
		"transport", label, "remote", conn.RemoteAddr().String())

	established, err := session.HandshakeAsInitiator(conn, *staticKP, serverPub)
	if err != nil {
		return c.fail("handshake", err)
	}
	c.logger.Info("session established")
	c.emit(Event{
		Type: EventConnected, Transport: label,
		Remote: conn.RemoteAddr().String(),
	})

	secure := session.NewSecureChannel(conn, established)
	sess := session.New(secure, session.Options{Role: session.RoleClient, Logger: c.logger})

	runErr := make(chan error, 1)
	go func() { runErr <- sess.Run() }()

	socksLogger := c.logger
	socksProxy := proxy.NewSOCKS5(sess, socksLogger)
	socksErr := make(chan error, 1)
	go func() { socksErr <- socksProxy.ListenAndServe(ctx, socksAddr) }()

	if c.cfg.Decoy.Enabled {
		c.startDecoy(ctx, staticKP.Public)
	}

	c.logger.Info("ready", "socks5", socksAddr, "transport", label)

	// Periodic traffic event so SDK consumers can poll-free observe
	// throughput. Cheap; no allocation in the steady state.
	tickerStop := make(chan struct{})
	go c.trafficTicker(ctx, tickerStop)

	defer func() {
		close(tickerStop)
		_ = sess.Close()
		c.emit(Event{Type: EventDisconnected, Transport: label})
	}()

	select {
	case err := <-runErr:
		if err != nil {
			return c.fail("session", err)
		}
		return nil
	case err := <-socksErr:
		if err != nil {
			return c.fail("socks5", err)
		}
		return nil
	case <-ctx.Done():
		return nil
	}
}

// Stop signals a running Client to terminate. It is safe to call
// concurrently with Run, including before Run starts (no-op).
func (c *Client) Stop() {
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// MetricsJSON returns a snapshot of the client's current metrics
// in JSON. Suitable for exposing across the C-API.
func (c *Client) MetricsJSON() string {
	snap := struct {
		Running bool  `json:"running"`
		BytesTx int64 `json:"bytes_tx"`
		BytesRx int64 `json:"bytes_rx"`
	}{
		Running: c.running.Load(),
		BytesTx: c.bytesTx.Load(),
		BytesRx: c.bytesRx.Load(),
	}
	b, _ := json.Marshal(snap)
	return string(b)
}

func (c *Client) emit(e Event) {
	if c.listener == nil {
		return
	}
	c.listener.OnEvent(e)
}

func (c *Client) fail(stage string, err error) error {
	wrapped := fmt.Errorf("%s: %w", stage, err)
	c.emit(Event{Type: EventError, Message: wrapped.Error()})
	return wrapped
}

func (c *Client) trafficTicker(ctx context.Context, stop chan struct{}) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			c.emit(Event{
				Type:    EventTraffic,
				BytesTx: c.bytesTx.Load(),
				BytesRx: c.bytesRx.Load(),
			})
		case <-ctx.Done():
			return
		case <-stop:
			return
		}
	}
}

func (c *Client) startDecoy(ctx context.Context, userKey []byte) {
	dc := c.cfg.Decoy
	pool := snipool.New()
	region := snipool.Region(dc.Region)
	fp := utlsdial.Fingerprint(dc.Fingerprint)
	if fp == "" {
		fp = utlsdial.FingerprintChromeAuto
	}
	eng := decoy.New(pool, decoy.Config{
		Region:      region,
		UserKey:     string(userKey),
		ShardSize:   dc.ShardSize,
		Concurrency: dc.Concurrency,
		IntervalMS:  dc.IntervalMS,
		Fingerprint: fp,
	}, c.logger)
	go func() {
		if err := eng.Run(ctx); err != nil {
			c.logger.Warn("decoy engine stopped", "err", err)
		}
	}()
}

func buildDialer(s config.ClientServer, serverStaticPub []byte) (transport.Dialer, error) {
	switch s.Type {
	case config.TransportQUIC:
		return quictr.NewDialer(), nil
	case config.TransportWSS:
		dc := wsstr.DialConfig{
			SNI:                s.SNI,
			Path:               s.Path,
			InsecureSkipVerify: s.InsecureSkipVerify(),
		}
		if s.Fingerprint != "off" {
			fp := utlsdial.FingerprintChromeAuto
			if s.Fingerprint != "" {
				fp = utlsdial.Fingerprint(s.Fingerprint)
			}
			insecure := s.InsecureSkipVerify()
			dc.TLSDial = func(ctx context.Context, network, addr, sni string) (net.Conn, error) {
				return utlsdial.Dial(ctx, network, addr, utlsdial.Options{
					Fingerprint:        fp,
					SNI:                sni,
					InsecureSkipVerify: insecure,
					NextProtos:         []string{"http/1.1"},
				})
			}
		}
		return wsstr.NewDialer(dc), nil
	case config.TransportReality:
		secret, err := realitytr.DeriveAuthSecret(serverStaticPub)
		if err != nil {
			return nil, err
		}
		return realitytr.NewDialer(realitytr.DialConfig{
			Secret: secret,
			SNI:    s.SNI,
		}), nil
	default:
		return nil, fmt.Errorf("unknown transport type %q", s.Type)
	}
}
