// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package session

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"

	"github.com/redstone-md/veil/core/internal/frame"
)

// Stream is one end of a logical bidirectional byte channel
// multiplexed onto a Session.
//
// Read returns io.EOF once the peer has closed its send side and the
// receive buffer has drained. Write returns ErrStreamClosed once the
// local close-on-send side has fired.
type Stream struct {
	id     uint32
	sess   *Session
	target frame.Address

	rxMu     sync.Mutex
	rxCond   *sync.Cond
	rxBuf    []byte // ring would be nicer; this works fine for v0
	rxFin    bool   // peer has signalled END_STREAM / STREAM_CLOSE
	rxErr    error
	rxClosed bool // local consumer abandoned the read side

	txClosed atomic.Bool
}

// ErrStreamClosed indicates an operation was attempted on a stream
// whose local side has already been closed.
var ErrStreamClosed = errors.New("stream: closed")

// ID returns the stream identifier (odd: client-initiated; even
// non-zero: server-initiated).
func (s *Stream) ID() uint32 { return s.id }

// Target returns the destination address advertised when the stream
// was opened. For locally-opened streams this is the address the
// caller passed to Session.OpenStream; for accepted streams this is
// the address received in the peer's STREAM_OPEN.
func (s *Stream) Target() frame.Address { return s.target }

// Read fills p with received bytes, blocking until at least one byte
// is available, the peer signals end-of-stream, or the stream is
// torn down by an error. Read is single-consumer: a stream MUST NOT
// be read from concurrently from multiple goroutines.
func (s *Stream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.rxMu.Lock()
	defer s.rxMu.Unlock()
	for {
		if s.rxClosed {
			return 0, ErrStreamClosed
		}
		if len(s.rxBuf) > 0 {
			n := copy(p, s.rxBuf)
			s.rxBuf = s.rxBuf[n:]
			// Truncating the slice but holding onto the
			// underlying array is fine for typical loads.
			s.rxCond.Broadcast()
			return n, nil
		}
		if s.rxFin {
			return 0, io.EOF
		}
		if s.rxErr != nil {
			return 0, s.rxErr
		}
		s.rxCond.Wait()
	}
}

// Write packetises p into one or more STREAM_DATA frames and sends
// them in order over the session. Write is single-producer.
func (s *Stream) Write(p []byte) (int, error) {
	if s.txClosed.Load() {
		return 0, ErrStreamClosed
	}
	written := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > streamDataChunk {
			chunk = chunk[:streamDataChunk]
		}
		f := &frame.Frame{
			Type:     frame.TypeStreamData,
			StreamID: s.id,
			Payload:  chunk,
		}
		encoded, err := f.Encode()
		if err != nil {
			return written, err
		}
		if err := s.sess.secure.SendFrame(encoded); err != nil {
			return written, err
		}
		written += len(chunk)
		p = p[len(chunk):]
	}
	return written, nil
}

// Close signals end-of-stream to the peer and forbids further local
// writes. Reads may continue draining the receive buffer. The stream
// is fully removed from its session when both sides are closed and
// the buffer has been drained.
func (s *Stream) Close() error {
	if s.txClosed.Swap(true) {
		return nil
	}
	f := &frame.Frame{
		Type:     frame.TypeStreamClose,
		StreamID: s.id,
	}
	encoded, err := f.Encode()
	if err != nil {
		return err
	}
	if err := s.sess.secure.SendFrame(encoded); err != nil {
		return err
	}
	s.rxMu.Lock()
	allDone := s.rxFin
	s.rxMu.Unlock()
	if allDone {
		s.sess.removeStream(s.id)
	}
	return nil
}

// deliver appends payload to the stream's receive buffer and, if
// fin is true, marks the peer's send side as closed. Called by the
// session dispatcher.
func (s *Stream) deliver(payload []byte, fin bool) {
	s.rxMu.Lock()
	if s.rxClosed {
		// Consumer has gone; drop bytes.
		s.rxMu.Unlock()
		return
	}
	if len(payload) > 0 {
		s.rxBuf = append(s.rxBuf, payload...)
	}
	if fin {
		s.rxFin = true
	}
	s.rxCond.Broadcast()
	s.rxMu.Unlock()
	if fin && s.txClosed.Load() {
		s.sess.removeStream(s.id)
	}
}

// abort tears the stream down with err: subsequent Reads return err,
// further data is dropped. Called by the session on shutdown.
func (s *Stream) abort(err error) {
	if err == nil {
		err = io.ErrClosedPipe
	}
	s.rxMu.Lock()
	s.rxErr = err
	s.rxFin = true
	s.rxCond.Broadcast()
	s.rxMu.Unlock()
	s.txClosed.Store(true)
}
