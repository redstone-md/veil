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
	"sync/atomic"
	"time"

	"github.com/redstone-md/veil/core/internal/frame"
	"github.com/redstone-md/veil/core/internal/session"
)

// SOCKS5 protocol constants (RFC 1928).
const (
	socks5Version    = 0x05
	socks5MethodNone = 0x00
	socks5MethodFail = 0xFF

	socks5CmdConnect   = 0x01
	socks5CmdUDPAssoc  = 0x03

	socks5ATYPIPv4   = 0x01
	socks5ATYPDomain = 0x03
	socks5ATYPIPv6   = 0x04

	socks5RepSuccess        = 0x00
	socks5RepGeneralFailure = 0x01
	socks5RepConnRefused    = 0x05
	socks5RepATYPNotSupp    = 0x08
	socks5RepCmdNotSupp     = 0x07
)

// udpFlowIdleTimeout closes a per-(client,dst) Datagram stream after
// this long without traffic. Discord voice keeps RTP flowing every
// ~20 ms, so an idle flow really is dead.
const udpFlowIdleTimeout = 90 * time.Second

// negotiationTimeout caps the time spent on the SOCKS5 handshake
// before the connection is dropped.
const negotiationTimeout = 30 * time.Second

// ByteCounter is invoked by the SOCKS5 server's per-flow pipe with
// the number of bytes shuttled in each direction. nil is allowed and
// means "don't account."
type ByteCounter func(tx, rx int64)

// SOCKS5Server accepts SOCKS5 client connections and forwards each
// CONNECT request through a Veil session. The session pointer is
// held atomically so the embedding client can hot-swap the session
// across reconnects without unbinding the listener — open streams
// die with the old session, new accepts pick up the new one.
type SOCKS5Server struct {
	sess    atomic.Pointer[session.Session]
	logger  *slog.Logger
	counter ByteCounter
}

// NewSOCKS5 returns a server that maps incoming SOCKS5 CONNECTs onto
// streams of sess.
func NewSOCKS5(sess *session.Session, logger *slog.Logger) *SOCKS5Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &SOCKS5Server{logger: logger}
	s.sess.Store(sess)
	return s
}

// SetSession swaps the active session under an atomic pointer so a
// reconnect loop can install a fresh session without rebinding the
// listener. New accepts use the new session immediately; in-flight
// streams continue against the old one until it errors out.
func (s *SOCKS5Server) SetSession(sess *session.Session) {
	s.sess.Store(sess)
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

	cmd, target, err := socks5Negotiate(c)
	if err != nil {
		s.logger.Debug("socks5 negotiate", "err", err, "client", c.RemoteAddr().String())
		return
	}
	_ = c.SetDeadline(time.Time{})

	switch cmd {
	case socks5CmdConnect:
		s.handleConnect(ctx, c, target)
	case socks5CmdUDPAssoc:
		s.handleUDPAssociate(ctx, c)
	default:
		_ = writeSocksReply(c, socks5RepCmdNotSupp)
	}
}

func (s *SOCKS5Server) handleConnect(ctx context.Context, c net.Conn, target frame.Address) {
	sess := s.sess.Load()
	if sess == nil {
		_ = writeSocksReply(c, socks5RepGeneralFailure)
		return
	}
	st, err := sess.OpenStream(ctx, target)
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

// handleUDPAssociate services a SOCKS5 UDP ASSOCIATE (cmd 0x03)
// request. Binds an ephemeral UDP relay socket on 127.0.0.1, replies
// with its address, and tunnels every received datagram through the
// Veil session as a Datagram stream (one stream per unique target
// addr). The TCP control connection MUST stay open; when it closes
// we tear the UDP relay and all flow streams down.
func (s *SOCKS5Server) handleUDPAssociate(ctx context.Context, c net.Conn) {
	relay, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		s.logger.Warn("socks5 udp listen", "err", err)
		_ = writeSocksReply(c, socks5RepGeneralFailure)
		return
	}
	defer relay.Close()

	bound := relay.LocalAddr().(*net.UDPAddr)
	if err := writeSocksReplyAddr(c, socks5RepSuccess, bound.IP, uint16(bound.Port)); err != nil {
		return
	}
	s.logger.Info("socks5 udp relay", "bound", bound.String())

	sess := s.sess.Load()
	if sess == nil {
		return
	}
	flows := newUDPFlowMux(ctx, sess, relay, s.counter, s.logger)
	defer flows.close()

	// Tear UDP down when TCP control conn closes. RFC 1928 §6.
	tcpDone := make(chan struct{})
	go func() {
		defer close(tcpDone)
		buf := make([]byte, 1)
		for {
			if _, err := c.Read(buf); err != nil {
				return
			}
		}
	}()

	// Read SOCKS5 UDP packets from the relay socket. Each carries
	// the actual destination addr+port + payload.
	buf := make([]byte, 64*1024)
	for {
		_ = relay.SetReadDeadline(time.Now().Add(udpFlowIdleTimeout))
		n, src, err := relay.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				select {
				case <-tcpDone:
					return
				case <-ctx.Done():
					return
				default:
					continue
				}
			}
			return
		}
		dst, payload, perr := decodeSocksUDPPacket(buf[:n])
		if perr != nil {
			s.logger.Debug("socks5 udp parse", "err", perr)
			continue
		}
		flows.send(src, dst, payload)
		select {
		case <-tcpDone:
			return
		case <-ctx.Done():
			return
		default:
		}
	}
}

// writeSocksReplyAddr is the variant of writeSocksReply that fills
// the BND.ADDR/BND.PORT fields with the relay socket's actual bound
// address — required by UDP ASSOCIATE so the client knows where to
// send its datagrams.
func writeSocksReplyAddr(c net.Conn, rep byte, ip net.IP, port uint16) error {
	v4 := ip.To4()
	var atyp byte
	var addrBytes []byte
	if v4 != nil {
		atyp = socks5ATYPIPv4
		addrBytes = v4
	} else {
		atyp = socks5ATYPIPv6
		addrBytes = ip.To16()
	}
	out := make([]byte, 0, 4+len(addrBytes)+2)
	out = append(out, socks5Version, rep, 0x00, atyp)
	out = append(out, addrBytes...)
	out = binary.BigEndian.AppendUint16(out, port)
	_, err := c.Write(out)
	return err
}

// decodeSocksUDPPacket parses the SOCKS5 UDP datagram framing.
//   +----+------+------+----------+----------+----------+
//   |RSV | FRAG | ATYP | DST.ADDR | DST.PORT |   DATA   |
//   +----+------+------+----------+----------+----------+
//   | 2  |   1  |   1  | Variable |     2    | Variable |
//   +----+------+------+----------+----------+----------+
// We don't support fragmentation (FRAG must be 0).
func decodeSocksUDPPacket(b []byte) (frame.Address, []byte, error) {
	if len(b) < 4 {
		return frame.Address{}, nil, errors.New("udp packet too short")
	}
	if b[2] != 0 {
		return frame.Address{}, nil, fmt.Errorf("udp fragmentation unsupported: frag=%d", b[2])
	}
	off := 4
	var addr frame.Address
	switch b[3] {
	case socks5ATYPIPv4:
		if len(b) < off+4+2 {
			return frame.Address{}, nil, errors.New("ipv4 truncated")
		}
		addr.IP = net.IP(b[off : off+4])
		addr.Port = binary.BigEndian.Uint16(b[off+4 : off+6])
		off += 6
	case socks5ATYPIPv6:
		if len(b) < off+16+2 {
			return frame.Address{}, nil, errors.New("ipv6 truncated")
		}
		addr.IP = net.IP(b[off : off+16])
		addr.Port = binary.BigEndian.Uint16(b[off+16 : off+18])
		off += 18
	case socks5ATYPDomain:
		if len(b) < off+1 {
			return frame.Address{}, nil, errors.New("domain len missing")
		}
		dlen := int(b[off])
		off++
		if len(b) < off+dlen+2 {
			return frame.Address{}, nil, errors.New("domain truncated")
		}
		addr.Host = string(b[off : off+dlen])
		addr.Port = binary.BigEndian.Uint16(b[off+dlen : off+dlen+2])
		off += dlen + 2
	default:
		return frame.Address{}, nil, fmt.Errorf("unknown atyp %#02x", b[3])
	}
	return addr, b[off:], nil
}

// encodeSocksUDPPacket builds the reverse SOCKS5 UDP framing for a
// reply from upstream → client. src is the upstream address that
// produced the datagram (Discord etc. expect it to match the dst
// the client originally sent to, so the relay echoes it back).
func encodeSocksUDPPacket(src frame.Address, payload []byte) ([]byte, error) {
	out := make([]byte, 0, 4+len(payload)+18)
	out = append(out, 0, 0, 0) // RSV(2) FRAG(1)
	if src.IP != nil && src.IP.To4() != nil {
		out = append(out, socks5ATYPIPv4)
		out = append(out, src.IP.To4()...)
	} else if src.IP != nil {
		out = append(out, socks5ATYPIPv6)
		out = append(out, src.IP.To16()...)
	} else if src.Host != "" {
		if len(src.Host) > 255 {
			return nil, errors.New("hostname too long")
		}
		out = append(out, socks5ATYPDomain, byte(len(src.Host)))
		out = append(out, src.Host...)
	} else {
		return nil, errors.New("empty source")
	}
	out = binary.BigEndian.AppendUint16(out, src.Port)
	out = append(out, payload...)
	return out, nil
}

// socks5Negotiate runs the SOCKS5 handshake and returns the requested
// command + target. The reply has not yet been sent: the caller is
// expected to send it once the upstream is established (or the UDP
// relay socket is bound).
func socks5Negotiate(c net.Conn) (byte, frame.Address, error) {
	// Method-selection request.
	header := make([]byte, 2)
	if _, err := io.ReadFull(c, header); err != nil {
		return 0, frame.Address{}, fmt.Errorf("read greeting: %w", err)
	}
	if header[0] != socks5Version {
		return 0, frame.Address{}, fmt.Errorf("unsupported version %#02x", header[0])
	}
	nmethods := int(header[1])
	if nmethods == 0 {
		return 0, frame.Address{}, errors.New("no methods offered")
	}
	methods := make([]byte, nmethods)
	if _, err := io.ReadFull(c, methods); err != nil {
		return 0, frame.Address{}, fmt.Errorf("read methods: %w", err)
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
		return 0, frame.Address{}, errors.New("no acceptable method")
	}
	if _, err := c.Write([]byte{socks5Version, socks5MethodNone}); err != nil {
		return 0, frame.Address{}, fmt.Errorf("write method choice: %w", err)
	}

	// Request.
	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil {
		return 0, frame.Address{}, fmt.Errorf("read request: %w", err)
	}
	if req[0] != socks5Version {
		return 0, frame.Address{}, fmt.Errorf("bad request version %#02x", req[0])
	}
	if req[1] != socks5CmdConnect && req[1] != socks5CmdUDPAssoc {
		_ = writeSocksReply(c, socks5RepCmdNotSupp)
		return 0, frame.Address{}, fmt.Errorf("unsupported command %#02x", req[1])
	}
	cmd := req[1]

	var addr frame.Address
	switch req[3] {
	case socks5ATYPIPv4:
		buf := make([]byte, 4+2)
		if _, err := io.ReadFull(c, buf); err != nil {
			return 0, frame.Address{}, fmt.Errorf("read ipv4: %w", err)
		}
		addr.IP = net.IP(buf[:4])
		addr.Port = binary.BigEndian.Uint16(buf[4:])
	case socks5ATYPIPv6:
		buf := make([]byte, 16+2)
		if _, err := io.ReadFull(c, buf); err != nil {
			return 0, frame.Address{}, fmt.Errorf("read ipv6: %w", err)
		}
		addr.IP = net.IP(buf[:16])
		addr.Port = binary.BigEndian.Uint16(buf[16:])
	case socks5ATYPDomain:
		lenByte := make([]byte, 1)
		if _, err := io.ReadFull(c, lenByte); err != nil {
			return 0, frame.Address{}, fmt.Errorf("read domain len: %w", err)
		}
		dlen := int(lenByte[0])
		buf := make([]byte, dlen+2)
		if _, err := io.ReadFull(c, buf); err != nil {
			return 0, frame.Address{}, fmt.Errorf("read domain: %w", err)
		}
		addr.Host = string(buf[:dlen])
		addr.Port = binary.BigEndian.Uint16(buf[dlen:])
	default:
		_ = writeSocksReply(c, socks5RepATYPNotSupp)
		return 0, frame.Address{}, fmt.Errorf("unsupported atyp %#02x", req[3])
	}
	return cmd, addr, nil
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
