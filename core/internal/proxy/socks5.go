// Package proxy implements local user-facing proxy protocols that
// translate application traffic into Veil sessions.
//
// The v0/v1 implementation supports SOCKS5 (RFC 1928) with the
// CONNECT method and no authentication. HTTP CONNECT and other
// methods are deferred to a later phase.
package proxy

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/redstone-md/veil/core/internal/frame"
	"github.com/redstone-md/veil/core/internal/session"
)

// SOCKS5 protocol constants (RFC 1928).
const (
	socks5Version    = 0x05
	socks5MethodNone = 0x00
	socks5MethodFail = 0xFF

	socks5CmdConnect = 0x01

	socks5ATYPIPv4   = 0x01
	socks5ATYPDomain = 0x03
	socks5ATYPIPv6   = 0x04

	socks5RepSuccess        = 0x00
	socks5RepGeneralFailure = 0x01
	socks5RepConnRefused    = 0x05
	socks5RepATYPNotSupp    = 0x08
	socks5RepCmdNotSupp     = 0x07
)

// negotiationTimeout caps the time spent on the SOCKS5 handshake
// before the connection is dropped.
const negotiationTimeout = 30 * time.Second

// ByteCounter is invoked by the SOCKS5 server's per-flow pipe with
// the number of bytes shuttled in each direction. nil is allowed and
// means "don't account."
type ByteCounter func(tx, rx int64)

// SOCKS5Server accepts SOCKS5 client connections and forwards each
// CONNECT request through a Veil session.
type SOCKS5Server struct {
	sess    *session.Session
	logger  *slog.Logger
	counter ByteCounter
}

// NewSOCKS5 returns a server that maps incoming SOCKS5 CONNECTs onto
// streams of sess.
func NewSOCKS5(sess *session.Session, logger *slog.Logger) *SOCKS5Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &SOCKS5Server{sess: sess, logger: logger}
}

// SetByteCounter installs a per-flow byte accounting callback. The
// counter is invoked once per byte direction with deltas after each
// pipe iteration, so an atomic adder on the receiver side is the
// natural shape.
func (s *SOCKS5Server) SetByteCounter(c ByteCounter) {
	s.counter = c
}

// ListenAndServe binds addr (e.g. "127.0.0.1:1080") and serves until
// ctx is cancelled or the underlying listener fails.
func (s *SOCKS5Server) ListenAndServe(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("socks5 listen: %w", err)
	}
	defer ln.Close()
	s.logger.Info("socks5 listening", "addr", ln.Addr().String())

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			s.logger.Warn("socks5 accept", "err", err)
			continue
		}
		go s.handle(ctx, conn)
	}
}

func (s *SOCKS5Server) handle(ctx context.Context, c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(negotiationTimeout))

	target, err := socks5Negotiate(c)
	if err != nil {
		s.logger.Debug("socks5 negotiate", "err", err, "client", c.RemoteAddr().String())
		return
	}
	_ = c.SetDeadline(time.Time{})

	st, err := s.sess.OpenStream(ctx, target)
	if err != nil {
		s.logger.Warn("session open stream", "err", err, "target", target.String())
		_ = writeSocksReply(c, socks5RepGeneralFailure)
		return
	}
	if err := writeSocksReply(c, socks5RepSuccess); err != nil {
		_ = st.Close()
		return
	}

	pipe(st, c, s.counter)
}

// socks5Negotiate runs the SOCKS5 handshake and returns the requested
// CONNECT target. The reply has not yet been sent: the caller is
// expected to send it once the upstream stream is established.
func socks5Negotiate(c net.Conn) (frame.Address, error) {
	// Method-selection request.
	header := make([]byte, 2)
	if _, err := io.ReadFull(c, header); err != nil {
		return frame.Address{}, fmt.Errorf("read greeting: %w", err)
	}
	if header[0] != socks5Version {
		return frame.Address{}, fmt.Errorf("unsupported version %#02x", header[0])
	}
	nmethods := int(header[1])
	if nmethods == 0 {
		return frame.Address{}, errors.New("no methods offered")
	}
	methods := make([]byte, nmethods)
	if _, err := io.ReadFull(c, methods); err != nil {
		return frame.Address{}, fmt.Errorf("read methods: %w", err)
	}
	supported := false
	for _, m := range methods {
		if m == socks5MethodNone {
			supported = true
			break
		}
	}
	if !supported {
		_, _ = c.Write([]byte{socks5Version, socks5MethodFail})
		return frame.Address{}, errors.New("no acceptable method")
	}
	if _, err := c.Write([]byte{socks5Version, socks5MethodNone}); err != nil {
		return frame.Address{}, fmt.Errorf("write method choice: %w", err)
	}

	// Request.
	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil {
		return frame.Address{}, fmt.Errorf("read request: %w", err)
	}
	if req[0] != socks5Version {
		return frame.Address{}, fmt.Errorf("bad request version %#02x", req[0])
	}
	if req[1] != socks5CmdConnect {
		_ = writeSocksReply(c, socks5RepCmdNotSupp)
		return frame.Address{}, fmt.Errorf("unsupported command %#02x", req[1])
	}

	var addr frame.Address
	switch req[3] {
	case socks5ATYPIPv4:
		buf := make([]byte, 4+2)
		if _, err := io.ReadFull(c, buf); err != nil {
			return frame.Address{}, fmt.Errorf("read ipv4: %w", err)
		}
		addr.IP = net.IP(buf[:4])
		addr.Port = binary.BigEndian.Uint16(buf[4:])
	case socks5ATYPIPv6:
		buf := make([]byte, 16+2)
		if _, err := io.ReadFull(c, buf); err != nil {
			return frame.Address{}, fmt.Errorf("read ipv6: %w", err)
		}
		addr.IP = net.IP(buf[:16])
		addr.Port = binary.BigEndian.Uint16(buf[16:])
	case socks5ATYPDomain:
		lenByte := make([]byte, 1)
		if _, err := io.ReadFull(c, lenByte); err != nil {
			return frame.Address{}, fmt.Errorf("read domain len: %w", err)
		}
		dlen := int(lenByte[0])
		buf := make([]byte, dlen+2)
		if _, err := io.ReadFull(c, buf); err != nil {
			return frame.Address{}, fmt.Errorf("read domain: %w", err)
		}
		addr.Host = string(buf[:dlen])
		addr.Port = binary.BigEndian.Uint16(buf[dlen:])
	default:
		_ = writeSocksReply(c, socks5RepATYPNotSupp)
		return frame.Address{}, fmt.Errorf("unsupported atyp %#02x", req[3])
	}
	return addr, nil
}

// writeSocksReply emits a SOCKS5 reply with rep code and the
// canonical "0.0.0.0:0" bound-address tuple. The bound address is
// not meaningful for our transport: clients use it informationally.
func writeSocksReply(c net.Conn, rep byte) error {
	reply := []byte{
		socks5Version,
		rep,
		0x00,           // reserved
		socks5ATYPIPv4, // bound atyp
		0, 0, 0, 0,     // bound addr
		0, 0, // bound port
	}
	_, err := c.Write(reply)
	return err
}

// pipe runs full-duplex io.Copy between a Veil stream and a TCP
// connection, optionally accounting for the bytes shuttled in each
// direction via counter (nil = no accounting).
func pipe(stream io.ReadWriteCloser, downstream net.Conn, counter ByteCounter) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		// downstream → stream is the client's outbound direction (TX).
		n, _ := io.Copy(stream, downstream)
		if counter != nil && n > 0 {
			counter(n, 0)
		}
		_ = stream.Close()
	}()
	go func() {
		defer wg.Done()
		// stream → downstream is the server's response (RX).
		n, _ := io.Copy(downstream, stream)
		if counter != nil && n > 0 {
			counter(0, n)
		}
		if tc, ok := downstream.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()
	wg.Wait()
}
