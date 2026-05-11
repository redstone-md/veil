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
	"time"

	"github.com/redstone-md/veil/core/internal/bufpool"
	"github.com/redstone-md/veil/core/internal/frame"
)

// Stream is one end of a logical bidirectional byte channel
// multiplexed onto a Session.
//
// Read returns io.EOF once the peer has closed its send side and the
// receive buffer has drained. Write returns ErrStreamClosed once the
// local close-on-send side has fired.
type Stream struct {
	id         uint32
	sess       *Session
	target     frame.Address
	streamType frame.StreamType // Reliable (TCP-like) or Datagram (UDP relay)

	// Receive side. The ring is bounded — its capacity is the flow-
	// control window we advertised to the peer at STREAM_OPEN time.
	// The peer is expected to respect the window: Write blocks on
	// txCreditCond until the receiver signals additional credit via
	// WINDOW_UPDATE.
	rxMu       sync.Mutex
	rxCond     *sync.Cond
	rxRing     *ringBuf
	rxFin      bool   // peer has signalled END_STREAM / STREAM_CLOSE
	rxErr      error
	rxClosed   bool   // local consumer abandoned the read side
	rxWindow   uint32 // window we advertised; same as ring capacity
	rxConsumed uint32 // bytes consumed since last WINDOW_UPDATE

	// Send side. txCredit tracks the bytes we may send before the
	// peer next bumps our window. Writes block on txCreditCond when
	// the next chunk would exceed remaining credit.
	txMu         sync.Mutex
	txCreditCond *sync.Cond
	txCredit     int64
	txClosed     atomic.Bool
}

// windowUpdateThreshold controls when the receiver tells the peer
// it has freed buffer space. Sending one WINDOW_UPDATE per byte
// would waste an entire frame for every read; sending only after
// half the window has been consumed batches updates and still keeps
// the sender's pipe full so long as RTT < half-window-drain-time.
const windowUpdateThreshold = 2 // emit when consumed > capacity / threshold

// Type reports whether the stream carries a reliable byte stream
// (TCP CONNECT semantics) or len-prefixed UDP datagrams. Forward
// servers branch on this to choose between net.DialTCP and a UDP
// relay.
func (s *Stream) Type() frame.StreamType { return s.streamType }

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
	for {
		if s.rxClosed {
			s.rxMu.Unlock()
			return 0, ErrStreamClosed
		}
		if s.rxRing.Len() > 0 {
			n := s.rxRing.Read(p)
			s.rxConsumed += uint32(n)
			// Wake any deliver() blocked on a full ring.
			s.rxCond.Broadcast()
			// Decide whether to emit a WINDOW_UPDATE.
			//
			// (a) Batch case: consumed >= half the window — sender
			//     can keep its pipe full without an extra round trip.
			// (b) Drain case: ring just hit empty AND consumed > 0
			//     — required for forward progress when the producer
			//     is blocked on credit smaller than streamDataChunk.
			//     Without this the producer can deadlock waiting for
			//     a credit grant that the consumer never decides to
			//     send because consumed < half-window.
			//
			// Capture the increment under the lock, drop the lock,
			// send the frame outside it — SendFrame may block on its
			// own mutex and we don't want to stall Reads.
			emit := uint32(0)
			if s.rxWindow > 0 && s.rxConsumed > 0 &&
				(s.rxConsumed >= s.rxWindow/windowUpdateThreshold || s.rxRing.Len() == 0) {
				emit = s.rxConsumed
				s.rxConsumed = 0
			}
			s.rxMu.Unlock()
			if emit > 0 {
				_ = s.sendWindowUpdate(emit)
			}
			return n, nil
		}
		if s.rxFin {
			s.rxMu.Unlock()
			return 0, io.EOF
		}
		if s.rxErr != nil {
			err := s.rxErr
			s.rxMu.Unlock()
			return 0, err
		}
		s.rxCond.Wait()
	}
}

// Write packetises p into one or more STREAM_DATA frames and sends
// them in order over the session. Write is single-producer.
//
// Respects the per-stream send credit: blocks until at least the
// next chunk's worth of credit is available. Credit is replenished
// by peer-emitted WINDOW_UPDATE frames.
//
// When the parent Session was constructed with a Shaper, every
// STREAM_DATA frame is padded up to the shaper's target plaintext
// size (PadTarget) before encryption, and the call sleeps for the
// shaper's NextDelay before issuing the underlying write.
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

		// Block on credit. We require at least len(chunk) bytes of
		// window before pulling them off the queue.
		s.txMu.Lock()
		for s.txCredit < int64(len(chunk)) {
			if s.txClosed.Load() {
				s.txMu.Unlock()
				return written, ErrStreamClosed
			}
			s.txCreditCond.Wait()
		}
		s.txCredit -= int64(len(chunk))
		s.txMu.Unlock()

		f := frame.Frame{
			Type:     frame.TypeStreamData,
			StreamID: s.id,
			Payload:  chunk,
		}
		if shaper := s.sess.shaper; shaper != nil {
			target := shaper.PadTarget(len(chunk))
			if pad := target - len(chunk); pad > 0 && pad <= frame.MaxPadding {
				f.PaddingLen = uint16(pad)
			}
		}
		// Encode into a pooled buffer instead of letting f.Encode()
		// allocate a fresh slice every chunk. AppendEncoded writes
		// in-place, so the pool covers the steady-state hot path.
		buf := bufpool.Get(f.EncodedLen())[:0]
		buf, err := f.AppendEncoded(buf)
		if err != nil {
			bufpool.Put(buf)
			return written, err
		}
		if shaper := s.sess.shaper; shaper != nil {
			if d := shaper.NextDelay(); d > 0 {
				time.Sleep(d)
			}
		}
		if err := s.sess.secure.SendFrame(buf); err != nil {
			bufpool.Put(buf)
			return written, err
		}
		bufpool.Put(buf)
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
	// Wake any Write blocked waiting for credit so it returns
	// ErrStreamClosed instead of leaking forever.
	s.txMu.Lock()
	if s.txCreditCond != nil {
		s.txCreditCond.Broadcast()
	}
	s.txMu.Unlock()

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
//
// The peer is expected to honour our advertised window so the ring
// should never overflow — but if it does (peer misbehaving or
// pre-window-update inflight), we block until Read frees room. The
// dispatcher waits for us, which provides natural TCP-level back-
// pressure all the way back to the sender.
func (s *Stream) deliver(payload []byte, fin bool) {
	s.rxMu.Lock()
	if s.rxClosed {
		s.rxMu.Unlock()
		return
	}
	for len(payload) > 0 && !s.rxClosed {
		n := s.rxRing.Write(payload)
		payload = payload[n:]
		if n > 0 {
			s.rxCond.Broadcast() // wake Read
		}
		if len(payload) > 0 {
			s.rxCond.Wait() // wait for Read to free space
			if s.rxClosed {
				break
			}
		}
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

// addCredit is invoked by Session.handleWindowUpdate when the peer
// frees buffer space and tells us we may send more.
func (s *Stream) addCredit(inc uint32) {
	s.txMu.Lock()
	s.txCredit += int64(inc)
	if s.txCreditCond != nil {
		s.txCreditCond.Broadcast()
	}
	s.txMu.Unlock()
}

// sendWindowUpdate emits a WINDOW_UPDATE frame back to the peer
// telling it how much receive-buffer space we have freed. Called
// from Read after a half-window's worth of bytes have been consumed.
func (s *Stream) sendWindowUpdate(inc uint32) error {
	p := &frame.WindowUpdatePayload{Increment: inc}
	f := &frame.Frame{
		Type:     frame.TypeWindowUpdate,
		StreamID: s.id,
		Payload:  p.Encode(),
	}
	encoded, err := f.Encode()
	if err != nil {
		return err
	}
	return s.sess.secure.SendFrame(encoded)
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
	s.txMu.Lock()
	if s.txCreditCond != nil {
		s.txCreditCond.Broadcast()
	}
	s.txMu.Unlock()
}
