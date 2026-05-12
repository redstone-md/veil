// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redstone-md/veil/core/internal/crypto"
	"github.com/redstone-md/veil/core/internal/frame"
)

// Role identifies which side of a session this peer is.
type Role int

// Role values are aliases of the corresponding handshake roles so
// callers can use the same constants throughout the stack.
const (
	RoleClient Role = Role(crypto.RoleInitiator)
	RoleServer Role = Role(crypto.RoleResponder)
)

// DefaultStreamRecvBuffer is the INITIAL per-stream receive window
// we advertise. Sized at 256 KiB so the typical browser fan-out
// (50-200 concurrent flows) doesn't accumulate hundreds of MiB of
// receiver-side buffer. Each Stream may grow its ring up to
// MaxStreamRecvBuffer on demand — see Stream.deliver's saturation
// detector — so single-stream high-bandwidth downloads (large file
// fetches, single torrent peer) still scale beyond this floor.
//
// Tradeoff context: at 50 ms RTT a 256 KiB window saturates a single
// flow at ~40 Mbit/s; the auto-grow path lifts that to ~160 Mbit/s
// once a flow demonstrates it can absorb the data. Multi-flow
// workloads (speedtest, browsers, Steam) aggregate across flows and
// hit line rate at the floor.
const DefaultStreamRecvBuffer = 1 << 18 // 256 KiB

// MaxStreamRecvBuffer caps the auto-grow ladder. Picked at 1 MiB
// because beyond that bufferbloat costs more (RTT inflation, CWND
// confusion) than the single-flow throughput buys back.
const MaxStreamRecvBuffer = 1 << 20 // 1 MiB

// streamDataChunk caps how many plaintext bytes one STREAM_DATA frame
// carries on the wire. Sized just under frame.MaxPayload (16383)
// minus header (12) and a 64-byte padding budget so an active
// shaper can still grow each frame to PadTarget without splitting.
//
// Bumped from 8 KiB → 14 KiB to halve the per-byte cost of the
// secure-channel encrypt + sendMu acquisition + writev syscall on
// long streams. io.Copy's default 32 KiB read fills two frames
// instead of four, and TCP-level throughput on the underlying
// transport scales accordingly.
const streamDataChunk = 14 * 1024

// Session is a multiplexed, encrypted, full-duplex pipe established
// between a Veil client and a Veil server.
//
// One Session owns one SecureChannel. Multiple Streams flow over the
// session concurrently; their open/close/data lifecycle is signalled
// with VWP/1 frames.
type Session struct {
	secure *SecureChannel
	role   Role
	logger *slog.Logger
	shaper Shaper

	streamsMu sync.Mutex
	streams   map[uint32]*Stream

	nextOutbound atomic.Uint32
	incoming     chan *Stream

	closeOnce sync.Once
	closeErr  error
	closed    chan struct{}
}

// Options configures a new Session.
type Options struct {
	// Role is RoleClient or RoleServer.
	Role Role
	// Logger receives session-level diagnostic events. If nil,
	// slog.Default() is used.
	Logger *slog.Logger
	// Shaper, when non-nil, is consulted by every outgoing
	// STREAM_DATA frame to apply the mimicry layer (padding + write
	// delay). nil disables shaping.
	Shaper Shaper
}

// Shaper is the subset of the mimicry interface the session needs.
// It is declared here as an interface (rather than importing the
// concrete type) to keep the session package free of a dependency
// on the dpi tree.
type Shaper interface {
	PadTarget(currentLen int) int
	NextDelay() time.Duration
}

// New constructs a Session over the given SecureChannel. The caller
// MUST start the dispatch loop with Run before opening or accepting
// streams.
func New(secure *SecureChannel, opts Options) *Session {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &Session{
		secure:   secure,
		role:     opts.Role,
		logger:   logger,
		shaper:   opts.Shaper,
		streams:  make(map[uint32]*Stream),
		incoming: make(chan *Stream, 32),
		closed:   make(chan struct{}),
	}
	switch opts.Role {
	case RoleClient:
		s.nextOutbound.Store(1) // odd
	case RoleServer:
		s.nextOutbound.Store(2) // even, non-zero
	}
	return s
}

// Run drives the session's frame dispatcher. It blocks until the
// underlying transport returns an error (including io.EOF on a
// graceful peer close) or the session is explicitly closed. The
// returned error is the cause; nil is returned on a clean close.
func (s *Session) Run() error {
	for {
		plain, err := s.secure.RecvFrame()
		if err != nil {
			s.shutdown(err)
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		f, _, err := frame.Decode(plain)
		if err != nil {
			s.shutdown(fmt.Errorf("decode frame: %w", err))
			return err
		}
		if err := s.dispatch(f); err != nil {
			s.shutdown(err)
			return err
		}
	}
}

func (s *Session) dispatch(f *frame.Frame) error {
	switch f.Type {
	case frame.TypeStreamOpen:
		return s.handleStreamOpen(f)
	case frame.TypeStreamData:
		return s.handleStreamData(f)
	case frame.TypeStreamClose:
		return s.handleStreamClose(f)
	case frame.TypePing:
		return s.handlePing(f)
	case frame.TypePong:
		// No-op until we wire RTT estimation.
		return nil
	case frame.TypeWindowUpdate:
		return s.handleWindowUpdate(f)
	case frame.TypeControl:
		// Capability / rekey ops land here. Phase 1 ignores them.
		return nil
	case frame.TypePaddingOnly:
		return nil
	default:
		return fmt.Errorf("unknown frame type %s", f.Type)
	}
}

func (s *Session) handleStreamOpen(f *frame.Frame) error {
	payload, err := frame.DecodeStreamOpen(f.Payload)
	if err != nil {
		return fmt.Errorf("decode stream open: %w", err)
	}
	if !s.isPeerInitiatedID(f.StreamID) {
		return fmt.Errorf("stream open with locally-owned id %d", f.StreamID)
	}
	st := s.newStream(f.StreamID, payload.Target, payload.StreamType, payload.InitialWindow)
	s.streamsMu.Lock()
	if _, dup := s.streams[f.StreamID]; dup {
		s.streamsMu.Unlock()
		return fmt.Errorf("duplicate stream id %d", f.StreamID)
	}
	s.streams[f.StreamID] = st
	s.streamsMu.Unlock()

	select {
	case s.incoming <- st:
	case <-s.closed:
		return errors.New("session closed")
	}
	return nil
}

func (s *Session) handleStreamData(f *frame.Frame) error {
	st := s.lookup(f.StreamID)
	if st == nil {
		// Peer sent data on a stream we have already torn down.
		// Best-effort: ignore. A strict implementation could send
		// STREAM_CLOSE to remind the peer.
		return nil
	}
	st.deliver(f.Payload, f.Flags&frame.FlagEndStream != 0)
	return nil
}

func (s *Session) handleStreamClose(f *frame.Frame) error {
	st := s.lookup(f.StreamID)
	if st == nil {
		return nil
	}
	st.deliver(nil, true)
	return nil
}

func (s *Session) handleWindowUpdate(f *frame.Frame) error {
	p, err := frame.DecodeWindowUpdate(f.Payload)
	if err != nil {
		return fmt.Errorf("decode window update: %w", err)
	}
	st := s.lookup(f.StreamID)
	if st == nil {
		// Stream already torn down — drop silently.
		return nil
	}
	st.addCredit(p.Increment)
	return nil
}

func (s *Session) handlePing(f *frame.Frame) error {
	pong := &frame.Frame{
		Type:     frame.TypePong,
		StreamID: 0,
		Payload:  append([]byte(nil), f.Payload...),
	}
	encoded, err := pong.Encode()
	if err != nil {
		return err
	}
	return s.secure.SendFrame(encoded)
}

// OpenStream initiates a new reliable (TCP-like) stream toward target.
// For UDP relay use OpenStreamWithType with StreamTypeDatagram.
func (s *Session) OpenStream(ctx context.Context, target frame.Address) (*Stream, error) {
	return s.OpenStreamWithType(ctx, target, frame.StreamTypeReliable)
}

// OpenStreamWithType initiates a stream of the given substrate kind.
// Reliable streams behave as ordered byte pipes (TCP CONNECT). Datagram
// streams carry len-prefixed UDP datagrams that the remote forward
// server relays via net.DialUDP.
func (s *Session) OpenStreamWithType(ctx context.Context, target frame.Address, streamType frame.StreamType) (*Stream, error) {
	return s.OpenStreamFull(ctx, target, streamType, DefaultStreamRecvBuffer)
}

// OpenStreamFull is the variant that lets the caller (or test) pick
// a specific initial flow-control window. Production callers should
// use OpenStream / OpenStreamWithType which default to
// DefaultStreamRecvBuffer.
func (s *Session) OpenStreamFull(ctx context.Context, target frame.Address, streamType frame.StreamType, initialWindow uint32) (*Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if streamType == 0 {
		streamType = frame.StreamTypeReliable
	}
	if initialWindow == 0 {
		initialWindow = DefaultStreamRecvBuffer
	}
	id := s.allocateOutboundID()

	open := &frame.StreamOpenPayload{
		StreamType:    streamType,
		InitialWindow: initialWindow,
		Target:        target,
	}
	openPayload, err := open.Encode()
	if err != nil {
		return nil, err
	}
	st := s.newStream(id, target, streamType, initialWindow)

	s.streamsMu.Lock()
	s.streams[id] = st
	s.streamsMu.Unlock()

	f := &frame.Frame{
		Type:     frame.TypeStreamOpen,
		StreamID: id,
		Payload:  openPayload,
	}
	encoded, err := f.Encode()
	if err != nil {
		s.removeStream(id)
		return nil, err
	}
	if err := s.secure.SendFrame(encoded); err != nil {
		s.removeStream(id)
		return nil, fmt.Errorf("send stream open: %w", err)
	}
	return st, nil
}

// AcceptStream blocks until the peer opens a new stream toward this
// session, or until ctx is cancelled, or the session is closed.
func (s *Session) AcceptStream(ctx context.Context) (*Stream, error) {
	select {
	case st := <-s.incoming:
		return st, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.closed:
		if s.closeErr != nil {
			return nil, s.closeErr
		}
		return nil, io.EOF
	}
}

// Close shuts the session down: tears down all live streams, closes
// the underlying secure channel, and unblocks any pending Accept.
func (s *Session) Close() error {
	s.shutdown(nil)
	return s.secure.Close()
}

func (s *Session) shutdown(cause error) {
	s.closeOnce.Do(func() {
		s.closeErr = cause
		close(s.closed)
		s.streamsMu.Lock()
		live := make([]*Stream, 0, len(s.streams))
		for _, st := range s.streams {
			live = append(live, st)
		}
		s.streamsMu.Unlock()
		for _, st := range live {
			st.abort(cause)
		}
	})
}

func (s *Session) lookup(id uint32) *Stream {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	return s.streams[id]
}

func (s *Session) removeStream(id uint32) {
	s.streamsMu.Lock()
	delete(s.streams, id)
	s.streamsMu.Unlock()
}

func (s *Session) allocateOutboundID() uint32 {
	// Streams are spaced by two so client (odd) and server (even)
	// allocators never collide. ID 0 is reserved for session-scope
	// frames.
	return s.nextOutbound.Add(2) - 2
}

func (s *Session) isPeerInitiatedID(id uint32) bool {
	if id == 0 {
		return false
	}
	switch s.role {
	case RoleClient:
		return id%2 == 0 // server-allocated IDs are even
	case RoleServer:
		return id%2 == 1 // client-allocated IDs are odd
	}
	return false
}

func (s *Session) newStream(id uint32, target frame.Address, streamType frame.StreamType, initialWindow uint32) *Stream {
	if streamType == 0 {
		streamType = frame.StreamTypeReliable
	}
	if initialWindow == 0 {
		initialWindow = DefaultStreamRecvBuffer
	}
	st := &Stream{
		id:         id,
		sess:       s,
		target:     target,
		streamType: streamType,
		rxRing:     newRingBuf(int(initialWindow)),
		rxWindow:   initialWindow,
		txCredit:   int64(initialWindow),
	}
	st.rxCond = sync.NewCond(&st.rxMu)
	st.txCreditCond = sync.NewCond(&st.txMu)
	return st
}
