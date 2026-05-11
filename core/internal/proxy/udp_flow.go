// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package proxy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/redstone-md/veil/core/internal/frame"
	"github.com/redstone-md/veil/core/internal/session"
)

// udpFlowMux multiplexes SOCKS5 UDP packets onto per-(client,dst)
// Datagram streams.
//
// One stream per unique (src_addr, dst_addr) pair. The src_addr lets
// us route reverse-direction datagrams back to the right client when
// multiple apps share the SOCKS5 listener. Streams stay open until
// the TCP control conn closes or udpFlowIdleTimeout elapses without
// traffic.
type udpFlowMux struct {
	ctx     context.Context
	sess    *session.Session
	relay   *net.UDPConn
	counter ByteCounter
	logger  *slog.Logger

	mu    sync.Mutex
	flows map[string]*udpFlow
}

type udpFlow struct {
	src    *net.UDPAddr
	dst    frame.Address
	stream *session.Stream
	last   time.Time
	closed chan struct{}
}

func newUDPFlowMux(ctx context.Context, sess *session.Session, relay *net.UDPConn, counter ByteCounter, logger *slog.Logger) *udpFlowMux {
	return &udpFlowMux{
		ctx: ctx, sess: sess, relay: relay,
		counter: counter, logger: logger,
		flows: make(map[string]*udpFlow),
	}
}

// send forwards one client→upstream datagram. Opens a stream lazily
// the first time it sees a new (src,dst) pair.
func (m *udpFlowMux) send(src *net.UDPAddr, dst frame.Address, payload []byte) {
	key := src.String() + "|" + dst.String()
	m.mu.Lock()
	f, ok := m.flows[key]
	m.mu.Unlock()

	if !ok {
		st, err := m.sess.OpenStreamWithType(m.ctx, dst, frame.StreamTypeDatagram)
		if err != nil {
			m.logger.Debug("udp open stream", "err", err, "dst", dst.String())
			return
		}
		f = &udpFlow{
			src: src, dst: dst, stream: st,
			last: time.Now(), closed: make(chan struct{}),
		}
		m.mu.Lock()
		// Re-check under lock in case a parallel send raced us.
		if existing, dup := m.flows[key]; dup {
			m.mu.Unlock()
			_ = st.Close()
			f = existing
		} else {
			m.flows[key] = f
			m.mu.Unlock()
			go m.readLoop(key, f)
		}
	}

	f.last = time.Now()
	if err := session.WriteDatagram(f.stream, payload); err != nil {
		m.logger.Debug("udp write datagram", "err", err)
		m.drop(key)
		return
	}
	if m.counter != nil {
		m.counter(int64(len(payload)), 0)
	}
}

// readLoop pumps upstream → client for one flow. Wraps each datagram
// in the SOCKS5 UDP framing with src=dst so the client app sees the
// reply as if it came directly from the destination it sent to.
func (m *udpFlowMux) readLoop(key string, f *udpFlow) {
	defer m.drop(key)
	buf := make([]byte, session.MaxDatagramSize)
	for {
		n, err := session.ReadDatagram(f.stream, buf)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, session.ErrStreamClosed) {
				m.logger.Debug("udp read datagram", "err", err)
			}
			return
		}
		pkt, err := encodeSocksUDPPacket(f.dst, buf[:n])
		if err != nil {
			continue
		}
		if _, err := m.relay.WriteToUDP(pkt, f.src); err != nil {
			m.logger.Debug("udp relay write", "err", err)
			return
		}
		f.last = time.Now()
		if m.counter != nil {
			m.counter(0, int64(n))
		}
	}
}

func (m *udpFlowMux) drop(key string) {
	m.mu.Lock()
	f, ok := m.flows[key]
	if ok {
		delete(m.flows, key)
	}
	m.mu.Unlock()
	if ok {
		_ = f.stream.Close()
		select {
		case <-f.closed:
		default:
			close(f.closed)
		}
	}
}

// close tears down every active flow. Called when the SOCKS5 TCP
// control conn closes.
func (m *udpFlowMux) close() {
	m.mu.Lock()
	flows := m.flows
	m.flows = make(map[string]*udpFlow)
	m.mu.Unlock()
	for _, f := range flows {
		_ = f.stream.Close()
		select {
		case <-f.closed:
		default:
			close(f.closed)
		}
	}
}
