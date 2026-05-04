// Veil VPN
// Copyright 2026 Veil VPN Project Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");

package transport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
)

// FanInListener accepts from N underlying listeners and exposes a
// single Accept channel. It is the server-side glue that lets one
// session-handling code path serve every configured transport.
type FanInListener struct {
	logger *slog.Logger

	mu        sync.Mutex
	listeners []Listener
	closed    chan struct{}
	once      sync.Once

	accepted chan acceptResult
}

type acceptResult struct {
	conn Conn
	from string
	err  error
}

// NewFanIn returns an empty fan-in listener. Add backends with Add.
func NewFanIn(logger *slog.Logger) *FanInListener {
	if logger == nil {
		logger = slog.Default()
	}
	return &FanInListener{
		logger:   logger,
		closed:   make(chan struct{}),
		accepted: make(chan acceptResult, 32),
	}
}

// Add starts a goroutine that pulls from ln until ln returns an error
// (typically because Close was called) and forwards every connection
// to the fan-in channel. The label is used for logging only.
func (f *FanInListener) Add(label string, ln Listener) {
	f.mu.Lock()
	f.listeners = append(f.listeners, ln)
	f.mu.Unlock()

	go func() {
		for {
			conn, err := ln.Accept(context.Background())
			if err != nil {
				select {
				case f.accepted <- acceptResult{err: err, from: label}:
				case <-f.closed:
				}
				return
			}
			select {
			case f.accepted <- acceptResult{conn: conn, from: label}:
			case <-f.closed:
				_ = conn.Close()
				return
			}
		}
	}()
}

// Accept returns the next connection from any backend listener, or
// an error if ctx is cancelled or the fan-in is closed.
func (f *FanInListener) Accept(ctx context.Context) (Conn, error) {
	for {
		select {
		case r := <-f.accepted:
			if r.err != nil {
				// Per-backend errors are logged but not surfaced
				// unless every backend has died, which we detect
				// by the closed signal.
				f.logger.Warn("transport listener stopped",
					"transport", r.from, "err", r.err)
				continue
			}
			return r.conn, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-f.closed:
			return nil, errors.New("transport: fan-in closed")
		}
	}
}

// Close shuts down every backend listener.
func (f *FanInListener) Close() error {
	var first error
	f.once.Do(func() {
		close(f.closed)
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, ln := range f.listeners {
			if err := ln.Close(); err != nil && first == nil {
				first = err
			}
		}
	})
	return first
}

// FallbackDialer races every configured backend in parallel and
// returns the first connection that finishes handshake; the rest
// are cancelled and torn down. Once a winner is picked it is held
// for the lifetime of the session — no mid-flight transport
// switching, since that would interrupt long-lived flows
// (game sessions, video calls) for marginal latency wins.
//
// The name is preserved for source compatibility with earlier
// callers; semantically this is a race, not a sequential fallback.
// If every backend errors, the last error is wrapped and returned.
type FallbackDialer struct {
	logger  *slog.Logger
	options []dialerOption
}

type dialerOption struct {
	label  string
	addr   string
	dialer Dialer
}

// NewFallback constructs an empty fall-back dialer.
func NewFallback(logger *slog.Logger) *FallbackDialer {
	if logger == nil {
		logger = slog.Default()
	}
	return &FallbackDialer{logger: logger}
}

// Add registers a backend. The order in which backends are added is
// no longer significant — Dial races them all in parallel — but
// Add is kept as the construction API so the rest of the codebase
// (and any third-party wrappers) keep compiling unchanged.
func (f *FallbackDialer) Add(label, addr string, d Dialer) {
	f.options = append(f.options, dialerOption{label: label, addr: addr, dialer: d})
}

// dialResult carries the outcome of one racing dial attempt.
type dialResult struct {
	conn  Conn
	label string
	addr  string
	err   error
}

// Dial races every configured backend and returns the first that
// finishes handshake. Losing backends are cancelled via the shared
// context; any connection they produce after the race is closed by
// the drain goroutine.
func (f *FallbackDialer) Dial(ctx context.Context) (Conn, string, error) {
	if len(f.options) == 0 {
		return nil, "", errors.New("transport: no backends configured")
	}

	raceCtx, cancel := context.WithCancel(ctx)
	results := make(chan dialResult, len(f.options))

	for _, opt := range f.options {
		opt := opt // capture
		f.logger.Info("transport dial", "transport", opt.label, "addr", opt.addr)
		go func() {
			conn, err := opt.dialer.Dial(raceCtx, opt.addr)
			results <- dialResult{conn: conn, label: opt.label, addr: opt.addr, err: err}
		}()
	}

	var (
		winner   *dialResult
		lastErr  error
		finished int
		total    = len(f.options)
	)
	for finished < total {
		select {
		case r := <-results:
			finished++
			if r.err != nil {
				f.logger.Warn("transport dial failed",
					"transport", r.label, "addr", r.addr, "err", r.err)
				lastErr = r.err
				continue
			}
			if winner == nil {
				w := r
				winner = &w
				cancel() // signal the laggards to abort
				f.logger.Info("transport race won",
					"transport", winner.label, "addr", winner.addr)
				// keep draining the rest in the background so any
				// late-arriving connections get closed cleanly
				go drainAndClose(results, total-finished)
				return winner.conn, winner.label, nil
			}
		case <-ctx.Done():
			cancel()
			go drainAndClose(results, total-finished)
			return nil, "", ctx.Err()
		}
	}
	cancel() // unused; satisfies the linter
	return nil, "", fmt.Errorf("all transports failed: %w", lastErr)
}

// drainAndClose receives the remaining race results and closes any
// connection that arrives after the race is already settled.
func drainAndClose(ch <-chan dialResult, expected int) {
	for i := 0; i < expected; i++ {
		r := <-ch
		if r.conn != nil {
			_ = r.conn.Close()
		}
	}
}
